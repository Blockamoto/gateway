package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRecipeProjectionParentBuiltinEmptyAndLaterAppend(t *testing.T) {
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	revision, semantic, err := recipeNativeUTXORevision()
	if err != nil {
		t.Fatal(err)
	}
	dep := recipeDependency{Name: "source", Scope: "builtin", Instance: "utxo-state", Revision: revision, SemanticHash: semantic, Export: "coins", Coverage: "snapshot_delta", Type: "coin"}
	if _, err = a.resolveRecipeProjectionParent(ctx, dep); err == nil {
		t.Fatal("unbuilt parent became an empty checkpoint")
	}
	if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", "utxo-state")); !os.IsNotExist(err) {
		t.Fatal("resolving parent created state", err)
	}
	u, err := openUTXOStoreContext(ctx, a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	blocks := recipeCoordinatorSource(t)
	utxoApply(t, u, blocks[0])
	parent, err := a.resolveRecipeProjectionParent(ctx, dep)
	if err != nil || parent.point.Checkpoint.Height != 0 || parent.point.Checkpoint.LiveOutputs != 0 {
		t.Fatal("processed empty genesis unavailable", parent, err)
	}
	utxoApply(t, u, blocks[1])
	recipeRegistryMu.Lock()
	err = parent.checkLocked(ctx, a)
	recipeRegistryMu.Unlock()
	if err != nil {
		t.Fatal("later append invalidated historical checkpoint", err)
	}
	current, err := a.resolveRecipeProjectionParent(ctx, dep)
	if err != nil {
		t.Fatal(err)
	}
	if err = current.atHeight(ctx, 0); err != nil || current.point.Checkpoint.Commitment != parent.point.Checkpoint.Commitment {
		t.Fatal("historical source selection rewrote state", err)
	}
	wrong := dep
	wrong.SemanticHash = recipeTestHash("a")
	if _, err = a.resolveRecipeProjectionParent(ctx, wrong); err == nil {
		t.Fatal("wrong builtin generation accepted")
	}
	if err = os.Remove(filepath.Join(a.dataDir, "indexes", "utxo-state", "head.json")); err != nil {
		t.Fatal(err)
	}
	recipeRegistryMu.Lock()
	err = parent.checkLocked(ctx, a)
	recipeRegistryMu.Unlock()
	if err == nil {
		t.Fatal("lost source head became valid historical evidence")
	}
}

func TestRecipeProjectionParentSavedScopeAndNoHiddenPrefix(t *testing.T) {
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	view := saveOrderedRecipeForRun(t, a, "projection-coins", false)
	dep := recipeDependency{Name: "source", Scope: "saved", Instance: view.Recipe.ID, Revision: view.Revision, SemanticHash: view.SemanticHash, Export: "coins", Coverage: "snapshot_delta", Type: "coin"}
	program, err := compileRecipe(view.Recipe)
	if err != nil {
		t.Fatal(err)
	}
	g, err := openRecipeOrderedGeneration(a.dataDir, program)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.resolveRecipeProjectionParent(ctx, dep); err == nil {
		t.Fatal("missing saved prefix became a checkpoint")
	}
	if _, err = os.Stat(g.profileRoot); !os.IsNotExist(err) {
		t.Fatal("parent resolution created a private generation", err)
	}
	if err = g.create(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = a.resolveRecipeProjectionParent(ctx, dep); err == nil {
		t.Fatal("unprocessed origin became evaluated genesis")
	}
	blocks := recipeCoordinatorSource(t)
	if err = g.apply(ctx, blocks[0], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	parent, err := a.resolveRecipeProjectionParent(ctx, dep)
	if err != nil {
		t.Fatal(err)
	}
	if parent.root == a.dataDir || parent.point.Dependency.Scope != "saved" {
		t.Fatal("saved generation aliased native data")
	}
	if err = g.apply(ctx, blocks[1], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	recipeRegistryMu.Lock()
	err = parent.checkLocked(ctx, a)
	recipeRegistryMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	wrong := dep
	wrong.Scope = "builtin"
	if _, err = a.resolveRecipeProjectionParent(ctx, wrong); err == nil {
		t.Fatal("saved identity used a builtin namespace")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = a.resolveRecipeProjectionParent(canceled, dep); err == nil {
		t.Fatal("canceled source resolution succeeded")
	}
}
