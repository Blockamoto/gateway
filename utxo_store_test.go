package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func utxoFixtureBlock(height int64, previous string, txs ...transactionView) blockView {
	b := satIndexTestBlock(height, previous, txs...)
	if height == 0 {
		b.Hash = genesisHashDisplay
	}
	return b
}
func utxoFixtureCoinbase(id uint64, values ...uint64) transactionView {
	tx := testCoinbase(testID(id), values...)
	for i := range tx.Outputs {
		tx.Outputs[i].ScriptPubKey = "51"
	}
	return tx
}
func utxoFixtureSpend(id uint64, inputs []inputView, values ...uint64) transactionView {
	tx := testTx(testID(id), inputs, values...)
	for i := range tx.Outputs {
		tx.Outputs[i].ScriptPubKey = "51"
	}
	return tx
}
func utxoApply(t *testing.T, s *utxoStore, b blockView) {
	t.Helper()
	if e := s.ApplyBlock(context.Background(), b); e != nil {
		t.Fatalf("apply height %d: %v", b.Height, e)
	}
}
func utxoRequireCoin(t *testing.T, s *utxoStore, position string, value uint64) utxoCoin {
	t.Helper()
	coin, found, e := s.LookupBPS(context.Background(), position)
	if e != nil || !found || coin.ValueSats != value {
		t.Fatalf("Coin %s: found=%t value=%d error=%v", position, found, coin.ValueSats, e)
	}
	return coin
}

func TestUTXOGenesisOrderedCoinsAndRawFreeUndo(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, e := openUTXOStoreContext(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ApplyBlock(ctx, utxoFixtureBlock(1, genesisHashDisplay, utxoFixtureCoinbase(100, 10))); e == nil {
		t.Fatal("non-genesis fresh state accepted")
	}
	b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(101, 50))
	utxoApply(t, s, b0)
	if _, found, e := s.LookupOutpoint(ctx, testID(101), 0); e != nil || found {
		t.Fatal("genesis coinbase became spendable", found, e)
	}
	cb := utxoFixtureCoinbase(102, 10, 20, 3, 4)
	cb.Outputs[2].ScriptPubKey = "6a"
	cb.Outputs[3].ScriptPubKey = strings.Repeat("51", 10001)
	b1 := utxoFixtureBlock(1, b0.Hash, cb)
	utxoApply(t, s, b1)
	prior := s.Head()
	if prior.LiveOutputs != 2 || prior.TotalValueSats != 30 || prior.ConsensusValidated {
		t.Fatal(prior)
	}
	coin := utxoRequireCoin(t, s, "0.0.1", 10)
	if !coin.Coinbase || coin.ScriptPubKey != "51" || coin.BlockHash != b1.Hash {
		t.Fatal(coin)
	}
	for _, vout := range []uint32{2, 3} {
		if _, found, e := s.LookupOutpoint(ctx, cb.TxID, vout); e != nil || found {
			t.Fatal("unspendable output retained", vout, found, e)
		}
	}
	a := utxoFixtureSpend(103, []inputView{{PrevTxID: cb.TxID}}, 6)
	b := utxoFixtureSpend(104, []inputView{{PrevTxID: a.TxID}}, 5)
	c := utxoFixtureSpend(105, []inputView{{PrevTxID: cb.TxID, PrevVout: 1}}, 19)
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(106, 10), a, b, c)
	utxoApply(t, s, b2)
	if got := s.Status(); got.LiveOutputCount != 3 || got.LiveValueSats != 34 || got.ProcessedHeight != 2 {
		t.Fatal(got)
	}
	if _, found, e := s.LookupBPS(ctx, "0.1.2"); e != nil || found {
		t.Fatal("same-block spent output remains live", found, e)
	}
	journal, e := s.readJournal(ctx, s.head.Checkpoint)
	if e != nil {
		t.Fatal(e)
	}
	for _, image := range journal.BeforeImages {
		if image.Tree == "live" && image.Key == utxoPositionKey(2, 1, 0) && len(image.Previous) != 0 {
			t.Fatal("born-and-spent output would be resurrected")
		}
	}
	// No body was retained by the engine; its only recovery dependencies are
	// immutable Coin facts, paged roots and ordered journal before-images.
	if _, e = os.Stat(filepath.Join(root, "blocks", "raw")); !os.IsNotExist(e) {
		t.Fatal("unexpected raw-body dependency", e)
	}
	if e = s.Reconcile(ctx, func(_ context.Context, h int64) (string, error) {
		if h == 2 {
			return testID(9002), nil
		}
		return b1.Hash, nil
	}); e != nil {
		t.Fatal(e)
	}
	if got := s.Head(); got.Height != 1 || got.LiveRoot != prior.LiveRoot || got.BridgeRoot != prior.BridgeRoot || got.TransactionRoot != prior.TransactionRoot {
		t.Fatal("rollback did not restore prior roots", got, prior)
	}
	utxoRequireCoin(t, s, "0.0.1", 10)
	utxoRequireCoin(t, s, "1.0.1", 20)
	for _, position := range []string{"0.0.2", "0.1.2", "0.2.2", "0.3.2"} {
		if _, found, e := s.LookupBPS(ctx, position); e != nil || found {
			t.Fatal("disconnected output resurrected", position, found, e)
		}
	}
	replacement := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(107, 2))
	replacement.Hash = testID(9002)
	utxoApply(t, s, replacement)
	restarted, e := openUTXOStoreContext(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	utxoRequireCoin(t, restarted, "0.0.1", 10)
	calls := 0
	if e = restarted.Reconcile(ctx, func(_ context.Context, h int64) (string, error) { calls++; return replacement.Hash, nil }); e != nil || calls != 1 {
		t.Fatal("ordinary reconciliation traversed ancestry", calls, e)
	}
}

