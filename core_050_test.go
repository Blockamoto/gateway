package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testRPCSettings050(t *testing.T, u string) appSettings {
	t.Helper()
	x, e := url.Parse(u)
	if e != nil {
		t.Fatal(e)
	}
	port, e := strconv.Atoi(x.Port())
	if e != nil {
		t.Fatal(e)
	}
	return appSettings{BitcoinDataDir: t.TempDir(), RPCPort: port, RPCAuthMode: "userpass", RPCUser: "test", RPCPassword: "test"}
}
func Test050CoreRPCContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer srv.Close()
	c, e := newCoreRPC(testRPCSettings050(t, srv.URL))
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	var out any
	e = c.callContext(ctx, "getblock", nil, &out)
	if e == nil || time.Since(start) > time.Second {
		t.Fatalf("cancellation not propagated %v", e)
	}
}
func Test050ExactBitcoinAmounts(t *testing.T) {
	yes := map[string]uint64{"0": 0, "0.00000001": 1, "1.23456789": 123456789, "21000000": 2100000000000000, "1e-8": 1}
	for s, n := range yes {
		v, e := exactBTCSats(json.RawMessage(s))
		if e != nil || v != n {
			t.Fatalf("%s -> %d %v", s, v, e)
		}
	}
	for _, s := range []string{"-1", "0.000000001", "21000000.00000001", "null", "\"1\""} {
		if _, e := exactBTCSats(json.RawMessage(s)); e == nil {
			t.Fatal("invalid amount", s)
		}
	}
}
func Test050CoreUndoValuesUseOneTransactionQuery(t *testing.T) {
	id := strings.Repeat("aa", 32)
	parent := strings.Repeat("bb", 32)
	hash := strings.Repeat("cc", 32)
	var txCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&q)
		var result any
		switch q.Method {
		case "getrawtransaction":
			atomic.AddInt32(&txCalls, 1)
			result = map[string]any{"txid": id, "blockhash": hash, "vin": []any{map[string]any{"txid": parent, "vout": 0, "prevout": map[string]any{"value": json.Number("0.00001234")}}, map[string]any{"txid": parent, "vout": 1, "prevout": map[string]any{"value": json.Number("0.00005678")}}}}
		case "getblockhash":
			result = hash
		default:
			t.Errorf("unexpected method %s", q.Method)
		}
		json.NewEncoder(w).Encode(map[string]any{"id": q.ID, "result": result, "error": nil})
	}))
	defer srv.Close()
	a := &app{settings: testRPCSettings050(t, srv.URL)}
	b := contextSatlineBackend{appSatlineBackend{a}, context.Background()}
	tx := transactionView{TxID: id, Inputs: []inputView{{PrevTxID: parent, PrevVout: 0}, {PrevTxID: parent, PrevVout: 1}}}
	vals, e := b.InputValues(tx, 800000, hash)
	if e != nil || vals[parent+":0"] != 1234 || vals[parent+":1"] != 5678 || atomic.LoadInt32(&txCalls) != 1 {
		t.Fatalf("batch %v %v calls=%d", vals, e, txCalls)
	}
}
func Test050CoreFeePrefixChecksOrder(t *testing.T) {
	hash := strings.Repeat("cc", 32)
	ids := []string{strings.Repeat("01", 32), strings.Repeat("02", 32), strings.Repeat("03", 32)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q map[string]any
		json.NewDecoder(r.Body).Decode(&q)
		json.NewEncoder(w).Encode(map[string]any{"id": q["id"], "error": nil, "result": map[string]any{"hash": hash, "confirmations": 1, "tx": []any{map[string]any{"txid": ids[0]}, map[string]any{"txid": ids[1], "fee": json.Number("0.00001000")}, map[string]any{"txid": ids[2], "fee": json.Number("0.00002000")}}}})
	}))
	defer srv.Close()
	a := &app{settings: testRPCSettings050(t, srv.URL)}
	b := contextSatlineBackend{appSatlineBackend{a}, context.Background()}
	v := blockView{Hash: hash, Transactions: []transactionView{{TxID: ids[0]}, {TxID: ids[1]}, {TxID: ids[2]}}}
	p, e := b.FeePrefix(v)
	if e != nil || len(p) != 4 || p[0] != 0 || p[2] != 1000 || p[3] != 3000 {
		t.Fatalf("prefix %v %v", p, e)
	}
	v.Transactions[1].TxID = ids[2]
	if _, e = b.FeePrefix(v); e == nil {
		t.Fatal("wrong block transaction order accepted")
	}
}
