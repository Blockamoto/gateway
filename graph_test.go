package main

import (
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Graph fixtures below supply synthetic verification receipts to isolate graph semantics.
// Block-byte integrity is exercised independently in integrity_050_test.go.
func initGraphTestApp(t *testing.T) (*app, blockView) {
	t.Helper()
	dataDir := t.TempDir()
	blocksDir := t.TempDir()
	writeSyntheticCoreBlockFile(t, blocksDir, testGenesisBlockPayload(t), [8]byte{})
	a := newCoreMountTestApp(t, dataDir, blocksDir)
	a.settings.GraphIndex = true
	a.coverage = newCoverageState()
	a.initGraphStore()
	a.status.TipHash = genesisHashDisplay
	a.refreshCoreBlockStore()
	h, _ := hex.DecodeString(genesisHeaderHex)
	v, err := parseBlockDetailed(0, genesisHashDisplay, h, testGenesisBlockPayload(t), "test", false)
	if err != nil {
		t.Fatal(err)
	}
	v.Verification.HeaderChainMatch = true
	v.VerificationState = "header_anchored"
	v.CacheVisibility = "public"
	return a, v
}

func appendGraphTestHeader(t *testing.T, a *app, prevHash string, nonce byte) string {
	t.Helper()
	h := make([]byte, 80)
	prev, err := displayHashRaw(prevHash)
	if err != nil {
		t.Fatal(err)
	}
	copy(h[4:36], prev[:])
	h[68] = 0xff
	h[76] = nonce
	d := hash256(h)
	hash := reverseHex(d[:])
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(h); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	a.status.HeaderCount++
	a.status.HeaderHeight++
	a.status.TipHash = hash
	return hash
}

func TestNativeGraphPositiveSpendPersists(t *testing.T) {
	a, genesis := initGraphTestApp(t)
	if err := a.graphIndexBlock(genesis, true); err != nil {
		t.Fatal(err)
	}
	hash1 := appendGraphTestHeader(t, a, genesisHashDisplay, 1)
	prev := genesis.Transactions[0].TxID
	spender := "2222222222222222222222222222222222222222222222222222222222222222"
	v := blockView{
		Height: 1, Hash: hash1, CacheVisibility: "public", VerificationState: "header_anchored",
		Verification: verificationView{VerifierVersion: blockVerifierVersion, HeaderHash: true, ProofOfWork: true, MerkleRoot: true, TransactionsParsed: true, HeaderChainMatch: true},
		Transactions: []transactionView{{Index: 0, TxID: spender, Inputs: []inputView{{N: 0, PrevTxID: prev, PrevVout: 0}}, Outputs: []outputView{{N: 0, ValueSats: 1}}}},
	}
	if err := a.graphIndexBlock(v, true); err != nil {
		t.Fatal(err)
	}
	got, ok := a.graphFindSpend(prev, 0)
	if !ok || got.SpendingTxID != spender || got.SpendingVin != 0 || got.Height != 1 {
		t.Fatalf("unexpected graph spend: %+v ok=%v", got, ok)
	}

	// A restart reuses the on-disk graph without a rebuild.
	a2 := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.status, cacheIndex: newCacheIndex(), coverage: newCoverageState()}
	a2.loadCoverage()
	a2.initGraphStore()
	got2, ok := a2.graphFindSpend(prev, 0)
	if !ok || got2.SpendingTxID != spender {
		t.Fatalf("restart lost graph spend: %+v ok=%v", got2, ok)
	}
}

