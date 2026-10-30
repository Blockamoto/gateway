package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"testing"
)

func saveSourceDAGRecipe(t *testing.T, a *app, id, source, keyField string, parent *resolvedSavedRecipe, target string) resolvedSavedRecipe {
	t.Helper()
	deps := []recipeDependency{}
	steps := ""
	value := `{"field":["ref"]}`
	if target != "" {
		steps += fmt.Sprintf(`{"op":"require","test":{"call":"eq","args":[{"field":["txid"]},{"literal":%q}]}},`, target)
	}
	if parent != nil {
		deps = append(deps, recipeGraphFixtureEdge("input", *parent))
		steps += `{"op":"let","as":"found","value":{"call":"dependency.lookup","args":[{"literal":"input"},{"field":["txid"]}]}},{"op":"require","test":{"call":"valid","args":[{"variable":"found"}]}},`
		value = `{"variable":"found"}`
	}
	encoded, _ := json.Marshal(deps)
	raw := fmt.Sprintf(`{"format":"gateway-recipe","version":1,"id":%q,"name":%q,"source":%q,"origin":0,"history":"sparse","dependencies":%s,"exports":[{"name":"kind","kind":"same_block","table":"items","column":"value","type":"text"}],"tables":[{"name":"items","key":"id"}],"steps":[%s{"op":"write","table":"items","row":{"id":{"field":[%q]},"value":%s}}]}`, id, id, source, encoded, steps, keyField, value)
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

func TestRecipeTransactionDAGPublicCoordinatorAndSavedOnly(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	parent := saveSourceDAGRecipe(t, a, "tx-parent", "bitcoin.transaction", "txid", nil, "")
	child := saveSourceDAGRecipe(t, a, "tx-child", "bitcoin.transaction", "txid", &parent, "")
	source := recipeCoordinatorSource(t)
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) { return source[0], nil }
	z := int64(0)
	req := indexRunRequest{In: &z, Out: &z, Consumers: []indexRunSelection{{Index: child.ID, Armed: true}, {Index: parent.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	done := waitRecipeDAGRun(t, a)
	if done.Run.State != "complete" {
		t.Fatalf("run: %+v", done.Run)
	}
	if done.Run.Counts.InscriptionExtractions != 0 {
		t.Fatal("transaction-only DAG extracted inscriptions")
	}
	result, _, err := a.readRecipeOutput(ctx, child.ID, child.Revision, child.SemanticHash, 0, 0, 10, "")
	if err != nil || result["row_count"] != 1 {
		t.Fatalf("output: %+v %v", result, err)
	}
	// A second child can use saved parent coverage without selecting a writer.
	saved := saveSourceDAGRecipe(t, a, "tx-saved-child", "bitcoin.transaction", "txid", &parent, "")
	req.Consumers = []indexRunSelection{{Index: saved.ID, Armed: true}}
	plan, err = a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("saved plan: %+v %v", plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done = waitRecipeDAGRun(t, a); done.Run.State != "complete" || len(done.Run.Consumers) != 1 {
		t.Fatalf("saved run: %+v", done.Run)
	}
}

func TestRecipeTransactionDAGCrossSourceRealBlock(t *testing.T) {
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	b, inventory := recipeDependencyReferenceBlock(t)
	txParent := saveSourceDAGRecipe(t, a, "cross-tx", "bitcoin.transaction", "txid", nil, "")
	revealChild := saveSourceDAGRecipe(t, a, "cross-reveal", "bitcoin.reveal", "ref", &txParent, "")
	revealParent := saveSourceDAGRecipe(t, a, "cross-reveal-parent", "bitcoin.reveal", "txid", nil, "86539aff946c437af8088955827b7e6ff48fc6192836d4071b697b5359b7a732")
	txChild := saveSourceDAGRecipe(t, a, "cross-tx-child", "bitcoin.transaction", "txid", &revealParent, "")
	snapshot, err := recipeTransactionSourceFromBlock(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]resolvedSavedRecipe{{txParent, revealChild}, {revealParent, txChild}} {
		parent, child := pair[0], pair[1]
		parentStore, err := openRecipeStore(a.dataDir, parent.ID, parent.SemanticHash)
		if err != nil {
			t.Fatal(err)
		}
		childStore, err := openRecipeStore(a.dataDir, child.ID, child.SemanticHash)
		if err != nil {
			t.Fatal(err)
		}
		if err = a.recipeExecuteDAGBlock(ctx, child, childStore, b, inventory, 0, recipeTestGuard, snapshot); !errors.Is(err, errRecipeDependencyUnavailable) {
			t.Fatalf("missing parent: %v", err)
		}
		program, err := compileRecipe(parent.Definition)
		if err != nil {
			t.Fatal(err)
		}
		var rows recipeRows
		if parent.Source == "bitcoin.transaction" {
			rows, err = program.evaluateTransactionSource(ctx, b, snapshot)
		} else {
			rows, err = program.evaluateInscriptionBlock(ctx, b, inventory)
		}
		if err != nil {
			t.Fatal(err)
		}
		unit := recipeStoreUnit{Schema: 1, Fingerprint: parent.SemanticHash, Height: b.Height, BlockHash: b.Hash, Rows: []recipeStoreRow{}}
		for table, values := range rows {
			for key, row := range values {
				compound, _ := json.Marshal([]string{table, key})
				value, err := json.Marshal(row)
				if err != nil {
					t.Fatal(err)
				}
				unit.Rows = append(unit.Rows, recipeStoreRow{Key: string(compound), Value: value})
			}
		}
		sort.Slice(unit.Rows, func(i, j int) bool { return unit.Rows[i].Key < unit.Rows[j].Key })
		if err = parentStore.Publish(ctx, unit, recipeTestGuard); err != nil {
			t.Fatal(err)
		}
		if err = a.recipeExecuteDAGBlock(ctx, child, childStore, b, inventory, 0, recipeTestGuard, snapshot); err != nil {
			t.Fatal(err)
		}
		unit, found, err := a.recipeReadDAGUnit(ctx, child, childStore, b.Height, b.Hash, recipeTestGuard)
		if err != nil || !found || len(unit.Rows) == 0 || len(unit.Lineage) != 1 {
			t.Fatalf("cross-source result: %+v %v %v", unit, found, err)
		}
		if err = parentStore.Reconcile(ctx, map[int64]string{b.Height: b.Hash}, recipeTestGuard); err != nil {
			t.Fatal(err)
		}
		if _, found, err = a.recipeReadDAGUnit(ctx, child, childStore, b.Height, b.Hash, recipeTestGuard); found || !errors.Is(err, errRecipeDependencyUnavailable) {
			t.Fatalf("stale cross-source row exposed: %v %v", found, err)
		}
	}
}
