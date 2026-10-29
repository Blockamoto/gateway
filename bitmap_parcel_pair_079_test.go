package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Synthetic protocol-height blocks exercise publication ordering. Their
// header slots are local fixtures, not a mainnet full-chain acceptance test.
func TestParcelPairHoldsDistrictUntilExactCompleteCatchup079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &b0)
	b1 := bitmapDevelopmentFixtureBlock(b0.Height+1, b0.Hash)
	installRunProtocolHeader(t, a, &b1)
	b2 := bitmapDevelopmentFixtureBlock(b1.Height+1, b1.Hash)
	installRunProtocolHeader(t, a, &b2)
	b3 := bitmapDevelopmentFixtureBlock(b2.Height+1, b2.Hash)
	installRunProtocolHeader(t, a, &b3)
	b4 := bitmapDevelopmentFixtureBlock(b3.Height+1, b3.Hash)
	installRunProtocolHeader(t, a, &b4)
	district := bitmapTestConsumer(t, a.dataDir, bitmapStandardDistrictID)
	parcel := bitmapTestConsumer(t, a.dataDir, bitmapReplayParcelID)
	if err := district.Apply(ctx, b0, nil); err != nil {
		t.Fatal(err)
	}
	if err := district.Apply(ctx, b1, nil); err != nil {
		t.Fatal(err)
	}
	if err := parcel.Apply(ctx, b0, nil); err != nil {
		t.Fatal(err)
	}
	choice := indexRunSelection{Index: bitmapReplayParcelID, Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "retained-raw", ParcelRawScanBudget: 12}}
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: bitmapStandardDistrictID, Armed: true}, {Index: "blocks", Armed: true}, choice}}
	doc, err := a.prepareIndexRun(req)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := a.parcelDistrictSnapshot079(ctx, doc.Prepared, b3.Height)
	if err != nil || pair.CeilingHeight != b1.Height || pair.CeilingCommit != district.head.Commitment || pair.EvidenceThrough != b1.Height {
		t.Fatalf("wrong fixed ceiling: %+v %v", pair, err)
	}
	doc.Run = &indexRunRecord{ID: "pair-test", State: "running", Request: doc.Prepared, TargetHeight: b3.Height, InitialTargetHeight: b3.Height, ParcelDistrict: pair, Consumers: []indexRunProgress{
		{Index: bitmapStandardDistrictID, State: "ready", AdmissionState: "admitted", From: b0.Height, To: b3.Height},
		{Index: bitmapReplayParcelID, State: "ready", AdmissionState: "admitted", From: b0.Height, To: b3.Height},
	}}
	a.indexRunMu.Lock()
	a.indexRunState = doc
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	writer, err := openBitmapRunConsumer(ctx, a, bitmapStandardDistrictID)
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.Apply(ctx, b2, nil); !errors.Is(err, ErrBitmapEvidenceRequired) {
		t.Fatalf("producer advanced during catch-up: %v", err)
	}
	if err = a.ingestArmedBitmapBlock(ctx, b2, nil); !errors.Is(err, ErrBitmapEvidenceRequired) {
		t.Fatalf("incidental producer bypassed hold: %v", err)
	}
	if head, _ := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapStandardDistrictID); head.Height != b1.Height {
		t.Fatal("District advanced while Parcel lagged", head.Height)
	}
	// The saved admission boundary is not recomputed when a run is paused and
	// reopened; paused incidental ingestion remains held at that same H.
	a.indexRunMu.Lock()
	a.indexRunState.Run.State = "paused"
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := readIndexRunDocument(a.dataDir)
	if err != nil || reopened.Run.ParcelDistrict.CeilingCommit != pair.CeilingCommit || reopened.Run.ParcelDistrict.CeilingHeight != b1.Height {
		t.Fatalf("lost fixed catch-up boundary on restart: %+v %v", reopened.Run, err)
	}
	if err = a.ingestArmedBitmapBlock(ctx, b2, nil); !errors.Is(err, ErrBitmapEvidenceRequired) {
		t.Fatalf("paused incidental producer bypassed hold: %v", err)
	}
	a.indexRunMu.Lock()
	a.indexRunState.Run.State = "running"
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = parcel.Apply(ctx, b1, nil); err != nil {
		t.Fatal(err)
	}
	if err = writer.Apply(ctx, b2, nil); err != nil {
		t.Fatal("validated-aligned first producer block:", err)
	}
	if err = writer.Apply(ctx, b3, nil); !errors.Is(err, ErrBitmapEvidenceRequired) {
		t.Fatalf("producer exceeded one dependent block: %v", err)
	}
	if err = parcel.Apply(ctx, b2, nil); err != nil {
		t.Fatal(err)
	}
	if err = writer.Apply(ctx, b3, nil); err != nil {
		t.Fatal("aligned pair did not resume:", err)
	}
	if err = parcel.Apply(ctx, b3, nil); err != nil {
		t.Fatal(err)
	}
	if err = a.ingestArmedBitmapBlock(ctx, b4, nil); !errors.Is(err, ErrBitmapEvidenceRequired) {
		t.Fatalf("incidental producer crossed finite pair boundary after alignment: %v", err)
	}
}

