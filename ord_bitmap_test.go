package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestOrdBitmapBadgeRequiresSavedSelectedEvidence(t *testing.T) {
	a, _, b := bitmapTargetFixture(t, "0.bitmap", "0.bitmap", "1.bitmap")
	ctx := context.Background()
	for _, tc := range []struct {
		index int
		want  string
	}{{1, "0.bitmap"}, {2, ""}, {3, "1.bitmap"}} {
		idx := tc.index
		rec := ordRecord{ID: b.Transactions[idx].TxID + "i0", Height: b.Height, BlockHash: b.Hash, TxIndex: &idx, Index: 0}
		bitmapSnapshotMu.RLock()
		head, candidate := a.savedOrdBitmapCandidate(ctx, &rec, inscriptionCoordinate(uint32(idx), 0, b.Height), bitmapStandardDistrictID)
		bitmapSnapshotMu.RUnlock()
		if tc.want == "" && head != nil || tc.want != "" && (head == nil || candidate.District == nil || fmt.Sprintf("%d.bitmap", *candidate.District) != tc.want) {
			t.Fatalf("index %d: %+v", idx, candidate)
		}
	}
	// A historical Parcel store can never satisfy the replay-only badge path.
	legacyIndex := 1
	legacyRec := ordRecord{ID: b.Transactions[legacyIndex].TxID + "i0", Height: b.Height, BlockHash: b.Hash, TxIndex: &legacyIndex, Index: 0}
	bitmapSnapshotMu.RLock()
	legacyHead, _ := a.savedOrdBitmapCandidate(ctx, &legacyRec, inscriptionCoordinate(uint32(legacyIndex), 0, b.Height), bitmapStandardParcelID)
	bitmapSnapshotMu.RUnlock()
	if legacyHead != nil {
		t.Fatal("legacy Parcel identity produced recognition")
	}
	idx := 1
	rec := ordRecord{ID: b.Transactions[idx].TxID + "i0", Height: b.Height, BlockHash: testID(987654), TxIndex: &idx, Index: 0}
	a.enrichOrdBitmap(ctx, &rec)
	if len(rec.BitmapAddresses) != 0 {
		t.Fatal("wrong chain hash produced a badge", rec.BitmapAddresses)
	}
	rec.BlockHash = b.Hash
	rec.TxIndex = nil
	a.enrichOrdBitmap(ctx, &rec)
	if len(rec.BitmapAddresses) != 0 {
		t.Fatal("unlocated inscription produced a badge", rec.BitmapAddresses)
	}
}

func TestOrdBitmapWitnessIdentityBindingRealBlock(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("docs", "bitmap-evidence", "genesis-block.raw"))
	if err != nil {
		t.Fatal(err)
	}
	hash := "000000000000000000035d4eee4c3b3864b503ed5c6b35a92de219529b34ca2c"
	block, err := parseBlockDetailed(792435, hash, raw[:80], raw, "fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := extractInscriptionOccurrences(block)
	if err != nil {
		t.Fatal(err)
	}
	id := "86539aff946c437af8088955827b7e6ff48fc6192836d4071b697b5359b7a732i0"
	var record ordRecord
	for _, occurrence := range parsed.Occurrences {
		if occurrence.ID == id {
			index := occurrence.TxIndex
			record = ordRecord{ID: id, Height: block.Height, BlockHash: block.Hash, TxIndex: &index, Index: int(occurrence.Index)}
			break
		}
	}
	if record.TxIndex == nil || !ordBitmapWitnessMatches(&record, block) {
		t.Fatal("known Bitmap origin did not match authenticated witness")
	}
	corrupt := record
	corrupt.ID = testID(999) + "i0"
	if ordBitmapWitnessMatches(&corrupt, block) {
		t.Fatal("coordinate was accepted for another ID")
	}
	corrupt = record
	index := *record.TxIndex + 1
	corrupt.TxIndex = &index
	if ordBitmapWitnessMatches(&corrupt, block) {
		t.Fatal("wrong transaction position accepted")
	}
	corrupt = record
	corrupt.Index++
	if ordBitmapWitnessMatches(&corrupt, block) {
		t.Fatal("wrong inscription index accepted")
	}
	block.Verification.WitnessCommitment = false
	if ordBitmapWitnessMatches(&record, block) {
		t.Fatal("unauthenticated witness accepted")
	}
}
