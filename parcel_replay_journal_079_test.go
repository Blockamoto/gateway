package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParcelScanAttestationResumeAndLocalOrigin079(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	point := satlinePoint{TxID: testID(500), Vout: 0, Offset: 3, Height: 100, BlockHash: testID(1000), TxIndex: 1}
	blocks := map[int64]blockView{}
	previous := ""
	for h := int64(100); h <= 230; h++ {
		b := utxoFixtureBlock(h, previous, utxoFixtureCoinbase(uint64(h), 50))
		blocks[h] = b
		previous = b.Hash
	}
	point.BlockHash = blocks[100].Hash
	selected := func(h int64) (string, error) { return blocks[h].Hash, nil }
	j, err := newParcelReplayJournal079(ctx, root, point, 230, selected)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Observe(blocks[100]); err != nil {
		t.Fatal(err)
	}
	if ok, err := j.HasPrefix(101); err != nil || ok {
		t.Fatalf("unscanned block entered journal: %v %v", ok, err)
	}
	for h := int64(100); h <= 165; h++ {
		if err := j.Scanned(blocks[h]); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.Flush(); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after appending an uncommitted suffix but before its
	// authenticated checkpoint. Recovery may discard only that suffix.
	f, err := os.OpenFile(j.path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(blocks[166].Hash + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := newParcelReplayJournal079(ctx, root, point, 230, selected)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := resumed.HasPrefix(166); err != nil || !ok {
		t.Fatalf("durable prefix missing: %v %v", ok, err)
	}
	mixed := blocks[166]
	mixed.PreviousBlockHash = testID(999999)
	if err := resumed.Scanned(mixed); err == nil {
		t.Fatal("mixed-branch block accepted after restart")
	}
	for h := int64(166); h <= 230; h++ {
		if err := resumed.Scanned(blocks[h]); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := resumed.Finalize(root)
	if err != nil {
		t.Fatal(err)
	}
	negative := &parcelReplayNegative079{Point: point, Through: 230, Attestation: digest}
	if err := verifyParcelScanAttestation079(ctx, root, negative, selected); err != nil {
		t.Fatalf("local attestation: %v", err)
	}
	foreign := t.TempDir()
	var att parcelScanAttestation079
	if err := readBitmapJSON(ctx, parcelScanAttestationPath079(root, digest), &att); err != nil {
		t.Fatal(err)
	}
	if err := writeBitmapChainJSON(ctx, parcelScanAttestationPath079(foreign, digest), att); err != nil {
		t.Fatal(err)
	}
	if err := verifyParcelScanAttestation079(ctx, foreign, negative, selected); err == nil {
		t.Fatal("foreign attestation accepted")
	}
	if _, err := os.Stat(filepath.Join(foreign, "indexes", "bitmap-parent-replay-v3", "local-attestation.key")); !os.IsNotExist(err) {
		t.Fatal("read unexpectedly created a key", err)
	}
	att.HashListDigest = testID(123456)
	tampered := indexDigest(att)
	if err := writeBitmapChainJSON(ctx, parcelScanAttestationPath079(root, tampered), att); err != nil {
		t.Fatal(err)
	}
	bad := *negative
	bad.Attestation = tampered
	if err := verifyParcelScanAttestation079(ctx, root, &bad, selected); err == nil {
		t.Fatal("tampered attestation accepted")
	}
	reorg := func(h int64) (string, error) {
		if h == 230 {
			return testID(999999), nil
		}
		return selected(h)
	}
	if err := verifyParcelScanAttestation079(ctx, root, negative, reorg); err == nil {
		t.Fatal("replaced endpoint accepted")
	}
	// A forged journal ending at the correct selected hash cannot be converted
	// into a local attestation after restart because the checkpoint MAC binds
	// the digest of every record, not just the last record.
	f, err = os.OpenFile(j.path, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte(testID(777)+"\n"), 0); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	tamperedJournal, err := newParcelReplayJournal079(ctx, root, point, 230, selected)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tamperedJournal.HasPrefix(231); err == nil {
		t.Fatal("tampered journal accepted")
	}
}