func TestParcelEvidenceAdmissionDoesNotRequireLedger079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	choice := indexRunSelection{Index: bitmapReplayParcelID, Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "tx-locators"}}
	doc, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{choice, {Index: bitmapStandardDistrictID, Armed: true}, {Index: "tx-locator", Armed: true}}})
	if err != nil {
		t.Fatal(err)
	}
	progress := indexRunProgress{Index: bitmapReplayParcelID, State: "needs_prerequisite", AdmissionState: "blocked", SavedCoverageState: "none"}
	if _, err = bindIndexRunBitmapProgress(ctx, a.dataDir, &progress); err != nil {
		t.Fatal(err)
	}
	doc.Run = &indexRunRecord{ID: "parcel-evidence-no-ledger", State: "running", Request: doc.Prepared, Consumers: []indexRunProgress{progress}}
	a.indexRunMu.Lock()
	a.indexRunState = doc
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := openParcelRawBudget079(a.dataDir, doc.Run.ID, 0, false)
	if err != nil {
		t.Fatalf("legacy run without ledger must resume: %v", err)
	}
	if err = tracker.take(); err != nil || tracker.count() != 1 || tracker.exhausted() {
		t.Fatalf("unlimited work tracking failed: count=%d err=%v", tracker.count(), err)
	}
}

func TestParcelProducerOffDisarmsSelection079(t *testing.T) {
	a := runTestApp(t)
	choice := indexRunSelection{Index: bitmapReplayParcelID, Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "retained-raw", ParcelRawScanBudget: 12}}
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: "blocks", Armed: true}, choice}}
	if _, err := normalizeIndexRunRequest(req); err == nil {
		t.Fatal("Parcel was executable without District selected")
	}
	doc, err := a.prepareIndexRun(req)
	if err != nil {
		t.Fatal(err)
	}
	parcel, _ := runSelection(doc.Prepared, bitmapReplayParcelID)
	if parcel.Armed {
		t.Fatal("Parcel remained armed without District")
	}
}

func TestParcelPairRejectsMissingProofAndMismatchedCheckpoint079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &b0)
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: bitmapStandardDistrictID, Armed: true}, {Index: bitmapReplayParcelID, Armed: true}}}
	if _, err := normalizeIndexRunRequest(req); err == nil {
		t.Fatal("missing route and allowance accepted")
	}
	doc, err := a.prepareIndexRun(req)
	if err != nil {
		t.Fatal(err)
	}
	parcel, _ := runSelection(doc.Prepared, bitmapReplayParcelID)
	if parcel.Armed {
		t.Fatal("Parcel remained armed without proof")
	}
	pair := &parcelDistrictPair079{CeilingHeight: b0.Height, CeilingHash: b0.Hash, CeilingCommit: testID(987)}
	if _, err = a.parcelDistrictAlignedHeight079(ctx, pair); err == nil {
		t.Fatal("missing/mismatched committed District accepted")
	}
}

func TestParcelPairedRunCannotDiscoverWithoutProofRoute079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap", "0.0.bitmap")
	installRunProtocolHeader(t, a, &b0)
	zero := int64(0)
	req := indexRunRequest{In: &zero, Consumers: []indexRunSelection{{Index: bitmapStandardDistrictID, Armed: true}, {Index: bitmapReplayParcelID, Armed: true}}}
	if _, err := a.planIndexRun(ctx, req); err == nil {
		t.Fatal("missing proof route was admitted")
	}
	if head, err := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapReplayParcelID); err != nil || head != nil {
		t.Fatalf("published validated winners: %+v %v", head, err)
	}
	if head, err := readParcelDiscoveryHead079(ctx, a.dataDir); err != nil || head != nil {
		t.Fatalf("created discovery state: %+v %v", head, err)
	}
}

