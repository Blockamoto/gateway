package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func recipeTypeTestDefinition(steps string) recipeDefinition {
	text := fmt.Sprintf(`{"format":"gateway-recipe","version":1,"id":"types","name":"Types","source":"bitcoin.reveal","origin":0,"history":"sparse","tables":[{"name":"items","key":"ref"}],"steps":[%s]}`, steps)
	d, err := parseRecipe([]byte(text))
	if err != nil {
		panic(err)
	}
	return d
}

func TestRecipeCatalogueDescribesInstalledTypes(t *testing.T) {
	c := recipeCatalogue()
	if c.Version != 1 || len(c.Sources) != 5 || c.Sources[0].ID != "bitcoin.reveal" || len(c.Steps) != 3 {
		t.Fatalf("catalogue: %+v", c)
	}
	projection := c.Sources[3]
	if txid, ok := recipeFindField(recipeSatSource, []string{"txid"}); !ok || !txid.Type.Optional {
		t.Fatal("FIFO block initialization has no transaction fields", txid)
	}
	if projection.ID != recipeUTXOProjectionSource || strings.Join(projection.Histories, ",") != "projection" || strings.Join(projection.StateContracts, ",") != recipeAddressProjectionStorage {
		t.Fatal("checkpoint dependency contract missing", projection)
	}
	if add, ok := recipeFindField(recipeUTXOProjectionSource, []string{"add"}); !ok || strings.Join(add.Type.Types, ",") != "bool" {
		t.Fatal("source-bound add/remove field missing", add)
	}
	if len(c.BuiltinCheckpoints) != 3 || c.BuiltinCheckpoints[0].Scope != "builtin" || c.BuiltinCheckpoints[0].Instance != "utxo-state" || c.BuiltinCheckpoints[0].Coverage != "snapshot_delta" {
		t.Fatal("installed builtin checkpoint binding missing", c.BuiltinCheckpoints)
	}
	for i, table := range []string{"winners", "candidates"} {
		binding := c.BuiltinCheckpoints[i+1]
		parent, err := recipeResolveBuiltinDefinition(binding)
		if err != nil || binding.Instance != bitmapStandardDistrictID || binding.Export != table || !recipeTableParentDeclares(parent, binding) {
			t.Fatal("installed District table checkpoint unavailable", binding, err)
		}
	}
	metadata, ok := recipeFindField("bitcoin.reveal", []string{"metadata_hex"})
	if !ok || !metadata.Type.Optional || strings.Join(metadata.Type.Types, ",") != "text" {
		t.Fatal("metadata optional type missing", metadata)
	}
	trait, ok := recipeFindField("bitcoin.reveal", []string{"properties", "attributes", "traits", "color"})
	if !ok || !trait.Type.Optional || strings.Join(trait.Type.Types, ",") != "text,bool,integer,null" {
		t.Fatal("trait union missing", trait)
	}
	if _, ok = recipeFindField("bitcoin.reveal", []string{"properties", "attributes", "traits", "color", "extra"}); ok {
		t.Fatal("dynamic path admitted extra component")
	}
	encoded, err := json.Marshal(c)
	if err != nil || !strings.Contains(string(encoded), `"types":["cbor"]`) || !strings.Contains(string(encoded), `"optional":true`) {
		t.Fatalf("catalogue JSON: %s %v", encoded, err)
	}
	// Every call returns independent declarations suitable for API use.
	c.Sources[0].Fields[0].Path[0] = "changed"
	if recipeCatalogue().Sources[0].Fields[0].Path[0] != "ref" {
		t.Fatal("catalogue aliases mutable state")
	}
}

