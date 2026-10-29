package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

// Tests inject a coverage observation, not a fabricated full Bitcoin prefix.
func testParcelProofTimeline079(route string, through, tip int64) indexTimelineView {
	track := indexTimelineTrack{ID: parcelEvidenceProducer079(route)}
	if route == "retained-raw" {
		track.RetainedCoverage = &rawCoverageView{Complete: true, Ranges: []heightInterval{{From: 0, To: through}}}
	} else {
		track.Sources = []indexTimelineSource{{ID: "gateway_committed_index", Kind: "committed_records", Complete: true, Ranges: []heightInterval{{From: 0, To: through}}}}
	}
	return indexTimelineView{TipKnown: true, TipHeight: tip, Tracks: []indexTimelineTrack{track}}
}

func TestParcelDistrictPublicationWaitsForProofPrefix079(t *testing.T) {
	for _, route := range []string{"retained-raw", "tx-locators", "txio-links"} {
		t.Run(route, func(t *testing.T) {
			ctx := context.Background()
			a := runTestApp(t)
			block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
			installRunProtocolHeader(t, a, &block)
			through := block.Height - 1
			a.parcelEvidenceHooks079 = &parcelEvidenceHooks079{Timeline: func() indexTimelineView {
				return testParcelProofTimeline079(route, through, block.Height)
			}}
			zero := int64(0)
			req := indexRunRequest{In: &zero, Consumers: []indexRunSelection{
				{Index: bitmapStandardDistrictID, Armed: true},
				{Index: parcelEvidenceProducer079(route), Armed: true},
				{Index: bitmapReplayParcelID, Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: route}},
			}}
			doc, err := a.prepareIndexRun(req)
			if err != nil {
				t.Fatal(err)
			}
			pair, err := a.parcelDistrictSnapshot079(ctx, doc.Prepared, block.Height)
			if err != nil {
				t.Fatal(err)
			}
			doc.Run = &indexRunRecord{ID: "prefix-fence", State: "running", Request: doc.Prepared, InitialTargetHeight: block.Height, TargetHeight: block.Height, ParcelDistrict: pair, Consumers: []indexRunProgress{{Index: bitmapReplayParcelID, AdmissionState: "admitted", From: block.Height, To: block.Height}}}
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
			if err = writer.Apply(ctx, block, nil); !errors.Is(err, ErrBitmapEvidenceRequired) || !strings.Contains(err.Error(), "proof prefix") {
				t.Fatalf("District published ahead of missing %s prefix: %v", route, err)
			}
			if head, e := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapStandardDistrictID); e != nil || head != nil {
				t.Fatalf("District coverage advanced while proof was unavailable: %+v %v", head, e)
			}
			through = block.Height
			if err = writer.Apply(ctx, block, nil); err != nil {
				t.Fatalf("District did not resume when selected prefix was ready: %v", err)
			}
		})
	}
}

func TestParcelSweepDoesNotSpeculateAtBitmapOrigin079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &block)
	var genesisReads, bitmapReads atomic.Int64
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height == 0 {
			genesisReads.Add(1)
		}
		if target.Height == block.Height {
			bitmapReads.Add(1)
			return block, nil
		}
		return blockView{}, blockUnavailableError{fmt.Errorf("fixture source unavailable at %d", target.Height)}
	}
	a.parcelEvidenceHooks079 = &parcelEvidenceHooks079{Timeline: func() indexTimelineView {
		return testParcelProofTimeline079("tx-locators", block.Height-1, block.Height)
	}}
	zero, end := int64(0), block.Height
	req := indexRunRequest{In: &zero, Out: &end, Consumers: []indexRunSelection{
		{Index: bitmapStandardDistrictID, Armed: true}, {Index: "tx-locator", Armed: true},
		{Index: bitmapReplayParcelID, Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "tx-locators"}},
	}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	started, err := a.startIndexRun(ctx, req, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = a.controlIndexRun(context.Background(), started.Run.ID, "cancel") }()
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		if d.Run == nil {
			return false
		}
		p, ok := indexRunProgressFor(d.Run, bitmapStandardDistrictID)
		return ok && p.State == "waiting" && strings.Contains(p.Reason, "proof prefix") && genesisReads.Load() > 0
	})
	if bitmapReads.Load() != 0 {
		t.Fatalf("speculative Bitmap source reads: %d", bitmapReads.Load())
	}
	if h, e := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapStandardDistrictID); e != nil || h != nil {
		t.Fatalf("District advanced before proof prefix: %+v %v", h, e)
	}
	if _, err = a.controlIndexRun(ctx, started.Run.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	waitRun(t, a)
}
