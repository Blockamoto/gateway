package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
)

//go:embed recipes/builtins/tx-locator.json
var builtinTransactionRecipeJSON []byte

var builtinTransactionRecipeOnce sync.Once
var builtinTransactionRecipeProgram *recipeProgram
var builtinTransactionRecipeError error
var builtinTransactionRecipeSemantic string

// Deliberate semantic-equivalence binding to the existing v1/v2 native
// generations. A JSON, interpreter, parser or operator-profile change must
// not acquire an old generation merely because its legacy rule hash is frozen.
const builtinLocatorEquivalentSemantic = "509d0084357660b8168611045fa2f05c3fccef619ec406436a3925168b9389ac"
const builtinLocatorLegacyV1Rule = "1ef5e5b7fbfca27a92861e40fb7b319425281db2012c0aa19de6adc1fe6f004a"
const builtinLocatorLegacyV2Rule = "bd3006d8a88506a3d90933ec6487315a760c2852d6a63ebb3aeb3aafe671c6f5"

func recipeLocatorCheckEquivalence(ruleHash string, program *recipeProgram) error {
	if program == nil {
		return fmt.Errorf("locator recipe is unavailable")
	}
	return recipeLocatorCheckEquivalenceHash(ruleHash, program.semanticHash())
}

func recipeLocatorCheckEquivalenceHash(ruleHash, semantic string) error {
	if semantic != builtinLocatorEquivalentSemantic || (ruleHash != builtinLocatorLegacyV1Rule && ruleHash != builtinLocatorLegacyV2Rule) {
		return fmt.Errorf("locator recipe semantics are not bound to this native generation; an explicit migration is required")
	}
	return nil
}

func recipeLocatorCheckStoreDefinition(d indexDefinition) error {
	if d.ID != "tx-locator" {
		return nil
	}
	_, err := builtinTransactionRecipe()
	if err != nil {
		return err
	}
	return recipeLocatorCheckEquivalenceHash(d.RuleHash, builtinTransactionRecipeSemantic)
}

func builtinTransactionRecipe() (*recipeProgram, error) {
	builtinTransactionRecipeOnce.Do(func() {
		var definition recipeDefinition
		definition, builtinTransactionRecipeError = parseRecipe(builtinTransactionRecipeJSON)
		if builtinTransactionRecipeError == nil {
			builtinTransactionRecipeProgram, builtinTransactionRecipeError = compileRecipe(definition)
		}
		if builtinTransactionRecipeError == nil {
			builtinTransactionRecipeSemantic = builtinTransactionRecipeProgram.semanticHash()
		}
	})
	return builtinTransactionRecipeProgram, builtinTransactionRecipeError
}

// Native ingestion already admits the selected, verified decoded inventory.
// This source boundary retains those checks and takes an owned ordered snapshot;
// it does not upgrade a legacy decoded inventory to parser-owned raw evidence.
// When the decoder supplied its private receipt, recheck that evidence too.
func recipeNativeTransactionInventory(ctx context.Context, block blockView) (*recipeTransactionSource, error) {
	if ctx == nil || block.Height < 0 || !integrityVerified(block) || (!block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated) || !validHash(block.Hash) || len(block.Transactions) == 0 || uint64(len(block.Transactions)) != block.TransactionCount {
		return nil, fmt.Errorf("native transaction source requires admitted complete verified inventory")
	}
	if block.verifiedRawProof != nil {
		return recipeTransactionSourceFromBlock(ctx, block)
	}
	ids := make([]string, len(block.Transactions))
	for i, tx := range block.Transactions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if tx.Index != i || !recipeStoreHashValid(tx.TxID) {
			return nil, fmt.Errorf("native transaction inventory is not ordered")
		}
		ids[i] = tx.TxID
	}
	return &recipeTransactionSource{height: block.Height, hash: block.Hash, ids: ids}, nil
}

