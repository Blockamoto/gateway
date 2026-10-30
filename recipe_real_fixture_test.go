package main

import (
	"context"
	"encoding/hex"
	"testing"
)

// The Bitcoin genesis block is a real bounded fixture. It contains no Ord
// reveals, so this validates an authenticated empty unit, not metadata parity.
func TestRecipeVerifiedGenesisEmptyUnit(t *testing.T) {
	header, err := hex.DecodeString(genesisHeaderHex)
	if err != nil {
		t.Fatal(err)
	}
	block, err := parseBlockDetailed(0, genesisHashDisplay, header, testGenesisBlockPayload(t), "fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	result, err := extractInscriptionOccurrences(block)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := parseRecipe([]byte(`{"format":"gateway-recipe","version":1,"id":"genesis-test","name":"Genesis","source":"bitcoin.reveal","origin":0,"history":"sparse","tables":[{"name":"rows","key":"ref"}],"steps":[{"op":"write","table":"rows","row":{"ref":{"field":["ref"]}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	program, err := compileRecipe(definition)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openRecipeStore(t.TempDir(), definition.ID, program.semanticHash())
	if err != nil {
		t.Fatal(err)
	}
	if err := program.commitInscriptionBlock(context.Background(), store, block, &result, func(publish func() error) error { return publish() }); err != nil {
		t.Fatal(err)
	}
	unit, found, err := store.Read(context.Background(), 0)
	if err != nil || !found || unit.BlockHash != genesisHashDisplay || len(unit.Rows) != 0 {
		t.Fatalf("unexpected unit: %#v, found=%v, err=%v", unit, found, err)
	}
}
