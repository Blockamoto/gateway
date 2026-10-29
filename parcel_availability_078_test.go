package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func Test078ParcelsExecutionDisabledWithoutChangingSavedRecipe(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	definition, err := findIndexDefinition(bitmapStandardParcelID)
	if err != nil || definition.Buildable || definition.Locked || definition.LockReason != errBitmapParcelsDeferred.Error() || !releaseFeatureAvailable(bitmapStandardParcelID) || releaseAvailability()[bitmapStandardParcelID] {
		t.Fatal("saved-read and execution availability confused", definition, err)
	}
	instance, err := bitmapResolvedInstance(ctx, a.dataDir, bitmapStandardParcelID)
	if err != nil || definition.RuleHash != instance.ProfileHash || definition.ProfileHash != bitmapProfileHash(bitmapStandardProfile("parcel")) || definition.StartHeight != bitmapDevelopmentStart || definition.DistrictInstance != bitmapStandardDistrictID {
		t.Fatal("native definition identity changed", definition, instance, err)
	}
	for _, req := range []indexBuildRequest{{Index: bitmapStandardParcelID}, {Index: "blocks", Outputs: []string{bitmapStandardParcelID}}} {
		if _, err := a.planIndexBuild(req); err == nil || !strings.Contains(err.Error(), errBitmapParcelsDeferred.Error()) {
			t.Fatal("individual/parent plan admitted Parcels", req, err)
		}
		if _, err := a.startIndexBuild(req); err == nil {
			t.Fatal("individual/parent build admitted Parcels", req)
		}
	}
	if _, err := a.setIndexLivePolicy(indexLiveRequest{Index: bitmapStandardParcelID, Action: "enable"}); !errors.Is(err, errBitmapParcelsDeferred) && (err == nil || err.Error() != errBitmapParcelsDeferred.Error()) {
		t.Fatal("live entry admitted Parcels", err)
	}
	if _, err := openBitmapRunConsumer(ctx, a, bitmapStandardParcelID); err == nil {
		t.Fatal("public worker factory admitted Parcels")
	}
	if err := checkIndexRunPublication(a.indexRunConsumerContext(ctx, bitmapStandardParcelID)); !errors.Is(err, errBitmapParcelsDeferred) {
		t.Fatal("publication boundary admitted Parcels", err)
	}
	// The retained format adapter still validates and reads the original recipe.
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	installRunProtocolHeader(t, a, &b)
	district := bitmapTestConsumer(t, a.dataDir, bitmapStandardDistrictID)
	parcel := bitmapTestConsumer(t, a.dataDir, bitmapStandardParcelID)
	if err := district.Apply(ctx, b, nil); err != nil {
		t.Fatal(err)
	}
	if err := parcel.Apply(ctx, b, nil); err != nil {
		t.Fatal("retained implementation unavailable", err)
	}
	before, err := os.ReadFile(filepath.Join(parcel.dir, "head.json"))
	if err != nil {
		t.Fatal(err)
	}
	view, err := a.bitmapInstanceViewContext(ctx, "parcel", bitmapStandardParcelID, instance.ProfileHash, "", nil, nil, nil, "", 10, true)
	if err != nil || view.State != "ready" || view.Snapshot == nil || view.Snapshot.Commitment != parcel.head.Commitment {
		t.Fatal("saved inspection unavailable", view, err)
	}
	meta, err := a.indexExportReview(ctx, indexExportRequest{Index: bitmapStandardParcelID, Representation: "native"})
	if err != nil || !meta.Complete || !meta.Ready || meta.Blocks != 1 {
		t.Fatal("saved export unavailable", meta, err)
	}
	if after, err := os.ReadFile(filepath.Join(parcel.dir, "head.json")); err != nil || string(before) != string(after) {
		t.Fatal("read-only checks changed saved Parcel", err)
	}
}

