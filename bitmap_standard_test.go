package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBitmapStandardV1FrozenProfilesAndFreshIdentity(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	frozen := []struct {
		id, hash string
		start    int64
	}{
		{bitmapCanonicalV1DistrictID, "eee99fd265545a86f71373406a3b302a30ffe11dfbdd4f06eafc2b1780ee961f", 0},
		{bitmapCanonicalV1ParcelID, "ced973c270616540a3580022dfc3a035fb4e9eb26c45f0d3795208be12565307", 0},
		{bitmapCanonicalV2DistrictID, "ab1154b7a3d6ea6256a191e6bb07e6d0199067af95dcc4cfd0245e1f48591b2c", 792435},
		{bitmapCanonicalV2ParcelID, "99a8ff74f55c4de451f9b26b446ae71e8e820d91a1fa8f6aaa65dd6a8535e051", 792435},
	}
	savedHeads := map[string][]byte{}
	for _, old := range frozen {
		instance, err := bitmapResolvedInstance(ctx, a.dataDir, old.id)
		if err != nil || instance.ProfileHash != old.hash || instance.StartHeight != old.start || !instance.Legacy || releaseFeatureAvailable(old.id) {
			t.Fatal("frozen identity changed or became executable", old.id, instance, err)
		}
		block := bitmapDevelopmentFixtureBlock(old.start, "", "0.bitmap")
		installRunProtocolHeader(t, a, &block)
		store := bitmapTestConsumer(t, a.dataDir, old.id)
		if err := store.Apply(ctx, block, nil); err != nil {
			t.Fatal(old.id, err)
		}
		raw, err := os.ReadFile(filepath.Join(store.dir, "head.json"))
		if err != nil {
			t.Fatal(err)
		}
		savedHeads[old.id] = raw
		if _, err := openBitmapRunConsumer(ctx, a, old.id); err == nil {
			t.Fatal("previous generation execution admitted", old.id)
		}
	}
	district := bitmapTestConsumer(t, a.dataDir, bitmapStandardDistrictID)
	parcel := bitmapTestConsumer(t, a.dataDir, bitmapStandardParcelID)
	if district.head != nil || parcel.head != nil || district.NextHeight() != 792435 || parcel.NextHeight() != 792435 {
		t.Fatal("new Standard silently adopted an old saved head", district.head, parcel.head)
	}
	if district.instance.Label != "Bitmap Districts" || parcel.instance.Label != "Bitmap Parcels" || parcel.instance.DistrictInstance != bitmapStandardDistrictID || parcel.instance.DistrictProfileHash != district.instance.ProfileHash {
		t.Fatal("Standard labels/dependency identity mismatch", district.instance, parcel.instance)
	}
	if district.instance.ProfileHash == frozen[2].hash || parcel.instance.ProfileHash == frozen[3].hash || indexDigest(district.instance.Profile.Rules) != indexDigest(bitmapCanonicalV2Profile("district").Rules) || indexDigest(parcel.instance.Profile.Rules) != indexDigest(bitmapCanonicalV2Profile("parcel").Rules) {
		t.Fatal("new generation reused old identity or changed published rules")
	}
	block := bitmapDevelopmentFixtureBlock(792435, "", "0.bitmap")
	installRunProtocolHeader(t, a, &block)
	if err := parcel.Apply(ctx, block, nil); !errors.Is(err, ErrBitmapEvidenceRequired) {
		t.Fatal("Standard Parcel accepted only an old-generation District producer", err)
	}
	if err := district.Apply(ctx, block, nil); err != nil {
		t.Fatal(err)
	}
	if err := parcel.Apply(ctx, block, nil); err != nil {
		t.Fatal("Standard Parcel did not accept its exact Standard District producer", err)
	}
	if parcel.head.DistrictInstance != bitmapStandardDistrictID || parcel.head.LastBlock.WinnerBlocators == nil || district.head.LastBlock.WinnerBlocators[0] != "1i0.792435" {
		t.Fatal("Standard storage/reference generation mismatch", district.head, parcel.head)
	}
	for id, before := range savedHeads {
		after, err := os.ReadFile(filepath.Join(bitmapDevelopmentDir(a.dataDir, id), "head.json"))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("Standard evaluation rewrote a frozen head", id, err)
		}
		if _, err := readBitmapDevelopmentHead(ctx, a.dataDir, id); err != nil {
			t.Fatal("frozen saved state became unreadable", id, err)
		}
	}
	p := indexRunProgress{Index: bitmapStandardParcelID}
	if _, err := bindIndexRunBitmapProgress(ctx, a.dataDir, &p); err != nil {
		t.Fatal(err)
	}
	p.DistrictInstance, p.DistrictProfileHash = bitmapCanonicalV2DistrictID, frozen[2].hash
	if err := validateIndexRunBitmapBinding(ctx, a.dataDir, p); !errors.Is(err, errIndexRunProfileChanged) {
		t.Fatal("Standard Parcel plan accepted a frozen District binding", err)
	}
	t.Log("Standard profile hashes", district.instance.ProfileHash, parcel.instance.ProfileHash)
}