func TestNativeGraphNegativeRequiresCompleteCoverage(t *testing.T) {
	a, genesis := initGraphTestApp(t)
	if err := a.graphIndexBlock(genesis, true); err != nil {
		t.Fatal(err)
	}
	txid := genesis.Transactions[0].TxID

	snap, why := a.graphCanProveUnspent(txid, 0)
	if snap == nil || snap.Height != 0 {
		t.Fatalf("genesis-only complete graph should prove unspent at genesis: %+v %s", snap, why)
	}

	hash1 := appendGraphTestHeader(t, a, genesisHashDisplay, 2)
	if snap, _ = a.graphCanProveUnspent(txid, 0); snap != nil {
		t.Fatalf("missing height 1 graph coverage must not prove unspent, got %+v", snap)
	}

	// Index the whole next block (with no spend of the genesis output). Now the
	// negative is authoritative through the new snapshot.
	v1 := blockView{Height: 1, Hash: hash1, CacheVisibility: "public", VerificationState: "header_anchored", Verification: verificationView{VerifierVersion: blockVerifierVersion, HeaderHash: true, ProofOfWork: true, MerkleRoot: true, TransactionsParsed: true, HeaderChainMatch: true}, Transactions: []transactionView{{Index: 0, TxID: "3333333333333333333333333333333333333333333333333333333333333333", Coinbase: true, Outputs: []outputView{{N: 0, ValueSats: 1}}}}}
	if err := a.graphIndexBlock(v1, true); err != nil {
		t.Fatal(err)
	}
	if snap, _ = a.graphCanProveUnspent(txid, 0); snap == nil || snap.Height != 1 {
		t.Fatalf("complete height 0..1 graph should prove unspent: %+v", snap)
	}
}

func TestGraphStoreIgnoresNonCanonicalSpend(t *testing.T) {
	a, genesis := initGraphTestApp(t)
	if err := a.graphIndexBlock(genesis, true); err != nil {
		t.Fatal(err)
	}
	hash1 := appendGraphTestHeader(t, a, genesisHashDisplay, 3)
	prev := genesis.Transactions[0].TxID
	canonicalTx := "4444444444444444444444444444444444444444444444444444444444444444"
	v1 := blockView{Height: 1, Hash: hash1, CacheVisibility: "public", VerificationState: "header_anchored", Verification: verificationView{VerifierVersion: blockVerifierVersion, HeaderHash: true, ProofOfWork: true, MerkleRoot: true, TransactionsParsed: true, HeaderChainMatch: true}, Transactions: []transactionView{{Index: 0, TxID: canonicalTx, Inputs: []inputView{{N: 0, PrevTxID: prev, PrevVout: 0}}, Outputs: []outputView{{N: 0, ValueSats: 1}}}}}
	if err := a.graphIndexBlock(v1, true); err != nil {
		t.Fatal(err)
	}
	if got, ok := a.graphFindSpend(prev, 0); !ok || got.SpendingTxID != canonicalTx {
		t.Fatalf("canonical spend missing: %+v %v", got, ok)
	}
}

func TestCoreIndexStatusDistinguishesEnabledAndSynced(t *testing.T) {
	dir, stop := fakeCore(t)
	defer stop()
	st := inspectCore(appSettings{BitcoinDataDir: dir, RPCAuthMode: "auto"})
	if !st.SpenderIndexEnabled || !st.SpenderIndex || st.SpenderIndexHeight != 0 {
		t.Fatalf("spender index status lost: %+v", st)
	}
	if !st.TxIndexEnabled || !st.TxIndex {
		t.Fatalf("txindex status lost: %+v", st)
	}
}