func Test078EveryParcelGenerationAndCustomAliasIsOff(t *testing.T) {
	a := runTestApp(t)
	bitmapTestCreateProfile(t, a.dataDir, "Retained custom Parcel", 1, true)
	instances, err := bitmapProfileInstances(context.Background(), a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, instance := range instances {
		if instance.Kind != "parcel" {
			continue
		}
		if instance.ID == bitmapReplayParcelID {
			continue
		} // New replay identity is separate from frozen generations.
		seen++
		if !bitmapParcelExecutionDeferred(instance.ID) || releaseIndexExecutionAvailable(instance.ID) || releaseAvailability()[instance.ID] {
			t.Fatal("Parcel generation/alias remained available", instance.ID)
		}
		if _, err := normalizeIndexRunRequest(indexRunRequest{Consumers: []indexRunSelection{{Index: instance.ID, Armed: true}}}); err == nil {
			t.Fatal("Parcel generation/alias could arm", instance.ID)
		}
		if _, err := openBitmapRunConsumer(context.Background(), a, instance.ID); err == nil {
			t.Fatal("Parcel generation/alias could execute", instance.ID)
		}
		if err := checkIndexRunPublication(a.indexRunConsumerContext(context.Background(), instance.ID)); !errors.Is(err, errBitmapParcelsDeferred) {
			t.Fatal("Parcel generation/alias could publish", instance.ID, err)
		}
	}
	if seen < 5 {
		t.Fatal("missing Standard, preserved generations, legacy alias or custom Parcel fixture", seen)
	}
}

func Test078RestoredParcelIntentCannotRunOrFanout(t *testing.T) {
	a := runTestApp(t)
	ctx := context.Background()
	choice := indexRunSelection{Index: bitmapStandardParcelID, Armed: true, Live: true}
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}, choice}}
	stored := indexRunDocument{Schema: 1, Configured: true, Prepared: req, Run: &indexRunRecord{ID: "saved-parcel", State: "paused", Request: req}}
	stored.Checksum = indexRunDigest(stored)
	if err := atomicWriteJSON(indexRunPath(a.dataDir), stored); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(indexRunPath(a.dataDir))
	if d, err := readIndexRunDocument(a.dataDir); err != nil || d.Run == nil || len(d.Prepared.Consumers) != 2 {
		t.Fatal("saved intent became unreadable", d, err)
	}
	for _, fn := range []func() error{
		func() error { _, e := normalizeIndexRunRequest(req); return e },
		func() error { _, e := a.prepareIndexRun(req); return e },
		func() error { _, e := a.planIndexRun(ctx, req); return e },
		func() error { _, e := a.startIndexRun(ctx, req, ""); return e },
		func() error {
			_, e := a.amendIndexRun(ctx, indexRunAmendment{Consumers: []indexRunSelection{choice}})
			return e
		},
		func() error { _, e := a.controlIndexRun(ctx, "saved-parcel", "resume"); return e },
	} {
		if err := fn(); err == nil || !strings.Contains(err.Error(), "Parcels indexing is disabled") {
			t.Fatal("restored/public request admitted Parcel", err)
		}
	}
	for _, row := range resolvedIngestionCapabilities(stored) {
		if row.Index == bitmapStandardParcelID && (row.Available || row.Enabled || row.Capability != "disabled") {
			t.Fatal("status advertised disabled Parcel work", row)
		}
	}
	if on, err := timelineTrackArmed(a.dataDir, bitmapStandardParcelID); err != nil || on || indexRunOptionalWorkAllowed(a.dataDir, bitmapStandardParcelID) {
		t.Fatal("restored Parcel consent bypassed availability", on, err)
	}
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &b)
	var calls atomic.Int64
	prepared := &indexRunPrepared{block: b}
	work := context.WithValue(ctx, indexRunPreparedKey{}, prepared)
	work = context.WithValue(work, indexRunBlockConsumersKey{}, map[string]indexRunBlockConsumer{bitmapStandardParcelID: func(context.Context) error { calls.Add(1); return nil }})
	a.ingestArmedResolvedBlock(work, b)
	if err := a.ingestArmedBitmapBlock(ctx, b, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("shared/native fanout invoked disabled Parcel")
	}
	if _, err := os.Stat(bitmapDevelopmentDir(a.dataDir, bitmapStandardParcelID)); !os.IsNotExist(err) {
		t.Fatal("disabled Parcel created storage", err)
	}
	if after, _ := os.ReadFile(indexRunPath(a.dataDir)); string(before) != string(after) {
		t.Fatal("rejected work rewrote restored choices")
	}
	// Turning the unavailable saved choice Off is still permitted.
	choice.Armed, choice.Live = false, false
	if _, err := normalizeIndexRunRequest(indexRunRequest{Consumers: []indexRunSelection{choice}}); err != nil {
		t.Fatal("disabled choice cannot be turned Off", err)
	}
}

