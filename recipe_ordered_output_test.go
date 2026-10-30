package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestRecipeOrderedOutputHistoricalCursorAndSelectedAuthority(t *testing.T) {
	ctx := context.Background()
	a, c, blocks := orderedConsumerFixture(t, false)
	resolved, err := a.ResolveSavedRecipe(ctx, c.plan.Index, c.plan.Revision, c.plan.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	read := func(height int64, token, cursor string) (map[string]any, int, error) {
		return a.readOrderedRecipeOutput(ctx, resolved, height, 1, token, cursor)
	}
	if output, _, err := read(0, "", ""); err != nil || output["state"] != "uncovered" {
		t.Fatal("unprocessed origin was treated as evaluated genesis", output, err)
	}
	for height := int64(0); height <= 1; height++ {
		next, err := c.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = c.Apply(ctx, *next.Work, blocks[height], nil); err != nil {
			t.Fatal(err)
		}
	}
	empty, _, err := read(0, "", "")
	if err != nil || empty["state"] != "covered" || len(empty["rows"].([]recipeStoreRow)) != 0 || empty["live_coin_count"] != "0" {
		t.Fatal("evaluated empty genesis is not queryable", empty, err)
	}
	first, _, err := read(1, "", "")
	if err != nil || first["state"] != "covered" || len(first["rows"].([]recipeStoreRow)) != 1 {
		t.Fatal("ordered first page", first, err)
	}
	unit, cursor := first["unit"].(string), first["next_cursor"].(string)
	rows := first["rows"].([]recipeStoreRow)
	var key []string
	var value recipeOrderedValue
	if json.Unmarshal([]byte(rows[0].Key), &key) != nil || len(key) != 2 || key[0] != "coins" || json.Unmarshal(rows[0].Value, &value) != nil || value.Object["value"].Uint != 5_000_000_000 {
		t.Fatal("ordered query lost its exact typed Coin", rows)
	}
	second, _, err := read(1, unit, cursor)
	if err != nil || len(second["rows"].([]recipeStoreRow)) != 1 || second["next_cursor"] != nil || second["unit"] != unit {
		t.Fatal("ordered count continuation", second, err)
	}
	rows = second["rows"].([]recipeStoreRow)
	if json.Unmarshal([]byte(rows[0].Key), &key) != nil || key[0] != "counts" || json.Unmarshal(rows[0].Value, &value) != nil || value.Object["live"].Uint != 1 {
		t.Fatal("ordered query lost its logical counter", rows)
	}
	if _, code, err := read(1, strings.Repeat("f", 64), cursor); code != 409 || err == nil {
		t.Fatal("mismatched snapshot accepted", code, err)
	}
	if _, code, err := read(1, unit, "coins:"+strings.Repeat("f", 64)); code != 409 || err == nil {
		t.Fatal("absent physical cursor accepted", code, err)
	}
	if _, code, err := read(1, "", cursor); code != 400 || err == nil {
		t.Fatal("unbound later page accepted", code, err)
	}
	// Synthetic storage/selected-header append, not an external-chain proof.
	// The historical block-one snapshot must survive an unrelated new head.
	header, err := a.readSelectedHeader(1)
	if err != nil {
		t.Fatal(err)
	}
	previous, _ := hex.DecodeString(blocks[1].Hash)
	for i := 0; i < 32; i++ {
		header[4+i] = previous[31-i]
	}
	header[79] ^= 1
	digest := hash256(header)
	b2 := utxoFixtureBlock(2, blocks[1].Hash, utxoFixtureCoinbase(77982, 1))
	b2.Hash = reverseHex(digest[:])
	a.headerChainMu.Lock()
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err == nil {
		_, err = f.WriteAt(header, 160)
		_ = f.Close()
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = c.generation.apply(ctx, b2, orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	continued, _, err := read(1, unit, cursor)
	if err != nil || indexDigest(continued) != indexDigest(second) {
		t.Fatal("unrelated append invalidated immutable historical page", continued, err)
	}
	// Replace the prior selected header before recovery runs. A saved state
	// from that disconnected branch must become unreadable immediately.
	genesis, _ := a.readSelectedHeader(0)
	a.headerChainMu.Lock()
	f, err = os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err == nil {
		_, err = f.WriteAt(genesis, 80)
		_ = f.Close()
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, code, err := read(1, unit, cursor); code != 409 || err == nil {
		t.Fatal("disconnected checkpoint remained queryable", code, err)
	}
}

func TestRecipeOrderedOutputRangeBoundaries(t *testing.T) {
	ctx := context.Background()
	tree := newOrdinalTree(t.TempDir(), "").withContext(ctx)
	for i := 0; i < 512; i++ {
		if err := tree.put(fmt.Sprintf("%064x", i), i); err != nil {
			t.Fatal(err)
		}
	}
	root, err := tree.flush()
	if err != nil {
		t.Fatal(err)
	}
	for _, after := range []int{-1, 0, 63, 64, 255, 510, 511, 512} {
		cursor := ""
		if after >= 0 {
			cursor = fmt.Sprintf("%064x", after)
		}
		next := after + 1
		fresh := newOrdinalTree(tree.dir, root).withContext(ctx)
		err := fresh.walkAfter(cursor, func(kv ordinalKV) error {
			if kv.Key != fmt.Sprintf("%064x", next) {
				t.Fatalf("range after %d skipped or repeated key %s, wanted %d", after, kv.Key, next)
			}
			next++
			return nil
		})
		want := 512
		if after == 512 {
			want = 513
		}
		if err != nil || next != want {
			t.Fatal("range end", after, next, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := newOrdinalTree(tree.dir, root).withContext(canceled).walkAfter("", func(ordinalKV) error { return nil }); err != context.Canceled {
		t.Fatal("range ignored cancellation", err)
	}
}
