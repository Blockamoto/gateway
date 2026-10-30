package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func recipeDAGExecutionFixtureRecipe(t *testing.T, a *app, id string, parent *resolvedSavedRecipe) resolvedSavedRecipe {
	t.Helper()
	deps := []recipeDependency{}
	steps := ""
	if parent != nil {
		deps = append(deps, recipeGraphFixtureEdge("input", *parent))
		steps = `{"op":"let","as":"found","value":{"call":"dependency.lookup","args":[{"literal":"input"},{"field":["ref"]}]}},{"op":"require","test":{"call":"valid","args":[{"variable":"found"}]}},`
	}
	encoded, err := json.Marshal(deps)
	if err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf(`{"format":"gateway-recipe","version":1,"id":%q,"name":%q,"source":"bitcoin.reveal","origin":0,"history":"sparse","dependencies":%s,"exports":[{"name":"kind","kind":"same_block","table":"items","column":"ref","type":"text"}],"tables":[{"name":"items","key":"ref"}],"steps":[%s{"op":"write","table":"items","row":{"ref":{"field":["ref"]}}}]}`, id, id, encoded, steps)
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
	resolved, err := a.ResolveSavedRecipe(context.Background(), id, view.Revision, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
func TestRecipeDAGExecutorRealBlockTransitiveAndAtomic(t *testing.T) {
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	block, inventory := recipeDependencyReferenceBlock(t)
	first := recipeDAGExecutionFixtureRecipe(t, a, "dag-a", nil)
	middle := recipeDAGExecutionFixtureRecipe(t, a, "dag-b", &first)
	child := recipeDAGExecutionFixtureRecipe(t, a, "dag-c", &middle)
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
	if err = a.recipeExecuteDAGBlock(ctx, middle, middleStore, block, inventory, 0, recipeTestGuard); !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatalf("missing parent not unknown: %v", err)
	}
	program, err := compileRecipe(first.Definition)
	if err != nil {
		t.Fatal(err)
	}
	if err = program.commitInscriptionBlock(ctx, firstStore, block, inventory, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if err = a.recipeExecuteDAGBlock(ctx, middle, middleStore, block, inventory, 0, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if err = a.recipeExecuteDAGBlock(ctx, child, childStore, block, inventory, 0, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	unit, found, err := a.recipeReadDAGUnit(ctx, child, childStore, block.Height, block.Hash, recipeTestGuard)
	if err != nil || !found || len(unit.Rows) == 0 || len(unit.Lineage) != 1 {
		t.Fatalf("DAG result: rows=%d lineage=%d found=%v err=%v", len(unit.Rows), len(unit.Lineage), found, err)
	}
	if _, _, err = childStore.Read(ctx, block.Height); err == nil {
		t.Fatal("generic child read bypassed lineage")
	}
	coverageGuard := func(anchors []recipeCoverageAnchor, commit func() error) error {
		for _, anchor := range anchors {
			if anchor.Height != block.Height || anchor.BlockHash != block.Hash {
				return fmt.Errorf("unexpected selected anchor")
			}
		}
		return commit()
	}
	coverage, err := a.recipeDAGCoverage(ctx, child, childStore, block.Height, block.Height, nil, 0, 1, "", coverageGuard)
	if err != nil || len(coverage.Anchors) != 1 {
		t.Fatalf("verified DAG coverage: %+v %v", coverage, err)
	}
	if _, err = a.recipeDAGCoverage(ctx, child, childStore, block.Height, block.Height, nil, 0, 1, "", func([]recipeCoverageAnchor, func() error) error { return fmt.Errorf("authority denied") }); err == nil {
		t.Fatal("denied coverage authority accepted")
	}
	raceGuard := func(_ []recipeCoverageAnchor, commit func() error) error {
		if e := firstStore.Reconcile(ctx, map[int64]string{block.Height: block.Hash}, recipeTestGuard); e != nil {
			return e
		}
		return commit()
	}
	raced, err := a.recipeDAGCoverage(ctx, child, childStore, block.Height, block.Height, nil, 0, 1, "", raceGuard)
	if !errors.Is(err, errRecipeDependencyUnavailable) || len(raced.Anchors) != 0 {
		t.Fatalf("racing grandparent leaked coverage: %+v %v", raced, err)
	}
	if err = program.commitInscriptionBlock(ctx, firstStore, block, inventory, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	fourth := recipeDAGExecutionFixtureRecipe(t, a, "dag-d", &middle)
	fourthStore, err := openRecipeStore(a.dataDir, fourth.ID, fourth.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	guard := func(commit func() error) error {
		if err := firstStore.Reconcile(ctx, map[int64]string{block.Height: block.Hash}, recipeTestGuard); err != nil {
			return err
		}
		return commit()
	}
	if err = a.recipeExecuteDAGBlock(ctx, fourth, fourthStore, block, inventory, 0, guard); !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatalf("grandparent removal raced publication: %v", err)
	}
	if _, found, err = fourthStore.Read(ctx, block.Height); err != nil || found {
		t.Fatalf("stale child committed: %v %v", found, err)
	}
	if _, found, err = a.recipeReadDAGUnit(ctx, child, childStore, block.Height, block.Hash, recipeTestGuard); found || !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatalf("stale child served: %v %v", found, err)
	}
	coverage, err = a.recipeDAGCoverage(ctx, child, childStore, block.Height, block.Height, nil, 0, 1, "", coverageGuard)
	if !errors.Is(err, errRecipeDependencyUnavailable) || len(coverage.Anchors) != 0 {
		t.Fatalf("missing ancestor treated as coverage: %+v %v", coverage, err)
	}
}
