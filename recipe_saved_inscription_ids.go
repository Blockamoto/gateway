package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
)

//go:embed recipes/builtins/inscriptions-saved-conventional-ids.json
var builtinSavedInscriptionIDsJSON []byte
var builtinSavedInscriptionIDsOnce sync.Once
var builtinSavedInscriptionIDsProgram *recipeProgram
var builtinSavedInscriptionIDsError error
var builtinSavedInscriptionIDsSemantic string

const recipeSavedRevealSourceProfile = "gateway-admitted-saved-reveal-positions-v1"
const builtinSavedInscriptionIDsEquivalent = "66d52fd73611644c1decb8df7bf03bfadc83a3f9966190ceee66bb271fae4741"
const builtinSavedInscriptionIDsRule = "bb1e9cf30432ef9ec5aa5c7e2e68771bef10a736c604021ade52409a810d5521"
const builtinSavedInscriptionIDsLegacyRule = "db7b4bfc92b82c311dbed0ff322a33c1fa63daf4704771bb05cc43f51ef7ef91"

func builtinSavedInscriptionIDsRecipe() (*recipeProgram, error) {
	builtinSavedInscriptionIDsOnce.Do(func() {
		d, err := parseRecipe(builtinSavedInscriptionIDsJSON)
		if err == nil {
			builtinSavedInscriptionIDsProgram, err = compileRecipe(d)
		}
		builtinSavedInscriptionIDsError = err
		if err == nil {
			builtinSavedInscriptionIDsSemantic = indexDigest(struct{ Program, Source string }{builtinSavedInscriptionIDsProgram.semanticHash(), recipeSavedRevealSourceProfile})
		}
	})
	return builtinSavedInscriptionIDsProgram, builtinSavedInscriptionIDsError
}
func recipeSavedInscriptionIDsCheckEquivalence(rule, semantic string) error {
	if semantic != builtinSavedInscriptionIDsEquivalent || rule != builtinSavedInscriptionIDsRule && rule != builtinSavedInscriptionIDsLegacyRule {
		return fmt.Errorf("saved inscription ID recipe is not bound to this native generation; an explicit migration is required")
	}
	return nil
}
func recipeSavedInscriptionIDsCheckStore(d indexDefinition) error {
	if d.ID != inscriptionLocatorIndex {
		return nil
	}
	if _, err := builtinSavedInscriptionIDsRecipe(); err != nil {
		return err
	}
	return recipeSavedInscriptionIDsCheckEquivalence(d.RuleHash, builtinSavedInscriptionIDsSemantic)
}

// Each admitted saved position is a logical reveal-source event. Full content
// supplies its saved ID; Lean needs the matching complete verified block. This
// never upgrades a saved coordinate into a raw extraction or consensus proof.
func recipeSavedInscriptionIDsRows(ctx context.Context, source indexBatch, block *blockView, payload *transactionLocatorPayload) error {
	program, err := builtinSavedInscriptionIDsRecipe()
	if err != nil {
		return err
	}
	if err = recipeSavedInscriptionIDsCheckEquivalence(builtinSavedInscriptionIDsRule, builtinSavedInscriptionIDsSemantic); err != nil {
		return err
	}
	coordinates := inscriptionBatchCoordinates(source)
	stream := newRecipeRowStream(program.definition, func(row recipeRowProposal) error {
		n, ok := row.Value["tx_index"].(json.Number)
		if !ok {
			return fmt.Errorf("invalid saved reveal transaction position")
		}
		ti, err := strconv.ParseUint(string(n), 10, 32)
		id, idOK := row.Value["txid"].(string)
		if err != nil || strconv.FormatUint(ti, 10) != string(n) || !idOK || !validHash(id) || row.Table != "transactions" || len(row.Value) != 3 || row.Key != txCoordinate(int(ti), source.Checkpoint.Height) || row.Value["ref"] != row.Key {
			return fmt.Errorf("invalid saved reveal transaction row")
		}
		payload.Block.Transactions = append(payload.Block.Transactions, transactionLocatorEntry{TxIndex: uint32(ti), TxID: id})
		return nil
	})
	for i, coordinate := range coordinates {
		if err = ctx.Err(); err != nil {
			return err
		}
		position, ok := parseBODCoordinate(coordinate)
		if !ok || position.Kind != coordInscription || position.Height != source.Checkpoint.Height {
			return fmt.Errorf("invalid source inscription position")
		}
		var txid string
		if len(source.Inscriptions) == len(coordinates) {
			txid = source.Inscriptions[i].TxID
		} else if block != nil && position.TxIndex < len(block.Transactions) {
			txid = block.Transactions[position.TxIndex].TxID
		} else {
			return fmt.Errorf("inscription positions need their verified reveal block")
		}
		if !validHash(txid) {
			return fmt.Errorf("invalid reveal transaction identifier")
		}
		event := map[string]any{"tx_ref": txCoordinate(position.TxIndex, position.Height), "tx_index": json.Number(strconv.Itoa(position.TxIndex)), "txid": txid}
		rows, _, err := program.evaluateRowProposals(event, nil)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err = stream.add(row); err != nil {
				return err
			}
		}
	}
	return nil
}
