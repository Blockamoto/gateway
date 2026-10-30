package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func recipeDAGMakeChildStale(t *testing.T, lineage *recipeLineagePlan, middle *recipeStore, firstRef recipeStoreRef) {
	t.Helper()
	ctx := context.Background()
	if err := middle.Reconcile(ctx, map[int64]string{0: genesisHashDisplay}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	root := lineage.nodes[lineage.root]
	dep := root.definition.Dependencies[0]
	up := lineage.nodes[recipeGraphIdentity{dep.Instance, dep.Revision, dep.SemanticHash}].definition.Dependencies[0]
	replacement := recipeStoreUnit{Schema: 1, Fingerprint: middle.fingerprint, Height: 0, BlockHash: genesisHashDisplay, Rows: []recipeStoreRow{{Key: `["items","x"]`, Value: json.RawMessage(`{"ref":"x"}`)}}, Lineage: []recipeDependencyReceipt{{Name: up.Name, Instance: up.Instance, SemanticHash: up.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Unit: firstRef.Unit}}}
	if err := middle.publish(ctx, replacement, nil, recipeTestGuard, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}
func TestRecipeDAGReconcileKnownStaleAndUnknownParent(t *testing.T) {
	ctx := context.Background()
	a, plan, binding, lineage, first, middle, child, firstRef := recipeDAGInspectFixture(t)
	r := &recipeLocalDAGReconciler{a: a, binding: binding, lineage: lineage, store: child}
	if err := first.Reconcile(ctx, map[int64]string{0: genesisHashDisplay}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcile(ctx, plan); !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatalf("unavailable ancestor was not unknown: %v", err)
	}
	if _, pending, err := child.inspectReconcileFence(); err != nil || pending {
		t.Fatalf("unknown created fence: %v %v", pending, err)
	}
	if err := first.Publish(ctx, recipeStoreUnit{Schema: 1, Fingerprint: first.fingerprint, Height: 0, BlockHash: genesisHashDisplay, Rows: []recipeStoreRow{}}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	recipeStoreMutex.Lock()
	_, oldRef, found, err := recipeDependencyReadLocked(ctx, child, 0)
	recipeStoreMutex.Unlock()
	if err != nil || !found {
		t.Fatal("missing old child", err)
	}
	recipeDAGMakeChildStale(t, lineage, middle, firstRef)
	if err = r.reconcile(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if _, found, err = child.Read(ctx, 0); err != nil || found {
		t.Fatalf("stale child remains: %v %v", found, err)
	}
	if _, err = os.Stat(filepath.Join(child.dir, "units", oldRef.Unit+".json")); err != nil {
		t.Fatalf("old unit not retained: %v", err)
	}
	if _, pending, err := child.inspectReconcileFence(); err != nil || pending {
		t.Fatalf("completed journal remains: %v %v", pending, err)
	}
}
func TestRecipeDAGReconcileParentChangeKeepsFenceUntilReaudit(t *testing.T) {
	ctx := context.Background()
	a, plan, binding, lineage, first, middle, child, firstRef := recipeDAGInspectFixture(t)
	recipeDAGMakeChildStale(t, lineage, middle, firstRef)
	r := &recipeLocalDAGReconciler{a: a, binding: binding, lineage: lineage, store: child}
	token, err := r.token(ctx, plan, "")
	if err != nil {
		t.Fatal(err)
	}
	page, err := a.recipeLocalDAGInspectPage(ctx, binding, plan, lineage, child, nil, 0, false)
	if err != nil || len(page.Anchors) != 1 {
		t.Fatalf("inspection: %+v %v", page, err)
	}
	if err = r.begin(ctx, plan, page.Revision, token); err != nil {
		t.Fatal(err)
	}
	header, err := a.readSelectedHeader(1)
	if err != nil {
		t.Fatal(err)
	}
	digest := hash256(header)
	if err = first.Publish(ctx, recipeStoreUnit{Schema: 1, Fingerprint: first.fingerprint, Height: 1, BlockHash: reverseHex(digest[:]), Rows: []recipeStoreRow{}}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if err = r.batch(ctx, plan, page.Revision, token, page.Anchors); err == nil {
		t.Fatal("batch accepted changed parent vector")
	}
	if err = r.finish(ctx, plan, page.Revision, token); err == nil {
		t.Fatal("changed parent vector cleared fence")
	}
	if _, _, err = child.Read(ctx, 0); !errors.Is(err, errRecipeReconcileFenced) {
		t.Fatalf("fenced child exposed: %v", err)
	}
	if err = r.reconcile(ctx, plan); err != nil {
		t.Fatalf("re-audit failed: %v", err)
	}
	if _, pending, err := child.inspectReconcileFence(); err != nil || pending {
		t.Fatalf("fence did not clear after re-audit: %v %v", pending, err)
	}
	if _, found, err := child.Read(ctx, 0); err != nil || found {
		t.Fatalf("stale child survived: %v %v", found, err)
	}
}
