package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTimelineRunBitmapBindingRejectsUnrecordedCustomInterpretation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	created, err := bitmapSaveProfile(ctx, root, bitmapProfileRequest{Action: "create", Kind: "district", Label: "Reviewed rules", Rules: bitmapReferenceAnswers("district")})
	if err != nil {
		t.Fatal(err)
	}
	i := created["instance"].(bitmapProfileInstance)
	p := indexRunProgress{Index: i.ID}
	if err = validateIndexRunBitmapProgress(ctx, root, p); !errors.Is(err, errIndexRunProfileChanged) {
		t.Fatal("unrecorded custom rules were silently adopted", err)
	}
	if _, err = bindIndexRunBitmapProgress(ctx, root, &p); err != nil {
		t.Fatal(err)
	}
	if p.ProfileHash != i.ProfileHash || p.ConfigurationRevision != 1 {
		t.Fatal("review omitted exact interpretation", p)
	}
	if err = validateIndexRunBitmapProgress(ctx, root, p); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"bitmap-districts", "bitmap-parcels", "bitmap-chain", "utxo-state"} {
		if err = validateIndexRunBitmapProgress(ctx, root, indexRunProgress{Index: id}); err != nil {
			t.Fatal("immutable legacy/non-Bitmap run lost compatibility", id, err)
		}
	}
}

func TestTimelineRunBitmapEditInvalidatesReviewedPlanAndPreservesSingleSourcePass(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	created, err := bitmapSaveProfile(ctx, a.dataDir, bitmapProfileRequest{Action: "create", Kind: "district", Label: "Editable interpretation", Rules: bitmapReferenceAnswers("district")})
	if err != nil {
		t.Fatal(err)
	}
	i := created["instance"].(bitmapProfileInstance)
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	installRunProtocolHeader(t, a, &b)
	var calls atomic.Int64
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) { calls.Add(1); return b, nil }
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: i.ID, Armed: true}, {Index: i.ChainInstance, Armed: true}}}
	p, err := a.planIndexRun(ctx, req)
	if err != nil || !p.Ready {
		t.Fatal(p, err)
	}
	rules := bitmapReferenceAnswers("district")
	rules["text"] = 0
	edited, err := a.updateBitmapProfile(ctx, bitmapProfileRequest{Action: "update", ID: i.ID, Kind: i.Kind, Label: i.Label, Rules: rules, ExpectedProfile: i.ProfileHash, ExpectedConfigurationRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	updated := edited["instance"].(bitmapProfileInstance)
	if _, err = a.startIndexRun(ctx, req, p.PlanID); err == nil || calls.Load() != 0 || a.indexRunSnapshot().Run != nil {
		t.Fatal("stale reviewed plan started source work", err, calls.Load())
	}
	fresh, err := a.planIndexRun(ctx, req)
	if err != nil || !fresh.Ready || fresh.PlanID == p.PlanID {
		t.Fatal("edit did not invalidate review", fresh, err)
	}
	for _, q := range fresh.Consumers {
		if q.ProfileHash != updated.ProfileHash || q.ConfigurationRevision != 2 {
			t.Fatal("new review omitted interpretation revision", q)
		}
		if q.Index == i.ChainInstance && (q.DistrictInstance != i.ID || q.DistrictProfileHash != updated.ProfileHash || q.DistrictConfigurationRevision != 2) {
			t.Fatal("attached Chain lost exact producer revision", q)
		}
	}
	if _, err = a.startIndexRun(ctx, req, fresh.PlanID); err != nil {
		t.Fatal(err)
	}
	d := waitRun(t, a)
	if d.Run.State != "complete" || calls.Load() != 1 || d.Run.Counts.ChainSteps != 1 {
		t.Fatal("new interpretation did not share one source pass", d.Run, calls.Load())
	}
}

func TestTimelineRunBitmapPausedReservationRequiresCancellationBeforeEdit(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	created, err := bitmapSaveProfile(ctx, a.dataDir, bitmapProfileRequest{Action: "create", Kind: "district", Label: "Reserved interpretation", Rules: bitmapReferenceAnswers("district")})
	if err != nil {
		t.Fatal(err)
	}
	i := created["instance"].(bitmapProfileInstance)
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &b)
	started := make(chan struct{})
	a.indexRunSourceBlock = func(ctx context.Context, _ blockTarget, _ string) (blockView, error) {
		close(started)
		<-ctx.Done()
		return blockView{}, ctx.Err()
	}
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: i.ID, Armed: true, Live: true}}}
	p, err := a.planIndexRun(ctx, req)
	if err != nil || !p.Ready {
		t.Fatal(p, err)
	}
	d, err := a.startIndexRun(ctx, req, p.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("admitted source did not start")
	}
	if _, err = a.controlIndexRun(ctx, d.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	paused := waitRun(t, a)
	rules := bitmapReferenceAnswers("district")
	rules["text"] = 0
	edit := bitmapProfileRequest{Action: "update", ID: i.ID, Kind: i.Kind, Label: i.Label, Rules: rules, ExpectedProfile: i.ProfileHash, ExpectedConfigurationRevision: 1}
	if _, err = a.updateBitmapProfile(ctx, edit); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatal("paused admitted interpretation was edited", err)
	}
	if paused.Run.Request.Consumers[0].Live != true || paused.Run.Consumers[0].ProfileHash != i.ProfileHash {
		t.Fatal("pause lost admitted choices", paused.Run)
	}
	if _, err = a.controlIndexRun(ctx, d.Run.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.updateBitmapProfile(ctx, edit); err != nil {
		t.Fatal("cancel did not release an unbuilt interpretation", err)
	}
	if _, err = a.resumeIndexRun(ctx, paused.Run); err == nil {
		t.Fatal("stale paused record resumed after cancellation/edit")
	}
}