func Test078ParcelsSavedQueueAndLiveCannotRestart(t *testing.T) {
	a := runTestApp(t)
	from := int64(bitmapDevelopmentStart)
	policy := indexLivePolicy{Index: bitmapStandardParcelID, Enabled: true, InitialFrom: &from, Retention: "ephemeral"}
	if err := a.writeIndexLivePolicy(policy); err != nil {
		t.Fatal(err)
	}
	if err := a.writeIndexLivePolicy(indexLivePolicy{Index: "tx-locator", Retention: "ephemeral"}); err != nil {
		t.Fatal("disabled saved policy damaged unrelated policy parsing", err)
	}
	livePath := filepath.Join(a.dataDir, "indexes", "live.json")
	liveBefore, _ := os.ReadFile(livePath)
	queue := indexQueueState{Schema: 1, Entries: []indexQueueEntry{
		{Request: indexBuildRequest{Index: bitmapStandardParcelID}, Job: indexJob{ID: "saved-parcel", Index: bitmapStandardParcelID, State: "queued"}},
		{Request: indexBuildRequest{Index: "blocks", Outputs: []string{bitmapStandardParcelID}}, Job: indexJob{ID: "saved-parent", Index: "blocks", State: "queued", Outputs: []string{bitmapStandardParcelID}}},
	}}
	queuePath := filepath.Join(a.dataDir, "indexes", "queue.json")
	if err := atomicWriteJSON(queuePath, queue); err != nil {
		t.Fatal(err)
	}
	queueBefore, _ := os.ReadFile(queuePath)
	a.liveIndexTick()
	jobs, err := a.indexJobsSnapshot()
	if err != nil || len(jobs) != 2 {
		t.Fatal(jobs, err)
	}
	for _, job := range jobs {
		if job.State != "paused" || !strings.Contains(job.WaitingReason, "Parcels indexing is disabled") {
			t.Fatal("saved queue activated", job)
		}
		if _, err := a.controlIndexQueue(indexQueueRequest{ID: job.ID, Action: "resume"}); err == nil {
			t.Fatal("saved queue resumed")
		}
	}
	if view := a.indexLiveView(bitmapStandardParcelID, policy); view.Enabled || view.On || !strings.Contains(view.Reason, "Parcels indexing is disabled") {
		t.Fatal("saved Live remained active", view)
	}
	for path, before := range map[string][]byte{livePath: liveBefore, queuePath: queueBefore} {
		if after, _ := os.ReadFile(path); string(before) != string(after) {
			t.Fatal("read/scheduler rewrote saved policy", path)
		}
	}
	if a.indexCancel != nil {
		t.Fatal("disabled queue started worker")
	}
}

func Test078ParcelsHTTPActivationRejected(t *testing.T) {
	a := runTestApp(t)
	h := indexInterfaceHandler(a)
	request := `{"consumers":[{"index":"` + bitmapStandardParcelID + `","armed":true,"live":true}]}`
	for _, tc := range []struct{ path, body string }{
		{"run/prepare", request}, {"run/plan", request}, {"run/start", `{"request":` + request + `}`},
		{"run/amend", `{"consumers":[{"index":"` + bitmapStandardParcelID + `","armed":true}]}`},
		{"plan", `{"index":"` + bitmapStandardParcelID + `"}`},
		{"build", `{"index":"blocks","outputs":["` + bitmapStandardParcelID + `"]}`},
		{"live", `{"index":"` + bitmapStandardParcelID + `","action":"enable"}`},
	} {
		response := indexInterfaceCall(h, "POST", "/api/v1/index/"+tc.path, tc.body)
		if response.Code < 400 || !strings.Contains(response.Body.String(), "Parcels indexing is disabled") {
			t.Fatal(tc.path, response.Code, response.Body.String())
		}
	}
	w := indexInterfaceCall(h, "GET", "/api/v1/index/rules?index="+bitmapStandardParcelID, "")
	var definition indexDefinition
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &definition) != nil || definition.Buildable || definition.Locked || definition.RuleHash == "" {
		t.Fatal("read-only rule contract broken", w.Code, w.Body.String())
	}
}
