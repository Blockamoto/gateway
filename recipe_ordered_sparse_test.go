package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func recipeOrderedSparseTestDefinition(t *testing.T) recipeDefinition {
	t.Helper()
	raw, err := os.ReadFile("recipes/output-destinations.json")
	if err != nil {
		t.Fatal(err)
	}
	d, err := parseRecipe(raw)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRecipeOrderedSparseLargeRowsLongKeysAndFieldBudget(t *testing.T) {
	ctx := context.Background()
	d := recipeOrderedSparseTestDefinition(t)
	d.Exports = nil
	chunk := roLit(rovText(strings.Repeat("x", 200<<10)))
	part := map[string]recipeOrderedExpr{}
	for i := 0; i < 60; i++ {
		part[fmt.Sprintf("field_%02d", i)] = roVar("chunk")
	}
	key := strings.Repeat("k", 9000)
	row := map[string]recipeOrderedExpr{"ref": roLit(rovText(key)), "left": roVar("part"), "right": roVar("part")}
	emit := recipeOrderedInstruction{Op: "emit", Table: "outputs", Key: roLit(rovText(key)), Expr: recipeOrderedExpr{Object: row}}
	body := []recipeOrderedInstruction{{Op: "let", Name: "chunk", Expr: chunk}, {Op: "let", Name: "part", Expr: recipeOrderedExpr{Object: part}}, emit}
	condition := recipeOrderedInstruction{Op: "if", Expr: roCall("eq", roField("kind"), roLit(rovText("output"))), Body: body}
	program := recipeOrderedProgram{Steps: []recipeOrderedInstruction{{Op: "for_events", Body: []recipeOrderedInstruction{condition}}}}

	d.Ordered, _ = recipeOrderedProgramJSON(program)
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	spool, err := newRecipeSparseSpool(ctx, t.TempDir(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	block := recipeCoordinatorSource(t)[0]
	if err = p.evaluateOrderedSparseInto(ctx, block, spool.Add); err != nil {
		t.Fatal(err)
	}
	staged, err := spool.Seal(recipeStoreUnit{Schema: 1, Fingerprint: p.semanticHash(), Height: 0, BlockHash: block.Hash})
	if err != nil || staged.Bytes <= recipeStoreUnitMaxBytes || staged.Rows != 1 {
		t.Fatal(staged, err)
	}
	store := recipeTestOpen(t, t.TempDir(), d.ID, p.semanticHash())
	if err = store.publishSparseStaged(ctx, staged, 0, recipeTestGuard, nil); err != nil {
		t.Fatal(err)
	}
	view, err := store.openSparseView(ctx, recipeStoreRef{BlockHash: block.Hash, Unit: staged.Digest}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	compound, _ := json.Marshal([]string{"outputs", key})
	value, found, err := view.LookupScalar(string(compound), "ref")
	if err != nil || !found || value != key {
		t.Fatal("long-key large row unavailable", found, err)
	}
	// A single expanded field still owes the decoded-value/expression budget.
	// The total projected row does not inherit that per-field bound.
	oversized := recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{}}
	for i := 0; i < 60; i++ {
		oversized.Object[fmt.Sprint(i)] = rovText(strings.Repeat("y", 300<<10))
	}
	if _, err = recipeOrderedRowValue(recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"one_field": oversized}}); err == nil {
		t.Fatal("expanded field bypassed the decoded-value budget")
	}
	// Shared object references cannot make structural validation exponential.
	expanded := rovUint(1)
	for level := 0; level < 4; level++ {
		next := map[string]recipeOrderedValue{}
		for i := 0; i < 64; i++ {
			next[fmt.Sprint(i)] = expanded
		}
		expanded = recipeOrderedValue{Kind: "object", Object: next}
	}
	if err = recipeOrderedCheck(expanded, 0); err == nil {
		t.Fatal("amplified value bypassed structural work budget")
	}
}
func TestRecipeOrderedSparseProjectionAndRejection(t *testing.T) {
	ctx := context.Background()
	d := recipeOrderedSparseTestDefinition(t)
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	block := recipeCoordinatorSource(t)[0]
	spool, err := newRecipeSparseSpool(ctx, t.TempDir(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	if err = p.evaluateOrderedSparseInto(ctx, block, spool.Add); err != nil {
		t.Fatal(err)
	}
	staged, err := spool.Seal(recipeStoreUnit{Schema: 1, Fingerprint: p.semanticHash(), Height: 0, BlockHash: block.Hash})
	if err != nil || staged.Rows != 1 {
		t.Fatal(staged, err)
	}
	store := recipeTestOpen(t, t.TempDir(), d.ID, p.semanticHash())
	if err = store.publishSparseStaged(ctx, staged, 0, recipeTestGuard, nil); err != nil {
		t.Fatal(err)
	}
	unit, found, err := store.Read(ctx, 0)
	if err != nil || !found || len(unit.Rows) != 1 {
		t.Fatal(found, err)
	}
	var row map[string]any
	decoder := json.NewDecoder(bytes.NewReader(unit.Rows[0].Value))
	decoder.UseNumber()
	if err = decoder.Decode(&row); err != nil {
		t.Fatal(err)
	}
	if row["ref"] != outputCoordinate(0, 0, 0) || row["script_type"] != "p2pk" || row["value_sats"] != json.Number("5000000000") {
		t.Fatal(row)
	}
	for _, mode := range []string{"state", "table", "source", "contract", "history"} {
		t.Run(mode, func(t *testing.T) {
			copy := recipeOrderedSparseTestDefinition(t)
			switch mode {
			case "state":
				program, _ := parseOrderedRecipeProgram(copy.Ordered, true)
				program.Steps[0].Body = append(program.Steps[0].Body, recipeOrderedInstruction{Op: "read", Name: "coin", Table: "outputs", Key: roLit(rovText("x"))})
				copy.Ordered, _ = recipeOrderedProgramJSON(program)
			case "table":
				copy.Tables[0].Name = "other"
			case "source":
				copy.Ordered = json.RawMessage(`{"steps":[{"op":"for_events","body":[{"op":"let","name":"x","expr":{"field":"unavailable"}}]}]}`)
			case "contract":
				copy.Storage = recipeOrderedUTXOStorage
			case "history":
				copy.History = "ordered"
			}
			if _, err := compileRecipe(copy); err == nil {
				t.Fatal("invalid stateless projection compiled", mode)
			}
		})
	}
}

func TestRecipeOrderedSparsePublicArbitraryStartAndExportedParent(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	source := recipeCoordinatorSource(t)
	d := recipeOrderedSparseTestDefinition(t)
	d.Origin = 1
	raw, _ := json.Marshal(d)
	parent := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(raw), "")))
	childDefinition := locatorRecipeProgram(t).definition
	childDefinition.ID = "destination-consumer"
	childDefinition.Name = childDefinition.ID
	childDefinition.Origin = 1
	childDefinition.Dependencies = []recipeDependency{{Name: "outputs", Instance: d.ID, Revision: parent.Revision, SemanticHash: parent.SemanticHash, Export: "destination", Type: "text", Coverage: "same_block"}}
	key, _ := json.Marshal(outputCoordinate(0, 0, 1))
	childDefinition.Steps[len(childDefinition.Steps)-1].Row["destination"] = recipeExpr{Call: "dependency.lookup", Args: []recipeExpr{{Literal: json.RawMessage(`"outputs"`)}, {Literal: key}}}
	raw, _ = json.Marshal(childDefinition)
	child := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(raw), "")))
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		return source[target.Height], nil
	}
	one := int64(1)
	request := indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{{Index: child.Recipe.ID, Armed: true}, {Index: parent.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, request)
	if err != nil || !plan.Ready || plan.RequiresGenesis {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, request, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
	if done.Run.Counts.SourceReads != 1 || done.Run.Counts.OrderedSourceVerifications != 1 {
		t.Fatal("owned output source was not shared", done.Run.Counts)
	}
	for _, view := range []recipeRegistryView{parent, child} {
		page, _, err := a.readRecipeOutput(ctx, view.Recipe.ID, view.Revision, view.SemanticHash, 1, 0, 10, "")
		if err != nil || page["row_count"] != 1 {
			t.Fatal(view.Recipe.ID, page, err)
		}
		var artifact bytes.Buffer
		resolved, err := a.ResolveSavedRecipe(ctx, view.Recipe.ID, view.Revision, view.SemanticHash)
		if err != nil {
			t.Fatal(err)
		}
		meta, err := a.writeRecipeRangeExport(ctx, resolved, 1, 1, &artifact)
		if err != nil || meta.Rows != 1 || meta.UncoveredBlocks != 0 {
			t.Fatal(view.Recipe.ID, meta, err)
		}
	}
}
