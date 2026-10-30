package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecipeDAGSchedulerOrdersSelectedParentsAtSameBlock(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	first := recipeDAGExecutionFixtureRecipe(t, a, "sched-a", nil)
	middle := recipeDAGExecutionFixtureRecipe(t, a, "sched-b", &first)
	child := recipeDAGExecutionFixtureRecipe(t, a, "sched-c", &middle)
	zero := int64(0)
	req := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: child.ID, Armed: true}, {Index: middle.ID, Armed: true}, {Index: first.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ready {
		t.Fatal("public dependency gate unexpectedly open")
	}
	source := recipeCoordinatorSource(t)
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != 0 {
			return blockView{}, fmt.Errorf("unexpected height")
		}
		return source[0], nil
	}
	installRecipeCoordinatorFixture(t, a, plan)
	launchRecipeCoordinatorFixture(t, a)
	done := waitRun(t, a)
	if done.Run.State != "complete" {
		t.Fatalf("DAG run: %+v", done.Run)
	}
	for _, node := range []resolvedSavedRecipe{middle, child} {
		store, e := openRecipeStore(a.dataDir, node.ID, node.SemanticHash)
		if e != nil {
			t.Fatal(e)
		}
		unit, found, e := a.recipeReadDAGUnit(ctx, node, store, 0, genesisHashDisplay, recipeTestGuard)
		if e != nil || !found || unit.Rows == nil || len(unit.Lineage) != 1 {
			t.Fatalf("missing ordered receipt %s: %+v %v %v", node.ID, unit, found, e)
		}
	}
	if done.Run.Counts.InscriptionExtractions != 1 {
		t.Fatalf("cohort did not share extraction: %+v", done.Run.Counts)
	}
	firstStore, err := openRecipeStore(a.dataDir, first.ID, first.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	middleStore, err := openRecipeStore(a.dataDir, middle.ID, middle.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	childStore, err := openRecipeStore(a.dataDir, child.ID, child.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	recipeStoreMutex.Lock()
	_, firstRef, firstFound, e1 := recipeDependencyReadLocked(ctx, firstStore, 0)
	_, oldChild, childFound, e2 := recipeDependencyReadLocked(ctx, childStore, 0)
	recipeStoreMutex.Unlock()
	if e1 != nil || e2 != nil || !firstFound || !childFound {
		t.Fatalf("missing saved units: %v %v", e1, e2)
	}
	lineage, err := a.prepareRecipeLineage(ctx, child)
	if err != nil {
		t.Fatal(err)
	}
	// Inject a different durable parent unit to exercise lineage invalidation;
	// normal deterministic execution cannot change this same-anchor result.
	recipeDAGMakeChildStale(t, lineage, middleStore, firstRef)
	deadline := time.Now().Add(10 * time.Second)
	for {
		a.indexMu.Lock()
		active := a.indexCancel != nil
		a.indexMu.Unlock()
		if !active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first writer did not exit")
		}
		time.Sleep(10 * time.Millisecond)
	}
	installRecipeCoordinatorFixture(t, a, plan)
	launchRecipeCoordinatorFixture(t, a)
	second := waitRun(t, a)
	if second.Run.State != "complete" {
		t.Fatalf("DAG lineage reorg did not recover: %+v", second.Run)
	}
	_, found, err := a.recipeReadDAGUnit(ctx, child, childStore, 0, genesisHashDisplay, recipeTestGuard)
	if err != nil || !found {
		t.Fatalf("reconciled child unavailable: %v %v", found, err)
	}
	if _, err = os.Stat(filepath.Join(childStore.dir, "units", oldChild.Unit+".json")); err != nil {
		t.Fatalf("old child unit lost: %v", err)
	}
}
func TestRecipeDAGSchedulerUsesSavedParentsWithoutStartingThem(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	first := recipeDAGExecutionFixtureRecipe(t, a, "saved-a", nil)
	middle := recipeDAGExecutionFixtureRecipe(t, a, "saved-b", &first)
	child := recipeDAGExecutionFixtureRecipe(t, a, "saved-c", &middle)
	firstRef, firstStore := recipeLineagePublishFixture(t, a, first, nil)
	middleRef, middleStore := recipeLineagePublishFixture(t, a, middle, []recipeDependencyReceipt{{Name: "input", Instance: first.ID, SemanticHash: first.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Unit: firstRef.Unit}})
	_ = middleRef
	beforeFirst, err := firstStore.Coverage(ctx, 0, 0, nil, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	recipeStoreMutex.Lock()
	beforeMiddle, err := middleStore.head()
	recipeStoreMutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	req := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: child.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	source := recipeCoordinatorSource(t)
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) { return source[0], nil }
	installRecipeCoordinatorFixture(t, a, plan)
	launchRecipeCoordinatorFixture(t, a)
	done := waitRun(t, a)
	if done.Run.State != "complete" {
		t.Fatalf("saved-parent run: %+v", done.Run)
	}
	afterFirst, err := firstStore.Coverage(ctx, 0, 0, nil, 0, 1)
	if err != nil || afterFirst.Revision != beforeFirst.Revision {
		t.Fatalf("saved parent was implicitly written: %+v %v", afterFirst, err)
	}
	recipeStoreMutex.Lock()
	afterMiddle, err := middleStore.head()
	recipeStoreMutex.Unlock()
	if err != nil || afterMiddle.Revision != beforeMiddle.Revision {
		t.Fatalf("saved middle was implicitly written: %+v %v", afterMiddle, err)
	}
	store, err := openRecipeStore(a.dataDir, child.ID, child.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := a.recipeReadDAGUnit(ctx, child, store, 0, genesisHashDisplay, recipeTestGuard)
	if err != nil || !found {
		t.Fatalf("child did not use saved graph: %v %v", found, err)
	}
}
func TestRecipeDAGSchedulerWaitsForMissingSavedParent(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	parent := recipeDAGExecutionFixtureRecipe(t, a, "missing-a", nil)
	child := recipeDAGExecutionFixtureRecipe(t, a, "missing-b", &parent)
	zero := int64(0)
	req := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: child.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	source := recipeCoordinatorSource(t)
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) { return source[0], nil }
	installRecipeCoordinatorFixture(t, a, plan)
	launchRecipeCoordinatorFixture(t, a)
	var waiting indexRunDocument
	deadline := time.Now().Add(15 * time.Second)
	for {
		waiting = a.indexRunSnapshot()
		if waiting.Run != nil && len(waiting.Run.Consumers) == 1 && waiting.Run.Consumers[0].State == "waiting" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("missing parent did not wait: run=%+v", waiting.Run)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if waiting.Run.Consumers[0].Reason == "" {
		t.Fatal("missing prerequisite was not explained")
	}
	parentStore, err := openRecipeStore(a.dataDir, parent.ID, parent.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := parentStore.Read(ctx, 0); err != nil || found {
		t.Fatalf("missing saved parent was implicitly built: %v %v", found, err)
	}
	childStore, err := openRecipeStore(a.dataDir, child.ID, child.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := childStore.Read(ctx, 0); err != nil || found {
		t.Fatalf("child published without parent: %v %v", found, err)
	}
	// Another explicitly authorized producer supplies the missing receipt;
	// the waiting child must resume without silently building the parent.
	if err = parentStore.Publish(ctx, recipeStoreUnit{Schema: 1, Fingerprint: parent.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Rows: []recipeStoreRow{}}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" {
		t.Fatalf("saved-parent backfill did not resume: %+v", done.Run)
	}
	_, found, err := a.recipeReadDAGUnit(ctx, child, childStore, 0, genesisHashDisplay, recipeTestGuard)
	if err != nil || !found {
		t.Fatalf("child did not consume backfilled parent: %v %v", found, err)
	}
}

func TestRecipeDAGSchedulerMixedIndependentTransactionAndRevealGraph(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	parent := recipeDAGExecutionFixtureRecipe(t, a, "mixed-parent", nil)
	child := recipeDAGExecutionFixtureRecipe(t, a, "mixed-child", &parent)
	data, err := os.ReadFile(filepath.Join("recipes", "tx-id-locator.json"))
	if err != nil {
		t.Fatal(err)
	}
	def, err := parseRecipe(data)
	if err != nil {
		t.Fatal(err)
	}
	def.ID = "mixed-transaction"
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	saved := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(raw), "")))
	zero := int64(0)
	req := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: saved.Recipe.ID, Armed: true}, {Index: child.ID, Armed: true}, {Index: parent.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ready {
		t.Fatal("dependency gate unexpectedly open")
	}
	source := recipeCoordinatorSource(t)
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) { return source[0], nil }
	installRecipeCoordinatorFixture(t, a, plan)
	launchRecipeCoordinatorFixture(t, a)
	done := waitRun(t, a)
	if done.Run.State != "complete" {
		t.Fatalf("mixed DAG run: %+v", done.Run)
	}
	txStore, err := openRecipeStore(a.dataDir, saved.Recipe.ID, saved.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	txUnit, found, err := txStore.Read(ctx, 0)
	if err != nil || !found || len(txUnit.Rows) != 1 {
		t.Fatalf("transaction receipt: %+v %v %v", txUnit, found, err)
	}
	childStore, err := openRecipeStore(a.dataDir, child.ID, child.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	_, found, err = a.recipeReadDAGUnit(ctx, child, childStore, 0, genesisHashDisplay, recipeTestGuard)
	if err != nil || !found {
		t.Fatalf("dependent reveal receipt: %v %v", found, err)
	}
	if done.Run.Counts.InscriptionExtractions != 1 {
		t.Fatalf("mixed cohort inventory count: %+v", done.Run.Counts)
	}
}
