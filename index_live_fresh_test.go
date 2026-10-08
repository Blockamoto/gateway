package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func Test064FreshLiveViewReportsPersistedInitialHeight(t *testing.T) {
	a := prepareLiveTestApp063(t)
	view, err := a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "start"})
	if err != nil || view.InitialFrom != 0 {
		t.Fatal("fresh mainnet start missing from mutation view", view, err)
	}
	view, err = a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "enable"})
	if err != nil || view.InitialFrom != 0 {
		t.Fatal("fresh Live view replaced mainnet start", view, err)
	}
	policies, err := a.indexLivePolicies()
	if err != nil {
		t.Fatal(err)
	}
	policy := policies["blocks"]
	zero := int64(0)
	policy.InitialFrom = &zero
	if err = a.writeIndexLivePolicy(policy); err != nil {
		t.Fatal(err)
	}
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex()}
	restored, err := restarted.indexLivePolicies()
	if err != nil {
		t.Fatal(err)
	}
	view = restarted.indexLiveView("blocks", restored["blocks"])
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]json.RawMessage{}
	if err = json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if view.InitialFrom != 0 || string(fields["initial_from"]) != "0" {
		t.Fatal("persisted explicit zero omitted/replaced in status", view, string(encoded))
	}
	if got := restarted.indexLiveView("bitmap", indexLivePolicy{}); got.State != "locked" {
		t.Fatal("recipe start replaced by inscription default", got)
	}
}

func waitLiveComplete064(t *testing.T, a *app) indexJob {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	job, err := a.waitIndexBuild(ctx)
	if err != nil || job.State != "complete" || !job.Live {
		t.Fatalf("live completion: %+v %v", job, err)
	}
	waitLiveWriterReleased063(t, a)
	return job
}

func Test064FreshOnDoesNotAuthorizeHistoryOrHeaderDemand(t *testing.T) {
	a := prepareLiveTestApp063(t)
	if got := a.indexLiveView("blocks", indexLivePolicy{}); got.On || got.Enabled {
		t.Fatalf("unconfigured fresh index is on: %+v", got)
	}
	a.headerNeededHeight = -1
	view, err := a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "start", Retention: "retain"})
	if err != nil || !view.On || view.Enabled || view.CheckpointHeight != -1 {
		t.Fatalf("fresh On: %+v %v", view, err)
	}
	a.liveIndexTick()
	if a.indexJobSnapshot().ID != "" || a.headerNeededHeight != -1 {
		t.Fatal("On alone implicitly started history/header work")
	}
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus()}
	policies, err := restarted.indexLivePolicies()
	if err != nil {
		t.Fatal(err)
	}
	view = restarted.indexLiveView("blocks", policies["blocks"])
	if !view.On || view.Enabled || view.Retention != "retain" {
		t.Fatalf("fresh On policy did not survive restart: %+v", view)
	}
	view, err = restarted.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "stop"})
	if err != nil || view.On || !view.Stopped || view.Retention != "retain" {
		t.Fatalf("fresh stop: %+v %v", view, err)
	}
	if _, err := restarted.setIndexLivePolicy(indexLiveRequest{Index: "address-history", Action: "enable"}); err == nil {
		t.Fatal("unsupported index accepted Live history")
	}
}

