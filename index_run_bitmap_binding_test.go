package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTimelineRunBitmapBindingRejectsUnrecordedCustomInterpretation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	created, err := bitmapSaveStoredProfile(ctx, root, bitmapProfileRequest{Action: "create", Kind: "district", Label: "Reviewed rules", Rules: bitmapReferenceAnswers("district")})
	if err != nil {
		t.Fatal(err)
	}
	i := created["instance"].(bitmapProfileInstance)
	p := indexRunProgress{Index: i.ID}
	if err = validateIndexRunBitmapBinding(ctx, root, p); !errors.Is(err, errIndexRunProfileChanged) {
		t.Fatal("unrecorded custom rules were silently adopted", err)
	}
	if _, err = bindIndexRunBitmapProgress(ctx, root, &p); err != nil {
		t.Fatal(err)
	}
	if p.ProfileHash != i.ProfileHash || p.ConfigurationRevision != 1 {
		t.Fatal("review omitted exact interpretation", p)
	}
	if err = validateIndexRunBitmapBinding(ctx, root, p); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"bitmap-districts", "bitmap-parcels", "bitmap-chain", "utxo-state"} {
		if err = validateIndexRunBitmapBinding(ctx, root, indexRunProgress{Index: id}); err != nil {
			t.Fatal("immutable legacy/non-Bitmap run lost compatibility", id, err)
		}
	}
}

func TestTimelineRunBitmapStoredEditInvalidatesBindingsWhileAdmissionIsDeferred(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	i := bitmapTestCreateProfile(t, a.dataDir, "Stored interpretation", 1, false)
	old := indexRunProgress{Index: i.ID}
	if _, err := bindIndexRunBitmapProgress(ctx, a.dataDir, &old); err != nil {
		t.Fatal(err)
	}
	rules := bitmapReferenceAnswers("district")
	rules["text"] = 0
	out, err := a.updateBitmapStoredProfile(ctx, bitmapProfileRequest{Action: "update", ID: i.ID, Kind: i.Kind, Label: i.Label, Rules: rules, ExpectedProfile: i.ProfileHash, ExpectedConfigurationRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	updated := out["instance"].(bitmapProfileInstance)
	if err = validateIndexRunBitmapBinding(ctx, a.dataDir, old); !errors.Is(err, errIndexRunProfileChanged) {
		t.Fatal("stored edit reused old binding", err)
	}
	fresh := indexRunProgress{Index: i.ID}
	if _, err = bindIndexRunBitmapProgress(ctx, a.dataDir, &fresh); err != nil || fresh.ProfileHash != updated.ProfileHash || fresh.ConfigurationRevision != 2 {
		t.Fatal(fresh, err)
	}
	var calls atomic.Int64
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) { calls.Add(1); return blockView{}, nil }
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: i.ID, Armed: true}, {Index: i.ChainInstance, Armed: true}}}
	if _, err = a.planIndexRun(ctx, req); err == nil {
		t.Fatal("custom plan admitted")
	}
	if _, err = a.startIndexRun(ctx, req, ""); err == nil || calls.Load() != 0 || a.indexRunSnapshot().Run != nil {
		t.Fatal("deferred interpretation started work", err, calls.Load())
	}
}

func TestTimelineRunBitmapPausedReservationRequiresCancellationBeforeStoredEdit(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	i := bitmapTestCreateProfile(t, a.dataDir, "Reserved interpretation", 1, false)
	p := indexRunProgress{Index: i.ID}
	if _, err := bindIndexRunBitmapProgress(ctx, a.dataDir, &p); err != nil {
		t.Fatal(err)
	}
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: i.ID, Armed: true, Live: true}}}
	a.indexRunMu.Lock()
	a.indexRunLoaded = true
	a.indexRunState = indexRunDocument{Schema: 1, Configured: true, Prepared: req, Run: &indexRunRecord{ID: "stored-paused", State: "paused", Request: req, Consumers: []indexRunProgress{p}}}
	err := a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	paused := a.indexRunSnapshot()
	edit := bitmapEditRequest(i, 0)
	if _, err = a.updateBitmapStoredProfile(ctx, edit); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatal("stored reservation lost", err)
	}
	if _, err = a.controlIndexRun(ctx, paused.Run.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.updateBitmapStoredProfile(ctx, edit); err != nil {
		t.Fatal("cancel did not release unbuilt binding", err)
	}
	if _, err = a.resumeIndexRun(ctx, paused.Run); err == nil {
		t.Fatal("unavailable old run resumed")
	}
}

