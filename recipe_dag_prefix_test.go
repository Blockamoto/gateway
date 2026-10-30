package main

import (
	"context"
	"errors"
	"testing"
)

func TestRecipeDAGPrefixComparisonAcrossTreeSplitsAndReorgs(t *testing.T) {
	ctx := context.Background()
	_, _, store, _, _ := recipeReconcileFixture(t, ordinalPageEntries)
	recipeStoreMutex.Lock()
	old, err := store.head()
	recipeStoreMutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	unit := recipeStoreUnit{Schema: 1, Fingerprint: store.fingerprint, Height: ordinalPageEntries, BlockHash: recipeTestHash("b"), Rows: []recipeStoreRow{}}
	if err = store.Publish(ctx, unit, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	recipeStoreMutex.Lock()
	extended, err := store.head()
	if err != nil {
		recipeStoreMutex.Unlock()
		t.Fatal(err)
	}
	same, e := recipeStoreRootsEqualRange(ctx, store, old.Root, extended.Root, 0, ordinalPageEntries-1)
	changed, e2 := recipeStoreRootsEqualRange(ctx, store, old.Root, extended.Root, 0, ordinalPageEntries)
	recipeStoreMutex.Unlock()
	if e != nil || !same || e2 != nil || changed {
		t.Fatalf("split range comparison: same=%v %v changed=%v %v", same, e, changed, e2)
	}
	if err = store.Reconcile(ctx, map[int64]string{2: recipeTestHash("a")}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	recipeStoreMutex.Lock()
	after, err := store.head()
	if err != nil {
		recipeStoreMutex.Unlock()
		t.Fatal(err)
	}
	beforeHole, e := recipeStoreRootsEqualRange(ctx, store, extended.Root, after.Root, 0, 1)
	includesHole, e2 := recipeStoreRootsEqualRange(ctx, store, extended.Root, after.Root, 0, 2)
	recipeStoreMutex.Unlock()
	if e != nil || !beforeHole || e2 != nil || includesHole {
		t.Fatalf("removal range comparison: %v %v %v %v", beforeHole, e, includesHole, e2)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	recipeStoreMutex.Lock()
	_, err = recipeStoreRootsEqualRange(cancelled, store, old.Root, extended.Root, 0, 1)
	recipeStoreMutex.Unlock()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
