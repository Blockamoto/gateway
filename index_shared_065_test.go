package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func Test065QueuedRangesMergeAndFailedHeadDoesNotStrandCLI(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	a.indexJob = indexJob{ID: "held", Index: "bitmap", State: "running"}
	a.indexCancel = func() {}
	zero, ten, five, fifteen, sixteen, twenty := int64(0), int64(10), int64(5), int64(15), int64(16), int64(20)
	first, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &zero, To: &ten})
	if err != nil {
		t.Fatal(err)
	}
	overlap, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &five, To: &fifteen})
	if err != nil || overlap.ID != first.ID || overlap.To != 15 {
		t.Fatal("overlap", overlap, err)
	}
	adjacent, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &sixteen, To: &twenty})
	if err != nil || adjacent.ID != first.ID || adjacent.From != 0 || adjacent.To != 20 {
		t.Fatal("adjacency", adjacent, err)
	}
	second, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &zero, To: &zero, Retention: "retain"})
	if err != nil {
		t.Fatal(err)
	}
	a.indexMu.Lock()
	// Model a damaged saved first request while the second remains valid.
	a.indexQueue[0].Request.Mode = "invalid"
	a.indexCancel = nil
	a.indexMu.Unlock()
	// No supervisor is running, as in local CLI startup. Draining must skip
	// the invalid saved head and start the next already approved request itself.
	a.indexLiveControlMu.Lock()
	a.drainIndexQueueLocked()
	a.indexLiveControlMu.Unlock()
	if done := waitIndexJob065(t, a, second.ID); done.State != "complete" {
		t.Fatal(done)
	}
	if failed := waitIndexJob065(t, a, first.ID); failed.State != "failed" {
		t.Fatal(failed)
	}
}