func TestParcelPairCapsDistrictLiveWhenTipAdvances079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &b0)
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: bitmapStandardDistrictID, Armed: true, Live: true}, {Index: "blocks", Armed: true}, {Index: bitmapReplayParcelID, Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "retained-raw", ParcelRawScanBudget: 12}}}}
	doc, err := a.prepareIndexRun(req)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := a.parcelDistrictSnapshot079(ctx, doc.Prepared, b0.Height)
	if err != nil {
		t.Fatal(err)
	}
	doc.Run = &indexRunRecord{ID: "finite-live-pair", State: "running", Request: doc.Prepared, InitialTargetHeight: b0.Height, TargetHeight: b0.Height, ParcelDistrict: pair, Consumers: []indexRunProgress{{Index: bitmapReplayParcelID, AdmissionState: "admitted", From: b0.Height, To: b0.Height}}}
	a.indexRunMu.Lock()
	a.indexRunState = doc
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	b1 := bitmapDevelopmentFixtureBlock(b0.Height+1, b0.Hash)
	installRunProtocolHeader(t, a, &b1)
	if err = a.allowPairedDistrictPublication079(ctx, b1.Height); !errors.Is(err, ErrBitmapEvidenceRequired) {
		t.Fatalf("paired publication crossed finite boundary: %v", err)
	}
	if err = a.allowPairedDistrictPublication079(ctx, b0.Height); err != nil {
		t.Fatalf("admitted boundary was blocked: %v", err)
	}
}

