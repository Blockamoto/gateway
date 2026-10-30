package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// These fixtures install admitted records directly for focused coordinator
// scenarios; separate tests exercise public Start and Resume.
func installRecipeCoordinatorFixture(t *testing.T, a *app, plan indexRunPlan) {
	t.Helper()
	progress := append([]indexRunProgress{}, plan.Consumers...)
	for i := range progress {
		if progress[i].Recipe == nil || progress[i].ConfigurationRevision != 1 {
			t.Fatal("missing planned binding")
		}
		progress[i].State, progress[i].AdmissionState = "ready", "admitted"
	}
	run := &indexRunRecord{ID: "recipe-coordinator-fixture", State: "running", Phase: "boundary", AdmissionRevision: 1, Request: plan.Request, TargetHeight: plan.TargetHeight, InitialTargetHeight: plan.TargetHeight, TargetHash: plan.TargetHash, Consumers: progress}
	run.FrontierHeight = initialIndexRunFrontier(run)
	a.indexRunMu.Lock()
	a.indexRunLoaded = true
	a.indexRunError = nil
	a.indexRunState = indexRunDocument{Schema: 1, Configured: true, Prepared: plan.Request, Run: run}
	err := a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}
func launchRecipeCoordinatorFixture(t *testing.T, a *app) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	a.indexMu.Lock()
	if a.indexCancel != nil {
		a.indexMu.Unlock()
		cancel()
		t.Fatal("writer already active")
	}
	a.indexCancel = cancel
	a.indexRunActiveID = "recipe-coordinator-fixture"
	a.indexMu.Unlock()
	go a.runCoordinatedIndexes(ctx, "recipe-coordinator-fixture")
}
func recipeCoordinatorSource(t *testing.T) map[int64]blockView {
	t.Helper()
	b0, _ := recipeConsumerGenesis(t)
	raw := mainnetBlockOne(t)
	hash := hash256(raw[:80])
	b1, err := parseBlockDetailed(1, reverseHex(hash[:]), raw[:80], raw, "fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	b0.Verification.HeaderChainMatch = true
	b1.Verification.HeaderChainMatch = true
	return map[int64]blockView{0: b0, 1: b1}
}
func TestRecipeCoordinatorFiniteOutAndSharedExtraction(t *testing.T) {
	ctx := context.Background()
	a, view, req := savedRecipeRunFixture(t)
	secondJSON := strings.Replace(registryRecipe, "custom-one", "custom-two", 1)
	second := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(secondJSON, "")))
	req.Consumers = append(req.Consumers, indexRunSelection{Index: "custom-two", Armed: true, Live: true})
	zero := int64(0)
	req.Out = &zero
	req.Consumers[0].Live = true
	plan, err := a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Ready {
		t.Fatalf("finite plan blocked: %+v", plan)
	}
	source := recipeCoordinatorSource(t)
	var mu sync.Mutex
	heights := []int64{}
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, _ string) (blockView, error) {
		mu.Lock()
		heights = append(heights, target.Height)
		mu.Unlock()
		b, ok := source[target.Height]
		if !ok {
			return blockView{}, fmt.Errorf("unexpected source height")
		}
		return b, nil
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatalf("public Start failed: %v", err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" {
		t.Fatalf("run: %+v", done.Run)
	}
	store, err := openRecipeStore(a.dataDir, view.Recipe.ID, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	unit, found, err := store.Read(ctx, 0)
	if err != nil || !found || unit.Rows == nil || len(unit.Rows) != 0 {
		t.Fatalf("empty outcome: %+v %v %v", unit, found, err)
	}
	if _, found, err = store.Read(ctx, 1); err != nil || found {
		t.Fatalf("Out exceeded: %v %v", found, err)
	}
	secondStore, err := openRecipeStore(a.dataDir, second.Recipe.ID, second.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	if u, found, err := secondStore.Read(ctx, 0); err != nil || !found || u.Rows == nil || len(u.Rows) != 0 {
		t.Fatalf("second consumer missed shared block: %+v %v %v", u, found, err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, h := range heights {
		if h > 0 {
			t.Fatalf("fetched beyond Out: %v", heights)
		}
	}
	if done.Run.Counts.InscriptionExtractions != 1 {
		t.Fatalf("expected one shared extraction: %+v", done.Run.Counts)
	}
}

func TestRecipeCoordinatorUnavailableLocalAuthorityDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	a, view, req := savedRecipeRunFixture(t)
	zero := int64(0)
	req.Out = &zero
	plan, err := a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	installRecipeCoordinatorFixture(t, a, plan)
	offline := a.headersPath + ".offline"
	if err = os.Rename(a.headersPath, offline); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(offline, a.headersPath)
	launchRecipeCoordinatorFixture(t, a)
	pending := waitRunCondition(t, a, func(d indexRunDocument) bool {
		return d.Run != nil && len(d.Run.Consumers) > 0 && d.Run.Consumers[0].State == "waiting"
	})
	if pending.Run.Consumers[0].Reason == "" {
		t.Fatal("missing authority reason")
	}
	if _, err = a.controlIndexRun(ctx, "recipe-coordinator-fixture", "pause"); err != nil {
		t.Fatal(err)
	}
	waitRun(t, a)
	store, err := openRecipeStore(a.dataDir, view.Recipe.ID, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Read(ctx, 0); err != nil || found {
		t.Fatalf("unavailable authority published: %v %v", found, err)
	}
}

func TestRecipeCoordinatorPausesAndReopensFrozenRevision(t *testing.T) {
	ctx := context.Background()
	a, view, req := savedRecipeRunFixture(t)
	plan, err := a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	source := recipeCoordinatorSource(t)
	entered := make(chan struct{})
	var once sync.Once
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height == 1 {
			once.Do(func() { close(entered) })
			<-ctx.Done()
			return blockView{}, ctx.Err()
		}
		return source[target.Height], nil
	}
	installRecipeCoordinatorFixture(t, a, plan)
	launchRecipeCoordinatorFixture(t, a)
	store, err := openRecipeStore(a.dataDir, view.Recipe.ID, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, found, e := store.Read(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("genesis unit did not commit")
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("block one source was not attempted")
	}
	if _, err = a.controlIndexRun(ctx, "recipe-coordinator-fixture", "pause"); err != nil {
		t.Fatal(err)
	}
	paused := waitRun(t, a)
	if paused.Run.State != "paused" {
		t.Fatalf("not paused: %+v", paused.Run)
	}
	if _, found, err := store.Read(ctx, 1); err != nil || found {
		t.Fatalf("paused body committed: %v %v", found, err)
	}
	// Replace the current registry head; the durable run still names the old
	// immutable revision, which public Resume must retain.
	changed := strings.Replace(registryRecipe, "text/plain", "image/png", 1)
	h := indexInterfaceHandler(a)
	registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(changed, view.SemanticHash, view.Revision)))
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex(), coverage: newCoverageState()}
	restarted.indexRunSourceBlock = func(ctx context.Context, target blockTarget, _ string) (blockView, error) {
		if b, ok := source[target.Height]; ok {
			return b, nil
		}
		return blockView{}, fmt.Errorf("unexpected source")
	}
	snapshot := restarted.indexRunSnapshot()
	if snapshot.Error != "" || snapshot.Run == nil || snapshot.Run.Consumers[0].Recipe.Revision != view.Revision {
		t.Fatalf("restart lost binding: %+v", snapshot)
	}
	if _, err = restarted.resumeIndexRun(ctx, snapshot.Run); err != nil {
		t.Fatalf("public Resume rejected the frozen revision: %v", err)
	}
	done := waitRun(t, restarted)
	if done.Run.State != "complete" {
		t.Fatalf("fixture restart: %+v", done.Run)
	}
	if _, found, err := store.Read(ctx, 1); err != nil || !found {
		t.Fatalf("restart did not finish original generation: %v %v", found, err)
	}
}

func TestRecipeCoordinatorReconcilesReorgAndRetainsOldUnits(t *testing.T) {
	ctx := context.Background()
	a, view, req := savedRecipeRunFixture(t)
	zero := int64(0)
	req.Out = &zero
	plan, err := a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openRecipeStore(a.dataDir, view.Recipe.ID, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	bad := recipeStoreUnit{Schema: 1, Fingerprint: view.SemanticHash, Height: 0, BlockHash: recipeTestHash("a"), Rows: []recipeStoreRow{}}
	if err = store.Publish(ctx, bad, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	oldBytes, err := json.Marshal(bad)
	if err != nil {
		t.Fatal(err)
	}
	source := recipeCoordinatorSource(t)
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, _ string) (blockView, error) {
		return source[target.Height], nil
	}
	installRecipeCoordinatorFixture(t, a, plan)
	launchRecipeCoordinatorFixture(t, a)
	done := waitRun(t, a)
	if done.Run.State != "complete" {
		t.Fatalf("reorg did not complete: %+v", done.Run)
	}
	now, found, err := store.Read(ctx, 0)
	if err != nil || !found || now.BlockHash != genesisHashDisplay {
		t.Fatalf("reorg not rebuilt: %+v %v %v", now, found, err)
	}
	if _, err = os.Stat(filepath.Join(store.dir, "units", recipeStoreHash(oldBytes)+".json")); err != nil {
		t.Fatalf("old immutable unit lost: %v", err)
	}
}

func TestRecipePublicStartRechecksAuthorityBeforePublish(t *testing.T) {
	ctx := context.Background()
	a, view, req := savedRecipeRunFixture(t)
	zero := int64(0)
	req.Out = &zero
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	source := recipeCoordinatorSource(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce sync.Once
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, _ string) (blockView, error) {
		enteredOnce.Do(func() { close(entered) })
		select {
		case <-release:
			return source[target.Height], nil
		case <-ctx.Done():
			return blockView{}, ctx.Err()
		}
	}
	started, err := a.startIndexRun(ctx, req, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(15 * time.Second):
		t.Fatal("source not reached")
	}
	offline := a.headersPath + ".offline"
	if err = os.Rename(a.headersPath, offline); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(offline, a.headersPath)
	close(release)
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		return d.Run != nil && len(d.Run.Consumers) > 0 && (d.Run.Consumers[0].State == "waiting" || d.Run.Consumers[0].State == "failed")
	})
	if _, err = a.controlIndexRun(ctx, started.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	waitRun(t, a)
	store, err := openRecipeStore(a.dataDir, view.Recipe.ID, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Read(ctx, 0); err != nil || found {
		t.Fatalf("published without authority: %v %v", found, err)
	}
}

func TestRecipePublicOffFencesInFlightWork(t *testing.T) {
	ctx := context.Background()
	a, view, req := savedRecipeRunFixture(t)
	zero := int64(0)
	req.Out = &zero
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	source := recipeCoordinatorSource(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, _ string) (blockView, error) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return source[target.Height], nil
		case <-ctx.Done():
			return blockView{}, ctx.Err()
		}
	}
	started, err := a.startIndexRun(ctx, req, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(15 * time.Second):
		t.Fatal("source not reached")
	}
	off := plan.Request.Consumers[0]
	off.Armed = false
	_, err = a.amendIndexRun(ctx, indexRunAmendment{RunID: started.Run.ID, AdmissionRevision: started.Run.AdmissionRevision, Consumers: []indexRunSelection{off}})
	if err != nil {
		t.Fatalf("Off amendment failed: %v", err)
	}
	close(release)
	waitRun(t, a)
	store, err := openRecipeStore(a.dataDir, view.Recipe.ID, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Read(ctx, 0); err != nil || found {
		t.Fatalf("Off track published: %v %v", found, err)
	}
}

func TestRecipeLiveRebindExtensionPauseRestartAndOff(t *testing.T) {
	ctx := context.Background()
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = true
	a.setStatus(func(st *appStatus) {
		st.HeaderHeight = 0
		st.HeaderCount = 1
		st.TipHash = genesisHashDisplay
		st.Ready = true
	})
	view := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(registryRecipe, "")))
	zero := int64(0)
	req := indexRunRequest{In: &zero, Consumers: []indexRunSelection{{Index: "custom-one", Armed: true, Live: true}}}
	source := recipeCoordinatorSource(t)
	alternateHeader := append([]byte(nil), mainnetBlockOne(t)[:80]...)
	alternateHeader[79] ^= 1
	alternateDigest := hash256(alternateHeader)
	alternateHash := reverseHex(alternateDigest[:])
	alternateRequested := make(chan struct{})
	var alternateOnce sync.Once
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height == 1 && target.HashDisplay == alternateHash {
			alternateOnce.Do(func() { close(alternateRequested) })
			<-ctx.Done()
			return blockView{}, ctx.Err()
		}
		b, ok := source[target.Height]
		if !ok {
			return blockView{}, fmt.Errorf("unexpected height")
		}
		return b, nil
	}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("live plan: %+v %v", plan, err)
	}
	started, err := a.startIndexRun(ctx, req, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openRecipeStore(a.dataDir, view.Recipe.ID, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		u, found, e := store.Read(ctx, 0)
		return e == nil && found && u.BlockHash == genesisHashDisplay && d.Run.State == "waiting"
	})
	a.headerChainMu.Lock()
	_, hash := installBlockOneHeader063(t, a)
	a.headerChainMu.Unlock()
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		u, found, e := store.Read(ctx, 1)
		return e == nil && found && u.BlockHash == hash && d.Run.TargetHeight == 1
	})
	// Synthetic header replacement tests reconciliation only; no body for
	// this intentionally invalid chain is ever returned or published.
	a.headerChainMu.Lock()
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err == nil {
		_, err = f.WriteAt(alternateHeader, 80)
		_ = f.Close()
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	a.setStatus(func(st *appStatus) { st.HeaderHeight = 1; st.HeaderCount = 2; st.TipHash = alternateHash })
	select {
	case <-alternateRequested:
	case <-time.After(15 * time.Second):
		t.Fatalf("live reorg did not request the hole: %+v", a.indexRunSnapshot().Run)
	}
	if _, found, e := store.Read(ctx, 1); e != nil || found {
		t.Fatalf("stale Live receipt remained: %v %v", found, e)
	}
	pending := a.indexRunSnapshot()
	if pending.Run == nil || len(pending.Run.Consumers) != 1 || pending.Run.Consumers[0].Height != 0 || pending.Run.Consumers[0].BlockHash != genesisHashDisplay || pending.Run.Consumers[0].NextRequiredHeight == nil || *pending.Run.Consumers[0].NextRequiredHeight != 1 {
		t.Fatalf("stale progress while new body unavailable: %+v", pending.Run)
	}
	if _, err = a.controlIndexRun(ctx, started.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	paused := waitRun(t, a)
	if paused.Run.State != "paused" {
		t.Fatalf("pause: %+v", paused.Run)
	}
	// Restore the real selected header before restart. The removed receipt is
	// rebuilt from the independently verified original block.
	a.headerChainMu.Lock()
	f, err = os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err == nil {
		_, err = f.WriteAt(mainnetBlockOne(t)[:80], 80)
		_ = f.Close()
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	a.setStatus(func(st *appStatus) { st.HeaderHeight = 1; st.HeaderCount = 2; st.TipHash = hash })
	changed := strings.Replace(registryRecipe, "text/plain", "image/png", 1)
	registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(changed, view.SemanticHash, view.Revision)))
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex(), coverage: newCoverageState()}
	restarted.indexRunSourceBlock = a.indexRunSourceBlock
	snap := restarted.indexRunSnapshot()
	if snap.Error != "" || snap.Run.Consumers[0].Recipe.Revision != view.Revision {
		t.Fatalf("binding: %+v", snap)
	}
	if _, err = restarted.resumeIndexRun(ctx, snap.Run); err != nil {
		t.Fatal(err)
	}
	waiting := waitRunCondition(t, restarted, func(d indexRunDocument) bool {
		u, found, e := store.Read(ctx, 1)
		return d.Run != nil && d.Run.State == "waiting" && d.Run.TargetHeight == 1 && e == nil && found && u.BlockHash == hash
	})
	off := waiting.Run.Request.Consumers[0]
	off.Armed = false
	if _, err = restarted.amendIndexRun(ctx, indexRunAmendment{RunID: waiting.Run.ID, AdmissionRevision: waiting.Run.AdmissionRevision, Consumers: []indexRunSelection{off}}); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, restarted)
	if done.Run.State != "complete" {
		t.Fatalf("Off: %+v", done.Run)
	}
}

