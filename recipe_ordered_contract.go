package main

import (
	"encoding/json"
	"fmt"
)

const recipeOrderedUTXOStorage = "bitcoin.utxo.cow.v2"
const recipeOrderedDocumentProfile = "gateway-ordered-document-v1"
const recipeOrderedSourceProfile = "gateway-ordered-raw-owned-source-v1"
const recipeOrderedGenerationProfile = "gateway-custom-utxo-origin-v1"
const recipeOrderedPublicEnabled = true

// The first physical contract reuses authenticated Coin facts and native COW
// state/undo. Its fixed table shapes are storage bindings, not an executor for
// an index ID. The executable program still derives every ordered mutation.
func compileOrderedRecipe(d recipeDefinition) (*recipeProgram, error) {
	if d.Storage == recipeTablesStorage {
		return compileRecipeTablesContract(d)
	}
	if d.History == "sparse" {
		return compileOrderedSparseRecipe(d)
	}
	if d.History != "ordered" {
		return nil, recipeDiagnosticAt("RECIPE_ORDERED_HISTORY", "/history", "ordered source requires ordered history")
	}
	if d.Origin != 0 {
		return nil, recipeDiagnosticAt("RECIPE_ORDERED_ORIGIN", "/origin", "this state contract begins at Bitcoin mainnet genesis")
	}
	if d.Storage != recipeOrderedUTXOStorage {
		return nil, recipeDiagnosticAt("RECIPE_ORDERED_STORAGE", "/storage", "unsupported ordered storage contract")
	}
	if len(d.Dependencies) != 0 || len(d.Exports) != 0 {
		return nil, recipeDiagnosticAt("RECIPE_ORDERED_DEPENDENCIES", "/dependencies", "ordered snapshot/delta dependencies require their own supported contract")
	}
	if len(d.Steps) != 0 {
		return nil, recipeDiagnosticAt("RECIPE_ORDERED_STEPS", "/steps", "put ordered instructions inside the ordered program")
	}
	if len(d.Tables) != 2 {
		return nil, recipeDiagnosticAt("RECIPE_ORDERED_TABLES", "/tables", "the Coin contract declares coins/outpoint and counts/txid tables")
	}
	tables := map[string]string{}
	for _, table := range d.Tables {
		if table.OnConflict != "" || tables[table.Name] != "" {
			return nil, recipeDiagnosticAt("RECIPE_ORDERED_TABLES", "/tables", "ordered keyed writes do not use row duplicate policies")
		}
		tables[table.Name] = table.Key
	}
	if tables["coins"] != "outpoint" || tables["counts"] != "txid" {
		return nil, recipeDiagnosticAt("RECIPE_ORDERED_TABLES", "/tables", "the Coin contract declares coins/outpoint and counts/txid tables")
	}
	program, err := parseOrderedRecipeProgram(d.Ordered, true)
	if err != nil {
		return nil, recipeDiagnosticAt("RECIPE_ORDERED_PROGRAM", "/ordered", err.Error())
	}
	fields := map[string]bool{}
	for _, field := range recipeOrderedSourceFields() {
		fields[field.Path[0]] = true
	}
	var expression func(recipeOrderedExpr) error
	expression = func(e recipeOrderedExpr) error {
		if e.Field != "" && !fields[e.Field] {
			return fmt.Errorf("unknown ordered source field %q", e.Field)
		}
		for _, a := range e.Args {
			if err := expression(a); err != nil {
				return err
			}
		}
		for _, a := range e.Object {
			if err := expression(a); err != nil {
				return err
			}
		}
		return nil
	}
	var instructions func([]recipeOrderedInstruction) error
	instructions = func(steps []recipeOrderedInstruction) error {
		for _, step := range steps {
			switch step.Op {
			case "read", "put", "delete":
				if step.Table != "coins" && step.Table != "counts" {
					return fmt.Errorf("unknown Coin-contract state table %q", step.Table)
				}
			case "emit":
				if step.Table != "prevouts" {
					return fmt.Errorf("unknown Coin-contract output table %q", step.Table)
				}
			}
			if err := expression(step.Key); err != nil {
				return err
			}
			if err := expression(step.Expr); err != nil {
				return err
			}
			if err := instructions(step.Body); err != nil {
				return err
			}
			if err := instructions(step.Else); err != nil {
				return err
			}
		}
		return nil
	}
	if err = instructions(program.Steps); err != nil {
		return nil, recipeDiagnosticAt("RECIPE_ORDERED_BINDING", "/ordered", err.Error())
	}
	// Canonicalize validated AST defaults and structural spelling while
	// preserving exact user object keys and typed integer strings.
	d.Ordered, err = recipeOrderedProgramJSON(program)
	if err != nil {
		return nil, err
	}
	d.Dependencies = []recipeDependency{}
	d.Exports = nil
	d.Steps = nil
	d.Tables = []recipeTable{{Name: "coins", Key: "outpoint"}, {Name: "counts", Key: "txid"}}
	return &recipeProgram{definition: d, ordered: &program}, nil
}

func recipeOrderedProgramJSON(program recipeOrderedProgram) ([]byte, error) {
	var expression func(recipeOrderedExpr) map[string]any
	expression = func(e recipeOrderedExpr) map[string]any {
		m := map[string]any{}
		switch {
		case e.Literal != nil:
			m["literal"] = e.Literal
		case e.Var != "":
			m["var"] = e.Var
		case e.Field != "":
			m["field"] = e.Field
		case e.Call != "":
			m["call"] = e.Call
			args := []any{}
			for _, a := range e.Args {
				args = append(args, expression(a))
			}
			m["args"] = args
		case e.Object != nil:
			object := map[string]any{}
			for name, a := range e.Object {
				object[name] = expression(a)
			}
			m["object"] = object
		}
		return m
	}
	var instructions func([]recipeOrderedInstruction) []any
	instructions = func(steps []recipeOrderedInstruction) []any {
		result := []any{}
		for _, step := range steps {
			m := map[string]any{"op": step.Op}
			if step.Name != "" {
				m["name"] = step.Name
			}
			if step.Table != "" {
				m["table"] = step.Table
			}
			if !recipeResumeExprEmpty(step.Expr) {
				m["expr"] = expression(step.Expr)
			}
			if !recipeResumeExprEmpty(step.Key) {
				m["key"] = expression(step.Key)
			}
			if len(step.Body) > 0 {
				m["body"] = instructions(step.Body)
			}
			if len(step.Else) > 0 {
				m["else"] = instructions(step.Else)
			}
			result = append(result, m)
		}
		return result
	}
	return json.Marshal(map[string]any{"steps": instructions(program.Steps)})
}

func recipeOrderedSourceFields() []recipeFieldCapability {
	result := []recipeFieldCapability{}
	for _, group := range []struct {
		names    []string
		kind     string
		optional bool
	}{
		{[]string{"kind", "block", "txid"}, "text", false},
		{[]string{"height", "tx_index", "output_count"}, "uint", false},
		{[]string{"coinbase"}, "bool", false},
		{[]string{"prev_txid", "input_ref", "fact_page"}, "text", true},
		{[]string{"prev_vout", "input_index", "vout", "value", "fact_row"}, "uint", true},
		{[]string{"script"}, "bytes", true},
	} {
		for _, name := range group.names {
			result = append(result, recipeFieldCapability{Path: []string{name}, Type: recipeTypeSpec{Types: []string{group.kind}, Optional: group.optional}})
		}
	}
	return result
}