func TestTimelineRunBitmapStaleRestoredRunCannotResumeFetchOrPublish(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	created, err := bitmapSaveProfile(ctx, a.dataDir, bitmapProfileRequest{Action: "create", Kind: "district", Label: "Restored intent", Rules: bitmapReferenceAnswers("district")})
	if err != nil {
		t.Fatal(err)
	}
	i := created["instance"].(bitmapProfileInstance)
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	installRunProtocolHeader(t, a, &b)
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: i.ID, Armed: true, Live: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	old := plan.Consumers[0]
	consumer := bitmapTestConsumer(t, a.dataDir, i.ID)
	rules := bitmapReferenceAnswers("district")
	rules["text"] = 0
	if _, err = a.updateBitmapProfile(ctx, bitmapProfileRequest{Action: "update", ID: i.ID, Kind: i.Kind, Label: i.Label, Rules: rules, ExpectedProfile: i.ProfileHash, ExpectedConfigurationRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.prepareIndexRun(req); err != nil {
		t.Fatal(err)
	}
	bound := a.indexRunBoundConsumerContext(ctx, old)
	if err = checkIndexRunPublication(bound); !errors.Is(err, errIndexRunProfileChanged) {
		t.Fatal("publication guard adopted the edited profile", err)
	}
	if err = consumer.Apply(bound, b, nil); !errors.Is(err, errIndexRunProfileChanged) {
		t.Fatal("old opened consumer published the edited interpretation", err)
	}
	// Simulate restoring an old run.json separately from the current registry.
	// No supported edit can produce this state because its reservation blocks it.
	r := &indexRunRecord{ID: "restored-old-intent", State: "paused", Request: plan.Request, InitialTargetHeight: plan.TargetHeight, TargetHeight: plan.TargetHeight, TargetHash: plan.TargetHash, Consumers: []indexRunProgress{old}}
	a.indexRunMu.Lock()
	a.indexRunState.Run = r
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) { calls.Add(1); return b, nil }
	if _, err = a.resumeIndexRun(ctx, r); !errors.Is(err, errIndexRunProfileChanged) {
		t.Fatal("restored old run resumed with current rules", err)
	}
	if got := a.indexRunSnapshot(); got.Run.State != "paused" || !got.Run.Request.Consumers[0].Live {
		t.Fatal("rejected resume changed admitted intent", got)
	}
	a.indexRunMu.Lock()
	a.indexRunState.Run.State = "running"
	a.indexRunMu.Unlock()
	a.runCoordinatedIndexes(ctx, r.ID)
	got := a.indexRunSnapshot()
	if calls.Load() != 0 || got.Run.State != "complete_with_errors" || got.Run.Counts.SourceReads != 0 {
		t.Fatal("stale worker fetched or continued", got.Run, calls.Load())
	}
	for _, name := range []string{"head.json", "pending.json", "pages", "commits"} {
		if _, err = os.Stat(filepath.Join(bitmapDevelopmentDir(a.dataDir, i.ID), name)); !os.IsNotExist(err) {
			t.Fatal("stale rules left derived evidence", name, err)
		}
	}
}

func TestTimelineRunBitmapConfigurationCycleCannotReusePriorReview(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	created, err := bitmapSaveProfile(ctx, a.dataDir, bitmapProfileRequest{Action: "create", Kind: "district", Label: "Same R, newer configuration", Rules: bitmapReferenceAnswers("district"), LinkedParcel: true})
	if err != nil {
		t.Fatal(err)
	}
	i := created["instance"].(bitmapProfileInstance)
	parcel := created["linked_parcel"].(bitmapProfileInstance)
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &b)
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: i.ID, Armed: true}, {Index: i.ChainInstance, Armed: true}, {Index: parcel.ID, Armed: true}}}
	old, err := a.planIndexRun(ctx, req)
	if err != nil || !old.Ready {
		t.Fatal(old, err)
	}
	current := i
	for _, value := range []int{0, 1} {
		rules := bitmapReferenceAnswers("district")
		rules["text"] = value
		edited, e := a.updateBitmapProfile(ctx, bitmapProfileRequest{Action: "update", ID: i.ID, Kind: i.Kind, Label: i.Label, Rules: rules, ExpectedProfile: current.ProfileHash, ExpectedConfigurationRevision: bitmapEffectiveConfigurationRevision(current)})
		if e != nil {
			t.Fatal(e)
		}
		current = edited["instance"].(bitmapProfileInstance)
	}
	if current.ProfileHash != i.ProfileHash || current.ConfigurationRevision != 3 {
		t.Fatal("fixture did not restore identical rules at a newer revision", current)
	}
	for _, p := range old.Consumers {
		if err = validateIndexRunBitmapProgress(ctx, a.dataDir, p); !errors.Is(err, errIndexRunProfileChanged) {
			t.Fatal("old same-R consumer binding was reused", p.Index, err)
		}
	}
	fresh, err := a.planIndexRun(ctx, req)
	if err != nil || !fresh.Ready || fresh.PlanID == old.PlanID {
		t.Fatal("same-R configuration cycle reused an old review", fresh, err)
	}
	var calls atomic.Int64
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) { calls.Add(1); return b, nil }
	if _, err = a.startIndexRun(ctx, req, old.PlanID); err == nil || calls.Load() != 0 {
		t.Fatal("same-R stale plan started source work", err, calls.Load())
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
