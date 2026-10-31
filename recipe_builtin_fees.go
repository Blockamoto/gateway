package main

import (
	"context"
	_ "embed"
	"fmt"
	"strconv"
	"sync"
)

//go:embed recipes/builtins/transaction-fees.json
var builtinFeesJSON []byte

var builtinFeesOnce sync.Once
var builtinFeesProgram *recipeProgram
var builtinFeesSemantic string
var builtinFeesError error

const recipeFeesEquivalentSemantic = "19fa54527affa93d4d5f83fb2f3ff542e4eeab2ffc944fd78fb3a07026540903"

func builtinFeesRecipe() (*recipeProgram, error) {
	builtinFeesOnce.Do(func() {
		d, err := parseRecipe(builtinFeesJSON)
		if err == nil {
			builtinFeesProgram, err = compileRecipe(d)
		}
		builtinFeesError = err
		if err == nil {
			builtinFeesSemantic = indexDigest(struct{ Program, Source, Arithmetic, Physical string }{builtinFeesProgram.semanticHash(), recipeInputValueLookupProfile, "checked-uint64-add-sub-v1", "gateway-satline-fees-v1-pages256-manifest1"})
		}
	})
	return builtinFeesProgram, builtinFeesError
}
func recipeFeesCheckEquivalence(semantic string) error {
	if semantic != recipeFeesEquivalentSemantic {
		return fmt.Errorf("fee JSON/source/codec changed; explicit migration required (actual %s)", semantic)
	}
	return nil
}

// This adapter supplies checked input amounts and accepts fee row proposals.
// Fee arithmetic and prefix policy live in the same saved/custom JSON program.
type recipeNativeFeesState struct {
	block       blockView
	values      [][]uint64
	checkedFees []uint64
	rows        []satFeeRow
	next        int
	total       uint64
	aborted     bool
}

func (s *recipeNativeFeesState) AbortOrdered() { s.aborted = true }
func (s *recipeNativeFeesState) DeleteOrdered(context.Context, recipeStateKey) error {
	return fmt.Errorf("fee codec does not delete rows")
}
func (s *recipeNativeFeesState) ReadOrdered(ctx context.Context, key recipeStateKey) (recipeOrderedValue, error) {
	if err := ctx.Err(); err != nil {
		return recipeOrderedValue{}, err
	}
	if key.Table == "transaction_fee" {
		p, ok := parseBODCoordinate(key.Key)
		if s.aborted || !ok || p.Kind != coordTransaction || p.TxID != "" || p.Height != s.block.Height || p.TxIndex < 0 || p.TxIndex >= len(s.checkedFees) || key.Key != strconv.Itoa(p.TxIndex)+"."+strconv.FormatInt(p.Height, 10) {
			return recipeOrderedValue{}, fmt.Errorf("checked fee source position differs")
		}
		return recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"fee_sats": rovUint(s.checkedFees[p.TxIndex])}}, nil
	}
	p, e := parseTXIOPosition("input", key.Key)
	if e != nil || s.aborted || key.Table != "input_value" || p.Height != s.block.Height || p.TxIndex < 1 || p.TxIndex >= len(s.values) || p.IOIndex >= len(s.values[p.TxIndex]) {
		return recipeOrderedValue{}, fmt.Errorf("fee input source differs from admitted position")
	}
	return recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"value": rovUint(s.values[p.TxIndex][p.IOIndex])}}, nil
}
func (s *recipeNativeFeesState) PutOrdered(ctx context.Context, key recipeStateKey, v recipeOrderedValue) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.aborted || v.Kind != "object" {
		return fmt.Errorf("fee row codec unavailable")
	}
	row := v.Object
	if key.Table == "totals" {
		if s.next < 1 || len(row) != 3 || key.Key != s.block.Hash || row["height"].Kind != "uint" || row["height"].Uint != uint64(s.block.Height) || row["block"].Kind != "text" || row["block"].Text != s.block.Hash || row["total_fees_sats"].Kind != "uint" {
			return fmt.Errorf("fee total proposal identity differs")
		}
		want, e := addUint64(s.rows[s.next-1].Before, s.rows[s.next-1].Fee)
		if e != nil || row["total_fees_sats"].Uint != want {
			return fmt.Errorf("fee total proposal differs from rows")
		}
		s.total = want
		return nil
	}
	if key.Table != "fees" || s.next >= len(s.rows) || key.Key != strconv.Itoa(s.next)+"."+strconv.FormatInt(s.block.Height, 10) || len(row) != 6 {
		return fmt.Errorf("fee row proposal position differs")
	}
	for _, name := range []string{"height", "tx_index", "fee_sats", "prior_fees_sats"} {
		if row[name].Kind != "uint" {
			return fmt.Errorf("fee row integer differs")
		}
	}
	for _, name := range []string{"block", "txid"} {
		if row[name].Kind != "text" {
			return fmt.Errorf("fee row text differs")
		}
	}
	if row["height"].Uint != uint64(s.block.Height) || row["tx_index"].Uint != uint64(s.next) || row["block"].Text != s.block.Hash || row["txid"].Text != s.block.Transactions[s.next].TxID || row["prior_fees_sats"].Uint != s.total || s.next == 0 && row["fee_sats"].Uint != 0 {
		return fmt.Errorf("fee row identity/prefix differs")
	}
	s.rows[s.next] = satFeeRow{Fee: row["fee_sats"].Uint, Before: row["prior_fees_sats"].Uint}
	s.next++
	return nil
}

