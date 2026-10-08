package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func indexProviderRow(t *testing.T, rows []indexProviderView, id, provider string) indexProviderView {
	t.Helper()
	for _, r := range rows {
		if r.ID == id && r.Provider == provider {
			return r
		}
	}
	t.Fatalf("missing provider %s/%s", id, provider)
	return indexProviderView{}
}

func TestIndexProvidersCoreProgressAndPolicy(t *testing.T) {
	settings := appSettings{ServeData: true}
	core := coreStatus{Connected: true, Height: 100, BestBlockHash: genesisHashDisplay, TxIndexEnabled: true, TxIndexHeight: 60, SpenderIndexEnabled: true, SpenderIndexHeight: 40}
	rows := buildIndexProviderViews(settings, core, indexProviderState{})
	tx := indexProviderRow(t, rows, "tx-locator", "bitcoin_core_txindex")
	spend := indexProviderRow(t, rows, "txo-spender", "bitcoin_core_txospenderindex")
	if tx.Queryable || tx.Serveable || tx.Readiness != "locked" || tx.Completeness != "provider_reported_partial" || len(tx.Coverage) != 1 || tx.Coverage[0].To != 60 {
		t.Fatalf("partial txindex claims: %+v", tx)
	}
	if spend.Queryable || spend.Serveable || spend.Coverage[0].To != 40 {
		t.Fatalf("partial spender claims: %+v", spend)
	}
	core.TxIndex, core.SpenderIndex = true, true
	core.TxIndexHeight, core.SpenderIndexHeight = 100, 100
	rows = buildIndexProviderViews(settings, core, indexProviderState{})
	tx = indexProviderRow(t, rows, "tx-locator", "bitcoin_core_txindex")
	if tx.Completeness != "provider_reported_at_snapshot" || tx.Serveable || tx.Queryable || tx.Snapshot == nil || tx.Snapshot.Height != 100 {
		t.Fatalf("synced provider lost its qualified snapshot: %+v", tx)
	}
	core.InitialBlockDownload = true
	rows = buildIndexProviderViews(settings, core, indexProviderState{})
	if got := indexProviderRow(t, rows, "tx-locator", "bitcoin_core_txindex"); got.Queryable || got.Serveable || got.Readiness != "locked" {
		t.Fatalf("IBD bare-ID route advertised: %+v", got)
	}
	if got := indexProviderRow(t, rows, "txo-spender", "bitcoin_core_txospenderindex"); got.Completeness != "provider_reported_partial" {
		t.Fatalf("IBD promoted to complete: %+v", got)
	}
	settings.CoreDisabled = true
	for _, r := range buildIndexProviderViews(settings, core, indexProviderState{}) {
		if strings.HasPrefix(r.Provider, "bitcoin_core") && (r.Queryable || r.Serveable || len(r.Coverage) > 0 || r.Snapshot != nil) {
			t.Fatalf("disabled Core remains available: %+v", r)
		}
	}
}

func TestIndexProvidersDoNotInventPrunedOrNativeCoverage(t *testing.T) {
	core := coreStatus{Connected: true, Height: 100, BestBlockHash: genesisHashDisplay, Pruned: true}
	local := indexProviderState{GraphCoverage: []heightInterval{{From: 90, To: 99}}}
	rows := buildIndexProviderViews(appSettings{ServeData: true}, core, local)
	blocks := indexProviderRow(t, rows, "blocks", "bitcoin_core")
	if len(blocks.Coverage) > 0 {
		t.Fatalf("missing pruning boundary invented archival coverage: %+v", blocks)
	}
	graph := indexProviderRow(t, rows, "tx-locator", "gateway_native_graph")
	if graph.Queryable || graph.Serveable || graph.Readiness != "locked" || graph.Completeness != "recorded_ranges" {
		t.Fatalf("graph metadata became full coverage or nonexistent wire support: %+v", graph)
	}
	core.PruneHeight = 95
	blocks = indexProviderRow(t, buildIndexProviderViews(appSettings{}, core, local), "blocks", "bitcoin_core")
	if len(blocks.Coverage) != 1 || blocks.Coverage[0] != (heightInterval{From: 95, To: 100}) || blocks.Serveable {
		t.Fatalf("pruned retention/policy lost: %+v", blocks)
	}
}

