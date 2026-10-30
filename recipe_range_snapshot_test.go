package main

import (
	"context"
	"testing"
)

func TestRecipeRangeSnapshotComparisonSurvivesOutsideSplits(t *testing.T) {
	ctx := context.Background()
	s, err := openRecipeStore(t.TempDir(), "range-view", recipeStoreHash([]byte("range")))
	if err != nil {
		t.Fatal(err)
	}
	tree := s.tree(ctx, recipeStoreHead{})
	for height := int64(0); height < 64; height++ {
		if err := tree.put(recipeStoreHeightKey(height), height); err != nil {
			t.Fatal(err)
		}
	}
	original, err := tree.flush()
	if err != nil {
		t.Fatal(err)
	}
	for height := int64(64); height < 160; height++ {
		if err := tree.put(recipeStoreHeightKey(height), height); err != nil {
			t.Fatal(err)
		}
	}
	appended, err := tree.flush()
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{original, appended}, {appended, original}} {
		for _, window := range [][2]int64{{0, 63}, {2, 50}, {63, 63}, {500, 900}} {
			if same, err := recipeStoreRangeEqual(ctx, s, pair[0], pair[1], window[0], window[1]); err != nil || !same {
				t.Fatal("outside split changed historical view", window, same, err)
			}
		}
		if same, err := recipeStoreRangeEqual(ctx, s, pair[0], pair[1], 0, 64); err != nil || same {
			t.Fatal("in-range append escaped comparison", same, err)
		}
	}
	tree.remove(recipeStoreHeightKey(0))
	trimmed, err := tree.flush()
	if err != nil {
		t.Fatal(err)
	}
	if same, err := recipeStoreRangeEqual(ctx, s, original, trimmed, 2, 50); err != nil || !same {
		t.Fatal("outside removal changed the view", same, err)
	}
	if err := tree.put(recipeStoreHeightKey(20), "changed value"); err != nil {
		t.Fatal(err)
	}
	changed, err := tree.flush()
	if err != nil {
		t.Fatal(err)
	}
	if same, err := recipeStoreRangeEqual(ctx, s, original, changed, 2, 50); err != nil || same {
		t.Fatal("changed historical receipt escaped comparison", same, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := recipeStoreRangeEqual(canceled, s, original, original, 0, 63); err != context.Canceled {
		t.Fatal("identical subtree reuse ignored cancellation", err)
	}
}
