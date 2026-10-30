package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func savedRecipeRunFixture(t *testing.T) (*app, recipeRegistryView, indexRunRequest) {
	t.Helper()
	a := runTestApp(t)
	h := indexInterfaceHandler(a)
	view := registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(registryRecipe, "")))
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: "custom-one", Armed: true}}}
	return a, view, req
}
func TestSavedRecipeRunPlanBindsRevisionAndRequiresReview(t *testing.T) {
	ctx := context.Background()
	a, view, req := savedRecipeRunFixture(t)
	prepared, err := a.prepareIndexRun(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Prepared.Consumers) != 1 || !prepared.Prepared.Consumers[0].Recipe || prepared.Run != nil {
		t.Fatalf("bad prepared choice: %+v", prepared)
	}
	reopened, err := readIndexRunDocument(a.dataDir)
	if err != nil || !reopened.Prepared.Consumers[0].Recipe {
		t.Fatalf("prepared intent did not reopen: %+v %v", reopened, err)
	}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Ready || len(plan.Consumers) != 1 {
		t.Fatalf("finite custom plan not ready: %+v", plan)
	}
	q := plan.Consumers[0]
	if q.State != "ready" || q.AdmissionState != "admitted" || q.Recipe == nil || q.Recipe.Revision != view.Revision || q.Recipe.SemanticHash != view.SemanticHash || q.RecipeRevision != view.Revision || q.RecipeSemanticHash != view.SemanticHash || q.ProfileHash != recipeAdmissionProfile(q.Index, view.SemanticHash, view.Revision) {
		t.Fatalf("missing saved recipe binding: %+v", q)
	}
	bindingJSON, err := json.Marshal(q.Recipe)
	if err != nil || strings.Contains(string(bindingJSON), `"request"`) {
		t.Fatalf("binding duplicates run request: %s %v", bindingJSON, err)
	}
	if _, err = a.startIndexRun(ctx, req, ""); err == nil {
		t.Fatal("unreviewed custom run started")
	}
	mixed := req
	mixed.Consumers = append(append([]indexRunSelection{}, req.Consumers...), indexRunSelection{Index: "tx-locator", Armed: true})
	mixedPlan, err := a.planIndexRun(ctx, mixed)
	if err != nil || !mixedPlan.Ready {
		t.Fatalf("mixed finite plan not ready: %+v %v", mixedPlan, err)
	}
	if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", "recipes-v1")); !os.IsNotExist(err) {
		t.Fatalf("planning created index output: %v", err)
	}
	h := indexInterfaceHandler(a)
	edited := strings.Replace(registryRecipe, `"name":"Custom"`, `"name":"Renamed"`, 1)
	changed := registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(edited, "", view.Revision)))
	later, err := a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if later.PlanID == plan.PlanID || later.Consumers[0].RecipeRevision != changed.Revision || changed.SemanticHash != view.SemanticHash {
		t.Fatal("saved revision edit did not invalidate plan identity")
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err == nil {
		t.Fatal("stale custom plan started")
	}
}
func TestSavedRecipeRunBindingPersistsAndDetectsCorruption(t *testing.T) {
	ctx := context.Background()
	a, _, req := savedRecipeRunFixture(t)
	plan, err := a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	record := &indexRunRecord{ID: "saved-recipe-test", State: "paused", Request: plan.Request, TargetHeight: plan.TargetHeight, InitialTargetHeight: plan.TargetHeight, TargetHash: plan.TargetHash, Consumers: plan.Consumers}
	doc := indexRunDocument{Schema: 1, Configured: true, Prepared: plan.Request, Run: record}
	doc.Checksum = indexRunDigest(doc)
	if err = atomicWriteJSON(indexRunPath(a.dataDir), doc); err != nil {
		t.Fatal(err)
	}
	read, err := readIndexRunDocument(a.dataDir)
	if err != nil || read.Run.Consumers[0].Recipe == nil || read.Run.Consumers[0].Recipe.Revision != plan.Consumers[0].Recipe.Revision {
		t.Fatalf("run binding lost: %+v %v", read, err)
	}
	// Recomputing the outer checksum is not enough to hide an inconsistent binding.
	doc.Run.Consumers[0].Recipe.Origin++
	doc.Checksum = indexRunDigest(doc)
	if err = atomicWriteJSON(indexRunPath(a.dataDir), doc); err != nil {
		t.Fatal(err)
	}
	if _, err = readIndexRunDocument(a.dataDir); err == nil {
		t.Fatal("inconsistent persisted recipe binding accepted")
	}
}
func TestSavedRecipeRunResolutionIsProfileScoped(t *testing.T) {
	a, _, req := savedRecipeRunFixture(t)
	if _, err := a.planIndexRun(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	other := runTestApp(t)
	if _, err := other.planIndexRun(context.Background(), req); err == nil {
		t.Fatal("recipe from another profile was resolved")
	}
	unknown := indexRunRequest{Consumers: []indexRunSelection{{Index: "missing-recipe", Armed: true}}}
	if _, err := a.prepareIndexRun(unknown); err == nil {
		t.Fatal("unknown recipe admitted")
	}
	fake := indexRunRequest{Consumers: []indexRunSelection{{Index: "blocks", Recipe: true, Armed: true}}}
	if _, err := a.prepareIndexRun(fake); err == nil {
		t.Fatal("built-in identity masquerades as recipe")
	}
}

func TestSavedRecipeRunAllowsOffIntentWhenDefinitionUnavailable(t *testing.T) {
	a, _, req := savedRecipeRunFixture(t)
	prepared, err := a.prepareIndexRun(req)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := recipeRegistryDir(a.dataDir, "custom-one")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(dir, "head.json")); err != nil {
		t.Fatal(err)
	}
	req = prepared.Prepared
	req.Consumers[0].Armed = false
	if _, err = a.prepareIndexRun(req); err != nil {
		t.Fatalf("cannot turn off unavailable recipe: %v", err)
	}
	if _, err = readIndexRunDocument(a.dataDir); err != nil {
		t.Fatalf("off intent cannot reopen: %v", err)
	}
	req.Consumers[0].Armed = true
	if _, err = a.planIndexRun(context.Background(), req); err == nil {
		t.Fatal("unavailable recipe accepted for planning")
	}
}

