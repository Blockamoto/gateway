package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func bitmapEditRequest(i bitmapProfileInstance, text int) bitmapProfileRequest {
	rules := bitmapCopyAnswers(i.Rules)
	rules["text"] = text
	return bitmapProfileRequest{Action: "update", AutoLabel: i.AutoLabel, ID: i.ID, Kind: i.Kind, Label: i.Label, Rules: rules, DistrictInstance: i.DistrictInstance, ExpectedProfile: i.ProfileHash, ExpectedConfigurationRevision: bitmapEffectiveConfigurationRevision(i)}
}

func bitmapLifecycleInstance(t *testing.T, a *app, id string) bitmapProfileInstance {
	t.Helper()
	instances, err := bitmapProfileInstances(context.Background(), a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range a.bitmapProfileLifecycleViews(context.Background(), instances) {
		if i.ID == id {
			return i
		}
	}
	t.Fatal("missing lifecycle instance", id)
	return bitmapProfileInstance{}
}

func TestBitmapLifecycleUnbuiltStableIDAtomicDependentsAndRename(t *testing.T) {
	ctx := context.Background()
	a, _, _ := timelineFixture(t, 1)
	district := bitmapTestCreateProfile(t, a.dataDir, "Standard named settings", 1, true)
	before, err := bitmapProfileInstances(ctx, a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	var parcel bitmapProfileInstance
	for _, i := range before {
		if i.Kind == "parcel" && i.DistrictInstance == district.ID {
			parcel = i
		}
	}
	if view := bitmapLifecycleInstance(t, a, district.ID); !view.RulesEditable || view.Classification != "standard" || view.ConfigurationRevision != 1 {
		t.Fatal(view)
	}
	out, err := a.updateBitmapProfile(ctx, bitmapEditRequest(district, 0))
	if err != nil {
		t.Fatal(err)
	}
	after := out["instance"].(bitmapProfileInstance)
	if after.ID != district.ID || after.ProfileHash == district.ProfileHash || after.ConfigurationRevision != 2 || after.Classification != "non_standard" || len(out["affected_instances"].([]bitmapProfileInstance)) != 3 {
		t.Fatal(out)
	}
	chain := bitmapLifecycleInstance(t, a, district.ChainInstance)
	changedParcel := bitmapLifecycleInstance(t, a, parcel.ID)
	if chain.ProfileHash != after.ProfileHash || chain.DistrictInstance != after.ID || chain.DistrictProfileHash != after.ProfileHash || chain.ConfigurationRevision != 2 || changedParcel.ConfigurationRevision != 2 || changedParcel.ProfileHash == parcel.ProfileHash || changedParcel.DistrictProfileHash != after.ProfileHash || !reflect.DeepEqual(changedParcel.Rules, parcel.Rules) {
		t.Fatal("dependent bindings were orphaned or Parcel answers changed", chain, changedParcel)
	}
	if _, err := a.updateBitmapProfile(ctx, bitmapEditRequest(district, 0)); err == nil {
		t.Fatal("stale pre-edit configuration saved")
	}
	if _, err := bitmapSaveProfile(ctx, a.dataDir, bitmapProfileRequest{Action: "rename", ID: after.ID, Label: "A freely chosen name"}); err != nil {
		t.Fatal(err)
	}
	renamed := bitmapLifecycleInstance(t, a, after.ID)
	if renamed.ProfileHash != after.ProfileHash || renamed.ConfigurationRevision != 2 || renamed.Classification != "non_standard" || !renamed.RulesEditable {
		t.Fatal("renaming altered execution or classification", renamed)
	}
	back, err := a.updateBitmapProfile(ctx, bitmapEditRequest(renamed, 1))
	if err != nil {
		t.Fatal(err)
	}
	restored := back["instance"].(bitmapProfileInstance)
	if restored.ProfileHash != district.ProfileHash || restored.ConfigurationRevision != 3 || restored.Classification != "standard" || restored.Label != renamed.Label {
		t.Fatal("returning to reference settings lost independent identity/name", restored)
	}
	for _, i := range out["affected_instances"].([]bitmapProfileInstance) {
		if _, err := os.Stat(bitmapDevelopmentDir(a.dataDir, i.ID)); !os.IsNotExist(err) {
			t.Fatal("editing created index evidence", i.ID, err)
		}
	}
	if a.indexRunSnapshot().Run != nil || a.indexJobSnapshot().ID != "" {
		t.Fatal("editing implicitly launched indexing")
	}
}

func TestBitmapLifecycleDependentEvidenceAndUnsupportedEditsAreAtomic(t *testing.T) {
	ctx := context.Background()
	a, _, _ := timelineFixture(t, 1)
	i := bitmapTestCreateProfile(t, a.dataDir, "Parent", 1, true)
	instances, _ := bitmapProfileInstances(ctx, a.dataDir)
	var parcel bitmapProfileInstance
	for _, p := range instances {
		if p.Kind == "parcel" && p.DistrictInstance == i.ID {
			parcel = p
		}
	}
	initial, _ := os.ReadFile(bitmapProfilePath(a.dataDir))
	q := bitmapEditRequest(i, 1)
	q.Rules["burns"] = 1
	if _, err := a.updateBitmapProfile(ctx, q); err == nil {
		t.Fatal("unsupported update became executable")
	}
	after, _ := os.ReadFile(bitmapProfilePath(a.dataDir))
	if string(after) != string(initial) {
		t.Fatal("unsupported update changed registry")
	}
	// Even damaged or partial dependent evidence cannot be silently reinterpreted.
	if err := atomicWriteBytes(filepath.Join(bitmapDevelopmentDir(a.dataDir, parcel.ID), "pending.json"), []byte("partial evidence")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.updateBitmapProfile(ctx, bitmapEditRequest(i, 0)); err == nil {
		t.Fatal("District rebind orphaned saved Parcel evidence")
	}
	after, _ = os.ReadFile(bitmapProfilePath(a.dataDir))
	if string(after) != string(initial) || bitmapLifecycleInstance(t, a, i.ID).RulesEditable {
		t.Fatal("blocked edit partially changed dependent registry")
	}
	other := bitmapTestCreateProfile(t, a.dataDir, "Different parent", 1, false)
	reparent := bitmapEditRequest(parcel, 0)
	reparent.DistrictInstance = other.ID
	if _, err := a.updateBitmapProfile(ctx, reparent); err == nil {
		t.Fatal("stable Parcel track was silently reparented")
	}
}

func TestBitmapLifecycleFirstSavedEvidenceLocksPersistentlyAndStaleConsumerCannotWrite(t *testing.T) {
	ctx := context.Background()
	a, _, _ := timelineFixture(t, 1)
	i := bitmapTestCreateProfile(t, a.dataDir, "Mutable until data", 1, false)
	stale := bitmapTestConsumer(t, a.dataDir, i.ID)
	if _, err := a.updateBitmapProfile(ctx, bitmapEditRequest(i, 0)); err != nil {
		t.Fatal(err)
	}
	block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	if err := stale.Apply(ctx, block, nil); err == nil {
		t.Fatal("stale opened consumer wrote under edited rules")
	}
	if _, err := os.Stat(bitmapDevelopmentDir(a.dataDir, i.ID)); !os.IsNotExist(err) {
		t.Fatal("stale consumer persisted old interpretation", err)
	}
	current := bitmapTestConsumer(t, a.dataDir, i.ID)
	if err := current.Apply(ctx, block, nil); err != nil {
		t.Fatal(err)
	}
	locked := bitmapLifecycleInstance(t, a, i.ID)
	if locked.RulesEditable || !locked.RulesLocked || locked.ConfigurationRevision != 2 {
		t.Fatal("first durable block did not persist lock", locked)
	}
	// Only a disposable profile is cleared. The lock lives in the separate
	// registry and survives consumer data deletion and an application restart.
	target, _ := filepath.Abs(bitmapDevelopmentDir(a.dataDir, i.ID))
	profile, _ := filepath.Abs(a.dataDir)
	if !strings.HasPrefix(target, profile+string(os.PathSeparator)) {
		t.Fatal("test deletion escaped disposable profile")
	}
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	if _, err := a.updateBitmapProfile(ctx, bitmapEditRequest(locked, 1)); err == nil {
		t.Fatal("deleting evidence unlocked an indexed interpretation")
	}
	if _, err := bitmapSaveProfile(ctx, a.dataDir, bitmapProfileRequest{Action: "rename", ID: i.ID, Label: "Still renamable"}); err != nil {
		t.Fatal("indexed track could not be renamed", err)
	}
	if !bitmapLifecycleInstance(t, a, i.ID).RulesLocked {
		t.Fatal("rename lost persistent lock")
	}
}

func TestBitmapLifecycleMutationAPIAndStaleComparisonBindings(t *testing.T) {
	ctx := context.Background()
	a, _, _ := timelineFixture(t, 1)
	i := bitmapTestCreateProfile(t, a.dataDir, "Reference", 1, false)
	v := bitmapTestCreateProfile(t, a.dataDir, "Other", 0, false)
	q := bitmapEditRequest(i, 0)
	raw, _ := json.Marshal(q)
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/bitmap/profiles", strings.NewReader(string(raw)))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.handleBitmapProfiles(w, r)
		return w
	}
	w := request()
	var result struct{ Instance bitmapProfileInstance }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.Instance.RulesEditable || result.Instance.Classification != "non_standard" || result.Instance.ConfigurationRevision != 2 {
		t.Fatal("mutation lacks lifecycle fields", w.Code, w.Body.String())
	}
	if w := request(); w.Code != http.StatusConflict {
		t.Fatal("stale edit did not return conflict", w.Code, w.Body.String())
	}
	expected := map[string]bitmapConfigurationBinding{"baseline": bitmapConfigurationOf(i), "variant": bitmapConfigurationOf(v)}
	compare, err := a.bitmapCompareBoundContext(ctx, i.ID, v.ID, nil, nil, 10, "", nil, "", expected)
	if err != nil || compare.State != "reset_required" || len(compare.Rows) != 0 || compare.Compared != 0 {
		t.Fatal("old selection silently adopted new interpretation", compare, err)
	}
	// Returning to exactly the former R still needs a new configuration review.
	current := bitmapLifecycleInstance(t, a, i.ID)
	if _, err := a.updateBitmapProfile(ctx, bitmapEditRequest(current, 1)); err != nil {
		t.Fatal(err)
	}
	compare, err = a.bitmapCompareBoundContext(ctx, i.ID, v.ID, nil, nil, 10, "", nil, "", expected)
	if err != nil || compare.State != "reset_required" || compare.Bindings["baseline"].ConfigurationRevision != 3 {
		t.Fatal("old revision accepted after matching-R edit cycle", compare, err)
	}
}

func TestBitmapLifecycleGeneratedLabelTracksClassificationWithoutChangingIdentity(t *testing.T) {
	ctx := context.Background()
	a, _, _ := timelineFixture(t, 1)
	out, err := bitmapSaveProfile(ctx, a.dataDir, bitmapProfileRequest{Action: "create", AutoLabel: true, Kind: "district", Label: "Bitmap District Standard 12", Rules: bitmapReferenceAnswers("district")})
	if err != nil {
		t.Fatal(err)
	}
	i := out["instance"].(bitmapProfileInstance)
	out, err = a.updateBitmapProfile(ctx, bitmapEditRequest(i, 0))
	if err != nil {
		t.Fatal(err)
	}
	changed := out["instance"].(bitmapProfileInstance)
	if !changed.AutoLabel || changed.Label != "Bitmap District Non-standard 12" || changed.ID != i.ID || changed.ConfigurationRevision != 2 {
		t.Fatal("generated label misrepresented changed rules", changed)
	}
	if _, err := bitmapSaveProfile(ctx, a.dataDir, bitmapProfileRequest{Action: "rename", ID: i.ID, Label: "My Standard study"}); err != nil {
		t.Fatal(err)
	}
	renamed := bitmapLifecycleInstance(t, a, i.ID)
	if renamed.AutoLabel || renamed.ProfileHash != changed.ProfileHash || renamed.ConfigurationRevision != 2 {
		t.Fatal("rename changed interpretation", renamed)
	}
	out, err = a.updateBitmapProfile(ctx, bitmapEditRequest(renamed, 1))
	if err != nil {
		t.Fatal(err)
	}
	restored := out["instance"].(bitmapProfileInstance)
	if restored.AutoLabel || restored.Label != "My Standard study" || restored.Classification != "standard" || restored.ProfileHash != i.ProfileHash {
		t.Fatal("rules edited explicit name", restored)
	}
	bad := bitmapEditRequest(restored, 0)
	bad.AutoLabel = true
	if _, err := a.updateBitmapProfile(ctx, bad); err == nil {
		t.Fatal("arbitrary name trusted as an automatically managed label")
	}
}

func TestBitmapLifecyclePendingOnlyEvidenceLocksParentRebinding(t *testing.T) {
	ctx := context.Background()
	a, _, _ := timelineFixture(t, 1)
	district := bitmapTestCreateProfile(t, a.dataDir, "Unbuilt parent", 1, true)
	instances, _ := bitmapProfileInstances(ctx, a.dataDir)
	var parcel bitmapProfileInstance
	for _, i := range instances {
		if i.Kind == "parcel" && i.DistrictInstance == district.ID {
			parcel = i
		}
	}
	consumer := bitmapTestConsumer(t, a.dataDir, parcel.ID)
	if err := consumer.Apply(ctx, bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, ""), nil); err == nil {
		t.Fatal("missing District outcome unexpectedly completed")
	}
	if consumer.head != nil {
		t.Fatal("pending evaluation fabricated a checkpoint")
	}
	if _, err := os.Stat(filepath.Join(consumer.dir, "pending.json")); err != nil {
		t.Fatal("pending evidence was not retained", err)
	}
	if view := bitmapLifecycleInstance(t, a, parcel.ID); !view.RulesLocked || view.RulesEditable {
		t.Fatal("pending-only interpretation remained editable", view)
	}
	if _, err := a.updateBitmapProfile(ctx, bitmapEditRequest(district, 0)); err == nil {
		t.Fatal("parent edit stranded pending dependent evidence")
	}
}

func TestBitmapLifecycleRegistryReadsSerializeWithAtomicMetadataReplacement(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	i := bitmapTestCreateProfile(t, root, "Concurrent display metadata", 1, false)
	errors := make(chan error, 4)
	var readers sync.WaitGroup
	for n := 0; n < 4; n++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for n := 0; n < 40; n++ {
				if _, err := bitmapProfileInstances(ctx, root); err != nil {
					errors <- err
					return
				}
			}
		}()
	}
	for n := 0; n < 12; n++ {
		if _, err := bitmapSaveProfile(ctx, root, bitmapProfileRequest{Action: "rename", ID: i.ID, Label: strings.Repeat("A", n+1)}); err != nil {
			t.Fatal("atomic metadata replacement collided with a reader", err)
		}
	}
	readers.Wait()
	close(errors)
	for err := range errors {
		t.Fatal("reader observed partial metadata", err)
	}
	current, err := bitmapResolvedInstance(ctx, root, i.ID)
	if err != nil || current.ProfileHash != i.ProfileHash || current.ConfigurationRevision != 1 {
		t.Fatal("display metadata changed executable configuration", current, err)
	}
}
