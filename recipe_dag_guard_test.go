package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRecipeLocalDAGGuardChecksEveryRevisionAndAnchor(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	parent := recipeDAGExecutionFixtureRecipe(t, a, "dag-parent", nil)
	child := recipeDAGExecutionFixtureRecipe(t, a, "dag-child", &parent)
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: child.ID, Recipe: true, Armed: true}}}
	header, err := a.readSelectedHeader(1)
	if err != nil {
		t.Fatal(err)
	}
	digest := hash256(header)
	target := reverseHex(digest[:])
	plan, err := recipeAdmissionBuild(child, req, 1, target)
	if err != nil {
		t.Fatal(err)
	}
	progress := indexRunProgress{Index: child.ID, AdmissionState: "admitted", From: 0, ConfigurationRevision: 1, ProfileHash: recipeAdmissionProfile(child.ID, child.SemanticHash, child.Revision)}
	run := &indexRunRecord{ID: "dag-guard", State: "running", Request: req, InitialTargetHeight: 1, TargetHeight: 1, TargetHash: target, Consumers: []indexRunProgress{progress}}
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
	guard := a.recipeLocalDAGCoverageGuard(ctx, binding, plan, lineage)
	called := false
	if err = guard([]recipeCoverageAnchor{{Height: 0, BlockHash: genesisHashDisplay}}, func() error { called = true; return nil }); err != nil || !called {
		t.Fatalf("valid DAG guard: %v", err)
	}
	called = false
	if err = guard([]recipeCoverageAnchor{{Height: 0, BlockHash: recipeTestHash("a")}}, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("wrong selected anchor accepted")
	}
	a.indexRunMu.Lock()
	a.indexRunState.Run.State = "pausing"
	a.indexRunMu.Unlock()
	called = false
	if err = guard(nil, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("paused run accepted")
	}
	a.indexRunMu.Lock()
	a.indexRunState.Run.State = "running"
	a.indexRunMu.Unlock()
	dir, err := recipeRegistryDir(a.dataDir, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "revisions", parent.Revision+".json"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	called = false
	if err = guard(nil, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("corrupt saved parent accepted")
	}
}
