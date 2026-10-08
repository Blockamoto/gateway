package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Synthetic committed receipts isolate lookup identity and selection; they do
// not claim to revalidate the real historical Bitcoin blocks in this test.
func contextualTransactionFixture(t *testing.T, from, to int64, counts map[string]int, duplicates map[int64]bool) (*app, string, map[int64]string) {
	t.Helper()
	root := t.TempDir()
	id := testID(990001)
	headers := make([]byte, (to+1)*80)
	blocks := make([]blockView, 0, to-from+1)
	hashes := map[int64]string{}
	previous := ""
	for h := from; h <= to; h++ {
		header := headers[h*80 : (h+1)*80]
		header[0], header[1], header[2] = byte(h), byte(h>>8), byte(h>>16)
		hash := hash256(header)
		txid := testID(uint64(h) + 1000000)
		if duplicates[h] {
			txid = id
		}
		block := satIndexTestBlock(h, previous, testCoinbase(txid, 100))
		block.Hash = reverseHex(hash[:])
		blocks = append(blocks, block)
		hashes[h] = block.Hash
		previous = block.Hash
	}
	for index, count := range counts {
		store, err := openIndexStore(root, index)
		if err != nil {
			t.Fatal(err)
		}
		for _, block := range blocks[:count] {
			if err = store.appendBlock(block, from, "ephemeral"); err != nil {
				t.Fatal(err)
			}
		}
	}
	path := filepath.Join(root, "headers.bin")
	if err := os.WriteFile(path, headers, 0600); err != nil {
		t.Fatal(err)
	}
	a := &app{dataDir: root, headersPath: path, settings: appSettings{CoreDisabled: true, CoreMountDisabled: true, NetworkDisabled: true}, status: appStatus{HeaderCount: to + 1, HeaderHeight: to}, cacheIndex: newCacheIndex()}
	return a, id, hashes
}

func Test065ContextualBIP30LookupPreservesPublicAmbiguity(t *testing.T) {
	const first, replacement = int64(91812), int64(91842)
	a, id, hashes := contextualTransactionFixture(t, first, replacement, map[string]int{"blocks": int(replacement - first + 1)}, map[int64]bool{first: true, replacement: true})
	if _, found, err := a.indexedTransaction(id); !found || err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatal("public txid-only lookup chose an incarnation", found, err)
	}
	for _, tc := range []struct{ before, want int64 }{{first, first}, {replacement - 1, first}, {replacement, replacement}, {replacement + 10, replacement}} {
		r, found, err := a.indexedTransactionBeforeContext(context.Background(), strings.ToUpper(id), tc.before)
		if err != nil || !found || !r.TransactionVerified || r.Height != tc.want || r.BlockHash != hashes[tc.want] || r.Transaction.TxID != id {
			t.Fatalf("before %d: %+v found=%v err=%v", tc.before, r, found, err)
		}
		// The production Satline provider must use the same contextual route,
		// with both raw-block storage and all network providers absent.
		evidence, err := (contextSatlineBackend{appSatlineBackend{a}, context.Background()}).Transaction(id, tc.before)
		if err != nil || evidence.Height != tc.want || evidence.BlockHash != hashes[tc.want] || evidence.Source != "local_index" {
			t.Fatalf("provider before %d: %+v err=%v", tc.before, evidence, err)
		}
	}
	if _, found, err := a.indexedTransactionBeforeContext(context.Background(), id, first-1); err != nil || found {
		t.Fatal("future incarnation escaped consuming height", found, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (appSatlineBackend{a}).transactionContext(ctx, id, replacement); !errors.Is(err, context.Canceled) {
		t.Fatal("contextual provider ignored cancellation", err)
	}
}

func Test065ContextualLookupDoesNotAttachOlderDecodedIncarnation(t *testing.T) {
	t.Skip("This cross-index resolution requires the standalone transaction locator, intentionally locked in 0.6.6. Blocks-only BIP30 tests remain active.")
	a, id, hashes := contextualTransactionFixture(t, 0, 1, map[string]int{"blocks": 1, "tx-locator": 2}, map[int64]bool{0: true, 1: true})
	r, found, err := a.indexedTransactionBeforeContext(context.Background(), id, 1)
	if err != nil || !found || r.Height != 1 || r.BlockHash != hashes[1] || r.TransactionVerified || r.Transaction.TxID != "" || r.ResolutionState != "located_bytes_unavailable" {
		t.Fatal("new locator inherited old decoded evidence", r, found, err)
	}
	r, found, err = a.indexedTransactionBeforeContext(context.Background(), id, 0)
	if err != nil || !found || !r.TransactionVerified || r.Height != 0 || r.BlockHash != hashes[0] {
		t.Fatal("older bounded lookup lost its own retained bytes", r, found, err)
	}
}

func Test065ContextualPartialIndexCannotResurrectDisplacedCoinbase(t *testing.T) {
	const first, replacement = int64(91812), int64(91842)
	a, id, _ := contextualTransactionFixture(t, first, first, map[string]int{"blocks": 1}, map[int64]bool{first: true})
	if _, found, err := a.indexedTransactionBeforeContext(context.Background(), id, replacement); err != nil || found {
		t.Fatal("partial coverage invented the pre-BIP30 incarnation after replacement", found, err)
	}
	if _, found, err := a.indexedTransactionBeforeContext(context.Background(), id, replacement-1); err != nil || !found {
		t.Fatal("valid pre-replacement context lost", found, err)
	}
	if evidence, err := (contextSatlineBackend{appSatlineBackend{a}, context.Background()}).Transaction(id, replacement); err == nil {
		t.Fatal("provider fallback resurrected displaced incarnation", evidence)
	}
}