func Test065QueueResumeRetiresPersistentPauseMarker(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	zero := int64(0)
	req := indexBuildRequest{Index: "blocks", From: &zero, To: &zero, Mode: "lean", Retention: "ephemeral"}
	j := indexJob{ID: "paused-fixture", Index: "blocks", State: "paused", From: 0, To: 0, Height: -1}
	a.indexMu.Lock()
	a.indexQueueLoaded = true
	a.indexQueue = []indexQueueEntry{{Request: req, Job: j}}
	err := a.persistIndexQueueLocked()
	a.indexMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = atomicWriteJSON(filepath.Join(a.dataDir, "indexes", "pause.json"), struct {
		ID string `json:"id"`
	}{j.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.controlIndexQueue(indexQueueRequest{j.ID, "resume"}); err != nil {
		t.Fatal(err)
	}
	if done := waitIndexJob065(t, a, j.ID); done.State != "complete" {
		t.Fatal(done)
	}
	if a.indexPauseRequested(j.ID) {
		t.Fatal("resume left a stale pause marker")
	}
}

func Test065SpenderDurableLookupAndReorgNeverInventUnspent(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	s, err := openIndexStore(a.dataDir, "txo-spender")
	if err != nil {
		t.Fatal(err)
	}
	prev := strings.Repeat("a", 64)
	block := inscriptionTestBlock(transactionView{TxID: strings.Repeat("b", 64), Inputs: []inputView{{PrevTxID: prev, PrevVout: 2}}})
	// Synthetic headers isolate store/locator/reorg behavior, not consensus.
	var header [80]byte
	binary.LittleEndian.PutUint32(header[76:], 65)
	digest := hash256(header[:])
	block.Height, block.Hash = 1, reverseHex(digest[:])
	f, err := os.OpenFile(a.headersPath, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(header[:], 80)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	a.setStatus(func(st *appStatus) { st.HeaderHeight = 1; st.HeaderCount = 2 })
	if err = s.appendBlock(block, 1, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus()}
	got, err := restarted.lookupIndexSpender(context.Background(), strings.ToUpper(prev)+":2")
	if err != nil || len(got.Rows) != 1 || got.Rows[0].TxID != strings.Repeat("b", 64) {
		t.Fatal(got, err)
	}
	miss, err := restarted.lookupIndexSpender(context.Background(), prev+":3")
	if err != nil || len(miss.Rows) != 0 || miss.Absence == "" {
		t.Fatal(miss, err)
	}
	if err = s.reconcile(func(int64) (string, error) { return strings.Repeat("c", 64), nil }); err != nil {
		t.Fatal(err)
	}
	got, err = restarted.lookupIndexSpender(context.Background(), prev+":2")
	if err != nil || len(got.Rows) != 0 || got.Checkpoint != nil {
		t.Fatal("orphan pointer escaped rewind", got, err)
	}
}

func Test065VerifierProvenanceAndHistoryChoice(t *testing.T) {
	if reusableBitcoinEvidence(&bitcoinIndexBlock{VerifierVersion: 999}) {
		t.Fatal("relabelled evidence from a different verifier")
	}
	t.Skip("Public Sat history builds and scheduling are intentionally locked in 0.6.6; raw history algorithms remain covered.")
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	zero := int64(0)
	j, err := a.startIndexBuild(indexBuildRequest{Index: "sat-state", From: &zero, To: &zero, RetainSatHistory: true, SatHistoryConfigured: true})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitIndexJob065(t, a, j.ID); done.State != "complete" {
		t.Fatal(done)
	}
	plan, err := a.planIndexBuild(indexBuildRequest{Index: "sat-state", From: &zero, To: &zero})
	if err != nil || !plan.RetainSatHistory {
		t.Fatal("omission erased saved history choice", plan, err)
	}
	plan, err = a.planIndexBuild(indexBuildRequest{Index: "sat-state", From: &zero, To: &zero, SatHistoryConfigured: true})
	if err != nil || plan.RetainSatHistory {
		t.Fatal("explicit off ignored", plan, err)
	}
	for _, id := range []string{"sat-state", "blocks"} {
		view := a.indexLiveView(id, indexLivePolicy{Index: id})
		if !view.RetainSatHistory {
			t.Fatal("unconfigured view lost retained history preference", id)
		}
		view = a.indexLiveView(id, indexLivePolicy{Index: id, SatHistoryConfigured: true})
		encoded, err := json.Marshal(view)
		if err != nil || view.RetainSatHistory || !strings.Contains(string(encoded), `"retain_sat_history":false`) {
			t.Fatal("explicit off was absent from status and could restore stale history", id, string(encoded), err)
		}
	}
	waitLiveWriterReleased063(t, a)
	j, err = a.startIndexBuildInternal(indexBuildRequest{Index: "sat-state", From: &zero, To: &zero}, true)
	if err != nil || !j.RetainSatHistory {
		t.Fatal("scheduler discarded resolved history preference", j, err)
	}
	if done := waitIndexJob065(t, a, j.ID); done.State != "complete" {
		t.Fatal(done)
	}
	waitLiveWriterReleased063(t, a)
}

func waitIndexJob065(t *testing.T, a *app, id string) indexJob {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		jobs, err := a.indexJobsSnapshot()
		if err != nil {
			t.Fatal(err)
		}
		for _, j := range jobs {
			if j.ID == id && (j.State == "complete" || j.State == "failed" || j.State == "paused") {
				return j
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not finish", id)
	return indexJob{}
}

func Test065SharedOutputsCommitAndReuseWithoutRawRetention(t *testing.T) {
	t.Skip("Public multi-output builds require derived indexes, intentionally locked in 0.6.6; planning algorithms and saved shared-job rejection remain covered.")
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	zero := int64(0)
	j, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &zero, To: &zero, Outputs: []string{"sat-state", "inscriptions", "tx-locator", "txo-spender"}, Retention: "ephemeral"})
	if err != nil {
		t.Fatal(err)
	}
	done := waitIndexJob065(t, a, j.ID)
	if done.State != "complete" || len(done.Progress) != 5 {
		t.Fatalf("combined result %+v", done)
	}
	for _, id := range []string{"blocks", "sat-state", "inscriptions", "tx-locator", "txo-spender"} {
		s, err := openIndexStore(a.dataDir, id)
		if err != nil || s.checkpoint == nil || s.checkpoint.Height != 0 {
			t.Fatalf("%s coverage %+v %v", id, s, err)
		}
	}
	if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", "sources", genesisHashDisplay+".block")); !os.IsNotExist(err) {
		t.Fatal("ephemeral combined build retained raw source", err)
	}
	// Remove only this fixture's preloaded raw source. A new derivation can use
	// the committed decoded block without network access or retained raw bytes.
	if err = os.Remove(filepath.Join(a.dataDir, "blocks", "raw", "0-"+genesisHashDisplay+".block")); err != nil {
		t.Fatal(err)
	}
	a.cacheIndex = newCacheIndex()
	target, err := a.localBlockTarget(0)
	if err != nil {
		t.Fatal(err)
	}
	block, err := a.indexSourceBlock(context.Background(), target, "ephemeral")
	if err != nil || block.SourceNetwork != "local_index" || !integrityVerified(block) {
		t.Fatalf("local evidence reuse %+v %v", block, err)
	}
	plan, err := a.planIndexBuild(indexBuildRequest{Index: "blocks", From: &zero, To: &zero, Outputs: []string{"sat-state", "inscriptions", "tx-locator", "txo-spender"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range plan.Outputs {
		if len(o.Reuse) != 1 || o.Reuse[0] != (heightInterval{0, 0}) || len(o.Add) != 0 {
			t.Fatalf("bad reuse %+v", o)
		}
	}
}

func Test065SharedOutputFailurePreservesSiblingCommits(t *testing.T) {
	t.Skip("Public multi-output builds require derived indexes, intentionally locked in 0.6.6; planning algorithms and saved shared-job rejection remain covered.")
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	broken := filepath.Join(a.dataDir, "indexes", "txo-spender")
	if err := os.MkdirAll(broken, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "commits"), []byte("deliberate fixture write failure"), 0600); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	j, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &zero, To: &zero, Outputs: []string{"txo-spender", "tx-locator"}})
	if err != nil {
		t.Fatal(err)
	}
	done := waitIndexJob065(t, a, j.ID)
	if done.State != "failed" {
		t.Fatalf("failure was hidden %+v", done)
	}
	for _, id := range []string{"blocks", "tx-locator"} {
		s, e := indexStoreHead(a.dataDir, id)
		if e != nil || s.checkpoint == nil || s.checkpoint.Height != 0 {
			t.Fatalf("sibling lost %s %v", id, e)
		}
	}
	s, err := indexStoreHead(a.dataDir, "txo-spender")
	if err != nil || s.checkpoint != nil {
		t.Fatal("failed output advertised coverage", s, err)
	}
}

func Test065CombinedHistoricalPrerequisitesAndMissingDerivations(t *testing.T) {
	t.Skip("Public combined derivation planning requires locked indexes in 0.6.6; rejection and retained recipe algorithms remain covered.")
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	recent := int64(850000)
	if _, err := a.planIndexBuild(indexBuildRequest{Index: "blocks", From: &recent, To: &recent, Outputs: []string{"sat-state"}}); err == nil {
		t.Fatal("isolated range invented sat state")
	}
	if _, err := a.planIndexBuild(indexBuildRequest{Index: "blocks", From: &recent, To: &recent, Outputs: []string{"bitmap"}}); err == nil {
		t.Fatal("isolated range invented Bitmap history")
	}
	zero := int64(0)
	s, err := openIndexStore(a.dataDir, "blocks")
	if err != nil {
		t.Fatal(err)
	}
	target, _ := a.localBlockTarget(0)
	b, err := a.fetchBlockWithPolicy(target, "ephemeral")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.appendBlock(b, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	p, err := a.planIndexBuild(indexBuildRequest{Index: "blocks", From: &zero, To: &zero, Outputs: []string{"inscriptions"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Outputs) != 2 || len(p.Outputs[0].Reuse) != 1 || len(p.Outputs[1].Reuse) != 0 || len(p.Outputs[1].Add) != 1 {
		t.Fatalf("raw/derived coverage conflated %+v", p.Outputs)
	}
}

func Test065QueueDurabilityOrderingDeduplicationAndCardPause(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	a.indexJob = indexJob{ID: "held", Index: "bitmap", State: "running"}
	cancelled := false
	a.indexCancel = func() { cancelled = true }
	t.Cleanup(func() { a.indexMu.Lock(); a.indexCancel = nil; a.indexMu.Unlock() })
	zero := int64(0)
	first, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &zero, To: &zero})
	if err != nil || first.State != "queued" {
		t.Fatal(first, err)
	}
	duplicate, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &zero, To: &zero})
	if err != nil || duplicate.ID != first.ID {
		t.Fatal("overlap duplicated", duplicate, err)
	}
	second, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &zero, To: &zero, Retention: "retain"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.pauseIndexBuildFor("inscriptions"); err == nil || cancelled {
		t.Fatal("card paused another index", err, cancelled)
	}
	if _, err = a.controlIndexQueue(indexQueueRequest{second.ID, "up"}); err != nil {
		t.Fatal(err)
	}
	jobs, err := a.indexJobsSnapshot()
	if err != nil || jobs[0].ID != second.ID || jobs[0].QueuePosition != 1 || jobs[1].QueuePosition != 2 {
		t.Fatal("ordering", jobs, err)
	}
	if _, err = a.controlIndexQueue(indexQueueRequest{first.ID, "pause"}); err != nil {
		t.Fatal(err)
	}
	a.indexMu.Lock()
	a.indexCancel = nil
	a.indexMu.Unlock() // release the synthetic fixture writer
	// A restarted scheduler retains the order and explicit pause, and runs
	// only the already approved queued request. No active process is restarted.
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: a.cacheIndex}
	restarted.liveIndexTick()
	if done := waitIndexJob065(t, restarted, second.ID); done.State != "complete" {
		t.Fatal(done)
	}
	jobs, err = restarted.indexJobsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.ID == first.ID && j.State != "paused" {
			t.Fatal("restart resumed paused job", j)
		}
	}
	if _, err = restarted.controlIndexQueue(indexQueueRequest{first.ID, "resume"}); err != nil {
		t.Fatal(err)
	}
	if done := waitIndexJob065(t, restarted, first.ID); done.State != "complete" {
		t.Fatal(done)
	}
}
