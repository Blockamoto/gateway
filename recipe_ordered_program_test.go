package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	recipeLegacyUTXOApply(t, legacy, genesis)
	recipeOrderedTestApply(t, m, genesis)
	recipeOrderedTestCompare(t, m, legacy, []blockView{genesis})
	cb := utxoFixtureCoinbase(64002, 10, 20, 3, 4)
	cb.Outputs[2].ScriptPubKey = "6a"
	cb.Outputs[3].ScriptPubKey = strings.Repeat("51", 10001)
	b1 := utxoFixtureBlock(1, genesis.Hash, cb)
	recipeLegacyUTXOApply(t, legacy, b1)
	recipeOrderedTestApply(t, m, b1)
	a := utxoFixtureSpend(64003, []inputView{{PrevTxID: cb.TxID}}, 6)
	b := utxoFixtureSpend(64004, []inputView{{PrevTxID: a.TxID}}, 5)
	c := utxoFixtureSpend(64005, []inputView{{PrevTxID: cb.TxID, PrevVout: 1}}, 19)
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(64006, 10), a, b, c)
	recipeLegacyUTXOApply(t, legacy, b2)
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
	recipeLegacyUTXOApply(t, legacy, g)
	recipeOrderedTestApply(t, m, g)
	cb := utxoFixtureCoinbase(64101, 10)
	b1 := utxoFixtureBlock(1, g.Hash, cb)
	recipeLegacyUTXOApply(t, legacy, b1)
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
		if err = legacy.applyBlockLegacyOracle(ctx, block); err == nil {
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

func TestRecipeOrderedBIP30AgainstLegacyAndUndo(t *testing.T) {
	exceptions := []struct {
		height int64
		hash   string
	}{{91842, "00000000000a4d0a398161ffc163c503763b1f4360639393e0e4c8e300e0caec"}, {91880, "00000000000743f190a18c5577a3c2d2a1f610ae9601ac046a38084ccb7cd721"}}
	for _, exception := range exceptions {
		for _, scenario := range []string{"replacement", "unspendable", "absent", "wrong_hash", "wrong_height", "non_coinbase"} {
			t.Run(fmt.Sprintf("%d_%s", exception.height, scenario), func(t *testing.T) {
				ctx := context.Background()
				legacy, err := openUTXOStoreContext(ctx, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				m := &recipeStateMemoryTest{version: recipeStateVersion{Checkpoint: "origin"}, values: map[recipeStateKey]recipeStateValue{}}
				genesis := utxoFixtureBlock(0, "", utxoFixtureCoinbase(65000, 50))
				recipeLegacyUTXOApply(t, legacy, genesis)
				recipeOrderedTestApply(t, m, genesis)
				old := utxoFixtureCoinbase(65001, 7, 9)
				first := utxoFixtureBlock(1, genesis.Hash, old)
				recipeLegacyUTXOApply(t, legacy, first)
				recipeOrderedTestApply(t, m, first)
				fund := utxoFixtureCoinbase(65002, 11)
				second := utxoFixtureBlock(2, first.Hash, fund)
				recipeLegacyUTXOApply(t, legacy, second)
				recipeOrderedTestApply(t, m, second)
				blockHeight := exception.height
				if scenario == "wrong_height" {
					blockHeight++
				}
				parent := legacy.head.Checkpoint
				parent.Height = blockHeight - 1
				parent.BlockHash = testID(uint64(910000 + exception.height))
				parent.PreviousBlockHash = testID(uint64(900000 + exception.height))
				parent.RecoveryBoundaryHeight = parent.Height
				parent.RecoveryBoundaryHash = parent.BlockHash
				parent.Commitment = utxoCheckpointDigest(parent)
				if err = legacy.writeRecord("commits", parent.Commitment, parent); err != nil {
					t.Fatal(err)
				}
				if err = legacy.publish(parent, parent.Height, parent.BlockHash); err != nil {
					t.Fatal(err)
				}
				replacementTx := old
				switch scenario {
				case "unspendable":
					replacementTx.Outputs = append([]outputView(nil), old.Outputs...)
					replacementTx.Outputs[1].ScriptPubKey = "6a"
				case "absent":
					replacementTx.Outputs = append([]outputView(nil), old.Outputs[:1]...)
				}
				block := utxoFixtureBlock(blockHeight, parent.BlockHash, replacementTx)
				if scenario == "non_coinbase" {
					duplicate := utxoFixtureSpend(65001, []inputView{{PrevTxID: fund.TxID}}, 5)
					block = utxoFixtureBlock(blockHeight, parent.BlockHash, utxoFixtureCoinbase(65003, 1), duplicate)
				}
				block.Hash = exception.hash
				if scenario == "wrong_hash" {
					block.Hash = testID(uint64(exception.height))
				}
				events, err := recipeOrderedBitcoinEvents(ctx, block)
				if err != nil {
					t.Fatal(err)
				}
				tx, err := recipeStateBegin(m, m.version, block.Hash, recipeStateLimits{MaxTouched: 1000, MaxOperations: 50000, MaxValueBytes: 20000, MaxBytes: 200000})
				if err != nil {
					t.Fatal(err)
				}
				before := m.version
				runErr := recipeOrderedExecute(ctx, recipeOrderedUTXOExample(), tx, events)
				legacyErr := legacy.applyBlockLegacyOracle(ctx, block)
				if scenario != "replacement" {
					if runErr == nil || legacyErr == nil {
						t.Fatalf("invalid replacement accepted: ordered=%v legacy=%v", runErr, legacyErr)
					}
					if _, err = tx.Commit(ctx); err == nil || m.version != before {
						t.Fatal("failed replacement published", err)
					}
					for i := uint32(0); i < 2; i++ {
						coin, found, e := legacy.LookupOutpoint(ctx, old.TxID, i)
						if e != nil || !found || coin.Height != 1 {
							t.Fatal("legacy damaged old coin", coin, found, e)
						}
					}
					return
				}
				if runErr != nil || legacyErr != nil {
					t.Fatalf("replacement failed: ordered=%v legacy=%v", runErr, legacyErr)
				}
				receipt, err := tx.Commit(ctx)
				if err != nil {
					t.Fatal(err)
				}
				for _, change := range receipt.Changes {
					if change.Key.Table == "displaced" {
						t.Fatal("temporary displacement escaped net state")
					}
				}
				for i := uint32(0); i < 2; i++ {
					key, _ := recipeBitcoinOutpoint(old.TxID, i)
					var beforeCoin, afterCoin recipeOrderedValue
					matched := false
					for _, change := range receipt.Changes {
						if change.Key == (recipeStateKey{Table: "coins", Key: key}) {
							matched = true
							if !change.Before.Present || !change.After.Present {
								t.Fatal("replacement missing before/after")
							}
							if e := json.Unmarshal(change.Before.Bytes, &beforeCoin); e != nil {
								t.Fatal(e)
							}
							if e := json.Unmarshal(change.After.Bytes, &afterCoin); e != nil {
								t.Fatal(e)
							}
						}
					}
					legacyCoin, found, e := legacy.LookupOutpoint(ctx, old.TxID, i)
					if !matched || e != nil || !found || beforeCoin.Object["height"].Uint != 1 || afterCoin.Object["height"].Uint != uint64(exception.height) || legacyCoin.Height != exception.height || afterCoin.Object["value"].Uint != legacyCoin.ValueSats {
						t.Fatal("replacement mismatch", beforeCoin, afterCoin, legacyCoin, e)
					}
				}
				if _, err = recipeStateUndo(ctx, m, receipt, m.version, recipeStateLimits{MaxTouched: 1000, MaxOperations: 50000, MaxValueBytes: 20000, MaxBytes: 200000}); err != nil {
					t.Fatal(err)
				}
				if err = legacy.Reconcile(ctx, func(_ context.Context, h int64) (string, error) {
					if h == exception.height {
						return testID(999999), nil
					}
					return parent.BlockHash, nil
				}); err != nil {
					t.Fatal(err)
				}
				for i := uint32(0); i < 2; i++ {
					key, _ := recipeBitcoinOutpoint(old.TxID, i)
					raw := m.values[recipeStateKey{Table: "coins", Key: key}]
					var coin recipeOrderedValue
					if e := json.Unmarshal(raw.Bytes, &coin); e != nil {
						t.Fatal(e)
					}
					legacyCoin, found, e := legacy.LookupOutpoint(ctx, old.TxID, i)
					if e != nil || !found || coin.Object["height"].Uint != 1 || legacyCoin.Height != 1 || coin.Object["value"].Uint != legacyCoin.ValueSats {
						t.Fatal("undo mismatch", coin, legacyCoin, e)
					}
				}
			})
		}
	}
}
func TestRecipeOrderedRangeBudgetAborts(t *testing.T) {
	m := &recipeStateMemoryTest{version: recipeStateVersion{Checkpoint: "origin"}, values: map[recipeStateKey]recipeStateValue{}}
	tx, err := recipeStateBegin(m, m.version, "next", recipeStateTestLimits())
	if err != nil {
		t.Fatal(err)
	}
	program := recipeOrderedProgram{Steps: []recipeOrderedInstruction{{Op: "for_range", Name: "i", Expr: roLit(rovUint(recipeOrderedMaxRange + 1))}}}
	if err = recipeOrderedExecute(context.Background(), program, tx, nil); !errors.Is(err, errRecipeStateBudget) {
		t.Fatal("range cap failed", err)
	}
	if _, err = tx.Commit(context.Background()); err == nil {
		t.Fatal("budget failure committed")
	}
}