func fakeCoreUnspentGraph(t *testing.T, existingTx string) (appSettings, func()) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".cookie"), []byte("user:pass"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bitcoin.conf"), []byte("rpcport="+strconv.Itoa(port)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		var result any
		switch q.Method {
		case "getblockchaininfo":
			result = map[string]any{"blocks": 0, "headers": 0, "bestblockhash": genesisHashDisplay, "initialblockdownload": false}
		case "getindexinfo":
			result = map[string]any{"txospenderindex": map[string]any{"synced": true, "best_block_height": 0}}
		case "gettxspendingprevout":
			var outs []map[string]any
			if len(q.Params) > 0 {
				_ = json.Unmarshal(q.Params[0], &outs)
			}
			tx, vv := "", float64(0)
			if len(outs) > 0 {
				tx, _ = outs[0]["txid"].(string)
				vv, _ = outs[0]["vout"].(float64)
			}
			result = []any{map[string]any{"txid": tx, "vout": vv}}
		case "gettxout":
			var tx string
			if len(q.Params) > 0 {
				_ = json.Unmarshal(q.Params[0], &tx)
			}
			if strings.EqualFold(tx, existingTx) {
				result = map[string]any{"bestblock": genesisHashDisplay}
			} else {
				result = nil
			}
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"result": nil, "error": map[string]any{"code": -1, "message": "unknown"}, "id": "bod"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil, "id": "bod"})
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	return appSettings{BitcoinDataDir: dir, RPCAuthMode: "auto", GraphIndex: true}, func() { _ = srv.Close() }
}

func TestCoreNegativeNeedsExistingOutpoint(t *testing.T) {
	existing := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	settings, stop := fakeCoreUnspentGraph(t, existing)
	defer stop()
	exists, hash, err := coreConfirmedUnspent(settings, existing, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || hash != genesisHashDisplay {
		t.Fatalf("existing Core UTXO should be authoritative unspent: %v %s", exists, hash)
	}
	missing := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	exists, _, err = coreConfirmedUnspent(settings, missing, 0)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("nonexistent outpoint must not become unspent")
	}
}

func TestNativeGraphReorgDropsDetachedSpend(t *testing.T) {
	a, genesis := initGraphTestApp(t)
	if err := a.graphIndexBlock(genesis, true); err != nil {
		t.Fatal(err)
	}
	hashA := appendGraphTestHeader(t, a, genesisHashDisplay, 10)
	prev := genesis.Transactions[0].TxID
	spendTx := "5555555555555555555555555555555555555555555555555555555555555555"
	va := blockView{Height: 1, Hash: hashA, CacheVisibility: "public", VerificationState: "header_anchored", Verification: verificationView{VerifierVersion: blockVerifierVersion, HeaderHash: true, ProofOfWork: true, MerkleRoot: true, TransactionsParsed: true, HeaderChainMatch: true}, Transactions: []transactionView{{Index: 0, TxID: spendTx, Inputs: []inputView{{N: 0, PrevTxID: prev, PrevVout: 0}}, Outputs: []outputView{{N: 0, ValueSats: 1}}}}}
	if err := a.graphIndexBlock(va, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.graphFindSpend(prev, 0); !ok {
		t.Fatal("branch A spend missing")
	}

	// Replace height 1 in the selected header chain with branch B.
	h := make([]byte, 80)
	rawPrev, _ := displayHashRaw(genesisHashDisplay)
	copy(h[4:36], rawPrev[:])
	h[68] = 0xff
	h[76] = 11
	d := hash256(h)
	hashB := reverseHex(d[:])
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteAt(h, 80); err != nil {
		f.Close()
		t.Fatal(err)
	}
	_ = f.Close()
	a.status.TipHash = hashB
	vb := blockView{Height: 1, Hash: hashB, CacheVisibility: "public", VerificationState: "header_anchored", Verification: verificationView{VerifierVersion: blockVerifierVersion, HeaderHash: true, ProofOfWork: true, MerkleRoot: true, TransactionsParsed: true, HeaderChainMatch: true}, Transactions: []transactionView{{Index: 0, TxID: "6666666666666666666666666666666666666666666666666666666666666666", Coinbase: true, Outputs: []outputView{{N: 0, ValueSats: 1}}}}}
	if err := a.graphIndexBlock(vb, true); err != nil {
		t.Fatal(err)
	}
	if got, ok := a.graphFindSpend(prev, 0); ok {
		t.Fatalf("detached branch spend remained canonical: %+v", got)
	}
	if snap, why := a.graphCanProveUnspent(prev, 0); snap == nil || snap.Hash != hashB {
		t.Fatalf("replacement branch should prove unspent at B: %+v %s", snap, why)
	}
}
