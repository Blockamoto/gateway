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
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: bitmapStandardDistrictID, Armed: true}, choice}}
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
	if _, err = appendParcelDiscovery079(ctx, a, b1, nil); err != nil {
		t.Fatal(err)
	}
	if parcel.head.Height != b0.Height {
		t.Fatal("provisional discovery advanced validated Parcel coverage")
	}
	if err = writer.Apply(ctx, b2, nil); err != nil {
		t.Fatal("discovery-aligned first producer block:", err)
	}
	if err = writer.Apply(ctx, b3, nil); !errors.Is(err, ErrBitmapEvidenceRequired) {
		t.Fatalf("producer exceeded one dependent block: %v", err)
	}
	if _, err = appendParcelDiscovery079(ctx, a, b2, nil); err != nil {
		t.Fatal(err)
	}
	if parcel.head.Height != b0.Height {
		t.Fatal("discovery incorrectly advanced validation")
	}
	if err = writer.Apply(ctx, b3, nil); err != nil {
		t.Fatal("aligned pair did not resume:", err)
	}
	if _, err = appendParcelDiscovery079(ctx, a, b3, nil); err != nil {
		t.Fatal(err)
	}
	if err = a.ingestArmedBitmapBlock(ctx, b4, nil); !errors.Is(err, ErrBitmapEvidenceRequired) {
		t.Fatalf("incidental producer crossed finite pair boundary after alignment: %v", err)
	}
}

func TestParcelEvidenceAdmissionRequiresExistingLedger079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	choice := indexRunSelection{Index: bitmapReplayParcelID, Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "tx-locators", ParcelRawScanBudget: 130}}
	doc, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{choice, {Index: "tx-locator", Armed: true}}})
	if err != nil {
		t.Fatal(err)
	}
	progress := indexRunProgress{Index: bitmapReplayParcelID, State: "needs_prerequisite", AdmissionState: "blocked", ParcelEvidencePending: true, SavedCoverageState: "none"}
	if _, err = bindIndexRunBitmapProgress(ctx, a.dataDir, &progress); err != nil {
		t.Fatal(err)
	}
	doc.Run = &indexRunRecord{ID: "parcel-evidence-ledger", State: "running", Request: doc.Prepared, Consumers: []indexRunProgress{progress}}
	a.indexRunMu.Lock()
	a.indexRunState = doc
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = openParcelRawBudget079(a.dataDir, doc.Run.ID, choice.ParcelRawScanBudget, false); err == nil {
		t.Fatal("missing ledger was recreated on recovery")
	}
	budget, err := openParcelRawBudget079(a.dataDir, doc.Run.ID, choice.ParcelRawScanBudget, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = budget.take(); err != nil {
		t.Fatal(err)
	}
	before, err := openParcelRawBudget079(a.dataDir, doc.Run.ID, choice.ParcelRawScanBudget, false)
	if err != nil {
		t.Fatal(err)
	}
	after, err := openParcelRawBudget079(a.dataDir, doc.Run.ID, choice.ParcelRawScanBudget, false)
	if err != nil || after.doc.Remaining != before.doc.Remaining || after.doc.Remaining >= choice.ParcelRawScanBudget {
		t.Fatalf("recovery expanded allowance: before=%+v after=%+v err=%v", before.doc, after.doc, err)
	}
}

