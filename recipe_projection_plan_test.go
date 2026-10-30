package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRecipeProjectionPlanningIsPureAndSeparatesCheckpointSources(t *testing.T) {
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	g := projectionTestGeneration(t, a.dataDir, "planned-addresses", false)
	resolved := projectionTestSave(t, a, g.program.definition)
	one := int64(1)
	choice := indexRunSelection{Index: resolved.ID, Recipe: true, Armed: true}
	req := indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{choice}}
	plan := func(from, to int64, c indexRunSelection, request indexRunRequest) indexRunProgress {
		t.Helper()
		q := indexRunProgress{Index: resolved.ID, State: "ready", AdmissionState: "admitted", Height: -1, From: from, To: to}
		if err := a.planProjectionRecipeState(ctx, resolved, c, request, &q); err != nil {
			t.Fatal(err)
		}
		return q
	}
	q := plan(1, 1, choice, req)
	if q.AdmissionState != "admitted" || q.RangeMode != "checkpoint_projection" || q.RequiresGenesis || !validHash(q.RecipeStateToken) || q.NextRequiredHeight == nil || *q.NextRequiredHeight != 1 {
		t.Fatal(q)
	}
	if _, err := os.Stat(g.profileRoot); !os.IsNotExist(err) {
		t.Fatal("planning created child state", err)
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", "utxo-state")); !os.IsNotExist(err) {
		t.Fatal("planning created parent state", err)
	}
	resolve := func(string) (resolvedSavedRecipe, error) { return resolved, nil }
	deps, err := a.recipePlanDependencies(ctx, resolved, req, 1, 1, resolve)
	if err != nil || len(deps) != 1 || deps[0].Kind != "snapshot_delta" || deps[0].State != "saved_checkpoint_unknown" {
		t.Fatal(deps, err)
	}
	req.Consumers = append(req.Consumers, indexRunSelection{Index: "utxo-state", Armed: true})
	deps, err = a.recipePlanDependencies(ctx, resolved, req, 1, 1, resolve)
	if err != nil || deps[0].State != "selected_checkpoint_unknown" {
		t.Fatal(deps, err)
	}
	q = plan(2, 1, choice, req)
	if q.AdmissionState != "admitted" || q.NextRequiredHeight == nil || *q.NextRequiredHeight != 2 {
		t.Fatal("future In caused hidden prefix work", q)
	}
}

func TestRecipeProjectionPlanningPreservesCurrentViewAndRequiresParentConsent(t *testing.T) {
	ctx := context.Background()
	u, b0, b1 := address076Source(t, []uint64{7}, []string{address076P2PKH})
	a := &app{dataDir: u.root}
	g := projectionTestGeneration(t, u.root, "planned-existing-addresses", false)
	resolved := projectionTestSave(t, a, g.program.definition)
	canonical := func(h int64) (string, error) {
		if h == 0 {
			return b0.Hash, nil
		}
		return b1.Hash, nil
	}
	if err := g.applySnapshot(ctx, a, 1, "", canonical, projectionGenerationFixtureGuard, nil); err != nil {
		t.Fatal(err)
	}
	choice := indexRunSelection{Index: resolved.ID, Recipe: true, Armed: true}
	q := indexRunProgress{Index: resolved.ID, State: "ready", AdmissionState: "admitted", From: 0, To: 0}
	if err := a.planProjectionRecipeState(ctx, resolved, choice, indexRunRequest{}, &q); err != nil || q.AdmissionState != "blocked" || q.State != "rebuild_required" {
		t.Fatal("unretained earlier view was silently admitted", q, err)
	}
	choice.Rebuild = true
	// A valid fixture head with no retained transition before height one.
	limited := *u.head
	limited.RecoveryBoundaryHeight, limited.RecoveryBoundaryHash, limited.Recovery = 1, b1.Hash, []utxoRecoveryPoint{}
	limited.Checksum = utxoHeadDigest(limited)
	if err := atomicWriteJSON(filepath.Join(u.dir, "head.json"), limited); err != nil {
		t.Fatal(err)
	}
	q = indexRunProgress{Index: resolved.ID, State: "ready", AdmissionState: "admitted", From: 0, To: 0}
	req := indexRunRequest{Consumers: []indexRunSelection{choice}}
	if err := a.planProjectionRecipeState(ctx, resolved, choice, req, &q); err != nil || q.AdmissionState != "blocked" || q.State != "needs_prerequisite" {
		t.Fatal("child rebuild granted missing parent recovery", q, err)
	}
	req.Consumers = append(req.Consumers, indexRunSelection{Index: "utxo-state", Armed: true, indexRunOptions: indexRunOptions{Rebuild: true}})
	q = indexRunProgress{Index: resolved.ID, State: "ready", AdmissionState: "admitted", From: 0, To: 0}
	if err := a.planProjectionRecipeState(ctx, resolved, choice, req, &q); err != nil || q.AdmissionState != "admitted" {
		t.Fatal("explicit parent rebuild was ignored", q, err)
	}
	_, cp := projectionTestLatest(t, g)
	if cp.Height != 1 {
		t.Fatal("review rewound the current view", cp)
	}
}
