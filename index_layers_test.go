package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func Test075TXIOEnrichmentUsesOnlyMissingLayerRanges(t *testing.T) {
	s := &indexStore{head: indexHead{TXIOLayersSchema: 1, TXIOLayersComplete: true, TXIOLayers: map[string][]heightInterval{
		"txio-values":  {{0, 99}, {150, 200}},
		"txio-scripts": {{0, 149}},
	}}}
	if got := s.txioEnrichmentRanges(0, 200, true, true); !reflect.DeepEqual(got, []heightInterval{{100, 200}}) {
		t.Fatal("incorrect missing layer union", got)
	}
	if got := s.txioEnrichmentRanges(0, 99, true, true); len(got) != 0 {
		t.Fatal("completed prefix would be scanned again", got)
	}
	if got := s.txioEnrichmentRanges(50, 180, true, false); !reflect.DeepEqual(got, []heightInterval{{100, 149}}) {
		t.Fatal("single layer range was not bounded", got)
	}
	s.head.TXIOLayersComplete = false
	if got := s.txioEnrichmentRanges(50, 180, true, false); !reflect.DeepEqual(got, []heightInterval{{50, 180}}) {
		t.Fatal("legacy partial catalogue invented completeness", got)
	}
}

func Test075TXIOQueueMergesHistoryExtentsWithoutLosingEarlierSelection(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	a.indexJob = indexJob{ID: "held", Index: "bitmap", State: "running"}
	a.indexCancel = func() {}
	t.Cleanup(func() { a.indexMu.Lock(); a.indexCancel = nil; a.indexMu.Unlock() })
	five, ten, zero, eight := int64(5), int64(10), int64(0), int64(8)
	first, err := a.startIndexBuild(indexBuildRequest{Index: "txo-spender", From: &five, To: &ten, RetainOutputValues: boolPointer(true)})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := a.startIndexBuild(indexBuildRequest{Index: "txo-spender", From: &zero, To: &eight, RetainOutputValues: boolPointer(true)})
	if err != nil || merged.ID != first.ID || merged.From != 0 || merged.To != 10 || merged.OutputHistoryFrom != 0 {
		t.Fatal("queued range lost earlier optional history", merged, err)
	}
	// If a writer is already processing only the later historical selection,
	// it cannot satisfy a new request for earlier facts merely by sharing a tip.
	a.indexMu.Lock()
	a.indexQueue[0].Job.State = "running"
	a.indexQueue[0].Job.OutputHistoryFrom = 5
	a.indexQueue[0].Request.OutputHistoryFrom = &five
	a.indexMu.Unlock()
	earlier, err := a.startIndexBuild(indexBuildRequest{Index: "txo-spender", From: &zero, To: &eight, RetainOutputValues: boolPointer(true)})
	if err != nil || earlier.ID == first.ID || earlier.OutputHistoryFrom != 0 {
		t.Fatal("active later selection hid earlier history request", earlier, err)
	}
}

func Test075TXIOHistoricalEnrichmentReusesCanonicalLinks(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	zero := int64(0)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	build := func(values, scripts bool) indexJob {
		t.Helper()
		j, err := a.startIndexBuild(indexBuildRequest{Index: "txo-spender", From: &zero, To: &zero, RetainOutputValues: boolPointer(values), RetainOutputScripts: boolPointer(scripts)})
		if err != nil {
			t.Fatal(err)
		}
		j, err = a.waitQueuedIndexJob(ctx, j.ID)
		if err != nil || j.State != "complete" {
			t.Fatal(j, err)
		}
		waitLiveWriterReleased063(t, a)
		return j
	}
	build(false, false)
	s, err := indexStoreHead(a.dataDir, "txo-spender")
	if err != nil {
		t.Fatal(err)
	}
	original := s.checkpoint.Commitment
	if len(s.head.TXIOLayers["txio-values"]) != 0 || len(s.head.TXIOLayers["txio-scripts"]) != 0 {
		t.Fatal("positional build invented enrichment", s.head)
	}
	build(true, false)
	s, err = indexStoreHead(a.dataDir, "txo-spender")
	if err != nil || s.checkpoint.Commitment != original {
		t.Fatal("enrichment replaced canonical links", err)
	}
	if len(s.head.TXIOLayers["txio-values"]) != 1 || len(s.head.TXIOLayers["txio-scripts"]) != 0 {
		t.Fatal("independent value layer unavailable", s.head)
	}
	build(true, true)
	s, err = indexStoreHead(a.dataDir, "txo-spender")
	if err != nil || s.checkpoint.Commitment != original || len(s.head.TXIOLayers["txio-scripts"]) != 1 {
		t.Fatal("script layer did not backfill", s, err)
	}
	overlay, found, err := s.txioEnrichmentContext(ctx, 0, genesisHashDisplay)
	if err != nil || !found || !overlay.Values || !overlay.Scripts {
		t.Fatal(overlay, found, err)
	}
	status := a.indexStatus()
	for _, id := range []string{"txio-links", "txio-values", "txio-scripts"} {
		found := false
		for _, track := range status.Timeline.Tracks {
			if track.ID == id {
				found = true
				if len(track.Coverage) != 1 || track.Coverage[0].From != 0 || track.Coverage[0].To != 0 {
					t.Fatal("wrong per-layer coverage", track)
				}
			}
		}
		if !found {
			t.Fatal("missing layer", id)
		}
	}
	build(false, false)
	s, err = indexStoreHead(a.dataDir, "txo-spender")
	if err != nil || len(s.head.TXIOLayers["txio-values"]) != 1 || len(s.head.TXIOLayers["txio-scripts"]) != 1 {
		t.Fatal("disabling history removed retained facts", s, err)
	}
}

