package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestInscriptionInventoryPrefixRejectsCompleteHeadAboveOlderPoint(t *testing.T) {
	a, _, _ := inscriptionProjectionSource(t, true, false)
	a.updateBackgroundWork.Wait()
	s, err := indexStoreHead(a.dataDir, "inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	var batches []indexBatch
	if err := s.walk(func(b indexBatch) error { batches = append(batches, b); return nil }); err != nil {
		t.Fatal(err)
	}
	previous := ""
	for i := len(batches) - 1; i >= 0; i-- {
		b := batches[i]
		if i == len(batches)-1 {
			b.Inscriptions = b.Inscriptions[:1]
			b.Checkpoint.DependencyFingerprint = indexDigest(struct{ KnownOccurrence string }{b.Inscriptions[0].ID})
		}
		if i == 0 {
			b.Numbering = &inscriptionNumberingSnapshot{Profile: inscriptionNumberingProfile, State: "complete"}
		}
		b.Checkpoint.PreviousCommitment = previous
		b.Checkpoint.RecordsHash = batchRecordsHash(b)
		b.Checkpoint.Commitment = checkpointHash(b.Checkpoint)
		if err := atomicWriteJSON(filepath.Join(s.dir, "commits", b.Checkpoint.Commitment+".json"), b); err != nil {
			t.Fatal(err)
		}
		previous = b.Checkpoint.Commitment
	}
	if err := atomicWriteJSON(filepath.Join(s.dir, "head.json"), indexHead{Mode: "full", Commitment: previous, Retention: "ephemeral"}); err != nil {
		t.Fatal(err)
	}
	s, err = indexStoreHead(a.dataDir, "inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	head, err := s.readBatch(s.checkpoint.Commitment)
	if err != nil || !inscriptionBatchInventoryComplete(head) {
		t.Fatal("fixture newest batch was not a valid complete inventory", err)
	}
	before := offLocatorFiles(t, s.dir)
	status := a.indexStatus()
	for _, instance := range status.Instances {
		if instance.Definition == "inscriptions" && (len(instance.Coverage) != 0 || !instance.Queryable) {
			t.Fatal("complete head laundered older partial ancestry", instance)
		}
	}
	track := timelineTrack(t, status.Timeline, "inscriptions")
	if len(track.Coverage) != 0 || len(track.RecordedCoverage) != 0 || track.CoverageComplete {
		t.Fatal("mixed prefix became complete timeline inventory", track)
	}
	for _, provider := range status.Providers {
		if provider.ID == "inscriptions" && provider.Provider == "gateway_canonical_numbering" && provider.Queryable {
			t.Fatal("mixed prefix became complete numbering readiness", provider)
		}
	}
	if view := inscriptionInventoryPrefixStatus(context.Background(), s); view.complete || !view.cached || view.batches != 0 {
		t.Fatal("repeated mixed-prefix observation was not cached unknown", view)
	}
	if after := offLocatorFiles(t, s.dir); !reflect.DeepEqual(before, after) {
		t.Fatal("Off prefix observation changed saved bytes or mtimes")
	}
	if folder := os.Getenv("GATEWAY_INSCRIPTION_PROJECTION_DIR"); folder != "" {
		if err := atomicWriteJSON(filepath.Join(folder, "older_point_prefix.json"), status); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInscriptionInventoryPrefixBoundedSingleflightAndWriterCertificates(t *testing.T) {
	a, last, _ := inscriptionProjectionSource(t, false, false)
	s, err := indexStoreHead(a.dataDir, "inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	if view := inspectInscriptionInventoryPrefix(context.Background(), s, 2, indexExplorerMaxBytes); view.complete || view.batches != 2 {
		t.Fatal("bounded probe walked beyond its batch budget", view)
	}
	if view := inspectInscriptionInventoryPrefix(context.Background(), s, indexExplorerMaxBatches, 1); view.complete || view.batches != 0 || view.bytes != 0 {
		t.Fatal("bounded probe read beyond its byte budget", view)
	}
	var wg sync.WaitGroup
	views := make(chan inscriptionInventoryPrefixView, 16)
	for i := 0; i < cap(views); i++ {
		wg.Add(1)
		go func() { defer wg.Done(); views <- inscriptionInventoryPrefixStatus(context.Background(), s) }()
	}
	wg.Wait()
	close(views)
	reads := 0
	for view := range views {
		if !view.complete {
			t.Fatal("valid old eight-block inventory was lost", view)
		}
		reads += view.batches
	}
	if reads != 8 {
		t.Fatal("concurrent observations duplicated the prefix read", reads)
	}
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "inscriptions", Armed: true, indexRunOptions: indexRunOptions{Mode: "full", ConventionalIDs: boolPointer(false)}}}}); err != nil {
		t.Fatal(err)
	}
	writer, err := openIndexStoreContext(context.Background(), a.dataDir, "inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	block := bitmapDevelopmentFixtureBlock(last.Checkpoint.Height+1, last.Checkpoint.BlockHash, "new complete reveal")
	installRunProtocolHeader(t, a, &block)
	extracted, err := extractInscriptionOccurrences(block)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.appendPreparedBlock(block, last.Checkpoint.From, "ephemeral", &extracted); err != nil {
		t.Fatal(err)
	}
	if view := inscriptionInventoryPrefixStatus(context.Background(), writer); !view.complete || !view.cached || view.batches != 0 {
		t.Fatal("post-publication append did not extend the certified prefix", view)
	}
	valid, err := writer.readBatch(writer.checkpoint.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"height", "hash", "origin"} {
		bad := valid
		bad.Checkpoint.PreviousCommitment = writer.checkpoint.Commitment
		bad.Checkpoint.Height++
		bad.PreviousBlockHash = writer.checkpoint.BlockHash
		bad.Checkpoint.BlockHash = indexDigest(field)
		if field == "height" {
			bad.Checkpoint.Height++
		} else if field == "hash" {
			bad.PreviousBlockHash = indexDigest("wrong parent hash")
		} else {
			bad.Checkpoint.From++
		}
		bad.InscriptionBlock = &inscriptionBlockMetadata{Mode: "lean", TransactionCount: 1}
		bad.Inscriptions, bad.InscriptionCoordinates, bad.Diagnostics = nil, []string{}, nil
		bad.Checkpoint.DependencyFingerprint = indexDigest(struct{ Block, Parser, Reference string }{bad.Checkpoint.BlockHash, inscriptionParserProfile, inscriptionReferenceCommit})
		bad.Checkpoint.RecordsHash = batchRecordsHash(bad)
		bad.Checkpoint.Commitment = checkpointHash(bad.Checkpoint)
		copyStore := *writer
		copyStore.checkpoint = &bad.Checkpoint
		rememberInscriptionInventoryAppend(&copyStore, bad)
		if _, have := inscriptionInventoryPrefixCached(&copyStore, bad.Checkpoint.Commitment); have {
			t.Fatal("invalid append edge was certified", field)
		}
	}
}

func TestInscriptionInventoryPrefixLargeUnknownUpgradesAfterFullWriterWalk(t *testing.T) {
	a := runTestApp(t)
	d, err := findIndexDefinition("inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(a.dataDir, "indexes", "inscriptions")
	if err := os.MkdirAll(filepath.Join(dir, "commits"), 0700); err != nil {
		t.Fatal(err)
	}
	previousHash, previousCommit := "", ""
	for h := int64(0); h <= indexExplorerMaxBatches; h++ {
		b := indexBatch{PreviousBlockHash: previousHash, InscriptionBlock: &inscriptionBlockMetadata{Mode: "lean", TransactionCount: 1}, InscriptionCoordinates: []string{}}
		b.Checkpoint = indexCheckpoint{Schema: d.CheckpointSchema, Definition: d.ID, Version: d.Version, RuleHash: d.RuleHash, Network: d.Network, From: 0, Height: h, BlockHash: indexDigest(h), PreviousCommitment: previousCommit}
		b.Checkpoint.DependencyFingerprint = indexDigest(struct{ Block, Parser, Reference string }{b.Checkpoint.BlockHash, inscriptionParserProfile, inscriptionReferenceCommit})
		b.Checkpoint.RecordsHash = batchRecordsHash(b)
		b.Checkpoint.Commitment = checkpointHash(b.Checkpoint)
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		// Fixture setup bypasses writer fsyncs; this test measures only bounded
		// ancestry and cache semantics, not durability or source validation.
		if err := os.WriteFile(filepath.Join(dir, "commits", b.Checkpoint.Commitment+".json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		previousHash, previousCommit = b.Checkpoint.BlockHash, b.Checkpoint.Commitment
	}
	if err := atomicWriteJSON(filepath.Join(dir, "head.json"), indexHead{Mode: "lean", Commitment: previousCommit}); err != nil {
		t.Fatal(err)
	}
	s, err := indexStoreHead(a.dataDir, "inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "inscriptions", Armed: false}}}); err != nil {
		t.Fatal(err)
	}
	before := offLocatorFiles(t, dir)
	view := inscriptionInventoryPrefixStatus(context.Background(), s)
	if view.complete || view.cached || view.batches != indexExplorerMaxBatches {
		t.Fatal("routine observation claimed an uninspected prefix", view)
	}
	if repeat := inscriptionInventoryPrefixStatus(context.Background(), s); repeat.complete || !repeat.cached || repeat.batches != 0 {
		t.Fatal("unknown immutable head repeated its bounded scan", repeat)
	}
	if _, err := openIndexStoreContext(context.Background(), a.dataDir, "inscriptions"); err != nil && !errors.Is(err, errIndexLocatorsReadOnly) {
		t.Fatal(err)
	}
	if view := inscriptionInventoryPrefixStatus(context.Background(), s); !view.complete || !view.cached || view.batches != 0 {
		t.Fatal("existing full writer walk did not upgrade the unknown certificate", view)
	}
	if after := offLocatorFiles(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("certificate work created sidecars or rewrote old source bytes")
	}
}

func TestInscriptionInventoryPrefixTransientReadRecoversSameHead(t *testing.T) {
	a, _, _ := inscriptionProjectionSource(t, false, false)
	s, err := indexStoreHead(a.dataDir, "inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	head, err := s.readBatch(s.checkpoint.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	before := offLocatorFiles(t, s.dir)
	missing := filepath.Join(s.dir, "commits", head.Checkpoint.PreviousCommitment+".json")
	held := filepath.Join(t.TempDir(), "temporarily-unavailable.json")
	if err := os.Rename(missing, held); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Rename(held, missing) }()
	first := inscriptionInventoryPrefixStatus(context.Background(), s)
	if first.complete || !first.retryable || first.cached || first.batches != 1 {
		t.Fatal("unavailable ancestor was not a retryable unknown", first)
	}
	if err := os.Rename(held, missing); err != nil {
		t.Fatal(err)
	}
	if immediate := inscriptionInventoryPrefixStatus(context.Background(), s); immediate.complete || !immediate.cached || !immediate.retryable || immediate.batches != 0 {
		t.Fatal("transient error retried without bounded backoff", immediate)
	}
	time.Sleep(inscriptionInventoryPrefixRetryDelay + 100*time.Millisecond)
	if recovered := inscriptionInventoryPrefixStatus(context.Background(), s); !recovered.complete || recovered.cached || recovered.batches != 8 {
		t.Fatal("restored bytes did not recover under the same head", recovered)
	}
	if final := inscriptionInventoryPrefixStatus(context.Background(), s); !final.complete || !final.cached || final.batches != 0 {
		t.Fatal("recovered certificate was not reused", final)
	}
	if after := offLocatorFiles(t, s.dir); !reflect.DeepEqual(before, after) {
		t.Fatal("read retry changed saved bytes, mtimes or sidecars")
	}
}
