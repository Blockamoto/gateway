package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func recipeDAGInspectFixture(t *testing.T) (*app, recipeAdmissionPlan, recipeRunBinding, *recipeLineagePlan, *recipeStore, *recipeStore, *recipeStore, recipeStoreRef) {
	t.Helper()
	ctx := context.Background()
	a := runTestApp(t)
	first := recipeDAGExecutionFixtureRecipe(t, a, "inspect-a", nil)
	middle := recipeDAGExecutionFixtureRecipe(t, a, "inspect-b", &first)
	child := recipeDAGExecutionFixtureRecipe(t, a, "inspect-c", &middle)
	firstRef, firstStore := recipeLineagePublishFixture(t, a, first, nil)
	middleReceipt := recipeDependencyReceipt{Name: "input", Instance: first.ID, SemanticHash: first.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Unit: firstRef.Unit}
	middleRef, middleStore := recipeLineagePublishFixture(t, a, middle, []recipeDependencyReceipt{middleReceipt})
	childReceipt := recipeDependencyReceipt{Name: "input", Instance: middle.ID, SemanticHash: middle.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Unit: middleRef.Unit}
	_, childStore := recipeLineagePublishFixture(t, a, child, []recipeDependencyReceipt{childReceipt})
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: child.ID, Recipe: true, Armed: true}}}
	h, err := a.readSelectedHeader(1)
	if err != nil {
		t.Fatal(err)
	}
	digest := hash256(h)
	plan, err := recipeAdmissionBuild(child, req, 1, reverseHex(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	progress := indexRunProgress{Index: child.ID, AdmissionState: "admitted", From: 0, ConfigurationRevision: 1, ProfileHash: recipeAdmissionProfile(child.ID, child.SemanticHash, child.Revision)}
	run := &indexRunRecord{ID: "dag-inspect", State: "running", Request: req, InitialTargetHeight: 1, TargetHeight: 1, TargetHash: plan.TargetHash, Consumers: []indexRunProgress{progress}}
	a.indexRunMu.Lock()
	a.indexRunLoaded = true
	a.indexRunState = indexRunDocument{Schema: 1, Configured: true, Run: run}
	a.indexRunMu.Unlock()
	binding, err := recipeBindRun(run, child.ID, child.SemanticHash, child.Revision)
	if err != nil {
		t.Fatal(err)
	}
	lineage, err := a.prepareRecipeLineage(ctx, child)
	if err != nil {
		t.Fatal(err)
	}
	return a, plan, binding, lineage, firstStore, middleStore, childStore, firstRef
}
func TestRecipeDAGInspectSeparatesUnknownFromKnownStale(t *testing.T) {
	ctx := context.Background()
	a, plan, binding, lineage, first, middle, child, firstRef := recipeDAGInspectFixture(t)
	page, err := a.recipeLocalDAGInspectPage(ctx, binding, plan, lineage, child, nil, 0, false)
	if err != nil || len(page.Anchors) != 0 {
		t.Fatalf("valid coverage: %+v %v", page, err)
	}
	if err = first.Reconcile(ctx, map[int64]string{0: genesisHashDisplay}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	unknown, err := a.recipeLocalDAGInspectPage(ctx, binding, plan, lineage, child, nil, 0, false)
	if !errors.Is(err, errRecipeDependencyUnavailable) || len(unknown.Anchors) != 0 {
		t.Fatalf("missing ancestor was treated as stale/absent: %+v %v", unknown, err)
	}
	if err = first.Publish(ctx, recipeStoreUnit{Schema: 1, Fingerprint: first.fingerprint, Height: 0, BlockHash: genesisHashDisplay, Rows: []recipeStoreRow{}}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if err = middle.Reconcile(ctx, map[int64]string{0: genesisHashDisplay}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	root := lineage.nodes[lineage.root]
	dep := root.definition.Dependencies[0]
	node := lineage.nodes[recipeGraphIdentity{dep.Instance, dep.Revision, dep.SemanticHash}]
	up := node.definition.Dependencies[0]
	replacement := recipeStoreUnit{Schema: 1, Fingerprint: middle.fingerprint, Height: 0, BlockHash: genesisHashDisplay, Rows: []recipeStoreRow{{Key: `["items","x"]`, Value: json.RawMessage(`{"ref":"x"}`)}}, Lineage: []recipeDependencyReceipt{{Name: up.Name, Instance: up.Instance, SemanticHash: up.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Unit: firstRef.Unit}}}
	if err = middle.publish(ctx, replacement, nil, recipeTestGuard, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	stale, err := a.recipeLocalDAGInspectPage(ctx, binding, plan, lineage, child, nil, 0, false)
	if err != nil || len(stale.Anchors) != 1 || stale.Anchors[0].Height != 0 {
		t.Fatalf("known changed parent not classified stale: %+v %v", stale, err)
	}
	work := recipeConsumerWork{PlanID: plan.PlanID, Height: plan.TargetHeight, ChainSnapshot: recipeLocalSnapshotToken(plan.TargetHeight, plan.TargetHash)}
	guard := a.recipeLocalDAGExecutionGuard(ctx, binding, plan, work, blockView{Height: plan.TargetHeight, Hash: plan.TargetHash}, lineage)
	if err = child.beginReconcileFence(ctx, stale.Revision, 0, 1, guard); err != nil {
		t.Fatal(err)
	}
	fenced, err := a.recipeLocalDAGInspectPage(ctx, binding, plan, lineage, child, nil, stale.Revision, true)
	if err != nil || len(fenced.Anchors) != 1 {
		t.Fatalf("fenced stale inspection: %+v %v", fenced, err)
	}
	if _, _, err = child.Read(ctx, 0); !errors.Is(err, errRecipeReconcileFenced) {
		t.Fatalf("journal exposed child: %v", err)
	}
}

func TestRecipeDAGInspectUsesSelectedAnchorBeforeUnknownParent(t *testing.T) {
	ctx := context.Background()
	a, plan, binding, lineage, first, _, child, _ := recipeDAGInspectFixture(t)
	if err := first.Reconcile(ctx, map[int64]string{0: genesisHashDisplay}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	// Synthetic in-place header replacement exercises the authority fence;
	// it does not represent a validated Bitcoin header chain.
	replacement, err := a.readSelectedHeader(1)
	if err != nil {
		t.Fatal(err)
	}
	a.headerChainMu.Lock()
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err == nil {
		_, err = f.WriteAt(replacement, 0)
		_ = f.Close()
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	stale, err := a.recipeLocalDAGInspectPage(ctx, binding, plan, lineage, child, nil, 0, false)
	if err != nil || len(stale.Anchors) != 1 || stale.Anchors[0].Height != 0 {
		t.Fatalf("changed selected anchor not proven stale: %+v %v", stale, err)
	}
}

func TestRecipeDAGCoverageRejectsPriorPageAncestorChange(t *testing.T) {
	ctx := context.Background()
	a, plan, binding, lineage, first, middle, child, _ := recipeDAGInspectFixture(t)
	header, err := a.readSelectedHeader(1)
	if err != nil {
		t.Fatal(err)
	}
	digest := hash256(header)
	hash := reverseHex(digest[:])
	firstUnit := recipeStoreUnit{Schema: 1, Fingerprint: first.fingerprint, Height: 1, BlockHash: hash, Rows: []recipeStoreRow{}}
	firstBytes, _ := json.Marshal(firstUnit)
	if err = first.Publish(ctx, firstUnit, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	root := lineage.nodes[lineage.root]
	dep := root.definition.Dependencies[0]
	middleID := recipeGraphIdentity{dep.Instance, dep.Revision, dep.SemanticHash}
	up := lineage.nodes[middleID].definition.Dependencies[0]
	middleUnit := recipeStoreUnit{Schema: 1, Fingerprint: middle.fingerprint, Height: 1, BlockHash: hash, Rows: []recipeStoreRow{}, Lineage: []recipeDependencyReceipt{{Name: up.Name, Instance: up.Instance, SemanticHash: up.SemanticHash, Height: 1, BlockHash: hash, Unit: recipeStoreHash(firstBytes)}}}
	middleBytes, _ := json.Marshal(middleUnit)
	if err = middle.publish(ctx, middleUnit, nil, recipeTestGuard, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	childUnit := recipeStoreUnit{Schema: 1, Fingerprint: child.fingerprint, Height: 1, BlockHash: hash, Rows: []recipeStoreRow{}, Lineage: []recipeDependencyReceipt{{Name: dep.Name, Instance: dep.Instance, SemanticHash: dep.SemanticHash, Height: 1, BlockHash: hash, Unit: recipeStoreHash(middleBytes)}}}
	if err = child.publish(ctx, childUnit, nil, recipeTestGuard, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	resolved, err := a.ResolveSavedRecipe(ctx, plan.Index, plan.Revision, plan.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	guard := a.recipeLocalDAGCoverageGuard(ctx, binding, plan, lineage)
	page, err := a.recipeDAGCoverage(ctx, resolved, child, 0, 1, nil, 0, 1, "", guard)
	if err != nil || page.Next == nil || !recipeStoreHashValid(page.LineageToken) {
		t.Fatalf("first page: %+v %v", page, err)
	}
	if _, err = a.recipeDAGCoverage(ctx, resolved, child, 0, 1, page.Next, page.Revision, 1, "", guard); err == nil {
		t.Fatal("continuation without lineage token accepted")
	}
	if err = first.Reconcile(ctx, map[int64]string{0: genesisHashDisplay}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	next, err := a.recipeDAGCoverage(ctx, resolved, child, 0, 1, page.Next, page.Revision, 1, page.LineageToken, guard)
	if err == nil || len(next.Anchors) != 0 {
		t.Fatalf("changed prior-page ancestor accepted: %+v %v", next, err)
	}
}
