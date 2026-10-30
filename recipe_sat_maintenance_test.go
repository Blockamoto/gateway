package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecipeSatMaintenancePinsAndCorruptionDeferral(t *testing.T) {
	ctx := context.Background()
	g, err := openRecipeSatGeneration(t.TempDir(), recipeSatTestProgram(t, "maintained-sats", true))
	if err != nil {
		t.Fatal(err)
	}
	if err = g.create(ctx); err != nil {
		t.Fatal(err)
	}
	blocks := recipeCoordinatorSource(t)
	for h := int64(0); h <= 1; h++ {
		if err = g.apply(ctx, blocks[h], orderedGenerationTestGuard); err != nil {
			t.Fatal(err)
		}
	}
	values := make([]uint64, 65)
	for i := 0; i < 64; i++ {
		values[i] = 1
	}
	values[64] = subsidyAtHeight(2) - 64
	b := utxoFixtureBlock(2, blocks[1].Hash, utxoFixtureCoinbase(741222, values...))
	if err = g.apply(ctx, b, orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	s, _, err := g.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := s.readBatch(s.head.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	orphanBlock := utxoFixtureBlock(99, testID(741223), utxoFixtureCoinbase(741224, 1))
	orphanRefs, err := storeBlockOutputFactsContext(ctx, g.profileRoot, orphanBlock)
	if err != nil {
		t.Fatal(err)
	}
	orphanRef := orphanRefs["0.0.99"]
	orphanPath, _ := outputFactPath(g.profileRoot, orphanRef.Page)
	old := time.Now().Add(-2 * time.Hour)
	if err = filepath.Walk(g.profileRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return os.Chtimes(path, old, old)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	assertOrphan := func() {
		t.Helper()
		if _, err := os.Stat(orphanPath); err != nil {
			t.Fatal("corruption allowed fact deletion", err)
		}
	}
	// Required fact damage defers cleanup before any orphan can be removed.
	var required string
	if err = newOrdinalTree(s.ordinalPages(), batch.Sats.FactPinRoot).walk(func(kv ordinalKV) error {
		if required == "" {
			required = kv.Key
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	path, _ := outputFactPath(g.profileRoot, required)
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(append([]byte{}, good...), byte(' ')), 0600); err != nil {
		t.Fatal(err)
	}
	if err = g.maintain(ctx); err == nil {
		t.Fatal("required fact damage ignored")
	}
	assertOrphan()
	if err = os.WriteFile(path, good, 0600); err != nil {
		t.Fatal(err)
	}
	// A root may be intact while one required member child is damaged.
	tree := newOrdinalTree(s.ordinalPages(), batch.Sats.UTXORoot)
	root, err := tree.read(batch.Sats.UTXORoot)
	if err != nil {
		t.Fatal(err)
	}
	child := ""
	for _, hash := range root.Children {
		if hash != "" {
			child = hash
			break
		}
	}
	if child == "" {
		t.Fatal("fixture did not create a member child")
	}
	path = tree.pagePath(child)
	good, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(append([]byte{}, good...), byte(' ')), 0600); err != nil {
		t.Fatal(err)
	}
	if err = g.maintain(ctx); err == nil {
		t.Fatal("required member damage ignored")
	}
	assertOrphan()
	if err = os.WriteFile(path, good, 0600); err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 100; pass++ {
		err = g.maintain(ctx)
		if !errors.Is(err, errOrdinalMaintenancePending) {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Fatal("unreachable fact was not collected", err)
	}
	s, _, err = g.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	genesis, found, err := recipeSatCheckpointAt(ctx, s, 0)
	if err != nil || !found {
		t.Fatal("retained genesis lost", found, err)
	}
	if _, _, err = recipeSatMemberPage(ctx, s, *genesis.Sats, "outputs", "", 0, 10); err != nil {
		t.Fatal("retained facts/pages collected", err)
	}
	if _, _, err = recipeSatCheckpointAt(ctx, s, 1); err == nil {
		t.Fatal("unretained intermediate point fabricated")
	}
}
