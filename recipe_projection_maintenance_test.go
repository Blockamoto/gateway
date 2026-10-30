package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func projectionTestAgeFacts(t *testing.T, root string, page string) {
	t.Helper()
	path, err := outputFactPath(root, page)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err = os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func TestRecipeProjectionMaintenanceRetainsHistoryAndIndependentPages(t *testing.T) {
	ctx := context.Background()
	u, b0, b1 := address076Source(t, []uint64{7}, []string{address076P2PKH})
	a := &app{dataDir: u.root}
	g := projectionTestGeneration(t, u.root, "maint-addresses", false)
	hashes := map[int64]string{0: b0.Hash, 1: b1.Hash}
	canonical := func(height int64) (string, error) { return hashes[height], nil }
	apply := func(height int64) {
		t.Helper()
		if err := g.applySnapshot(ctx, a, height, "", canonical, projectionGenerationFixtureGuard, nil); err != nil {
			t.Fatal(err)
		}
	}
	apply(1)
	_, first := projectionTestLatest(t, g)
	sourcePins := u.trees(ctx, first.Source)["pins"]
	childPins := newOrdinalTree(addressStatePages(g.profileRoot, first.PageGeneration), first.Source.PagePinRoot).withContext(ctx)
	sourceInfo, err := os.Stat(sourcePins.pagePath(first.Source.PagePinRoot))
	if err != nil {
		t.Fatal(err)
	}
	childInfo, err := os.Stat(childPins.pagePath(first.Source.PagePinRoot))
	if err != nil || os.SameFile(sourceInfo, childInfo) {
		t.Fatal("independent GC owners share mutable page timestamps", err)
	}
	oldPage := ""
	if err = sourcePins.walk(func(kv ordinalKV) error { oldPage = kv.Key; return nil }); err != nil {
		t.Fatal(err)
	}
	spend := utxoFixtureSpend(89001, []inputView{{PrevTxID: b1.Transactions[0].TxID}}, 6)
	spend.Outputs[0].ScriptPubKey = address076P2SH
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(89002, 1), spend)
	hashes[2] = b2.Hash
	utxoApply(t, u, b2)
	apply(2)
	_, second := projectionTestLatest(t, g)
	projectionTestAgeFacts(t, g.profileRoot, oldPage)
	orphan := second
	orphan.DestinationVersion = 0
	orphan.Commitment = addressStateDigest(orphan)
	point, err := g.writeSnapshot(ctx, orphan, indexDigest(u.root))
	if err != nil {
		t.Fatal(err)
	}
	if err = g.maintain(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = readOutputFactPageContext(ctx, g.profileRoot, oldPage); err != nil {
		t.Fatal("historical member facts expired", err)
	}
	if _, err = os.Stat(g.path("snapshots/" + point.Digest + ".json")); !os.IsNotExist(err) {
		t.Fatal("unpublished snapshot metadata was retained", err)
	}
	if _, err = childPins.read(first.Source.PagePinRoot); err != nil {
		t.Fatal("historical pin pages expired", err)
	}
	newPage := ""
	if err = u.trees(ctx, second.Source)["pins"].walk(func(kv ordinalKV) error { newPage = kv.Key; return nil }); err != nil {
		t.Fatal(err)
	}
	projectionTestAgeFacts(t, g.profileRoot, newPage)
	if err = u.ReconcileSelected(ctx, 1, func(_ context.Context, h int64) (string, error) { return hashes[h], nil }); err != nil {
		t.Fatal(err)
	}
	apply(1)
	if err = g.maintain(ctx); err != nil {
		t.Fatal(err)
	}
	path, _ := outputFactPath(g.profileRoot, newPage)
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("unreachable branch facts did not expire", err)
	}
	sourcePath, _ := outputFactPath(u.root, newPage)
	if _, err = os.Stat(sourcePath); err != nil {
		t.Fatal("child reclamation touched parent names", err)
	}
}

func TestRecipeProjectionMaintenanceProtectsPartialTraversal(t *testing.T) {
	ctx := context.Background()
	values, scripts := make([]uint64, 640), make([]string, 640)
	for i := range values {
		values[i], scripts[i] = 1, "51"
	}
	u, b0, b1 := address076Source(t, values, scripts)
	a := &app{dataDir: u.root}
	g := projectionTestGeneration(t, u.root, "partial-addresses", false)
	canonical := func(h int64) (string, error) {
		if h == 0 {
			return b0.Hash, nil
		}
		return b1.Hash, nil
	}
	work, cancel := context.WithCancel(ctx)
	err := g.applySnapshot(work, a, 1, "", canonical, projectionGenerationFixtureGuard, func(p addressStateBuildProgress) {
		if p.OutputsChecked == 512 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = u.trees(ctx, u.head.Checkpoint)["pins"].walk(func(kv ordinalKV) error {
		path, _ := outputFactPath(g.profileRoot, kv.Key)
		if _, err := os.Stat(path); err == nil {
			projectionTestAgeFacts(t, g.profileRoot, kv.Key)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = g.maintain(ctx); err != nil {
		t.Fatal(err)
	}
	// Excluded partial rows have only aggregate counters. Resume must restore
	// complete owned fact pins before publishing, including already checked rows.
	if err = g.applySnapshot(ctx, a, 1, "", canonical, projectionGenerationFixtureGuard, nil); err != nil {
		t.Fatal(err)
	}
	_, cp := projectionTestLatest(t, g)
	if cp.OutputCount != 0 || cp.ExcludedOutputCount != 640 {
		t.Fatal(cp)
	}
	if err = g.maintain(ctx); err != nil {
		t.Fatal("resumed complete fact ownership missing", err)
	}
}

func TestRecipeProjectionMaintenanceCorruptionDefersAllFactSweep(t *testing.T) {
	ctx := context.Background()
	u, _, b1 := address076Source(t, []uint64{7}, []string{address076P2PKH})
	a := &app{dataDir: u.root}
	g := projectionTestGeneration(t, u.root, "corrupt-maint-addresses", false)
	if err := g.applySnapshot(ctx, a, 1, "", func(int64) (string, error) { return b1.Hash, nil }, projectionGenerationFixtureGuard, nil); err != nil {
		t.Fatal(err)
	}
	_, cp := projectionTestLatest(t, g)
	garbage := utxoFixtureBlock(90, "", utxoFixtureCoinbase(89003, 9))
	refs, err := storeBlockOutputFactsContext(ctx, g.profileRoot, garbage)
	if err != nil {
		t.Fatal(err)
	}
	var orphan outputFactRef
	for _, ref := range refs {
		orphan = ref
	}
	projectionTestAgeFacts(t, g.profileRoot, orphan.Page)
	pins := newOrdinalTree(addressStatePages(g.profileRoot, cp.PageGeneration), cp.Source.PagePinRoot)
	if err = os.WriteFile(pins.pagePath(cp.Source.PagePinRoot), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = g.maintain(ctx); err == nil {
		t.Fatal("damaged retained pins allowed collection")
	}
	if _, err = readOutputFactPageContext(ctx, g.profileRoot, orphan.Page); err != nil {
		t.Fatal("failed collection removed bytes", err)
	}
	if _, err = u.trees(ctx, cp.Source)["pins"].read(cp.Source.PagePinRoot); err != nil {
		t.Fatal("child damage changed parent COW pages", err)
	}
}
