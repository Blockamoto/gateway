package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func prepareDiscoveryFixtureRun079(t *testing.T, a *app, from, to int64) {
	t.Helper()
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: bitmapStandardDistrictID, Armed: true}, {Index: "blocks", Armed: true}, {Index: bitmapReplayParcelID, Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "retained-raw", ParcelRawScanBudget: 12}}}}
	doc, err := a.prepareIndexRun(req)
	if err != nil {
		t.Fatal(err)
	}
	doc.Run = &indexRunRecord{ID: "discovery-fixture", State: "running", Request: doc.Prepared, ParcelDistrict: &parcelDistrictPair079{DistrictSelected: false}, InitialTargetHeight: to, TargetHeight: to, Consumers: []indexRunProgress{{Index: bitmapReplayParcelID, From: from, To: to, Height: from - 1, AdmissionState: "admitted"}}}
	a.indexRunMu.Lock()
	a.indexRunState = doc
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

// Synthetic selected-header fixture exercises journal semantics, not Bitcoin
// mainnet acceptance or parent proof. The stored claims are explicitly pending.
func TestParcelDiscoveryPersistsSeparatePendingAndRewinds079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	prepareDiscoveryFixtureRun079(t, a, bitmapDevelopmentStart, bitmapDevelopmentStart+1)
	first := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.100.bitmap", "01.100.bitmap", "ordinary text")
	installRunProtocolHeader(t, a, &first)
	district := bitmapTestConsumer(t, a.dataDir, bitmapStandardDistrictID)
	if err := district.Apply(ctx, first, nil); err != nil {
		t.Fatal(err)
	}
	extracted, err := extractInscriptionOccurrences(first)
	if err != nil {
		t.Fatal(err)
	}
	h, err := appendParcelDiscovery079(ctx, a, first, &extracted)
	if err != nil {
		t.Fatal(err)
	}
	if h.Height != first.Height || h.ProvisionalCount != 1 || h.InvalidCount != 1 || h.DistrictCommitment != district.head.Commitment {
		t.Fatalf("incorrect discovery: %+v", h)
	}
	if validated, err := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapReplayParcelID); err != nil || validated != nil {
		t.Fatalf("discovery published validated winner state: %+v %v", validated, err)
	}
	row, err := readParcelDiscoveryBlock079(ctx, a.dataDir, first.Height)
	if err != nil || row == nil || len(row.Candidates) != 2 {
		t.Fatalf("missing discovery rows: %+v %v", row, err)
	}
	if row.Candidates[0].Status != "not_yet_validated" || row.Candidates[1].Status != "invalid_syntax" || row.Candidates[0].Reason == "" {
		t.Fatal("provisional and invalid claims conflated", row.Candidates)
	}
	got, next, err := parcelDiscoveryTarget079(ctx, a.dataDir, h.Commitment, 100, 0, "", 1)
	if err != nil || len(got) != 1 || next != "" || got[0].ID != row.Candidates[0].ID {
		t.Fatalf("target lookup: %+v %q %v", got, next, err)
	}
	second := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart+1, first.Hash, "0.100.bitmap")
	installRunProtocolHeader(t, a, &second)
	if err = district.Apply(ctx, second, nil); err != nil {
		t.Fatal(err)
	}
	h2, err := appendParcelDiscovery079(ctx, a, second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h2.ProvisionalCount != 2 || h2.InvalidCount != 1 || h2.Previous != h.Commitment {
		t.Fatalf("incorrect cumulative journal %+v", h2)
	}
	got, next, err = parcelDiscoveryTarget079(ctx, a.dataDir, h2.Commitment, 100, 0, "", 1)
	if err != nil || len(got) != 1 || next == "" || got[0].RevealHeight != second.Height {
		t.Fatalf("target first page: %+v %q %v", got, next, err)
	}
	got, next, err = parcelDiscoveryTarget079(ctx, a.dataDir, h2.Commitment, 100, 0, next, 1)
	if err != nil || len(got) != 1 || next != "" || got[0].RevealHeight != first.Height {
		t.Fatalf("target second page: %+v %q %v", got, next, err)
	}
	if _, _, err = parcelDiscoveryTarget079(ctx, a.dataDir, h.Commitment, 100, 0, "", 1); err == nil {
		t.Fatal("stale snapshot cursor accepted")
	}
	if _, err = appendParcelDiscovery079(ctx, a, second, nil); err == nil {
		t.Fatal("duplicate block silently advanced")
	}
	if _, err = reconcileParcelDiscovery079(ctx, a, func(height int64) (string, error) {
		if height == second.Height {
			return strings.Repeat("f", 64), nil
		}
		return first.Hash, nil
	}); err != nil {
		t.Fatal(err)
	}
	rewound, err := readParcelDiscoveryHead079(ctx, a.dataDir)
	if err != nil || rewound == nil || rewound.Commitment != h.Commitment {
		t.Fatalf("rewind failed: %+v %v", rewound, err)
	}
	if b, err := readParcelDiscoveryBlock079(ctx, a.dataDir, second.Height); err != nil || b != nil {
		t.Fatalf("orphan appears in active discovery: %+v %v", b, err)
	}
	// The immutable old block stays on disk but cannot be returned from the new head.
	if _, err = os.Stat(filepath.Join(parcelDiscoveryDir079(a.dataDir), "commits", h2.Commitment[:2], h2.Commitment+".json")); err != nil {
		t.Fatal("reorg deleted old record", err)
	}
}

