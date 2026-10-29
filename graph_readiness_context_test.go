package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func Test078GraphReadinessReadersCancelLockAndScan(t *testing.T) {
	a, genesis := initGraphTestApp(t)
	a.settings.CoreDisabled = true
	if err := a.graphIndexBlock(genesis, true); err != nil {
		t.Fatal(err)
	}
	hash := appendGraphTestHeader(t, a, genesis.Hash, 82)
	parent := genesis.Transactions[0].TxID
	child := blockView{Height: 1, Hash: hash, CacheVisibility: "public", VerificationState: "header_anchored", Verification: genesis.Verification,
		Transactions: []transactionView{{Index: 0, TxID: testID(78003), Inputs: []inputView{{N: 0, PrevTxID: parent}}, Outputs: []outputView{{N: 0, ValueSats: 1}}}}}
	if err := a.graphIndexBlock(child, true); err != nil {
		t.Fatal(err)
	}
	if rows, err := a.graphFindTxContext(context.Background(), parent); err != nil || len(rows) != 1 || rows[0].BlockHash != genesis.Hash {
		t.Fatal(rows, err)
	}
	if row, found, err := a.graphReadSpendContext(context.Background(), parent, 0); err != nil || !found || row.BlockHash != hash {
		t.Fatal(row, found, err)
	}
	readers := []struct {
		name string
		read func(context.Context) error
	}{
		{"transaction", func(ctx context.Context) error { _, err := a.graphFindTxContext(ctx, parent); return err }},
		{"spend", func(ctx context.Context) error { _, _, err := a.graphReadSpendContext(ctx, parent, 0); return err }},
	}
	for _, reader := range readers {
		t.Run(reader.name+"_lock", func(t *testing.T) {
			a.graphStoreMu.Lock()
			defer a.graphStoreMu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			if err := reader.read(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("graph lock wait ignored cancellation", err)
			}
		})
	}
	// Cancellation after an anchored match must discard the partial result,
	// rather than return positive readiness before finishing the shard scan.
	ctx, cancel := context.WithCancel(context.Background())
	rows, err := a.graphFindTxWithAnchor(ctx, parent, func(int64) (string, error) { cancel(); return genesis.Hash, nil })
	if !errors.Is(err, context.Canceled) || len(rows) != 0 {
		t.Fatal("transaction scan returned a partial result", rows, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	row, found, err := a.graphReadSpendWithAnchor(ctx, parent, 0, func(int64) (string, error) { cancel(); return hash, nil })
	if !errors.Is(err, context.Canceled) || found {
		t.Fatal("spend scan returned a partial result", row, found, err)
	}
}

func Test078RelationshipReadinessGraphCoreAnchorUsesRequestContext(t *testing.T) {
	a, genesis := initGraphTestApp(t)
	a.settings.CoreDisabled = true
	if err := a.graphIndexBlock(genesis, true); err != nil {
		t.Fatal(err)
	}
	methods := make(chan string, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		methods <- q.Method
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer server.Close()
	a.settings = testRPCSettings050(t, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	got := a.relationshipReadinessFor(ctx, relationshipTxProof{Height: 1, Hash: testID(78004), Inputs: []inputView{{N: 0, PrevTxID: genesis.Transactions[0].TxID}}})
	if len(got.Inputs) != 1 || got.Inputs[0].Available || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal(got, ctx.Err())
	}
	select {
	case method := <-methods:
		if method != "getblockhash" {
			t.Fatal("readiness entered unbounded legacy Core inspection", method)
		}
	default:
		t.Fatal("graph selected-chain anchor was not checked")
	}
}
