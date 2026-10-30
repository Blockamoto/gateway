package main

import (
	"context"
	"encoding/hex"
	"math"
	"strings"
	"testing"
)

func TestRecipeBitcoinRetentionAgainstLegacyUTXO(t *testing.T) {
	ctx := context.Background()
	s, err := openUTXOStoreContext(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	genesisTx := utxoFixtureCoinbase(51001, 7)
	genesis := utxoFixtureBlock(0, "", genesisTx)
	utxoApply(t, s, genesis)
	expected, err := recipeBitcoinRetainsOutput(0, []byte{0x51})
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := s.LookupOutpoint(ctx, genesisTx.TxID, 0)
	if err != nil || found != expected {
		t.Fatalf("genesis: %v %v %v", expected, found, err)
	}
	scripts := [][]byte{nil, {0x51}, {0x6a}, {0, 0x6a}, make([]byte, 10000), make([]byte, 10001)}
	cb := utxoFixtureCoinbase(51002, 1, 2, 3, 4, 5, 6)
	for i, script := range scripts {
		cb.Outputs[i].ScriptPubKey = hex.EncodeToString(script)
	}
	block := utxoFixtureBlock(1, genesis.Hash, cb)
	utxoApply(t, s, block)
	for i, script := range scripts {
		want, e := recipeBitcoinRetainsOutput(1, script)
		if e != nil {
			t.Fatal(e)
		}
		key, e := recipeBitcoinOutpoint(cb.TxID, uint32(i))
		if e != nil || key != satlineOutpointKey(cb.TxID, uint32(i)) {
			t.Fatal(key, e)
		}
		coin, got, e := s.LookupOutpoint(ctx, cb.TxID, uint32(i))
		if e != nil || got != want {
			t.Fatalf("script %d retention=%v expected=%v err=%v", i, got, want, e)
		}
		if got && (coin.ValueSats != uint64(i+1) || coin.ScriptPubKey != cb.Outputs[i].ScriptPubKey) {
			t.Fatalf("coin mismatch: %+v", coin)
		}
	}
	if _, err = recipeBitcoinRetainsOutput(-1, nil); err == nil {
		t.Fatal("negative height accepted")
	}
	for _, invalid := range []string{"", strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 63)} {
		if _, err = recipeBitcoinOutpoint(invalid, 0); err == nil {
			t.Fatalf("invalid txid accepted: %q", invalid)
		}
	}
	key, err := recipeBitcoinOutpoint(strings.Repeat("a", 64), math.MaxUint32)
	if err != nil || !strings.HasSuffix(key, ":4294967295") {
		t.Fatal(key, err)
	}
}

func TestRecipeBitcoinBIP30AgainstLegacy(t *testing.T) {
	cases := []struct {
		height int64
		hash   string
	}{{91842, "00000000000a4d0a398161ffc163c503763b1f4360639393e0e4c8e300e0caec"}, {91880, "00000000000743f190a18c5577a3c2d2a1f610ae9601ac046a38084ccb7cd721"}, {91842, strings.Repeat("0", 64)}, {91880, strings.Repeat("0", 64)}, {91841, strings.Repeat("0", 64)}}
	for _, c := range cases {
		for _, coinbase := range []bool{false, true} {
			got, err := recipeBitcoinBIP30Exception(recipeBitcoinMainnet, c.height, c.hash, coinbase)
			want := coinbase && utxoBIP30Exception(blockView{Height: c.height, Hash: c.hash})
			if err != nil || got != want {
				t.Fatalf("%+v coinbase=%v got=%v want=%v err=%v", c, coinbase, got, want, err)
			}
		}
	}
	if _, err := recipeBitcoinBIP30Exception("testnet", 91842, cases[0].hash, true); err == nil {
		t.Fatal("unsupported network accepted")
	}
	if _, err := recipeBitcoinBIP30Exception(recipeBitcoinMainnet, 91842, strings.Repeat("A", 64), true); err == nil {
		t.Fatal("noncanonical block hash accepted")
	}
}

func TestRecipeBitcoinMoneyAgainstLegacyUTXO(t *testing.T) {
	if recipeBitcoinMoneyLimit != utxoMaxMoney {
		t.Fatal("money limit mismatch")
	}
	cases := [][]uint64{{0}, {recipeBitcoinMoneyLimit}, {recipeBitcoinMoneyLimit, 0}, {recipeBitcoinMoneyLimit, 1}, {recipeBitcoinMoneyLimit + 1}, {math.MaxUint64}, {1, 2, 3}}
	for n, values := range cases {
		t.Run(string(rune('a'+n)), func(t *testing.T) {
			ctx := context.Background()
			s, err := openUTXOStoreContext(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			genesis := utxoFixtureBlock(0, "", utxoFixtureCoinbase(uint64(52000+n*2), 1))
			utxoApply(t, s, genesis)
			var total uint64
			valid := true
			for _, v := range values {
				total, err = recipeBitcoinMoneyAdd(total, v)
				if err != nil {
					valid = false
					break
				}
			}
			cb := utxoFixtureCoinbase(uint64(52001+n*2), values...)
			block := utxoFixtureBlock(1, genesis.Hash, cb)
			err = s.ApplyBlock(ctx, block)
			if (err == nil) != valid {
				t.Fatalf("legacy accepted=%v helper accepted=%v error=%v", err == nil, valid, err)
			}
			if valid && s.Head().TotalValueSats != total {
				t.Fatalf("legacy total=%d helper=%d", s.Head().TotalValueSats, total)
			}
		})
	}
	for _, c := range []struct {
		total, value uint64
		ok           bool
		want         uint64
	}{{0, 0, true, 0}, {10, 10, true, 0}, {10, 3, true, 7}, {10, 11, false, 0}, {recipeBitcoinMoneyLimit + 1, 1, false, 0}, {math.MaxUint64, 1, false, 0}} {
		got, err := recipeBitcoinMoneySubtract(c.total, c.value)
		if (err == nil) != c.ok || c.ok && got != c.want {
			t.Fatalf("subtract %+v: %d %v", c, got, err)
		}
	}
}

func TestRecipeBitcoinMoneySpendAgainstLegacyUTXO(t *testing.T) {
	ctx := context.Background()
	s, err := openUTXOStoreContext(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	genesis := utxoFixtureBlock(0, "", utxoFixtureCoinbase(53001, 1))
	utxoApply(t, s, genesis)
	coinbase := utxoFixtureCoinbase(53002, 10, 20)
	b1 := utxoFixtureBlock(1, genesis.Hash, coinbase)
	utxoApply(t, s, b1)
	base := s.Head().TotalValueSats
	spend := utxoFixtureSpend(53003, []inputView{{PrevTxID: coinbase.TxID}}, 6)
	cb := utxoFixtureCoinbase(53004, 8)
	b2 := utxoFixtureBlock(2, b1.Hash, cb, spend)
	utxoApply(t, s, b2)
	want, err := recipeBitcoinMoneySubtract(base, 10)
	if err != nil {
		t.Fatal(err)
	}
	want, err = recipeBitcoinMoneyAdd(want, 8)
	if err != nil {
		t.Fatal(err)
	}
	want, err = recipeBitcoinMoneyAdd(want, 6)
	if err != nil || s.Head().TotalValueSats != want {
		t.Fatalf("legacy %d, calculated %d: %v", s.Head().TotalValueSats, want, err)
	}
}
