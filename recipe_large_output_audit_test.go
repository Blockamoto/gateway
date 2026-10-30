package main

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

// A verified real source with a legal static annotation demonstrates why the
// prototype's whole-unit buffer cap is not a valid release output restriction.
func TestRecipeLargeOutputCurrentBoundaryAudit(t *testing.T) {
	if os.Getenv("GATEWAY_RECIPE_LARGE_OUTPUT_AUDIT") != "1" {
		t.Skip("opt-in real-source large sparse-output boundary audit")
	}
	ctx := context.Background()
	block, _ := recipeDependencyReferenceBlock(t)
	base := locatorRecipeProgram(t).definition
	base.ID, base.Name, base.Origin = "large-output-audit", "Large output audit", block.Height
	annotation, _ := json.Marshal(strings.Repeat("a", 16<<10))
	base.Steps[len(base.Steps)-1].Row["annotation"] = recipeExpr{Literal: annotation}
	p, err := compileRecipe(base)
	if err != nil {
		t.Fatal(err)
	}
	source, err := recipeTransactionSourceFromBlock(ctx, block)
	if err != nil {
		t.Fatal(err)
	}
	unit := recipeStoreUnit{Schema: 1, Fingerprint: p.semanticHash(), Height: block.Height, BlockHash: block.Hash, Rows: []recipeStoreRow{}}
	if err = p.evaluateTransactionSourceInto(ctx, block, source, nil, func(row recipeRowProposal) error {
		key, err := json.Marshal([]string{row.Table, row.Key})
		if err != nil {
			return err
		}
		unit.Rows = append(unit.Rows, recipeStoreRow{Key: string(key), Value: row.Encoded})
		return nil
	}); err != nil {
		t.Fatal("valid recipe source failed before persistence", err)
	}
	sort.Slice(unit.Rows, func(i, j int) bool { return unit.Rows[i].Key < unit.Rows[j].Key })
	encoded, err := json.Marshal(unit)
	if err != nil || len(encoded) <= recipeStoreUnitMaxBytes {
		t.Fatal("fixture did not exceed the provisional durable-unit boundary", len(encoded), err)
	}
	store, err := openRecipeStore(t.TempDir(), base.ID, unit.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Publish(ctx, unit, recipeTestGuard); err == nil {
		t.Fatal("audit expected the current prototype boundary; update this check with its streaming replacement")
	}
	t.Logf("verified real block %d: transactions=%d legal annotation_bytes=%d completed rows=%d canonical_unit_bytes=%d provisional_limit_bytes=%d persistence_error=%v", block.Height, len(block.Transactions), 16<<10, len(unit.Rows), len(encoded), recipeStoreUnitMaxBytes, err)
}
