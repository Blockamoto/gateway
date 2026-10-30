package main

// A checkpoint/delta source is a declared data dependency. It cannot be
// substituted by a block scan, a display parent, or an unbuilt UTXO prefix.
import "fmt"

const recipeUTXOProjectionSource = "dependency.utxo"
const recipeUTXOProjectionProfile = "gateway-utxo-snapshot-delta-v1"
const recipeAddressProjectionStorage = "bitcoin.address.cow.v1"

func recipeUTXOProjectionFields() []recipeFieldCapability {
	result := []recipeFieldCapability{}
	for _, group := range []struct {
		names []string
		kind  string
	}{{[]string{"block"}, "text"}, {[]string{"height", "tx_index", "vout", "value"}, "uint"}, {[]string{"script"}, "bytes"}, {[]string{"add"}, "bool"}} {
		for _, name := range group.names {
			result = append(result, recipeFieldCapability{Path: []string{name}, Type: recipeTypeSpec{Types: []string{group.kind}}})
		}
	}
	return result
}

func compileUTXOProjectionRecipe(d recipeDefinition) (*recipeProgram, error) {
	if d.History != "projection" || d.Storage != recipeAddressProjectionStorage || d.Origin != 0 || len(d.Steps) != 0 || len(d.Exports) != 0 {
		return nil, recipeDiagnosticAt("RECIPE_PROJECTION_CONTRACT", "/storage", "address projection requires projection history, origin zero and bitcoin.address.cow.v1 storage")
	}
	if len(d.Dependencies) != 1 {
		return nil, recipeDiagnosticAt("RECIPE_PROJECTION_DEPENDENCY", "/dependencies", "declare one exact UTXO snapshot/delta dependency")
	}
	dep := d.Dependencies[0]
	if !recipeRegistryID.MatchString(dep.Name) || !recipeRegistryID.MatchString(dep.Instance) || dep.Instance == d.ID || !recipeStoreHashValid(dep.Revision) || !recipeStoreHashValid(dep.SemanticHash) || (dep.Scope != "" && dep.Scope != "saved" && dep.Scope != "builtin") || dep.Export != "coins" || dep.Coverage != "snapshot_delta" || dep.Type != "coin" {
		return nil, recipeDiagnosticAt("RECIPE_PROJECTION_DEPENDENCY", "/dependencies/0", "pin a saved or builtin UTXO coins snapshot/delta export and its exact revision/semantic identity")
	}
	if dep.Scope == "builtin" && dep.Instance != "utxo-state" {
		return nil, fmt.Errorf("builtin coin source is unavailable")
	}
	tables := map[string]bool{}
	for _, table := range d.Tables {
		if table.Key != "ref" || table.OnConflict != "" || tables[table.Name] {
			return nil, fmt.Errorf("invalid projection table declaration")
		}
		tables[table.Name] = true
	}
	if len(tables) != 2 || !tables["members"] || !tables["excluded"] {
		return nil, fmt.Errorf("address projection declares members/ref and excluded/ref tables")
	}
	program, err := parseOrderedRecipeProgram(d.Ordered, true)
	if err != nil {
		return nil, err
	}
	fields := map[string]bool{}
	for _, field := range recipeUTXOProjectionFields() {
		fields[field.Path[0]] = true
	}
	var expression func(recipeOrderedExpr) error
	expression = func(e recipeOrderedExpr) error {
		if e.Field != "" && !fields[e.Field] {
			return fmt.Errorf("unknown checkpoint/delta field %q", e.Field)
		}
		for _, arg := range e.Args {
			if err := expression(arg); err != nil {
				return err
			}
		}
		for _, arg := range e.Object {
			if err := expression(arg); err != nil {
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
				return fmt.Errorf("projection mutations are emitted as source-bound member deltas")
			case "emit":
				if !tables[step.Table] {
					return fmt.Errorf("undeclared projection table %q", step.Table)
				}
			}
			if err := expression(step.Expr); err != nil {
				return err
			}
			if err := expression(step.Key); err != nil {
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
		return nil, err
	}
	d.Ordered, err = recipeOrderedProgramJSON(program)
	if err != nil {
		return nil, err
	}
	d.Steps = nil
	d.Exports = nil
	return &recipeProgram{definition: d, ordered: &program}, nil
}
