package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A source fetch for the legacy history job must not race its own sparse
// history fanout. The same body must still feed other explicitly armed tracks.
func TestAddressHistoryJobOwnsSourceAndFeedsOtherArmedTracks079(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	req := indexBuildRequest{Index: "address-history", From: int64Pointer076(0), To: int64Pointer076(0), Retention: "ephemeral"}
	armLegacyBuildTracks(t, a, req)
	armLegacyBuildTracks(t, a, indexBuildRequest{Index: "tx-locator"})
	job, err := a.startIndexBuild(req)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	job, err = a.waitQueuedIndexJob(ctx, job.ID)
	if err != nil || job.State != "complete" || job.Height != 0 {
		t.Fatalf("history job failed: %+v %v", job, err)
	}
	history, err := openAddressHistoryStoreContext(ctx, a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.head.Coverage) != 1 || history.head.Coverage[0] != (heightInterval{0, 0}) {
		t.Fatalf("history coverage: %+v", history.head.Coverage)
	}
	_, locatorHead, err := a.browseStore("tx-locator")
	if err != nil || len(locatorHead.Blocks) != 1 || locatorHead.Blocks["0"] == "" {
		t.Fatalf("source did not feed armed TX locator: %+v %v", locatorHead, err)
	}
}

// Cancel on the first context check after the durable history head appears,
// which happens while draining the fetched block before the job reopens it.
type cancelAfterHistoryPublication079 struct {
	context.Context
	cancel context.CancelFunc
	head   string
}

func (c cancelAfterHistoryPublication079) Err() error {
	if _, err := os.Stat(c.head); err == nil {
		c.cancel()
	}
	return c.Context.Err()
}
func TestAddressHistoryCancellationAfterPublicationPauses079(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	armLegacyBuildTracks(t, a, indexBuildRequest{Index: "address-history"})
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := cancelAfterHistoryPublication079{Context: base, cancel: cancel, head: filepath.Join(a.dataDir, "indexes", "address-history", "head.json")}
	a.runAddressIndexBuild(ctx, indexJob{ID: "cancel-after-history-publication", Index: "address-history", State: "running", From: 0, To: 0, Height: -1, Mode: "lean", Retention: "ephemeral", Live: true})
	job := a.indexJobSnapshot()
	if job.State != "paused" || job.Error != "" || a.liveIndexRuntime("address-history").State == "error" {
		t.Fatalf("cancellation poisoned job or Live: %+v", job)
	}
	store, err := openAddressHistoryStoreContext(context.Background(), a.dataDir)
	if err != nil || store.head.Commitment == "" || len(store.head.Coverage) != 1 || store.head.Coverage[0] != (heightInterval{0, 0}) {
		t.Fatalf("committed history not preserved: %+v %v", store, err)
	}
}