func recipeNativeFeeRows(ctx context.Context, block blockView, values [][]uint64) ([]satFeeRow, uint64, error) {
	if ctx == nil || len(values) != len(block.Transactions) {
		return nil, 0, fmt.Errorf("fee input transaction count mismatch")
	}
	p, e := builtinFeesRecipe()
	if e != nil {
		return nil, 0, e
	}
	if e = recipeFeesCheckEquivalence(builtinFeesSemantic); e != nil {
		return nil, 0, e
	}
	s := &recipeNativeFeesState{block: block, values: values, rows: make([]satFeeRow, len(block.Transactions))}
	e = recipeOrderedExecuteStateStream(ctx, *p.ordered, s, func(emit func(map[string]recipeOrderedValue) error) error {
		for ti, tx := range block.Transactions {
			if err := ctx.Err(); err != nil {
				return err
			}
			if tx.Index != ti || tx.Coinbase != (ti == 0) || !validHash(tx.TxID) {
				return fmt.Errorf("fee transaction order mismatch")
			}
			if ti == 0 && len(values[ti]) != 0 {
				return fmt.Errorf("coinbase has no ordinary fee inputs")
			}
			if ti > 0 && (len(values[ti]) != len(tx.Inputs) || len(tx.Inputs) == 0) {
				return fmt.Errorf("fee ordered input values incomplete")
			}
			common := func(kind string) map[string]recipeOrderedValue {
				return map[string]recipeOrderedValue{"kind": rovText(kind), "height": rovUint(uint64(block.Height)), "block": rovText(block.Hash), "txid": rovText(tx.TxID), "tx_index": rovUint(uint64(ti)), "coinbase": rovBool(ti == 0)}
			}
			if err := emit(common("tx_begin")); err != nil {
				return err
			}
			if ti > 0 {
				for vin, in := range tx.Inputs {
					if in.Coinbase {
						return fmt.Errorf("ordinary fee transaction has coinbase input")
					}
					event := common("input")
					event["input_ref"] = rovText(inputCoordinate(vin, ti, block.Height))
					if err := emit(event); err != nil {
						return err
					}
				}
				for _, out := range tx.Outputs {
					event := common("output")
					event["value"] = rovUint(out.ValueSats)
					if err := emit(event); err != nil {
						return err
					}
				}
			}
			if err := emit(common("tx_end")); err != nil {
				return err
			}
		}
		return nil
	}, nil)
	if e == nil && s.next != len(s.rows) {
		e = fmt.Errorf("fee recipe omitted rows")
	}
	return s.rows, s.total, e
}