func TestBitmapStandardV1RejectsCopiedCheckpointAndWrongOrigin(t *testing.T) {
	ctx, root := context.Background(), t.TempDir()
	old := bitmapTestConsumer(t, root, bitmapCanonicalV2DistrictID)
	if err := old.Apply(ctx, bitmapDevelopmentFixtureBlock(792435, "", "0.bitmap"), nil); err != nil {
		t.Fatal(err)
	}
	newRoot := t.TempDir()
	copyDir := bitmapDevelopmentDir(newRoot, bitmapStandardDistrictID)
	oldCommit := old.head.Commitment
	if err := atomicWriteJSON(filepath.Join(copyDir, "commits", oldCommit[:2], oldCommit+".json"), old.head); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteJSON(filepath.Join(copyDir, "head.json"), map[string]string{"commitment": oldCommit}); err != nil {
		t.Fatal(err)
	}
	if _, err := readBitmapDevelopmentHead(ctx, newRoot, bitmapStandardDistrictID); err == nil {
		t.Fatal("old checkpoint copied into Standard path was adopted")
	}
	standard := bitmapTestConsumer(t, t.TempDir(), bitmapStandardDistrictID)
	if err := standard.Apply(ctx, bitmapDevelopmentFixtureBlock(792435, "", "0.bitmap"), nil); err != nil {
		t.Fatal(err)
	}
	wrong := *standard.head
	wrong.SegmentStart = 0
	wrong.Commitment = bitmapDevelopmentDigest(wrong)
	if err := atomicWriteJSON(filepath.Join(standard.dir, "commits", wrong.Commitment[:2], wrong.Commitment+".json"), wrong); err != nil {
		t.Fatal(err)
	}
	if _, err := readBitmapDevelopmentCommit(ctx, standard.root, bitmapStandardDistrictID, wrong.Commitment); err == nil {
		t.Fatal("rehashed wrong-origin prototype was accepted as Standard V1")
	}
}

func TestBitmapStandardV1SavedCanonicalRunDoesNotRetargetOrResume(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: bitmapCanonicalV2DistrictID, Armed: true}, {Index: bitmapCanonicalV2ParcelID, Armed: true}}}
	progress := []indexRunProgress{}
	for _, c := range req.Consumers {
		p := indexRunProgress{Index: c.Index, State: "waiting", Height: 792434}
		if _, err := bindIndexRunBitmapProgress(ctx, a.dataDir, &p); err != nil {
			t.Fatal(err)
		}
		progress = append(progress, p)
	}
	doc := indexRunDocument{Schema: 1, Configured: true, Prepared: req, Run: &indexRunRecord{ID: "saved-canonical-v2", State: "running", Request: req, Consumers: progress, InitialTargetHeight: 792435, TargetHeight: 792435}}
	doc.Checksum = indexRunDigest(doc)
	if err := atomicWriteJSON(indexRunPath(a.dataDir), doc); err != nil {
		t.Fatal(err)
	}
	a.indexRunLoaded = false
	saved := a.indexRunSnapshot()
	if saved.Error != "" || saved.Run == nil || saved.Run.State != "paused" || saved.Run.Consumers[0].Index != bitmapCanonicalV2DistrictID || saved.Run.Consumers[1].Index != bitmapCanonicalV2ParcelID {
		t.Fatal("saved generation identity was lost or retargeted", saved)
	}
	if _, err := a.controlIndexRun(ctx, saved.Run.ID, "resume"); err == nil {
		t.Fatal("frozen canonical-v2 run resumed")
	}
	if _, err := a.controlIndexRun(ctx, saved.Run.ID, "cancel"); err != nil {
		t.Fatal("old saved intent could not be cancelled", err)
	}
	for _, id := range []string{bitmapStandardDistrictID, bitmapStandardParcelID} {
		if _, err := os.Stat(bitmapDevelopmentDir(a.dataDir, id)); !os.IsNotExist(err) {
			t.Fatal("old run load/resume/cancel created Standard state", id, err)
		}
	}
}
