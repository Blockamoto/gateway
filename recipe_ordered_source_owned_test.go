package main

import (
	"context"
	"testing"
)

func TestRecipeOrderedOwnedSourceUsesRawFactsAndOwnsDecodedInventory(t *testing.T) {
	ctx := context.Background()
	block := recipeCoordinatorSource(t)[1]
	want := block.Transactions[0].Outputs[0].ValueSats
	block.Transactions[0].Outputs[0].ValueSats++ // caller's decoded facts are not authority
	source, err := recipeOrderedSourceFromBlock(ctx, block)
	if err != nil {
		t.Fatal(err)
	}
	if source.block.Transactions[0].Outputs[0].ValueSats != want {
		t.Fatal("ordered source trusted altered decoded output")
	}
	block.verifiedRaw[90] ^= 1
	block.Transactions[0].Outputs[0].ScriptPubKey = "6a"
	if source.block.Transactions[0].Outputs[0].ScriptPubKey == "6a" {
		t.Fatal("source retained caller aliases")
	}
	if _, _, err = exactVerifiedBlockPayload(source.block); err != nil {
		t.Fatal("owned raw receipt changed with its caller", err)
	}
	if source.block.Verification.HeaderChainMatch || source.block.Verification.ConsensusValidated {
		t.Fatal("raw source invented selected-chain or consensus authority")
	}
}

func TestRecipeOrderedOwnedSourceRejectsInventedAndCancelledInputs(t *testing.T) {
	if _, err := recipeOrderedSourceFromBlock(context.Background(), utxoFixtureBlock(1, genesisHashDisplay, utxoFixtureCoinbase(77800, 1))); err == nil {
		t.Fatal("invented decoded source acquired a raw receipt")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := recipeOrderedSourceFromBlock(ctx, recipeCoordinatorSource(t)[0]); err == nil {
		t.Fatal("cancelled source was decoded")
	}
}
