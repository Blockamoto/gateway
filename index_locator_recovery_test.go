package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nextLocatorBlock064(t *testing.T, a *app, s *indexStore) blockView {
	t.Helper()
	v, err := a.fetchAndDecode("0")
	if err != nil {
		t.Fatal(err)
	}
	v.Height = s.checkpoint.Height + 1
	v.PreviousBlockHash = s.checkpoint.BlockHash
	header := make([]byte, 80)
	header[0] = 71
	hash := hash256(header)
	v.Hash = reverseHex(hash[:])
	f, err := os.OpenFile(a.headersPath, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(header, v.Height*80)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	a.status.HeaderCount = v.Height + 1
	a.status.HeaderHeight = v.Height
	v.Transactions = append([]transactionView(nil), v.Transactions...)
	v.Transactions[0].TxID = strings.Repeat("b", 64)
	return v
}

func TestIndexLocatorPartialFirstRecordWithExistingHeadRecovers(t *testing.T) {
	for _, acknowledged := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing_receipt", true: "empty_receipt"}[acknowledged], func(t *testing.T) {
			a, s, oldID := olderExplorerFixture064(t, "blocks", 1)
			oldHead, err := os.ReadFile(filepath.Join(s.dir, "head.json"))
			if err != nil {
				t.Fatal(err)
			}
			next := nextLocatorBlock064(t, a, s)
			path, err := graphShardPath(s.dir, s.locatorKind(), next.Transactions[0].TxID)
			if err != nil {
				t.Fatal(err)
			}
			oldPath, _ := graphShardPath(s.dir, s.locatorKind(), oldID)
			if path == oldPath {
				t.Fatal("fixture did not introduce a new shard")
			}
			if err = s.makeLocatorDirectories(filepath.Dir(path)); err != nil {
				t.Fatal(err)
			}
			if acknowledged {
				indexLocatorMu.Lock()
				err = s.appendBitcoinLocatorRecords(path, nil)
				indexLocatorMu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			raw, _ := encodeIndexLocator(indexLocatorRecord{TxID: next.Transactions[0].TxID, Commit: strings.Repeat("c", 64), Height: 1, TxIndex: 0})
			if err = os.WriteFile(path, raw[:17], 0600); err != nil {
				t.Fatal(err)
			}
			// The old active table is ready, which formerly caused ensure to skip
			// a partial first shard forever. Append itself must repair this state.
			if err = s.ensureIndexLocators(context.Background()); err != nil {
				t.Fatal(err)
			}
			if current, _ := os.ReadFile(filepath.Join(s.dir, "head.json")); string(current) != string(oldHead) {
				t.Fatal("repair admission changed head")
			}
			if err = s.appendBlock(next, 0, "ephemeral"); err != nil {
				t.Fatal("first-record retry remained blocked", err)
			}
			records, err := s.bitcoinLocatorRecords(context.Background(), next.Transactions[0].TxID)
			if err != nil || len(records) != 1 || records[0].Commit != s.head.Commitment {
				t.Fatal("orphan bytes became a committed row", records, err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Size() != indexLocatorRecordBytes {
				t.Fatal("partial tail survived", info, err)
			}
			r, found, err := a.indexedTransaction(oldID)
			if err != nil || !found || r.Height != 0 || r.Transaction.OutputSats != 5000000000 {
				t.Fatal("older approved coverage was lost", r, found, err)
			}
		})
	}
}

func TestIndexLocatorReceiptDirectoryFailureKeepsPriorHead(t *testing.T) {
	a, s, oldID := olderExplorerFixture064(t, "blocks", 1)
	before, err := os.ReadFile(filepath.Join(s.dir, "head.json"))
	if err != nil {
		t.Fatal(err)
	}
	next := nextLocatorBlock064(t, a, s)
	path, _ := graphShardPath(s.dir, s.locatorKind(), next.Transactions[0].TxID)
	failure := errors.New("injected receipt-directory flush failure")
	s.locatorSyncDirectory = func(dir string) error {
		if dir == filepath.Dir(path) {
			if receipt, err := readIndexLocatorReceipt(path); err == nil && receipt.Bytes > 0 {
				return failure
			}
		}
		return nil
	}
	if err = s.appendBlock(next, 0, "ephemeral"); !errors.Is(err, failure) {
		t.Fatal("receipt flush failure was swallowed", err)
	}
	after, _ := os.ReadFile(filepath.Join(s.dir, "head.json"))
	if string(before) != string(after) {
		t.Fatal("failed receipt publication advanced head")
	}
	if r, found, err := a.indexedTransaction(oldID); err != nil || !found || r.Height != 0 {
		t.Fatal("old head coverage unavailable after failure", r, found, err)
	}
	if _, found, err := a.indexedTransaction(next.Transactions[0].TxID); err != nil || found {
		t.Fatal("unpublished pointer became visible", found, err)
	}
	s.locatorSyncDirectory = nil
	if err = s.appendBlock(next, 0, "ephemeral"); err != nil {
		t.Fatal("receipt failure could not be retried", err)
	}
}

func TestIndexLocatorParentFlushFailureIsRetried(t *testing.T) {
	a := prepareLiveTestApp063(t)
	v, err := a.fetchAndDecode("0")
	if err != nil {
		t.Fatal(err)
	}
	s, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil {
		t.Fatal(err)
	}
	indexes := filepath.Dir(s.dir)
	failure := errors.New("injected parent-directory flush failure")
	attempts := 0
	s.locatorSyncDirectory = func(dir string) error {
		if dir == indexes {
			attempts++
			if attempts == 1 {
				return failure
			}
		}
		return nil
	}
	if err = s.appendBlock(v, 0, "ephemeral"); !errors.Is(err, failure) {
		t.Fatal("parent flush failure swallowed", err)
	}
	if s.checkpoint != nil {
		t.Fatal("failed hierarchy publication advanced checkpoint")
	}
	if _, err = os.Stat(filepath.Join(s.dir, "head.json")); !os.IsNotExist(err) {
		t.Fatal("failed hierarchy published head", err)
	}
	if err = s.appendBlock(v, 0, "ephemeral"); err != nil {
		t.Fatal("retry failed", err)
	}
	if attempts != 2 || s.checkpoint == nil {
		t.Fatal("existing unacknowledged parent was not flushed on retry", attempts)
	}
	if !s.locatorDirectoriesSynced[a.dataDir] {
		t.Fatal("enclosing profile entry chain was not acknowledged")
	}
}

func TestIndexLocatorMissingReceiptRebuildKeepsOriginalOnValidationFailure(t *testing.T) {
	_, s, key := olderExplorerFixture064(t, "blocks", 1)
	path, _ := graphShardPath(s.dir, s.locatorKind(), key)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path + ".receipt.json"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.dir, "commits", s.head.Commitment+".json"), []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	indexLocatorMu.Lock()
	err = s.rebuildIndexLocatorShard(context.Background(), path)
	indexLocatorMu.Unlock()
	if err == nil {
		t.Fatal("invalid committed history authorized repair")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("failed reconstruction removed approved acceleration")
	}
}

