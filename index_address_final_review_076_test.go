package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func Test076AddressFinalReviewCanceledHistoryJobPausesWithoutPublishing(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.NetworkDisabled = true, true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.runAddressIndexBuild(ctx, indexJob{ID: "final-review-cancelled", Index: "address-history", State: "running", From: 0, To: 0, Height: -1, Mode: "lean", Retention: "ephemeral", Live: true})
	job := a.indexJobSnapshot()
	if job.State != "paused" || job.Error != "" || a.liveIndexRuntime("address-history").State == "error" {
		t.Fatalf("cancelled history poisoned job or Live: %+v %+v", job, a.liveIndexRuntime("address-history"))
	}
	h, err := openAddressHistoryStoreContext(context.Background(), a.dataDir)
	if err != nil || h.head.Commitment != "" {
		t.Fatalf("cancelled job published a history head: %+v %v", h, err)
	}
}

func Test076AddressFinalReviewCurrentLiveHonorsSavedErrorAndBackoff(t *testing.T) {
	a := prepareLiveTestApp063(t)
	b, err := a.fetchAndDecodeContext(context.Background(), "0")
	if err != nil {
		t.Fatal(err)
	}
	u, err := openUTXOStoreContext(context.Background(), a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	utxoApply(t, u, b)
	// Current Live requires its UTXO dependency to be enabled and following.
	if _, err = a.setIndexLivePolicy(indexLiveRequest{Index: "utxo-state", Action: "enable"}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.setIndexLivePolicy(indexLiveRequest{Index: "address-state", Action: "enable"}); err != nil {
		t.Fatal(err)
	}
	a.failLiveIndexRuntime("address-state", errors.New("reviewed repair required"))
	a.liveIndexTick()
	if a.indexJobSnapshot().ID != "" || a.liveIndexRuntime("address-state").State != "error" {
		t.Fatal("current Live ignored saved error", a.indexJobSnapshot(), a.liveIndexRuntime("address-state"))
	}
	a.clearLiveIndexRuntime("address-state")
	a.deferLiveIndexRetry("address-state", errors.New("temporary source failure"))
	a.liveIndexTick()
	if a.indexJobSnapshot().ID != "" || a.liveIndexRuntime("address-state").State != "waiting" {
		t.Fatal("current Live ignored backoff", a.indexJobSnapshot(), a.liveIndexRuntime("address-state"))
	}
}

func Test076AddressFinalReviewHistoryWithNoSelectedCoverageIsUnknown(t *testing.T) {
	ctx := context.Background()
	s, err := openAddressHistoryStoreContext(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script, address := addressHistoryTestScript(77)
	hashes := map[int64]string{10: strings.Repeat("1", 64)}
	hashAt := addressHistoryTestHashAt(hashes)
	if err = s.CommitBlockContext(ctx, addressHistoryTestBlock(10, hashes[10], outputView{ValueSats: 123, ScriptPubKey: script}), hashAt); err != nil {
		t.Fatal(err)
	}
	hashes[10] = strings.Repeat("2", 64)
	if err = s.ReconcileContext(ctx, hashAt); err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryContext(ctx, address, "", 25, hashAt)
	if err != nil || result.State != "unknown" || len(result.Coverage) != 0 || len(result.Outputs) != 0 {
		t.Fatalf("no selected scanned coverage became apparent known zero history: %+v %v", result, err)
	}
}

func Test076AddressFinalReviewRetentionPlanExplainsReusedBodies(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.NetworkDisabled = true, true
	for _, retention := range []string{"cache", "retain"} {
		plan, err := a.planIndexBuild(indexBuildRequest{Index: "address-history", From: int64Pointer076(0), To: int64Pointer076(0), Retention: retention})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(plan.Notes, " "), "changing retention does not refill reused or previously scanned bodies") {
			t.Fatal("retention upgrade claimed implicit body refill", plan)
		}
	}
}
