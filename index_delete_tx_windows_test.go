package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// These fixtures publish through the ordinary point writer first, then build
// one bounded visibility manifest. Session durability is tested by its owner;
// these cases exercise deletion of that exact on-disk receipt format.
func deleteTXWindowFixture(t *testing.T, a *app, heights ...int64) indexBrowseTXWindow {
	t.Helper()
	window := indexBrowseTXWindow{Schema: 3, Window: 0}
	for _, height := range heights {
		point, have, err := readBrowseTXPoint(context.Background(), a.dataDir, height)
		if err != nil || !have {
			t.Fatal("missing window fixture point", height, have, err)
		}
		window.Points = append(window.Points, point)
	}
	sort.Slice(window.Points, func(i, j int) bool { return window.Points[i].Height < window.Points[j].Height })
	window.Digest = browseTXWindowDigest(window)
	if err := atomicWriteJSON(browseTXWindowPath(a.dataDir, 0), window); err != nil {
		t.Fatal(err)
	}
	got, have, err := readBrowseTXWindow(context.Background(), a.dataDir, 0)
	if err != nil || !have || !reflect.DeepEqual(got, window) {
		t.Fatal("invalid window fixture", got, have, err)
	}
	return window
}

func TestIndexDeleteTXWindowRangeRetainsNeighborWithoutLeafResurrection(t *testing.T) {
	a, blocks := deleteTXPointFixture(t)
	first := deleteSaveTXPoint(t, a, blocks[0])
	second := deleteSaveTXPoint(t, a, blocks[1])
	deleteTXWindowFixture(t, a, 0, 1)
	// A different, validly authenticated older receipt is hidden by the
	// window. Deletion must remove its receipt and its reviewed commit too.
	older := first
	older.Checkpoint.DependencyFingerprint = indexDigest("retained older point recipe")
	older.Checkpoint.Commitment = checkpointHash(older.Checkpoint)
	dir := filepath.Join(a.dataDir, "indexes", "browse", "tx-locator")
	if err := atomicWriteJSON(filepath.Join(dir, "commits", older.Checkpoint.Commitment+".json"), older); err != nil {
		t.Fatal(err)
	}
	leaf, have, err := readBrowseTXPointLeaf(context.Background(), a.dataDir, 0)
	if err != nil || !have {
		t.Fatal(leaf, have, err)
	}
	leaf.Commitment = older.Checkpoint.Commitment
	leaf.Digest = browseTXPointDigest(leaf)
	if err := atomicWriteJSON(browseTXPointPath(a.dataDir, 0), leaf); err != nil {
		t.Fatal(err)
	}
	neighborPath := browseTXPointPath(a.dataDir, 1)
	neighborBefore, err := os.ReadFile(neighborPath)
	if err != nil {
		t.Fatal(err)
	}
	deleteConfirmBatch(t, a, indexDeleteRequest{Indices: []string{"tx-locator"}, Range: &indexDeleteRange{From: deleteHeight(0), To: deleteHeight(0)}})
	window, have, err := readBrowseTXWindow(context.Background(), a.dataDir, 0)
	if err != nil || !have || len(window.Points) != 1 || window.Points[0].Height != 1 || window.Points[0].Commitment != second.Checkpoint.Commitment {
		t.Fatal("unselected window member changed", window, have, err)
	}
	if _, have, err := readBrowseTXPoint(context.Background(), a.dataDir, 0); err != nil || have {
		t.Fatal("selected point or shadow leaf resurrected", have, err)
	}
	if after, err := os.ReadFile(neighborPath); err != nil || string(after) != string(neighborBefore) {
		t.Fatal("unselected old leaf changed", err)
	}
	for _, commitment := range []string{first.Checkpoint.Commitment, older.Checkpoint.Commitment} {
		if _, err := os.Stat(filepath.Join(dir, "commits", commitment+".json")); !os.IsNotExist(err) {
			t.Fatal("reviewed selected generation remains", commitment, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "commits", second.Checkpoint.Commitment+".json")); err != nil {
		t.Fatal("unselected commit was removed", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "head.json")); !os.IsNotExist(err) {
		t.Fatal("window deletion materialized a legacy head", err)
	}
}

func TestIndexDeleteTXWindowLastMemberRemovesManifestAndShadow(t *testing.T) {
	a, blocks := deleteTXPointFixture(t)
	deleteSaveTXPoint(t, a, blocks[0])
	deleteTXWindowFixture(t, a, 0)
	deleteConfirmBatch(t, a, indexDeleteRequest{Indices: []string{"tx-locator"}, Range: &indexDeleteRange{From: deleteHeight(0), To: deleteHeight(0)}})
	for _, path := range []string{browseTXWindowPath(a.dataDir, 0), browseTXPointPath(a.dataDir, 0)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("selected receipt remains", path, err)
		}
	}
	if _, have, err := readBrowseTXPoint(context.Background(), a.dataDir, 0); err != nil || have {
		t.Fatal("removed last window member remains visible", have, err)
	}
}

