package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func Test066IndexReleaseAvailability(t *testing.T) {
	a := independentTestApp(t)
	for _, d := range indexDefinitions() {
		available := d.ID == "headers" || d.ID == "blocks"
		if d.Locked == available || releaseFeatureAvailable(d.ID) != available {
			t.Fatalf("wrong release availability: %+v", d)
		}
		if available {
			continue
		}
		if d.LockReason == "" {
			t.Fatal("locked definition has no explanation", d.ID)
		}
		for _, req := range []indexBuildRequest{{Index: d.ID}, {Index: "blocks", Outputs: []string{d.ID}}} {
			if _, err := a.planIndexBuild(req); err == nil || !strings.Contains(err.Error(), "locked") {
				t.Fatal("plan bypass", req, err)
			}
			if _, err := a.startIndexBuild(req); err == nil || !strings.Contains(err.Error(), "locked") {
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

func Test066ManualIndexAndModuleRequestsAreLocked(t *testing.T) {
	a := independentTestApp(t)
	h := indexInterfaceHandler(a)
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "plan", `{"index":"bitmap"}`}, {"POST", "build", `{"index":"blocks","outputs":["inscriptions"]}`},
		{"POST", "build", `{"index":"blocks","retain_sat_history":true}`},
		{"POST", "live", `{"index":"sat-state","action":"enable"}`}, {"GET", "query?index=inscriptions", ""},
		{"GET", "lookup?index=bitmap&key=0", ""}, {"GET", "sat?sat=0", ""}, {"POST", "discover-sat", `{"id":"anything"}`},
	} {
		r := indexInterfaceCall(h, tc.method, "/api/v1/index/"+tc.path, tc.body)
		if r.Code < 400 || !strings.Contains(r.Body.String(), "locked") {
			t.Fatalf("%s bypass: %d %s", tc.path, r.Code, r.Body.String())
		}
	}
	for _, handler := range []http.HandlerFunc{a.handleOrdResolve, a.handleOrdFollow, a.serveOrdContent, a.handleSatlineResolve, a.handleSatlineFollow, a.handleSatlineRun, a.handleGraphBuild, a.handleResolveSpender, a.handleAddressUTXOs, a.handleJobs} {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("POST", "/", strings.NewReader(`{}`)))
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "locked") {
			t.Fatalf("handler bypass: %d %s", w.Code, w.Body.String())
		}
	}
	if _, err := a.resolveInscription(context.Background(), strings.Repeat("a", 64)+"i0", ""); err == nil {
		t.Fatal("known-id bypass")
	}
	if _, err := a.resolveCoordinate(bodCoordinate{Kind: coordInscription}); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatal("positional inscription bypass", err)
	}
}

func Test066SavedDerivedWorkCannotResume(t *testing.T) {
	a := independentTestApp(t)
	queue := indexQueueState{Schema: 1, Entries: []indexQueueEntry{
		{Request: indexBuildRequest{Index: "bitmap"}, Job: indexJob{ID: "derived", Index: "bitmap", State: "queued"}},
		{Request: indexBuildRequest{Index: "blocks", Outputs: []string{"sat-state"}}, Job: indexJob{ID: "shared", Index: "blocks", Outputs: []string{"sat-state"}, State: "queued"}},
	}}
	queuePath := filepath.Join(a.dataDir, "indexes", "queue.json")
	if err := atomicWriteJSON(queuePath, queue); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(queuePath)
	for _, p := range []indexLivePolicy{{Index: "bitmap", Enabled: true, Retention: "ephemeral"}, {Index: "blocks", Enabled: true, Retention: "ephemeral", Outputs: []string{"sat-state"}}} {
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

func Test066LocatorShortcutsCannotReadLockedProviders(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	id := strings.Repeat("a", 64)
	a.cacheIndex.Tx[id] = txLocation{TxID: id, Height: 0, BlockHash: genesisHashDisplay}
	for _, capability := range []string{"txloc", "spendloc", "utxos"} {
		for _, declared := range []string{capability, "", "blockloc"} {
			_, _, err := a.queryPeers(overlayRequest{Type: capability, TxID: id}, declared)
			if err == nil || !strings.Contains(err.Error(), "locked") {
				t.Fatal("local provider shortcut escaped release lock", capability, declared, err)
			}
		}
	}
	if a.cacheIndex.Tx[id].TxID != id {
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

func Test066BlockFetchDoesNotDeriveLockedIndexes(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	block, err := a.fetchAndDecode("0")
	if err != nil {
		t.Fatal(err)
	}
	a.settings.GraphIndex = true
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
}
