package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests deliberately install an admitted fixture directly. Public Start
// remains gated until the scheduler path and coverage exposure are reviewed.
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
	req.Consumers = append(req.Consumers, indexRunSelection{Index: "custom-two", Armed: true})
	zero := int64(0)
	req.Out = &zero
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

func TestRecipeCoordinatorHoldsReorgAndMissingAuthority(t *testing.T) {
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
	installRecipeCoordinatorFixture(t, a, plan)
	launchRecipeCoordinatorFixture(t, a)
	pending := waitRunCondition(t, a, func(d indexRunDocument) bool {
		return d.Run != nil && len(d.Run.Consumers) > 0 && strings.Contains(d.Run.Consumers[0].Reason, "differs from selected chain")
	})
	if pending.Run.Consumers[0].State != "waiting" {
		t.Fatalf("reorg not waiting: %+v", pending.Run.Consumers[0])
	}
	if _, err = a.controlIndexRun(ctx, "recipe-coordinator-fixture", "pause"); err != nil {
		t.Fatal(err)
	}
	waitRun(t, a)
	old, found, err := store.Read(ctx, 0)
	if err != nil || !found || old.BlockHash != bad.BlockHash {
		t.Fatalf("reorg silently changed data: %+v %v %v", old, found, err)
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
