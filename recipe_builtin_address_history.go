package main

import (
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
)

//go:embed recipes/builtins/address-history.json
var builtinAddressHistoryJSON []byte

//go:embed recipes/builtins/address-history-payment-legacy.json
var builtinAddressHistoryLegacyJSON []byte

const recipeAddressHistoryFactProfile = "gateway-address-history-complete-facts-v1"
const recipeAddressHistoryEquivalentSemantic = "5c5ef3a981053bd26e79ed4f9e91fb53a88cbbc3e9a96d5e85e7bb9fe6fc4398"

var builtinAddressHistoryOnce sync.Once
var builtinAddressHistoryPrograms [2]*recipeProgram
var builtinAddressHistorySemantic string
var builtinAddressHistoryError error

func builtinAddressHistoryRecipes() ([2]*recipeProgram, error) {
	builtinAddressHistoryOnce.Do(func() {
		for i, raw := range [][]byte{builtinAddressHistoryLegacyJSON, builtinAddressHistoryJSON} {
			d, err := parseRecipe(raw)
			if err == nil {
				builtinAddressHistoryPrograms[i], err = compileRecipe(d)
			}
			if err != nil {
				builtinAddressHistoryError = err
				return
			}
		}
		builtinAddressHistorySemantic = indexDigest(struct {
			Source   string
			Schema   int
			Programs []string
		}{recipeAddressHistoryFactProfile, addressHistorySchema, []string{builtinAddressHistoryPrograms[0].semanticHash(), builtinAddressHistoryPrograms[1].semanticHash()}})
	})
	return builtinAddressHistoryPrograms, builtinAddressHistoryError
}

func recipeAddressHistoryCheckEquivalence(semantic string, schema int) error {
	if schema != 1 || semantic != recipeAddressHistoryEquivalentSemantic {
		return fmt.Errorf("address-history JSON/fact semantics are not bound to native schema %d; explicit migration required (actual %s)", schema, semantic)
	}
	return nil
}
func recipeAddressHistoryCheckNative() error {
	if _, err := builtinAddressHistoryRecipes(); err != nil {
		return err
	}
	return recipeAddressHistoryCheckEquivalence(builtinAddressHistorySemantic, addressHistorySchema)
}

// A fact source supplies complete admitted page rows. It is not a raw-block
// source or selected-chain authority. Only fields read by these frozen JSON
// programs are synthesized; fact refs and exact values remain physical data.
// Selection/classification occurs exclusively in the common interpreter.
func recipeAddressHistoryVisit(ctx context.Context, version int, walk func(func(outputFactRef, outputFact) error) error, visit func(string, string, outputFactRef, outputFact) error) error {
	if ctx == nil || walk == nil || visit == nil || version < 0 || version > 1 {
		return fmt.Errorf("invalid address history fact projection")
	}
	programs, err := builtinAddressHistoryRecipes()
	if err != nil {
		return err
	}
	if err = recipeAddressHistoryCheckNative(); err != nil {
		return err
	}
	var current outputFact
	var currentRef outputFactRef
	return programs[version].evaluateOrderedSparseEventsInto(ctx, func(emit func(map[string]recipeOrderedValue) error) error {
		return walk(func(ref outputFactRef, fact outputFact) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if fact.Height < 0 || fact.TxIndex < 0 || !recipeStoreHashValid(fact.BlockHash) || !recipeStoreHashValid(ref.Page) {
				return fmt.Errorf("invalid address source fact")
			}
			script, err := hex.DecodeString(fact.ScriptPubKey)
			if err != nil || hex.EncodeToString(script) != fact.ScriptPubKey {
				return fmt.Errorf("invalid exact source script")
			}
			current, currentRef = fact, ref
			return emit(map[string]recipeOrderedValue{"kind": rovText("output"), "height": rovUint(uint64(fact.Height)), "block": rovText(fact.BlockHash), "tx_index": rovUint(uint64(fact.TxIndex)), "vout": rovUint(uint64(fact.Vout)), "value": rovUint(fact.ValueSats), "script": rovBytes(script), "coinbase": rovBool(fact.Coinbase)})
		})
	}, func(row recipeRowProposal) error {
		ref := outputCoordinate(int(current.Vout), current.TxIndex, current.Height)
		value, ok := row.Value["value_sats"].(json.Number)
		script, scriptOK := row.Value["script"].(recipeBytes)
		destination, destinationOK := row.Value["destination"].(string)
		kind, kindOK := row.Value["script_type"].(string)
		if row.Table != "outputs" || row.Key != ref || len(row.Value) != 6 || row.Value["ref"] != ref || row.Value["block_hash"] != current.BlockHash || !ok || string(value) != strconv.FormatUint(current.ValueSats, 10) || !scriptOK || script.Hex != current.ScriptPubKey || !destinationOK || destination == "" || !kindOK || kind == "" {
			return fmt.Errorf("address history proposal differs from its admitted source fact")
		}
		return visit(destination, kind, currentRef, current)
	})
}
