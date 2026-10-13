package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func txioTestBlock(t *testing.T, a *app, height int64, prev string, nonce byte, txs ...transactionView) blockView {
	t.Helper()
	b := inscriptionTestBlock(txs...)
	b.Height = height
	b.PreviousBlockHash = prev
	b.Hash = appendGraphTestHeader(t, a, prev, nonce)
	b.Transactions[0].TxID = strings.Repeat(string("0123456789abcdef"[nonce%16]), 64)
	b.Transactions[0].Outputs = []outputView{{N: 0, ValueSats: 50}}
	for i := range b.Transactions {
		if len(b.Transactions[i].Outputs) == 0 {
			b.Transactions[i].Outputs = []outputView{{N: 0, ValueSats: 1}}
		}
	}
	return b
}

func Test074TXIOCoreParentRequiresVerifiedTransactionOrdinal(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	b, e := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "fixture", false)
	if e != nil {
		t.Fatal(e)
	}
	id := b.Transactions[0].TxID
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		var result any
		var rpcError any
		switch q.Method {
		case "getrawtransaction":
			calls.Add(1)
			result = map[string]any{"hex": hex.EncodeToString(raw[81:]), "blockhash": genesisHashDisplay}
		case "getblockheader":
			result = map[string]any{"height": 0, "confirmations": 1}
		case "getblockhash":
			result = genesisHashDisplay
		case "getblockchaininfo":
			result = map[string]any{"blocks": 0, "headers": 0, "bestblockhash": genesisHashDisplay}
		case "getindexinfo":
			result = map[string]any{}
		case "getblock":
			rpcError = map[string]any{"code": -1, "message": "fixture block bytes unavailable"}
		default:
			t.Errorf("unexpected Core method %s", q.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": rpcError})
	}))
	defer server.Close()
	a.settings = testRPCSettings050(t, server.URL)
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = true
	a.cacheIndex.Tx = map[string]txLocation{}
	unpositioned, e := a.flowParentTransaction(context.Background(), id, 1)
	if e != nil || unpositioned.Tx.Index != -1 {
		t.Fatal("fixture did not exercise Core transaction-only path", unpositioned, e)
	}
	positioned, e := a.positionedTXIOParent(context.Background(), id, 1)
	if e != nil || positioned.Tx.Index != 0 || positioned.Height != 0 || positioned.BlockHash != genesisHashDisplay || calls.Load() < 2 {
		t.Fatal("Core parent ordinal not proved from block", positioned, e)
	}
	if e = os.Remove(filepath.Join(a.dataDir, "blocks", "raw", "0-"+genesisHashDisplay+".block")); e != nil {
		t.Fatal(e)
	}
	a.cacheIndex = newCacheIndex()
	cold := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex()}
	if _, e = cold.positionedTXIOParent(context.Background(), id, 1); e == nil {
		t.Fatal("missing block bytes still promoted an unknown ordinal")
	}
}