func TestUTXOPagedStateRollbackAcrossLeafSplit(t *testing.T) {
	ctx := context.Background()
	s, e := openUTXOStoreContext(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(200, 50))
	utxoApply(t, s, b0)
	values := make([]uint64, 193)
	for i := range values {
		values[i] = 1
	}
	cb := utxoFixtureCoinbase(201, values...)
	b1 := utxoFixtureBlock(1, b0.Hash, cb)
	utxoApply(t, s, b1)
	prior := s.Head()
	inputs := make([]inputView, len(values))
	for i := range inputs {
		inputs[i] = inputView{PrevTxID: cb.TxID, PrevVout: uint32(i)}
	}
	cb2 := utxoFixtureCoinbase(202, 0)
	cb2.Outputs[0].ScriptPubKey = "6a"
	b2 := utxoFixtureBlock(2, b1.Hash, cb2, utxoFixtureSpend(203, inputs, 180))
	utxoApply(t, s, b2)
	if got := s.Status(); got.LiveOutputCount != 1 || got.LiveValueSats != 180 {
		t.Fatal(got)
	}
	if e = s.Reconcile(ctx, func(_ context.Context, h int64) (string, error) {
		if h == 2 {
			return testID(10002), nil
		}
		return b1.Hash, nil
	}); e != nil {
		t.Fatal(e)
	}
	if got := s.Head(); got.LiveOutputs != 193 || got.LiveRoot != prior.LiveRoot {
		t.Fatal("undo of a split/shrunk tree changed state", got)
	}
	for _, index := range []int{0, 64, 128, 192} {
		utxoRequireCoin(t, s, outputCoordinate(index, 0, 1), 1)
	}
	if e = filepath.WalkDir(s.pages(), func(path string, d os.DirEntry, e error) error {
		if e != nil || d.IsDir() || !strings.HasSuffix(path, ".json") || len(d.Name()) != 69 {
			return e
		}
		var p ordinalPage
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if e = json.Unmarshal(raw, &p); e != nil {
			return e
		}
		if len(p.Entries) > ordinalPageEntries {
			return fmt.Errorf("unbounded Coin page")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

func TestUTXOInterruptedPublicationAndResume(t *testing.T) {
	for _, stage := range []string{"facts_written", "pages_written", "journal_written", "commit_written", "before_head", "after_head"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			s, e := openUTXOStoreContext(ctx, root)
			if e != nil {
				t.Fatal(e)
			}
			b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(300, 50))
			utxoApply(t, s, b0)
			b1 := utxoFixtureBlock(1, b0.Hash, utxoFixtureCoinbase(301, 10))
			utxoApply(t, s, b1)
			before, e := os.ReadFile(filepath.Join(s.dir, "head.json"))
			if e != nil {
				t.Fatal(e)
			}
			b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(302, 1), utxoFixtureSpend(303, []inputView{{PrevTxID: testID(301)}}, 9))
			interrupted := errors.New("injected interruption")
			s.commitHook = func(current string) error {
				if current == stage {
					return interrupted
				}
				return nil
			}
			if e = s.ApplyBlock(ctx, b2); !errors.Is(e, interrupted) {
				t.Fatal(e)
			}
			reopened, e := openUTXOStoreContext(ctx, root)
			if e != nil {
				t.Fatal(e)
			}
			if stage == "after_head" {
				if reopened.Head().Height != 2 {
					t.Fatal("durably published head lost")
				}
				utxoRequireCoin(t, reopened, "0.1.2", 9)
			} else {
				after, e := os.ReadFile(filepath.Join(s.dir, "head.json"))
				if e != nil || !bytes.Equal(before, after) {
					t.Fatal("interruption advanced head", e)
				}
				utxoRequireCoin(t, reopened, "0.0.1", 10)
				if _, found, e := reopened.LookupBPS(ctx, "0.1.2"); e != nil || found {
					t.Fatal("unpublished Coin became visible", found, e)
				}
				utxoApply(t, reopened, b2)
				utxoRequireCoin(t, reopened, "0.1.2", 9)
			}
		})
	}
}

