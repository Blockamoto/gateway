package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestRecipeSavedInscriptionIDsIdentity(t *testing.T) {
	_, err := builtinSavedInscriptionIDsRecipe()
	if err != nil {
		t.Fatal(err)
	}
	d, err := findIndexDefinition(inscriptionLocatorIndex)
	if err != nil {
		t.Fatal(err)
	}
	if d.RuleHash != builtinSavedInscriptionIDsRule || legacyIndexDefinition(d).RuleHash != builtinSavedInscriptionIDsLegacyRule || recipeSavedInscriptionIDsCheckStore(d) != nil {
		t.Fatal("saved inscription native identity changed")
	}
	if recipeSavedInscriptionIDsCheckEquivalence(d.RuleHash, "changed") == nil || recipeSavedInscriptionIDsCheckEquivalence("changed", builtinSavedInscriptionIDsSemantic) == nil {
		t.Fatal("changed saved projection entered native generation")
	}
}

func TestRecipeTableIdenticalRowsAndConflicts(t *testing.T) {
	program, err := builtinTransactionRecipe()
	if err != nil {
		t.Fatal(err)
	}
	definition := program.definition
	definition.Tables = []recipeTable{{Name: "rows", Key: "ref", OnConflict: "identical"}}
	definition.Steps = []recipeStep{{Op: "write", Table: "rows", Row: map[string]recipeExpr{"ref": {Literal: json.RawMessage(`"same"`)}, "value": {Literal: json.RawMessage(`1`)}}}}
	definition.Steps = append(definition.Steps, definition.Steps[0])
	distinct, err := compileRecipe(definition)
	if err != nil {
		t.Fatal(err)
	}
	block := recipeCoordinatorSource(t)[0]
	rows, err := distinct.evaluateTransactionBlock(context.Background(), block)
	if err != nil || len(rows["rows"]) != 1 {
		t.Fatal("identical event rows not collapsed", err)
	}
	block, _ = recipeDependencyReferenceBlock(t)
	rows, err = distinct.evaluateTransactionBlock(context.Background(), block)
	if err != nil || len(rows["rows"]) != 1 {
		t.Fatal("identical source rows not collapsed", err)
	}
	conflict := definition
	conflict.Steps = append([]recipeStep(nil), definition.Steps...)
	conflict.Steps[1] = recipeStep{Op: "write", Table: "rows", Row: map[string]recipeExpr{"ref": {Literal: json.RawMessage(`"same"`)}, "value": {Literal: json.RawMessage(`2`)}}}
	p, err := compileRecipe(conflict)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.evaluateTransactionBlock(context.Background(), block); err == nil {
		t.Fatal("conflicting same-event values accepted")
	}
	conflict.Steps = []recipeStep{{Op: "write", Table: "rows", Row: map[string]recipeExpr{"ref": {Literal: json.RawMessage(`"same"`)}, "value": {Field: []string{"txid"}}}}}
	p, err = compileRecipe(conflict)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.evaluateTransactionBlock(context.Background(), block); err == nil {
		t.Fatal("conflicting cross-event values accepted")
	}
	strict := definition
	strict.Tables = []recipeTable{{Name: "rows", Key: "ref"}}
	p, err = compileRecipe(strict)
	if err != nil {
		t.Fatal(err)
	}
	if p.semanticHash() == distinct.semanticHash() {
		t.Fatal("conflict policy did not bind semantics")
	}
	if _, err = p.evaluateTransactionBlock(context.Background(), block); err == nil {
		t.Fatal("default policy stopped rejecting duplicate rows")
	}
	strict.Tables[0].OnConflict = "overwrite"
	if _, err = compileRecipe(strict); err == nil {
		t.Fatal("unsupported overwrite policy accepted")
	}
}