// The private route hook substitutes a small committed-prefix fixture. It
// exercises the actual coordinator, proof accounting and wakeup rather
// than constructing a synthetic 792,000-block locator index on disk.
func TestParcelValidationWakesOnlyForRelevantPrefixWithoutQuota079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &b0)
	b1 := bitmapDevelopmentFixtureBlock(b0.Height+1, b0.Hash)
	installRunProtocolHeader(t, a, &b1)
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height == b0.Height {
			return b0, nil
		}
		if target.Height == b1.Height {
			return b1, nil
		}
		return blockView{}, fmt.Errorf("unexpected source")
	}
	var mu sync.Mutex
	through := b0.Height - 1
	var reads atomic.Int64
	a.parcelEvidenceHooks079 = &parcelEvidenceHooks079{
		Timeline: func() indexTimelineView {
			mu.Lock()
			h := through
			mu.Unlock()
			return indexTimelineView{TipKnown: true, TipHeight: b1.Height, Tracks: []indexTimelineTrack{{ID: "tx-locator", Sources: []indexTimelineSource{{ID: "gateway_committed_index", Kind: "committed_records", Complete: true, Ranges: []heightInterval{{From: 0, To: h}}}}}}}
		},
		Configure: func(_ context.Context, c *indexRunConsumer, h int64) error {
			consumer := c.external.(*bitmapRunConsumer)
			consumer.SetBlockSource(func(_ context.Context, height int64) (blockView, error) {
				if err := c.parcelBudget.take(); err != nil {
					return blockView{}, err
				}
				reads.Add(1)
				if height == b0.Height {
					return b0, nil
				}
				if height == b1.Height {
					return b1, nil
				}
				return blockView{}, fmt.Errorf("unexpected evidence height")
			})
			c.parcelEvidenceThrough = h
			return nil
		},
	}
	choice := indexRunSelection{Index: bitmapReplayParcelID, Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "tx-locators", ParcelRawScanBudget: 1}}
	zero := int64(0)
	req := indexRunRequest{In: &zero, Consumers: []indexRunSelection{{Index: bitmapStandardDistrictID, Armed: true}, {Index: "tx-locator", Armed: true}, choice}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("discovery should be admitted while evidence is incomplete: %+v %v", plan, err)
	}
	// Only the committed-prefix observations are injected. The reviewed request
	// includes the locator producer, but the protocol-height-only fixture omits
	// its worker; constructing 792k fake locator blocks would not verify it.
	var consumers []indexRunProgress
	for _, progress := range plan.Consumers {
		if progress.Index != "tx-locator" {
			consumers = append(consumers, progress)
		}
	}
	record := &indexRunRecord{ID: "injected-prefix-wakeup", State: "running", Request: plan.Request, TargetHeight: plan.TargetHeight, InitialTargetHeight: plan.TargetHeight, TargetHash: plan.TargetHash, Consumers: consumers, ParcelDistrict: plan.ParcelDistrict}
	record.FrontierHeight = initialIndexRunFrontier(record)
	if _, err = openParcelRawBudget079(a.dataDir, record.ID, choice.ParcelRawScanBudget, true); err != nil {
		t.Fatal(err)
	}
	a.indexRunMu.Lock()
	a.indexRunLoaded = true
	a.indexRunState.Schema = 1
	a.indexRunState.Configured = true
	a.indexRunState.Prepared = plan.Request
	a.indexRunState.Run = record
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	worker, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.indexMu.Lock()
	a.indexCancel = cancel
	a.indexRunActiveID = record.ID
	a.indexMu.Unlock()
	go a.runCoordinatedIndexes(worker, record.ID)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		doc := a.indexRunSnapshot()
		if doc.Run != nil {
			p, _ := indexRunProgressFor(doc.Run, bitmapReplayParcelID)
			if p.ParcelValidationState == "pending" && doc.Run.State == "waiting" {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	doc := a.indexRunSnapshot()
	if doc.Run == nil {
		t.Fatalf("run disappeared: %+v", doc)
	}
	p, _ := indexRunProgressFor(doc.Run, bitmapReplayParcelID)
	if p.ParcelDiscoveryHeight != nil || p.ParcelValidationState != "pending" || doc.Run.State != "waiting" || reads.Load() != 0 {
		t.Fatalf("did not park validation before proof coverage: %+v reads=%d", p, reads.Load())
	}
	if h, err := readParcelDiscoveryHead079(ctx, a.dataDir); err != nil || h != nil {
		t.Fatalf("validation created discovery state: %+v %v", h, err)
	}
	// A later commit outside the required prefix must not trigger proof work.
	notifyParcelIndexCommit079(a.dataDir, "tx-locator")
	time.Sleep(250 * time.Millisecond)
	if reads.Load() != 0 {
		t.Fatal("unrelated evidence event started proof work")
	}
	mu.Lock()
	through = b0.Height
	mu.Unlock()
	notifyParcelIndexCommit079(a.dataDir, "tx-locator")
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		doc = a.indexRunSnapshot()
		p, _ = indexRunProgressFor(doc.Run, bitmapReplayParcelID)
		if p.ParcelValidatedHeight != nil && *p.ParcelValidatedHeight == b0.Height {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if p.ParcelValidatedHeight == nil || *p.ParcelValidatedHeight != b0.Height || reads.Load() != 1 {
		t.Fatalf("first checked prefix did not publish: %+v reads=%d", p, reads.Load())
	}
	// A pause/resume does not require a ledger or regrant of work, and the
	// counter is retained as telemetry while the validated checkpoint survives.
	if _, err = a.controlIndexRun(ctx, record.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	paused := waitRunCondition(t, a, func(d indexRunDocument) bool { return d.Run != nil && d.Run.State == "paused" })
	if paused.Run.Counts.ParcelEvidenceChecks != 1 {
		t.Fatalf("pause lost work count: %+v", paused.Run.Counts)
	}
	if _, err = a.controlIndexRun(ctx, record.ID, "resume"); err != nil {
		t.Fatalf("resume without allowance failed: %v", err)
	}
	mu.Lock()
	through = b1.Height
	mu.Unlock()
	notifyParcelIndexCommit079(a.dataDir, "tx-locator")
	done := waitRun(t, a)
	p, _ = indexRunProgressFor(done.Run, bitmapReplayParcelID)
	if done.Run.State != "complete" || p.ParcelValidatedHeight == nil || *p.ParcelValidatedHeight != b1.Height || reads.Load() != 2 || done.Run.Counts.ParcelEvidenceChecks != 2 {
		t.Fatalf("legacy one-unit value stopped uncapped validation: %+v reads=%d work=%d", p, reads.Load(), done.Run.Counts.ParcelEvidenceChecks)
	}

}
