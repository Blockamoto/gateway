package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func recipeTestGuard(fn func() error) error { return fn() }
func recipeTestHash(c string) string        { return strings.Repeat(c, 64) }
func recipeTestUnit(height int64, hash string, rows []recipeStoreRow) recipeStoreUnit {
	return recipeStoreUnit{Schema: 1, Fingerprint: recipeTestHash("a"), Height: height, BlockHash: hash, Rows: rows}
}
func recipeTestOpen(t *testing.T, root, id, fingerprint string) *recipeStore {
	t.Helper()
	s, err := openRecipeStore(root, id, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRecipeStoreSparseEmptyReopenAndGeneration(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s := recipeTestOpen(t, root, "../display/id", recipeTestHash("a"))
	first := recipeTestUnit(2, recipeTestHash("b"), []recipeStoreRow{{Key: "1.2", Value: json.RawMessage(`{"x":1}`)}})
	empty := recipeTestUnit(9, recipeTestHash("c"), []recipeStoreRow{})
	for _, u := range []recipeStoreUnit{first, empty, first} {
		if err := s.Publish(ctx, u, recipeTestGuard); err != nil {
			t.Fatal(err)
		}
	}
	reopened := recipeTestOpen(t, root, "../display/id", recipeTestHash("a"))
	got, found, err := reopened.Read(ctx, 9)
	if err != nil || !found || got.Rows == nil || len(got.Rows) != 0 {
		t.Fatalf("empty receipt: %+v %v %v", got, found, err)
	}
	_, found, err = reopened.Read(ctx, 3)
	if err != nil || found {
		t.Fatalf("sparse gap: %v %v", found, err)
	}
	got, found, err = reopened.Read(ctx, 2)
	if err != nil || !found || got.Rows[0].Key != "1.2" {
		t.Fatalf("stored row: %+v %v %v", got, found, err)
	}
	other := recipeTestOpen(t, root, "../display/id", recipeTestHash("d"))
	_, found, err = other.Read(ctx, 2)
	if err != nil || found {
		t.Fatalf("generation isolation: %v %v", found, err)
	}
	if !strings.HasPrefix(s.dir, filepath.Join(root, "indexes", "recipes-v1")+string(os.PathSeparator)) {
		t.Fatalf("unsafe path: %s", s.dir)
	}
}
func TestRecipeStoreGuardFailuresAndDuplicateRows(t *testing.T) {
	ctx := context.Background()
	s := recipeTestOpen(t, t.TempDir(), "test", recipeTestHash("a"))
	u := recipeTestUnit(0, recipeTestHash("b"), []recipeStoreRow{})
	refusal := errors.New("stale admission")
	if err := s.Publish(ctx, u, func(func() error) error { return refusal }); !errors.Is(err, refusal) {
		t.Fatal(err)
	}
	if _, found, err := s.Read(ctx, 0); err != nil || found {
		t.Fatalf("guard published: %v %v", found, err)
	}
	if err := s.Publish(ctx, u, nil); err == nil {
		t.Fatal("accepted missing guard")
	}
	u.Rows = []recipeStoreRow{{Key: "same", Value: json.RawMessage(`1`)}, {Key: "same", Value: json.RawMessage(`2`)}}
	if err := s.Publish(ctx, u, recipeTestGuard); err == nil {
		t.Fatal("accepted duplicate row")
	}
	u.Rows = nil
	if err := s.Publish(ctx, u, recipeTestGuard); err == nil {
		t.Fatal("accepted null receipt")
	}
	u.Rows = []recipeStoreRow{}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Publish(canceled, u, recipeTestGuard); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestRecipeStoreReconcileIsAtomicAndKeepsOldObjects(t *testing.T) {
	ctx := context.Background()
	s := recipeTestOpen(t, t.TempDir(), "test", recipeTestHash("a"))
	a := recipeTestUnit(1, recipeTestHash("b"), []recipeStoreRow{})
	b := recipeTestUnit(7, recipeTestHash("c"), []recipeStoreRow{})
	if err := s.Publish(ctx, a, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(ctx, b, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	h, _ := s.head()
	tree := s.tree(ctx, h)
	var ref recipeStoreRef
	if found, err := tree.get(recipeStoreHeightKey(1), &ref); !found || err != nil {
		t.Fatal(found, err)
	}
	if err := s.Reconcile(ctx, map[int64]string{1: a.BlockHash, 7: recipeTestHash("d")}, recipeTestGuard); err == nil {
		t.Fatal("accepted stale anchor")
	}
	if _, found, err := s.Read(ctx, 1); err != nil || !found {
		t.Fatalf("partial reconciliation: %v %v", found, err)
	}
	if err := s.Reconcile(ctx, map[int64]string{1: a.BlockHash}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.Read(ctx, 1); err != nil || found {
		t.Fatalf("still covered: %v %v", found, err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "units", ref.Unit+".json")); err != nil {
		t.Fatal("old unit erased", err)
	}
	replacement := recipeTestUnit(1, recipeTestHash("d"), []recipeStoreRow{})
	if err := s.Publish(ctx, replacement, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if got, found, err := s.Read(ctx, 1); err != nil || !found || got.BlockHash != replacement.BlockHash {
		t.Fatalf("replacement: %+v %v %v", got, found, err)
	}
	if _, found, err := s.Read(ctx, 7); err != nil || !found {
		t.Fatalf("untouched receipt: %v %v", found, err)
	}
}
func TestRecipeStoreDetectsCorruptionAndNeverOverwrites(t *testing.T) {
	ctx := context.Background()
	s := recipeTestOpen(t, t.TempDir(), "test", recipeTestHash("a"))
	u := recipeTestUnit(1, recipeTestHash("b"), []recipeStoreRow{})
	if err := s.Publish(ctx, u, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	h, _ := s.head()
	var ref recipeStoreRef
	_, err := s.tree(ctx, h).get(recipeStoreHeightKey(1), &ref)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dir, "units", ref.Unit+".json")
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Read(ctx, 1); err == nil {
		t.Fatal("accepted corrupt unit")
	}
	if err := s.Publish(ctx, u, recipeTestGuard); err == nil {
		t.Fatal("overwrote corrupt unit")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "corrupt" {
		t.Fatalf("corruption overwritten: %q %v", data, err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "head.json"), []byte(strings.Repeat("x", recipeStoreHeadMaxBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Read(ctx, 1); err == nil {
		t.Fatal("accepted oversized head")
	}
}

func TestRecipeStoreConflictAndGuardCannotSuppressFailure(t *testing.T) {
	ctx := context.Background()
	s := recipeTestOpen(t, t.TempDir(), "test", recipeTestHash("a"))
	a := recipeTestUnit(1, recipeTestHash("b"), []recipeStoreRow{})
	b := recipeTestUnit(1, recipeTestHash("c"), []recipeStoreRow{})
	if err := s.Publish(ctx, a, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	swallowingGuard := func(f func() error) error { _ = f(); return nil }
	if err := s.Publish(ctx, b, swallowingGuard); err == nil {
		t.Fatal("guard suppressed conflict")
	}
	got, found, err := s.Read(ctx, 1)
	if err != nil || !found || got.BlockHash != a.BlockHash {
		t.Fatalf("conflict changed head: %+v %v %v", got, found, err)
	}
	if err := s.Reconcile(ctx, map[int64]string{1: b.BlockHash}, swallowingGuard); err == nil {
		t.Fatal("guard suppressed stale reconciliation")
	}
}

func TestRecipeStoreRejectsCorruptUnreferencedUnit(t *testing.T) {
	ctx := context.Background()
	s := recipeTestOpen(t, t.TempDir(), "test", recipeTestHash("a"))
	u := recipeTestUnit(5, recipeTestHash("b"), []recipeStoreRow{})
	raw, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.dir, "units")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, recipeStoreHash(raw)+".json")
	if err := os.WriteFile(path, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(ctx, u, recipeTestGuard); err == nil {
		t.Fatal("accepted corrupt immutable orphan")
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "bad" {
		t.Fatalf("corruption overwritten %q %v", b, err)
	}
	if _, found, err := s.Read(ctx, 5); err != nil || found {
		t.Fatalf("failed publication became visible: %v %v", found, err)
	}
}

func TestRecipeStoreMissingHeadFailsClosedAndKeepsHistory(t *testing.T) {
	ctx := context.Background()
	s := recipeTestOpen(t, t.TempDir(), "test", recipeTestHash("a"))
	u := recipeTestUnit(2, recipeTestHash("b"), []recipeStoreRow{})
	if err := s.Publish(ctx, u, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(filepath.Join(s.dir, "head.json"))
	if err != nil {
		t.Fatal(err)
	}
	u = recipeTestUnit(4, recipeTestHash("c"), []recipeStoreRow{})
	if err := s.Publish(ctx, u, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(s.dir, "heads", recipeStoreHash(old)+".json"))
	if err != nil || string(saved) != string(old) {
		t.Fatalf("previous head not retained: %v", err)
	}
	if err := os.Remove(filepath.Join(s.dir, "head.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Read(ctx, 2); err == nil {
		t.Fatal("missing initialized head read as empty")
	}
	if err := s.Publish(ctx, u, recipeTestGuard); err == nil {
		t.Fatal("missing initialized head overwritten")
	}
	if err := os.Remove(filepath.Join(s.dir, "initialized.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Read(ctx, 2); err == nil {
		t.Fatal("missing marker and head with retained history read as empty")
	}
}

func TestRecipeStoreRejectsLinkedPaths(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "indexes"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "indexes", "recipes-v1")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := openRecipeStore(root, "test", recipeTestHash("a")); err == nil {
		t.Fatal("accepted linked store path")
	}
}

func TestRecipeStoreCoverageAndReorgPlanning(t *testing.T) {
	ctx := context.Background()
	s := recipeTestOpen(t, t.TempDir(), "coverage", recipeTestHash("a"))
	for _, height := range []int64{1, 5, 12, 20} {
		if err := s.Publish(ctx, recipeTestUnit(height, recipeTestHash("b"), []recipeStoreRow{}), recipeTestGuard); err != nil {
			t.Fatal(err)
		}
	}
	p, err := s.Coverage(ctx, 0, 20, nil, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Anchors) != 2 || p.Anchors[0].Height != 1 || p.Anchors[1].Height != 5 || p.Next == nil || *p.Next != 5 {
		t.Fatalf("first page: %+v", p)
	}
	next, err := s.Coverage(ctx, 0, 20, p.Next, p.Revision, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Anchors) != 2 || next.Anchors[0].Height != 12 || next.Anchors[1].Height != 20 || next.Next != nil {
		t.Fatalf("second page: %+v", next)
	}
	empty, err := s.Coverage(ctx, 6, 11, nil, 0, 5)
	if err != nil || len(empty.Anchors) != 0 {
		t.Fatalf("gap: %+v %v", empty, err)
	}
	selected := func(h int64) (string, error) {
		if h == 5 {
			return recipeTestHash("c"), nil
		}
		return recipeTestHash("b"), nil
	}
	plan, err := s.PlanReorg(ctx, 0, 20, nil, 0, 8, selected)
	if err != nil || len(plan.Anchors) != 1 || plan.Anchors[0].Height != 5 {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	if _, found, err := s.Read(ctx, 5); err != nil || !found {
		t.Fatalf("planning mutated coverage: %v %v", found, err)
	}
	_, err = s.PlanReorg(ctx, 0, 20, nil, 0, 8, func(h int64) (string, error) {
		if h == 12 {
			return "", errors.New("missing header")
		}
		return selected(h)
	})
	if err == nil {
		t.Fatal("unavailable selected chain accepted")
	}
	if err := s.Publish(ctx, recipeTestUnit(25, recipeTestHash("b"), []recipeStoreRow{}), recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Coverage(ctx, 0, 20, p.Next, p.Revision, 2); err == nil {
		t.Fatal("stale pagination accepted")
	}
	if err := s.ReconcileAtRevision(ctx, plan.Revision, map[int64]string{5: recipeTestHash("b")}, recipeTestGuard); err == nil {
		t.Fatal("stale plan applied")
	}
	fresh, err := s.PlanReorg(ctx, 0, 20, nil, 0, 8, selected)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileAtRevision(ctx, fresh.Revision, map[int64]string{5: recipeTestHash("b")}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.Read(ctx, 5); err != nil || found {
		t.Fatalf("reconciliation failed: %v %v", found, err)
	}
}

func TestRecipeStoreRefusesLinkedTreePage(t *testing.T) {
	ctx := context.Background()
	s := recipeTestOpen(t, t.TempDir(), "pages", recipeTestHash("a"))
	if err := s.Publish(ctx, recipeTestUnit(1, recipeTestHash("b"), []recipeStoreRow{}), recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	h, err := s.head()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dir, "pages", h.Root[:2], h.Root+".json")
	saved := path + ".saved"
	if err := os.Rename(path, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(saved, path); err != nil {
		_ = os.Rename(saved, path)
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, _, err := s.Read(ctx, 1); err == nil {
		t.Fatal("linked tree page was accepted")
	}
	if _, err := s.Coverage(ctx, 0, 5, nil, 0, 5); err == nil {
		t.Fatal("coverage followed linked tree page")
	}
}

func TestRecipeCoveragePrunesUnrelatedRadixBranches(t *testing.T) {
	ctx := context.Background()
	s := recipeTestOpen(t, t.TempDir(), "branches", recipeTestHash("a"))
	low, high := int64(1), int64(1<<60)
	for _, h := range []int64{low, high} {
		if err := s.Publish(ctx, recipeTestUnit(h, recipeTestHash("b"), []recipeStoreRow{}), recipeTestGuard); err != nil {
			t.Fatal(err)
		}
	}
	head, err := s.head()
	if err != nil {
		t.Fatal(err)
	}
	oldTree := s.tree(ctx, head)
	var lowRef, highRef recipeStoreRef
	if found, err := oldTree.get(recipeStoreHeightKey(low), &lowRef); err != nil || !found {
		t.Fatal(found, err)
	}
	if found, err := oldTree.get(recipeStoreHeightKey(high), &highRef); err != nil || !found {
		t.Fatal(found, err)
	}
	lowRaw, _ := json.Marshal(lowRef)
	highRaw, _ := json.Marshal(highRef)
	tree := s.tree(ctx, head)
	lowPage, err := tree.write(ordinalPage{Entries: []ordinalKV{{Key: recipeStoreHeightKey(low), Value: lowRaw}}})
	if err != nil {
		t.Fatal(err)
	}
	highPage, err := tree.write(ordinalPage{Entries: []ordinalKV{{Key: recipeStoreHeightKey(high), Value: highRaw}}})
	if err != nil {
		t.Fatal(err)
	}
	children := make([]string, 16)
	children[0] = lowPage
	children[1] = highPage
	root, err := tree.write(ordinalPage{Children: children})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.flush(); err != nil {
		t.Fatal(err)
	}
	if err := s.publishHead(head, root); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(s.dir, "pages", highPage[:2], highPage+".json")
	if err := os.WriteFile(badPath, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := s.Coverage(ctx, 0, 10, nil, 0, 5)
	if err != nil || len(page.Anchors) != 1 || page.Anchors[0].Height != low {
		t.Fatalf("unrelated branch was traversed: %+v %v", page, err)
	}
	if _, err := s.Coverage(ctx, 0, high, nil, 0, 5); err == nil {
		t.Fatal("corrupt in-range branch accepted")
	}
}
