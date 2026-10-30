package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Frozen 0.7.9 derivation oracle. Production derives through the JSON engine.
func transactionLocatorFromBlock(block blockView) *transactionLocatorPayload {
	count := uint32(len(block.Transactions))
	entries := make([]transactionLocatorEntry, 0, len(block.Transactions))
	for i, tx := range block.Transactions {
		entries = append(entries, transactionLocatorEntry{uint32(i), tx.TxID})
	}
	return &transactionLocatorPayload{Block: transactionLocatorBlock{Height: block.Height, TransactionCount: &count, Transactions: entries}, Completeness: "complete", Selection: "all"}
}

func TestRecipeBuiltinLocatorDerivationAndPhysicalParity(t *testing.T) {
	program, err := builtinTransactionRecipe()
	if err != nil {
		t.Fatal(err)
	}
	customDefinition := locatorRecipeProgram(t).definition
	customDefinition.Dependencies = []recipeDependency{}
	custom, err := compileRecipe(customDefinition)
	if err != nil {
		t.Fatal(err)
	}
	if program.semanticHash() != custom.semanticHash() {
		t.Fatal("built-in and custom recipe execution differ")
	}
	for _, block := range recipeCoordinatorSource(t) {
		block.Verification.HeaderChainMatch = true
		payload, err := transactionLocatorFromRecipe(context.Background(), block)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(payload, transactionLocatorFromBlock(block)) {
			t.Fatalf("payload mismatch: %+v", payload)
		}
		batch, err := singletonBrowseBatch("tx-locator", block, nil)
		if err != nil || !reflect.DeepEqual(batch.TransactionLocator, payload) {
			t.Fatalf("native browse did not use recipe: %+v %v", batch.TransactionLocator, err)
		}
	}
	definition, err := findIndexDefinition("tx-locator")
	if definition.RuleHash != builtinLocatorLegacyV2Rule || legacyIndexDefinition(definition).RuleHash != builtinLocatorLegacyV1Rule {
		t.Fatal("legacy identity changed")
	}
	if err != nil || definition.ExecutableRecipeHash != program.semanticHash() {
		t.Fatal("executable recipe not exposed", err)
	}
	d, err := parseRecipe(definition.ExecutableRecipe)
	if err != nil || d.Source != "bitcoin.transaction" {
		t.Fatal("executable definition missing", err)
	}
	// A semantic change changes the actual derivation, not just metadata.
	d.Steps = append([]recipeStep{{Op: "require", Test: &recipeExpr{Literal: json.RawMessage("false")}}}, d.Steps...)
	filtered, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	if err = recipeLocatorCheckEquivalence(definition.RuleHash, filtered); err == nil {
		t.Fatal("changed executable meaning reused a frozen rule identity")
	}
	block := recipeCoordinatorSource(t)[0]
	if _, err = transactionLocatorFromRecipeProgram(context.Background(), block, filtered); err == nil {
		t.Fatal("native append derived changed semantics into the old generation")
	}
	for _, rule := range []string{builtinLocatorLegacyV1Rule, builtinLocatorLegacyV2Rule} {
		if err = recipeLocatorCheckEquivalence(rule, program); err != nil {
			t.Fatal("proven legacy generation rejected", err)
		}
	}
	store, err := indexStoreHead(t.TempDir(), "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	store.definition.RuleHash = recipeTestHash("f")
	if err = store.commitPreparedIndexBatch(indexBatch{}, 0, genesisHashDisplay, 0, "ephemeral"); err == nil {
		t.Fatal("unbound generation published")
	}
	if _, err = os.Stat(filepath.Join(store.dir, "head.json")); !os.IsNotExist(err) {
		t.Fatal("failed equivalence check wrote a head", err)
	}
	if _, err = store.readBatch(recipeTestHash("e")); err == nil {
		t.Fatal("unbound generation reused")
	}
	source, err := recipeTransactionSourceFromBlock(context.Background(), block)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := filtered.evaluateTransactionSource(context.Background(), block, source)
	if err != nil || len(rows["transactions"]) != 0 {
		t.Fatal("predicate did not change execution", err)
	}
	if _, err = recipeLocatorPhysicalPayload(context.Background(), block.Height, block.Hash, block.TransactionCount, rows); err == nil {
		t.Fatal("partial native inventory accepted")
	}
}
