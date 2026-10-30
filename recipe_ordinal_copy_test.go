package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestRecipeOrdinalCopyChangedBranchesAndOwnership(t *testing.T) {
	ctx := context.Background()
	from := newOrdinalTree(t.TempDir(), "").withContext(ctx)
	for i := 0; i < 320; i++ {
		if err := from.put(fmt.Sprintf("%064x", i), uint64(i+1)); err != nil {
			t.Fatal(err)
		}
	}
	base, err := from.flush()
	if err != nil {
		t.Fatal(err)
	}
	to := newOrdinalTree(t.TempDir(), "").withContext(ctx)
	visits := 0
	leaf := func(ordinalKV) error { visits++; return nil }
	if err = recipeCopyOrdinalSubtree(from, to, base, "", leaf); err != nil || visits != 320 {
		t.Fatal("incomplete initial copy", visits, err)
	}
	sourceInfo, err := os.Stat(from.pagePath(base))
	if err != nil {
		t.Fatal(err)
	}
	ownedInfo, err := os.Stat(to.pagePath(base))
	if err != nil || os.SameFile(sourceInfo, ownedInfo) {
		t.Fatal("COW metadata was hard-linked across owners", err)
	}
	if err = from.put(fmt.Sprintf("%064x", 320), uint64(321)); err != nil {
		t.Fatal(err)
	}
	next, err := from.flush()
	if err != nil {
		t.Fatal(err)
	}
	visits = 0
	if err = recipeCopyOrdinalSubtree(from, to, next, base, leaf); err != nil || visits == 0 || visits >= 320 {
		t.Fatal("incremental copy rescanned complete state", visits, err)
	}
	for i := 0; i <= 320; i++ {
		var value uint64
		found, err := to.get(fmt.Sprintf("%064x", i), &value)
		if err != nil || !found || value != uint64(i+1) {
			t.Fatal("copied state differs", i, value, err)
		}
	}
	visits = 0
	missingSource := newOrdinalTree(t.TempDir(), next).withContext(ctx)
	if err = recipeCopyOrdinalSubtree(missingSource, to, next, next, leaf); err != nil || visits != 0 {
		t.Fatal("unchanged owned root required source traversal", err)
	}
	work, cancel := context.WithCancel(ctx)
	cancel()
	if err = recipeCopyOrdinalSubtree(from, newOrdinalTree(t.TempDir(), "").withContext(work), next, "", nil); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled copy continued", err)
	}
}
