package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func addressHistoryTestBlock(height int64, hash string, outputs ...outputView) blockView {
	for i := range outputs {
		outputs[i].N = i
	}
	return blockView{Height: height, Hash: hash, Transactions: []transactionView{{Index: 0, Coinbase: true, TxID: strings.Repeat("a", 64), Outputs: outputs}}, Verification: verificationView{VerifierVersion: blockVerifierVersion, HeaderHash: true, HeaderChainMatch: true, ProofOfWork: true, MerkleRoot: true, TransactionsParsed: true}}
}
func addressHistoryTestScript(tag byte) (string, string) {
	script := append([]byte{0x00, 0x14}, make([]byte, 20)...)
	script[len(script)-1] = tag
	_, address := describeScript(script)
	return hex.EncodeToString(script), address
}
func addressHistoryTestHashAt(hashes map[int64]string) func(int64) (string, error) {
	return func(height int64) (string, error) {
		hash, ok := hashes[height]
		if !ok {
			return "", fmt.Errorf("header unavailable")
		}
		return hash, nil
	}
}

func Test076AddressHistorySparsePaginationRestartAndBoundedQuery(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, e := openAddressHistoryStoreContext(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	script, address := addressHistoryTestScript(1)
	outputs := make([]outputView, 140)
	for i := range outputs {
		outputs[i] = outputView{ValueSats: uint64(i + 1), ScriptPubKey: script}
	}
	hashes := map[int64]string{10: strings.Repeat("1", 64), 20: strings.Repeat("2", 64)}
	hashAt := addressHistoryTestHashAt(hashes)
	if e = s.CommitBlockContext(ctx, addressHistoryTestBlock(10, hashes[10], outputs...), hashAt); e != nil {
		t.Fatal(e)
	}
	if e = s.CommitBlockContext(ctx, addressHistoryTestBlock(20, hashes[20], outputView{ValueSats: 10, ScriptPubKey: "6a"}, outputView{ValueSats: 20, ScriptPubKey: "21" + strings.Repeat("11", 33) + "ac"}, outputView{ScriptPubKey: "51"}), hashAt); e != nil {
		t.Fatal(e)
	}
	if len(s.head.Coverage) != 2 || s.head.Coverage[1] != (heightInterval{20, 20}) {
		t.Fatal("empty-address block lost scanned coverage", s.head)
	}
	cold, e := openAddressHistoryStoreContext(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	counted := func(height int64) (string, error) { calls++; return hashAt(height) }
	first, e := cold.QueryContext(ctx, strings.ToUpper(address), "", 25, counted)
	if e != nil || len(first.Outputs) != 25 || first.Outputs[0].Output != "139.0.10" || !first.HasMore || first.NextCursor == "" || first.ReceivedOutputCount != 140 || first.ReceivedSats != 9870 || first.Complete || first.Snapshot.ConsensusValidated {
		t.Fatal(first, e)
	}
	if calls != 3 {
		t.Fatal("address page did not bound/memoize selected-height checks", calls)
	}
	calls = 0
	if e = cold.ReconcileContext(ctx, counted); e != nil || calls != 1 {
		t.Fatal("unchanged selected ancestry rescanned all sparse history", calls, e)
	}
	if first.Address != address || first.Outputs[0].SpentStatus != "unknown" || len(first.Gaps) != 1 || first.Gaps[0] != (heightInterval{11, 19}) {
		t.Fatal(first)
	}
	all := append([]addressHistoryOutput(nil), first.Outputs...)
	cursor := first.NextCursor
	for cursor != "" {
		page, e := cold.QueryContext(ctx, address, cursor, 25, hashAt)
		if e != nil {
			t.Fatal(e)
		}
		all = append(all, page.Outputs...)
		cursor = page.NextCursor
	}
	if len(all) != 140 || all[len(all)-1].Output != "0.0.10" {
		t.Fatal("pagination skipped/duplicated outputs", len(all))
	}
	_, other := addressHistoryTestScript(2)
	if _, e = cold.QueryContext(ctx, other, first.NextCursor, 25, hashAt); e == nil {
		t.Fatal("cross-address cursor admitted")
	}
	if e = s.CommitBlockContext(ctx, addressHistoryTestBlock(20, hashes[20], outputView{ScriptPubKey: "6a"}), hashAt); e != nil {
		t.Fatal(e)
	}
	changed, e := openAddressHistoryStoreContext(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = changed.QueryContext(ctx, address, first.NextCursor, 25, hashAt); e == nil {
		t.Fatal("cursor crossed atomic revision")
	}
	if e = cold.CommitBlockContext(ctx, addressHistoryTestBlock(20, hashes[20]), hashAt); e == nil {
		t.Fatal("stale writer overwrote a checkpoint")
	}
	var bucket addressHistoryBucket
	have, e := changed.tree("addresses", changed.head.AddressRoot, ctx).get(addressHistoryAddressKey(address), &bucket)
	if e != nil || !have {
		t.Fatal(e)
	}
	if e = changed.tree("rows", bucket.RowsRoot, ctx).walk(func(kv ordinalKV) error {
		if strings.Contains(string(kv.Value), "txid") || strings.Contains(string(kv.Value), address) {
			return errors.New("history row repeats native transaction/address identity")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

func Test076AddressHistoryCancellationUnavailableAndReorgRetention(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, e := openAddressHistoryStoreContext(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	script, address := addressHistoryTestScript(1)
	hashes := map[int64]string{10: strings.Repeat("1", 64)}
	hashAt := addressHistoryTestHashAt(hashes)
	b := addressHistoryTestBlock(10, hashes[10], outputView{ValueSats: 123, ScriptPubKey: script})
	if e = s.CommitBlockContext(ctx, b, hashAt); e != nil {
		t.Fatal(e)
	}
	initial := s.head.Commitment
	cancelled, cancel := context.WithCancel(ctx)
	calls := 0
	cancelAtBoundary := func(height int64) (string, error) {
		calls++
		if calls == 2 {
			cancel()
		}
		return hashAt(height)
	}
	if e = s.CommitBlockContext(cancelled, b, cancelAtBoundary); !errors.Is(e, context.Canceled) {
		t.Fatal("publication cancellation ignored", e)
	}
	cold, e := openAddressHistoryStoreContext(ctx, root)
	if e != nil || cold.head.Commitment != initial {
		t.Fatal("cancelled staging became visible", cold, e)
	}
	if e = s.ReconcileContext(ctx, func(int64) (string, error) { return "", errors.New("offline header authority") }); e == nil || s.head.Commitment != initial {
		t.Fatal("missing headers erased committed history", e)
	}
	hashes[10] = strings.Repeat("2", 64)
	if _, e = s.QueryContext(ctx, address, "", 10, hashAt); e == nil {
		t.Fatal("old branch was reinterpreted at the same height")
	}
	if e = s.ReconcileContext(ctx, hashAt); e != nil {
		t.Fatal(e)
	}
	if len(s.head.Coverage) != 0 || s.head.Height != -1 {
		t.Fatal("stale scanned coverage advertised", s.head)
	}
	script2, address2 := addressHistoryTestScript(2)
	if e = s.CommitBlockContext(ctx, addressHistoryTestBlock(10, hashes[10], outputView{ValueSats: 99, ScriptPubKey: script2}), hashAt); e != nil {
		t.Fatal(e)
	}
	old, e := s.QueryContext(ctx, address, "", 10, hashAt)
	if e != nil || len(old.Outputs) != 0 || old.ReceivedOutputCount != 0 {
		t.Fatal(old, e)
	}
	newer, e := s.QueryContext(ctx, address2, "", 10, hashAt)
	if e != nil || len(newer.Outputs) != 1 || newer.ReceivedSats != 99 {
		t.Fatal(newer, e)
	}
	pins := map[string]bool{}
	if e = s.VisitFactPagesContext(ctx, func(hash string) error { pins[hash] = true; return nil }); e != nil {
		t.Fatal(e)
	}
	if len(pins) != 2 {
		t.Fatal("retained old branch lost its independent fact owner", pins)
	}
	aged := time.Now().Add(-2 * time.Hour)
	for hash := range pins {
		path, _ := outputFactPath(root, hash)
		if e = os.Chtimes(path, aged, aged); e != nil {
			t.Fatal(e)
		}
	}
	orphan := addressHistoryTestBlock(99, strings.Repeat("9", 64), outputView{ValueSats: 1, ScriptPubKey: script})
	refs, e := storeBlockOutputFactsContext(ctx, root, orphan)
	if e != nil {
		t.Fatal(e)
	}
	orphanPath, _ := outputFactPath(root, refs["0.0.99"].Page)
	if e = os.Chtimes(orphanPath, aged, aged); e != nil {
		t.Fatal(e)
	}
	if e = collectSharedOutputFactsContext(ctx, root); e != nil {
		t.Fatal(e)
	}
	for hash := range pins {
		path, _ := outputFactPath(root, hash)
		if _, e = os.Stat(path); e != nil {
			t.Fatal("history-only or retained branch fact collected", e)
		}
	}
	if _, e = os.Stat(orphanPath); !os.IsNotExist(e) {
		t.Fatal("unowned old page not reclaimed", e)
	}
}

func Test076AddressHistoryGCFailClosedOnCorruptOwnerAndFact(t *testing.T) {
	for _, damage := range []string{"rows", "fact", "commit"} {
		t.Run(damage, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			s, e := openAddressHistoryStoreContext(ctx, root)
			if e != nil {
				t.Fatal(e)
			}
			script, address := addressHistoryTestScript(3)
			hashes := map[int64]string{4: strings.Repeat("4", 64)}
			if e = s.CommitBlockContext(ctx, addressHistoryTestBlock(4, hashes[4], outputView{ValueSats: 7, ScriptPubKey: script}), addressHistoryTestHashAt(hashes)); e != nil {
				t.Fatal(e)
			}
			refs, e := storeBlockOutputFactsContext(ctx, root, addressHistoryTestBlock(9, strings.Repeat("9", 64), outputView{ValueSats: 8, ScriptPubKey: script}))
			if e != nil {
				t.Fatal(e)
			}
			orphan, _ := outputFactPath(root, refs["0.0.9"].Page)
			aged := time.Now().Add(-2 * time.Hour)
			if e = os.Chtimes(orphan, aged, aged); e != nil {
				t.Fatal(e)
			}
			var path string
			switch damage {
			case "commit":
				path = filepath.Join(s.dir, "commits", s.head.Commitment[:2], s.head.Commitment+".json")
			case "fact":
				path, _ = outputFactPath(root, s.head.PinnedPages[0])
			case "rows":
				var bucket addressHistoryBucket
				if _, e = s.tree("addresses", s.head.AddressRoot, ctx).get(addressHistoryAddressKey(address), &bucket); e != nil {
					t.Fatal(e)
				}
				path = s.tree("rows", bucket.RowsRoot, ctx).pagePath(bucket.RowsRoot)
			}
			if e = os.WriteFile(path, []byte("corrupt"), 0600); e != nil {
				t.Fatal(e)
			}
			if e = collectSharedOutputFactsContext(ctx, root); e == nil {
				t.Fatal("damaged address owner permitted shared sweep")
			}
			if _, e = os.Stat(orphan); e != nil {
				t.Fatal("fail-closed collection deleted unrelated orphan", e)
			}
			if _, e = s.QueryContext(ctx, address, "", 10, addressHistoryTestHashAt(hashes)); damage != "commit" && e == nil {
				t.Fatal("address query accepted damaged output evidence")
			}
		})
	}
}

func Test076AddressHistoryReuseCompleteTXIOFactsWithoutRaw(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	ctx := context.Background()
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	txio, e := openIndexStore(a.dataDir, "txo-spender")
	if e != nil {
		t.Fatal(e)
	}
	script, address := addressHistoryTestScript(4)
	b := txioTestBlock(t, a, 1, genesisHashDisplay, 1, transactionView{TxID: strings.Repeat("b", 64), Outputs: []outputView{{N: 0, ValueSats: 456, ScriptPubKey: script}}})
	txio.retainOutputValues, txio.retainOutputScripts = true, true
	if e = txio.appendBlock(b, 1, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	batch, e := txio.readBatch(txio.head.Commitment)
	if e != nil {
		t.Fatal(e)
	}
	if !batch.Spenders.Values || !batch.Spenders.Scripts {
		t.Fatal("fixture missing retained TXIO layers")
	}
	s, e := openAddressHistoryStoreContext(ctx, a.dataDir)
	if e != nil {
		t.Fatal(e)
	}
	used, e := s.CommitTXIOFactsContext(ctx, 1, b.Hash, func(int64) (string, error) { return b.Hash, nil })
	if e != nil || !used {
		t.Fatal("complete TXIO reuse failed", used, e)
	}
	got, e := s.QueryContext(ctx, address, "", 10, func(int64) (string, error) { return b.Hash, nil })
	if e != nil || len(got.Outputs) != 1 || got.Outputs[0].ValueSats != 456 {
		t.Fatal(got, e)
	}
	var bucket addressHistoryBucket
	if _, e = s.tree("addresses", s.head.AddressRoot, ctx).get(addressHistoryAddressKey(address), &bucket); e != nil {
		t.Fatal(e)
	}
	var row addressHistoryRow
	key, _ := addressHistoryPositionKey("0.1.1")
	if _, e = s.tree("rows", bucket.RowsRoot, ctx).get(key, &row); e != nil {
		t.Fatal(e)
	}
	if row.Fact != batch.Spenders.OutputFacts["0.1.1"] {
		t.Fatal("history duplicated complete TXIO facts")
	}
	encoded, _ := json.Marshal(row)
	if strings.Contains(string(encoded), strings.Repeat("b", 64)) {
		t.Fatal("transaction hash repeated in history row")
	}
}

func Test076AddressHistoryIncompleteFactsAndChangingChainNeverPublish(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, e := openAddressHistoryStoreContext(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	script, _ := addressHistoryTestScript(5)
	b := addressHistoryTestBlock(5, strings.Repeat("5", 64), outputView{ValueSats: 10, ScriptPubKey: script}, outputView{ValueSats: 20, ScriptPubKey: script})
	refs, e := storeBlockOutputFactsContext(ctx, root, b)
	if e != nil {
		t.Fatal(e)
	}
	hashAt := func(int64) (string, error) { return b.Hash, nil }
	partial := map[string]outputFactRef{"0.0.5": refs["0.0.5"]}
	if e = s.commitFacts(ctx, 5, b.Hash, partial, hashAt); e == nil || s.head.Commitment != "" {
		t.Fatal("partial page advertised scanned coverage", e)
	}
	wrong := map[string]outputFactRef{"0.0.5": refs["1.0.5"], "1.0.5": refs["0.0.5"]}
	if e = s.commitFacts(ctx, 5, b.Hash, wrong, hashAt); e == nil || s.head.Commitment != "" {
		t.Fatal("wrong exact fact rows admitted", e)
	}
	calls := 0
	changing := func(int64) (string, error) {
		calls++
		if calls == 1 {
			return b.Hash, nil
		}
		return strings.Repeat("6", 64), nil
	}
	if e = s.CommitBlockContext(ctx, b, changing); e == nil {
		t.Fatal("changed selected anchor published")
	}
	cold, e := openAddressHistoryStoreContext(ctx, root)
	if e != nil || cold.head.Commitment != "" {
		t.Fatal("uncommitted staged pages became visible", e)
	}
}

func Test076AddressHistoryPartialTXIOLayersRequireRawFallback(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	ctx := context.Background()
	txio, e := openIndexStore(a.dataDir, "txo-spender")
	if e != nil {
		t.Fatal(e)
	}
	script, address := addressHistoryTestScript(6)
	b := txioTestBlock(t, a, 1, genesisHashDisplay, 1, transactionView{TxID: strings.Repeat("b", 64), Outputs: []outputView{{N: 0, ValueSats: 456, ScriptPubKey: script}}})
	txio.retainOutputValues = true
	if e = txio.appendBlock(b, 1, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	s, e := openAddressHistoryStoreContext(ctx, a.dataDir)
	if e != nil {
		t.Fatal(e)
	}
	hashAt := func(int64) (string, error) { return b.Hash, nil }
	if used, e := s.CommitTXIOFactsContext(ctx, 1, b.Hash, hashAt); e != nil || used || s.head.Commitment != "" {
		t.Fatal("value-only TXIO facts claimed complete script scan", used, e)
	}
	if e = s.CommitBlockContext(ctx, b, hashAt); e != nil {
		t.Fatal(e)
	}
	got, e := s.QueryContext(ctx, address, "", 10, hashAt)
	if e != nil || len(got.Outputs) != 1 {
		t.Fatal(got, e)
	}
	if _, e = s.QueryContext(ctx, "bc1invalid", "", 10, hashAt); e == nil {
		t.Fatal("invalid Bitcoin address admitted")
	}
}

func Test076AddressHistorySupportedDestinationsAndCreationIncarnations(t *testing.T) {
	ctx := context.Background()
	s, e := openAddressHistoryStoreContext(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	program20, program32 := strings.Repeat("11", 20), strings.Repeat("22", 32)
	scripts := []string{"76a914" + program20 + "88ac", "a914" + program20 + "87", "0014" + program20, "0020" + program32, "5120" + program32}
	outputs := []outputView{}
	for i, script := range scripts {
		outputs = append(outputs, outputView{ValueSats: uint64(10 + i), ScriptPubKey: script})
	}
	outputs = append(outputs, outputView{ValueSats: 70, ScriptPubKey: "21" + strings.Repeat("11", 33) + "ac"}, outputView{ValueSats: 80, ScriptPubKey: "6a"}, outputView{ValueSats: 90, ScriptPubKey: "51"})
	hashes := map[int64]string{9: strings.Repeat("9", 64), 91842: strings.Repeat("a", 64)}
	hashAt := addressHistoryTestHashAt(hashes)
	for _, height := range []int64{9, 91842} {
		// The same native transaction identifier does not collapse distinct
		// creation positions: historical address state retains both incarnations.
		if e = s.CommitBlockContext(ctx, addressHistoryTestBlock(height, hashes[height], outputs...), hashAt); e != nil {
			t.Fatal(e)
		}
	}
	buckets := 0
	if e = s.tree("addresses", s.head.AddressRoot, ctx).walk(func(ordinalKV) error { buckets++; return nil }); e != nil || buckets != 5 {
		t.Fatal("invented destination for non-address script", buckets, e)
	}
	for i, script := range scripts {
		decoded, _ := hex.DecodeString(script)
		typ, address := describeScript(decoded)
		got, e := s.QueryContext(ctx, address, "", 10, hashAt)
		if e != nil || len(got.Outputs) != 2 || got.ReceivedOutputCount != 2 || got.ReceivedSats != 2*uint64(10+i) || got.Outputs[0].Output != fmt.Sprintf("%d.0.91842", i) || got.Outputs[1].Output != fmt.Sprintf("%d.0.9", i) || got.Outputs[0].ScriptType != typ || got.Outputs[0].SpentStatus != "unknown" {
			t.Fatal(got, e)
		}
	}
}
