package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecipeOrderedMaintenanceRetainsRecoveryFactsAndIsolatesOwners(t *testing.T) {
	// Synthetic physical-state fixture; it does not assert Bitcoin validity.
	ctx := context.Background()
	root := t.TempDir()
	g := orderedGenerationTestOpen(t, root, "maintained-coins")
	if err := g.create(ctx); err != nil {
		t.Fatal(err)
	}
	b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(78200, 50))
	coinbase := utxoFixtureCoinbase(78201, 10)
	b1 := utxoFixtureBlock(1, b0.Hash, coinbase)
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(78202, 1), utxoFixtureSpend(78203, []inputView{{PrevTxID: coinbase.TxID}}, 9))
	b3 := utxoFixtureBlock(3, b2.Hash, utxoFixtureCoinbase(78204, 1))
	for _, block := range []blockView{b0, b1, b2} {
		if err := g.apply(ctx, block, orderedGenerationTestGuard); err != nil {
			t.Fatal(err)
		}
	}
	unused, err := storeBlockOutputFactsContext(ctx, g.profileRoot, b3)
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := storeBlockOutputFactsContext(ctx, root, b3)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, owner := range []string{root, g.profileRoot} {
		if err := filepath.WalkDir(outputFactDirectory(owner), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			return os.Chtimes(path, old, old)
		}); err != nil {
			t.Fatal(err)
		}
	}
	utxoWriterMu.Lock()
	utxoStateMu.Lock()
	err = g.maintain(ctx)
	utxoStateMu.Unlock()
	utxoWriterMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range unused {
		path, _ := outputFactPath(g.profileRoot, ref.Page)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("unowned private facts were retained", path, err)
		}
	}
	for _, ref := range unrelated {
		if _, err := readOutputFactPageContext(ctx, root, ref.Page); err != nil {
			t.Fatal("private maintenance touched unrelated native facts", err)
		}
	}
	if err = g.reconcile(ctx, 1, func(_ context.Context, height int64) (string, error) {
		return []blockView{b0, b1}[height].Hash, nil
	}, orderedGenerationTestGuard); err != nil {
		t.Fatal("maintenance lost retained undo", err)
	}
	s, err := g.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	trees := s.trees(ctx, s.head.Checkpoint)
	member, found, err := s.memberForOutpoint(trees, coinbase.TxID, 0)
	if err != nil || !found {
		t.Fatal("maintenance lost recovered membership", err)
	}
	coin, err := s.coin(ctx, trees, member)
	if err != nil || coin.ValueSats != 10 {
		t.Fatal("maintenance lost spent Coin recovery facts", coin, err)
	}
	if err = g.apply(ctx, b2, orderedGenerationTestGuard); err != nil {
		t.Fatal("maintenance lost next-transition dependencies", err)
	}
}

func TestRecipeOrderedMaintenanceCancellationAndMissingPinFailClosed(t *testing.T) {
	ctx := context.Background()
	g := orderedGenerationTestOpen(t, t.TempDir(), "gc-closed")
	if err := g.create(ctx); err != nil {
		t.Fatal(err)
	}
	blocks := recipeCoordinatorSource(t)
	for height := int64(0); height <= 1; height++ {
		if err := g.apply(ctx, blocks[height], orderedGenerationTestGuard); err != nil {
			t.Fatal(err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := g.maintain(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal("maintenance ignored cancellation", err)
	}
	s, _ := g.snapshot(ctx)
	var pin string
	if err := s.visitCurrentFactPages(ctx, func(hash string) error { pin = hash; return nil }); err != nil || pin == "" {
		t.Fatal("fixture has no authenticated live pin", err)
	}
	path, _ := outputFactPath(g.profileRoot, pin)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := g.maintain(ctx); err == nil {
		t.Fatal("missing owned fact did not stop reclamation")
	}
}
