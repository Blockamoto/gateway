package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func recipeOrderedTestApply(t *testing.T, m *recipeStateMemoryTest, b blockView) recipeStateReceipt {
	t.Helper()
	events, err := recipeOrderedBitcoinEvents(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := recipeStateBegin(m, m.version, b.Hash, recipeStateLimits{MaxTouched: 1000, MaxOperations: 50000, MaxValueBytes: 20000, MaxBytes: 200000})
	if err != nil {
		t.Fatal(err)
	}
	if err = recipeOrderedExecute(context.Background(), recipeOrderedUTXOExample(), tx, events); err != nil {
		t.Fatalf("ordered block %d: %v", b.Height, err)
	}
	r, err := tx.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRecipeOrderedCanonicalIntegerJSON(t *testing.T) {
	for _, n := range []uint64{0, 1<<53 + 1, math.MaxUint64} {
		data, err := json.Marshal(rovUint(n))
		if err != nil {
			t.Fatal(err)
		}
		var got recipeOrderedValue
		if err = json.Unmarshal(data, &got); err != nil || got.Kind != "uint" || got.Uint != n {
			t.Fatalf("%s: %+v %v", data, got, err)
		}
		if !strings.Contains(string(data), `"uint":"`) {
			t.Fatalf("not a decimal string: %s", data)
		}
	}
	for _, data := range []string{`{"kind":"uint","uint":9007199254740993}`, `{"kind":"uint","uint":"01"}`, `{"kind":"uint","uint":"+1"}`, `{"kind":"uint","uint":"-1"}`, `{"kind":"uint","uint":"18446744073709551616"}`, `{"kind":"uint"}`} {
		var value recipeOrderedValue
		if err := json.Unmarshal([]byte(data), &value); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	if _, err := json.Marshal(rovText(string([]byte{0xff}))); err == nil {
		t.Fatal("invalid UTF-8 would be silently replaced")
	}
	data, err := json.Marshal(recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{}})
	if err != nil {
		t.Fatal(err)
	}
	var empty recipeOrderedValue
	if err = json.Unmarshal(data, &empty); err != nil || empty.Object == nil || len(empty.Object) != 0 {
		t.Fatalf("empty object lost: %s %v", data, err)
	}
}

func TestRecipeOrderedLoopWorkBound(t *testing.T) {
	m := &recipeStateMemoryTest{version: recipeStateVersion{Checkpoint: "origin"}, values: map[recipeStateKey]recipeStateValue{}}
	tx, err := recipeStateBegin(m, m.version, "next", recipeStateTestLimits())
	if err != nil {
		t.Fatal(err)
	}
	events := make([]map[string]recipeOrderedValue, 2000)
	p := recipeOrderedProgram{Steps: []recipeOrderedInstruction{{Op: "for_events", Body: []recipeOrderedInstruction{{Op: "for_events"}}}}}
	if err = recipeOrderedExecute(context.Background(), p, tx, events); !errors.Is(err, errRecipeStateBudget) {
		t.Fatal("empty nested loops escaped work bound", err)
	}
	if _, err = tx.Commit(context.Background()); err == nil {
		t.Fatal("budget failure could commit")
	}
}

func TestRecipeOrderedGenericProgram(t *testing.T) {
	ctx := context.Background()
	m := &recipeStateMemoryTest{version: recipeStateVersion{Checkpoint: "origin"}, values: map[recipeStateKey]recipeStateValue{}}
	tx, err := recipeStateBegin(m, m.version, "one", recipeStateTestLimits())
	if err != nil {
		t.Fatal(err)
	}
	events := []map[string]recipeOrderedValue{{"name": rovText("a"), "delete": rovBool(false), "amount": rovUint(9007199254740993)}, {"name": rovText("a"), "delete": rovBool(true)}, {"name": rovText("b"), "delete": rovBool(false), "amount": rovUint(4)}}
	p := recipeOrderedProgram{Steps: []recipeOrderedInstruction{{Op: "for_events", Body: []recipeOrderedInstruction{{Op: "if", Expr: roField("delete"), Body: []recipeOrderedInstruction{{Op: "delete", Table: "other", Key: roField("name")}}, Else: []recipeOrderedInstruction{{Op: "put", Table: "other", Key: roField("name"), Expr: roField("amount")}}}}}}}
	if err = recipeOrderedExecute(ctx, p, tx, events); err != nil {
		t.Fatal(err)
	}
	r, err := tx.Commit(ctx)
	if err != nil || len(r.BeforeImages) != 2 || len(r.Changes) != 1 || r.Changes[0].Key.Key != "b" {
		t.Fatal(r, err)
	}
}
func recipeOrderedTestCompare(t *testing.T, m *recipeStateMemoryTest, s *utxoStore, blocks []blockView) {
	t.Helper()
	ctx := context.Background()
	var sum, count uint64
	for _, b := range blocks {
		for _, tx := range b.Transactions {
			for i := range tx.Outputs {
				key, err := recipeBitcoinOutpoint(tx.TxID, uint32(i))
				if err != nil {
					t.Fatal(err)
				}
				raw, ok := m.values[recipeStateKey{Table: "coins", Key: key}]
				legacy, found, err := s.LookupOutpoint(ctx, tx.TxID, uint32(i))
				if err != nil || found != ok {
					t.Fatalf("%s legacy=%v ordered=%v err=%v", key, found, ok, err)
				}
				if !ok {
					continue
				}
				var coin recipeOrderedValue
				if err = json.Unmarshal(raw.Bytes, &coin); err != nil {
					t.Fatal(err)
				}
				o := coin.Object
				if o["txid"].Text != legacy.TxID || o["vout"].Uint != uint64(legacy.Vout) || o["height"].Uint != uint64(legacy.Height) || o["block"].Text != legacy.BlockHash || o["tx_index"].Uint != uint64(legacy.TxIndex) || o["coinbase"].Bool != legacy.Coinbase || o["value"].Uint != legacy.ValueSats || strings.ToLower(fmtHex(o["script"].Bytes)) != legacy.ScriptPubKey {
					t.Fatalf("coin mismatch %+v %+v", o, legacy)
				}
				sum += legacy.ValueSats
				count++
			}
		}
	}
	if sum != s.Head().TotalValueSats || count != s.Head().LiveOutputs {
		t.Fatalf("totals %d/%d vs %d/%d", sum, count, s.Head().TotalValueSats, s.Head().LiveOutputs)
	}
}
func fmtHex(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, x := range b {
		out[2*i] = digits[x>>4]
		out[2*i+1] = digits[x&15]
	}
	return string(out)
}
func TestRecipeOrderedUTXOAgainstLegacy(t *testing.T) {
	ctx := context.Background()
	legacy, err := openUTXOStoreContext(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &recipeStateMemoryTest{version: recipeStateVersion{Checkpoint: "origin"}, values: map[recipeStateKey]recipeStateValue{}}
	genesis := utxoFixtureBlock(0, "", utxoFixtureCoinbase(64001, 50))
	utxoApply(t, legacy, genesis)
	recipeOrderedTestApply(t, m, genesis)
	recipeOrderedTestCompare(t, m, legacy, []blockView{genesis})
	cb := utxoFixtureCoinbase(64002, 10, 20, 3, 4)
	cb.Outputs[2].ScriptPubKey = "6a"
	cb.Outputs[3].ScriptPubKey = strings.Repeat("51", 10001)
	b1 := utxoFixtureBlock(1, genesis.Hash, cb)
	utxoApply(t, legacy, b1)
	recipeOrderedTestApply(t, m, b1)
	a := utxoFixtureSpend(64003, []inputView{{PrevTxID: cb.TxID}}, 6)
	b := utxoFixtureSpend(64004, []inputView{{PrevTxID: a.TxID}}, 5)
	c := utxoFixtureSpend(64005, []inputView{{PrevTxID: cb.TxID, PrevVout: 1}}, 19)
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(64006, 10), a, b, c)
	utxoApply(t, legacy, b2)
	receipt := recipeOrderedTestApply(t, m, b2)
	recipeOrderedTestCompare(t, m, legacy, []blockView{genesis, b1, b2})
	if _, err = recipeStateUndo(ctx, m, receipt, m.version, recipeStateTestLimits()); err != nil {
		t.Fatal(err)
	}
	if err = legacy.Reconcile(ctx, func(_ context.Context, h int64) (string, error) {
		if h == 2 {
			return testID(999999), nil
		}
		return b1.Hash, nil
	}); err != nil {
		t.Fatal(err)
	}
	recipeOrderedTestCompare(t, m, legacy, []blockView{genesis, b1})
}
func TestRecipeOrderedRejectsAndAborts(t *testing.T) {
	ctx := context.Background()
	legacy, err := openUTXOStoreContext(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &recipeStateMemoryTest{version: recipeStateVersion{Checkpoint: "origin"}, values: map[recipeStateKey]recipeStateValue{}}
	g := utxoFixtureBlock(0, "", utxoFixtureCoinbase(64100, 1))
	utxoApply(t, legacy, g)
	recipeOrderedTestApply(t, m, g)
	cb := utxoFixtureCoinbase(64101, 10)
	b1 := utxoFixtureBlock(1, g.Hash, cb)
	utxoApply(t, legacy, b1)
	recipeOrderedTestApply(t, m, b1)
	cases := []blockView{utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(64102, 1), utxoFixtureSpend(64103, []inputView{{PrevTxID: cb.TxID}}, 11)), utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(64101, 1)), utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(64104, 1), utxoFixtureSpend(64105, []inputView{{PrevTxID: testID(777)}}, 1))}
	for _, block := range cases {
		events, err := recipeOrderedBitcoinEvents(ctx, block)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := recipeStateBegin(m, m.version, block.Hash, recipeStateTestLimits())
		if err != nil {
			t.Fatal(err)
		}
		before := m.version
		if err = recipeOrderedExecute(ctx, recipeOrderedUTXOExample(), tx, events); err == nil {
			t.Fatal("invalid transition accepted")
		}
		if _, err = tx.Commit(ctx); err == nil {
			t.Fatal("failed execution could commit")
		}
		if m.version != before {
			t.Fatal("failed transition mutated state")
		}
		if err = legacy.ApplyBlock(ctx, block); err == nil {
			t.Fatal("legacy accepted invalid block")
		}
	}
	cancel, cancelFn := context.WithCancel(ctx)
	cancelFn()
	tx, _ := recipeStateBegin(m, m.version, testID(888), recipeStateTestLimits())
	if err = recipeOrderedExecute(cancel, recipeOrderedUTXOExample(), tx, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = tx.Commit(ctx); err == nil {
		t.Fatal("cancelled program could commit")
	}
}
