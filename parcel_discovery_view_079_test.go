package main

import (
	"context"
	"testing"
)

// Synthetic selected blocks exercise presentation and cursor safety; they do
// not establish a real-chain Parcel relationship or winner.
func TestParcelDiscoveryViewSeparatesUnvalidatedAndPaginates079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	start := bitmapDevelopmentStart
	prepareDiscoveryFixtureRun079(t, a, start, start+1)
	first := bitmapDevelopmentFixtureBlock(start, "", "0.100.bitmap", "01.100.bitmap")
	installRunProtocolHeader(t, a, &first)
	if _, err := appendParcelDiscovery079(ctx, a, first, nil); err != nil {
		t.Fatal(err)
	}
	second := bitmapDevelopmentFixtureBlock(start+1, first.Hash, "0.100.bitmap")
	installRunProtocolHeader(t, a, &second)
	head, err := appendParcelDiscovery079(ctx, a, second, nil)
	if err != nil {
		t.Fatal(err)
	}
	district, transaction := uint64(100), uint64(0)
	page, err := a.bitmapInstanceViewContext(ctx, "parcel", bitmapReplayParcelID, "", "", &district, &transaction, nil, "", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if page.State != "provisional" || page.Snapshot != nil || page.DiscoverySnapshot == nil || page.DiscoverySnapshot.Commitment != head.Commitment || len(page.ValidationCoverage) != 0 || len(page.DiscoveryCoverage) != 1 || len(page.ProvisionalCandidates) != 1 || page.ProvisionalCandidates[0].Status != "not_yet_validated" || page.ProvisionalNextCursor == "" {
		t.Fatalf("provisional state was conflated with validated coverage: %+v", page)
	}
	next, err := a.bitmapInstanceViewContext(ctx, "parcel", bitmapReplayParcelID, "", "", &district, &transaction, nil, "", 1, false, page.ProvisionalNextCursor)
	if err != nil || len(next.ProvisionalCandidates) != 1 || next.ProvisionalCandidates[0].RevealHeight != first.Height || next.ProvisionalNextCursor != "" {
		t.Fatalf("provisional pagination: %+v %v", next, err)
	}
	wrong := uint64(1)
	if _, err = a.bitmapInstanceViewContext(ctx, "parcel", bitmapReplayParcelID, "", "", &district, &wrong, nil, "", 1, false, page.ProvisionalNextCursor); err == nil {
		t.Fatal("cursor was reused for another target")
	}
	all, err := a.bitmapInstanceViewContext(ctx, "parcel", bitmapReplayParcelID, "", "", nil, nil, nil, "", 1, false)
	if err != nil || len(all.ProvisionalCandidates) != 1 || all.ProvisionalCandidates[0].RevealHeight != second.Height || all.ProvisionalNextCursor == "" {
		t.Fatalf("missing general backlog first page: %+v %v", all, err)
	}
	older, err := a.bitmapInstanceViewContext(ctx, "parcel", bitmapReplayParcelID, "", "", nil, nil, nil, "", 1, false, all.ProvisionalNextCursor)
	if err != nil || len(older.ProvisionalCandidates) != 1 || older.ProvisionalCandidates[0].Status != "invalid_syntax" || older.ProvisionalNextCursor == "" {
		t.Fatalf("missing earlier invalid declaration: %+v %v", older, err)
	}

	defs := []indexDefinition{}
	def, ok := bitmapFixedIndexDefinition(bitmapReplayParcelID)
	if !ok {
		t.Fatal("missing replay definition")
	}
	defs = append(defs, def)
	timeline := a.indexTimeline(defs, nil, nil)
	track := timelineTrack(t, timeline, bitmapReplayParcelID)
	if track.DiscoveryState != "not_yet_validated" || len(track.DiscoveryCoverage) != 1 || len(track.Coverage) != 0 {
		t.Fatalf("timeline mixed discovery with validated coverage: %+v", timeline.Tracks)
	}
}