func TestIndexDeleteTXWindowRejectsShadowClaimingNeighborCommit(t *testing.T) {
	a, blocks := deleteTXPointFixture(t)
	deleteSaveTXPoint(t, a, blocks[0])
	neighbor := deleteSaveTXPoint(t, a, blocks[1])
	deleteTXWindowFixture(t, a, 0, 1)
	leaf, have, err := readBrowseTXPointLeaf(context.Background(), a.dataDir, 0)
	if err != nil || !have {
		t.Fatal(leaf, have, err)
	}
	leaf.Commitment = neighbor.Checkpoint.Commitment
	leaf.Digest = browseTXPointDigest(leaf)
	if err := atomicWriteJSON(browseTXPointPath(a.dataDir, 0), leaf); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(a.dataDir, "indexes", "browse", "tx-locator")
	before := offLocatorFiles(t, dir)
	act := indexDeleteAction{Item: indexDeleteItem{Index: "tx-locator"}, Review: "captured"}
	err = a.planIndexDeleteBrowse(context.Background(), &act, &indexDeleteRange{From: deleteHeight(0), To: deleteHeight(0)})
	if err == nil || !strings.Contains(err.Error(), "does not own its commit") {
		t.Fatal("a shadow receipt claimed a different block's commit", err)
	}
	if after := offLocatorFiles(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("rejected shadow receipt changed saved files")
	}
}

func TestIndexDeleteTXWindowApplyRejectsChangedOrNewManifest(t *testing.T) {
	for _, existed := range []bool{false, true} {
		name := "introduced_after_review"
		if existed {
			name = "member_added_after_review"
		}
		t.Run(name, func(t *testing.T) {
			a, blocks := deleteTXPointFixture(t)
			deleteSaveTXPoint(t, a, blocks[0])
			deleteSaveTXPoint(t, a, blocks[1])
			if existed {
				deleteTXWindowFixture(t, a, 0)
			}
			act := indexDeleteAction{Item: indexDeleteItem{Index: "tx-locator"}, Review: "captured"}
			if err := a.planIndexDeleteBrowse(context.Background(), &act, &indexDeleteRange{From: deleteHeight(0), To: deleteHeight(0)}); err != nil {
				t.Fatal(err)
			}
			deleteTXWindowFixture(t, a, 0, 1)
			mutations := 0
			err := a.applyIndexDeleteBrowse(context.Background(), act.Browse, func(string) error { mutations++; return nil }, func(string) error { mutations++; return nil })
			if err == nil || !strings.Contains(err.Error(), "review deletion again") || mutations != 0 {
				t.Fatal("changed window reached deletion writes", mutations, err)
			}
		})
	}
}

func TestIndexDeleteTXWindowApplyRejectsNewShadowLeaf(t *testing.T) {
	a, blocks := deleteTXPointFixture(t)
	deleteSaveTXPoint(t, a, blocks[0])
	deleteTXWindowFixture(t, a, 0)
	leaf, have, err := readBrowseTXPointLeaf(context.Background(), a.dataDir, 0)
	if err != nil || !have {
		t.Fatal(leaf, have, err)
	}
	if err := os.Remove(browseTXPointPath(a.dataDir, 0)); err != nil {
		t.Fatal(err)
	}
	act := indexDeleteAction{Item: indexDeleteItem{Index: "tx-locator"}, Review: "captured"}
	if err := a.planIndexDeleteBrowse(context.Background(), &act, &indexDeleteRange{From: deleteHeight(0), To: deleteHeight(0)}); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteJSON(browseTXPointPath(a.dataDir, 0), leaf); err != nil {
		t.Fatal(err)
	}
	mutations := 0
	err = a.applyIndexDeleteBrowse(context.Background(), act.Browse, func(string) error { mutations++; return nil }, func(string) error { mutations++; return nil })
	if err == nil || !strings.Contains(err.Error(), "review deletion again") || mutations != 0 {
		t.Fatal("new unreviewed shadow leaf reached deletion writes", mutations, err)
	}
}

func TestIndexDeleteTXWindowWholeReviewBindsHiddenLeafAndMembership(t *testing.T) {
	for _, change := range []string{"hidden_leaf", "window_member"} {
		t.Run(change, func(t *testing.T) {
			a, blocks := deleteTXPointFixture(t)
			deleteSaveTXPoint(t, a, blocks[0])
			deleteSaveTXPoint(t, a, blocks[1])
			deleteTXWindowFixture(t, a, 0)
			a.updateBackgroundWork.Wait()
			q := indexDeleteRequest{Indices: []string{"tx-locator"}}
			review, err := a.reviewIndexDeletionBatch(context.Background(), q)
			if err != nil || !review.Ready {
				t.Fatal(review, err)
			}
			if change == "hidden_leaf" {
				leaf, have, err := readBrowseTXPointLeaf(context.Background(), a.dataDir, 0)
				if err != nil || !have {
					t.Fatal(leaf, have, err)
				}
				leaf.Packed = !leaf.Packed
				leaf.Digest = browseTXPointDigest(leaf)
				if err := atomicWriteJSON(browseTXPointPath(a.dataDir, 0), leaf); err != nil {
					t.Fatal(err)
				}
			} else {
				deleteTXWindowFixture(t, a, 0, 1)
			}
			q.ReviewID = review.ReviewID
			if _, err := a.deleteIndexDataBatch(context.Background(), q); err == nil || !strings.Contains(err.Error(), "review deletion again") {
				t.Fatal("whole-store deletion admitted a changed physical receipt", err)
			}
			for _, height := range []int64{0, 1} {
				if _, have, err := readBrowseTXPoint(context.Background(), a.dataDir, height); err != nil || !have {
					t.Fatal("stale whole-store review changed data", height, have, err)
				}
			}
		})
	}
}
