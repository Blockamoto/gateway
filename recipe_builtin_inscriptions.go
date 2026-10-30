package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
)

//go:embed recipes/builtins/inscriptions-lean.json
var builtinInscriptionLeanJSON []byte

//go:embed recipes/builtins/inscriptions-full.json
var builtinInscriptionFullJSON []byte

//go:embed recipes/builtins/inscriptions-conventional-ids.json
var builtinInscriptionIDsJSON []byte

var builtinInscriptionOnce sync.Once
var builtinInscriptionPrograms [3]*recipeProgram
var builtinInscriptionError error
var builtinInscriptionSemantic string

const builtinInscriptionEquivalentSemantic = "200c2ef4cf177d51e631bc6bc362ce49acf6113016d10627140e0a598a4baf9a"
const builtinInscriptionLegacyV1Rule = "8d3eb00515980a735f429c6b7cf6a6656ce8983af4e01d8deed08ec9ee2d6fb1"
const builtinInscriptionLegacyV2Rule = "2bbc7edf72c2baaaefd6a29c3b2efeebb3461309ba9d3fbd64b09e1cd596f05e"

func recipeInscriptionCheckEquivalence(rule, semantic string) error {
	if semantic != builtinInscriptionEquivalentSemantic || rule != builtinInscriptionLegacyV1Rule && rule != builtinInscriptionLegacyV2Rule {
		return fmt.Errorf("inscription recipe semantics are not bound to this native generation; an explicit migration is required")
	}
	return nil
}
func recipeInscriptionCheckStoreDefinition(d indexDefinition) error {
	if d.ID != "inscriptions" {
		return nil
	}
	if _, err := builtinInscriptionRecipes(); err != nil {
		return err
	}
	return recipeInscriptionCheckEquivalence(d.RuleHash, builtinInscriptionSemantic)
}

func builtinInscriptionRecipes() ([3]*recipeProgram, error) {
	builtinInscriptionOnce.Do(func() {
		for i, raw := range [][]byte{builtinInscriptionLeanJSON, builtinInscriptionFullJSON, builtinInscriptionIDsJSON} {
			d, err := parseRecipe(raw)
			if err == nil {
				builtinInscriptionPrograms[i], err = compileRecipe(d)
			}
			if err != nil {
				builtinInscriptionError = err
				return
			}
		}
		builtinInscriptionSemantic = indexDigest([]string{builtinInscriptionPrograms[0].semanticHash(), builtinInscriptionPrograms[1].semanticHash(), builtinInscriptionPrograms[2].semanticHash()})
	})
	return builtinInscriptionPrograms, builtinInscriptionError
}

// Preserve source order and exact source content. The common engine derives
// rows; this adapter validates and encodes them in the native physical layout.
func inscriptionPayloadFromRecipes(ctx context.Context, block blockView, result *inscriptionBlockResult, mode string, ids bool) (indexBatch, error) {
	if ctx == nil {
		ctx = context.Background()
	} // legacy synchronous native callers
	if mode != "lean" && mode != "full" {
		return indexBatch{}, fmt.Errorf("invalid inscription retention mode")
	}
	programs, err := builtinInscriptionRecipes()
	if err != nil {
		return indexBatch{}, err
	}
	if err = recipeInscriptionCheckEquivalence(builtinInscriptionLegacyV2Rule, builtinInscriptionSemantic); err != nil {
		return indexBatch{}, err
	}
	source, err := recipeAdmitInscriptionSource(ctx, block, result)
	if err != nil {
		return indexBatch{}, err
	}
	b := indexBatch{InscriptionBlock: &inscriptionBlockMetadata{Mode: mode, TransactionCount: uint32(len(block.Transactions))}, InscriptionCoordinates: []string{}}
	program := programs[0]
	if mode == "full" {
		program = programs[1]
	}
	err = program.evaluateAdmittedInscriptionSourceInto(ctx, block, source, nil, func(row recipeRowProposal) error {
		i := len(b.InscriptionCoordinates)
		if i >= len(result.Occurrences) || row.Table != "positions" || row.Value["ref"] != row.Key {
			return fmt.Errorf("invalid inscription position proposal")
		}
		o := result.Occurrences[i]
		if row.Key != inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height) {
			return fmt.Errorf("inscription position order differs from source")
		}
		if mode == "full" {
			record, ok := row.Value["record"].(*recipeRevealRecord)
			if !ok || len(row.Value) != 2 || record.receipt != result.inventoryReceipt || record.ordinal != i {
				return fmt.Errorf("inscription record is not from admitted source")
			}
			b.Inscriptions = append(b.Inscriptions, record.occurrence)
		} else if len(row.Value) != 1 {
			return fmt.Errorf("Lean recipe retained unexpected columns")
		}
		b.InscriptionCoordinates = append(b.InscriptionCoordinates, row.Key)
		return nil
	})
	if err != nil {
		return indexBatch{}, err
	}
	if len(b.InscriptionCoordinates) != len(result.Occurrences) {
		return indexBatch{}, fmt.Errorf("inscription recipe omitted admitted positions")
	}
	if ids {
		last := -1
		err = programs[2].evaluateAdmittedInscriptionSourceInto(ctx, block, source, nil, func(row recipeRowProposal) error {
			n, ok := row.Value["tx_index"].(json.Number)
			if !ok {
				return fmt.Errorf("invalid reveal transaction index")
			}
			ti, err := strconv.Atoi(string(n))
			if err != nil || strconv.Itoa(ti) != string(n) || ti <= last || ti >= len(block.Transactions) || row.Table != "transactions" || len(row.Value) != 3 || row.Key != txCoordinate(ti, block.Height) || row.Value["ref"] != row.Key || row.Value["txid"] != block.Transactions[ti].TxID {
				return fmt.Errorf("invalid reveal transaction ID proposal")
			}
			b.InscriptionBlock.ConventionalIDs = append(b.InscriptionBlock.ConventionalIDs, transactionLocatorEntry{TxIndex: uint32(ti), TxID: block.Transactions[ti].TxID})
			last = ti
			return nil
		})
		if err != nil {
			return indexBatch{}, err
		}
	}
	return b, nil
}