func Test074TXIOBidirectionalSparseRestartAndRebuild(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = true
	s, err := openIndexStore(a.dataDir, "txo-spender")
	if err != nil {
		t.Fatal(err)
	}
	creation := strings.Repeat("a", 64)
	spender := strings.Repeat("b", 64)
	missing := strings.Repeat("c", 64)
	b := txioTestBlock(t, a, 1, genesisHashDisplay, 1,
		transactionView{TxID: creation, Inputs: []inputView{{PrevTxID: missing, PrevVout: 2}}},
		transactionView{TxID: spender, Inputs: []inputView{{PrevTxID: creation, PrevVout: 0}, {PrevTxID: missing, PrevVout: 9}}})
	if err = s.appendBlock(b, 1, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	// No source bodies are installed for synthetic block1. Lookup therefore
	// demonstrates retained logical relationships independently of raw bytes.
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus()}
	assert := func() {
		t.Helper()
		forward, e := restarted.lookupTXIO(context.Background(), "output", "0.1.1")
		if e != nil || forward.State != "known" || len(forward.Rows) != 1 || forward.Rows[0].Input != "i0.2.1" {
			t.Fatalf("forward: %+v %v", forward, e)
		}
		back, e := restarted.lookupTXIO(context.Background(), "input", "i0.2.1")
		if e != nil || back.State != "known" || back.Rows[0].Output != "0.1.1" || back.Rows[0].PrevTxID != creation || back.Rows[0].TxID != spender {
			t.Fatalf("back: %+v %v", back, e)
		}
		unknown, e := restarted.lookupTXIO(context.Background(), "input", "i1.2.1")
		if e != nil || unknown.State != "unresolved" || len(unknown.Rows) != 1 || unknown.Rows[0].Output != "" || unknown.Rows[0].PrevTxID != missing || unknown.Rows[0].Vout != 9 {
			t.Fatalf("unresolved: %+v %v", unknown, e)
		}
		miss, e := restarted.lookupTXIO(context.Background(), "output", "5.1.1")
		if e != nil || miss.State != "unknown" || len(miss.Rows) != 0 || miss.Absence == "" {
			t.Fatalf("absence: %+v %v", miss, e)
		}
		legacy, e := restarted.lookupIndexSpender(context.Background(), creation+":0")
		if e != nil || len(legacy.Rows) != 1 || legacy.Rows[0].TxID != spender {
			t.Fatalf("legacy API: %+v %v", legacy, e)
		}
	}
	assert()
	// Corrupt only a private pointer receipt, then require reconstruction from
	// immutable spend facts, not interpretation of the old graph byte format.
	path, e := graphShardPath(s.dir, s.locatorKind(), txioKey("output", "0.1.1"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path+".receipt.json", []byte("damaged"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = restarted.lookupTXIO(context.Background(), "output", "0.1.1"); e == nil {
		t.Fatal("corrupt private pointer was accepted")
	}
	indexLocatorMu.Lock()
	e = s.rebuildIndexLocatorShard(context.Background(), path)
	indexLocatorMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	assert()
	if _, e = os.Stat(filepath.Join(s.dir, "spenders-v1")); !os.IsNotExist(e) {
		t.Fatal("old private layout reused", e)
	}
}

func Test074TXIOOriginsSurviveEvictionAndDuplicateIncarnations(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = true
	s, e := openIndexStore(a.dataDir, "txo-spender")
	if e != nil {
		t.Fatal(e)
	}
	duplicate := strings.Repeat("a", 64)
	missing := strings.Repeat("e", 64)
	b1 := txioTestBlock(t, a, 1, genesisHashDisplay, 1, transactionView{TxID: duplicate, Inputs: []inputView{{PrevTxID: missing}}, Outputs: []outputView{{N: 0}}})
	if e = s.appendBlock(b1, 1, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	b2 := txioTestBlock(t, a, 2, b1.Hash, 2, transactionView{TxID: duplicate, Inputs: []inputView{{PrevTxID: missing}}, Outputs: []outputView{{N: 0}, {N: 1}}})
	if e = s.appendBlock(b2, 1, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	s, e = openIndexStore(a.dataDir, "txo-spender")
	if e != nil {
		t.Fatal(e)
	}
	b3 := txioTestBlock(t, a, 3, b2.Hash, 3, transactionView{TxID: strings.Repeat("b", 64), Inputs: []inputView{{PrevTxID: duplicate, PrevVout: 1}}})
	if e = s.appendBlock(b3, 1, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	got, e := a.lookupTXIO(context.Background(), "input", "i0.1.3")
	if e != nil || got.State != "known" || got.Rows[0].Output != "1.1.2" || got.Rows[0].CreationBlockHash != b2.Hash {
		t.Fatal(got, e)
	}
	old, e := a.lookupTXIO(context.Background(), "output", "0.1.1")
	if e != nil || old.State != "unknown" {
		t.Fatal("different incarnation acquired later spend", old, e)
	}
}

func Test074TXIOReorgWithdrawsBothDirectionsAndCreationAnchors(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = true
	s, e := openIndexStore(a.dataDir, "txo-spender")
	if e != nil {
		t.Fatal(e)
	}
	parent := strings.Repeat("a", 64)
	missing := strings.Repeat("e", 64)
	b1 := txioTestBlock(t, a, 1, genesisHashDisplay, 1, transactionView{TxID: parent, Inputs: []inputView{{PrevTxID: missing}}})
	if e = s.appendBlock(b1, 1, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	b2 := txioTestBlock(t, a, 2, b1.Hash, 2, transactionView{TxID: strings.Repeat("b", 64), Inputs: []inputView{{PrevTxID: parent}}})
	if e = s.appendBlock(b2, 1, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	if e = s.reconcile(func(h int64) (string, error) {
		if h == 1 {
			return b1.Hash, nil
		}
		return strings.Repeat("c", 64), nil
	}); e != nil {
		t.Fatal(e)
	}
	for _, q := range []struct{ d, p string }{{"input", "i0.1.2"}, {"output", "0.1.1"}} {
		got, e := a.lookupTXIO(context.Background(), q.d, q.p)
		if e != nil || len(got.Rows) != 0 || got.State != "unknown" {
			t.Fatal("disconnected link visible", got, e)
		}
	}
	if e = s.reconcile(func(int64) (string, error) { return strings.Repeat("d", 64), nil }); e != nil {
		t.Fatal(e)
	}
	got, e := a.lookupTXIO(context.Background(), "output", "0.1.1")
	if e != nil || got.Checkpoint != nil || len(got.Rows) != 0 {
		t.Fatal("full rewind retained lookup", got, e)
	}
}

func Test074TXIOCreationEndpointCheckedWithoutSourceBytes(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = true
	s, e := openIndexStore(a.dataDir, "txo-spender")
	if e != nil {
		t.Fatal(e)
	}
	parent := strings.Repeat("a", 64)
	s.spenderOrigin = func(context.Context, inputView, int64, int) (txioCreation, error) {
		return txioCreation{"0.0.0", genesisHashDisplay}, nil
	}
	b := txioTestBlock(t, a, 1, genesisHashDisplay, 1, transactionView{TxID: strings.Repeat("b", 64), Inputs: []inputView{{PrevTxID: parent}}})
	if e = s.appendBlock(b, 1, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	got, e := a.lookupTXIO(context.Background(), "input", "i0.1.1")
	if e != nil || got.State != "known" {
		t.Fatal(got, e)
	}
	f, e := os.OpenFile(a.headersPath, os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.WriteAt([]byte{42}, 76)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.lookupTXIO(context.Background(), "input", "i0.1.1"); e == nil {
		t.Fatal("creation branch mismatch ignored")
	}
}

func Test074TXIOLegacyCanonicalRowsRemainReadable(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	s, e := openIndexStore(a.dataDir, "txo-spender")
	if e != nil {
		t.Fatal(e)
	}
	prev := strings.Repeat("a", 64)
	b := txioTestBlock(t, a, 1, genesisHashDisplay, 1, transactionView{TxID: strings.Repeat("b", 64), Inputs: []inputView{{PrevTxID: prev, PrevVout: 2}}})
	legacy, e := deriveIndexSpenders(b)
	if e != nil {
		t.Fatal(e)
	}
	before := batchRecordsHash(indexBatch{PreviousBlockHash: b.PreviousBlockHash, Spenders: legacy})
	if e = s.commitPreparedIndexBatch(indexBatch{PreviousBlockHash: b.PreviousBlockHash, Spenders: legacy}, 1, b.Hash, 1, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	read, e := s.readBatch(s.head.Commitment)
	if e != nil || batchRecordsHash(read) != before || read.Spenders.Version != 0 {
		t.Fatal("legacy commitment changed", read, e)
	}
	got, e := a.lookupTXIO(context.Background(), "input", "i0.1.1")
	if e != nil || got.State != "unresolved" || got.Rows[0].PrevTxID != prev {
		t.Fatal(got, e)
	}
}

func Test074TXIOExactGrammarCancellationAndStorageMeasurement(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	for _, q := range []struct{ d, p string }{{"input", "i00.1.1"}, {"input", "0.1.1"}, {"output", "i0.1.1"}, {"output", "0.1.1.bitcoin"}, {"bad", "0.1.1"}} {
		if _, e := a.lookupTXIO(context.Background(), q.d, q.p); e == nil {
			t.Fatal("noncanonical position accepted", q)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := a.lookupTXIO(ctx, "input", "i0.1.1"); e == nil {
		t.Fatal("cancelled lookup proceeded")
	}
	w := httptest.NewRecorder()
	a.handleTXIOLookup(w, httptest.NewRequest("POST", "/api/v1/txio/lookup", nil))
	if w.Code != 405 {
		t.Fatal(w.Code, w.Body.String())
	}
	b := inscriptionTestBlock(transactionView{TxID: strings.Repeat("a", 64), Inputs: []inputView{{PrevTxID: strings.Repeat("e", 64)}, {PrevTxID: strings.Repeat("e", 64), PrevVout: 1}}, Outputs: []outputView{{N: 0}}}, transactionView{TxID: strings.Repeat("b", 64), Inputs: []inputView{{PrevTxID: strings.Repeat("a", 64)}}, Outputs: []outputView{{N: 0}}})
	b.Transactions[0].Outputs = []outputView{{N: 0}}
	s, _ := openIndexStore(a.dataDir, "txo-spender")
	modern, e := s.deriveTXIOBlock(b)
	if e != nil {
		t.Fatal(e)
	}
	legacy, e := deriveIndexSpenders(b)
	if e != nil {
		t.Fatal(e)
	}
	oldJSON, _ := json.Marshal(legacy)
	newJSON, _ := json.Marshal(modern)
	keys := txioBatchKeys(indexBatch{Checkpoint: indexCheckpoint{Height: b.Height}, Spenders: modern})
	t.Logf("Measured fixture (3 spends,3 origin transactions,2 distinct previous IDs): legacy canonical %d bytes; TXIO canonical %d bytes; private pointer payload %d bytes; active commit table36 bytes/block; JSON receipts and filesystem allocation additional", len(oldJSON), len(newJSON), len(keys)*indexLocatorRecordBytes)
	if modern.Version != 3 || len(modern.Origins) != 0 || len(modern.PreviousTransactions) != 0 || modern.Rows[0].PreviousTransaction != nil || modern.Rows[0].PrevTxID != "" || modern.Rows[0].TxID != "" || len(modern.PendingTranslations) != 2 || modern.Rows[2].Output == "" {
		t.Fatal("new resolved positional records retained transaction identities or lost sparse evidence")
	}
}
