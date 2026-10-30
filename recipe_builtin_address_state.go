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

//go:embed recipes/builtins/address-state.json
var builtinAddressStateJSON []byte

//go:embed recipes/builtins/address-state-payment-legacy.json
var builtinAddressStateLegacyJSON []byte

const recipeAddressStateEquivalentSemantic = "e2fb15cd1832f22c8238421351329541515d1ff653be9615133d0fb7ce6f03be"

var builtinAddressStateOnce sync.Once
var builtinAddressStatePrograms [2]*recipeProgram
var builtinAddressStateSemantic string
var builtinAddressStateError error

func builtinAddressStateRecipes() ([2]*recipeProgram, error) {
	builtinAddressStateOnce.Do(func() {
		if _, err := loadBuiltinUTXOProgram(); err != nil {
			builtinAddressStateError = err
			return
		}
		canonical, err := json.Marshal(builtinUTXORecipeProgram.definition)
		if err != nil {
			builtinAddressStateError = err
			return
		}
		parentSemantic := builtinUTXORecipeProgram.semanticHash()
		parentRevision := recipeRegistryRevisionHash(parentSemantic, canonical)
		for i, raw := range [][]byte{builtinAddressStateLegacyJSON, builtinAddressStateJSON} {
			d, err := parseRecipe(raw)
			if err == nil {
				builtinAddressStatePrograms[i], err = compileRecipe(d)
			}
			if err == nil {
				dep := builtinAddressStatePrograms[i].definition.Dependencies[0]
				if dep.Scope != "builtin" || dep.Instance != "utxo-state" || dep.SemanticHash != parentSemantic || dep.Revision != parentRevision {
					err = fmt.Errorf("native current-address parent binding differs from immutable UTXO document")
				}
			}
			if err != nil {
				builtinAddressStateError = err
				return
			}
		}
		builtinAddressStateSemantic = indexDigest(struct {
			Source, Physical string
			Programs         []string
		}{recipeUTXOProjectionProfile, "gateway-native-address-cow-schema1-v1", []string{builtinAddressStatePrograms[0].semanticHash(), builtinAddressStatePrograms[1].semanticHash()}})
	})
	return builtinAddressStatePrograms, builtinAddressStateError
}
func recipeAddressStateCheckEquivalence(semantic string, schema int) error {
	if schema != 1 || semantic != recipeAddressStateEquivalentSemantic {
		return fmt.Errorf("current-address JSON/dependency semantics are not bound to native schema %d; explicit migration required (actual %s)", schema, semantic)
	}
	return nil
}
func recipeAddressStateCheckNative() error {
	if _, err := builtinAddressStateRecipes(); err != nil {
		return err
	}
	return recipeAddressStateCheckEquivalence(builtinAddressStateSemantic, 1)
}

// Native traversal/recovery supplies an admitted member from an exact source
// checkpoint or its checked net delta. JSON selects members versus exclusions;
// this boundary validates the emitted source identities before physical edits.
func recipeAddressStateMember(ctx context.Context, version int, member utxoMember, fact outputFact, add bool) (recipeRowProposal, error) {
	if ctx == nil || version < 0 || version > 1 {
		return recipeRowProposal{}, fmt.Errorf("invalid current-address projection source")
	}
	if err := recipeAddressStateCheckNative(); err != nil {
		return recipeRowProposal{}, err
	}
	programs, err := builtinAddressStateRecipes()
	if err != nil {
		return recipeRowProposal{}, err
	}
	return recipeAddressProjectionMember(ctx, programs[version], member, fact, add)
}

// Namespaced custom projections supply their own already-compiled program.
// Namespace, dependency, chain and publication authority are checked by the host.
func recipeAddressProjectionMember(ctx context.Context, program *recipeProgram, member utxoMember, fact outputFact, add bool) (recipeRowProposal, error) {
	if ctx == nil || program == nil || program.definition.Source != recipeUTXOProjectionSource || program.definition.Storage != recipeAddressProjectionStorage {
		return recipeRowProposal{}, fmt.Errorf("compiled checkpoint projection required")
	}
	position := outputCoordinate(int(fact.Vout), fact.TxIndex, fact.Height)
	if fact.Height < 0 || fact.TxIndex < 0 || member.Position != position || !recipeStoreHashValid(member.FactRef.Page) || !recipeStoreHashValid(fact.BlockHash) {
		return recipeRowProposal{}, fmt.Errorf("invalid projected coin identity")
	}
	script, err := hex.DecodeString(fact.ScriptPubKey)
	if err != nil || hex.EncodeToString(script) != fact.ScriptPubKey {
		return recipeRowProposal{}, fmt.Errorf("invalid projected coin script")
	}
	var proposal recipeRowProposal
	count := 0
	err = program.evaluateOrderedSparseEventsInto(ctx, func(emit func(map[string]recipeOrderedValue) error) error {
		return emit(map[string]recipeOrderedValue{"height": rovUint(uint64(fact.Height)), "block": rovText(fact.BlockHash), "tx_index": rovUint(uint64(fact.TxIndex)), "vout": rovUint(uint64(fact.Vout)), "value": rovUint(fact.ValueSats), "script": rovBytes(script), "add": rovBool(add)})
	}, func(row recipeRowProposal) error {
		count++
		expected := 5
		if row.Table == "members" {
			expected = 7
		} else if row.Table != "excluded" {
			return fmt.Errorf("unknown current-address output table")
		}
		value, valueOK := row.Value["value_sats"].(json.Number)
		bytes, bytesOK := row.Value["script"].(recipeBytes)
		if count != 1 || row.Key != position || row.Value["ref"] != position || len(row.Value) != expected || row.Value["block_hash"] != fact.BlockHash || row.Value["add"] != add || !valueOK || string(value) != strconv.FormatUint(fact.ValueSats, 10) || !bytesOK || bytes.Hex != fact.ScriptPubKey {
			return fmt.Errorf("projected row differs from its admitted coin")
		}
		if row.Table == "members" {
			destination, ok := row.Value["destination"].(string)
			kind, kindOK := row.Value["script_type"].(string)
			if !ok || !kindOK || destination == "" || kind == "" {
				return fmt.Errorf("projected destination missing")
			}
			_, exact, decodedKind, err := bitcoinDestinationScript(destination)
			if err != nil || exact != fact.ScriptPubKey || decodedKind != kind {
				return fmt.Errorf("projected destination does not identify the exact coin script")
			}
		}
		proposal = row
		return nil
	})
	if err != nil {
		return recipeRowProposal{}, err
	}
	if count != 1 {
		return recipeRowProposal{}, fmt.Errorf("current-address projection omitted its source coin")
	}
	return proposal, nil
}
