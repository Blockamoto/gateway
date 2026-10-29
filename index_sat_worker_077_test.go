package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func Test077SatWorkerIncompatibleHeadRequiresReviewedRebuild(t *testing.T) {
	a := independentTestApp(t)
	dir := filepath.Join(a.dataDir, "indexes", "sat-state")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	old := []byte(`{"schema":1,"definition":"sat-state","version":1,"rule_hash":"old-experimental","mode":"lean"}`)
	if err := os.WriteFile(filepath.Join(dir, "head.json"), old, 0600); err != nil {
		t.Fatal(err)
	}
	a.runSatIndexBuild077(context.Background(), indexJob{ID: "incompatible-sat-worker", Index: "sat-state", State: "running", From: 0, To: 0, Mode: "lean", Retention: "pruned"})
	job := a.indexJobSnapshot()
	got, err := os.ReadFile(filepath.Join(dir, "head.json"))
	if err != nil || job.State != "rebuild_required" || string(got) != string(old) {
		t.Fatal("worker did not preserve incompatible evidence for reviewed recovery", job, err)
	}
}

func Test077SatPublicationRechecksSelectedAnchorAfterDerivation(t *testing.T) {
	ctx := context.Background()
	a := prepareLiveTestApp063(t)
	raw, hash := installBlockOneHeader063(t, a)
	s, err := openIndexStoreContext(ctx, a.dataDir, "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	genesis, err := a.fetchAndDecodeContext(ctx, "0")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.appendBlock(genesis, 0, "pruned"); err != nil {
		t.Fatal(err)
	}
	before := s.checkpoint.Commitment
	block, err := parseBlockDetailed(1, hash, raw[:80], raw, "fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	authority := a.currentChainAuthority()
	replaceHeader := func(header []byte) error {
		f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = f.WriteAt(header, 80)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		return err
	}
	s.satCommitHook = func(stage string) error {
		if stage != "pages_written" {
			return nil
		}
		header := append([]byte(nil), raw[:80]...)
		header[79] ^= 1
		return replaceHeader(header)
	}
	err = appendSatSelectedBlock077(ctx, s, block, 0, "pruned", nil, a.addressSelectedHashes(ctx, authority))
	var changed satPublicationAnchorError077
	if !errors.As(err, &changed) {
		t.Fatalf("changed chain published without final anchor check: %v", err)
	}
	loaded, err := indexStoreHead(a.dataDir, "sat-state")
	if err != nil || loaded.checkpoint.Commitment != before || loaded.checkpoint.Height != 0 {
		t.Fatal("replacement selection advanced the durable Sat head", loaded, err)
	}
	s.satCommitHook = nil
	if err = replaceHeader(raw[:80]); err != nil {
		t.Fatal(err)
	}
	if err = appendSatSelectedBlock077(ctx, s, block, 0, "pruned", nil, a.addressSelectedHashes(ctx, authority)); err != nil {
		t.Fatal("retry from preserved head failed", err)
	}
	if s.checkpoint.Height != 1 || s.checkpoint.PreviousCommitment != before {
		t.Fatal("retry lost previous state", s.checkpoint)
	}
}

func waitSatJob077(t *testing.T, a *app, id string) indexJob {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	j, err := a.waitQueuedIndexJob(ctx, id)
	if err != nil {
		t.Fatal(j, err)
	}
	waitLiveWriterReleased063(t, a)
	return j
}
func Test077SatBuildGenesisFixedBoundaryAndLiveContinuation(t *testing.T) {
	a := prepareLiveTestApp063(t)
	raw, _ := installBlockOneHeader063(t, a)
	peer, requests := hashCoordinateBitcoinSource(t, raw)
	hashCoordinateConnect(t, a, peer)
	zero, one := int64(0), int64(1)
	p, err := a.planIndexBuild(indexBuildRequest{Index: "sat-state", From: &zero, To: &zero})
	if err != nil || p.Retention != "pruned" || p.RetainSatHistory || p.Mode != "lean" {
		t.Fatal(p, err)
	}
	if _, err = a.planIndexBuild(indexBuildRequest{Index: "sat-state", From: &one, To: &one}); err == nil {
		t.Fatal("arbitrary Sat start accepted")
	}
	j, err := a.startIndexBuild(indexBuildRequest{Index: "sat-state", From: &zero, To: &zero})
	if err != nil {
		t.Fatal(err)
	}
	j = waitSatJob077(t, a, j.ID)
	if j.State != "complete" || j.Height != 0 || requests.Load() != 0 {
		t.Fatal("fixed boundary fetched later body", j, requests.Load())
	}
	s, err := indexStoreHead(a.dataDir, "sat-state")
	if err != nil || s.checkpoint.Height != 0 {
		t.Fatal(s, err)
	}
	if _, err = a.setIndexLivePolicy(indexLiveRequest{Index: "sat-state", Action: "enable"}); err != nil {
		t.Fatal(err)
	}
	a.liveIndexTick()
	j = waitLiveComplete064(t, a)
	if j.Index != "sat-state" || j.Height != 1 || j.Retention != "pruned" {
		t.Fatal(j)
	}
	s, err = indexStoreHead(a.dataDir, "sat-state")
	if err != nil || s.checkpoint.Height != 1 {
		t.Fatal(s, err)
	}
	if _, err = a.planIndexBuild(indexBuildRequest{Index: "sat-state", To: &zero}); err == nil {
		t.Fatal("older fixed target silently returned later snapshot")
	}
	if _, err = os.Stat(filepath.Join(a.dataDir, "blocks", "raw", "1-"+s.checkpoint.BlockHash+".block")); !os.IsNotExist(err) {
		t.Fatal("pruned Sat processing retained fetched body", err)
	}
	oldID := a.indexJobSnapshot().ID
	a.liveIndexTick()
	if a.indexJobSnapshot().ID != oldID {
		t.Fatal("caught up Live rescheduled work")
	}
}
func Test077SatPauseResumeKeepsCommittedCheckpoint(t *testing.T) {
	a := prepareLiveTestApp063(t)
	raw, _ := installBlockOneHeader063(t, a)
	a.settings.NetworkDisabled = true
	zero, one := int64(0), int64(1)
	j, err := a.startIndexBuild(indexBuildRequest{Index: "sat-state", From: &zero, To: &one})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		job := a.indexJobSnapshot()
		if job.State == "waiting" && job.Height == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	before, err := indexStoreHead(a.dataDir, "sat-state")
	if err != nil || before.checkpoint == nil || before.checkpoint.Height != 0 {
		t.Fatal(before, err, a.indexJobSnapshot())
	}
	commit := before.checkpoint.Commitment
	if _, err = a.controlIndexQueue(indexQueueRequest{ID: j.ID, Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	paused := waitSatJob077(t, a, j.ID)
	if paused.State != "paused" {
		t.Fatal(paused)
	}
	a.settingsMu.Lock()
	a.settings.NetworkDisabled = false
	a.settingsMu.Unlock()
	peer, _ := hashCoordinateBitcoinSource(t, raw)
	hashCoordinateConnect(t, a, peer)
	if _, err = a.controlIndexQueue(indexQueueRequest{ID: j.ID, Action: "resume"}); err != nil {
		t.Fatal(err)
	}
	after := waitSatJob077(t, a, j.ID)
	if after.State != "complete" || after.Height != 1 {
		t.Fatal(after)
	}
	s, err := indexStoreHead(a.dataDir, "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.readBatch(s.checkpoint.Commitment)
	if err != nil || b.Checkpoint.PreviousCommitment != commit {
		t.Fatal("resume reset or skipped prior checkpoint", b.Checkpoint, err)
	}
	if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", "sat-state-archives")); !os.IsNotExist(err) {
		t.Fatal("ordinary resume archived/reset state", err)
	}
}
func Test077SatReviewedRebuildPreservesIncompatibleHead(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.NetworkDisabled = true, true
	dir := filepath.Join(a.dataDir, "indexes", "sat-state")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	old := []byte(`{"schema":1,"definition":"sat-state","version":1,"rule_hash":"old-experimental","mode":"lean"}`)
	if err := os.WriteFile(filepath.Join(dir, "head.json"), old, 0600); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if _, err := a.planIndexBuild(indexBuildRequest{Index: "sat-state", To: &zero}); err == nil {
		t.Fatal("incompatible state resumed implicitly")
	}
	request := indexBuildRequest{Index: "sat-state", To: &zero, Rebuild: true}
	p, err := a.planIndexBuild(request)
	if err != nil || p.Outputs[0].Recovery != "explicit_genesis_rebuild" {
		t.Fatal(p, err)
	}
	j, err := a.startIndexBuild(request)
	if err != nil {
		t.Fatal(err)
	}
	j = waitSatJob077(t, a, j.ID)
	if j.State != "complete" || j.Height != 0 {
		t.Fatal(j)
	}
	archived, err := os.ReadFile(filepath.Join(a.dataDir, "indexes", "sat-state-archives", indexDigest(j.ID), "head.json"))
	if err != nil || string(archived) != string(old) {
		t.Fatal("reviewed rebuild did not preserve exact prior evidence", err)
	}
	if err = resetSatStateForRebuildContext(context.Background(), a.dataDir, j.ID); err != nil {
		t.Fatal("same job reset not idempotent", err)
	}
	s, err := indexStoreHead(a.dataDir, "sat-state")
	if err != nil || s.checkpoint == nil || s.checkpoint.Height != 0 {
		t.Fatal("same job reset lost new head", s, err)
	}
}
func Test077SatShorterCoreSelectionDoesNotFallbackToOldMirror(t *testing.T) {
	ctx := context.Background()
	a := prepareLiveTestApp063(t)
	raw, hash := installBlockOneHeader063(t, a)
	s, err := openIndexStore(a.dataDir, "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	genesis, err := a.fetchAndDecodeContext(ctx, "0")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.appendBlock(genesis, 0, "pruned"); err != nil {
		t.Fatal(err)
	}
	block, err := parseBlockDetailed(1, hash, raw[:80], raw, "fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.appendBlock(block, 0, "pruned"); err != nil {
		t.Fatal(err)
	}
	settings, _, stop := satline077RPC(t, map[int64]string{0: genesis.Hash})
	defer stop()
	a.settings = settings
	zero := int64(0)
	p, err := a.planIndexBuild(indexBuildRequest{Index: "sat-state", To: &zero})
	if err != nil || p.Outputs[0].Recovery != "reconcile_reorg" || len(p.Outputs[0].Reuse) != 0 {
		t.Fatal("old mirror substituted for shorter Core selection", p, err)
	}
	j, err := a.startIndexBuild(indexBuildRequest{Index: "sat-state", To: &zero})
	if err != nil {
		t.Fatal(err)
	}
	j = waitSatJob077(t, a, j.ID)
	s, err = indexStoreHead(a.dataDir, "sat-state")
	if err != nil || j.State != "complete" || s.checkpoint.Height != 0 {
		t.Fatal(j, s, err)
	}
}
func Test077SatBeyondSavedRecoveryEndsWithReviewedRebuildRequired(t *testing.T) {
	ctx := context.Background()
	a := independentTestApp(t)
	s, err := openIndexStore(a.dataDir, "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	previous := ""
	genesisHash := ""
	for height := int64(0); height <= 300; height++ {
		block := utxoFixtureBlock(height, previous, utxoFixtureCoinbase(uint64(100000+height), subsidyAtHeight(height)))
		if height == 0 {
			genesisHash = block.Hash
		}
		if err = s.appendBlock(block, 0, "pruned"); err != nil {
			t.Fatal(height, err)
		}
		previous = block.Hash
	}
	before := s.checkpoint.Commitment
	settings, _, stop := satline077RPC(t, map[int64]string{0: genesisHash, 1: strings.Repeat("b", 64)})
	defer stop()
	a.settings = settings
	a.status = appStatus{HeaderHeight: -1}
	if _, err = a.planIndexBuild(indexBuildRequest{Index: "sat-state", To: int64Pointer076(1)}); err != nil {
		t.Fatal("reviewable recovery plan unavailable", err)
	}
	a.runSatIndexBuild077(ctx, indexJob{ID: "deep-sat-reorg-fixture", Index: "sat-state", State: "running", From: 0, To: 1, Mode: "lean", Retention: "pruned"})
	job := a.indexJobSnapshot()
	after, err := indexStoreHead(a.dataDir, "sat-state")
	if err != nil || job.State != "rebuild_required" || after.checkpoint.Commitment != before {
		t.Fatal("deep recovery waited/reset or discarded saved state", job, err)
	}
}
