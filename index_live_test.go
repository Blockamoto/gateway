package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func buildGenesisBlockIndex066(t *testing.T, a *app) indexCheckpoint {
	t.Helper()
	zero := int64(0)
	armLegacyBuildTracks(t, a, indexBuildRequest{Index: "blocks"})
	if _, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &zero, To: &zero, Mode: "lean", Retention: "ephemeral"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	job, err := a.waitIndexBuild(ctx)
	if err != nil || job.State != "complete" || job.Height != 0 {
		t.Fatalf("genesis build: %+v %v", job, err)
	}
	s, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || s.checkpoint == nil {
		t.Fatalf("missing genesis checkpoint: %v", err)
	}
	return *s.checkpoint
}

func startTimelineRawLive063(t *testing.T, a *app, mode string) indexRunDocument {
	t.Helper()
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: "blocks", Armed: true, Live: true, indexRunOptions: indexRunOptions{Mode: mode}}}}
	if _, err := a.prepareIndexRun(req); err != nil {
		t.Fatal(err)
	}
	d, err := a.startIndexRun(context.Background(), req, "")
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func waitTimelineRawHeight063(t *testing.T, a *app, height int64) indexRunDocument {
	t.Helper()
	hash, err := a.canonicalHashAtHeight(height)
	if err != nil {
		t.Fatal(err)
	}
	return waitRunCondition(t, a, func(d indexRunDocument) bool {
		return d.Run != nil && len(d.Run.Consumers) == 1 && d.Run.Consumers[0].Height == height && d.Run.Consumers[0].BlockHash == hash
	})
}

func installBlockOneHeader063(t *testing.T, a *app) ([]byte, string) {
	t.Helper()
	raw := mainnetBlockOne(t)
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.Write(raw[:80])
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append header: %v %v", writeErr, closeErr)
	}
	h := hash256(raw[:80])
	hash := reverseHex(h[:])
	a.setStatus(func(st *appStatus) {
		st.HeaderCount = 2
		st.HeaderHeight = 1
		st.TipHash = hash
		st.Ready = true
		st.Syncing = false
		st.HeaderState = "current"
		st.Error = ""
	})
	return raw, hash
}

func prepareLiveTestApp063(t *testing.T) *app {
	t.Helper()
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = false
	a.settings.HeadersPaused = false
	a.network = newBitcoinNetwork(a)
	a.network.noBootstrap = true
	a.network.allowPrivate = true
	return a
}

func Test064LiveExplicitlyStartsFreshHistoryAndPersistsPolicy(t *testing.T) {
	a := prepareLiveTestApp063(t)
	view, err := a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "enable", Retention: "ephemeral"})
	if err != nil || !view.On || !view.Enabled || view.Paused || view.Retention != "ephemeral" || view.CheckpointHeight != -1 {
		t.Fatalf("enable: %+v %v", view, err)
	}
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex()}
	policies, err := restarted.indexLivePolicies()
	if err != nil || !policies["blocks"].Enabled || policies["blocks"].Retention != "ephemeral" {
		t.Fatalf("live policy did not survive restart: %+v %v", policies, err)
	}
	if restarted.indexLiveView("blocks", policies["blocks"]).On != true {
		t.Fatal("fresh explicit On intent did not survive restart")
	}
	if _, err = restarted.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	paused, err := restarted.indexLivePolicies()
	if err != nil || !paused["blocks"].Paused {
		t.Fatalf("pause not durable: %+v %v", paused, err)
	}
	if _, err = restarted.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "resume"}); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "disable"}); err != nil {
		t.Fatal(err)
	}
	disabled, _ := restarted.indexLivePolicies()
	if disabled["blocks"].Enabled || disabled["blocks"].Paused {
		t.Fatalf("disable did not persist: %+v", disabled["blocks"])
	}
}

func Test063LiveCatchupUsesRawArmAndDoesNotExpandPublication(t *testing.T) {
	a := prepareLiveTestApp063(t)
	buildGenesisBlockIndex066(t, a)
	raw, blockOneHash := installBlockOneHeader063(t, a)
	peer := indexBitcoinSource(t, raw)
	hashCoordinateConnect(t, a, peer)
	view, err := a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "enable", Retention: "ephemeral"})
	if err != nil || !view.Enabled {
		t.Fatalf("enable: %+v %v", view, err)
	}
	a.liveIndexTick() // Configured timeline owns new work; the legacy tick yields.
	startTimelineRawLive063(t, a, "lean")
	waitTimelineRawHeight063(t, a, 1)
	s, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || s.checkpoint == nil || s.checkpoint.Height != 1 || s.checkpoint.BlockHash != blockOneHash || s.head.Retention != "retain" {
		t.Fatalf("live head: %+v %+v %v", s.head, s.checkpoint, err)
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "blocks", "raw", "1-"+blockOneHash+".block")); !os.IsNotExist(err) {
		t.Fatalf("ephemeral Live accumulated raw cache: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", "sources", blockOneHash+".block")); !os.IsNotExist(err) {
		t.Fatalf("ephemeral Live retained source block: %v", err)
	}
	if len(a.publishedIndexManifests()) != 0 {
		t.Fatal("Live published private data")
	}
	current := a.indexLiveView("blocks", indexLivePolicy{Index: "blocks", Enabled: true, Retention: "ephemeral"})
	if current.State != "synced" || !current.LagKnown || current.Lag != 0 || !current.TipFresh {
		t.Fatalf("fresh live state: %+v", current)
	}
	a.settings.NetworkDisabled = true
	offline := a.indexLiveView("blocks", indexLivePolicy{Index: "blocks", Enabled: true, Retention: "ephemeral"})
	if offline.State != "offline_unknown" || offline.LagKnown || offline.TipFresh {
		t.Fatalf("stale local arithmetic was mislabeled current: %+v", offline)
	}
}