func TestUTXOCancelAndInvalidTransitionPreserveHead(t *testing.T) {
	ctx := context.Background()
	s, e := openUTXOStoreContext(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(400, 50))
	utxoApply(t, s, b0)
	b1 := utxoFixtureBlock(1, b0.Hash, utxoFixtureCoinbase(401, 10))
	utxoApply(t, s, b1)
	prior := s.Head().Commitment
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(402, 1))
	if e = s.ApplyBlock(cancelled, b2); e == nil || s.Head().Commitment != prior {
		t.Fatal("cancelled transition published", e)
	}
	for _, tx := range []transactionView{
		utxoFixtureSpend(403, []inputView{{PrevTxID: testID(999)}}, 1),
		utxoFixtureSpend(404, []inputView{{PrevTxID: testID(401)}, {PrevTxID: testID(401)}}, 1),
		utxoFixtureSpend(405, []inputView{{PrevTxID: testID(401)}}, 11),
	} {
		invalid := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(402, 1), tx)
		if e = s.ApplyBlock(ctx, invalid); e == nil || s.Head().Commitment != prior {
			t.Fatal("invalid transition published", e)
		}
		utxoRequireCoin(t, s, "0.0.1", 10)
	}
	if e = s.Reconcile(ctx, func(context.Context, int64) (string, error) { return "", errors.New("header unavailable") }); e == nil || s.Head().Commitment != prior {
		t.Fatal("missing header was treated as a reorg", e)
	}
}

