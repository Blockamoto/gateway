package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParcelValidatedSelectionInvariant079(t *testing.T) {
	for _, route := range []struct{ name, producer string }{{"retained-raw", "blocks"}, {"tx-locators", "tx-locator"}, {"txio-links", "txo-spender"}} {
		t.Run(route.name, func(t *testing.T) {
			a := runTestApp(t)
			parcel := indexRunSelection{Index: bitmapReplayParcelID, Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: route.name}}
			district := indexRunSelection{Index: bitmapStandardDistrictID, Armed: true}
			producer := indexRunSelection{Index: route.producer, Armed: true}
			zero := int64(0)
			req := indexRunRequest{In: &zero, Consumers: []indexRunSelection{district, producer, parcel}}
			if _, err := normalizeIndexRunRequest(req); err != nil {
				t.Fatalf("valid route rejected: %v", err)
			}
			for _, removed := range []string{bitmapStandardDistrictID, route.producer} {
				partial := req
				partial.Consumers = append([]indexRunSelection(nil), req.Consumers...)
				for i := range partial.Consumers {
					if partial.Consumers[i].Index == removed {
						partial.Consumers[i].Armed = false
					}
				}
				if _, err := normalizeIndexRunRequest(partial); err == nil {
					t.Fatalf("missing %s accepted for execution", removed)
				}
				saved, err := a.prepareIndexRun(partial)
				if err != nil {
					t.Fatal(err)
				}
				p, _ := runSelection(saved.Prepared, bitmapReplayParcelID)
				if p.Armed {
					t.Fatalf("missing %s left Parcel armed", removed)
				}
			}
		})
	}
}

func TestParcelPairIgnoresSavedDiscoveryUntilValidated079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &b0)
	b1 := bitmapDevelopmentFixtureBlock(b0.Height+1, b0.Hash)
	installRunProtocolHeader(t, a, &b1)
	b2 := bitmapDevelopmentFixtureBlock(b1.Height+1, b1.Hash)
	installRunProtocolHeader(t, a, &b2)
	a.parcelEvidenceHooks079 = &parcelEvidenceHooks079{Timeline: func() indexTimelineView {
		return testParcelProofTimeline079("retained-raw", b2.Height, b2.Height)
	}}
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
	pair, err := a.parcelDistrictSnapshot079(ctx, doc.Prepared, b2.Height)
	if err != nil {
		t.Fatal(err)
	}
	doc.Run = &indexRunRecord{ID: "validated-pair-test", State: "running", Request: doc.Prepared, InitialTargetHeight: b2.Height, ParcelDistrict: pair, Consumers: []indexRunProgress{{Index: bitmapReplayParcelID, AdmissionState: "admitted", From: b0.Height, To: b2.Height}}}
	a.indexRunMu.Lock()
	a.indexRunState = doc
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = appendParcelDiscovery079(ctx, a, b1, nil); err != nil {
		t.Fatal(err)
	}
	writer, err := openBitmapRunConsumer(ctx, a, bitmapStandardDistrictID)
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.Apply(ctx, b2, nil); !errors.Is(err, ErrBitmapEvidenceRequired) {
		t.Fatalf("unvalidated discovery advanced District: %v", err)
	}
	if err = parcel.Apply(ctx, b1, nil); err != nil {
		t.Fatal(err)
	}
	if err = writer.Apply(ctx, b2, nil); err != nil {
		t.Fatalf("validated catch-up did not release District: %v", err)
	}
	view, err := a.bitmapInstanceViewContext(ctx, "parcel", bitmapReplayParcelID, "", "", nil, nil, nil, "", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if view.DiscoverySnapshot != nil || len(view.ProvisionalCandidates) != 0 || len(view.DiscoveryCoverage) != 0 {
		t.Fatalf("experimental discovery escaped: %+v", view)
	}
	def, _ := bitmapFixedIndexDefinition(bitmapReplayParcelID)
	track := timelineTrack(t, a.indexTimeline([]indexDefinition{def}, nil, nil), bitmapReplayParcelID)
	if len(track.DiscoveryCoverage) != 0 || track.DiscoveryState != "" {
		t.Fatalf("discovery shown on timeline: %+v", track)
	}
}

func TestParcelUnresolvedProofLeavesExperimentalPendingReadOnly079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	consumer := bitmapTestConsumer(t, a.dataDir, bitmapReplayParcelID)
	path := filepath.Join(consumer.dir, "pending.json")
	if err := os.MkdirAll(consumer.dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("saved experimental evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	if err := consumer.pending(ctx, block, nil, "proof unavailable"); !errors.Is(err, ErrBitmapEvidenceRequired) {
		t.Fatalf("missing proof did not wait: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "saved experimental evidence" {
		t.Fatalf("experimental data changed: %q %v", body, err)
	}
	if head, err := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapReplayParcelID); err != nil || head != nil {
		t.Fatalf("missing proof advanced validated head: %+v %v", head, err)
	}
}

func TestParcelAllRoutesAcceptGrowingCommittedPrefix079(t *testing.T) {
	for _, route := range []struct{ name, producer string }{{"retained-raw", "blocks"}, {"tx-locators", "tx-locator"}, {"txio-links", "txo-spender"}} {
		t.Run(route.name, func(t *testing.T) {
			track := indexTimelineTrack{ID: route.producer}
			if route.name == "retained-raw" {
				track.RetainedCoverage = &rawCoverageView{Complete: true, Ranges: []heightInterval{{From: 0, To: 7}}}
			} else {
				track.Sources = []indexTimelineSource{{ID: "gateway_committed_index", Kind: "committed_records", Complete: true, Ranges: []heightInterval{{From: 0, To: 7}}}}
			}
			timeline := indexTimelineView{TipKnown: true, TipHeight: 10, Tracks: []indexTimelineTrack{track}}
			gaps, err := parcelEvidenceGaps079(route.name, timeline, 7)
			if err != nil || len(gaps) != 0 {
				t.Fatalf("current prefix rejected: %v %v", gaps, err)
			}
			gaps, err = parcelEvidenceGaps079(route.name, timeline, 8)
			if err != nil || len(gaps) == 0 || gaps[0].From != 8 {
				t.Fatalf("unbuilt next block accepted: %v %v", gaps, err)
			}
		})
	}
}
