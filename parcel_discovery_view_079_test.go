package main

import (
	"context"
	"testing"
)

// Synthetic selected blocks exercise presentation and cursor safety; they do
// not establish a real-chain Parcel relationship or winner.
func TestParcelDiscoveryViewKeepsExperimentalDataPrivate079(t *testing.T) {
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
	if page.DiscoverySnapshot != nil || len(page.DiscoveryCoverage) != 0 || len(page.ProvisionalCandidates) != 0 || page.ProvisionalNextCursor != "" || len(page.ValidationCoverage) != 0 || page.Snapshot != nil {
		t.Fatalf("experimental data was presented as validated: %+v", page)
	}
	retained, err := readParcelDiscoveryHead079(ctx, a.dataDir)
	if err != nil || retained == nil || retained.Commitment != head.Commitment {
		t.Fatalf("experimental journal was not preserved: %+v %v", retained, err)
	}
	def, ok := bitmapFixedIndexDefinition(bitmapReplayParcelID)
	if !ok {
		t.Fatal("missing replay definition")
	}
	track := timelineTrack(t, a.indexTimeline([]indexDefinition{def}, nil, nil), bitmapReplayParcelID)
	if track.DiscoveryState != "" || len(track.DiscoveryCoverage) != 0 || len(track.Coverage) != 0 {
		t.Fatalf("timeline presented experimental data as coverage: %+v", track)
	}
}
