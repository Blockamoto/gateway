package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func Test078RelationshipReadinessSavedSourcesAndMissingSpend(t *testing.T) {
	a, b := initGraphTestApp(t)
	a.settings.CoreDisabled = true
	tx := b.Transactions[0]
	p := relationshipTxProof{Height: b.Height, Hash: b.Hash, TxID: tx.TxID, Index: 0, Outputs: 1, Inputs: tx.Inputs}
	got := a.relationshipReadinessFor(context.Background(), p)
	if len(got.Inputs) != 1 || got.Inputs[0].Available || !strings.Contains(got.Inputs[0].Reason, "Coinbase") || len(got.Outputs) != 1 || got.Outputs[0].Available || !strings.Contains(got.Outputs[0].Reason, "not proof of unspentness") {
		t.Fatal(got)
	}
	hash := appendGraphTestHeader(t, a, b.Hash, 71)
	a.cacheIndex.Tx[tx.TxID] = txLocation{TxID: tx.TxID, Height: 0, BlockHash: b.Hash, TxIndex: 0}
	a.cacheIndex.Spends[tx.TxID+":0"] = spendLocation{Found: true, Outpoint: tx.TxID + ":0", SpendingTxID: testID(78001), InputIndex: 0, Height: 1, BlockHash: hash}
	got = a.relationshipReadinessFor(context.Background(), p)
	if !got.Outputs[0].Available || !strings.Contains(got.Outputs[0].Source, "saved local positive spend") {
		t.Fatal(got)
	}
	// A native outpoint hit must not enable the action for a different
	// historical creation incarnation, even when its spender is selected.
	a.cacheIndex.Tx[tx.TxID] = txLocation{TxID: tx.TxID, Height: 1, BlockHash: hash, TxIndex: 0}
	got = a.relationshipReadinessFor(context.Background(), p)
	if got.Outputs[0].Available {
		t.Fatal("wrong historical creation enabled spender", got)
	}
	child := relationshipTxProof{Height: 1, Hash: hash, Inputs: []inputView{{N: 0, PrevTxID: tx.TxID, PrevVout: 0}}, Parents: map[string]txLocation{tx.TxID: {Height: 1, BlockHash: hash, TxIndex: 1}}}
	got = a.relationshipReadinessFor(context.Background(), child)
	if len(got.Inputs) != 1 || !got.Inputs[0].Available || got.Inputs[0].Target != "0.1.1" {
		t.Fatal(got)
	}
}

func Test078RelationshipReadinessCoreRouteNeedsNoInventedOrdinal(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	b, e := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "fixture", false)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		var result any
		switch q.Method {
		case "getrawtransaction":
			result = map[string]any{"hex": hex.EncodeToString(raw[81:]), "blockhash": genesisHashDisplay}
		case "getblockheader":
			result = map[string]any{"height": 0, "confirmations": 1}
		default:
			t.Errorf("unexpected RPC %s", q.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil})
	}))
	defer server.Close()
	a.settings = testRPCSettings050(t, server.URL)
	a.cacheIndex = newCacheIndex()
	p := relationshipTxProof{Height: 1, Hash: testID(78002), Inputs: []inputView{{N: 0, PrevTxID: b.Transactions[0].TxID, PrevVout: 0}}}
	got := a.relationshipReadinessFor(context.Background(), p)
	if len(got.Inputs) != 1 || !got.Inputs[0].Available || got.Inputs[0].Target != "" || !strings.Contains(got.Inputs[0].Source, "Core RPC") {
		t.Fatal(got)
	}
	if !strings.Contains(got.Note, "does not request historical locator hints") {
		t.Fatal(got.Note)
	}
}

func Test078RelationshipReadinessRequiresResolvedVerifiedBlock(t *testing.T) {
	a, b := initGraphTestApp(t)
	a.settings.CoreDisabled = true
	request := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		a.handleRelationshipReadiness(w, httptest.NewRequest("GET", "/api/v1/txio/readiness?height=0&block_hash="+b.Hash+"&tx_index=0", nil))
		return w
	}
	if w := request(); w.Code != 200 || !strings.Contains(w.Body.String(), "Resolve this transaction") {
		t.Fatal(w.Code, w.Body.String())
	}
	a.rememberRelationshipTransaction(b, b.Transactions[0])
	if w := request(); w.Code != 200 || !strings.Contains(w.Body.String(), "Coinbase inputs") {
		t.Fatal(w.Code, w.Body.String())
	}
	b.Height = 1
	a.rememberRelationshipTransaction(b, b.Transactions[0])
	if w := request(); !strings.Contains(w.Body.String(), "Resolve this transaction") {
		t.Fatal("height mismatch reused proof", w.Body.String())
	}
}