func TestParcelDiscoveryRejectsIncompleteInventory079(t *testing.T) {
	a := runTestApp(t)
	prepareDiscoveryFixtureRun079(t, a, bitmapDevelopmentStart, bitmapDevelopmentStart)
	block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.100.bitmap")
	installRunProtocolHeader(t, a, &block)
	district := bitmapTestConsumer(t, a.dataDir, bitmapStandardDistrictID)
	if err := district.Apply(context.Background(), block, nil); err != nil {
		t.Fatal(err)
	}
	x, err := extractInscriptionOccurrences(block)
	if err != nil {
		t.Fatal(err)
	}
	x.Occurrences = nil // a subset is not evidence that this block was completely scanned
	if _, err = appendParcelDiscovery079(context.Background(), a, block, &x); err == nil {
		t.Fatal("incomplete inventory accepted")
	}
	if h, err := readParcelDiscoveryHead079(context.Background(), a.dataDir); err != nil || h != nil {
		t.Fatalf("incomplete inventory advanced: %+v %v", h, err)
	}
}

func TestParcelDiscoveryRejectsChangedPreparedClaim079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	prepareDiscoveryFixtureRun079(t, a, bitmapDevelopmentStart, bitmapDevelopmentStart)
	block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.100.bitmap")
	installRunProtocolHeader(t, a, &block)
	full, err := extractInscriptionOccurrences(block)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*inscriptionOccurrence){
		func(o *inscriptionOccurrence) { o.Body = []byte("1.100.bitmap") },
		func(o *inscriptionOccurrence) { o.Envelope.Parents = []string{strings.Repeat("a", 64) + "i0"} },
	} {
		x := full
		x.Occurrences = append([]inscriptionOccurrence(nil), full.Occurrences...)
		change(&x.Occurrences[0])
		if _, err = appendParcelDiscovery079(ctx, a, block, &x); err == nil {
			t.Fatal("modified prepared claim data accepted under old inventory receipt")
		}
	}
}

func TestParcelDiscoveryMissingDistrictAndAdmissionFence079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.100.bitmap")
	installRunProtocolHeader(t, a, &block)
	if _, err := appendParcelDiscovery079(ctx, a, block, nil); err == nil {
		t.Fatal("unadmitted scan was accepted")
	}
	prepareDiscoveryFixtureRun079(t, a, block.Height, block.Height)
	h, err := appendParcelDiscovery079(ctx, a, block, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h.DistrictState != "missing" || h.DistrictCommitment != "" || h.ProvisionalCount != 1 {
		t.Fatalf("missing District presented as complete: %+v", h)
	}
	b, err := readParcelDiscoveryBlock079(ctx, a.dataDir, block.Height)
	if err != nil || b == nil || b.DistrictState != "missing" || len(b.Candidates) != 1 || !strings.Contains(b.Candidates[0].Reason, "District") {
		t.Fatalf("missing District reason absent: %+v %v", b, err)
	}
	page, next, err := parcelDiscoveryBlockPage079(ctx, a.dataDir, h.Commitment, block.Height, 0, 1)
	if err != nil || len(page) != 1 || next != 0 {
		t.Fatalf("bounded page failed: %+v %d %v", page, next, err)
	}
	if _, _, err = parcelDiscoveryBlockPage079(ctx, a.dataDir, h.Commitment, block.Height, 0, 101); err == nil {
		t.Fatal("unbounded page accepted")
	}
	path := filepath.Join(parcelDiscoveryDir079(a.dataDir), "blocks", b.Digest[:2], b.Digest+".json")
	if err = os.WriteFile(path, []byte(`{"schema":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readParcelDiscoveryBlock079(ctx, a.dataDir, block.Height); err == nil {
		t.Fatal("tampered discovery block accepted")
	}
}
