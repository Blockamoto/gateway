package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func Test074IndexReleaseAvailability(t *testing.T) {
	a := independentTestApp(t)
	for _, d := range indexDefinitions() {
		// The development consumers have their own profiles and stores. Their
		// availability must not unlock or reinterpret the legacy Bitmap index.
		available := d.ID == "headers" || d.ID == "blocks" || d.ID == "inscriptions" || d.ID == "tx-locator" || d.ID == "txo-spender" || d.ID == "utxo-state" || d.ID == "address-state" || d.ID == "address-history" || d.ID == "sat-state" || d.ID == "bitmap-districts" || d.ID == "bitmap-parcels" || d.ID == "bitmap-chain"
		if d.Locked == available || releaseFeatureAvailable(d.ID) != available {
			t.Fatalf("wrong release availability: %+v", d)
		}
		if available {
			if d.ID == "bitmap-districts" || d.ID == "bitmap-parcels" || d.ID == "bitmap-chain" {
				for _, start := range []func(indexBuildRequest) (indexJob, error){a.startIndexBuild, func(req indexBuildRequest) (indexJob, error) { return a.startIndexBuildID(req, false, "") }} {
					if _, err := start(indexBuildRequest{Index: d.ID}); err == nil || !strings.Contains(err.Error(), "coordinated timeline run") {
						t.Fatal("development Bitmap consumer entered the legacy executor", d.ID, err)
					}
				}
			}
			continue
		}
		if d.LockReason == "" {
			t.Fatal("locked definition has no explanation", d.ID)
		}
		for _, req := range []indexBuildRequest{{Index: d.ID}, {Index: "blocks", Outputs: []string{d.ID}}} {
			if _, err := a.planIndexBuild(req); err == nil || !strings.Contains(err.Error(), d.LockReason) {
				t.Fatal("plan bypass", req, err)
			}
			if _, err := a.startIndexBuild(req); err == nil || !strings.Contains(err.Error(), d.LockReason) {
				t.Fatal("build bypass", req, err)
			}
		}
		if _, err := a.setIndexLivePolicy(indexLiveRequest{Index: d.ID, Action: "enable"}); err == nil {
			t.Fatal("Live bypass", d.ID)
		}
		if _, err := a.indexQuery(d.ID, 50); err == nil {
			t.Fatal("query bypass", d.ID)
		}
		if _, err := a.runIndexCLI([]string{"plan", d.ID}); err == nil {
			t.Fatal("CLI bypass", d.ID)
		}
	}
	if _, err := a.planIndexBuild(indexBuildRequest{Index: "blocks"}); err != nil {
		t.Fatal("Blocks unavailable", err)
	}
	if _, err := a.planIndexBuild(indexBuildRequest{Index: "blocks", RetainSatHistory: true}); err == nil {
		t.Fatal("sat-history flag bypassed the Sat index lock")
	}
}

func Test077ManualUnavailableIndexesStayLocked(t *testing.T) {
	a := independentTestApp(t)
	h := indexInterfaceHandler(a)
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "plan", `{"index":"bitmap"}`},
		{"POST", "build", `{"index":"blocks","outputs":["bitmap"]}`},
		{"POST", "live", `{"index":"bitmap","action":"enable"}`},
		{"GET", "query?index=bitmap", ""}, {"GET", "lookup?index=bitmap&key=0", ""},
	} {
		r := indexInterfaceCall(h, tc.method, "/api/v1/index/"+tc.path, tc.body)
		if r.Code < 400 || !strings.Contains(r.Body.String(), "locked") {
			t.Fatalf("%s bypass: %d %s", tc.path, r.Code, r.Body.String())
		}
	}
	for _, handler := range []http.HandlerFunc{a.handleSatlineResolve, a.handleJobs, a.handleOrdFollow} {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("POST", "/", strings.NewReader(`{`)))
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "deferred") {
			t.Fatalf("deferred Satline API remained callable: %d %s", w.Code, w.Body.String())
		}
	}
}