func Test078CoinbaseMessageExactBytesAndReadableText(t *testing.T) {
	raw := testGenesisBlockPayload(t)
	b, e := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "fixture", false)
	if e != nil {
		t.Fatal(e)
	}
	m := b.CoinbaseMessage
	if m == nil || !strings.Contains(m.Text, "The Times 03/Jan/2009 Chancellor on brink of second bailout for banks") || m.Hex != b.Transactions[0].Inputs[0].ScriptSig || !strings.Contains(m.Note, "does not identify") {
		t.Fatal(m)
	}
	if coinbaseMessageForTransactions(nil) != nil || coinbaseMessageForTransactions([]transactionView{{}}) != nil {
		t.Fatal("non-coinbase message invented")
	}
	tx := b.Transactions[0]
	tx.Inputs = append([]inputView(nil), tx.Inputs...)
	tx.Inputs[0].ScriptSig = "zz"
	if coinbaseMessageForTransactions([]transactionView{tx}) != nil {
		t.Fatal("malformed bytes decoded")
	}
}

func Test078ReadinessPagesDoNotImplyUncheckedAbsence(t *testing.T) {
	a, b := initGraphTestApp(t)
	a.settings.CoreDisabled = true
	p := relationshipTxProof{Height: b.Height, Hash: b.Hash}
	for n := 0; n < 130; n++ {
		p.Inputs = append(p.Inputs, inputView{N: n, Coinbase: true})
	}
	first := a.relationshipReadinessPage(context.Background(), p, 0)
	if len(first.Inputs) != 128 || first.NextFrom == nil || *first.NextFrom != 128 {
		t.Fatal(first)
	}
	last := a.relationshipReadinessPage(context.Background(), p, 128)
	if len(last.Inputs) != 2 || last.Inputs[0].Index != 128 || last.Inputs[1].Index != 129 || last.NextFrom != nil {
		t.Fatal(last)
	}
	tx := b.Transactions[0]
	tx.Inputs = p.Inputs
	b.Transactions[0] = tx
	a.rememberRelationshipTransaction(b, tx)
	relationshipProofs.Lock()
	saved := relationshipProofs.rows[a.relationshipProofKey(b.Hash, 0)]
	relationshipProofs.Unlock()
	if len(saved.Inputs) != 130 {
		t.Fatal("later availability pages lost verified prevout metadata", len(saved.Inputs))
	}
}

func Test078ReadinessNeverReconstructsLocatorSidecars(t *testing.T) {
	a, b := initGraphTestApp(t)
	a.settings.CoreDisabled = true
	for _, id := range []string{"tx-locator", "txo-spender"} {
		s, e := openIndexStore(a.dataDir, id)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.appendBlock(b, 0, "ephemeral"); e != nil {
			t.Fatal(e)
		}
		path := s.locatorActivePath()
		for _, p := range []string{path, path + ".receipt.json"} {
			if e = os.Remove(p); e != nil {
				t.Fatal(e)
			}
		}
		ctx := context.WithValue(context.Background(), indexLocatorReadOnlyKey{}, true)
		if e = s.ensureIndexLocators(ctx); e == nil {
			t.Fatal("automatic lookup accepted missing acceleration")
		}
		proof := relationshipTxProof{Height: 0, Hash: b.Hash, TxID: b.Transactions[0].TxID, Index: 0, Outputs: 1, Inputs: []inputView{{N: 0, PrevTxID: b.Transactions[0].TxID}}}
		a.relationshipReadinessFor(ctx, proof)
		for _, p := range []string{path, path + ".receipt.json", path + ".repair"} {
			if _, e = os.Stat(p); !os.IsNotExist(e) {
				t.Fatal("readiness reconstructed sidecar", p, e)
			}
		}
	}
}
