package main

import "fmt"

const recipeSatSource = "bitcoin.fifo"
const recipeSatSourceProfile = "gateway-fifo-ordinary-then-coinbase-events-v1"
const recipeSatStorage = "bitcoin.sats.cow.v1"

func recipeSatSourceFields() []recipeFieldCapability {
	fields := append([]recipeFieldCapability{}, recipeOrderedSourceFields()...)
	for _, group := range []struct {
		names []string
		kind  string
	}{
		{[]string{"id"}, "text"},
		{[]string{"unbound", "has_pointer"}, "bool"},
		{[]string{"input_value", "input_offset", "pointer", "outputs_value"}, "uint"},
	} {
		for _, name := range group.names {
			fields = append(fields, recipeFieldCapability{Path: []string{name}, Type: recipeTypeSpec{Types: []string{group.kind}, Optional: true}})
		}
	}
	return fields
}

func compileSatRecipe(d recipeDefinition) (*recipeProgram, error) {
	if d.History != "ordered" || d.Origin != 0 || d.Storage != recipeSatStorage || len(d.Dependencies) != 0 || len(d.Exports) != 0 || len(d.Steps) != 0 {
		return nil, recipeDiagnosticAt("RECIPE_FIFO_CONTRACT", "/storage", "FIFO state requires a genesis prefix, ordered history and bitcoin.sats.cow.v1 storage")
	}
	tables := map[string]string{}
	for _, t := range d.Tables {
		if tables[t.Name] != "" || t.OnConflict != "" || t.Name != "outputs" && t.Name != "history" || t.Name == "outputs" && t.Key != "outpoint" || t.Name == "history" && t.Key != "output" {
			return nil, recipeDiagnosticAt("RECIPE_FIFO_TABLES", "/tables", "declare outputs/outpoint and optional history/output")
		}
		tables[t.Name] = t.Key
	}
	if tables["outputs"] != "outpoint" {
		return nil, fmt.Errorf("FIFO state requires outputs/outpoint")
	}
	program, err := parseOrderedRecipeProgram(d.Ordered, true)
	if err != nil {
		return nil, err
	}
	fields := map[string]bool{}
	for _, f := range recipeSatSourceFields() {
		fields[f.Path[0]] = true
	}
	var expr func(recipeOrderedExpr) error
	expr = func(e recipeOrderedExpr) error {
		if e.Field != "" && !fields[e.Field] {
			return fmt.Errorf("unknown FIFO source field %q", e.Field)
		}
		for _, a := range e.Args {
			if err := expr(a); err != nil {
				return err
			}
		}
		for _, a := range e.Object {
			if err := expr(a); err != nil {
				return err
			}
		}
		return nil
	}
	var scan func([]recipeOrderedInstruction) error
	scan = func(steps []recipeOrderedInstruction) error {
		for _, s := range steps {
			switch s.Op {
			case "read", "put", "delete":
				if tables[s.Table] == "" || s.Table == "history" && s.Op != "put" {
					return fmt.Errorf("unsupported FIFO state table operation")
				}
			case "emit":
				if s.Table != "identities" && s.Table != "lost" && s.Table != "incarnations" {
					return fmt.Errorf("unsupported FIFO output table %q", s.Table)
				}
			}
			if err := expr(s.Expr); err != nil {
				return err
			}
			if err := expr(s.Key); err != nil {
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
	d.Tables = []recipeTable{{Name: "outputs", Key: "outpoint"}}
	if tables["history"] != "" {
		d.Tables = append(d.Tables, recipeTable{Name: "history", Key: "output"})
	}
	d.Exports = nil
	d.Steps = nil
	return &recipeProgram{definition: d, ordered: &program}, nil
}