func TestParcelProducerOffPlansOnlySavedDistrictBoundary079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &b0)
	b1 := bitmapDevelopmentFixtureBlock(b0.Height+1, b0.Hash)
	installRunProtocolHeader(t, a, &b1)
	b2 := bitmapDevelopmentFixtureBlock(b1.Height+1, b1.Hash)
	installRunProtocolHeader(t, a, &b2)
	district := bitmapTestConsumer(t, a.dataDir, bitmapStandardDistrictID)
	if err := district.Apply(ctx, b0, nil); err != nil {
		t.Fatal(err)
	}
	if err := district.Apply(ctx, b1, nil); err != nil {
		t.Fatal(err)
	}
	choice := indexRunSelection{Index: bitmapReplayParcelID, Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "retained-raw", ParcelRawScanBudget: 12}}
	plan, err := a.planIndexRun(ctx, indexRunRequest{Consumers: []indexRunSelection{choice}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ParcelDistrict == nil || plan.ParcelDistrict.DistrictSelected || plan.ParcelDistrict.CeilingHeight != b1.Height || plan.ParcelDistrict.CeilingCommit != district.head.Commitment || plan.ParcelDistrict.EvidenceThrough != b0.Height {
		t.Fatalf("wrong finite catch-up plan: %+v", plan.ParcelDistrict)
	}
	if len(plan.Consumers) != 1 || plan.Consumers[0].To != b1.Height {
		t.Fatalf("Parcel did not cap work to saved District: %+v", plan.Consumers)
	}
	out := b0.Height
	plan, err = a.planIndexRun(ctx, indexRunRequest{Out: &out, Consumers: []indexRunSelection{choice}})
	if err != nil || plan.ParcelDistrict.CeilingHeight != b0.Height || plan.Consumers[0].To != b0.Height {
		t.Fatalf("Out did not cap saved District boundary: %+v %v", plan, err)
	}
}

func TestParcelPairAllowsDiscoveryWithoutProofAndRejectsMismatchedCheckpoint079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &b0)
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: bitmapStandardDistrictID, Armed: true}, {Index: bitmapReplayParcelID, Armed: true}}}
	doc, err := a.prepareIndexRun(req)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := a.parcelDistrictSnapshot079(ctx, doc.Prepared, b0.Height)
	if err != nil || pair.CeilingCommit != "" {
		t.Fatalf("fresh pair: %+v %v", pair, err)
	}
	doc.Run = &indexRunRecord{ID: "pair-discovery", State: "running", Request: doc.Prepared, InitialTargetHeight: b0.Height, ParcelDistrict: pair, Consumers: []indexRunProgress{{Index: bitmapReplayParcelID, AdmissionState: "admitted"}}}
	a.indexRunMu.Lock()
	a.indexRunState = doc
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.allowPairedDistrictPublication079(ctx, b0.Height); err != nil {
		t.Fatalf("missing proof consent incorrectly stopped discovery: %v", err)
	}
	pair.CeilingHeight, pair.CeilingHash, pair.CeilingCommit = b0.Height, b0.Hash, testID(987)
	if _, err = a.parcelDistrictAlignedHeight079(ctx, pair); err == nil {
		t.Fatal("missing/mismatched committed District accepted")
	}
}

func TestParcelPairedRunDiscoversWithoutProofRoute079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap", "0.0.bitmap")
	installRunProtocolHeader(t, a, &b0)
	b1 := bitmapDevelopmentFixtureBlock(b0.Height+1, b0.Hash, "0.0.bitmap")
	installRunProtocolHeader(t, a, &b1)
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height == b0.Height {
			return b0, nil
		}
		if target.Height == b1.Height {
			return b1, nil
		}
		return blockView{}, fmt.Errorf("unexpected source height %d", target.Height)
	}
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: bitmapStandardDistrictID, Armed: true}, {Index: bitmapReplayParcelID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("discovery should not require proof route: %+v %v", plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run == nil || done.Run.State != "complete" {
		t.Fatalf("finite discovery did not complete: %+v", done.Run)
	}
	progress, ok := indexRunProgressFor(done.Run, bitmapReplayParcelID)
	if !ok || progress.ParcelDiscoveryHeight == nil || *progress.ParcelDiscoveryHeight != b1.Height || progress.ParcelValidatedHeight != nil || progress.ParcelValidationState != "pending" {
		t.Fatalf("discovery/validation were conflated: %+v", progress)
	}
	if head, err := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapReplayParcelID); err != nil || head != nil {
		t.Fatalf("no-route discovery published validated winners: %+v %v", head, err)
	}
	row, err := readParcelDiscoveryBlock079(ctx, a.dataDir, b0.Height)
	if err != nil || row == nil || len(row.Candidates) != 1 || row.Candidates[0].Status != "not_yet_validated" {
		t.Fatalf("provisional declaration missing: %+v %v", row, err)
	}
	if _, err = openParcelRawBudget079(a.dataDir, done.Run.ID, 1, false); err == nil {
		t.Fatal("discovery without allowance created a proof budget")
	}
}