func TestRecipeCompilerTypedDiagnostics(t *testing.T) {
	cases := []struct{ step, code, pointer string }{
		{`{"op":"require","test":{"literal":"yes"}}`, "RECIPE_TYPE_MISMATCH", "/steps/0/test"},
		{`{"op":"require","test":{"call":"not","args":[{"field":["ref"]}]}}`, "RECIPE_TYPE_MISMATCH", "/steps/0/test/args/0"},
		{`{"op":"require","test":{"call":"eq","args":[{"field":["metadata_hex"]},{"literal":true}]}}`, "RECIPE_TYPE_MISMATCH", "/steps/0/test/args/1"},
		{`{"op":"require","test":{"call":"eq","args":[{"field":["payload_hex"]},{"literal":[]}]}}`, "RECIPE_TYPE_MISMATCH", "/steps/0/test/args/0"},
		{`{"op":"require","test":{"call":"eq","args":[{"literal":{}},{"literal":{}}]}}`, "RECIPE_TYPE_MISMATCH", "/steps/0/test/args/0"},
		{`{"op":"require","test":{"call":"eq","args":[{"literal":1.0},{"literal":1.00}]}}`, "RECIPE_TYPE_MISMATCH", "/steps/0/test/args/0"},
		{`{"op":"require","test":{"call":"eq","args":[{"literal":1e0},{"literal":1}]}}`, "RECIPE_TYPE_MISMATCH", "/steps/0/test/args/0"},
		{`{"op":"let","as":"m","value":{"call":"cbor.decode","args":[{"literal":42}]}}`, "RECIPE_TYPE_MISMATCH", "/steps/0/value/args/0"},
		{`{"op":"let","as":"m","value":{"call":"cbor.scalar","args":[{"field":["ref"]}]}}`, "RECIPE_TYPE_MISMATCH", "/steps/0/value/args/0"},
		{`{"op":"let","as":"m","value":{"call":"cbor.index","args":[{"call":"cbor.decode","args":[{"literal":"80"}]},{"literal":-1}]}}`, "RECIPE_INVALID_ARGUMENT", "/steps/0/value/args/1"},
		{`{"op":"let","as":"m","value":{"call":"cbor.index","args":[{"call":"cbor.decode","args":[{"literal":"80"}]},{"literal":1.2}]}}`, "RECIPE_TYPE_MISMATCH", "/steps/0/value/args/1"},
		{`{"op":"let","as":"m","value":{"call":"cbor.key","args":[{"call":"cbor.decode","args":[{"literal":"a0"}]},{"literal":18446744073709551616}]}}`, "RECIPE_INVALID_ARGUMENT", "/steps/0/value/args/1"},
		{`{"op":"write","table":"items","row":{"ref":{"literal":42}}}`, "RECIPE_TYPE_MISMATCH", "/steps/0/row/ref"},
		{`{"op":"write","table":"items","row":{"ref":{"field":["nope"]}}}`, "RECIPE_UNKNOWN_FIELD", "/steps/0/row/ref/field"},
		{`{"op":"write","table":"items","row":{"ref":{"field":["ref"]},"x/y~z":{"call":"not","args":[{"literal":0}]}}}`, "RECIPE_TYPE_MISMATCH", "/steps/0/row/x~1y~0z/args/0"},
	}
	for _, tc := range cases {
		t.Run(tc.pointer+tc.code, func(t *testing.T) {
			_, err := compileRecipe(recipeTypeTestDefinition(tc.step))
			var d *recipeDiagnostic
			if !errors.As(err, &d) || d.Code != tc.code || d.Pointer != tc.pointer {
				t.Fatalf("diagnostic: %#v, %v", d, err)
			}
		})
	}
	d := recipeTypeTestDefinition(`{"op":"write","table":"items","row":{"ref":{"field":["ref"]}}}`)
	d.Dependencies = []recipeDependency{{Name: "other"}}
	_, err := compileRecipe(d)
	var diagnostic *recipeDiagnostic
	if !errors.As(err, &diagnostic) || diagnostic.Code != "RECIPE_INVALID_DEPENDENCY" || diagnostic.Pointer != "/dependencies/0" {
		t.Fatalf("unsupported dependency diagnostic: %v", err)
	}
}

func TestRecipeTypedIntegerEqualityIsExact(t *testing.T) {
	d := recipeTypeTestDefinition(`{"op":"require","test":{"call":"eq","args":[{"literal":-0},{"literal":0}]}},{"op":"write","table":"items","row":{"ref":{"field":["ref"]}}}`)
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := p.evaluate(map[string]any{"ref": "r"})
	if err != nil || len(rows["items"]) != 1 {
		t.Fatalf("negative zero equality: %v %v", rows, err)
	}
	_, err = recipeEvaluateExpr(recipeExpr{Call: "eq", Args: []recipeExpr{{Literal: json.RawMessage("1.0")}, {Literal: json.RawMessage("1.00")}}}, nil, nil)
	if err == nil {
		t.Fatal("dynamic decimal equality must fail rather than compare spelling")
	}
}

func TestRecipeTypedNumbersUnionsAndGuardedMissing(t *testing.T) {
	steps := `{"op":"let","as":"value","value":{"call":"cbor.scalar","args":[{"call":"cbor.key","args":[{"call":"cbor.decode","args":[{"field":["metadata_hex"]}]},{"literal":"n"}]}]}},{"op":"require","test":{"call":"and","args":[{"call":"valid","args":[{"variable":"value"}]},{"call":"eq","args":[{"variable":"value"},{"literal":18446744073709551615}]}]}},{"op":"write","table":"items","row":{"ref":{"field":["ref"]},"value":{"variable":"value"}}}`
	d := recipeTypeTestDefinition(steps)
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		event    map[string]any
		expected int
	}{
		{map[string]any{"ref": "r", "metadata_hex": "a1616e1bffffffffffffffff"}, 1},
		{map[string]any{"ref": "r", "metadata_hex": "a1616e01"}, 0},
		{map[string]any{"ref": "r", "metadata_hex": "ff"}, 0},
		{map[string]any{"ref": "r"}, 0},
	} {
		rows, err := p.evaluate(tc.event)
		if err != nil || len(rows["items"]) != tc.expected {
			t.Fatalf("event %v: %v %v", tc.event, rows, err)
		}
		for _, row := range rows["items"] {
			if row["value"] != json.Number("18446744073709551615") {
				t.Fatalf("integer lost precision: %#v", row["value"])
			}
		}
	}
	typeInfo, err := recipeInferType(*d.Steps[0].Value, map[string]recipeStaticType{}, d.Source, "/test")
	if err != nil || !typeInfo.optional || !typeInfo.invalid || typeInfo.mask&recipeTypeInteger == 0 {
		t.Fatalf("status propagation: %+v %v", typeInfo, err)
	}
}

func TestRecipeCatalogueExistingLargeNumber(t *testing.T) {
	text := strings.Replace(strings.Replace(registryRecipe, `"content_type"`, `"height"`, 1), `"text/plain"`, `9007199254740993`, 1)
	d, err := parseRecipe([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	left, err := recipeInferType(d.Steps[0].Test.Args[0], nil, d.Source, "/left")
	if err != nil {
		t.Fatal(err)
	}
	right, err := recipeInferType(d.Steps[0].Test.Args[1], nil, d.Source, "/right")
	if err != nil {
		t.Fatal(err)
	}
	if left.mask != recipeTypeInteger || right.mask != recipeTypeInteger {
		t.Fatalf("not exact integers: left=%+v right=%+v definition=%s", left, right, text)
	}
	if _, err = compileRecipe(d); err != nil {
		t.Fatal(err)
	}
}
