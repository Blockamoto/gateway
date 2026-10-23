package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBitmapStandardOnlyPublicCapabilitiesAndMutationFences(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	i := bitmapTestCreateProfile(t, a.dataDir, "Preserved reference answers", 1, true)
	before, err := os.ReadFile(bitmapProfilePath(a.dataDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/bitmap/profiles", "/api/v1/bitmap/profiles/validate"} {
		for _, action := range []string{"create", "update", "rename", "save_draft"} {
			r := httptest.NewRequest("POST", "http://127.0.0.1"+path, strings.NewReader(fmt.Sprintf(`{"action":%q,"id":%q}`, action, i.ID)))
			r.RemoteAddr = "127.0.0.1:1234"
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			a.handleBitmapProfiles(w, r)
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "fixed canonical") {
				t.Fatal(path, action, w.Code, w.Body.String())
			}
		}
	}
	if _, err = bitmapSaveProfile(context.Background(), a.dataDir, bitmapProfileRequest{Action: "create", Kind: "district"}); err == nil {
		t.Fatal("public mutation helper bypassed scope")
	}
	if _, err = a.updateBitmapProfile(context.Background(), bitmapEditRequest(i, 0)); err == nil {
		t.Fatal("public edit helper bypassed scope")
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/bitmap/compare?baseline="+i.ID+"&variant="+i.ID, nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	a.handleBitmapComparison(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("comparison remained available", w.Code, w.Body.String())
	}
	r = httptest.NewRequest("GET", "http://127.0.0.1/api/v1/bitmap/profiles", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w = httptest.NewRecorder()
	a.handleBitmapProfiles(w, r)
	var view struct {
		Capabilities bitmapCapabilitiesView  `json:"capabilities"`
		Instances    []bitmapProfileInstance `json:"instances"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Capabilities.CustomProfiles || view.Capabilities.Comparisons || view.Capabilities.CustomExecution || view.Capabilities.Reason == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, instance := range view.Instances {
		if instance.ExecutionAvailable == nil || *instance.ExecutionAvailable != bitmapBuiltinExecutionAvailable(instance.ID) || instance.RulesEditable {
			t.Fatal("misleading profile capability", instance)
		}
		if bitmapCustomConsumerID(instance.ID) && instance.ExecutionUnavailableReason == "" {
			t.Fatal("missing custom reason", instance)
		}
	}
	after, _ := os.ReadFile(bitmapProfilePath(a.dataDir))
	if string(before) != string(after) {
		t.Fatal("denied public work changed registry")
	}
}

func TestBitmapStandardOnlySavedCustomReadsStopAndExecutionFences(t *testing.T) {
	for _, state := range []string{"paused", "running"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			a := runTestApp(t)
			district := bitmapTestCreateProfile(t, a.dataDir, "Saved custom", 1, true)
			instances, err := bitmapProfileInstances(ctx, a.dataDir)
			if err != nil {
				t.Fatal(err)
			}
			var custom []bitmapProfileInstance
			for _, i := range instances {
				if i.ID == district.ID || i.DistrictInstance == district.ID {
					custom = append(custom, i)
				}
			}
			// Create old, authenticated saved evidence through the format adapter.
			// No public capability is enabled for this compatibility fixture.
			b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
			installRunProtocolHeader(t, a, &b)
			for _, kind := range []string{"district", "parcel", "chain"} {
				for _, i := range custom {
					if i.Kind == kind {
						if err = bitmapTestConsumer(t, a.dataDir, i.ID).Apply(ctx, b, nil); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			before, _ := os.ReadFile(bitmapProfilePath(a.dataDir))
			heads := map[string]string{}
			req := indexRunRequest{}
			progress := []indexRunProgress{}
			for _, i := range custom {
				head, _ := os.ReadFile(filepath.Join(bitmapDevelopmentDir(a.dataDir, i.ID), "head.json"))
				heads[i.ID] = string(head)
				req.Consumers = append(req.Consumers, indexRunSelection{Index: i.ID, Armed: true, Live: true})
				p := indexRunProgress{Index: i.ID, Height: b.Height}
				if _, err = bindIndexRunBitmapProgress(ctx, a.dataDir, &p); err != nil {
					t.Fatal(err)
				}
				progress = append(progress, p)
				if _, err = openBitmapRunConsumer(ctx, a, i.ID); err == nil {
					t.Fatal("custom executable factory allowed", i.ID)
				}
				for _, records := range []bool{false, true} {
					view, e := a.bitmapInstanceViewContext(ctx, i.Kind, i.ID, i.ProfileHash, "", nil, nil, nil, "", 10, records)
					if e != nil || view.State != "ready" || view.Snapshot == nil || view.Snapshot.Height != b.Height {
						t.Fatal("saved read blocked", i.ID, records, view.State, e)
					}
				}
				savedHead, readErr := readBitmapDevelopmentHead(ctx, a.dataDir, i.ID)
				if readErr != nil || savedHead == nil {
					t.Fatal("saved head unavailable", readErr)
				}
				r := httptest.NewRequest("GET", fmt.Sprintf("http://127.0.0.1/api/v1/bitmap/audit?instance=%s&height=%d&snapshot=%s", i.ID, b.Height, savedHead.Commitment), nil)
				r.RemoteAddr = "127.0.0.1:1234"
				w := httptest.NewRecorder()
				a.handleBitmapCandidateAudit(w, r)
				expectedStatus := http.StatusOK
				if i.Kind == "chain" {
					expectedStatus = http.StatusBadRequest
				} // Audits belong to the claim producer.
				if w.Code != expectedStatus {
					t.Fatal("saved audit contract", i.ID, w.Code, w.Body.String())
				}
			}
			stored := indexRunDocument{Schema: 1, Configured: true, Prepared: req, Run: &indexRunRecord{ID: "old-custom-run", State: state, Phase: "idle", Request: req, Consumers: progress, TargetHeight: b.Height, InitialTargetHeight: b.Height, TargetHash: b.Hash}}
			stored.Checksum = indexRunDigest(stored)
			if err = atomicWriteJSON(indexRunPath(a.dataDir), stored); err != nil {
				t.Fatal(err)
			}
			// Simulate a new process opening old intent; it must remain cancellable.
			a.indexRunLoaded = false
			got := a.indexRunSnapshot()
			if got.Error != "" || got.Run == nil || got.Run.State != "paused" || len(got.Run.Consumers) != 3 {
				t.Fatal("old custom intent became unreadable", got)
			}
			var calls atomic.Int64
			a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) { calls.Add(1); return b, nil }
			if _, err = a.prepareIndexRun(req); err == nil {
				t.Fatal("custom prepare accepted")
			}
			if _, err = a.startIndexRun(ctx, req, "old-review"); err == nil {
				t.Fatal("custom start accepted")
			}
			if _, err = a.controlIndexRun(ctx, got.Run.ID, "resume"); err == nil {
				t.Fatal("custom resume accepted")
			}
			if _, err = a.amendIndexRun(ctx, indexRunAmendment{RunID: got.Run.ID, AdmissionRevision: 1, Consumers: req.Consumers}); err == nil {
				t.Fatal("custom join accepted")
			}
			if err = requireReleaseIndexRequest(indexBuildRequest{Index: "blocks", Outputs: []string{district.ID}}); err == nil {
				t.Fatal("legacy output bypass accepted")
			}
			for _, p := range progress {
				if err = checkIndexRunPublication(a.indexRunBoundConsumerContext(ctx, p)); err == nil {
					t.Fatal("custom final publication accepted", p.Index)
				}
			}
			if _, err = a.controlIndexRun(ctx, got.Run.ID, "cancel"); err != nil {
				t.Fatal("old custom run cannot Stop", err)
			}
			if got = a.indexRunSnapshot(); got.Error != "" || got.Run.State != "cancelled" || calls.Load() != 0 {
				t.Fatal("rejected custom run did work or lost Stop", got, calls.Load())
			}
			after, _ := os.ReadFile(bitmapProfilePath(a.dataDir))
			if string(before) != string(after) {
				t.Fatal("read/Stop changed saved identities")
			}
			for _, i := range custom {
				head, _ := os.ReadFile(filepath.Join(bitmapDevelopmentDir(a.dataDir, i.ID), "head.json"))
				if string(head) != heads[i.ID] {
					t.Fatal("scope gate changed saved data", i.ID)
				}
			}
		})
	}
}

func TestBitmapCanonicalOnlyFixedRunRemainsAvailable(t *testing.T) {
	a := runTestApp(t)
	ctx := context.Background()
	b := bitmapDevelopmentFixtureBlock(0, "", "0.bitmap")
	installRunProtocolHeader(t, a, &b)
	var calls atomic.Int64
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) { calls.Add(1); return b, nil }
	from, to := b.Height, b.Height
	req := indexRunRequest{In: &from, Out: &to, Consumers: []indexRunSelection{{Index: bitmapCanonicalDistrictID, Armed: true}, {Index: bitmapCanonicalParcelID, Armed: true}, {Index: "bitmap-district-chain-dev-v1", Armed: true}, {Index: "bitmap-parcel-chain-dev-v1", Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" || calls.Load() != 1 || done.Run.Counts.ChainSteps != 2 {
		t.Fatal("fixed canonical run was blocked or duplicated", done.Run, calls.Load())
	}
}
