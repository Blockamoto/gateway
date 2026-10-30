package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

// Native ordered derivation uses the same typed interpreter as other ordered
// compositions, with the existing physical state and guarded publisher.
//
//go:embed recipes/builtins/utxo-state.json
var builtinUTXODefinitionJSON []byte

var builtinUTXOProgramJSON = func() []byte {
	var d struct {
		Ordered json.RawMessage `json:"ordered"`
	}
	_ = json.Unmarshal(builtinUTXODefinitionJSON, &d)
	return d.Ordered
}()

var builtinUTXOProgramOnce sync.Once
var builtinUTXOProgram recipeOrderedProgram
var builtinUTXOProgramError error
var builtinUTXOSemantic string
var builtinUTXORecipeProgram *recipeProgram

const builtinUTXOEquivalentSemantic = "b0c3c2ff4df3a9cd7cd2203aa70a914e779ac949d4be0991e21dc2e99e994d96"

func recipeNativeUTXOSemantic(program recipeOrderedProgram, stream, source, bitcoin, physical string) string {
	return indexDigest(struct {
		Domain, Interpreter, Stream, Source, Bitcoin, Physical string
		Program                                                recipeOrderedProgram
	}{"gateway-native-utxo-equivalence-v1", recipeOrderedProfile, stream, source, bitcoin, physical, program})
}

func recipeNativeUTXOCheckEquivalence(semantic string, schema int) error {
	if semantic != builtinUTXOEquivalentSemantic || schema != 2 {
		return fmt.Errorf("UTXO recipe semantics are not bound to this native generation; an explicit migration is required")
	}
	return nil
}

func loadBuiltinUTXOProgram() (recipeOrderedProgram, error) {
	builtinUTXOProgramOnce.Do(func() {
		var definition recipeDefinition
		definition, builtinUTXOProgramError = parseRecipe(builtinUTXODefinitionJSON)
		if builtinUTXOProgramError == nil {
			builtinUTXORecipeProgram, builtinUTXOProgramError = compileRecipe(definition)
		}
		if builtinUTXOProgramError == nil && builtinUTXORecipeProgram.ordered == nil {
			builtinUTXOProgramError = fmt.Errorf("built-in UTXO definition has no executable ordered program")
		}
		if builtinUTXOProgramError == nil {
			builtinUTXOProgram = *builtinUTXORecipeProgram.ordered
		}
		if builtinUTXOProgramError == nil {
			builtinUTXOSemantic = recipeNativeUTXOSemantic(builtinUTXOProgram, recipeOrderedStreamProfile, recipeVerifiedEventProfile+"/native-admitted-facts-v1", "gateway-bitcoin-primitives-v1", "gateway-utxo-cow-facts-schema2-v1")
		}
	})
	return builtinUTXOProgram, builtinUTXOProgramError
}

func recipeNativeUTXOCheckGeneration() error {
	if _, err := loadBuiltinUTXOProgram(); err != nil {
		return err
	}
	return recipeNativeUTXOCheckEquivalence(builtinUTXOSemantic, utxoCheckpointSchema)
}

func recipeNativeUTXOEventStream(ctx context.Context, block blockView, facts map[string]outputFactRef, sink func(map[string]recipeOrderedValue) error) error {
	inputIndex := uint64(0)
	return recipeVerifiedWalk(ctx, block, func(event map[string]recipeOrderedValue) error {
		switch event["kind"].Text {
		case "tx_begin":
			inputIndex = 0
		case "input":
			event["input_index"] = rovUint(inputIndex)
			event["input_ref"] = rovText(inputCoordinate(int(inputIndex), int(event["tx_index"].Uint), block.Height))
			inputIndex++
		case "output":
			position := outputCoordinate(int(event["vout"].Uint), int(event["tx_index"].Uint), block.Height)
			ref, found := facts[position]
			if !found {
				return fmt.Errorf("ordered source lacks mandatory output fact")
			}
			event["fact_page"], event["fact_row"] = rovText(ref.Page), rovUint(uint64(ref.Row))
		}
		return sink(event)
	})
}

func (s *utxoStore) applyBlockRecipe(ctx context.Context, block blockView) error {
	if err := recipeNativeUTXOCheckGeneration(); err != nil {
		return err
	}
	utxoWriterMu.Lock()
	defer utxoWriterMu.Unlock()
	utxoStateMu.Lock()
	defer utxoStateMu.Unlock()
	program, err := loadBuiltinUTXOProgram()
	if err != nil {
		return err
	}
	return s.applyOrderedBlockSnapshot(ctx, block, program)
}

// A private snapshot owns its decoded source and COW edits. Publication may
// be supplied by a generation CAS host; no native global locks are acquired.
func (s *utxoStore) applyOrderedBlockSnapshot(ctx context.Context, block blockView, program recipeOrderedProgram) error {
	if err := s.reload(ctx); err != nil {
		return err
	}
	if block.Height < 0 || !validHash(block.Hash) || !integrityVerified(block) || !block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated || len(block.Transactions) == 0 || block.TransactionCount != uint64(len(block.Transactions)) || !block.Transactions[0].Coinbase {
		return fmt.Errorf("UTXO processing requires a complete header-anchored block")
	}
	if s.head == nil && (block.Height != 0 || block.Hash != genesisHashDisplay || len(block.Transactions) != 1) {
		return fmt.Errorf("UTXO state must begin at Bitcoin mainnet genesis")
	}
	var checkpoint utxoCheckpoint
	if s.head != nil {
		checkpoint = s.head.Checkpoint
		if block.Height != checkpoint.Height+1 || block.PreviousBlockHash != checkpoint.BlockHash {
			return fmt.Errorf("UTXO block is not the next committed transition; reconcile first")
		}
	}
	previous := checkpoint.Commitment
	journal := utxoJournal{Schema: 1, Height: block.Height, BlockHash: block.Hash, PreviousCommitment: previous, BeforeImages: []utxoBeforeImage{}}
	edit := &utxoEdit{trees: s.trees(ctx, checkpoint), facts: newUTXOFactReader(ctx, s.root), seen: map[string]bool{}, journal: &journal}
	var facts map[string]outputFactRef
	var err error
	if s.privateFacts {
		// Own physical facts without retaining one whole-block reference map
		// per custom generation in the shared prepared-source cache.
		facts, err = storeBlockOutputFactLayersUncachedContext(ctx, s.root, block, true, true)
	} else {
		facts, err = storeBlockOutputFactsContext(ctx, s.root, block)
	}
	if err != nil {
		return err
	}
	if err = s.hook("facts_written"); err != nil {
		return err
	}
	adapter := &recipeUTXOAdapter{store: s, baseCommit: previous, edit: edit, checkpoint: &checkpoint, block: block, facts: facts, emitted: map[string]bool{}, counts: map[string]recipeOrderedValue{}, origins: map[string]utxoBridge{}, coins: map[string]utxoMember{}}
	adapter.publish = func() error { return s.finishBlockEdit(ctx, block, checkpoint, previous, journal, edit) }
	defer adapter.AbortOrdered()
	source := func(emit func(map[string]recipeOrderedValue) error) error {
		return recipeNativeUTXOEventStream(ctx, block, facts, emit)
	}
	if err = recipeOrderedExecuteStateStream(ctx, program, adapter, source, func(table, key string, value recipeOrderedValue) error { return adapter.emit(ctx, table, key, value) }); err != nil {
		return err
	}
	return adapter.CommitOrdered(ctx)
}
