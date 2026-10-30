package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecipeOrderedDocumentCompilesNativeAndCustomThroughSameContract(t *testing.T) {
	d, err := parseRecipe(builtinUTXODefinitionJSON)
	if err != nil {
		t.Fatal(err)
	}
	native, err := compileRecipe(d)
	if err != nil || native.ordered == nil {
		t.Fatal("native complete ordered document unavailable", err)
	}
	d.ID, d.Name = "my-coins", "My coin state"
	pretty, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	custom, err := compileRecipe(d)
	if err != nil || custom.ordered == nil {
		t.Fatal(err)
	}
	if custom.semanticHash() != native.semanticHash() || recipeResumeProgramID(*custom.ordered) != recipeResumeProgramID(*native.ordered) {
		t.Fatal("instance identity changed ordered execution")
	}
	view, canonical, err := recipeRegistryCompile(pretty)
	if err != nil || !view.Validated || view.SemanticHash != native.semanticHash() || len(canonical) == 0 {
		t.Fatal("ordered draft did not pass common registry compilation", err)
	}
	parsed, err := parseRecipe(canonical)
	if err != nil {
		t.Fatal(err)
	}
	again, err := compileRecipe(parsed)
	if err != nil || again.semanticHash() != custom.semanticHash() {
		t.Fatal("ordered document round trip changed semantics", err)
	}
	if _, err = loadBuiltinUTXOProgram(); err != nil {
		t.Fatal(err)
	}
	if recipeNativeUTXOCheckGeneration() != nil {
		t.Fatal("full document changed the bound native AST")
	}
}

func TestRecipeOrderedDocumentRejectsUnknownBindingsAndMixedModes(t *testing.T) {
	d, err := parseRecipe(builtinUTXODefinitionJSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"origin", "history", "storage", "tables", "field", "state", "output", "steps", "dependencies"} {
		raw, _ := json.Marshal(d)
		copy, err := parseRecipe(raw)
		if err != nil {
			t.Fatal(err)
		}
		switch bad {
		case "origin":
			copy.Origin = 1
		case "history":
			copy.History = "sparse"
		case "storage":
			copy.Storage = "dispatch-utxo-index"
		case "tables":
			copy.Tables[0].Key = "other"
		case "field":
			copy.Ordered = []byte(strings.Replace(string(copy.Ordered), `"kind"`, `"not_a_source_field"`, 1))
		case "state":
			copy.Ordered = []byte(strings.Replace(string(copy.Ordered), `"coins"`, `"unknown_table"`, 1))
		case "output":
			copy.Ordered = []byte(strings.Replace(string(copy.Ordered), `"prevouts"`, `"unknown_output"`, 1))
		case "steps":
			copy.Steps = []recipeStep{{Op: "write"}}
		case "dependencies":
			copy.Dependencies = []recipeDependency{{Name: "future"}}
		}
		if _, err = compileRecipe(copy); err == nil {
			t.Fatalf("accepted invalid ordered %s binding", bad)
		}
	}
	d.Source = "bitcoin.reveal"
	d.History = "sparse"
	if _, err = compileRecipe(d); err == nil {
		t.Fatal("mixed sparse and ordered instruction sets")
	}
}

func TestRecipeStructuralKeysAreExactAndLiteralKeysRemainUserData(t *testing.T) {
	raw := string(builtinTransactionRecipeJSON)
	for _, bad := range []string{strings.Replace(raw, `"source"`, `"Source"`, 1), strings.Replace(raw, `"source":`, `"Source":"bitcoin.reveal","source":`, 1), strings.Replace(raw, `"key"`, `"Key"`, 1), strings.Replace(raw, `"origin": 0`, `"origin": null`, 1)} {
		if _, err := parseRecipe([]byte(bad)); err == nil {
			t.Fatal("ambiguous structural spelling/null accepted")
		}
	}
	d := recipeTypeTestDefinition(`{"op":"write","table":"items","row":{"ref":{"field":["ref"]},"user":{"literal":{"Title":"Kept","title":"distinct"}}}}`)
	if _, err := compileRecipe(d); err != nil {
		t.Fatal("literal object keys were treated as structural fields", err)
	}
}
