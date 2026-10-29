package main

import "testing"

func TestParcelPrerequisitesDistinguishInventoryAndProof(t *testing.T) {
	timeline := indexTimelineView{TipKnown: true, TipHeight: 792440, TipHash: "tip", Tracks: []indexTimelineTrack{
		{ID: "blocks", RetainedCoverage: &rawCoverageView{Complete: true, Ranges: []heightInterval{{0, 10}, {12, 792440}}}},
		{ID: "tx-locator", CoverageComplete: true, RecordedCoverage: []heightInterval{{0, 792440}}},
		{ID: "txo-spender", CoverageComplete: true, RecordedCoverage: []heightInterval{{792435, 792440}}},
		{ID: bitmapStandardDistrictID, CoverageComplete: true, RecordedCoverage: []heightInterval{{792435, 792437}}},
	}}
	got := parcelPrerequisites079(timeline)
	wantState := "unavailable"
	if bitmapBuiltinExecutionAvailable(bitmapReplayParcelID) {
		wantState = "requires_review"
	}
	if got.State != wantState || len(got.Routes) != 3 {
		t.Fatal(got)
	}
	if got.Routes[0].State != "coverage_gaps" || len(got.Routes[0].Gaps) != 1 || got.Routes[0].Gaps[0] != (heightInterval{11, 11}) {
		t.Fatal(got.Routes[0])
	}
	if got.Routes[1].State != "coverage_observed" || got.Routes[2].State != "coverage_observed" {
		t.Fatal(got.Routes)
	}
	if got.District.State != "coverage_gaps" || len(got.District.Gaps) != 1 || got.District.Gaps[0] != (heightInterval{792438, 792440}) {
		t.Fatal(got.District)
	}
	// Unknown physical inventory must not be promoted to a complete negative.
	timeline.Tracks[0].RetainedCoverage.Complete = false
	timeline.Tracks[1].CoverageComplete = false
	got = parcelPrerequisites079(timeline)
	if got.Routes[0].State != "unknown" || got.Routes[0].Gaps != nil || got.Routes[1].State != "unknown" {
		t.Fatal(got.Routes)
	}
	timeline.TipKnown = false
	got = parcelPrerequisites079(timeline)
	for _, route := range got.Routes {
		if route.State != "unknown" || len(route.Gaps) != 0 {
			t.Fatal(route)
		}
	}
}