func TestRecipeTableIdenticalStreamOwnsComparisonBytes(t *testing.T) {
	definition := recipeDefinition{Tables: []recipeTable{{Name: "rows", Key: "ref", OnConflict: "identical"}}}
	calls := 0
	stream := newRecipeRowStream(definition, func(row recipeRowProposal) error { calls++; row.Encoded[0] = '!'; return nil })
	for i := 0; i < 2; i++ {
		if err := stream.add(recipeRowProposal{Table: "rows", Key: "one", Encoded: []byte(`{"ref":"one"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("borrowed sink buffer changed distinct comparison")
	}
}

func TestRecipeTableIdenticalDuplicatesStillValidateExports(t *testing.T) {
	base, err := builtinTransactionRecipe()
	if err != nil {
		t.Fatal(err)
	}
	d := base.definition
	d.Tables = []recipeTable{{Name: "rows", Key: "ref", OnConflict: "identical"}}
	d.Exports = []recipeExport{{Name: "number", Kind: "same_block", Table: "rows", Column: "value", Type: "integer"}}
	d.Steps = nil
	for _, field := range []string{"height", "tx_index"} {
		d.Steps = append(d.Steps, recipeStep{Op: "write", Table: "rows", Row: map[string]recipeExpr{"ref": {Literal: json.RawMessage(`"one"`)}, "value": {Field: []string{field}}}})
	}
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	// Malformed runtime source: equal JSON bytes, different scalar types.
	event := map[string]any{"height": json.Number("1"), "tx_index": float64(1)}
	if _, _, err = p.evaluateRowProposals(event, nil); err == nil {
		t.Fatal("duplicate row bypassed export type validation")
	}
	d.Steps = append(d.Steps, recipeStep{Op: "require", Test: &recipeExpr{Literal: json.RawMessage("false")}})
	p, err = compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	rows, matched, err := p.evaluateRowProposals(event, nil)
	if err != nil || matched || len(rows) != 0 {
		t.Fatal("late rejection did not discard duplicate export checks", err)
	}
}

func TestRecipeSavedInscriptionIDsProjectionParity(t *testing.T) {
	for _, mode := range []string{"lean", "full"} {
		for _, populated := range []bool{false, true} {
			_, source, block := positionalInscriptionFixture(t, t.TempDir(), mode, 840000, recipeTestHash("a"), recipeTestHash("b"), populated)
			for _, available := range []bool{false, true} {
				var raw *blockView
				if available {
					raw = &block
				}
				old, a := sparseTransactionsFromInscriptionOracle(source, raw)
				next, b := sparseTransactionsFromInscription(source, raw)
				if (a == nil) != (b == nil) || a == nil && !reflect.DeepEqual(old, next) {
					t.Fatalf("saved projection differs mode=%s populated=%t source=%t: %v / %v", mode, populated, available, a, b)
				}
			}
		}
	}
}

func TestRecipeSavedInscriptionIDsCancellation(t *testing.T) {
	_, source, block := positionalInscriptionFixture(t, t.TempDir(), "full", 840000, recipeTestHash("a"), recipeTestHash("b"), true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if payload, err := sparseTransactionsFromInscriptionContext(ctx, source, &block); !errors.Is(err, context.Canceled) || payload != nil {
		t.Fatal("cancelled projection returned a payload", err)
	}
}

func TestRecipeSavedInscriptionIDsRealAndCustomParity(t *testing.T) {
	ctx := context.Background()
	block, inventory := recipeDependencyReferenceBlock(t)
	block.Verification.HeaderChainMatch = true // local fixture admission, not external authority
	for _, mode := range []string{"lean", "full"} {
		source, err := singletonBrowseBatchMode(indexRunSelection{Index: "inscriptions", indexRunOptions: indexRunOptions{Mode: mode, ConventionalIDs: boolPointer(false)}}, block, inventory)
		if err != nil {
			t.Fatal(err)
		}
		old, err := sparseTransactionsFromInscriptionOracle(source, &block)
		if err != nil {
			t.Fatal(err)
		}
		next, err := sparseTransactionsFromInscription(source, &block)
		if err != nil || !reflect.DeepEqual(old, next) {
			t.Fatal("real saved projection differs", err)
		}
		if mode == "full" {
			program, err := builtinSavedInscriptionIDsRecipe()
			if err != nil {
				t.Fatal(err)
			}
			definition := program.definition
			definition.ID, definition.Name = "my-reveal-ids", "My reveal IDs"
			custom, err := compileRecipe(definition)
			if err != nil {
				t.Fatal(err)
			}
			if custom.semanticHash() != program.semanticHash() {
				t.Fatal("custom copy changed table semantics")
			}
			store, err := openRecipeStore(t.TempDir(), definition.ID, custom.semanticHash())
			if err != nil {
				t.Fatal(err)
			}
			if err = custom.commitInscriptionBlock(ctx, store, block, inventory, func(publish func() error) error { return publish() }); err != nil {
				t.Fatal(err)
			}
			unit, found, err := store.Read(ctx, block.Height)
			if err != nil || !found || len(unit.Rows) != len(next.Block.Transactions) {
				t.Fatal("custom identical-row output differs from native", err)
			}
		}
	}
}