func Test073RelatedInscriptionLocatorsAvailableAndOptional(t *testing.T) {
	if !releaseFeatureAvailable("inscriptions") || !releaseInscriptionLocatorsAvailable() || releaseFeatureAvailable("gateway-peerhood") {
		t.Fatal("wrong staged release capabilities")
	}
	a := independentTestApp(t)
	on, off := true, false
	for _, req := range []indexBuildRequest{
		{Index: "inscriptions", Outputs: []string{"inscription-tx-locator"}, ConventionalIDs: &off},
		{Index: "inscriptions", Outputs: []string{"tx-locator"}, ConventionalIDs: &off},
	} {
		if _, err := a.planIndexBuild(req); err == nil {
			t.Fatal("gated locator work accepted", req, err)
		}
	}
	for _, choice := range []*bool{nil, &on} {
		plan, err := a.planIndexBuild(indexBuildRequest{Index: "inscriptions", ConventionalIDs: choice})
		if err != nil || !plan.ConventionalIDs || len(plan.Outputs) != 2 || plan.Outputs[1].Index != inscriptionLocatorIndex {
			t.Fatal("transaction-stage enrichment unavailable", plan, err)
		}
	}
	for _, choice := range []*bool{&off} {
		plan, err := a.planIndexBuild(indexBuildRequest{Index: "inscriptions", ConventionalIDs: choice})
		if err != nil || plan.ConventionalIDs {
			t.Fatal("positional-only plan did not honor explicit off", plan, err)
		}
		for _, output := range plan.Outputs {
			if output.Index == "inscription-tx-locator" || output.Index == "tx-locator" {
				t.Fatal("explicit off derived transaction locators", output)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", "inscription-tx-locator")); !os.IsNotExist(err) {
		t.Fatal("checking a gated choice touched locator storage", err)
	}
}

func Test072InscriptionDefaultsPreserveExplicitChoice(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	if !a.loadSettings().OrdEnabled {
		t.Fatal("fresh inscription profile did not enable viewing")
	}
	if err := os.WriteFile(a.settingsPath(), []byte(`{"ord_enabled":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	if a.loadSettings().OrdEnabled {
		t.Fatal("upgrade overrode an explicit disabled choice")
	}
}

func Test066SavedDerivedWorkCannotResume(t *testing.T) {
	a := independentTestApp(t)
	queue := indexQueueState{Schema: 1, Entries: []indexQueueEntry{
		{Request: indexBuildRequest{Index: "bitmap"}, Job: indexJob{ID: "derived", Index: "bitmap", State: "queued"}},
		{Request: indexBuildRequest{Index: "blocks", Outputs: []string{"bitmap-compatibility"}}, Job: indexJob{ID: "shared", Index: "blocks", Outputs: []string{"bitmap-compatibility"}, State: "queued"}},
	}}
	queuePath := filepath.Join(a.dataDir, "indexes", "queue.json")
	if err := atomicWriteJSON(queuePath, queue); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(queuePath)
	for _, p := range []indexLivePolicy{{Index: "bitmap", Enabled: true, Retention: "ephemeral"}, {Index: "blocks", Enabled: true, Retention: "ephemeral", Outputs: []string{"bitmap-compatibility"}}} {
		if err := a.writeIndexLivePolicy(p); err != nil {
			t.Fatal(err)
		}
	}
	a.liveIndexTick()
	jobs, err := a.indexJobsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatal(jobs)
	}
	for _, job := range jobs {
		if job.State != "paused" || !strings.Contains(job.WaitingReason, "locked") {
			t.Fatal("saved job activated", job)
		}
		if _, err := a.controlIndexQueue(indexQueueRequest{ID: job.ID, Action: "resume"}); err == nil {
			t.Fatal("saved queue resumed", job)
		}
	}
	views, err := a.indexLiveViews()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.State != "locked" || v.On || v.Enabled {
			t.Fatal("saved Live activated", v)
		}
		if _, err := a.setIndexLivePolicy(indexLiveRequest{Index: v.Index, Action: "enable"}); err == nil {
			t.Fatal("saved shared/derived Live accepted enable", v)
		}
	}
	after, _ := os.ReadFile(queuePath)
	if string(before) != string(after) {
		t.Fatal("status/scheduler rewrote saved queue")
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", "job.json")); !os.IsNotExist(err) {
		t.Fatal("locked queue started a worker", err)
	}
}

func Test074LocatorShortcutsRespectReleaseAvailability(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	id := strings.Repeat("a", 64)
	a.cacheIndex.Tx[id] = txLocation{TxID: id, Height: 0, BlockHash: genesisHashDisplay}
	outpoint := id + ":0"
	spend := spendLocation{Outpoint: outpoint, Found: true, SpendingTxID: strings.Repeat("b", 64), Height: 0, BlockHash: genesisHashDisplay}
	a.cacheIndex.Spends[outpoint] = spend
	for _, declared := range []string{"utxos", "", "blockloc", "txloc", "spendloc"} {
		_, _, err := a.queryPeers(overlayRequest{Type: "utxos", TxID: id}, declared)
		if err == nil || !strings.Contains(err.Error(), "locked") {
			t.Fatal("request alias escaped address-state lock", declared, err)
		}
	}
	for _, requested := range []string{"txloc", "spendloc", "blockloc"} {
		_, _, err := a.queryPeers(overlayRequest{Type: requested, TxID: id}, "utxos")
		if err == nil || !strings.Contains(err.Error(), "locked") {
			t.Fatal("declared capability escaped address-state lock", requested, err)
		}
	}
	response, _, err := a.queryPeers(overlayRequest{Type: "txloc", TxID: id}, "txloc")
	if err != nil || response.TxLocation == nil || response.TxLocation.TxID != id {
		t.Fatal("available local TX locator shortcut stayed locked", response, err)
	}
	response, _, err = a.queryPeers(overlayRequest{Type: "spendloc", TxID: id}, "spendloc")
	if err != nil || response.SpendLocation == nil || *response.SpendLocation != spend {
		t.Fatal("available local spender shortcut stayed locked", response, err)
	}
	// Enabling local spender evidence does not enable the Gateway peer protocol.
	response, status := a.answerBODRequest(overlayRequest{Type: "spendloc", TxID: id})
	if status != "unsupported" || response.OK || response.SpendLocation != nil {
		t.Fatal("local spender availability enabled peer serving", response, status)
	}
	if _, err := a.queryGatewayPeer(overlayPeer{Addr: "127.0.0.1:1", Capabilities: []string{"spendloc"}}, overlayRequest{Type: "spendloc", TxID: id}); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatal("local spender availability enabled peer querying", err)
	}
	if a.cacheIndex.Tx[id].TxID != id || a.cacheIndex.Spends[outpoint] != spend {
		t.Fatal("release lock changed retained locator data")
	}
}

func Test066LockPreservesDerivedEvidenceAndRules(t *testing.T) {
	a, _ := indexPeerFixture(t, "bitmap", 2)
	p := filepath.Join(a.dataDir, "indexes", "bitmap", "head.json")
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = a.indexLookup("bitmap", "0")
	_, _ = a.indexQuery("bitmap", 50)
	status := a.indexStatus()
	for _, i := range status.Instances {
		if i.Definition == "bitmap" && (i.Queryable || i.Serveable) {
			t.Fatal("saved results still available", i)
		}
	}
	for _, provider := range status.Providers {
		if !releaseFeatureAvailable(provider.ID) && (provider.Queryable || provider.Serveable) {
			t.Fatal(provider)
		}
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("lock changed retained evidence")
	}
	d, _ := findIndexDefinition("bitmap")
	hash := d.RuleHash
	d.Locked = false
	d.LockReason = ""
	d.RuleHash = ""
	if indexDigest(struct {
		Definition        indexDefinition
		Parser, Reference string
	}{d, inscriptionParserProfile, inscriptionReferenceCommit}) != hash {
		t.Fatal("release policy entered the persisted recipe identity")
	}
	var head map[string]any
	if err = json.Unmarshal(after, &head); err != nil {
		t.Fatal(err)
	}
}

func Test074BlockFetchDoesNotImplicitlyDeriveLegacyIndexes(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	block, err := a.fetchAndDecode("0")
	if err != nil {
		t.Fatal(err)
	}
	a.indexVerifiedBlockKnowledge(block)
	a.indexCachedBlockPolicy(block, "release-block", true)
	if len(a.cacheIndex.Tx) != 0 || len(a.cacheIndex.Spends) != 0 {
		t.Fatal("block possession silently derived transaction/spender knowledge")
	}
	if len(a.cacheIndex.Blocks) == 0 {
		t.Fatal("block possession lost")
	}
	if a.ingest != nil {
		t.Fatal("graph worker started")
	}
	// Rebuilding possession metadata does not select derived indexes either.
	a.rebuildCacheIndex()
	if len(a.cacheIndex.Tx) != 0 || len(a.cacheIndex.Spends) != 0 {
		t.Fatal("cache recovery silently enabled indexing")
	}
	// The legacy explicit choice remains supported, including enabling it after
	// the same block was viewed while off. Canonical browse/range policies are
	// independent and remain covered by their own tests.
	a.settings.GraphIndex = true
	a.indexVerifiedBlockKnowledge(block)
	if len(a.cacheIndex.Tx) != 1 {
		t.Fatal("explicit legacy indexing did not retain known transaction")
	}
	a.indexCachedBlockPolicy(block, "release-block", false)
	if len(a.cacheIndex.Tx) != 1 {
		t.Fatal("source-only cache update removed existing knowledge")
	}
}
