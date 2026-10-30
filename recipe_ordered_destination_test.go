package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestRecipeOrderedDestinationExactScripts(t *testing.T) {
	m := &recipeOrderedMachine{ctx: context.Background(), vars: map[string]recipeOrderedValue{}}
	cases := []struct{ script, kind, destination string }{
		{address076SameKeyP2PKH, "p2pkh", "1BgGZ9tcN4rm9KBzDn7KprQz87SZ26SAMH"},
		{"21" + address076PublicKey + "ac", "p2pk", address076PublicKey},
		{"41" + address076UncompressedKey + "ac", "p2pk", address076UncompressedKey},
		{"21" + "02" + strings.Repeat("00", 32) + "ac", "p2pk", ""},
		{"6a", "op_return", ""},
	}
	for _, tc := range cases {
		script, err := hex.DecodeString(tc.script)
		if err != nil {
			t.Fatal(err)
		}
		got, err := m.eval(roCall("destination", roLit(rovBytes(script))), 0)
		if err != nil || got.Object["kind"].Text != tc.kind || got.Object["destination"].Text != tc.destination || got.Object["supported"].Bool != (tc.destination != "") {
			t.Fatalf("script %s: %+v %v", tc.script, got, err)
		}
	}
	if _, err := m.eval(roCall("destination", roLit(rovText("script"))), 0); err == nil {
		t.Fatal("accepted non-byte script")
	}
}

// This exercises the compositional operators against caller-supplied net
// changes. It does not authenticate a UTXO snapshot or publish a projection.
func TestRecipeOrderedDestinationNetDeltaComposition(t *testing.T) {
	ctx := context.Background()
	store := &recipeStateMemoryTest{version: recipeStateVersion{Checkpoint: "origin"}, values: map[recipeStateKey]recipeStateValue{}}
	tx, err := recipeStateBegin(store, store.version, "next", recipeStateTestLimits())
	if err != nil {
		t.Fatal(err)
	}
	script, _ := hex.DecodeString(address076SameKeyP2PKH)
	other, _ := hex.DecodeString("21" + address076PublicKey + "ac")
	events := []map[string]recipeOrderedValue{
		{"script": rovBytes(script), "amount": rovUint(12), "spend": rovBool(false)},
		{"script": rovBytes(other), "amount": rovUint(7), "spend": rovBool(false)},
		{"script": rovBytes(script), "amount": rovUint(5), "spend": rovBool(true)},
		{"script": rovBytes([]byte{0x6a}), "amount": rovUint(2), "spend": rovBool(false)},
	}
	get := func(field string) recipeOrderedExpr { return roCall("get", roVar("dest"), roLit(rovText(field))) }
	adjust := func(table string, key recipeOrderedExpr) []recipeOrderedInstruction {
		return []recipeOrderedInstruction{
			{Op: "read", Table: table, Key: key, Name: "old"},
			{Op: "if", Expr: roCall("not", roCall("present", roVar("old"))), Body: []recipeOrderedInstruction{{Op: "let", Name: "old", Expr: roLit(rovUint(0))}}},
			{Op: "if", Expr: roField("spend"), Body: []recipeOrderedInstruction{{Op: "let", Name: "next", Expr: roCall("sub_money", roVar("old"), roField("amount"))}}, Else: []recipeOrderedInstruction{{Op: "let", Name: "next", Expr: roCall("add_money", roVar("old"), roField("amount"))}}},
			{Op: "put", Table: table, Key: key, Expr: roVar("next")},
		}
	}
	program := recipeOrderedProgram{Steps: []recipeOrderedInstruction{{Op: "for_events", Body: []recipeOrderedInstruction{
		{Op: "let", Name: "dest", Expr: roCall("destination", roField("script"))},
		{Op: "if", Expr: get("supported"), Body: adjust("destinations", get("destination")), Else: adjust("unsupported", roLit(rovText("total")))},
	}}}}
	if err = recipeOrderedExecute(ctx, program, tx, events); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[recipeStateKey]uint64{
		{Table: "destinations", Key: "1BgGZ9tcN4rm9KBzDn7KprQz87SZ26SAMH"}: 7,
		{Table: "destinations", Key: address076PublicKey}:                  7,
		{Table: "unsupported", Key: "total"}:                               2,
	} {
		var got recipeOrderedValue
		v, ok := store.values[key]
		if !ok || json.Unmarshal(v.Bytes, &got) != nil || got.Kind != "uint" || got.Uint != want {
			t.Fatalf("%+v: %+v", key, v)
		}
	}
}