func Test075TXIOLayerCoverageIsCheckpointBound(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	s, err := openIndexStore(a.dataDir, "txo-spender")
	if err != nil {
		t.Fatal(err)
	}
	target, err := a.localBlockTarget(0)
	if err != nil {
		t.Fatal(err)
	}
	block, err := a.indexSourceBlock(context.Background(), target, "ephemeral")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.appendBlock(block, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	head := cloneTXIOLayerCoverage(s.head)
	head.TXIOLayers["txio-values"] = []heightInterval{{0, 100}}
	if err = atomicWriteJSON(filepath.Join(s.dir, "head.json"), head); err != nil {
		t.Fatal(err)
	}
	if _, err = indexStoreHead(a.dataDir, "txo-spender"); err == nil {
		t.Fatal("unbound coverage accepted")
	}
}

func Test075TXIOLiveEnrichesCurrentPositionalHistory(t *testing.T) {
	a := prepareLiveTestApp063(t)
	zero := int64(0)
	j, err := a.startIndexBuild(indexBuildRequest{Index: "txo-spender", From: &zero, To: &zero})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err = a.waitQueuedIndexJob(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	waitLiveWriterReleased063(t, a)
	// A connected peer observation makes the stored genesis tip fresh; no
	// positional block is missing when the history preference is enabled.
	peer := indexBitcoinSource(t, nil)
	hashCoordinateConnect(t, a, peer)
	a.setStatus(func(st *appStatus) {
		st.HeaderCount, st.HeaderHeight = 1, 0
		st.TipHash, st.HeaderState, st.Error = genesisHashDisplay, "current", ""
		st.Ready, st.Syncing = true, false
	})
	if _, err = a.setIndexLivePolicy(indexLiveRequest{Index: "txo-spender", Action: "enable", RetainOutputValues: boolPointer(true)}); err != nil {
		t.Fatal(err)
	}
	a.liveIndexTick()
	live := a.indexJobSnapshot()
	if live.ID == j.ID || !live.Live {
		t.Fatal("live scheduler skipped current-head history work", live)
	}
	if done, err := a.waitIndexBuild(ctx); err != nil || done.State != "complete" {
		t.Fatal(done, err)
	}
	waitLiveWriterReleased063(t, a)
	s, err := indexStoreHead(a.dataDir, "txo-spender")
	if err != nil || len(s.head.TXIOLayers["txio-values"]) != 1 {
		t.Fatal("new live history option was not backfilled", s, err)
	}
	if got := a.indexLiveView("txo-spender", indexLivePolicy{Index: "txo-spender", Enabled: true, RetainOutputValues: boolPointer(true)}); got.State != "synced" {
		t.Fatal("completed layer history was not caught up", got)
	}
}

func Test075TXIOReviewedRangeRepairsCoveredOutputPages(t *testing.T) {
	for _, inline := range []bool{false, true} {
		name := "overlay"
		if inline {
			name = "inline"
		}
		t.Run(name, func(t *testing.T) {
			a, _ := testAppWithGenesis(t)
			a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
			zero := int64(0)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			build := func(facts bool) {
				t.Helper()
				j, err := a.startIndexBuild(indexBuildRequest{Index: "txo-spender", From: &zero, To: &zero, Live: inline, LiveConfigured: true, RetainOutputValues: boolPointer(facts), RetainOutputScripts: boolPointer(facts)})
				if err != nil {
					t.Fatal(err)
				}
				if done, err := a.waitQueuedIndexJob(ctx, j.ID); err != nil || done.State != "complete" {
					t.Fatal(done, err)
				}
				waitLiveWriterReleased063(t, a)
			}
			build(inline)
			if !inline {
				build(true)
			}
			s, err := indexStoreHead(a.dataDir, "txo-spender")
			if err != nil {
				t.Fatal(err)
			}
			commitment := s.checkpoint.Commitment
			batch, err := s.readBatch(commitment)
			if err != nil {
				t.Fatal(err)
			}
			refs := batch.Spenders.OutputFacts
			if overlay, exists, err := s.txioEnrichmentContext(ctx, 0, genesisHashDisplay); err != nil {
				t.Fatal(err)
			} else if exists {
				refs = overlay.OutputFacts
			}
			if len(refs) == 0 {
				t.Fatal("no output pages in fixture")
			}
			for _, ref := range refs {
				path, err := outputFactPath(a.dataDir, ref.Page)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(path, []byte("damaged output page"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = a.lookupTXIOOutputFacts(ctx, "0.0.0"); err == nil {
				t.Fatal("lookup accepted covered damaged data")
			}
			build(true)
			s, err = indexStoreHead(a.dataDir, "txo-spender")
			if err != nil || s.checkpoint.Commitment != commitment {
				t.Fatal("repair replaced canonical spend links", err)
			}
			facts, err := a.lookupTXIOOutputFacts(ctx, "0.0.0")
			if err != nil || facts.State != "known" || facts.ValueSats == nil || facts.ScriptPubKey == nil {
				t.Fatal("explicit range did not repair covered facts", facts, err)
			}
		})
	}
}
