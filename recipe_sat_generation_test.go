package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func recipeSatTestProgram(t *testing.T, id string, history bool) *recipeProgram {
	t.Helper()
	raw := builtinSatStateJSON
	if history {
		raw = builtinSatRangesJSON
	}
	d, err := parseRecipe(raw)
	if err != nil {
		t.Fatal(err)
	}
	d.ID, d.Name = id, id
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRecipeSatGenerationParityIsolationCASAndLostHead(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	g, err := openRecipeSatGeneration(root, recipeSatTestProgram(t, "my-sats", true))
	if err != nil {
		t.Fatal(err)
	}
	if _, initialized, err := g.prefixSnapshot(ctx); err != nil || initialized {
		t.Fatal(initialized, err)
	}
	if _, err = os.Stat(g.profileRoot); !os.IsNotExist(err) {
		t.Fatal("inspection created a generation", err)
	}
	if err = g.create(ctx); err != nil {
		t.Fatal(err)
	}
	origin, initialized, err := g.prefixSnapshot(ctx)
	if err != nil || !initialized || origin.Height != -1 || origin.Token == "" {
		t.Fatal(origin, initialized, err)
	}
	native, err := openIndexStore(root, "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	native.retainSatHistory = true
	blocks := recipeCoordinatorSource(t)
	for height := int64(0); height <= 1; height++ {
		before, _, _ := g.prefixSnapshot(ctx)
		if err = g.applyFromToken(ctx, blocks[height], before.Token, orderedGenerationTestGuard); err != nil {
			t.Fatal(height, err)
		}
		if err = native.appendBlock(blocks[height], 0, "pruned"); err != nil {
			t.Fatal(err)
		}
		s, h, err := g.snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got, e1 := s.readBatch(s.head.Commitment)
		want, e2 := native.readBatch(native.head.Commitment)
		if e1 != nil || e2 != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(h.Native, native.head) {
			t.Fatal("private/native physical parity", e1, e2)
		}
		if err = g.applyFromToken(ctx, blocks[height], before.Token, orderedGenerationTestGuard); !errors.Is(err, errRecipeStateConflict) {
			t.Fatal("stale receipt accepted", err)
		}
	}
	if _, err = os.Stat(filepath.Join(g.profileRoot, "indexes", "sat-state", "head.json")); !os.IsNotExist(err) {
		t.Fatal("private work wrote a native head")
	}
	other, err := openRecipeSatGeneration(root, recipeSatTestProgram(t, "other-sats", true))
	if err != nil {
		t.Fatal(err)
	}
	if other.profileRoot == g.profileRoot {
		t.Fatal("instance generations overlap")
	}
	if _, initialized, err := other.prefixSnapshot(ctx); err != nil || initialized {
		t.Fatal("generation adopted another instance", err)
	}
	if err = os.Remove(g.path("prefix-head.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err = g.prefixSnapshot(ctx); !errors.Is(err, ErrSatStateCorrupt) {
		t.Fatal("lost head became empty state", err)
	}
}

func TestRecipeSatGenerationGuardRebuildAndRecovery(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	g, err := openRecipeSatGeneration(root, recipeSatTestProgram(t, "recover-sats", false))
	if err != nil {
		t.Fatal(err)
	}
	if err = g.create(ctx); err != nil {
		t.Fatal(err)
	}
	blocks := recipeCoordinatorSource(t)
	before, _, _ := g.prefixSnapshot(ctx)
	if err = g.apply(ctx, blocks[0], func(func() error) error { return nil }); err == nil {
		t.Fatal("guard returned without publication")
	}
	after, _, _ := g.prefixSnapshot(ctx)
	if after != before {
		t.Fatal("staging published a head")
	}
	for height := int64(0); height <= 1; height++ {
		if err = g.apply(ctx, blocks[height], orderedGenerationTestGuard); err != nil {
			t.Fatal(err)
		}
	}
	if err = g.reconcile(ctx, 1, func(_ context.Context, h int64) (string, error) {
		if h == 1 {
			return testID(981444), nil
		}
		return blocks[0].Hash, nil
	}, orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	point, _, err := g.prefixSnapshot(ctx)
	if err != nil || point.Height != 0 {
		t.Fatal(point, err)
	}
	if err = g.apply(ctx, blocks[1], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	review, err := g.reviewToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.resetForRebuild(ctx, "reviewed-sat-job", review, orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	if _, initialized, err := g.prefixSnapshot(ctx); err != nil || initialized {
		t.Fatal("rebuild replacement was not empty", initialized, err)
	}
	if err = g.create(ctx); err != nil {
		t.Fatal(err)
	}
	if err = g.apply(ctx, blocks[0], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	if err = g.resetForRebuild(ctx, "reviewed-sat-job", review, orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	point, _, err = g.prefixSnapshot(ctx)
	if err != nil || point.Height != 0 {
		t.Fatal("resumed rebuild reset replacement progress", point, err)
	}
	if err = g.maintain(ctx); err != nil && !errors.Is(err, errOrdinalMaintenancePending) {
		t.Fatal(err)
	}
}
