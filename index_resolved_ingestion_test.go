package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolvedIngestionLocalBodiesAllSparseTracksWithoutRaw(t *testing.T) {
	a := runTestApp(t)
	ctx := context.Background()
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: "blocks", Armed: false}, {Index: "tx-locator", Armed: true}, {Index: "inscriptions", Armed: true, indexRunOptions: indexRunOptions{Mode: "full", ConventionalIDs: boolPointer(true)}}, {Index: "txo-spender", Armed: true, indexRunOptions: indexRunOptions{RetainOutputValues: boolPointer(true), RetainOutputScripts: boolPointer(true)}}, {Index: "address-history", Armed: true}}}
	if _, err := a.prepareIndexRun(req); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"1", "0", "1"} {
		if _, err := a.fetchAndDecodeContext(ctx, query); err != nil {
			t.Fatal(query, err)
		}
	}
	for _, id := range []string{"tx-locator", "inscriptions", "txo-spender"} {
		_, h, err := a.browseStore(id)
		if err != nil || len(h.Blocks) != 2 {
			t.Fatal(id, h, err)
		}
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", "blocks", "body-receipts")); !os.IsNotExist(err) {
		t.Fatal("Raw Off retained body producer", err)
	}
	value, err := a.lookupTXIOOutputFacts(ctx, "0.0.1")
	if err != nil || value.State != "known" || value.ValueSats == nil || *value.ValueSats != 5000000000 || value.ScriptPubKey == nil {
		t.Fatal(value, err)
	}
	b, have, err := a.browsedBatchContext(ctx, "inscriptions", 1, "")
	if err != nil || !have || inscriptionBatchMode(b) != "full" {
		t.Fatal("current Full choice ignored", b, err)
	}
	h, err := openAddressHistoryStoreContext(ctx, a.dataDir)
	if err != nil || len(h.head.Coverage) != 1 || h.head.Coverage[0] != (heightInterval{0, 1}) {
		t.Fatal("sparse address source coverage", h, err)
	}
	if got := a.indexRunSnapshot(); got.Run != nil {
		t.Fatal("ordinary resolution started background work", got.Run)
	}
	rows, _ := a.indexBrowsePolicies()
	if len(rows) != 15 {
		t.Fatal("capability lanes", rows)
	}
}

func TestResolvedIngestionIndependentFactsOnlyExplicitRunAndOwnership(t *testing.T) {
	a := runTestApp(t)
	ctx := context.Background()
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: "txo-spender", Armed: false, indexRunOptions: indexRunOptions{RetainOutputValues: boolPointer(true), RetainOutputScripts: boolPointer(true)}}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready || len(plan.Consumers) != 1 || plan.Consumers[0].RangeMode != "sparse_facts" {
		t.Fatal(plan, err)
	}
	if _, err := a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
	if c, _ := runSelection(done.Prepared, "txo-spender"); c.Armed {
		t.Fatal("fact-only pass armed links")
	}
	for _, path := range []string{filepath.Join(a.dataDir, "indexes", "txo-spender", "head.json"), filepath.Join(a.dataDir, "indexes", "browse", "txo-spender", "head.json")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("facts created links", path, err)
		}
	}
	for _, id := range []string{"txio-values", "txio-scripts"} {
		h, err := readSparseOutputLayerHead(a.dataDir, id)
		if err != nil || len(h.Blocks) != 2 {
			t.Fatal(id, h, err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	filepath.Walk(outputFactDirectory(a.dataDir), func(path string, info os.FileInfo, e error) error {
		if e == nil && !info.IsDir() {
			return os.Chtimes(path, old, old)
		}
		return e
	})
	if err := collectSharedOutputFactsContext(ctx, a.dataDir); err != nil {
		t.Fatal(err)
	}
	facts, err := a.lookupTXIOOutputFacts(ctx, "0.0.1")
	if err != nil || facts.ValueSats == nil || facts.ScriptPubKey == nil {
		t.Fatal("independent owners lost facts", facts, err)
	}
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "txo-spender", Armed: false}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.fetchAndDecodeContext(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	facts, err = a.lookupTXIOOutputFacts(ctx, "0.0.1")
	if err != nil || facts.State != "known" {
		t.Fatal("Off destroyed saved facts", facts, err)
	}
}

func TestResolvedIngestionSparseTXIOPendingAndParentTranslation(t *testing.T) {
	a := runTestApp(t)
	ctx := context.Background()
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "txo-spender", Armed: true}}}); err != nil {
		t.Fatal(err)
	}
	parent, spender, unknown := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	b1 := txioTestBlock(t, a, 1, genesisHashDisplay, 1, transactionView{TxID: parent, Inputs: []inputView{{PrevTxID: unknown}}, Outputs: []outputView{{N: 0}}})
	b7 := txioTestBlock(t, a, 7, testID(6), 7, transactionView{TxID: spender, Inputs: []inputView{{PrevTxID: parent}, {PrevTxID: unknown, PrevVout: 2}}})
	installRunProtocolHeader(t, a, &b1)
	installRunProtocolHeader(t, a, &b7)
	for _, b := range []blockView{b7, b1, b7} {
		results := a.ingestResolvedVerifiedBlock(ctx, b)
		for _, r := range results {
			if r.State == "unavailable" || r.State == "error" {
				t.Fatal("sparse producer", r)
			}
		}
	}
	known, err := a.lookupTXIO(ctx, "input", "i0.1.7")
	if err != nil || known.State != "known" || len(known.Rows) != 1 || known.Rows[0].Output != "0.1.1" {
		t.Fatal("sparse translation", known, err)
	}
	pending, err := a.lookupTXIO(ctx, "input", "i1.1.7")
	if err != nil || pending.State != "unresolved" || pending.Rows[0].PrevTxID != unknown {
		t.Fatal("missing translation became empty", pending, err)
	}
	forward, err := a.lookupTXIO(ctx, "output", "0.1.1")
	if err != nil || forward.State != "known" || forward.Rows[0].Input != "i0.1.7" {
		t.Fatal(forward, err)
	}
	if s, err := indexStoreHead(a.dataDir, "txo-spender"); err != nil || s.checkpoint != nil {
		t.Fatal("sparse writes advanced contiguous head", s, err)
	}
}