func TestIndexProvidersPrivateLocatorsSurviveBlockEviction(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.ShareCache = true, true
	a.coverage.TxLocation = []heightInterval{{From: 0, To: 0}}
	for id, b := range a.cacheIndex.Blocks {
		b.Private = true
		a.cacheIndex.Blocks[id] = b
	}
	for id := range a.cacheIndex.Tx {
		a.cacheIndex.PrivateTx[id] = true
	}
	rows := a.indexProviderViews()
	tx := indexProviderRow(t, rows, "tx-locator", "gateway_cache")
	blocks := indexProviderRow(t, rows, "blocks", "gateway_cache")
	if tx.Queryable || tx.Serveable || len(tx.PublicCoverage) != 0 || !blocks.Queryable || blocks.Serveable {
		t.Fatalf("private data leaked or became unavailable locally: tx=%+v blocks=%+v", tx, blocks)
	}
	for _, b := range a.cacheIndex.Blocks {
		if err := os.Remove(filepath.Join(a.dataDir, "blocks", "raw", b.Prefix+".block")); err != nil {
			t.Fatal(err)
		}
	}
	rows = a.indexProviderViews()
	if indexProviderRow(t, rows, "blocks", "gateway_cache").Queryable || indexProviderRow(t, rows, "tx-locator", "gateway_cache").Queryable {
		t.Fatal("block possession and retained locator knowledge were conflated")
	}
}

func TestIndexProvidersCoreAddressStateIsLocalAndOnDemand(t *testing.T) {
	dir, stop, scans := fakeCoreWithScanCounter(t)
	defer stop()
	a := &app{dataDir: t.TempDir(), settings: appSettings{BitcoinDataDir: dir, RPCAuthMode: "auto", ServeData: true}, status: appStatus{HeaderHeight: -1}, cacheIndex: newCacheIndex()}
	rows := a.indexProviderViews()
	current := indexProviderRow(t, rows, "address-state", "bitcoin_core_chainstate")
	history := indexProviderRow(t, rows, "address-history", "none")
	if current.Queryable || current.Serveable || current.Readiness != "locked" || current.Completeness != "on_demand_snapshot" || len(current.Coverage) != 0 || history.Queryable || history.Serveable {
		t.Fatalf("current state and history confused: current=%+v history=%+v", current, history)
	}
	if scans.Load() != 0 {
		t.Fatal("catalog inspection initiated an address scan")
	}
	set, err := coreAddressUTXOs(a.settings, "bc1qtest")
	if err != nil || !set.Success || set.BestBlock != genesisHashDisplay || scans.Load() != 1 {
		t.Fatalf("local explicit scan failed: %+v %v", set, err)
	}
}

