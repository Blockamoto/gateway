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
	"time"
)

func Test075UTXOPlanningIsGenesisOnlyAndPruned(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	p, err := a.planIndexBuild(indexBuildRequest{Index: "utxo-state"})
	if err != nil || p.From != 0 || p.Retention != "pruned" || !p.Sequential || p.Definition.Locked {
		t.Fatalf("UTXO plan: %+v %v", p, err)
	}
	if !strings.Contains(p.Verification, "consensus_validated=false") {
		t.Fatal("plan implies validation", p.Verification)
	}
	one := int64(1)
	for _, request := range []indexBuildRequest{{Index: "utxo-state", From: &one}, {Index: "utxo-state", Mode: "full"}, {Index: "blocks", Retention: "pruned"}, {Index: "utxo-state", RetainOutputValues: boolPointer(true)}, {Index: "utxo-state", Outputs: []string{"txo-spender"}}} {
		if _, err := a.planIndexBuild(request); err == nil {
			t.Fatal("invalid UTXO/history request accepted", request)
		}
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", "utxo-state", "head.json")); !os.IsNotExist(err) {
		t.Fatal("planning wrote a checkpoint", err)
	}
}

func Test075UTXOJobProcessesLocalBodiesAndPreservesRawPolicy(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	raw, hash := installBlockOneHeader063(t, a)
	retained := filepath.Join(a.dataDir, "indexes", "sources", hash+".block")
	if err := os.MkdirAll(filepath.Dir(retained), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(retained, raw, 0600); err != nil {
		t.Fatal(err)
	}
	zero, one := int64(0), int64(1)
	job, err := a.startIndexBuild(indexBuildRequest{Index: "utxo-state", From: &zero, To: &one})
	if err != nil || job.Retention != "pruned" {
		t.Fatal(job, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if job, err = a.waitQueuedIndexJob(ctx, job.ID); err != nil || job.State != "complete" || job.Height != 1 {
		t.Fatal(job, err)
	}
	view := a.indexStatus().UTXO
	if view == nil || view.State != "current" || view.ProcessedHeight == nil || *view.ProcessedHeight != 1 || view.LiveOutputCount == nil || *view.LiveOutputCount != 1 || view.ConsensusValidated || view.RecoveryWindow != 288 {
		t.Fatalf("UTXO status: %+v", view)
	}
	if body, err := os.ReadFile(retained); err != nil || string(body) != string(raw) {
		t.Fatal("pruned build changed preexisting retained source", err)
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "blocks", "raw", "1-"+hash+".block")); !os.IsNotExist(err) {
		t.Fatal("pruned build created raw cache", err)
	}
	// Body availability is independent of durable Coin lookup.
	if err := os.Remove(retained); err != nil {
		t.Fatal(err)
	}
	result, err := a.lookupUTXOIndex(ctx, "0.0.1")
	if err != nil {
		t.Fatal("offline coin lookup", err)
	}
	encoded, _ := json.Marshal(result)
	if !strings.Contains(string(encoded), `"found":true`) || !strings.Contains(string(encoded), `"consensus_validated":false`) {
		t.Fatal(string(encoded))
	}
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex()}
	if got := restarted.indexUTXOStatus(nil); got.ProcessedHeight == nil || *got.ProcessedHeight != 1 || got.State != "current" {
		t.Fatal("restart lost state", got)
	}
}

func Test075UTXOPauseMissingBodyAndResumeCheckpoint(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	raw, hash := installBlockOneHeader063(t, a)
	one := int64(1)
	job, err := a.startIndexBuild(indexBuildRequest{Index: "utxo-state", To: &one})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		j := a.indexJobSnapshot()
		if j.State == "waiting" && j.Height == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("did not checkpoint before waiting", j)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := a.controlIndexQueue(indexQueueRequest{ID: job.ID, Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if paused, err := a.waitQueuedIndexJob(ctx, job.ID); err != nil || paused.State != "paused" || paused.Height != 0 {
		t.Fatal(paused, err)
	}
	waitLiveWriterReleased063(t, a)
	retained := filepath.Join(a.dataDir, "indexes", "sources", hash+".block")
	if err := os.MkdirAll(filepath.Dir(retained), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(retained, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.controlIndexQueue(indexQueueRequest{ID: job.ID, Action: "resume"}); err != nil {
		t.Fatal(err)
	}
	if done, err := a.waitQueuedIndexJob(ctx, job.ID); err != nil || done.State != "complete" || done.Height != 1 {
		t.Fatal(done, err)
	}
}

func Test075TXIOHistoryOptionsPersistIndependently(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	view, err := a.setIndexLivePolicy(indexLiveRequest{Index: "txo-spender", Action: "start", RetainOutputValues: boolPointer(true), RetainOutputScripts: boolPointer(false)})
	if err != nil || !view.RetainOutputValues || view.RetainOutputScripts {
		t.Fatal(view, err)
	}
	plan, err := a.planIndexBuild(indexBuildRequest{Index: "txo-spender"})
	if err != nil || !plan.RetainOutputValues || plan.RetainOutputScripts {
		t.Fatal("omitted options did not preserve settings", plan, err)
	}
	plan, err = a.planIndexBuild(indexBuildRequest{Index: "txo-spender", RetainOutputValues: boolPointer(false), RetainOutputScripts: boolPointer(true)})
	if err != nil || plan.RetainOutputValues || !plan.RetainOutputScripts {
		t.Fatal("explicit history options lost", plan, err)
	}
	original := indexBuildRequest{Index: "txo-spender", RetainOutputValues: boolPointer(true), RetainOutputScripts: boolPointer(true)}
	copy := cloneIndexBuildRequest(original)
	*copy.RetainOutputValues, *copy.RetainOutputScripts = false, false
	if !*original.RetainOutputValues || !*original.RetainOutputScripts {
		t.Fatal("queued options alias request")
	}
	if sameIndexWork(original, copy) {
		t.Fatal("different history policies deduplicated")
	}
}

func Test075UTXOHTTPPlanAndStatusAreReadOnly(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	handler := indexInterfaceHandler(a)
	for _, req := range []struct{ method, path, body string }{{"POST", "plan", `{"index":"utxo-state","retention":"pruned"}`}, {"GET", "status", ""}} {
		r := httptest.NewRequest(req.method, "http://127.0.0.1:9090/api/v1/index/"+req.path, strings.NewReader(req.body))
		r.RemoteAddr = "127.0.0.1:50000"
		r.Header.Set("Origin", "http://127.0.0.1:9090")
		if req.method == "POST" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatal(req.path, w.Code, w.Body.String())
		}
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", "utxo-state", "head.json")); !os.IsNotExist(err) {
		t.Fatal("GET/plan started indexing", err)
	}
}

func Test075UTXOReviewedRebuildPreservesPriorStateAndResumes(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	raw, hash := installBlockOneHeader063(t, a)
	head := filepath.Join(a.dataDir, "indexes", "utxo-state", "head.json")
	if err := os.MkdirAll(filepath.Dir(head), 0700); err != nil {
		t.Fatal(err)
	}
	prior := []byte("damaged previous state: retain for inspection")
	if err := os.WriteFile(head, prior, 0600); err != nil {
		t.Fatal(err)
	}
	one := int64(1)
	if _, err := a.planIndexBuild(indexBuildRequest{Index: "utxo-state", To: &one}); err == nil {
		t.Fatal("normal plan accepted damaged state")
	}
	plan, err := a.planIndexBuild(indexBuildRequest{Index: "utxo-state", To: &one, Rebuild: true})
	if err != nil || !plan.Rebuild || plan.Outputs[0].Recovery != "explicit_genesis_rebuild" {
		t.Fatal("explicit rebuild unavailable", plan, err)
	}
	if data, err := os.ReadFile(head); err != nil || string(data) != string(prior) {
		t.Fatal("review mutated prior state", err)
	}
	job, err := a.startIndexBuild(indexBuildRequest{Index: "utxo-state", To: &one, Rebuild: true})
	if err != nil || !job.Rebuild {
		t.Fatal(job, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		j := a.indexJobSnapshot()
		if j.State == "waiting" && j.Height == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rebuild did not save genesis before waiting", j)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err = a.controlIndexQueue(indexQueueRequest{ID: job.ID, Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if paused, err := a.waitQueuedIndexJob(ctx, job.ID); err != nil || paused.State != "paused" {
		t.Fatal(paused, err)
	}
	waitLiveWriterReleased063(t, a)
	retained := filepath.Join(a.dataDir, "indexes", "sources", hash+".block")
	if err = os.MkdirAll(filepath.Dir(retained), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(retained, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = a.controlIndexQueue(indexQueueRequest{ID: job.ID, Action: "resume"}); err != nil {
		t.Fatal(err)
	}
	if done, err := a.waitQueuedIndexJob(ctx, job.ID); err != nil || done.State != "complete" || done.Height != 1 {
		t.Fatal(done, err)
	}
	waitLiveWriterReleased063(t, a)
	archives, err := os.ReadDir(filepath.Join(a.dataDir, "indexes", "utxo-state-archives"))
	if err != nil || len(archives) != 1 {
		t.Fatal("resume archived rebuilt state again", archives, err)
	}
	archived := filepath.Join(a.dataDir, "indexes", "utxo-state-archives", archives[0].Name(), "head.json")
	if data, err := os.ReadFile(archived); err != nil || string(data) != string(prior) {
		t.Fatal("prior state was not retained", err)
	}
	if got := a.indexStatus().UTXO; got.State != "current" || got.ProcessedHeight == nil || *got.ProcessedHeight != 1 {
		t.Fatal("rebuilt state not current", got)
	}
}

func Test075UTXOExplicitStorageMeasurementIsRevisionBound(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	raw, hash := installBlockOneHeader063(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	build := func(to int64) {
		t.Helper()
		j, err := a.startIndexBuild(indexBuildRequest{Index: "utxo-state", To: &to})
		if err != nil {
			t.Fatal(err)
		}
		if done, err := a.waitQueuedIndexJob(ctx, j.ID); err != nil || done.State != "complete" {
			t.Fatal(done, err)
		}
		waitLiveWriterReleased063(t, a)
	}
	build(0)
	if a.indexStatus().UTXO.StorageBytes != nil {
		t.Fatal("status invented an unmeasured storage size")
	}
	measurement, err := a.inspectUTXOStorage(ctx)
	if err != nil || measurement.State != "complete" || measurement.TotalBytes <= 0 {
		t.Fatal(measurement, err)
	}
	if got := a.indexStatus().UTXO; got.StorageBytes == nil || *got.StorageBytes != measurement.TotalBytes || got.StorageRevision != measurement.Revision || got.StorageMeasuredAt == "" {
		t.Fatal("completed measurement not exposed", got)
	}
	retained := filepath.Join(a.dataDir, "indexes", "sources", hash+".block")
	if err = os.MkdirAll(filepath.Dir(retained), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(retained, raw, 0600); err != nil {
		t.Fatal(err)
	}
	build(1)
	if a.indexStatus().UTXO.StorageBytes != nil {
		t.Fatal("measurement from previous state revision was reused")
	}
	measurement, err = a.inspectUTXOStorage(ctx)
	if err != nil || measurement.State != "complete" || measurement.Bytes["shared_output_fact_pages"] <= 0 || measurement.Bytes["engine_files"] <= 0 {
		t.Fatal("live coin storage not measured", measurement, err)
	}
	// A canceled explicit inspection must not replace the complete cached result.
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err = a.inspectUTXOStorage(canceled); err == nil {
		t.Fatal("cancellation was ignored")
	}
	if got := a.indexStatus().UTXO; got.StorageBytes == nil || *got.StorageBytes != measurement.TotalBytes {
		t.Fatal("canceled measurement replaced completed result", got)
	}
}
