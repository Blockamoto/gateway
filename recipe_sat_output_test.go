package main

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRecipeSatOutputFragmentPaging(t *testing.T) {
	ctx := context.Background()
	s, err := openIndexStore(t.TempDir(), "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	b := utxoFixtureBlock(2, testID(99111), utxoFixtureCoinbase(99112, 769))
	refs, err := storeBlockOutputFactsContext(ctx, s.profileRoot, b)
	if err != nil {
		t.Fatal(err)
	}
	ref := refs["0.0.2"]
	ranges := make([]satRange, 769)
	for i := range ranges {
		ranges[i] = satRange{uint64((768 - i) * 2), uint64((768-i)*2 + 1)}
	}
	o, err := s.storeSatOutput(satIndexOutput{Output: "0.0.2", FactRef: &ref, Ranges: ranges, State: "unspent"})
	if err != nil {
		t.Fatal(err)
	}
	e := newSatEditor077(ctx, s, satIndexSnapshot{Profile: satIndexProfile, Layout: 1})
	if err = e.add(o, b.Transactions[0].TxID, false); err != nil {
		t.Fatal(err)
	}
	if err = e.flush(); err != nil {
		t.Fatal(err)
	}
	table, after, next := "outputs", "", uint64(0)
	seen := 0
	for page := 0; page < 4; page++ {
		rows, cursor, err := recipeSatMemberPage(ctx, s, e.snapshot, table, after, next, 1)
		if err != nil || len(rows) != 1 {
			t.Fatal(page, rows, cursor, err)
		}
		var value struct {
			RangeOffset string              `json:"range_offset"`
			RangeCount  string              `json:"range_count"`
			Ranges      recipeOrderedRanges `json:"ranges"`
		}
		if err = json.Unmarshal(rows[0].Value, &value); err != nil {
			t.Fatal(err)
		}
		if value.RangeOffset != strconv.Itoa(seen) || value.RangeCount != "769" {
			t.Fatal(value)
		}
		for _, r := range value.Ranges {
			if r != ranges[seen] {
				t.Fatal("FIFO fragments sorted or skipped", r, seen)
			}
			seen++
		}
		if page == 3 {
			if cursor != "" || seen != 769 {
				t.Fatal(seen, cursor)
			}
			break
		}
		parts := strings.Split(cursor, ":")
		if len(parts) != 3 {
			t.Fatal(cursor)
		}
		table, after = parts[0], parts[1]
		next, err = strconv.ParseUint(parts[2], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err = recipeSatMemberPage(ctx, s, e.snapshot, "outputs", after, 769, 1); err == nil {
		t.Fatal("absent interval cursor accepted")
	}
}

func TestRecipeSatRangePageRejectsIncompleteSegmentImmediately(t *testing.T) {
	tree := newOrdinalTree(t.TempDir(), "")
	if err := tree.put(satRangeKey(0), satRangeSegment077{Ranges: []satRange{{0, 1}}}); err != nil {
		t.Fatal(err)
	}
	root, err := tree.flush()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err = satRangePage077(ctx, tree.dir, satIndexOutput{RangesRoot: root, RangeCount: 2}, 0, 256); !errors.Is(err, ErrSatStateCorrupt) {
		t.Fatal("short segment must fail before waiting for cancellation", err)
	}
	if ctx.Err() != nil {
		t.Fatal("short segment looped until timeout")
	}
	if _, err = satRangePage077(ctx, tree.dir, satIndexOutput{}, 1, 1); !errors.Is(err, ErrSatStateCorrupt) {
		t.Fatal("invalid inline cursor accepted", err)
	}
}

func TestRecipeSatOutputDuplicateCoinbaseCreationsKeepDistinctKeys(t *testing.T) {
	ctx := context.Background()
	s, err := openIndexStore(t.TempDir(), "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	e := newSatEditor077(ctx, s, satIndexSnapshot{Profile: satIndexProfile, Layout: 1})
	for i, height := range []int64{91812, 91842} {
		b := utxoFixtureBlock(height, testID(771888), utxoFixtureCoinbase(771889, 10))
		refs, err := storeBlockOutputFactsContext(ctx, s.profileRoot, b)
		if err != nil {
			t.Fatal(err)
		}
		position := outputCoordinate(0, 0, height)
		ref := refs[position]
		state := "unspent"
		if i == 0 {
			state = "bip30_destroyed"
		}
		o := satIndexOutput{Output: position, FactRef: &ref, Ranges: []satRange{{uint64(100 + i*10), uint64(110 + i*10)}}, RangeCount: 1, State: state}
		if err = e.add(o, b.Transactions[0].TxID, false); err != nil {
			t.Fatal(err)
		}
	}
	if err = e.flush(); err != nil {
		t.Fatal(err)
	}
	rows, cursor, err := recipeSatMemberPage(ctx, s, e.snapshot, "outputs", "", 0, 10)
	if err != nil || cursor != "" || len(rows) != 2 {
		t.Fatal(rows, cursor, err)
	}
	if rows[0].Key == rows[1].Key {
		t.Fatal("duplicate coinbase incarnations collapsed into one row key")
	}
	var first, second map[string]any
	if json.Unmarshal(rows[0].Value, &first) != nil || json.Unmarshal(rows[1].Value, &second) != nil {
		t.Fatal(rows)
	}
	if first["native_outpoint"] != second["native_outpoint"] || first["output"] == second["output"] {
		t.Fatal("creation/native identity distinction lost", first, second)
	}
}
