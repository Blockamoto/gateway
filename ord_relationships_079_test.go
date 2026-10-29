package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestOrdCoreAuthorityDoesNotFallback079(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":null,"error":{"code":-8,"message":"Block height out of range"},"id":"gateway"}`))
	}))
	defer server.Close()
	authority := &ordAuthority{mode: "core", core: &coreRPC{url: server.URL, http: server.Client()}}
	ctx := context.WithValue(context.Background(), ordAuthorityKey{}, authority)
	if _, err := (&app{}).ordCanonicalHash(ctx, 100); err == nil || authority.mode != "core" {
		t.Fatalf("missing Core height must remain unresolved: %v", err)
	}
}

func TestOrdKnownChildrenBoundedRevalidation079(t *testing.T) {
	rows := []ordRelationship{{ChildID: "one", State: "pending"}, {ChildID: "two", State: "pending"}, {ChildID: "three", State: "pending"}}
	calls := 0
	got := revalidateKnownOrdChildren(context.Background(), rows, 2, func(_ context.Context, r ordRelationship) (ordRelationship, bool) {
		calls++
		if r.ChildID == "two" {
			return r, false
		}
		r.State = "verified"
		return r, true
	})
	if calls != 2 || len(got) != 2 || got[0].State != "verified" || got[1].ChildID != "three" || got[1].State != "pending" {
		t.Fatalf("bounded recheck: calls %d rows %+v", calls, got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls = 0
	got = revalidateKnownOrdChildren(ctx, rows, 2, func(_ context.Context, r ordRelationship) (ordRelationship, bool) { calls++; return r, true })
	if calls != 0 || len(got) != 3 {
		t.Fatalf("canceled recheck: calls %d rows %+v", calls, got)
	}
}

func TestOrdReparseIgnoresCachedIdentity079(t *testing.T) {
	parent := make([]byte, 32)
	parent[0] = 1
	tx := inscriptionTestTx(inscriptionTestScript([]byte{3}, parent, nil, []byte("child")))
	block := inscriptionTestBlock(tx)
	// A saved indexed record's provider and metadata are not proof; extraction
	// finds the inscription by ID and derives its transaction and fields anew.
	cached := ordRecord{ID: tx.TxID + "i0", TxID: testID(999), Provider: "local_occurrence_index"}
	fresh, err := (&app{}).authenticatedOrdFromBlock(cached.ID, block)
	if err != nil || fresh.TxID != tx.TxID || len(fresh.Envelope.Parents) != 1 || fresh.Envelope.Parents[0] != decodeInscriptionReference(parent) {
		t.Fatalf("authenticated reparse: %+v %v", fresh, err)
	}
}

func TestOrdParentReferenceEncoding079(t *testing.T) {
	// Published Ord handbook provenance example, independent of our serializer.
	example, _ := hex.DecodeString("1f1e1d1c1b1a191817161514131211100f0e0d0c0b0a09080706050403020100")
	if got := decodeInscriptionReference(example); got != "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1fi0" {
		t.Fatalf("Ord handbook reference: %s", got)
	}
	raw := make([]byte, 32)
	raw[0] = 1
	a := decodeInscriptionReference(raw)
	if a != strings.Repeat("0", 62)+"01i0" {
		t.Fatalf("id %s", a)
	}
	if decodeInscriptionReference(append(append([]byte{}, raw...), 0)) != "" {
		t.Fatal("non-minimal zero index accepted")
	}
	if decodeInscriptionReference(append(append([]byte{}, raw...), 0, 1)) != strings.Repeat("0", 62)+"01i256" {
		t.Fatal("little endian index")
	}
	if decodeInscriptionReference(append(append([]byte{}, raw...), 1, 0, 0, 0)) != strings.Repeat("0", 62)+"01i1" {
		t.Fatal("Ord accepts a four-byte index with trailing zeroes")
	}
	e := parseOrdFields([][]byte{{3}, raw, {3}, raw, {3}, {1, 2, 3}})
	if len(e.Parents) != 2 || len(e.Fields["03"]) != 3 || !e.Duplicate {
		t.Fatalf("raw declarations not retained: %+v", e)
	}
	if _, err := hex.DecodeString(e.Fields["03"][2]); err != nil {
		t.Fatal(err)
	}
}

func TestOrdParentMovement079(t *testing.T) {
	origin := testID(110)
	extra := testID(111)
	moved := testID(112)
	childID := testID(113)
	b0 := testBlock(100, testCoinbase(origin, 100), testTx(extra, nil, 50))
	b1 := testBlock(101, testCoinbase(testID(114), subsidyAtHeight(101)), testTx(moved, []inputView{{PrevTxID: extra, PrevVout: 0}, {PrevTxID: origin, PrevVout: 0}}, 70, 80))
	child := testTx(childID, []inputView{{PrevTxID: moved, PrevVout: 1}}, 80)
	b2 := testBlock(102, testCoinbase(testID(115), subsidyAtHeight(102)), child)
	backend := &memorySatlineBackend{blocks: map[int64]blockView{100: b0, 101: b1, 102: b2}, tip: 102}
	start := satlinePoint{TxID: origin, Vout: 0, Offset: 40, Height: 100, BlockHash: b0.Hash, TxIndex: 0}
	state, reason, vin, point := ordParentAtReveal(newSatlineResolver(backend), start, b2.Transactions[1], 102, 1, selectedOrdTestChain(backend))
	if state != "verified" || vin != 0 || point.TxID != moved || point.Vout != 1 || point.Offset != 20 {
		t.Fatalf("%s %s %d %+v", state, reason, vin, point)
	}
	unrelated := testTx(testID(116), []inputView{{PrevTxID: extra, PrevVout: 0}}, 50)
	state, _, _, _ = ordParentAtReveal(newSatlineResolver(backend), start, unrelated, 102, 1, selectedOrdTestChain(backend))
	if state != "rejected" {
		t.Fatalf("expected rejected, got %s", state)
	}
	missing := &unresolvedSatlineBackend{memorySatlineBackend: backend}
	state, _, _, _ = ordParentAtReveal(newSatlineResolver(missing), start, child, 102, 1, selectedOrdTestChain(backend))
	if state != "pending" {
		t.Fatalf("expected pending, got %s", state)
	}
	wrongHop := func(h int64) (string, error) {
		if h == 101 {
			return testID(999), nil
		}
		return selectedOrdTestChain(backend)(h)
	}
	state, _, _, _ = ordParentAtReveal(newSatlineResolver(backend), start, child, 102, 1, wrongHop)
	if state != "pending" {
		t.Fatalf("orphan hop must remain pending, got %s", state)
	}
	missingAuthority := func(int64) (string, error) { return "", fmt.Errorf("unavailable") }
	state, _, _, _ = ordParentAtReveal(newSatlineResolver(backend), start, child, 102, 1, missingAuthority)
	if state != "pending" {
		t.Fatalf("missing authority must remain pending, got %s", state)
	}
}

func TestOrdParentHistoricalDuplicateOutpoint079(t *testing.T) {
	point := satlinePoint{TxID: testID(711), Vout: 0, Offset: 0, Height: 91722, BlockHash: testID(712), TxIndex: 0}
	child := testTx(testID(713), []inputView{{PrevTxID: point.TxID, PrevVout: point.Vout}}, 1)
	selected := func(int64) (string, error) { return point.BlockHash, nil }
	state, _, _, _ := ordParentAtReveal(newSatlineResolver(&memorySatlineBackend{}), point, child, 91880, 1, selected)
	if state != "pending" {
		t.Fatalf("duplicate outpoint after replacement must be unresolved: %s", state)
	}
	state, _, _, _ = ordParentAtReveal(newSatlineResolver(&memorySatlineBackend{}), point, child, 91879, 1, selected)
	if state != "verified" {
		t.Fatalf("pre-replacement spend should remain verifiable: %s", state)
	}
}

func TestOrdParentFeeMovement079(t *testing.T) {
	origin, coinbase, moved, childID := testID(210), testID(211), testID(212), testID(213)
	b0 := testBlock(100, testCoinbase(origin, 100))
	b1 := testBlock(101, testCoinbase(coinbase, subsidyAtHeight(101)+10), testTx(moved, []inputView{{PrevTxID: origin, PrevVout: 0}}, 90))
	child := testTx(childID, []inputView{{PrevTxID: coinbase, PrevVout: 0}}, subsidyAtHeight(101)+10)
	b2 := testBlock(102, testCoinbase(testID(214), subsidyAtHeight(102)), child)
	backend := &memorySatlineBackend{blocks: map[int64]blockView{100: b0, 101: b1, 102: b2}, tip: 102}
	start := satlinePoint{TxID: origin, Vout: 0, Offset: 95, Height: 100, BlockHash: b0.Hash, TxIndex: 0}
	state, reason, _, point := ordParentAtReveal(newSatlineResolver(backend), start, b2.Transactions[1], 102, 1, selectedOrdTestChain(backend))
	if state != "verified" || point.TxID != coinbase || point.Offset != subsidyAtHeight(101)+5 {
		t.Fatalf("%s %s %+v", state, reason, point)
	}
}

func TestOrdKnownChildrenRestartAndReorg079(t *testing.T) {
	dir := t.TempDir()
	parent := testID(220) + "i0"
	child := testID(221) + "i0"
	block := testID(222)
	rows := []ordRelationship{{ParentID: parent, ChildID: child, State: "verified", ChildHeight: 42, ChildBlockHash: block, Reference: ordParentReference}, {ParentID: parent, ChildID: child, State: "verified", ChildHeight: 42, ChildBlockHash: block, Reference: ordParentReference}}
	if err := atomicWriteJSON(filepath.Join(dir, "child.json"), rows); err != nil {
		t.Fatal(err)
	}
	selected := func(int64) (string, error) { return block, nil }
	if got := loadOrdChildDeclarations(filepath.Join(dir, "child.json"), parent, selected); len(got) != 1 || got[0].State != "pending" {
		t.Fatalf("restart/dedup: %+v", got)
	}
	reorg := func(int64) (string, error) { return testID(223), nil }
	if got := loadOrdChildDeclarations(filepath.Join(dir, "child.json"), parent, reorg); len(got) != 0 {
		t.Fatalf("orphan retained: %+v", got)
	}
	unavailable := func(int64) (string, error) { return "", fmt.Errorf("chain unavailable") }
	if got := loadOrdChildDeclarations(filepath.Join(dir, "child.json"), parent, unavailable); len(got) != 0 {
		t.Fatalf("unknown chain retained: %+v", got)
	}
}

func selectedOrdTestChain(b *memorySatlineBackend) func(int64) (string, error) {
	return func(h int64) (string, error) {
		v, ok := b.blocks[h]
		if !ok {
			return "", fmt.Errorf("missing selected header")
		}
		return v.Hash, nil
	}
}
