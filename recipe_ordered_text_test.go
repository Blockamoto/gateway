package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestRecipeOrderedTextExactBytesDecimalsAndMissing(t *testing.T) {
	ctx := context.Background()
	call := func(name string, args ...recipeOrderedValue) recipeOrderedValue {
		t.Helper()
		v, e := recipeOrderedTextCall(ctx, name, args)
		if e != nil {
			t.Fatal(name, e)
		}
		return v
	}
	for _, decimal := range []string{"0", "9007199254740993", "18446744073709551615"} {
		n := call("decimal_parse", rovBytes([]byte(decimal)))
		if n.Kind != "uint" {
			t.Fatal(decimal, n)
		}
		if got := call("decimal_format", n); got.Text != decimal {
			t.Fatal("decimal round trip", decimal, got)
		}
	}
	for _, decimal := range []string{"", "00", "01", "+1", "-1", "1 ", " 1", "1.0", "1e2", "１", "18446744073709551616"} {
		if v := call("decimal_parse", rovBytes([]byte(decimal))); v.Kind != "missing" {
			t.Fatal("noncanonical decimal accepted", decimal, v)
		}
	}
	input := rovBytes([]byte{0xff, '0', '.', 'B', 'i', 'T', 'M', 'A', 'P', 0xfe})
	before := append([]byte(nil), input.Bytes...)
	if v := call("bytes_find_fold_ascii", input, rovBytes([]byte(".bitmap"))); v.Kind != "uint" || v.Uint != 2 {
		t.Fatal(v)
	}
	if !bytes.Equal(input.Bytes, before) {
		t.Fatal("folding mutated its source")
	}
	if v := call("bytes_find", input, rovBytes([]byte(".bitmap"))); v.Kind != "missing" {
		t.Fatal(v)
	}
	if v := call("bytes_find", input, rovBytes(nil)); v.Kind != "uint" || v.Uint != 0 {
		t.Fatal(v)
	}
	if v := call("bytes_ascii", input); v.Kind != "missing" {
		t.Fatal("invalid ASCII normalized", v)
	}
	if v := call("bytes_ascii", rovBytes([]byte("\x00\x7f A\n"))); v.Text != "\x00\x7f A\n" {
		t.Fatal("ASCII bytes trimmed or changed", v)
	}
	part := call("bytes_slice", input, rovUint(1), rovUint(9))
	part.Bytes[0] = '9'
	if input.Bytes[1] != '0' {
		t.Fatal("slice aliases caller bytes")
	}
	for _, bounds := range [][2]uint64{{2, 1}, {0, 11}, {math.MaxUint64, math.MaxUint64}} {
		if v := call("bytes_slice", input, rovUint(bounds[0]), rovUint(bounds[1])); v.Kind != "missing" {
			t.Fatal(bounds, v)
		}
	}
	joined := call("bytes_concat", rovBytes([]byte("0")), rovBytes([]byte(".bitmap")))
	if string(joined.Bytes) != "0.bitmap" {
		t.Fatal(joined)
	}
	if !call("bytes_equal", joined, rovBytes([]byte("0.bitmap"))).Bool || call("bytes_equal", joined, rovBytes([]byte("0.Bitmap"))).Bool {
		t.Fatal("byte equality normalizes values")
	}
	if call("bytes_length", input).Uint != uint64(len(input.Bytes)) {
		t.Fatal("byte length changed")
	}
	if _, e := recipeOrderedTextCall(ctx, "bytes_concat", []recipeOrderedValue{rovBytes(make([]byte, recipeStateHardValueBytes)), rovBytes([]byte{1})}); !errors.Is(e, errRecipeStateBudget) {
		t.Fatal("oversized concatenation", e)
	}
	for _, media := range []string{"text/plain", "Text/Plain; charset=utf-16", "TEXT/PLAIN; charset=made-up"} {
		if v := call("media_type", rovText(media)); v.Kind != "text" || v.Text != "text/plain" {
			t.Fatal(media, v)
		}
	}
	if v := call("media_type", rovText("text/plain; =bad")); v.Kind != "missing" {
		t.Fatal(v)
	}
	if v := call("inscription_ref", rovUint(792435), rovUint(1), rovUint(0)); v.Text != "1i0.792435" {
		t.Fatal(v)
	}
	for _, args := range [][]recipeOrderedValue{{rovUint(math.MaxUint64), rovUint(1), rovUint(0)}, {rovUint(1), rovUint(math.MaxUint32 + 1), rovUint(0)}} {
		if _, e := recipeOrderedTextCall(ctx, "inscription_ref", args); e == nil {
			t.Fatal("overflowing coordinate accepted")
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := recipeOrderedTextCall(cancelled, "bytes_find_fold_ascii", []recipeOrderedValue{input, input}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestRecipeOrderedTextCompositionPublicExactState(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	source := recipeCoordinatorSource(t)
	content := roLit(rovBytes([]byte("18446744073709551615.bitmap")))
	steps := []recipeOrderedInstruction{
		{Op: "let", Name: "marker", Expr: roCall("bytes_find", content, roLit(rovBytes([]byte(".bitmap"))))},
		{Op: "let", Name: "stem", Expr: roCall("bytes_slice", content, roLit(rovUint(0)), roVar("marker"))},
		{Op: "let", Name: "number", Expr: roCall("decimal_parse", roVar("stem"))},
		{Op: "put", Table: "claims", Key: roCall("decimal_format", roVar("number")), Expr: recipeOrderedExpr{Object: map[string]recipeOrderedExpr{
			"number": roVar("number"), "ref": roCall("inscription_ref", roField("height"), roField("tx_index"), roLit(rovUint(0))),
			"media": roCall("media_type", roLit(rovText("Text/Plain; charset=utf-16"))),
			"exact": roCall("bytes_equal", content, roCall("bytes_concat", roVar("stem"), roLit(rovBytes([]byte(".bitmap"))))),
		}}},
	}
	program := recipeOrderedProgram{Steps: []recipeOrderedInstruction{{Op: "for_events", Body: []recipeOrderedInstruction{{Op: "if", Expr: roCall("eq", roField("kind"), roLit(rovText("tx_begin"))), Body: steps}}}}}
	raw, err := recipeOrderedProgramJSON(program)
	if err != nil {
		t.Fatal(err)
	}
	d := recipeDefinition{Format: "gateway-recipe", Version: 1, ID: "exact-text-state", Name: "Exact text state", Source: "bitcoin.ordered", Origin: 1, History: "ordered", Storage: recipeTablesStorage, Tables: []recipeTable{{Name: "claims", Key: "number"}}, Ordered: raw}
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	v := saveTablesRecipeForRun(t, a, p)
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != 1 {
			t.Error("hidden prefix", target.Height)
		}
		return source[target.Height], nil
	}
	one := int64(1)
	req := indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready || plan.RequiresGenesis {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a); done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
	out, _, err := a.readRecipeOutput(ctx, v.Recipe.ID, v.Revision, v.SemanticHash, 1, 0, 10, "")
	if err != nil || out["state"] != "covered" {
		t.Fatal(out, err)
	}
	rows := out["rows"].([]recipeStoreRow)
	var result recipeOrderedValue
	if len(rows) != 1 || rows[0].Key != `["claims","18446744073709551615"]` || json.Unmarshal(rows[0].Value, &result) != nil {
		t.Fatal(rows)
	}
	if result.Object["number"].Uint != math.MaxUint64 || result.Object["ref"].Text != "0i0.1" || result.Object["media"].Text != "text/plain" || !result.Object["exact"].Bool {
		t.Fatal(result)
	}
	// The existing eq contract is unchanged; byte comparisons use their new
	// explicit operator instead of changing the meaning of an old definition.
	machine := recipeOrderedMachine{ctx: ctx, vars: map[string]recipeOrderedValue{}}
	if _, err = machine.eval(roCall("eq", content, content), 0); err == nil {
		t.Fatal("old eq byte rejection changed")
	}
}
