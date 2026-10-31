package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// This contract is kept separate from native Coin/Sat codecs. Its source is a
// continuous declared prefix; table names never select a domain algorithm.
const recipeTablesStorage = "tables.cow.v1"
const recipeTablesGenerationProfile = "gateway-private-typed-tables-v1"
const recipeTablesSourceProfile = "gateway-typed-table-raw-events-v1"

func recipeTablesFields() []recipeFieldCapability {
	fields := []recipeFieldCapability{}
	for _, f := range recipeOrderedSourceFields() {
		if f.Path[0] != "fact_page" && f.Path[0] != "fact_row" {
			fields = append(fields, f)
		}
	}
	return fields
}

func compileRecipeTablesContract(d recipeDefinition) (*recipeProgram, error) {
	if d.Source != "bitcoin.ordered" && d.Source != "bitcoin.reveal" || d.History != "ordered" || d.Storage != recipeTablesStorage || d.Origin < 0 || len(d.Steps) != 0 || len(d.Dependencies) != 0 || len(d.Exports) != 0 {
		return nil, recipeDiagnosticAt("RECIPE_TABLE_STATE_CONTRACT", "/storage", "typed COW tables require a continuous ordered event prefix and declared tables")
	}
	if len(d.Tables) == 0 || len(d.Tables) > recipeMaxTables {
		return nil, fmt.Errorf("declare one or more state tables")
	}
	tables := map[string]bool{}
	for _, t := range d.Tables {
		if t.Name == "" || len(t.Name) > recipeStateMaxTableBytes || !utf8.ValidString(t.Name) || strings.HasPrefix(t.Name, "__gateway_") || t.Key == "" || len(t.Key) > 256 || !utf8.ValidString(t.Key) || t.OnConflict != "" || tables[t.Name] {
			return nil, recipeDiagnosticAt("RECIPE_TABLE_STATE_TABLES", "/tables", "state tables need unique names, key labels and explicit keyed mutations")
		}
		tables[t.Name] = true
	}
	program, err := parseOrderedRecipeProgram(d.Ordered, false)
	if err != nil {
		return nil, err
	}
	fields := map[string]bool{}
	for _, f := range recipeTablesFieldsFor(d.Source) {
		fields[f.Path[0]] = true
	}
	var expression func(recipeOrderedExpr) error
	expression = func(e recipeOrderedExpr) error {
		if e.Field != "" && !fields[e.Field] {
			return fmt.Errorf("unsupported table-state source field %q", e.Field)
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
	var scan func([]recipeOrderedInstruction) error
	scan = func(steps []recipeOrderedInstruction) error {
		for _, s := range steps {
			if (s.Op == "read" || s.Op == "put" || s.Op == "delete") && !tables[s.Table] {
				return fmt.Errorf("undeclared state table %q", s.Table)
			}
			if s.Op == "emit" {
				return fmt.Errorf("state table rows are written with put; independent emits require a declared output contract")
			}
			if err := expression(s.Expr); err != nil {
				return err
			}
			if err := expression(s.Key); err != nil {
				return err
			}
			if err := scan(s.Body); err != nil {
				return err
			}
			if err := scan(s.Else); err != nil {
				return err
			}
		}
		return nil
	}
	if err = scan(program.Steps); err != nil {
		return nil, err
	}
	d.Ordered, err = recipeOrderedProgramJSON(program)
	if err != nil {
		return nil, err
	}
	d.Dependencies = []recipeDependency{}
	d.Exports = nil
	d.Steps = nil
	return &recipeProgram{definition: d, ordered: &program}, nil
}