func TestResolvedIngestionDeferredDependencyQueueAndExactNextState(t *testing.T) {
	a := runTestApp(t)
	ctx := context.Background()
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: "utxo-state", Armed: true}, {Index: "sat-state", Armed: true, indexRunOptions: indexRunOptions{RetainSatHistory: true}}, {Index: "address-state", Armed: true}, {Index: "tx-locator", Armed: true}}}
	if _, err := a.prepareIndexRun(req); err != nil {
		t.Fatal(err)
	}
	// An isolated later block cannot fabricate either complete state.
	if _, err := a.fetchAndDecodeContext(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	if u, err := utxoStoreHead(a.dataDir); err != nil || u.Head() != nil {
		t.Fatal("UTXO skipped genesis", u, err)
	}
	if s, err := indexStoreHead(a.dataDir, "sat-state"); err != nil || s.checkpoint != nil {
		t.Fatal("Sats skipped genesis", s, err)
	}
	work, drain := a.withDeferredBlockIngestion(ctx)
	deferredFetch := func() error {
		utxoWriterMu.Lock()
		defer utxoWriterMu.Unlock()
		_, err := a.fetchAndDecodeContext(work, "0")
		return err
	}
	if err := deferredFetch(); err != nil {
		t.Fatal(err)
	}
	if u, _ := utxoStoreHead(a.dataDir); u.Head() != nil {
		t.Fatal("deferred fetch entered state writer before its lock boundary")
	}
	drain()
	if _, err := a.fetchAndDecodeContext(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	u, err := utxoStoreHead(a.dataDir)
	if err != nil || u.Head() == nil || u.Head().Height != 1 {
		t.Fatal("exact-next UTXO", u, err)
	}
	s, err := indexStoreHead(a.dataDir, "sat-state")
	if err != nil || s.checkpoint == nil || s.checkpoint.Height != 1 {
		t.Fatal("exact-next Sats", s, err)
	}
	address, err := readAddressStateContext(ctx, a.dataDir)
	if err != nil || address.Height != 1 {
		t.Fatal("matching Current Addresses", address, err)
	}
	_, head, err := a.browseStore("tx-locator")
	if err != nil || len(head.Blocks) != 2 {
		t.Fatal("dependency blocks were dropped", head, err)
	}
}

func TestResolvedIngestionChildDeletionPreservesLinksAndSibling(t *testing.T) {
	a := runTestApp(t)
	ctx := context.Background()
	choice := indexRunSelection{Index: "txo-spender", Armed: true, indexRunOptions: indexRunOptions{RetainOutputValues: boolPointer(true), RetainOutputScripts: boolPointer(true)}}
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{choice}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.fetchAndDecodeContext(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	_, before, err := a.browseStore("txo-spender")
	if err != nil {
		t.Fatal(err)
	}
	deleteConfirmBatch(t, a, indexDeleteRequest{Indices: []string{"txio-values"}})
	_, after, err := a.browseStore("txo-spender")
	if err != nil || indexDigest(before) != indexDigest(after) {
		t.Fatal("child deletion changed positional links", before, after, err)
	}
	if _, err := a.fetchAndDecodeContext(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	facts, err := a.lookupTXIOOutputFacts(ctx, "0.0.1")
	if err != nil || facts.ValueSats != nil || facts.ScriptPubKey == nil {
		t.Fatal("child deletion removed sibling or recreated values", facts, err)
	}
	selected, _ := runSelection(a.indexRunSnapshot().Prepared, "txo-spender")
	if !selected.Armed || selected.RetainOutputValues == nil || *selected.RetainOutputValues || selected.RetainOutputScripts == nil || !*selected.RetainOutputScripts {
		t.Fatal("wrong child consent changed", selected)
	}
}

func TestResolvedIngestionDecoratesLegacyDecodedCoinbase(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	ctx := context.Background()
	b, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "legacy decoded cache", true)
	if err != nil {
		t.Fatal(err)
	}
	b.CoinbaseMessage = nil
	a.blockBaseAcquire = func(context.Context, blockTarget, appSettings) (blockView, error) { return b, nil }
	view, err := a.fetchAndDecodeContext(ctx, "0")
	if err != nil || view.CoinbaseMessage == nil {
		t.Fatal("legacy decoded source omitted complete coinbase message", view.CoinbaseMessage, err)
	}
}

func TestResolvedIngestionPreparedBodyReuseHonorsNewChildConsent(t *testing.T) {
	a := runTestApp(t)
	ctx := context.Background()
	b, err := a.fetchAndDecodeContext(ctx, "0")
	if err != nil {
		t.Fatal(err)
	}
	prepared := &indexRunPrepared{block: b, rawProduced: true}
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "txo-spender", Armed: false, indexRunOptions: indexRunOptions{RetainOutputValues: boolPointer(true)}}}}); err != nil {
		t.Fatal(err)
	}
	if err := a.ensurePreparedRawProducer(context.WithValue(ctx, indexRunPreparedKey{}, prepared), prepared); err != nil {
		t.Fatal(err)
	}
	facts, err := a.lookupTXIOOutputFacts(ctx, "0.0.0")
	if err != nil || facts.ValueSats == nil || facts.ScriptPubKey != nil {
		t.Fatal("cached prepared body missed newly armed Values", facts, err)
	}
}

