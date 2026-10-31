package main

import (
	"context"
	_ "embed"
	"fmt"
	"sync"
)

//go:embed recipes/builtins/txio-inputs.json
var builtinTXIOInputsJSON []byte

const recipeTXIOInputSourceProfile = "gateway-admitted-spending-inputs-v1"
const recipeTXIOInputsEquivalentSemantic = "f6be769c76c8c0fe8030405dcb5069f6035843c12aa1378ed907c27f82e5423d"

var builtinTXIOInputsOnce sync.Once
var builtinTXIOInputsProgram *recipeProgram
var builtinTXIOInputsSemantic string
var builtinTXIOInputsError error

func builtinTXIOInputsRecipe() (*recipeProgram, error) {
	builtinTXIOInputsOnce.Do(func() {
		d, err := parseRecipe(builtinTXIOInputsJSON)
		if err == nil {
			builtinTXIOInputsProgram, err = compileRecipe(d)
		}
		if err != nil {
			builtinTXIOInputsError = err
			return
		}
		builtinTXIOInputsSemantic = indexDigest(struct{ Source, Physical, Program string }{recipeTXIOInputSourceProfile, "gateway-spender-v0-positional-v3-inputs", builtinTXIOInputsProgram.semanticHash()})
	})
	return builtinTXIOInputsProgram, builtinTXIOInputsError
}

func recipeTXIOInputsCheckEquivalence(semantic string) error {
	if semantic != recipeTXIOInputsEquivalentSemantic {
		return fmt.Errorf("TXIO input recipe binding changed; explicit migration required (actual %s)", semantic)
	}
	return nil
}

// Native ingestion retains its existing admitted-input boundary. JSON derives
// input ordinals and raw outpoint rows; no creation proof or unspent assertion
// is synthesized by this source. The sparse public copy uses raw-owned events.
func deriveIndexSpendersContext(ctx context.Context, block blockView) (*indexSpenderBlock, error) {
	if ctx == nil || block.Height < 0 {
		return nil, fmt.Errorf("invalid spending input source")
	}
	p, err := builtinTXIOInputsRecipe()
	if err != nil {
		return nil, err
	}
	if err = recipeTXIOInputsCheckEquivalence(builtinTXIOInputsSemantic); err != nil {
		return nil, err
	}
	out := &indexSpenderBlock{Rows: []indexSpendRecord{}}
	current := indexSpendRecord{}
	emitted := false
	err = p.evaluateOrderedSparseTypedEventsInto(ctx, func(emit func(map[string]recipeOrderedValue) error) error {
		event := map[string]recipeOrderedValue{"height": rovUint(uint64(block.Height)), "block": rovText(block.Hash)}
		for ti, tx := range block.Transactions {
			event["kind"], event["txid"], event["tx_index"] = rovText("tx_begin"), rovText(tx.TxID), rovUint(uint64(ti))
			if err := emit(event); err != nil {
				return err
			}
			if tx.Coinbase {
				continue
			}
			for vin, in := range tx.Inputs {
				if err := ctx.Err(); err != nil {
					return err
				}
				if in.Coinbase || !validHash(in.PrevTxID) || ti == 0 {
					return fmt.Errorf("invalid spender input evidence")
				}
				current = indexSpendRecord{PrevTxID: in.PrevTxID, Vout: in.PrevVout, TxID: tx.TxID, TxIndex: ti, Vin: vin}
				event["kind"], event["prev_txid"], event["prev_vout"] = rovText("input"), rovText(in.PrevTxID), rovUint(uint64(in.PrevVout))
				emitted = false
				if err := emit(event); err != nil {
					return err
				}
				if !emitted {
					return fmt.Errorf("TXIO input recipe omitted an admitted input")
				}
			}
		}
		return ctx.Err()
	}, func(table, key string, value recipeOrderedValue) error {
		row := value.Object
		ref := inputCoordinate(current.Vin, current.TxIndex, block.Height)
		if emitted || table != "inputs" || key != ref || len(row) != 7 {
			return fmt.Errorf("TXIO input proposal differs from its source")
		}
		for name, want := range map[string]string{"ref": ref, "block_hash": block.Hash, "prev_txid": current.PrevTxID, "txid": current.TxID} {
			if row[name].Kind != "text" || row[name].Text != want {
				return fmt.Errorf("TXIO input proposal differs at %s", name)
			}
		}
		for name, want := range map[string]uint64{"prev_vout": uint64(current.Vout), "tx_index": uint64(current.TxIndex), "input_index": uint64(current.Vin)} {
			if row[name].Kind != "uint" || row[name].Uint != want {
				return fmt.Errorf("TXIO input proposal differs at %s", name)
			}
		}
		emitted = true
		out.Rows = append(out.Rows, indexSpendRecord{PrevTxID: row["prev_txid"].Text, Vout: uint32(row["prev_vout"].Uint), TxID: row["txid"].Text, TxIndex: int(row["tx_index"].Uint), Vin: int(row["input_index"].Uint)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