func TestUTXOFixedRecoveryWindowPinsAncientCoinsAndPrunesVersions(t *testing.T) {
	ctx := context.Background()
	s, e := openUTXOStoreContext(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(500, 50))
	utxoApply(t, s, b0)
	values := make([]uint64, outputFactPageRows+1)
	values[0], values[len(values)-1] = 7, 9
	cb := utxoFixtureCoinbase(501, values...)
	for i := 1; i < len(values)-1; i++ {
		cb.Outputs[i].ScriptPubKey = "6a"
	}
	b1 := utxoFixtureBlock(1, b0.Hash, cb)
	utxoApply(t, s, b1)
	expiredLiveRoot := s.Head().LiveRoot
	spentPage := utxoRequireCoin(t, s, "0.0.1", 7).FactRef.Page
	livePage := utxoRequireCoin(t, s, outputCoordinate(len(values)-1, 0, 1), 9).FactRef.Page
	if spentPage == livePage {
		t.Fatal("fixture must place ancient Coins on distinct pages")
	}
	hashes := []string{b0.Hash, b1.Hash}
	previous := b1.Hash
	var expiredCommit string
	for h := int64(2); h <= 300; h++ {
		coinbase := utxoFixtureCoinbase(uint64(600+h), 0)
		coinbase.Outputs[0].ScriptPubKey = "6a"
		txs := []transactionView{coinbase}
		if h == 2 {
			txs = append(txs, utxoFixtureSpend(502, []inputView{{PrevTxID: cb.TxID}}, 6))
		}
		block := utxoFixtureBlock(h, previous, txs...)
		utxoApply(t, s, block)
		previous = block.Hash
		hashes = append(hashes, block.Hash)
		if h == 2 {
			expiredCommit = s.Head().Commitment
		}
		if h == 289 {
			pins := map[string]bool{}
			if e = s.VisitFactPages(ctx, func(hash string) error { pins[hash] = true; return nil }); e != nil {
				t.Fatal(e)
			}
			if !pins[spentPage] || !pins[livePage] {
				t.Fatal("old spent/live facts not pinned through recovery window")
			}
		}
	}
	if got := s.Status(); got.RecoveryWindow != 288 || got.RecoveryTransitions != 288 || got.RecoveryBoundaryHeight != 12 {
		t.Fatal(got)
	}
	pins := map[string]bool{}
	if e = s.VisitFactPages(ctx, func(hash string) error { pins[hash] = true; return nil }); e != nil {
		t.Fatal(e)
	}
	if pins[spentPage] || !pins[livePage] {
		t.Fatal("expired spend pin retained or ancient live Coin lost", pins[spentPage], pins[livePage])
	}
	for i := 0; i < 500; i++ {
		if e = s.CollectPages(ctx); e != nil {
			t.Fatal(e)
		}
		if !ordinalCollectionPending(s.pages()) {
			break
		}
		if i == 499 {
			t.Fatal("bounded cleanup did not complete")
		}
	}
	if _, e = os.Stat(s.recordPath("commits", expiredCommit)); !os.IsNotExist(e) {
		t.Fatal("expired state version survived collection", e)
	}
	if _, e = os.Stat(newOrdinalTree(s.pages(), "").pagePath(expiredLiveRoot)); !os.IsNotExist(e) {
		t.Fatal("unreferenced old Coin page survived collection", e)
	}
	commits, e := os.ReadDir(filepath.Join(s.dir, "commits"))
	if e != nil || len(commits) != 289 {
		t.Fatal("recovery checkpoint retention", len(commits), e)
	}
	journals, e := os.ReadDir(filepath.Join(s.dir, "journals"))
	if e != nil || len(journals) != 288 {
		t.Fatal("recovery journal retention", len(journals), e)
	}
	prior := s.Head().Commitment
	if e = s.Reconcile(ctx, func(_ context.Context, h int64) (string, error) {
		if h <= 11 {
			return hashes[h], nil
		}
		return testID(uint64(100000 + h)), nil
	}); !errors.Is(e, ErrUTXORebuildRequired) || s.Head().Commitment != prior {
		t.Fatal("deeper-than-retained reorg changed state", e)
	}
	if e = s.Reconcile(ctx, func(_ context.Context, h int64) (string, error) {
		if h <= 12 {
			return hashes[h], nil
		}
		return testID(uint64(100000 + h)), nil
	}); e != nil {
		t.Fatal("exactly288 transitions did not recover", e)
	}
	if got := s.Status(); got.ProcessedHeight != 12 || got.RecoveryTransitions != 0 || got.RecoveryBoundaryHeight != 12 {
		t.Fatal("rewind extended retention policy", got)
	}
	utxoRequireCoin(t, s, outputCoordinate(len(values)-1, 0, 1), 9)
}

func TestUTXOBIP30RestoresDisplacedIncarnation(t *testing.T) {
	ctx := context.Background()
	s, e := openUTXOStoreContext(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(800, 50))
	utxoApply(t, s, b0)
	old := utxoFixtureCoinbase(801, 7, 9)
	b1 := utxoFixtureBlock(1, b0.Hash, old)
	utxoApply(t, s, b1)
	// A bounded test checkpoint substitutes for replaying91841 fixture blocks.
	// Production ApplyBlock exposes no import/height-jump path.
	parent := s.head.Checkpoint
	parent.Height = 91841
	parent.BlockHash = testID(918410)
	parent.PreviousBlockHash = testID(918400)
	parent.RecoveryBoundaryHeight, parent.RecoveryBoundaryHash = parent.Height, parent.BlockHash
	parent.Commitment = utxoCheckpointDigest(parent)
	if e = s.writeRecord("commits", parent.Commitment, parent); e != nil {
		t.Fatal(e)
	}
	if e = s.publish(parent, parent.Height, parent.BlockHash); e != nil {
		t.Fatal(e)
	}
	replacement := utxoFixtureBlock(91842, parent.BlockHash, old)
	if e = s.ApplyBlock(ctx, replacement); e == nil {
		t.Fatal("height-only BIP30 exception accepted")
	}
	replacement.Hash = "00000000000a4d0a398161ffc163c503763b1f4360639393e0e4c8e300e0caec"
	utxoApply(t, s, replacement)
	coin, found, e := s.LookupOutpoint(ctx, old.TxID, 1)
	if e != nil || !found || coin.Height != 91842 || coin.ValueSats != 9 {
		t.Fatal("new incarnation not selected", coin, found, e)
	}
	if _, found, e = s.LookupBPS(ctx, "1.0.1"); e != nil || found {
		t.Fatal("displaced incarnation stayed live", found, e)
	}
	if e = s.Reconcile(ctx, func(_ context.Context, h int64) (string, error) {
		if h == 91842 {
			return testID(999842), nil
		}
		return parent.BlockHash, nil
	}); e != nil {
		t.Fatal(e)
	}
	coin, found, e = s.LookupOutpoint(ctx, old.TxID, 1)
	if e != nil || !found || coin.Height != 1 || coin.ValueSats != 9 {
		t.Fatal("old incarnation not restored", coin, found, e)
	}
	utxoRequireCoin(t, s, "0.0.1", 7)
}

