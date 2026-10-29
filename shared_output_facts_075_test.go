package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func Test075SharedFactsPartialLayersAndAuthentication(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	b := inscriptionTestBlock(transactionView{TxID: strings.Repeat("a", 64), Inputs: []inputView{{PrevTxID: strings.Repeat("b", 64)}}, Outputs: []outputView{{N: 0, ValueSats: 123, ScriptPubKey: "51"}, {N: 1, ValueSats: 0, ScriptPubKey: "6a"}}})
	b.Transactions[0].Outputs = []outputView{{N: 0, ValueSats: 50, ScriptPubKey: "51"}}
	full, e := storeBlockOutputFactsContext(ctx, root, b)
	if e != nil {
		t.Fatal(e)
	}
	again, e := storeBlockOutputFactLayersContext(ctx, root, b, true, true)
	if e != nil || again["0.1.1"] != full["0.1.1"] {
		t.Fatal("full fact owners did not share exact immutable page", again, e)
	}
	ref := full[outputCoordinate(0, 1, b.Height)]
	f, e := readOutputFactContext(ctx, root, ref)
	if e != nil || f.ValueSats != 123 || f.ScriptPubKey != "51" || f.Coinbase || f.BlockHash != b.Hash {
		t.Fatal(f, e)
	}
	partial, e := storeBlockOutputFactLayersContext(ctx, root, b, true, false)
	if e != nil {
		t.Fatal(e)
	}
	f, v, s, e := readOutputFactLayersContext(ctx, root, partial[outputCoordinate(0, 1, b.Height)])
	if e != nil || !v || s || f.ScriptPubKey != "" || f.ValueSats != 123 {
		t.Fatal("value-only history retained scripts", f, v, s, e)
	}
	if _, e = readOutputFactContext(ctx, root, partial[outputCoordinate(0, 1, b.Height)]); e == nil {
		t.Fatal("partial fact admitted as complete live coin")
	}
	path, _ := outputFactPath(root, ref.Page)
	if e = os.WriteFile(path, []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = readOutputFactContext(ctx, root, ref); e == nil {
		t.Fatal("damaged shared fact admitted")
	}
	repaired, e := storeBlockOutputFactsContext(ctx, root, b)
	if e != nil || repaired[outputCoordinate(0, 1, b.Height)] != ref {
		t.Fatal("canonical body could not repair content-addressed facts", repaired, e)
	}
	if f, e = readOutputFactContext(ctx, root, ref); e != nil || f.ValueSats != 123 || f.ScriptPubKey != "51" {
		t.Fatal("repaired facts changed their identity", f, e)
	}
}

func Test075SharedFactCollectionOnlyDeletesUnownedPages(t *testing.T) {
	root := t.TempDir()
	b := inscriptionTestBlock(transactionView{TxID: strings.Repeat("a", 64), Outputs: []outputView{{N: 0, ValueSats: 123, ScriptPubKey: "51"}}})
	refs, e := storeBlockOutputFactsContext(context.Background(), root, b)
	if e != nil {
		t.Fatal(e)
	}
	paths := map[string]bool{}
	old := time.Now().Add(-2 * time.Hour)
	for _, r := range refs {
		path, _ := outputFactPath(root, r.Page)
		paths[path] = true
		if e = os.Chtimes(path, old, old); e != nil {
			t.Fatal(e)
		}
	}
	if e = collectSharedOutputFactsContext(context.Background(), root); e != nil {
		t.Fatal(e)
	}
	for path := range paths {
		if _, e = os.Stat(path); !os.IsNotExist(e) {
			t.Fatal("unowned staged page retained", path, e)
		}
	}
}

func Test075TXIOExternalParentTranslationSurvivesRestart(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = true
	s, e := openIndexStore(a.dataDir, "txo-spender")
	if e != nil {
		t.Fatal(e)
	}
	parent, spender := strings.Repeat("a", 64), strings.Repeat("b", 64)
	s.spenderOrigin = func(context.Context, inputView, int64, int) (txioCreation, error) {
		return txioCreation{"0.0.0", genesisHashDisplay}, nil
	}
	b := txioTestBlock(t, a, 1, genesisHashDisplay, 1, transactionView{TxID: spender, Inputs: []inputView{{PrevTxID: parent}}})
	if e = s.appendBlock(b, 1, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	batch, e := s.readBatch(s.head.Commitment)
	if e != nil {
		t.Fatal(e)
	}
	if batch.Spenders.Rows[0].PrevTxID != "" || batch.Spenders.Rows[0].TxID != "" || len(batch.Spenders.PendingTranslations) != 0 {
		t.Fatal("resolved identities duplicated in TXIO")
	}
	if e = os.RemoveAll(filepath.Join(a.dataDir, "blocks")); e != nil {
		t.Fatal(e)
	}
	cold := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex()}
	got, e := cold.lookupIndexSpender(context.Background(), parent+":0")
	if e != nil || len(got.Rows) != 1 || got.Rows[0].TxID != spender || got.Rows[0].TxIndex != 1 {
		t.Fatal("external parent alias lost", got, e)
	}
	back, e := cold.lookupTXIO(context.Background(), "input", "i0.1.1")
	if e != nil || len(back.Rows) != 1 || back.Rows[0].Output != "0.0.0" || back.Rows[0].PrevTxID != parent {
		t.Fatal("external backward link lost", back, e)
	}
}

func Test075TXIOOutputAliasesAreNotTransactionIncarnations(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	s, e := openIndexStore(a.dataDir, "txo-spender")
	if e != nil {
		t.Fatal(e)
	}
	parent := strings.Repeat("a", 64)
	inputs := make([]inputView, 129)
	for i := range inputs {
		inputs[i] = inputView{PrevTxID: parent, PrevVout: uint32(i)}
	}
	s.spenderOrigin = func(_ context.Context, in inputView, _ int64, _ int) (txioCreation, error) {
		return txioCreation{strconv.Itoa(int(in.PrevVout)) + ".0.0", genesisHashDisplay}, nil
	}
	b := txioTestBlock(t, a, 1, genesisHashDisplay, 1, transactionView{TxID: strings.Repeat("b", 64), Inputs: inputs})
	if e = s.appendBlock(b, 1, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	got, e := a.lookupIndexSpender(context.Background(), parent+":128")
	if e != nil || len(got.Rows) != 1 || got.Rows[0].Vin != 128 {
		t.Fatal("output aliases counted as creation incarnations", got, e)
	}
	creation, e := s.bridgeTXIOCreation(context.Background(), inputView{PrevTxID: parent, PrevVout: 128}, 101)
	if e != nil || creation.Position != "128.0.0" {
		t.Fatal("builder alias fanout lookup failed", creation, e)
	}
}

func Test075TXIOEnrichmentBackfillsWithoutRewritingSpends(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	s, e := openIndexStore(a.dataDir, "txo-spender")
	if e != nil {
		t.Fatal(e)
	}
	b := txioTestBlock(t, a, 1, genesisHashDisplay, 1, transactionView{TxID: strings.Repeat("a", 64), Inputs: []inputView{{PrevTxID: strings.Repeat("b", 64)}}, Outputs: []outputView{{N: 0, ValueSats: 9, ScriptPubKey: "51"}}})
	if e = s.appendBlock(b, 1, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	before, e := s.readBatch(s.head.Commitment)
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(before)
	if len(before.Spenders.Origins) != 0 || len(before.Spenders.PreviousTransactions) != 0 || before.Spenders.Rows[0].PrevTxID != "" {
		t.Fatal("resolved row identity duplication")
	}
	if e = s.enrichTXIOBlockContext(context.Background(), b, true, false); e != nil {
		t.Fatal(e)
	}
	p, have, e := s.txioEnrichmentContext(context.Background(), 1, b.Hash)
	if e != nil || !have || !p.Values || p.Scripts {
		t.Fatal(p, have, e)
	}
	if e = s.enrichTXIOBlockContext(context.Background(), b, false, true); e != nil {
		t.Fatal(e)
	}
	p, have, e = s.txioEnrichmentContext(context.Background(), 1, b.Hash)
	if e != nil || !have || !p.Values || !p.Scripts {
		t.Fatal("layer upgrade lost retained values", p, have, e)
	}
	after, e := s.readBatch(s.head.Commitment)
	if e != nil {
		t.Fatal(e)
	}
	encodedAfter, _ := json.Marshal(after)
	if string(encoded) != string(encodedAfter) {
		t.Fatal("enrichment rewrote canonical spend commit")
	}
	full, e := storeBlockOutputFactsContext(context.Background(), a.dataDir, b)
	if e != nil || full["0.1.1"] != p.OutputFacts["0.1.1"] {
		t.Fatal("backfill and UTXO full facts did not share pages", e)
	}
	path, _ := outputFactPath(a.dataDir, p.OutputFacts["0.1.1"].Page)
	old := time.Now().Add(-2 * time.Hour)
	if e = os.Chtimes(path, old, old); e != nil {
		t.Fatal(e)
	}
	if e = collectSharedOutputFactsContext(context.Background(), a.dataDir); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(path); e != nil {
		t.Fatal("optional history pin deleted", e)
	}
}
