package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"testing"
)

func TestRecipeOrderedListExampleAndSparseArrayEncoding(t *testing.T) {
	raw, err := os.ReadFile("recipes/declared-parent-mentions.json")
	if err != nil {
		t.Fatal(err)
	}
	d, err := parseRecipe(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = compileRecipe(d); err != nil {
		t.Fatal(err)
	}
	list := recipeOrderedValue{Kind: "list", List: []recipeOrderedValue{rovUint(math.MaxUint64), rovBytes([]byte{0xff}), {Kind: "object", Object: map[string]recipeOrderedValue{"yes": rovBool(true)}}}}
	value, err := recipeOrderedPlainRowValue(list, map[string]recipeBytes{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != `[18446744073709551615,{"hex":"ff"},{"yes":true}]` {
		t.Fatal(string(encoded), err)
	}
}

func TestRecipeRevealTypedParentDeclarationsPreserveOrder(t *testing.T) {
	ctx := context.Background()
	first := bytes.Repeat([]byte{0x22}, 32)
	second := append(bytes.Repeat([]byte{0x33}, 32), 1)
	block := bitmapDevelopmentFixtureBlock(792435, testID(669511), "child")
	block.Transactions[1].Inputs[0].Witness = ordWitness050(inscriptionTestScript([]byte{1}, []byte("text/plain"), []byte{3}, first, []byte{3}, second, nil, []byte("child")))
	inventory, err := extractInscriptionOccurrences(block)
	if err != nil || len(inventory.Occurrences) != 1 || len(inventory.Occurrences[0].Envelope.Parents) != 2 {
		t.Fatal(inventory, err)
	}
	seen := 0
	if err = recipeRevealInventoryWalk(ctx, block, &inventory, func(event map[string]recipeOrderedValue) error {
		seen++
		parents := event["parents"]
		if parents.Kind != "list" || len(parents.List) != 2 || event["parent_count"].Uint != 2 {
			t.Fatal(event)
		}
		for i, id := range inventory.Occurrences[0].Envelope.Parents {
			if parents.List[i].Text != id {
				t.Fatal("declaration order changed", parents)
			}
		}
		if _, present := event["parent_verified"]; present {
			t.Fatal("declaration became provenance")
		}
		return nil
	}); err != nil || seen != 1 {
		t.Fatal(seen, err)
	}
}

func TestRecipeOrderedListsExactTypesOrderBoundsAndBudgets(t *testing.T) {
	ctx := context.Background()
	list := recipeOrderedValue{Kind: "list", List: []recipeOrderedValue{rovText("1"), rovUint(math.MaxUint64), rovBytes([]byte{0xff, 0}), rovBool(false), {Kind: "missing"}, {Kind: "list", List: []recipeOrderedValue{}}}}
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	var restored recipeOrderedValue
	if err = json.Unmarshal(raw, &restored); err != nil || indexDigest(list) != indexDigest(restored) {
		t.Fatal(string(raw), err)
	}
	call := func(name string, args ...recipeOrderedValue) recipeOrderedValue {
		t.Helper()
		m := recipeOrderedMachine{ctx: ctx}
		value, handled, err := m.listCall(name, args)
		if err != nil || !handled {
			t.Fatal(name, err)
		}
		return value
	}
	if value := call("list_length", list); value.Uint != 6 {
		t.Fatal(value)
	}
	if value := call("list_at", list, rovUint(1)); value.Uint != math.MaxUint64 {
		t.Fatal(value)
	}
	for _, index := range []uint64{6, math.MaxUint64} {
		if value := call("list_at", list, rovUint(index)); value.Kind != "missing" {
			t.Fatal(value)
		}
	}
	for _, needle := range list.List[:5] {
		if value := call("list_contains", list, needle); !value.Bool {
			t.Fatal(needle, value)
		}
	}
	if value := call("list_contains", list, rovUint(1)); value.Bool {
		t.Fatal("membership coerced text to integer")
	}
	for _, bad := range []string{`{"kind":"list"}`, `{"kind":"list","list":null}`, `{"kind":"list","list":{}}`, `{"kind":"list","list":[{"kind":"uint","uint":"01"}]}`, `{"kind":"text","list":[]}`} {
		if err = json.Unmarshal([]byte(bad), &restored); err == nil {
			t.Fatal("accepted malformed list", bad)
		}
	}
	remaining := 100
	if err = recipeResumeValueBudget(list, 0, &remaining); !errors.Is(err, errRecipeStateBudget) {
		t.Fatal("list escaped byte accounting", err)
	}
	m := recipeOrderedMachine{ctx: ctx, work: recipeOrderedMaxWork}
	if _, _, err = m.listCall("list_contains", []recipeOrderedValue{list, rovText("absent")}); !errors.Is(err, errRecipeStateBudget) {
		t.Fatal("membership escaped work budget", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	m.ctx = cancelled
	if _, _, err = m.listCall("list_at", []recipeOrderedValue{list, rovUint(0)}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	clone, err := recipeTablesCloneValue(list)
	if err != nil {
		t.Fatal(err)
	}
	clone.List[2].Bytes[0] = 0
	if list.List[2].Bytes[0] != 0xff {
		t.Fatal("owned imported list aliases bytes")
	}
}

func TestRecipeOrderedListPublicTableRoundTrip(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	p := recipeTablesTestProgram(t, "list-state", 1, false)
	list := recipeOrderedValue{Kind: "list", List: []recipeOrderedValue{rovUint(math.MaxUint64), rovText("exact"), {Kind: "list", List: []recipeOrderedValue{rovBool(false)}}}}
	program := *p.ordered
	program.Steps[0].Body[0].Body = []recipeOrderedInstruction{{Op: "put", Table: "counts", Key: roLit(rovText("values")), Expr: roLit(list)}}
	p.definition.Ordered, _ = recipeOrderedProgramJSON(program)
	var err error
	p, err = compileRecipe(p.definition)
	if err != nil {
		t.Fatal(err)
	}
	saved := saveTablesRecipeForRun(t, a, p)
	one := int64(1)
	req := indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{{Index: saved.Recipe.ID, Armed: true}}}
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) { return blocks[1], nil }
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a); done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
	out, _, err := a.readRecipeOutput(ctx, saved.Recipe.ID, saved.Revision, saved.SemanticHash, 1, 0, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	rows := out["rows"].([]recipeStoreRow)
	var value recipeOrderedValue
	if len(rows) != 1 || json.Unmarshal(rows[0].Value, &value) != nil || indexDigest(value) != indexDigest(list) {
		t.Fatal(rows)
	}
}
