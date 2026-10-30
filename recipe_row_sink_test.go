package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestRecipeRowSinkRetainsEventAtomicityAndBlockChecks(t *testing.T) {
	ctx := context.Background()
	base, err := builtinTransactionRecipe()
	if err != nil {
		t.Fatal(err)
	}
	block := recipeCoordinatorSource(t)[0]
	source, err := recipeTransactionSourceFromBlock(ctx, block)
	if err != nil {
		t.Fatal(err)
	}
	definition := base.definition
	definition.Steps = append(append([]recipeStep(nil), definition.Steps...), recipeStep{Op: "require", Test: &recipeExpr{Literal: json.RawMessage("false")}})
	filtered, err := compileRecipe(definition)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err = filtered.evaluateTransactionSourceInto(ctx, block, source, nil, func(recipeRowProposal) error { calls++; return nil }); err != nil || calls != 0 {
		t.Fatalf("write before false require escaped: %d %v", calls, err)
	}
	stop := errors.New("sink failure")
	if err = base.evaluateTransactionSourceInto(ctx, block, source, nil, func(recipeRowProposal) error { return stop }); !errors.Is(err, stop) {
		t.Fatal("sink error lost", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	if err = base.evaluateTransactionSourceInto(cancelled, block, source, nil, func(recipeRowProposal) error { cancel(); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation after final row lost", err)
	}

	// Executor-only trusted inventory: no raw verification or selected-chain
	// assertion is made by this synthetic duplicate-key vector.
	synthetic := block
	synthetic.TransactionCount = 2
	ids := &recipeTransactionSource{height: block.Height, hash: block.Hash, ids: []string{recipeTestHash("a"), recipeTestHash("b")}}
	definition = base.definition
	definition.Steps = append([]recipeStep(nil), definition.Steps...)
	definition.Steps[0].Row = map[string]recipeExpr{"txid": {Literal: json.RawMessage(`"same"`)}}
	duplicate, err := compileRecipe(definition)
	if err != nil {
		t.Fatal(err)
	}
	calls = 0
	if err = duplicate.evaluateTransactionSourceInto(ctx, synthetic, ids, nil, func(recipeRowProposal) error { calls++; return nil }); err == nil || calls != 1 {
		t.Fatalf("cross-event duplicate escaped: %d %v", calls, err)
	}
}