func TestIndexProvidersLocatorHintStillNeedsBitcoinEvidence(t *testing.T) {
	a, payload := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.NetworkDisabled = true, true
	header, err := a.readSelectedHeader(0)
	if err != nil {
		t.Fatal(err)
	}
	v, err := parseBlockDetailed(0, genesisHashDisplay, header, payload, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	id := v.Transactions[0].TxID
	resolved, err := a.verifyTxLocation(id, txLocation{TxID: id, Height: 0, BlockHash: genesisHashDisplay}, "catalog fixture locator")
	if err != nil || !resolved.TransactionVerified || resolved.LocatorPeer != "catalog fixture locator" {
		t.Fatalf("locator provenance/evidence missing: %+v %v", resolved, err)
	}
	falseID := strings.Repeat("a", 64)
	if _, err := a.verifyTxLocation(falseID, txLocation{TxID: falseID, Height: 0, BlockHash: genesisHashDisplay}, "false hint"); err == nil {
		t.Fatal("locator hint was accepted without transaction inclusion")
	}
}

func TestIndexProvidersSpenderMissRemainsUnknown(t *testing.T) {
	existing := strings.Repeat("a", 64)
	settings, stop := fakeCoreUnspentGraph(t, existing)
	defer stop()
	for _, tc := range []struct{ txid, state string }{{existing, "unspent_at_snapshot"}, {strings.Repeat("b", 64), "unknown"}} {
		exists, hash, err := coreConfirmedUnspent(settings, tc.txid, 0)
		if err != nil || exists != (tc.state == "unspent_at_snapshot") {
			t.Fatalf("spender %s: %v %v", tc.txid, exists, err)
		}
		if exists && hash != genesisHashDisplay {
			t.Fatalf("unspent answer omitted snapshot: %s", hash)
		}
	}
	// Catalog presence does not upgrade incomplete native coverage into a
	// negative answer for an output whose creation block is known.
	b, genesis := initGraphTestApp(t)
	b.settings.CoreDisabled = true
	if err := b.graphIndexBlock(genesis, true); err != nil {
		t.Fatal(err)
	}
	appendGraphTestHeader(t, b, genesisHashDisplay, 123)
	if got := indexProviderRow(t, b.indexProviderViews(), "txo-spender", "gateway_native_graph"); got.Queryable || got.Completeness != "recorded_ranges" {
		t.Fatalf("native sparse provider missing: %+v", got)
	}
	if snap, why := b.graphCanProveUnspent(genesis.Transactions[0].TxID, 0); snap != nil {
		t.Fatalf("native coverage gap became unspent: %+v %s", snap, why)
	}
}

func TestIndexProvidersSatStateRequiresSnapshotTerminal(t *testing.T) {
	point := satlinePoint{TxID: strings.Repeat("a", 64), BlockHash: genesisHashDisplay, Height: 0, TxIndex: 0}
	base := satlineResult{CurrentSatpoint: &point, State: "CURRENTLY_UNSPENT", VerificationState: "header_anchored", Snapshot: &chainSnapshot{Network: "mainnet", Height: 0, Hash: genesisHashDisplay}}
	if !indexSatStateAtSnapshot(base) {
		t.Fatal("verified snapshot-terminal answer not recognized")
	}
	for _, state := range []string{"UNRESOLVED", "STEP_LIMIT", "PAUSED", "LOST_DUPLICATE_TXID"} {
		r := base
		r.State = state
		if indexSatStateAtSnapshot(r) {
			t.Fatalf("%s frontier presented as current placement", state)
		}
	}
	for _, anchor := range []anchorState{anchorUnavailable, anchorConflict} {
		r := base
		r.AnchorStatus = string(anchor)
		if indexSatStateAtSnapshot(r) {
			t.Fatalf("unchecked anchor %s accepted", anchor)
		}
	}
	base.Snapshot = nil
	if indexSatStateAtSnapshot(base) {
		t.Fatal("missing snapshot accepted")
	}
}

func TestIndexProvidersSatlineRequiresActualPublication(t *testing.T) {
	_, q, r := satlinePeerFixture(t)
	a := &app{dataDir: t.TempDir(), settings: appSettings{CoreDisabled: true, SatlineEnabled: true, SatlineServePublished: true}, status: appStatus{HeaderHeight: -1}}
	a.initSatlineStore()
	if err := a.saveSatlineRecord(q.Kind, q.key(), q.Input, r); err != nil {
		t.Fatal(err)
	}
	check := func(want bool) {
		t.Helper()
		rows := a.indexProviderViews()
		line := indexProviderRow(t, rows, "satline", "gateway_satline")
		state := indexProviderRow(t, rows, "sat-state", "gateway_satline_resolver")
		if line.Serveable != want || state.Serveable || len(line.Coverage) != 0 || len(state.Coverage) != 0 {
			t.Fatalf("publication/state capability mismatch: line=%+v state=%+v", line, state)
		}
	}
	check(false)
	p := satlinePublication{VerifierVersion: blockVerifierVersion, Schema: 1, Query: q, Start: *r.BirthSatpoint, Hops: r.Hops[:1]}
	if err := atomicWriteJSON(a.satlinePublicationPath(q), p); err != nil {
		t.Fatal(err)
	}
	check(false) // Retained publications cannot bypass this release lock.
	p.Hops = nil
	if err := atomicWriteJSON(a.satlinePublicationPath(q), p); err != nil {
		t.Fatal(err)
	}
	check(false) // An empty publication cannot answer any segment request.
	a.settings.SatlineEnabled = false
	check(false)
}
