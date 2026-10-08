package main

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Later blocks use synthetic already-verified receipts to isolate persistent
// storage scale; the transaction retained at height zero is real mainnet genesis.
// No test pretends those synthetic receipts constitute consensus validation.
func olderExplorerFixture064(t *testing.T, id string, count int) (*app, *indexStore, string) {
	t.Helper()
	a := prepareLiveTestApp063(t)
	genesis, err := a.fetchAndDecode("0")
	if err != nil {
		t.Fatal(err)
	}
	s, err := openIndexStore(a.dataDir, id)
	if err != nil {
		t.Fatal(err)
	}
	first := genesis.Transactions[0].TxID
	previous := ""
	for h := 0; h < count; h++ {
		b := genesis
		if id == "inscriptions" {
			b = bitmapTestBlock(int64(h), "", previous, "old retained content")
			if h != 0 {
				b.Transactions = b.Transactions[:1]
				b.TransactionCount = 1
			}
		}
		b.Height = int64(h)
		b.PreviousBlockHash = previous
		if h != 0 || id == "inscriptions" {
			header := make([]byte, 80)
			binary.LittleEndian.PutUint64(header[72:], uint64(h+1234))
			hash := hash256(header)
			b.Hash = reverseHex(hash[:])
			f, err := os.OpenFile(a.headersPath, os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.WriteAt(header, int64(h)*80)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			if id != "inscriptions" {
				b.Transactions = append([]transactionView(nil), b.Transactions...)
				b.Transactions[0].TxID = indexDigest(h)
			}
		}
		if err = s.appendBlock(b, 0, "ephemeral"); err != nil {
			t.Fatal(h, err)
		}
		previous = b.Hash
	}
	a.status.HeaderCount = int64(count)
	a.status.HeaderHeight = int64(count - 1)
	if id == "inscriptions" {
		var oldest indexBatch
		err = s.walk(func(b indexBatch) error { oldest = b; return nil })
		if err != nil {
			t.Fatal(err)
		}
		first = oldest.Inscriptions[0].ID
	}
	return a, s, first
}

func restartExplorerFixture064(t *testing.T, a *app) *app {
	t.Helper()
	r := &app{dataDir: a.dataDir, headersPath: a.headersPath, status: a.getStatus(), settings: a.settings, cacheIndex: newCacheIndex()}
	r.settings.NetworkDisabled = true
	r.settings.CoreDisabled = true
	r.settings.CoreMountDisabled = true
	r.settings.OrdEnabled = true
	r.network = newBitcoinNetwork(r)
	r.network.noBootstrap = true
	t.Cleanup(func() { r.network.stop() })
	return r
}

func TestIntegrationDurableOlderIDsAfterRestartAndEphemeralEviction(t *testing.T) {
	for _, id := range []string{"blocks", "tx-locator", "inscriptions"} {
		t.Run(id, func(t *testing.T) {
			if !releaseFeatureAvailable(id) {
				t.Skip("This public resolver requires an index intentionally locked in 0.6.6; Blocks persistence remains active.")
			}
			a, s, key := olderExplorerFixture064(t, id, indexExplorerMaxBatches+2)
			if err := os.Remove(filepath.Join(a.dataDir, "blocks", "raw", "0-"+genesisHashDisplay+".block")); err != nil {
				t.Fatal(err)
			}
			r := restartExplorerFixture064(t, a)
			if id == "inscriptions" {
				rec, err := r.resolveInscription(context.Background(), key, "")
				if err != nil || string(rec.Envelope.Body) != "old retained content" || rec.Height != 0 {
					t.Fatal(rec, err)
				}
			} else {
				rec, err := r.resolveTransactionViaOverlay(key)
				if err != nil || rec.Height != 0 || rec.BlockHash != genesisHashDisplay || rec.TxIndex != 0 || !rec.LocatorVerified {
					t.Fatal(rec, err)
				}
				if id == "blocks" {
					if !rec.TransactionVerified || rec.Transaction.TxID != key || rec.Transaction.OutputSats != 5000000000 {
						t.Fatal("stored fields were lost", rec)
					}
				} else {
					if rec.TransactionVerified || rec.ResolutionState != "located_bytes_unavailable" {
						t.Fatal("location became invented transaction evidence", rec)
					}
					if _, err = r.resolvePrevoutViaOverlay(key, 0); err == nil || !strings.Contains(err.Error(), "block source") {
						t.Fatal("location-only prevout invented output absence", err)
					}
				}
			}
			if len(r.cacheIndex.Tx) != 0 || len(r.cacheIndex.Spends) != 0 {
				t.Fatal("duplicated private index into global map")
			}
			if _, err := os.Stat(filepath.Join(a.dataDir, "graph", "public-v2")); !os.IsNotExist(err) {
				t.Fatal("private lookup touched public graph", err)
			}
			if s.head.Published {
				t.Fatal("private index published")
			}
		})
	}
}

func TestIntegrationLegacyOccurrenceBackfillPreservesOldPayloads(t *testing.T) {
	a, s, key := olderExplorerFixture064(t, "inscriptions", indexExplorerMaxBatches+2)
	headBefore, err := os.ReadFile(filepath.Join(s.dir, "head.json"))
	if err != nil {
		t.Fatal(err)
	}
	oldest := ""
	if err = s.walk(func(b indexBatch) error { oldest = b.Checkpoint.Commitment; return nil }); err != nil {
		t.Fatal(err)
	}
	payloadPath := filepath.Join(s.dir, "commits", oldest+".json")
	payloadBefore, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatal(err)
	}
	// Remove only this fixture's new acceleration to model an untouched .3 store.
	if err = os.RemoveAll(filepath.Join(s.dir, s.locatorKind())); err != nil {
		t.Fatal(err)
	}
	r := restartExplorerFixture064(t, a)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = r.indexedInscription(ctx, key); !errors.Is(err, context.Canceled) {
		t.Fatal("backfill ignored cancellation", err)
	}
	rec, found, err := r.indexedInscription(context.Background(), key)
	if err != nil || !found || string(rec.Envelope.Body) != "old retained content" {
		t.Fatal(rec, err)
	}
	headAfter, _ := os.ReadFile(filepath.Join(s.dir, "head.json"))
	payloadAfter, _ := os.ReadFile(payloadPath)
	if string(headBefore) != string(headAfter) || string(payloadBefore) != string(payloadAfter) {
		t.Fatal("backfill modified existing checkpoint/payload")
	}
	if _, err = os.Stat(s.locatorActivePath()); err != nil {
		t.Fatal("backfill did not publish durable membership", err)
	}
}