func Test063LiveRepairsSelectedChainReorgBeforeExtending(t *testing.T) {
	a := prepareLiveTestApp063(t)
	initial := buildGenesisBlockIndex066(t, a)
	raw, blockOneHash := installBlockOneHeader063(t, a)
	peer := indexBitcoinSource(t, raw)
	hashCoordinateConnect(t, a, peer)

	s, err := openIndexStore(a.dataDir, "blocks")
	if err != nil {
		t.Fatal(err)
	}
	stale := inscriptionTestBlock()
	stale.Height = 1
	stale.Hash = strings.Repeat("f", 64)
	stale.PreviousBlockHash = genesisHashDisplay
	// A deliberately disconnected synthetic receipt tests reconciliation.
	// It cannot be serialized as a valid raw Bitcoin block.
	ids := make([]string, len(stale.Transactions))
	for i, tx := range stale.Transactions {
		ids[i] = tx.TxID
	}
	if err = s.commitPreparedIndexBatch(indexBatch{PreviousBlockHash: stale.PreviousBlockHash, Bitcoin: &bitcoinIndexBlock{VerifierVersion: blockVerifierVersion, TxIDs: ids, Transactions: stale.Transactions}}, stale.Height, stale.Hash, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	staleCommit := s.checkpoint.Commitment
	if state := a.indexChainState(s.checkpoint); state != "stale_reorg_resume_required" {
		t.Fatalf("stale checkpoint not detected: %s", state)
	}
	if _, err = a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "enable", Retention: "ephemeral"}); err != nil {
		t.Fatal(err)
	}
	a.liveIndexTick()
	startTimelineRawLive063(t, a, "lean")
	waitTimelineRawHeight063(t, a, 1)
	repaired, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || repaired.checkpoint == nil || repaired.checkpoint.BlockHash != blockOneHash || repaired.checkpoint.PreviousCommitment != initial.Commitment {
		t.Fatalf("wrong repaired head: %+v %v", repaired.checkpoint, err)
	}
	if repaired.checkpoint.Commitment == staleCommit {
		t.Fatal("stale checkpoint remained selected")
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", "blocks", "commits", staleCommit+".json")); err != nil {
		t.Fatal("immutable stale audit batch was destroyed instead of deselected")
	}
}

func Test063PausingLiveJobPausesPolicy(t *testing.T) {
	a := prepareLiveTestApp063(t)
	buildGenesisBlockIndex066(t, a)
	if _, err := a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "enable"}); err != nil {
		t.Fatal(err)
	}
	cancelled := false
	a.indexMu.Lock()
	a.indexJob = indexJob{ID: "live-fixture", Index: "blocks", Live: true, State: "running"}
	a.indexCancel = func() { cancelled = true }
	a.indexMu.Unlock()
	// This cancellation fixture has no goroutine to clear its synthetic writer.
	t.Cleanup(func() { a.indexMu.Lock(); a.indexCancel = nil; a.indexMu.Unlock() })
	job, err := a.pauseIndexBuild()
	if err != nil || job.State != "pausing" || !cancelled {
		t.Fatalf("pause live job: %+v %v cancelled=%v", job, err, cancelled)
	}
	policies, err := a.indexLivePolicies()
	if err != nil || !policies["blocks"].Paused {
		t.Fatalf("supervisor could immediately undo generic pause: %+v %v", policies, err)
	}
}

// Generic Pause still stops future maintenance when a live batch has just
// finished and the writer is between runs.
func Test063IdleLivePauseIsDurable(t *testing.T) {
	a := prepareLiveTestApp063(t)
	buildGenesisBlockIndex066(t, a)
	if _, err := a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "enable"}); err != nil {
		t.Fatal(err)
	}
	j := indexJob{ID: "idle-live", Index: "blocks", Live: true, State: "complete", Height: 0}
	if err := a.saveIndexJob(j); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pauseIndexBuild(); err != nil {
		t.Fatal(err)
	}
	p, err := a.indexLivePolicies()
	if err != nil || !p["blocks"].Paused {
		t.Fatalf("idle pause lost: %+v %v", p, err)
	}
	a.liveIndexTick()
	if got := a.indexJobSnapshot(); got.ID != j.ID {
		t.Fatal("paused live job restarted")
	}
}

func Test063FailedLiveJobStatusIsNotCatchingUp(t *testing.T) {
	a := prepareLiveTestApp063(t)
	buildGenesisBlockIndex066(t, a)
	if err := a.saveIndexJob(indexJob{ID: "failed-live", Index: "blocks", Live: true, State: "failed", Error: "fixture disk failure"}); err != nil {
		t.Fatal(err)
	}
	v := a.indexLiveView("blocks", indexLivePolicy{Index: "blocks", Enabled: true, Retention: "ephemeral"})
	if v.State != "error" || v.Error != "fixture disk failure" {
		t.Fatalf("failure hidden: %+v", v)
	}
}
