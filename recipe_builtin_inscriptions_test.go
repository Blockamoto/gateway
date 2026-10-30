package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

// Frozen pre-recipe retention projection; source admission is tested separately.
func inscriptionPayloadLegacyOracle(block blockView, result *inscriptionBlockResult, mode string, ids bool) indexBatch {
	b := indexBatch{InscriptionBlock: &inscriptionBlockMetadata{Mode: mode, TransactionCount: uint32(len(block.Transactions))}, InscriptionCoordinates: []string{}}
	for _, o := range result.Occurrences {
		b.InscriptionCoordinates = append(b.InscriptionCoordinates, inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height))
	}
	if mode == "full" {
		b.Inscriptions = append([]inscriptionOccurrence(nil), result.Occurrences...)
	}
	if ids {
		seen := map[int]bool{}
		for _, o := range result.Occurrences {
			if !seen[o.TxIndex] {
				b.InscriptionBlock.ConventionalIDs = append(b.InscriptionBlock.ConventionalIDs, transactionLocatorEntry{TxIndex: uint32(o.TxIndex), TxID: o.TxID})
				seen[o.TxIndex] = true
			}
		}
	}
	return b
}

func TestRecipeBuiltinInscriptionsPayloadParity(t *testing.T) {
	ctx := context.Background()
	genesis, empty := recipeConsumerGenesis(t)
	real, inventory := recipeDependencyReferenceBlock(t)
	synthetic := inscriptionTestBlock()
	script := inscriptionTestScript(nil, []byte("retained body"))
	tx := inscriptionTestTx(append(append([]byte{}, script...), script...))
	tx.Index = 1
	synthetic.Transactions = append(synthetic.Transactions, tx)
	synthetic.TransactionCount = uint64(len(synthetic.Transactions))
	extracted, err := extractInscriptionOccurrences(synthetic)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := []struct {
		block  blockView
		result *inscriptionBlockResult
	}{{genesis, empty}, {real, inventory}, {synthetic, &extracted}}
	if path := os.Getenv("GATEWAY_RECIPE_REFERENCE_BLOCK"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		b, err := recipeVerifiedParse(ctx, 908657, "00000000000000000001d253a31be91698f02d4851078e3cd5b4e29048219d26", raw)
		if err != nil {
			t.Fatal(err)
		}
		r, err := extractInscriptionOccurrences(b)
		if err != nil {
			t.Fatal(err)
		}
		fixtures = append(fixtures, struct {
			block  blockView
			result *inscriptionBlockResult
		}{b, &r})
	}
	for _, f := range fixtures {
		for _, mode := range []string{"lean", "full"} {
			for _, ids := range []bool{false, true} {
				got, err := inscriptionPayloadFromRecipes(ctx, f.block, f.result, mode, ids)
				if err != nil {
					t.Fatal(err)
				}
				want := inscriptionPayloadLegacyOracle(f.block, f.result, mode, ids)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("retention differs at height %d mode %s ids %t", f.block.Height, mode, ids)
				}
				if indexDigest(got) != indexDigest(want) {
					t.Fatal("retention wire bytes differ")
				}
			}
		}
	}
}

func TestRecipeBuiltinInscriptionsIdentityAndCustomRecord(t *testing.T) {
	programs, err := builtinInscriptionRecipes()
	if err != nil {
		t.Fatal(err)
	}
	d, err := findIndexDefinition("inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	legacy := legacyIndexDefinition(d)
	if d.RuleHash != builtinInscriptionLegacyV2Rule || legacy.RuleHash != builtinInscriptionLegacyV1Rule || len(d.ExecutableRecipeVariants) != 4 || recipeInscriptionCheckStoreDefinition(d) != nil {
		t.Fatal("native identity or recipe variants changed")
	}
	if recipeInscriptionCheckEquivalence(d.RuleHash, "changed") == nil || recipeInscriptionCheckEquivalence("changed", builtinInscriptionSemantic) == nil {
		t.Fatal("unbound generation accepted")
	}
	definition := programs[1].definition
	definition.ID, definition.Name = "custom-full", "Custom full"
	custom, err := compileRecipe(definition)
	if err != nil {
		t.Fatal(err)
	}
	if custom.semanticHash() != programs[1].semanticHash() {
		t.Fatal("display copy changed execution")
	}
	block, inventory := recipeDependencyReferenceBlock(t)
	rows, err := custom.evaluateInscriptionBlock(context.Background(), block, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows["positions"]) != len(inventory.Occurrences) || len(inventory.Occurrences) == 0 {
		t.Fatal("custom full recipe omitted reveals")
	}
	for _, o := range inventory.Occurrences {
		key := inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height)
		wire, err := json.Marshal(rows["positions"][key]["record"])
		if err != nil {
			t.Fatal(err)
		}
		want, _ := json.Marshal(o)
		if string(wire) != string(want) {
			t.Fatal("custom source record changed bytes")
		}
	}
	definition.Steps = append([]recipeStep{{Op: "require", Test: &recipeExpr{Literal: json.RawMessage("false")}}}, definition.Steps...)
	filtered, err := compileRecipe(definition)
	if err != nil {
		t.Fatal(err)
	}
	if filtered.semanticHash() == custom.semanticHash() {
		t.Fatal("predicate change retained generation")
	}
	changedBundle := indexDigest([]string{programs[0].semanticHash(), filtered.semanticHash(), programs[2].semanticHash()})
	if recipeInscriptionCheckEquivalence(d.RuleHash, changedBundle) == nil {
		t.Fatal("changed predicate reused native generation")
	}
	rows, err = filtered.evaluateInscriptionBlock(context.Background(), block, inventory)
	if err != nil || len(rows["positions"]) != 0 {
		t.Fatal("custom predicate did not execute", err)
	}
}

