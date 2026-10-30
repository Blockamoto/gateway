package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecipeDAGPlanningSeparatesSelectedSavedAndUnknownCoverage(t *testing.T) {
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	first := recipeDAGExecutionFixtureRecipe(t, a, "plan-a", nil)
	middle := recipeDAGExecutionFixtureRecipe(t, a, "plan-b", &first)
	child := recipeDAGExecutionFixtureRecipe(t, a, "plan-c", &middle)
	roots := map[string]resolvedSavedRecipe{first.ID: first, child.ID: child}
	resolve := func(id string) (resolvedSavedRecipe, error) { return roots[id], nil }
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: child.ID, Recipe: true, Armed: true}, {Index: first.ID, Recipe: true, Armed: true}}}
	deps, err := a.recipePlanDependencies(ctx, child, req, 0, 1, resolve)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 2 {
		t.Fatalf("dependency graph: %+v", deps)
	}
	states := map[string]string{}
	for _, d := range deps {
		states[d.Index] = d.State
		if !strings.Contains(d.Reason, "same-block") {
			t.Fatalf("missing provenance: %+v", d)
		}
	}
	if states[middle.ID] != "saved_coverage_unknown" || states[first.ID] != "selected_coverage_unknown" {
		t.Fatalf("selected/saved coverage conflated: %+v", deps)
	}
}

func TestRecipeDAGPlanningRejectsParentOriginAfterRequestedStart(t *testing.T) {
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	parent := recipeDAGExecutionFixtureRecipe(t, a, "origin-a", nil)
	data, err := json.Marshal(parent.Definition)
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.Replace(string(data), `"origin":0`, `"origin":2`, 1)
	view, canonical, err := recipeRegistryCompile([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	recipeRegistryMu.Lock()
	view, err = recipeRegistrySave(ctx, a.dataDir, view, canonical, parent.SemanticHash, parent.Revision)
	recipeRegistryMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	parent, err = a.ResolveSavedRecipe(ctx, parent.ID, view.Revision, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	child := recipeDAGExecutionFixtureRecipe(t, a, "origin-b", &parent)
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: child.ID, Recipe: true, Armed: true}}}
	resolve := func(id string) (resolvedSavedRecipe, error) { return a.ResolveSavedRecipe(ctx, id, "", "") }
	if _, err = a.recipePlanDependencies(ctx, child, req, 0, 1, resolve); err == nil || !strings.Contains(err.Error(), "after requested start") {
		t.Fatalf("unavailable prefix admitted: %v", err)
	}
	if _, err = a.recipePlanDependencies(ctx, child, req, 2, 3, resolve); err != nil {
		t.Fatalf("valid origin blocked: %v", err)
	}
}

func TestRecipeTransactionDependencyRemainsExplicitlyBlocked(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	parent := recipeDAGExecutionFixtureRecipe(t, a, "txdep-parent", nil)
	data, err := os.ReadFile(filepath.Join("recipes", "tx-id-locator.json"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := parseRecipe(data)
	if err != nil {
		t.Fatal(err)
	}
	d.ID = "txdep-child"
	d.Dependencies = []recipeDependency{recipeGraphFixtureEdge("parent", parent)}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	saved := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(raw), "")))
	zero := int64(0)
	req := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: saved.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ready || len(plan.Consumers) != 1 || plan.Consumers[0].AdmissionState != "blocked" || !strings.Contains(plan.Consumers[0].Reason, "transaction") {
		t.Fatalf("transaction dependency not explicitly blocked: %+v", plan)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err == nil {
		t.Fatal("transaction dependency started")
	}
}