func TestIndexLocatorExistingEmptyShardWithoutReceiptRestoresOlderRows(t *testing.T) {
	for _, entry := range []string{"lookup", "append"} {
		t.Run(entry, func(t *testing.T) {
			_, s, key := olderExplorerFixture064(t, "blocks", 1)
			prior := s.head.Commitment
			path, _ := graphShardPath(s.dir, s.locatorKind(), key)
			if err := os.Truncate(path, 0); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path + ".receipt.json"); err != nil {
				t.Fatal(err)
			}
			if entry == "append" {
				newKey := key[:4] + strings.Repeat("c", 60)
				raw, _ := encodeIndexLocator(indexLocatorRecord{TxID: newKey, Commit: strings.Repeat("d", 64), Height: 1})
				indexLocatorMu.Lock()
				err := s.appendBitcoinLocatorRecords(path, [][]byte{raw})
				indexLocatorMu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			records, err := s.bitcoinLocatorRecords(context.Background(), key)
			if err != nil || len(records) != 1 || records[0].Commit != prior || s.head.Commitment != prior {
				t.Fatal("empty receipt-less shard lost committed rows", records, err)
			}
		})
	}
}

func TestIndexLocatorStaleRepairCannotDropNewCommittedRows(t *testing.T) {
	for _, kind := range []string{"shard", "active_table"} {
		t.Run(kind, func(t *testing.T) {
			a, s, _ := olderExplorerFixture064(t, "blocks", 1)
			stale, err := indexStoreHead(a.dataDir, "blocks")
			if err != nil {
				t.Fatal(err)
			}
			next := nextLocatorBlock064(t, a, s)
			if err = s.appendBlock(next, 0, "ephemeral"); err != nil {
				t.Fatal(err)
			}
			path, _ := graphShardPath(s.dir, s.locatorKind(), next.Transactions[0].TxID)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "shard" {
				if err = os.Remove(path + ".receipt.json"); err != nil {
					t.Fatal(err)
				}
				_, err = stale.bitcoinLocatorRecords(context.Background(), next.Transactions[0].TxID)
			} else {
				if err = os.Remove(s.locatorActivePath() + ".receipt.json"); err != nil {
					t.Fatal(err)
				}
				err = stale.ensureIndexLocators(context.Background())
			}
			if err == nil || !strings.Contains(err.Error(), "head changed") {
				t.Fatal("stale snapshot authorized repair", err)
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("stale repair dropped newer rows")
			}
			current, err := indexStoreHead(a.dataDir, "blocks")
			if err != nil {
				t.Fatal(err)
			}
			if current.head.Commitment != s.head.Commitment {
				t.Fatal("stale repair changed real head")
			}
			if err = current.ensureIndexLocators(context.Background()); err != nil {
				t.Fatal("fresh table repair failed", err)
			}
			records, err := current.bitcoinLocatorRecords(context.Background(), next.Transactions[0].TxID)
			if err != nil || len(records) != 1 || records[0].Commit != current.head.Commitment {
				t.Fatal("fresh retry did not preserve new coverage", records, err)
			}
		})
	}
}