func TestIndexRunPersistenceRejectsOversizedDocumentBeforeWrite(t *testing.T) {
	a, _, req := savedRecipeRunFixture(t)
	if _, err := a.prepareIndexRun(req); err != nil {
		t.Fatal(err)
	}
	path := indexRunPath(a.dataDir)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	a.indexRunMu.Lock()
	oldRevision, oldChecksum := a.indexRunState.Revision, a.indexRunState.Checksum
	a.indexRunState.Error = strings.Repeat("x", indexRunMaxDocumentBytes)
	err = a.persistIndexRunLocked()
	if a.indexRunState.Revision != oldRevision || a.indexRunState.Checksum != oldChecksum {
		a.indexRunMu.Unlock()
		t.Fatal("failed save advanced in-memory revision or checksum")
	}
	a.indexRunMu.Unlock()
	if err == nil {
		t.Fatal("oversized run document accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("oversized save replaced prior run: %v", err)
	}
}

func TestSavedRecipeRunBindingSurvivesUnrelatedAmendment(t *testing.T) {
	ctx := context.Background()
	a, _, req := savedRecipeRunFixture(t)
	plan, err := a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	run := &indexRunRecord{ID: "recipe-amend", State: "paused", Request: plan.Request, TargetHeight: plan.TargetHeight, InitialTargetHeight: plan.TargetHeight, TargetHash: plan.TargetHash, Consumers: plan.Consumers}
	// Model a future unrelated cohort amendment; no custom execution is enabled.
	run.Request.Consumers = append(run.Request.Consumers, indexRunSelection{Index: "tx-locator", Armed: true, indexRunOptions: indexRunOptions{Mode: "lean"}})
	doc := indexRunDocument{Schema: 1, Configured: true, Prepared: run.Request, Run: run}
	doc.Checksum = indexRunDigest(doc)
	if err = atomicWriteJSON(indexRunPath(a.dataDir), doc); err != nil {
		t.Fatal(err)
	}
	if _, err = readIndexRunDocument(a.dataDir); err != nil {
		t.Fatalf("unrelated amendment invalidated recipe: %v", err)
	}
	for i := range run.Request.Consumers {
		if run.Request.Consumers[i].Index == "custom-one" {
			run.Request.Consumers[i].Live = true
		}
	}
	doc.Checksum = indexRunDigest(doc)
	if err = atomicWriteJSON(indexRunPath(a.dataDir), doc); err != nil {
		t.Fatal(err)
	}
	if _, err = readIndexRunDocument(a.dataDir); err == nil {
		t.Fatal("changed recipe selection accepted")
	}
	for i := range run.Request.Consumers {
		if run.Request.Consumers[i].Index == "custom-one" {
			run.Request.Consumers[i].Live = false
		}
	}
	run.Consumers[0].Recipe.Schema = 1
	doc.Checksum = indexRunDigest(doc)
	if err = atomicWriteJSON(indexRunPath(a.dataDir), doc); err != nil {
		t.Fatal(err)
	}
	if _, err = readIndexRunDocument(a.dataDir); err == nil {
		t.Fatal("earlier binding schema accepted")
	}
}

func TestSavedRecipeFinitePlanBlocksLiveAndDependencies(t *testing.T) {
	ctx := context.Background()
	a, view, req := savedRecipeRunFixture(t)
	req.Consumers[0].Live = true
	plan, err := a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ready || plan.Consumers[0].AdmissionState != "blocked" {
		t.Fatal("Live recipe admitted")
	}
	req.Consumers[0].Live = false
	req.Out = nil
	plan, err = a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ready || plan.Consumers[0].AdmissionState != "blocked" {
		t.Fatal("unbounded recipe admitted")
	}
	childJSON := fmt.Sprintf(`{"format":"gateway-recipe","version":1,"id":"child","name":"Child","source":"bitcoin.reveal","origin":0,"history":"sparse","dependencies":[{"name":"parent","instance":"custom-one","revision":%q,"semantic_hash":%q,"export":"kind","coverage":"same_block","type":"text"}],"tables":[{"name":"items","key":"ref"}],"steps":[{"op":"write","table":"items","row":{"ref":{"field":["ref"]}}}]}`, view.Revision, view.SemanticHash)
	registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(childJSON, "")))
	zero := int64(0)
	req.Out = &zero
	req.Consumers = []indexRunSelection{{Index: "child", Armed: true}}
	plan, err = a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ready || plan.Consumers[0].AdmissionState != "blocked" || !strings.Contains(plan.Consumers[0].Reason, "Dependency") {
		t.Fatalf("dependency admitted: %+v", plan)
	}
}