func Test064FreshLiveBuildsHistoryCatchesUpAndFollowsTip(t *testing.T) {
	a := prepareLiveTestApp063(t)
	// This fixture explicitly reviews genesis history rather than using the
	// integrated mainnet occurrence scan suggestion of block 767430.
	zero := int64(0)
	if err := a.writeIndexLivePolicy(indexLivePolicy{Index: "blocks", InitialFrom: &zero, Mode: "lean", Retention: "ephemeral"}); err != nil {
		t.Fatal(err)
	}
	peer, requests := hashCoordinateBitcoinSource(t, mainnetBlockOne(t))
	hashCoordinateConnect(t, a, peer)
	a.setStatus(func(st *appStatus) { st.HeaderState = "current"; st.Syncing = false; st.Error = "" })
	view, err := a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "enable"})
	if err != nil || !view.On || !view.Enabled || view.State != "catching_up" || view.Lag != 1 {
		t.Fatalf("fresh history offer: %+v %v", view, err)
	}
	a.liveIndexTick()
	first := waitLiveComplete064(t, a)
	if first.From != 0 || first.To != 0 || first.Height != 0 || first.Mode != "lean" || first.Retention != "ephemeral" {
		t.Fatalf("fresh Live did not start applicable history: %+v", first)
	}
	before, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || before.checkpoint == nil || before.checkpoint.From != 0 {
		t.Fatalf("genesis history checkpoint: %+v %v", before, err)
	}
	if requests.Load() != 0 {
		t.Fatal("fresh genesis reread ignored locally available source")
	}
	installBlockOneHeader063(t, a)
	a.liveIndexTick()
	second := waitLiveComplete064(t, a)
	if second.From != 0 || second.To != 1 || second.Height != 1 || second.Mode != "lean" {
		t.Fatalf("following next selected tip: %+v", second)
	}
	after, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || after.checkpoint == nil || after.checkpoint.PreviousCommitment != before.checkpoint.Commitment || after.checkpoint.From != 0 {
		t.Fatalf("tip extension lost history: %+v %v", after, err)
	}
	policies, err := a.indexLivePolicies()
	if err != nil {
		t.Fatal(err)
	}
	view = a.indexLiveView("blocks", policies["blocks"])
	if view.State != "synced" || view.Lag != 0 || !view.LagKnown {
		t.Fatalf("caught-up state: %+v", view)
	}
	a.liveIndexTick()
	if got := a.indexJobSnapshot(); got.ID != second.ID {
		t.Fatal("synced index unnecessarily rebuilt")
	}
	if len(a.publishedIndexManifests()) != 0 {
		t.Fatal("fresh Live published private history")
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", "sources", after.checkpoint.BlockHash+".block")); !os.IsNotExist(err) {
		t.Fatal("ephemeral Live retained source", err)
	}
}

func Test064FreshLiveStopResumeRestartAndGlobalSyncChoices(t *testing.T) {
	a := prepareLiveTestApp063(t)
	a.headerNeededHeight = -1
	from := int64(100)
	if err := a.writeIndexLivePolicy(indexLivePolicy{Index: "blocks", InitialFrom: &from, Retention: "ephemeral"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "stop"}); err != nil {
		t.Fatal(err)
	}
	view, err := a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "enable"})
	if err != nil || !view.On || view.Stopped || !view.Enabled {
		t.Fatalf("Live alone did not turn on fresh stopped index: %+v %v", view, err)
	}
	for _, choice := range []string{"paused", "offline"} {
		a.settingsMu.Lock()
		a.settings.HeadersPaused = choice == "paused"
		a.settings.NetworkDisabled = choice == "offline"
		a.settingsMu.Unlock()
		a.liveIndexTick()
		if a.headerNeededHeight != -1 || a.indexJobSnapshot().ID != "" {
			t.Fatalf("Live overrode global %s choice", choice)
		}
	}
	a.settingsMu.Lock()
	a.settings.HeadersPaused = false
	a.settings.NetworkDisabled = false
	a.settingsMu.Unlock()
	a.liveIndexTick()
	if a.headerNeededHeight != 100 {
		t.Fatalf("fresh Bitmap did not request its applicable headers: %d", a.headerNeededHeight)
	}
	if _, err := a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	if view, err = a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "resume"}); err != nil || !view.On || view.Paused {
		t.Fatalf("fresh no-checkpoint resume failed: %+v %v", view, err)
	}
	if _, err := a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "stop"}); err != nil {
		t.Fatal(err)
	}
	if view, err = a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "resume"}); err != nil || !view.On || view.Stopped {
		t.Fatalf("resume retained stopped flag: %+v %v", view, err)
	}
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus()}
	policies, err := restarted.indexLivePolicies()
	if err != nil || !policies["blocks"].Enabled || policies["blocks"].Paused || policies["blocks"].Stopped {
		t.Fatalf("fresh Live restart intent: %+v %v", policies, err)
	}
	if _, err := restarted.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "disable"}); err != nil {
		t.Fatal(err)
	}
	restarted.headerNeededHeight = -1
	restarted.liveIndexTick()
	if restarted.headerNeededHeight != -1 {
		t.Fatal("disabled Live continued header demand")
	}
}

