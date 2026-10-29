package main

import (
	"context"
	"strings"
	"testing"
)

func TestPreparedChoicesDoNotChangeActiveAdmission079(t *testing.T) {
	a := runTestApp(t)
	active := indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true, indexRunOptions: indexRunOptions{Mode: "lean"}}, {Index: "blocks", Armed: false}}}
	doc, err := a.prepareIndexRun(active)
	if err != nil {
		t.Fatal(err)
	}
	progress := indexRunProgress{Index: "tx-locator", From: 0, To: 1, Height: -1, AdmissionState: "admitted"}
	doc.Run = &indexRunRecord{ID: "scope-fixture", State: "running", AdmissionRevision: 1, Request: doc.Prepared, Consumers: []indexRunProgress{progress}, InitialTargetHeight: 1}
	a.indexRunMu.Lock()
	a.indexRunState = doc
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	guard := a.indexRunConsumerContext(context.Background(), "tx-locator", progress)
	if err = checkIndexRunPublication(guard); err != nil {
		t.Fatal(err)
	}
	staged := indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: false, indexRunOptions: indexRunOptions{Mode: "full"}}, {Index: "blocks", Armed: true}}}
	updated, err := a.prepareIndexRun(staged)
	if err != nil {
		t.Fatal(err)
	}
	selection, _ := runSelection(updated.Run.Request, "tx-locator")
	if !selection.Armed || selection.Mode != "lean" || updated.Run.AdmissionRevision != 1 {
		t.Fatalf("staging altered active run: %+v", updated.Run)
	}
	if err = checkIndexRunPublication(guard); err != nil {
		t.Fatalf("staging stopped admitted writer: %v", err)
	}
	if !indexRunOptionalWorkAllowed(a.dataDir, "tx-locator") || indexRunOptionalWorkAllowed(a.dataDir, "blocks") {
		t.Fatal("staged arm changed active ingestion policy")
	}
	if on, err := timelineTrackArmed(a.dataDir, "blocks"); err != nil || on {
		t.Fatalf("staged Raw became active: %t %v", on, err)
	}
	response := indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/run/amend", `{"run_id":"scope-fixture","admission_revision":1,"consumers":[{"index":"tx-locator","armed":false}]}`)
	if response.Code != 409 || !strings.Contains(response.Body.String(), "review") {
		t.Fatalf("unreviewed active amendment was accepted: %d %s", response.Code, response.Body.String())
	}
	a.indexRunMu.Lock()
	a.indexRunState.Run.State = "paused"
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	paused, err := readIndexRunDocument(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	pausedChoice, _ := runSelection(indexRunEffectiveRequest(paused), "tx-locator")
	if !pausedChoice.Armed || pausedChoice.Mode != "lean" {
		t.Fatal("paused run lost its admitted scope")
	}
	// Once the run ends, the staged request becomes the policy for later work.
	a.indexRunMu.Lock()
	a.indexRunState.Run.State = "complete"
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if indexRunOptionalWorkAllowed(a.dataDir, "tx-locator") || !indexRunOptionalWorkAllowed(a.dataDir, "blocks") {
		t.Fatal("staged choices did not become the later policy")
	}
}