func transactionLocatorFromRecipe(ctx context.Context, block blockView) (*transactionLocatorPayload, error) {
	if ctx == nil {
		ctx = context.Background()
	} // legacy synchronous native callers
	program, err := builtinTransactionRecipe()
	if err != nil {
		return nil, fmt.Errorf("built-in locator recipe: %w", err)
	}
	return transactionLocatorFromRecipeProgram(ctx, block, program)
}

func transactionLocatorFromRecipeProgram(ctx context.Context, block blockView, program *recipeProgram) (*transactionLocatorPayload, error) {
	if err := recipeLocatorCheckEquivalence(builtinLocatorLegacyV2Rule, program); err != nil {
		return nil, err
	}
	source, err := recipeNativeTransactionInventory(ctx, block)
	if err != nil {
		return nil, err
	}
	sink, err := newRecipeLocatorSink(ctx, block.Height, block.Hash, uint64(len(source.ids)))
	if err != nil {
		return nil, err
	}
	if err = program.evaluateTransactionSourceInto(ctx, block, source, nil, sink.add); err != nil {
		return nil, err
	}
	return sink.finish()
}

// This is a physical row adapter, not a derivation executor. The common engine
// has already produced the transaction table; the compact v0.7.9 layout needs
// one exact ordered ID per position. Reject incomplete or ambiguous tables.
func recipeLocatorPhysicalPayload(ctx context.Context, height int64, hash string, count uint64, rows recipeRows) (*transactionLocatorPayload, error) {
	if len(rows) != 1 || uint64(len(rows["transactions"])) != count {
		return nil, fmt.Errorf("locator rows are not a complete transaction inventory")
	}
	sink, err := newRecipeLocatorSink(ctx, height, hash, count)
	if err != nil {
		return nil, err
	}
	for key, row := range rows["transactions"] {
		if err = sink.add(recipeRowProposal{Table: "transactions", Key: key, Value: row}); err != nil {
			return nil, err
		}
	}
	return sink.finish()
}

type recipeLocatorSink struct {
	ctx             context.Context
	height          int64
	hash            string
	count, received uint64
	entries         []transactionLocatorEntry
	seen            []bool
}

func newRecipeLocatorSink(ctx context.Context, height int64, hash string, count uint64) (*recipeLocatorSink, error) {
	if ctx == nil || height < 0 || !recipeStoreHashValid(hash) || count == 0 || count > uint64(^uint32(0)) {
		return nil, fmt.Errorf("invalid locator sink")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &recipeLocatorSink{ctx: ctx, height: height, hash: hash, count: count, entries: make([]transactionLocatorEntry, int(count)), seen: make([]bool, int(count))}, nil
}

func (s *recipeLocatorSink) add(proposal recipeRowProposal) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if proposal.Table != "transactions" {
		return fmt.Errorf("unexpected locator table")
	}
	row, key := proposal.Value, proposal.Key
	n, ok := row["tx_index"].(json.Number)
	if !ok {
		return fmt.Errorf("locator position is not an integer")
	}
	index, err := strconv.ParseUint(string(n), 10, 32)
	id, idOK := row["txid"].(string)
	if err != nil || strconv.FormatUint(index, 10) != string(n) || index >= s.count || s.seen[index] || !idOK || !recipeStoreHashValid(id) || key != id || row["ref"] != txCoordinate(int(index), s.height) || row["block_hash"] != s.hash || row["height"] != json.Number(strconv.FormatInt(s.height, 10)) {
		return fmt.Errorf("locator row has inconsistent identity or position")
	}
	s.seen[index] = true
	s.entries[index] = transactionLocatorEntry{uint32(index), id}
	s.received++
	return nil
}

func (s *recipeLocatorSink) finish() (*transactionLocatorPayload, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	if s.received != s.count {
		return nil, fmt.Errorf("locator rows are not a complete transaction inventory")
	}
	n := uint32(s.count)
	return &transactionLocatorPayload{Block: transactionLocatorBlock{Height: s.height, TransactionCount: &n, Transactions: s.entries}, Completeness: "complete", Selection: "all"}, nil
}
