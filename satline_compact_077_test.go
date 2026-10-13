package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func satline077Fixture(t *testing.T) (*app, blockView, blockView, *atomic.Int64, func()) {
	t.Helper()
	ctx := context.Background()
	a := independentTestApp(t)
	parent := utxoFixtureBlock(1, testID(70000), utxoFixtureCoinbase(70001, 100, 200))
	spend := utxoFixtureBlock(2, parent.Hash, utxoFixtureCoinbase(70002, subsidyAtHeight(2)+90), utxoFixtureSpend(70003, []inputView{{PrevTxID: parent.Transactions[0].TxID, PrevVout: 0}}, 90), utxoFixtureSpend(70004, []inputView{{PrevTxID: parent.Transactions[0].TxID, PrevVout: 1}}, 120))
	s, err := openIndexStore(a.dataDir, "txo-spender")
	if err != nil {
		t.Fatal(err)
	}
	s.retainOutputValues = true
	if err = s.appendBlock(parent, 1, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	if err = s.appendBlock(spend, 1, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	if err = storeSatFeeBlockContext(ctx, a.dataDir, spend, [][]uint64{nil, {100}, {200}}); err != nil {
		t.Fatal(err)
	}
	settings, rawCalls, closeRPC := satline077RPC(t, map[int64]string{1: parent.Hash, 2: spend.Hash})
	a.settings = settings
	a.settings.SatlineEnabled = true
	a.settings.NetworkDisabled = true
	a.status = appStatus{HeaderHeight: -1}
	a.initSatlineStore()
	return a, parent, spend, rawCalls, closeRPC
}
func satline077RPC(t *testing.T, hashes map[int64]string) (appSettings, *atomic.Int64, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = os.WriteFile(filepath.Join(dir, ".cookie"), []byte("user:pass"), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := &atomic.Int64{}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		mu.Lock()
		defer mu.Unlock()
		var result any
		var rpcError any
		tip := int64(-1)
		for h := range hashes {
			if h > tip {
				tip = h
			}
		}
		switch request.Method {
		case "getblockchaininfo":
			result = map[string]any{"blocks": tip, "headers": tip, "bestblockhash": hashes[tip], "pruned": false, "initialblockdownload": false}
		case "getindexinfo":
			result = map[string]any{}
		case "getblockhash":
			var h int64
			_ = json.Unmarshal(request.Params[0], &h)
			if hash, ok := hashes[h]; ok {
				result = hash
			} else {
				rpcError = map[string]any{"code": -8, "message": "height unavailable"}
			}
		case "getblockheader":
			var hash string
			_ = json.Unmarshal(request.Params[0], &hash)
			for h, v := range hashes {
				if v == hash {
					result = map[string]any{"height": h, "confirmations": 1}
				}
			}
			if result == nil {
				rpcError = map[string]any{"code": -5, "message": "header unavailable"}
			}
		default:
			if request.Method == "getblock" || request.Method == "getrawtransaction" {
				calls.Add(1)
			}
			rpcError = map[string]any{"code": -32601, "message": "raw evidence unavailable"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": rpcError, "id": "bod"})
	})}
	go srv.Serve(ln)
	return appSettings{BitcoinDataDir: dir, RPCAuthMode: "cookie", RPCPort: ln.Addr().(*net.TCPAddr).Port, CoreMountDisabled: true}, calls, func() { _ = srv.Close(); _ = ln.Close() }
}

func Test077SatlineCompactForwardFeeAndReverseWithoutRawOrFullRanges(t *testing.T) {
	a, parent, spend, calls, closeRPC := satline077Fixture(t)
	defer closeRPC()
	ctx := context.Background()
	input := "150.1.0.1.bitcoin"
	r := newSatlineResolver(newSatlineEvidenceBackend077(a, ctx))
	r.ctx = ctx
	result := r.follow(input, 1)
	if result.State != "STEP_LIMIT" || len(result.Hops) != 1 || result.Hops[0].Type != "fee_to_coinbase" || result.Hops[0].TransactionFeeSats != 80 || result.Hops[0].PriorBlockFeesSats != 10 || result.Hops[0].CoinbaseStreamOffset != subsidyAtHeight(2)+40 || result.CurrentSatpoint.TxID != spend.Transactions[0].TxID {
		t.Fatalf("compact forward FIFO: %+v", result)
	}
	position := fmt.Sprintf("%d.0.0.2.bitcoin", subsidyAtHeight(2)+40)
	back, err := a.reverseSatlineSatpointContext(ctx, position)
	want := firstSatAtHeight(1) + 250
	if err != nil || back.State != "known" || back.SatNumber == nil || *back.SatNumber != want || back.Steps != 2 || back.Snapshot.Hash != spend.Hash {
		t.Fatal(back, err)
	}
	if calls.Load() != 0 {
		t.Fatal("sufficient local FIFO facts triggered raw RPC", calls.Load())
	}
	if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", "sat-state", "head.json")); !os.IsNotExist(err) {
		t.Fatal("targeted trace silently built Sat index", err)
	}
	if err = os.Rename(filepath.Join(a.dataDir, "indexes", "txo-spender"), filepath.Join(a.dataDir, "indexes", "txo-spender-evicted-fixture")); err != nil {
		t.Fatal(err)
	}
	cached, err := a.reverseSatlineSatpointContext(ctx, position)
	if err != nil || cached.SatNumber == nil || *cached.SatNumber != want || calls.Load() != 0 {
		t.Fatal("visited identity did not survive source eviction", cached, err, calls.Load())
	}
	_ = parent
}
func Test077SatlineFeePagesBoundariesCorruptionAndInterruptedPublication(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	txs := []transactionView{utxoFixtureCoinbase(80000, subsidyAtHeight(3)+5990)}
	values := [][]uint64{nil}
	for i := 1; i < 600; i++ {
		txs = append(txs, utxoFixtureSpend(uint64(80000+i), []inputView{{PrevTxID: testID(uint64(i)), PrevVout: 0}}, 90))
		values = append(values, []uint64{100})
	}
	block := utxoFixtureBlock(3, testID(79999), txs...)
	if err := storeSatFeeBlockContext(ctx, root, block, values); err != nil {
		t.Fatal(err)
	}
	manifest, err := readSatFeeBlockContext(ctx, root, 3, block.Hash)
	if err != nil || len(manifest.Pages) != 3 || manifest.Total != 5990 {
		t.Fatal(manifest, err)
	}
	row, err := satFeeRowContext(ctx, root, 3, block.Hash, 256)
	if err != nil || row.Before != 2550 || row.Fee != 10 {
		t.Fatal(row, err)
	}
	prefix, err := satFeePrefixContext(ctx, root, 3, block.Hash, 600)
	if err != nil || len(prefix) != 601 || prefix[600] != 5990 {
		t.Fatal(prefix, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	other := block
	other.Hash = testID(89999)
	if err = storeSatFeeBlockContext(cancelled, root, other, values); err == nil {
		t.Fatal("cancel ignored")
	}
	path, _ := satFeeBlockPath(root, 3, other.Hash)
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("cancelled receipt published", err)
	}
	ref := manifest.Pages[1].Hash
	path = filepath.Join(satFeeDirectory(root), "pages", ref[:2], ref+".json")
	if err = os.WriteFile(path, []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = satFeeRowContext(ctx, root, 3, block.Hash, 256); err == nil {
		t.Fatal("damaged fee page supplied arithmetic")
	}
	if err = storeSatFeeBlockContext(ctx, root, block, values); err != nil {
		t.Fatal(err)
	}
	if _, err = satFeePrefixContext(ctx, root, 3, block.Hash, 600); err != nil {
		t.Fatal("verified reprocessing did not repair cache", err)
	}
}
func Test077SatlineCompactMissingValuesRemainUnresolved(t *testing.T) {
	a, _, spend, _, closeRPC := satline077Fixture(t)
	defer closeRPC()
	ctx := context.Background()
	s, b, err := satlineTXIOBatchContext(ctx, a.dataDir, 2, spend.Hash)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := satlineTXIORefsContext(ctx, s, b)
	if err != nil {
		t.Fatal(err)
	}
	ref := refs["0.0.2"]
	path, _ := outputFactPath(a.dataDir, ref.Page)
	if err = os.WriteFile(path, []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = satlineTXIOBlockContext(ctx, a.dataDir, 2, spend.Hash); err == nil {
		t.Fatal("damaged output value accepted")
	}
	result, err := a.reverseSatlineSatpointContext(ctx, strconv.FormatUint(subsidyAtHeight(2)+40, 10)+".0.0.2.bitcoin")
	if err == nil || result.State == "known" || result.SatNumber != nil {
		t.Fatal("missing compact/raw evidence became known", result, err)
	}
}
func Test077SatlineBurnedAndGenesisNeverClaimUnspent(t *testing.T) {
	b := testBlock(1, testCoinbase(testID(90001), 100))
	b.Transactions[0].Outputs[0].ScriptPubKey = "6a01ff"
	backend := &memorySatlineBackend{blocks: map[int64]blockView{1: b}, tip: 1}
	r := newSatlineResolver(backend)
	r.cacheBlock(b)
	result := r.traverse(satlinePoint{TxID: b.Transactions[0].TxID, Vout: 0, Offset: 0, Height: 1, BlockHash: b.Hash, TxIndex: 0}, 1)
	if result.State != "LOST_BURNED_OUTPUT" || len(result.Hops) != 0 {
		t.Fatal(result)
	}
	genesis := testBlock(0, testCoinbase(testID(90002), 100))
	genesis.Hash = genesisHashDisplay
	backend.blocks[0], backend.tip = genesis, 0
	r = newSatlineResolver(backend)
	r.cacheBlock(genesis)
	result = r.traverse(satlinePoint{TxID: genesis.Transactions[0].TxID, Vout: 0, Height: 0, BlockHash: genesisHashDisplay, TxIndex: 0}, 1)
	if result.State != "GENESIS_UNSPENDABLE" {
		t.Fatal(result)
	}
}

func Test077SatlineUnspendableScriptThresholdRawAndCompact(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		burned       bool
	}{{"limit", strings.Repeat("51", 10000), false}, {"oversized", strings.Repeat("51", 10001), true}, {"opreturn", "6A01ff", true}, {"missing", "", false}, {"malformed", "6azz", false}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := satlineUnspendableScript077(tc.script); got != tc.burned {
				t.Fatalf("script threshold: got %v want %v", got, tc.burned)
			}
			if tc.name == "malformed" {
				return // Authenticated fact writers reject malformed hex.
			}
			block := utxoFixtureBlock(1, genesisHashDisplay, utxoFixtureCoinbase(94001, 100))
			block.Transactions[0].Outputs[0].ScriptPubKey = strings.ToLower(tc.script)
			point := satlinePoint{TxID: block.Transactions[0].TxID, Vout: 0, Height: 1, BlockHash: block.Hash, TxIndex: 0}
			rawBackend := &memorySatlineBackend{blocks: map[int64]blockView{1: block}, tip: 1}
			r := newSatlineResolver(rawBackend)
			r.cacheBlock(block)
			result := r.traverse(point, 1)
			if (result.State == "LOST_BURNED_OUTPUT") != tc.burned {
				t.Fatal("raw path disagrees with unspendability threshold", result)
			}
			a := independentTestApp(t)
			s, err := openIndexStore(a.dataDir, "txo-spender")
			if err != nil {
				t.Fatal(err)
			}
			s.retainOutputValues, s.retainOutputScripts = true, true
			if err = s.appendBlock(block, 1, "pruned"); err != nil {
				t.Fatal(err)
			}
			settings, rawCalls, stop := satline077RPC(t, map[int64]string{1: block.Hash})
			defer stop()
			a.settings = settings
			a.status = appStatus{HeaderHeight: -1}
			backend := newSatlineEvidenceBackend077(a, context.Background())
			terminal := backend.TerminalPoint(point)
			if (terminal == "LOST_BURNED_OUTPUT") != tc.burned || rawCalls.Load() != 0 {
				t.Fatal("compact path disagrees or fetched raw evidence", terminal, rawCalls.Load())
			}
			if tc.burned {
				r = newSatlineResolver(backend)
				result = r.traverse(point, 1)
				if result.State != "LOST_BURNED_OUTPUT" || len(result.Hops) != 0 || rawCalls.Load() != 0 {
					t.Fatal("compact burned output attempted spender discovery", result, rawCalls.Load())
				}
			}
		})
	}
}
