package main

import (
	"context"
	_ "embed"
	"fmt"
	"sync"
)

//go:embed recipes/builtins/checked-fee-prefix.json
var builtinFeePrefixJSON []byte
var builtinFeePrefixOnce sync.Once
var builtinFeePrefixProgram *recipeProgram
var builtinFeePrefixSemantic string
var builtinFeePrefixError error

const recipeFeePrefixEquivalentSemantic = "c147da2ab49a7298f1afb473bddd6cc5fef571cde6e089c207cd0e6c6e2f8c1b"

func builtinFeePrefixRecipe() (*recipeProgram, error) {
	builtinFeePrefixOnce.Do(func() {
		d, e := parseRecipe(builtinFeePrefixJSON)
		if e == nil {
			builtinFeePrefixProgram, e = compileRecipe(d)
		}
		builtinFeePrefixError = e
		if e == nil {
			builtinFeePrefixSemantic = indexDigest(struct{ Program, Source, Arithmetic, Physical string }{builtinFeePrefixProgram.semanticHash(), recipeCoreFeeLookupProfile, "checked-uint64-add-v1", "gateway-satline-fees-v1-pages256-manifest1"})
		}
	})
	return builtinFeePrefixProgram, builtinFeePrefixError
}
func recipeFeePrefixCheckEquivalence(semantic string) error {
	if semantic != recipeFeePrefixEquivalentSemantic {
		return fmt.Errorf("Core fee prefix JSON/source/codec changed; explicit migration required (actual %s)", semantic)
	}
	return nil
}
func recipeCheckedFeePrefix(ctx context.Context, block blockView, fees []uint64) ([]uint64, error) {
	if ctx == nil || len(fees) != len(block.Transactions) || len(fees) > satFeeMaxTransactions || len(fees) > 0 && fees[0] != 0 {
		return nil, fmt.Errorf("checked fee layout differs")
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if len(fees) == 0 {
		return []uint64{0}, nil
	}
	p, e := builtinFeePrefixRecipe()
	if e != nil {
		return nil, e
	}
	if e = recipeFeePrefixCheckEquivalence(builtinFeePrefixSemantic); e != nil {
		return nil, e
	}
	state := &recipeNativeFeesState{block: block, checkedFees: fees, rows: make([]satFeeRow, len(fees))}
	e = recipeOrderedExecuteStateStream(ctx, *p.ordered, state, func(emit func(map[string]recipeOrderedValue) error) error {
		for ti, tx := range block.Transactions {
			event := map[string]recipeOrderedValue{"kind": rovText("tx_begin"), "height": rovUint(uint64(block.Height)), "block": rovText(block.Hash), "txid": rovText(tx.TxID), "tx_index": rovUint(uint64(ti)), "coinbase": rovBool(ti == 0)}
			if err := emit(event); err != nil {
				return err
			}
		}
		return nil
	}, nil)
	if e != nil {
		return nil, e
	}
	if state.next != len(fees) {
		return nil, fmt.Errorf("fee prefix omitted rows")
	}
	prefix := make([]uint64, len(fees)+1)
	for i, row := range state.rows {
		prefix[i] = row.Before
	}
	prefix[len(fees)] = state.total
	return prefix, nil
}
