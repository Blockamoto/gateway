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

func satlineReleaseRequest077(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, "http://127.0.0.1:9090"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Content-Type", "application/json")
	return r
}

func Test077SatlineReleaseBlocksEveryTraceEntryAndPreservesSavedSatQueries(t *testing.T) {
	a, s, _, b1 := satRuntime077Real(t)
	a.settings.SatlineEnabled, a.settings.SatlineUsePeers, a.settings.SatlineServePublished = true, true, true
	if releaseFeatureAvailable("satline") || a.satlineEnabled() || a.satlineNetworkingEnabled() || !releaseFeatureAvailable("sat-state") {
		t.Fatal("saved settings bypassed trace deferral or disabled Sat state")
	}
	path := filepath.Join(a.dataDir, "satline", "jobs", "saved.json")
	job := satlineJob{ID: "saved", Query: satlineQuery{Kind: "sat", Input: "5000000000"}, Operation: "resolve", Status: "running", Done: false}
	if err := atomicWriteJSON(path, job); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	a.loadDurableJobs()
	if a.satlineActiveJob != "" || a.satlineJobs["saved"] == nil || !a.satlineJobs["saved"].Done || a.satlineJobs["saved"].Status != "paused" || !strings.Contains(a.satlineJobs["saved"].Stage, "deferred") {
		t.Fatal("saved work became runnable", a.satlineJobs)
	}
	for _, prefix := range []string{"/api/satline", "/api/v1/satline"} {
		for _, tc := range []struct {
			method, path, body string
			handler            http.HandlerFunc
		}{
			{"POST", "/resolve", `{"sat_number":5000000000}`, a.handleSatlineResolve},
			{"POST", "/follow", `{"satpoint":"0.0.0.1"}`, a.handleSatlineFollow},
			{"GET", "/status", "", a.handleSatlineStatus},
			{"POST", "/ui/run", `{"input":"5000000000","operation":"start"}`, a.handleSatlineRun},
			{"POST", "/ui/run", `{"input":"5000000000","operation":"next"}`, a.handleSatlineRun},
			{"POST", "/ui/run", `{"input":"5000000000","operation":"recheck"}`, a.handleSatlineRun},
			{"POST", "/ui/run", `{"input":"5000000000","operation":"rebuild"}`, a.handleSatlineRun},
		} {
			w := httptest.NewRecorder()
			tc.handler(w, satlineReleaseRequest077(tc.method, prefix+tc.path, tc.body))
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "deferred") {
				t.Fatalf("trace entry remained available %s%s: %d %s", prefix, tc.path, w.Code, w.Body.String())
			}
		}
	}
	for _, tc := range []struct {
		path, body string
		handler    http.HandlerFunc
	}{
		{"/api/v1/jobs", `{"id":"saved","action":"resume"}`, a.handleJobs},
		{"/api/jobs", `{"id":"saved","action":"resume"}`, a.handleJobs},
		{"/api/v1/sats/reverse", `{"input":"0.0.0.1","rebuild":true}`, a.handleSatReverse077},
		{"/api/v1/ord/follow", `{"id":"` + testID(1) + `i0"}`, a.handleOrdFollow},
	} {
		w := httptest.NewRecorder()
		tc.handler(w, satlineReleaseRequest077("POST", tc.path, tc.body))
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "deferred") {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	q := job.Query
	if _, err := a.startSatlineJob(q, "resolve", 1000, ""); err == nil {
		t.Fatal("direct start bypassed deferral")
	}
	if got := a.runSatlineLocal(context.Background(), q, "resolve", 1000, nil); got.State != "MODULE_DISABLED" || !strings.Contains(got.Note, "deferred") {
		t.Fatal(got)
	}
	if got, err := a.reverseSatlineSatpointContext(context.Background(), "0.0.0.1"); err == nil || got.State != "unavailable" {
		t.Fatal(got, err)
	}
	if got := a.resolveSatlineSatPersistent(firstSatAtHeight(1), 1000, true); got.State != "MODULE_DISABLED" {
		t.Fatal(got)
	}
	if got := a.followSatlinePersistent("0.0.0.1", 1000, true); got.State != "MODULE_DISABLED" {
		t.Fatal(got)
	}
	if _, have := a.cachedSatlineResult(satlineRecord{VerifierVersion: blockVerifierVersion, Result: satlineResult{State: "INVALID"}}, 1000); have {
		t.Fatal("legacy cached traversal bypassed release gate")
	}
	for _, args := range [][]string{{"satline", "resolve", "5000000000"}, {"satline", "follow", "0.0.0.1"}, {"satline", "start", "5000000000"}, {"satline", "next", "5000000000"}, {"satline", "recheck", "5000000000"}, {"bitcoin", "satline", "resolve", "5000000000"}} {
		if handled, code := runCLI(a, args); !handled || code == 0 {
			t.Fatal("CLI trace bypassed deferral", args, handled, code)
		}
	}
	st := a.satlineStatus()
	if st.Enabled || st.Ready || st.State != "deferred" || !strings.Contains(st.Note, "Sats indexing") {
		t.Fatal(st)
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != string(before) {
		t.Fatal("deferral rewrote saved work", err)
	}
	if a.satlineActiveJob != "" || len(a.satlineJobs) != 1 {
		t.Fatal("blocked requests created work")
	}
	if _, err := os.Stat(a.satlineMetaPath()); !os.IsNotExist(err) {
		t.Fatal("blocked request initialized trace storage", err)
	}
	w := httptest.NewRecorder()
	a.handleSatView077(w, satlineReleaseRequest077("GET", "/api/v1/sats/view?sat=5000000000", ""))
	var view satViewResult077
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.State != "ready" || view.Location == nil || view.Location.Output != "0.0.1" || view.Snapshot.BlockHash != b1.Hash || view.Snapshot.Commitment != s.checkpoint.Commitment {
		t.Fatal("trace deferral disabled saved Sat location", w.Code, w.Body.String())
	}
	ranges, err := a.satOutputContext077(context.Background(), "0.0.1", 10, "")
	if err != nil || ranges.State != "ready" || len(ranges.Ranges) != 1 || ranges.Ranges[0].Start != firstSatAtHeight(1) {
		t.Fatal(ranges, err)
	}
}

func Test077SatlineDeferredReleaseStillCancelsOnlyRequestedJob(t *testing.T) {
	a := &app{dataDir: t.TempDir(), satlineJobs: map[string]*satlineJob{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	otherCtx, otherCancel := context.WithCancel(context.Background())
	defer otherCancel()
	a.satlineJobs["active"] = &satlineJob{ID: "active", Query: satlineQuery{Kind: "sat", Input: "1"}, Status: "running", cancel: cancel}
	a.satlineJobs["other"] = &satlineJob{ID: "other", Query: satlineQuery{Kind: "sat", Input: "2"}, Status: "running", cancel: otherCancel}
	w := httptest.NewRecorder()
	a.handleSatlineCancel(w, satlineReleaseRequest077("POST", "/api/v1/satline/ui/cancel", `{"id":"active"}`))
	if w.Code != 200 || ctx.Err() != context.Canceled || otherCtx.Err() != nil || a.satlineJobs["active"].Status != "cancelled" || a.satlineJobs["other"].Status != "running" {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "satline", "jobs", "active.json")); err != nil {
		t.Fatal("cancel intent was not durable", err)
	}
	w = httptest.NewRecorder()
	a.handleSatlineCancel(w, satlineReleaseRequest077("POST", "/api/v1/satline/ui/cancel", `{"id":"missing"}`))
	if w.Code != 404 {
		t.Fatal("unknown cancellation falsely acknowledged", w.Code)
	}
	remote := httptest.NewRecorder()
	a.handleSatlineCancel(remote, httptest.NewRequest("POST", "http://untrusted.invalid/api/v1/satline/ui/cancel", strings.NewReader(`{"id":"other"}`)))
	if remote.Code != 403 || otherCtx.Err() != nil {
		t.Fatal("remote cancel affected local work")
	}
}

func Test077SatlineDeferralKeepsSavedInscriptionIdentityAndSkipsHistoricalFetch(t *testing.T) {
	ctx := context.Background()
	a, _, block := retainedSatFixture(t, 3)
	tx := testTx(testID(77921), []inputView{{PrevTxID: testID(77920), Witness: ordWitness050(inscriptionTestScript(nil, []byte("deferred identity")))}}, 1)
	reveal := block(2, testCoinbase(testID(77922), subsidyAtHeight(2)), tx)
	x, err := extractInscriptionOccurrences(reveal)
	if err != nil || len(x.Occurrences) != 1 {
		t.Fatal(x, err)
	}
	savePreOriginInscriptionFixture(t, a.dataDir, reveal, &x, "pruned")
	settings, rawCalls, stop := satline077RPC(t, map[int64]string{0: block(0).Hash, 1: block(1).Hash, 2: reveal.Hash})
	defer stop()
	a.settings = settings
	a.settings.NetworkDisabled = true
	id := tx.TxID + "i0"
	unknown, err := a.discoverInscriptionSat(ctx, id)
	if err != nil || unknown.Known || unknown.SatNumber != nil || unknown.State != "unknown" || !strings.Contains(unknown.Note, "deferred") || rawCalls.Load() != 0 {
		t.Fatal("identity fallback started historical work", unknown, err, rawCalls.Load())
	}
	number := firstSatAtHeight(1)
	saved := inscriptionSatResult{ID: id, Known: true, SatNumber: &number, State: "known", Profile: inscriptionSatProfile, RevealHeight: 2, RevealHash: reveal.Hash}
	if err = atomicWriteJSON(a.inscriptionSatPath(id), saved); err != nil {
		t.Fatal(err)
	}
	known, err := a.discoverInscriptionSat(ctx, id)
	if err != nil || !known.Known || known.SatNumber == nil || *known.SatNumber != number || rawCalls.Load() != 0 {
		t.Fatal("deferral discarded saved identity", known, err, rawCalls.Load())
	}
}