func TestIntegrationTransactionPositionIsNotAlwaysZero(t *testing.T) {
	a := prepareLiveTestApp063(t)
	raw, err := os.ReadFile(filepath.Join("docs", "bitmap-evidence", "genesis-block.raw"))
	if err != nil {
		t.Fatal(err)
	}
	hash := hash256(raw[:80])
	blockHash := reverseHex(hash[:])
	height := int64(792435)
	f, err := os.OpenFile(a.headersPath, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(raw[:80], height*80)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	a.status.HeaderHeight = height
	a.status.HeaderCount = height + 1
	if err = atomicWriteBytes(filepath.Join(a.dataDir, "indexes", "sources", blockHash+".block"), raw); err != nil {
		t.Fatal(err)
	}
	b, err := a.fetchBlockAtLocation(height, blockHash, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Transactions) < 2 {
		t.Fatal("fixture lacks nonzero transaction")
	}
	tx := b.Transactions[1]
	rec, err := a.verifyTxLocation(tx.TxID, txLocation{TxID: tx.TxID, Height: height, BlockHash: blockHash, TxIndex: 1}, "fixture")
	if err != nil || rec.TxIndex != 1 || rec.Transaction.Index != 1 {
		t.Fatal("verified locator position lost", rec, err)
	}
	coord, ok := parseBODCoordinate(tx.TxID + ".792435")
	if !ok {
		t.Fatal("coordinate")
	}
	result, err := a.resolveCoordinate(coord)
	if err != nil || result.Resolution.TxIndex != 1 {
		t.Fatal("coordinate position lost", result, err)
	}
	s, err := openIndexStore(a.dataDir, "blocks")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.appendBlock(b, height, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	rec, found, err := a.indexedTransaction(tx.TxID)
	if err != nil || !found || rec.TxIndex != 1 || !rec.TransactionVerified {
		t.Fatal("committed position lost", rec, found, err)
	}
}
