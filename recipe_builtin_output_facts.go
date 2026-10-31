package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
)

// These are independent retained layers. The program chooses the exact
// projected fields; the adapter only writes individual physical fact rows.
//
//go:embed recipes/builtins/output-values.json
var builtinOutputValuesJSON []byte

//go:embed recipes/builtins/output-scripts.json
var builtinOutputScriptsJSON []byte

//go:embed recipes/builtins/output-values-scripts.json
var builtinOutputValuesScriptsJSON []byte

const recipeOutputFactSourceProfile = "gateway-admitted-output-inventory-v1"
const recipeOutputFactEquivalentSemantic = "e2d02c262d402fa108e3f98166d9fc50abb50ba0df27e64c72da57feecfdf830"

var builtinOutputFactOnce sync.Once
var builtinOutputFactPrograms [3]*recipeProgram
var builtinOutputFactSemantic string
var builtinOutputFactError error

func builtinOutputFactRecipes() ([3]*recipeProgram, error) {
	builtinOutputFactOnce.Do(func() {
		for i, raw := range [][]byte{builtinOutputValuesJSON, builtinOutputScriptsJSON, builtinOutputValuesScriptsJSON} {
			d, err := parseRecipe(raw)
			if err == nil {
				builtinOutputFactPrograms[i], err = compileRecipe(d)
			}
			if err != nil {
				builtinOutputFactError = err
				return
			}
		}
		builtinOutputFactSemantic = indexDigest(struct {
			Source, Physical string
			Schema           int
			Programs         []string
		}{recipeOutputFactSourceProfile, "gateway-shared-output-facts-v1", 1, []string{builtinOutputFactPrograms[0].semanticHash(), builtinOutputFactPrograms[1].semanticHash(), builtinOutputFactPrograms[2].semanticHash()}})
	})
	return builtinOutputFactPrograms, builtinOutputFactError
}

func recipeOutputFactCheckEquivalence(semantic string, schema int) error {
	if schema != 1 || semantic != recipeOutputFactEquivalentSemantic {
		return fmt.Errorf("output fact recipe/codec binding changed; explicit migration required (actual %s)", semantic)
	}
	return nil
}

func recipeOutputFactVisit(ctx context.Context, block blockView, values, scripts bool, visit func(outputFactRow) error) error {
	if ctx == nil || visit == nil || !values && !scripts {
		return fmt.Errorf("invalid output fact projection")
	}
	programs, err := builtinOutputFactRecipes()
	if err != nil {
		return err
	}
	if err = recipeOutputFactCheckEquivalence(builtinOutputFactSemantic, 1); err != nil {
		return err
	}
	variant := 0
	if scripts {
		variant = 1
	}
	if values && scripts {
		variant = 2
	}
	var current outputFactRow
	var currentScript []byte
	emitted := false
	return programs[variant].evaluateOrderedSparseTypedEventsInto(ctx, func(emit func(map[string]recipeOrderedValue) error) error {
		// The synchronous interpreter keeps field values, never the source map.
		// Reuse its envelope while every newly decoded byte value stays owned.
		event := map[string]recipeOrderedValue{"kind": rovText("output"), "height": rovUint(uint64(block.Height)), "block": rovText(block.Hash)}
		for ti, tx := range block.Transactions {
			if tx.Coinbase != (ti == 0) {
				return fmt.Errorf("output facts require verified coinbase position")
			}
			for oi, out := range tx.Outputs {
				if err := ctx.Err(); err != nil {
					return err
				}
				if out.N != oi {
					return fmt.Errorf("output facts require canonical output order")
				}
				current = outputFactRow{TxIndex: ti, Vout: uint32(oi)}
				event["tx_index"], event["vout"] = rovUint(uint64(ti)), rovUint(uint64(oi))
				if values {
					v := out.ValueSats
					current.Value = &v
					event["value"] = rovUint(v)
				}
				if scripts {
					decoded, err := hex.DecodeString(out.ScriptPubKey)
					if err != nil || strings.ToLower(out.ScriptPubKey) != out.ScriptPubKey {
						return fmt.Errorf("output facts require exact script bytes")
					}
					script := out.ScriptPubKey
					current.Script = &script
					currentScript = decoded
					event["script"] = recipeOrderedValue{Kind: "bytes", Bytes: decoded}
				}
				emitted = false
				if err := emit(event); err != nil {
					return err
				}
				if !emitted {
					return fmt.Errorf("output fact recipe omitted a source output")
				}
			}
		}
		return ctx.Err()
	}, func(table, key string, value recipeOrderedValue) error {
		ref := outputCoordinate(int(current.Vout), current.TxIndex, block.Height)
		fields := 2
		if values {
			fields++
		}
		if scripts {
			fields++
		}
		row := value.Object
		if emitted || table != "outputs" || key != ref || len(row) != fields || row["ref"].Kind != "text" || row["ref"].Text != ref || row["block_hash"].Kind != "text" || row["block_hash"].Text != block.Hash {
			return fmt.Errorf("output fact proposal differs from its source output")
		}
		projected := outputFactRow{TxIndex: current.TxIndex, Vout: current.Vout}
		if values {
			number := row["value_sats"]
			if number.Kind != "uint" || number.Uint != *current.Value {
				return fmt.Errorf("output fact value differs from its source")
			}
			projected.Value = current.Value
		}
		if scripts {
			script := row["script"]
			if script.Kind != "bytes" || !bytes.Equal(script.Bytes, currentScript) || len(script.Bytes) > outputFactPageBytes/2 {
				return fmt.Errorf("output fact script differs from its source")
			}
			projected.Script = current.Script
		}
		emitted = true
		return visit(projected)
	})
}
