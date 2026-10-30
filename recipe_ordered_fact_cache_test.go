package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestRecipeOrderedFactCacheBindsPhysicalOwner(t *testing.T) {
	block := recipeCoordinatorSource(t)[1]
	prepared := &indexRunPrepared{block: block}
	ctx := context.WithValue(context.Background(), indexRunPreparedKey{}, prepared)
	a, b := t.TempDir(), t.TempDir()
	for _, root := range []string{a, b, filepath.Join(a, ".")} {
		refs, err := storeBlockOutputFactsContext(ctx, root, block)
		if err != nil || len(refs) != 1 {
			t.Fatal("physical owner fact publication", len(refs), err)
		}
		for _, ref := range refs {
			fact, err := readOutputFactContext(ctx, root, ref)
			if err != nil || fact.ValueSats != 5_000_000_000 {
				t.Fatal("cached references did not establish facts in this owner", root, err)
			}
		}
	}
	if len(prepared.refs) != 2 {
		t.Fatal("cache failed to separate or normalize physical ownership", len(prepared.refs))
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := storeBlockOutputFactsContext(canceled, a, block); !errors.Is(err, context.Canceled) {
		t.Fatal("cached fact publication ignored cancellation", err)
	}
}