func TestUTXOCorruptHeadFactAndUndoFailClosed(t *testing.T) {
	for _, damage := range []string{"head", "root", "facts", "journal"} {
		t.Run(damage, func(t *testing.T) {
			ctx := context.Background()
			s, e := openUTXOStoreContext(ctx, t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(900, 50))
			utxoApply(t, s, b0)
			b1 := utxoFixtureBlock(1, b0.Hash, utxoFixtureCoinbase(901, 10))
			utxoApply(t, s, b1)
			b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(902, 1))
			utxoApply(t, s, b2)
			prior := s.Head().Commitment
			switch damage {
			case "head":
				path := filepath.Join(s.dir, "head.json")
				raw, _ := os.ReadFile(path)
				raw = bytes.Replace(raw, []byte(`"total_value_sats": 11`), []byte(`"total_value_sats": 12`), 1)
				if e = os.WriteFile(path, raw, 0600); e != nil {
					t.Fatal(e)
				}
				if _, e = utxoStoreHead(s.root); !errors.Is(e, ErrUTXOStateCorrupt) {
					t.Fatal("corrupt head accepted", e)
				}
			case "facts":
				coin := utxoRequireCoin(t, s, "0.0.1", 10)
				path, e := outputFactPath(s.root, coin.FactRef.Page)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.Remove(path); e != nil {
					t.Fatal(e)
				}
				if _, _, e = s.LookupBPS(ctx, coin.Position); !errors.Is(e, ErrUTXOStateCorrupt) {
					t.Fatal("missing facts accepted", e)
				}
			case "root":
				if e = os.Remove(newOrdinalTree(s.pages(), "").pagePath(s.head.Checkpoint.LiveRoot)); e != nil {
					t.Fatal(e)
				}
				if _, e = utxoStoreHead(s.root); !errors.Is(e, ErrUTXOStateCorrupt) {
					t.Fatal("missing top-level state page accepted by status", e)
				}
			case "journal":
				if e = os.WriteFile(s.recordPath("journals", s.head.Checkpoint.Journal), []byte(`{}`), 0600); e != nil {
					t.Fatal(e)
				}
				if e = s.Reconcile(ctx, func(_ context.Context, h int64) (string, error) {
					if h == 2 {
						return testID(90002), nil
					}
					return b1.Hash, nil
				}); !errors.Is(e, ErrUTXOStateCorrupt) || s.Head().Commitment != prior {
					t.Fatal("corrupt undo advanced state", e)
				}
			}
		})
	}
}

func TestUTXOMissingRecoveryCoinFactsPreservesHead(t *testing.T) {
	ctx := context.Background()
	s, e := openUTXOStoreContext(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(1100, 50))
	utxoApply(t, s, b0)
	b1 := utxoFixtureBlock(1, b0.Hash, utxoFixtureCoinbase(1101, 10))
	utxoApply(t, s, b1)
	old := utxoRequireCoin(t, s, "0.0.1", 10)
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(1102, 1), utxoFixtureSpend(1103, []inputView{{PrevTxID: old.TxID}}, 9))
	utxoApply(t, s, b2)
	prior := s.Head().Commitment
	path, e := outputFactPath(s.root, old.FactRef.Page)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e = s.Reconcile(ctx, func(_ context.Context, h int64) (string, error) {
		if h == 2 {
			return testID(111111), nil
		}
		return b1.Hash, nil
	}); !errors.Is(e, ErrUTXOStateCorrupt) || s.Head().Commitment != prior {
		t.Fatal("undo published a Coin with missing full facts", e)
	}
}