func TestRecipeLivePreOriginDoesNotFetchOrPublish(t *testing.T) {
	ctx := context.Background()
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = true
	a.setStatus(func(st *appStatus) {
		st.HeaderHeight = 0
		st.HeaderCount = 1
		st.TipHash = genesisHashDisplay
		st.Ready = true
	})
	source := strings.Replace(registryRecipe, `"origin":0`, `"origin":2`, 1)
	view := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(source, "")))
	requested := make(chan int64, 1)
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, _ string) (blockView, error) {
		requested <- target.Height
		return blockView{}, fmt.Errorf("unexpected pre-origin source")
	}
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: view.Recipe.ID, Armed: true, Live: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	started, err := a.startIndexRun(ctx, req, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool { return d.Run != nil && d.Run.State == "waiting" })
	select {
	case h := <-requested:
		t.Fatalf("pre-origin block fetched: %d", h)
	default:
	}
	store, err := openRecipeStore(a.dataDir, view.Recipe.ID, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	recipeGuardUncovered(t, store, 0)
	if _, err = a.controlIndexRun(ctx, started.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	waitRun(t, a)
}

func TestRecipeCoordinatorReportsReusedSelectedCoverage(t *testing.T) {
	ctx := context.Background()
	a, view, req := savedRecipeRunFixture(t)
	zero := int64(0)
	req.Out = &zero
	store, err := openRecipeStore(a.dataDir, view.Recipe.ID, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Publish(ctx, recipeStoreUnit{Schema: 1, Fingerprint: view.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Rows: []recipeStoreRow{}}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) {
		return blockView{}, fmt.Errorf("unexpected source fetch")
	}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" || len(done.Run.Consumers) != 1 || done.Run.Consumers[0].Height != 0 || done.Run.Consumers[0].BlockHash != genesisHashDisplay {
		t.Fatalf("reused coverage progress: %+v", done.Run)
	}
}