func Test064FreshBitmapStartsAtRecipeEpochAndRotatesBoundedHistory(t *testing.T) {
	t.Skip("Multi-index scheduling requires Bitmap/inscriptions, intentionally locked in 0.6.6. Blocks Live history/following remains active.")
	a := liveSchedulingFixture063(t)
	zero := int64(0)
	policies, err := a.indexLivePolicies()
	if err != nil {
		t.Fatal(err)
	}
	policy := policies["blocks"]
	policy.InitialFrom = &zero // Retain the explicitly chosen whole-history fixture.
	if err := a.writeIndexLivePolicy(policy); err != nil {
		t.Fatal(err)
	}
	// Keep selected-header evidence, remove only the disposable fixture's index
	// roots. Both Live recipes now begin without an existing checkpoint.
	for _, id := range []string{"bitmap", "blocks"} {
		if err := os.RemoveAll(filepath.Join(a.dataDir, "indexes", id)); err != nil {
			t.Fatal(err)
		}
	}
	a.liveIndexTick()
	first := waitLiveWriterReleased063(t, a)
	if first.Index != "bitmap" || first.From != 792435 || first.To != 792436 || first.Mode != "lean" {
		t.Fatalf("fresh Bitmap range was not its epoch: %+v", first)
	}
	a.liveIndexTick()
	second := waitLiveWriterReleased063(t, a)
	if second.Index != "blocks" || second.From != 0 || second.To != indexLiveBatchBlocks-1 || second.Height != 1 || second.Mode != "lean" {
		t.Fatalf("full-history sibling did not rotate in a bounded batch: %+v", second)
	}
}

func Test064LiveKeepsExistingAuthenticatedSubsetAndMode(t *testing.T) {
	a := prepareLiveTestApp063(t)
	raw, _ := installBlockOneHeader063(t, a)
	peer, _ := hashCoordinateBitcoinSource(t, raw)
	hashCoordinateConnect(t, a, peer)
	one := int64(1)
	if _, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &one, To: &one, Mode: "full", Retention: "retain"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if job, err := a.waitIndexBuild(ctx); err != nil || job.State != "complete" {
		t.Fatalf("subset fixture: %+v %v", job, err)
	}
	waitLiveWriterReleased063(t, a)
	// A sparse selected header raises the tip without pretending there is a full
	// chain/provider. The next missing body yields, making requested scope visible.
	f, err := os.OpenFile(a.headersPath, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	header := append([]byte(nil), raw[:80]...)
	header[79] ^= 1 // Deliberately unavailable sparse scheduling anchor.
	if _, err = f.WriteAt(header, 2*80); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	a.setStatus(func(st *appStatus) {
		st.HeaderCount = 3
		st.HeaderHeight = 2
		st.HeaderState = "current"
		st.Syncing = false
		st.Error = ""
	})
	if _, err := a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "enable"}); err != nil {
		t.Fatal(err)
	}
	a.liveIndexTick()
	job := waitLiveWriterReleased063(t, a)
	if job.State != "waiting" || job.From != 1 || job.Mode != "full" || job.Retention != "retain" {
		t.Fatalf("Live silently refilled before authenticated subset or changed mode: %+v", job)
	}
	store, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || store.checkpoint == nil || store.checkpoint.From != 1 {
		t.Fatalf("subset history changed: %+v %v", store, err)
	}
}