func TestTimelineRunBitmapStaleRestoredRunCannotResumeFetchOrPublish(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	i := bitmapTestCreateProfile(t, a.dataDir, "Restored intent", 1, false)
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	installRunProtocolHeader(t, a, &b)
	old := indexRunProgress{Index: i.ID}
	if _, err := bindIndexRunBitmapProgress(ctx, a.dataDir, &old); err != nil {
		t.Fatal(err)
	}
	consumer := bitmapTestConsumer(t, a.dataDir, i.ID)
	if _, err := a.updateBitmapStoredProfile(ctx, bitmapEditRequest(i, 0)); err != nil {
		t.Fatal(err)
	}
	if err := validateIndexRunBitmapBinding(ctx, a.dataDir, old); !errors.Is(err, errIndexRunProfileChanged) {
		t.Fatal(err)
	}
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: i.ID, Armed: true, Live: true}}}
	r := &indexRunRecord{ID: "restored-old-intent", State: "paused", Request: req, InitialTargetHeight: b.Height, TargetHeight: b.Height, TargetHash: b.Hash, Consumers: []indexRunProgress{old}}
	a.indexRunMu.Lock()
	a.indexRunLoaded = true
	a.indexRunState = indexRunDocument{Schema: 1, Configured: true, Prepared: req, Run: r}
	err := a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	bound := a.indexRunBoundConsumerContext(ctx, old)
	if err = checkIndexRunPublication(bound); err == nil || err.Error() != errBitmapComparisonDeferred.Error() {
		t.Fatal("publication accepted unavailable custom", err)
	}
	if err = consumer.Apply(bound, b, nil); err == nil {
		t.Fatal("opened custom consumer published")
	}
	var calls atomic.Int64
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) { calls.Add(1); return b, nil }
	if _, err = a.resumeIndexRun(ctx, r); err == nil {
		t.Fatal("restored custom run resumed")
	}
	if got := a.indexRunSnapshot(); got.Error != "" || got.Run.State != "paused" || !got.Run.Request.Consumers[0].Live {
		t.Fatal("rejection lost saved intent", got)
	}
	a.indexRunMu.Lock()
	a.indexRunState.Run.State = "running"
	a.indexRunMu.Unlock()
	a.runCoordinatedIndexes(ctx, r.ID)
	got := a.indexRunSnapshot()
	if calls.Load() != 0 || got.Run.Counts.SourceReads != 0 || got.Run.State != "complete_with_errors" {
		t.Fatal("restored worker fetched or continued", got.Run, calls.Load())
	}
	for _, name := range []string{"head.json", "pending.json", "pages", "commits"} {
		if _, err = os.Stat(filepath.Join(bitmapDevelopmentDir(a.dataDir, i.ID), name)); !os.IsNotExist(err) {
			t.Fatal("disabled work left derived evidence", name, err)
		}
	}
}

func TestTimelineRunBitmapConfigurationCycleCannotReusePriorBinding(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	out, err := bitmapSaveStoredProfile(ctx, a.dataDir, bitmapProfileRequest{Action: "create", Kind: "district", Label: "Same R newer configuration", Rules: bitmapReferenceAnswers("district"), LinkedParcel: true})
	if err != nil {
		t.Fatal(err)
	}
	i := out["instance"].(bitmapProfileInstance)
	parcel := out["linked_parcel"].(bitmapProfileInstance)
	old := []indexRunProgress{{Index: i.ID}, {Index: i.ChainInstance}, {Index: parcel.ID}}
	for n := range old {
		if _, err = bindIndexRunBitmapProgress(ctx, a.dataDir, &old[n]); err != nil {
			t.Fatal(err)
		}
	}
	current := i
	for _, value := range []int{0, 1} {
		out, err = a.updateBitmapStoredProfile(ctx, bitmapEditRequest(current, value))
		if err != nil {
			t.Fatal(err)
		}
		current = out["instance"].(bitmapProfileInstance)
	}
	if current.ProfileHash != i.ProfileHash || current.ConfigurationRevision != 3 {
		t.Fatal(current)
	}
	for _, p := range old {
		if err = validateIndexRunBitmapBinding(ctx, a.dataDir, p); !errors.Is(err, errIndexRunProfileChanged) {
			t.Fatal("old same-R binding accepted", p.Index, err)
		}
	}
}

func TestTimelineRunBitmapResumableRecordsReserveExactProducerBindings(t *testing.T) {
	for _, state := range []string{"running", "waiting", "pausing", "paused", "failed", "complete_with_errors", "complete", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			a := &app{dataDir: t.TempDir(), indexRunLoaded: true}
			a.indexRunState = indexRunDocument{Schema: 1, Run: &indexRunRecord{ID: "admitted", State: state, Request: indexRunRequest{Consumers: []indexRunSelection{{Index: "child", Armed: false}}}, Consumers: []indexRunProgress{{Index: "child", DistrictInstance: "producer"}}}}
			for _, id := range []string{"child", "producer"} {
				reserved, reason, err := a.indexRunBitmapBindingsReserved([]string{id})
				want := state != "complete" && state != "cancelled"
				if err != nil || reserved != want || reserved && reason == "" {
					t.Fatal(id, reserved, reason, err)
				}
				if reserved && (strings.Contains(reason, "admitted") || strings.Contains(reason, "child") || strings.Contains(reason, "producer")) {
					t.Fatal("ordinary editor exposes internal identities", reason)
				}
			}
			if reserved, _, err := a.indexRunBitmapBindingsReserved([]string{"independent"}); err != nil || reserved {
				t.Fatal("independent interpretation was reserved", reserved, err)
			}
		})
	}
}