func TestParcelPairCapsDistrictLiveWhenTipAdvances079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &b0)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != b0.Height {
			return blockView{}, fmt.Errorf("read beyond reviewed Parcel boundary: %d", target.Height)
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return b0, nil
	}
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: bitmapStandardDistrictID, Armed: true, Live: true}, {Index: bitmapReplayParcelID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready || plan.TargetHeight != b0.Height {
		t.Fatalf("finite pair plan: %+v %v", plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("worker did not start")
	}
	b1 := bitmapDevelopmentFixtureBlock(b0.Height+1, b0.Hash)
	installRunProtocolHeader(t, a, &b1)
	parcelDistrictAdmissionMu079.Lock()
	err = a.allowPairedDistrictPublication079(ctx, b1.Height)
	parcelDistrictAdmissionMu079.Unlock()
	if !errors.Is(err, ErrBitmapEvidenceRequired) {
		close(release)
		t.Fatalf("paired publication crossed the finite admission boundary: %v", err)
	}
	close(release)
	done := waitRun(t, a)
	if done.Run == nil || done.Run.State != "complete" {
		t.Fatalf("capped live pair did not complete: %+v", done.Run)
	}
	district, ok := indexRunProgressFor(done.Run, bitmapStandardDistrictID)
	if !ok || district.Height != b0.Height || district.To != b0.Height || district.State != "complete" {
		t.Fatalf("District crossed the finite Parcel boundary: %+v", district)
	}
	parcel, ok := indexRunProgressFor(done.Run, bitmapReplayParcelID)
	if !ok || parcel.ParcelDiscoveryHeight == nil || *parcel.ParcelDiscoveryHeight != b0.Height {
		t.Fatalf("Parcel discovery failed to reach the shared boundary: %+v", parcel)
	}
}

// The private route hook substitutes a small committed-prefix fixture. It
// exercises the actual coordinator, persistent allowance and wakeup rather
// than constructing a synthetic 792,000-block locator index on disk.
func TestParcelValidationWakesOnlyForRelevantPrefixAndRespectsBudget079(t *testing.T) {
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
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: bitmapStandardDistrictID, Armed: true}, choice}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("discovery should be admitted while evidence is incomplete: %+v %v", plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		doc := a.indexRunSnapshot()
		if doc.Run != nil {
			p, _ := indexRunProgressFor(doc.Run, bitmapReplayParcelID)
			if p.ParcelDiscoveryHeight != nil && *p.ParcelDiscoveryHeight == b1.Height && doc.Run.State == "waiting" {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	doc := a.indexRunSnapshot()
	p, _ := indexRunProgressFor(doc.Run, bitmapReplayParcelID)
	if p.ParcelDiscoveryHeight == nil || *p.ParcelDiscoveryHeight != b1.Height || doc.Run.State != "waiting" || reads.Load() != 0 {
		t.Fatalf("did not park provisional discovery: %+v reads=%d", p, reads.Load())
	}
	// A later commit outside the required prefix must not trigger proof work.
	notifyParcelIndexCommit079(a.dataDir, "tx-locator")
	time.Sleep(250 * time.Millisecond)
	if reads.Load() != 0 {
		t.Fatal("unrelated evidence event consumed the allowance")
	}
	mu.Lock()
	through = b0.Height
	mu.Unlock()
	notifyParcelIndexCommit079(a.dataDir, "tx-locator")
	done := waitRun(t, a)
	p, _ = indexRunProgressFor(done.Run, bitmapReplayParcelID)
	if done.Run.State != "complete_with_errors" || p.ParcelValidatedHeight == nil || *p.ParcelValidatedHeight != b0.Height || p.ParcelValidationState != "blocked" || reads.Load() != 1 {
		t.Fatalf("evidence wakeup or budget handling failed: %+v reads=%d", p, reads.Load())
	}
	if _, err := openParcelRawBudget079(a.dataDir, done.Run.ID, 1, false); err != nil {
		t.Fatal("admitted allowance ledger disappeared", err)
	}
}
