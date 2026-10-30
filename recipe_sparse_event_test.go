package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRecipeSparseEventPreservesIdenticalAndLateFilterSemantics(t *testing.T) {
	base := locatorRecipeProgram(t).definition
	base.Tables = []recipeTable{{Name: "rows", Key: "ref", OnConflict: "identical"}}
	base.Exports = []recipeExport{{Name: "number", Kind: "same_block", Table: "rows", Column: "value", Type: "integer"}}
	base.Steps = nil
	for _, field := range []string{"height", "tx_index"} {
		base.Steps = append(base.Steps, recipeStep{Op: "write", Table: "rows", Row: map[string]recipeExpr{"ref": {Literal: json.RawMessage(`"one"`)}, "value": {Field: []string{field}}}})
	}
	for _, lateFilter := range []bool{false, true} {
		d := base
		if lateFilter {
			d.Steps = append(append([]recipeStep(nil), base.Steps...), recipeStep{Op: "require", Test: &recipeExpr{Literal: json.RawMessage(`false`)}})
		}
		program, err := compileRecipe(d)
		if err != nil {
			t.Fatal(err)
		}
		spool, err := newRecipeSparseSpool(context.Background(), t.TempDir(), program)
		if err != nil {
			t.Fatal(err)
		}
		event := map[string]any{"height": json.Number("1"), "tx_index": float64(1)}
		rows, matched, err := program.evaluateRowProposals(event, nil, spool)
		if lateFilter {
			if err != nil || matched || len(rows) != 0 {
				t.Fatal("late filter failed to discard deferred duplicate type checks", err)
			}
		} else if err == nil {
			t.Fatal("equal wire bytes bypassed duplicate export type check")
		}
		// A real conflicting duplicate remains an error even if a later filter
		// would reject this event. This matches the existing interpreter.
		event["tx_index"] = json.Number("2")
		if _, _, err = program.evaluateRowProposals(event, nil, spool); err == nil {
			t.Fatal("late filter hid conflicting duplicate")
		}
		_ = spool.Close()
	}
}