func TestResolvedIngestionInscriptionModeIDsAndPublicationFence(t *testing.T) {
	a := runTestApp(t)
	ctx := context.Background()
	choice := indexRunSelection{Index: "inscriptions", Armed: true, indexRunOptions: indexRunOptions{Mode: "full", ConventionalIDs: boolPointer(true)}}
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{choice}}); err != nil {
		t.Fatal(err)
	}
	b := bitmapDevelopmentFixtureBlock(2, testID(1), "0.bitmap")
	installRunProtocolHeader(t, a, &b)
	a.ingestResolvedVerifiedBlock(ctx, b)
	batch, have, err := a.browsedBatchContext(ctx, "inscriptions", 2, b.Hash)
	if err != nil || !have || len(batch.Inscriptions) != 1 || len(batch.InscriptionBlock.ConventionalIDs) != 1 {
		t.Fatal(batch, err)
	}
	choice.Mode = "lean"
	choice.ConventionalIDs = boolPointer(false)
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{choice}}); err != nil {
		t.Fatal(err)
	}
	a.ingestResolvedVerifiedBlock(ctx, b)
	batch, have, err = a.browsedBatchContext(ctx, "inscriptions", 2, b.Hash)
	if err != nil || !have || len(batch.Inscriptions) != 1 {
		t.Fatal("normal lookup discarded saved Full data", batch, err)
	}
	guard := resolvedSelectionContext(ctx, a.dataDir, "inscriptions", choice, func(int64) (string, error) { return b.Hash, nil }, b)
	choice.Armed = false
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{choice}}); err != nil {
		t.Fatal(err)
	}
	if err := checkIndexRunPublication(guard); err == nil {
		t.Fatal("late Off allowed publication")
	}
}
