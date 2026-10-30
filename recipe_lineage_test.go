package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func recipeLineageSaveFixture(t *testing.T, a *app, id string, deps []recipeDependency) resolvedSavedRecipe {
	t.Helper()
	encoded, err := json.Marshal(deps)
	if err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf(`{"format":"gateway-recipe","version":1,"id":%q,"name":%q,"source":"bitcoin.reveal","origin":0,"history":"sparse","dependencies":%s,"exports":[{"name":"kind","kind":"same_block","table":"items","column":"ref","type":"text"}],"tables":[{"name":"items","key":"ref"}],"steps":[{"op":"write","table":"items","row":{"ref":{"field":["ref"]}}}]}`, id, id, encoded)
	view, canonical, err := recipeRegistryCompile([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	recipeRegistryMu.Lock()
	view, err = recipeRegistrySave(context.Background(), a.dataDir, view, canonical, "", "")
	recipeRegistryMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.ResolveSavedRecipe(context.Background(), id, view.Revision, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func recipeLineagePublishFixture(t *testing.T, a *app, r resolvedSavedRecipe, children []recipeDependencyReceipt) (recipeStoreRef, *recipeStore) {
	t.Helper()
	store, err := openRecipeStore(a.dataDir, r.ID, r.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	unit := recipeStoreUnit{Schema: 1, Fingerprint: r.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Rows: []recipeStoreRow{}, Lineage: children}
	data, err := json.Marshal(unit)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.publish(context.Background(), unit, nil, recipeTestGuard, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	return recipeStoreRef{BlockHash: unit.BlockHash, Unit: recipeStoreHash(data)}, store
}
func recipeLineageThreeFixture(t *testing.T, wrong bool) (*app, *recipeLineagePlan, *recipeStore, *recipeStore) {
	t.Helper()
	a := &app{dataDir: t.TempDir()}
	parent := recipeLineageSaveFixture(t, a, "parent-a", nil)
	parentRef, parentStore := recipeLineagePublishFixture(t, a, parent, nil)
	middle := recipeLineageSaveFixture(t, a, "parent-b", []recipeDependency{recipeGraphFixtureEdge("up", parent)})
	receipt := recipeDependencyReceipt{Name: "up", Instance: parent.ID, SemanticHash: parent.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Unit: parentRef.Unit}
	if wrong {
		receipt.Unit = recipeTestHash("b")
	}
	_, middleStore := recipeLineagePublishFixture(t, a, middle, []recipeDependencyReceipt{receipt})
	child := recipeLineageSaveFixture(t, a, "child-c", []recipeDependency{recipeGraphFixtureEdge("middle", middle)})
	plan, err := a.prepareRecipeLineage(context.Background(), child)
	if err != nil {
		t.Fatal(err)
	}
	return a, plan, parentStore, middleStore
}
func TestRecipeLineageRecursesAndRechecksEveryAncestor(t *testing.T) {
	_, plan, parent, middle := recipeLineageThreeFixture(t, false)
	ctx := context.Background()
	snapshot, err := plan.captureParents(ctx, 0, genesisHashDisplay)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.refs) != 2 || snapshot.direct["middle"].Rows != 0 {
		t.Fatalf("complete empty parent was not preserved: %+v", snapshot)
	}
	recipeStoreMutex.Lock()
	err = plan.checkLocked(ctx, snapshot, 0, genesisHashDisplay)
	recipeStoreMutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	recipeStoreMutex.Lock()
	err = plan.checkLocked(ctx, snapshot, 1, genesisHashDisplay)
	recipeStoreMutex.Unlock()
	if !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatalf("snapshot accepted at wrong height: %v", err)
	}
	if err = parent.Reconcile(ctx, map[int64]string{0: genesisHashDisplay}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	recipeStoreMutex.Lock()
	err = plan.checkLocked(ctx, snapshot, 0, genesisHashDisplay)
	recipeStoreMutex.Unlock()
	if !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatalf("changed grandparent accepted: %v", err)
	}
	if _, err = plan.captureParents(ctx, 0, genesisHashDisplay); !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatalf("missing ancestor treated as empty: %v", err)
	}
	if _, found, err := middle.Read(ctx, 0); err == nil || found {
		t.Fatalf("dependent unit exposed generically: %v %v", found, err)
	}
}
func TestRecipeLineageRejectsCorruptionFenceAndIncompleteSnapshot(t *testing.T) {
	_, bad, _, _ := recipeLineageThreeFixture(t, true)
	ctx := context.Background()
	if _, err := bad.captureParents(ctx, 0, genesisHashDisplay); !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatalf("wrong transitive digest accepted: %v", err)
	}
	_, plan, parent, _ := recipeLineageThreeFixture(t, false)
	snapshot, err := plan.captureParents(ctx, 0, genesisHashDisplay)
	if err != nil {
		t.Fatal(err)
	}
	delete(snapshot.refs, recipeGraphIdentity{}) // unrelated deletion cannot fake completeness
	for id := range snapshot.refs {
		delete(snapshot.refs, id)
		break
	}
	recipeStoreMutex.Lock()
	err = plan.checkLocked(ctx, snapshot, 0, genesisHashDisplay)
	recipeStoreMutex.Unlock()
	if !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatalf("incomplete snapshot accepted: %v", err)
	}
	page, err := parent.Coverage(ctx, 0, 0, nil, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = parent.beginReconcileFence(ctx, page.Revision, 0, 0, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if _, err = plan.captureParents(ctx, 0, genesisHashDisplay); !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatalf("fenced ancestor accepted: %v", err)
	}
}

func TestRecipeLineageVerifiedChildReadFollowsTransitiveReceipts(t *testing.T) {
	_, plan, parent, _ := recipeLineageThreeFixture(t, false)
	ctx := context.Background()
	snapshot, err := plan.captureParents(ctx, 0, genesisHashDisplay)
	if err != nil {
		t.Fatal(err)
	}
	root := plan.nodes[plan.root]
	dep := root.definition.Dependencies[0]
	id := recipeGraphIdentity{dep.Instance, dep.Revision, dep.SemanticHash}
	ref := snapshot.refs[id]
	child := recipeStoreUnit{Schema: 1, Fingerprint: plan.root.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Rows: []recipeStoreRow{}, Lineage: []recipeDependencyReceipt{{Name: dep.Name, Instance: dep.Instance, SemanticHash: dep.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Unit: ref.unit}}}
	if err = root.store.publish(ctx, child, nil, recipeTestGuard, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	recipeStoreMutex.Lock()
	unit, found, err := plan.readChildLocked(ctx, snapshot, 0, genesisHashDisplay)
	recipeStoreMutex.Unlock()
	if err != nil || !found || unit.Rows == nil || len(unit.Rows) != 0 {
		t.Fatalf("verified empty child: %+v %v %v", unit, found, err)
	}
	if err = parent.Reconcile(ctx, map[int64]string{0: genesisHashDisplay}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	recipeStoreMutex.Lock()
	_, found, err = plan.readChildLocked(ctx, snapshot, 0, genesisHashDisplay)
	recipeStoreMutex.Unlock()
	if found || !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatalf("child exposed with stale transitive parent: %v %v", found, err)
	}
}

type recipeLineageCancelContext struct {
	context.Context
	calls, at int
	triggered bool
}

func (c *recipeLineageCancelContext) Err() error {
	c.calls++
	if c.calls >= c.at {
		c.triggered = true
		return context.Canceled
	}
	return nil
}
func TestRecipeLineagePreservesCancellationDuringRead(t *testing.T) {
	_, plan, _, _ := recipeLineageThreeFixture(t, false)
	snapshot, err := plan.captureParents(context.Background(), 0, genesisHashDisplay)
	if err != nil {
		t.Fatal(err)
	}
	for at := 1; at <= 12; at++ {
		ctx := &recipeLineageCancelContext{Context: context.Background(), at: at}
		_, err = plan.captureParents(ctx, 0, genesisHashDisplay)
		if ctx.triggered && !errors.Is(err, context.Canceled) {
			t.Fatalf("capture cancellation lost at %d: %v", at, err)
		}
		ctx = &recipeLineageCancelContext{Context: context.Background(), at: at}
		recipeStoreMutex.Lock()
		err = plan.checkLocked(ctx, snapshot, 0, genesisHashDisplay)
		recipeStoreMutex.Unlock()
		if ctx.triggered && !errors.Is(err, context.Canceled) {
			t.Fatalf("recheck cancellation lost at %d: %v", at, err)
		}
	}
}