func TestRecipeBuiltinInscriptionsSourceAndSinkFailures(t *testing.T) {
	ctx := context.Background()
	block, inventory := recipeDependencyReferenceBlock(t)
	for _, damage := range []string{"receipt", "position", "body", "block"} {
		copy := *inventory
		copy.Occurrences = append([]inscriptionOccurrence(nil), inventory.Occurrences...)
		source := block
		switch damage {
		case "receipt":
			copy.inventoryReceipt = nil
		case "position":
			copy.Occurrences[0].Index++
		case "body":
			copy.Occurrences[0].Body = append([]byte("changed"), copy.Occurrences[0].Body...)
		case "block":
			source.Hash = recipeTestHash("e")
		}
		if _, err := inscriptionPayloadFromRecipes(ctx, source, &copy, "full", true); err == nil {
			t.Fatalf("accepted changed %s", damage)
		}
	}
	programs, err := builtinInscriptionRecipes()
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("sink stopped")
	if err = programs[0].evaluateInscriptionSourceInto(ctx, block, inventory, nil, func(recipeRowProposal) error { return stop }); !errors.Is(err, stop) {
		t.Fatal("sink error lost", err)
	}
	definition := programs[1].definition
	definition.Steps = append(append([]recipeStep(nil), definition.Steps...), recipeStep{Op: "require", Test: &recipeExpr{Literal: json.RawMessage("false")}})
	filtered, err := compileRecipe(definition)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err = filtered.evaluateInscriptionSourceInto(ctx, block, inventory, nil, func(recipeRowProposal) error { calls++; return nil }); err != nil || calls != 0 {
		t.Fatal("late filter emitted source records", calls, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = inscriptionPayloadFromRecipes(cancelled, block, inventory, "full", true); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}

func TestRecipeBuiltinFullCustomDurableRows(t *testing.T) {
	ctx := context.Background()
	block, inventory := recipeDependencyReferenceBlock(t)
	programs, err := builtinInscriptionRecipes()
	if err != nil {
		t.Fatal(err)
	}
	definition := programs[1].definition
	definition.ID, definition.Name = "custom-full", "My retained reveals"
	program, err := compileRecipe(definition)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	store, err := openRecipeStore(root, definition.ID, program.semanticHash())
	if err != nil {
		t.Fatal(err)
	}
	if err = program.commitInscriptionBlock(ctx, store, block, inventory, func(publish func() error) error { return publish() }); err != nil {
		t.Fatal(err)
	}
	store, err = openRecipeStore(root, definition.ID, program.semanticHash())
	if err != nil {
		t.Fatal(err)
	}
	unit, found, err := store.Read(ctx, block.Height)
	if err != nil || !found || len(unit.Rows) != len(inventory.Occurrences) {
		t.Fatal("durable custom records unavailable", found, err)
	}
	for _, row := range unit.Rows {
		var value struct {
			Ref    string                `json:"ref"`
			Record inscriptionOccurrence `json:"record"`
		}
		if err = json.Unmarshal(row.Value, &value); err != nil {
			t.Fatal(err)
		}
		if value.Ref != inscriptionCoordinate(uint32(value.Record.TxIndex), value.Record.Index, block.Height) {
			t.Fatal("durable record lost its position")
		}
	}
}
