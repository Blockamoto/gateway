package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func saveOrderedRecipeForRun(t *testing.T, a *app, id string, empty bool) recipeRegistryView {
	t.Helper()
	p := orderedGenerationTestProgram(t, id)
	if empty {
		var expr func(*recipeOrderedExpr)
		expr = func(e *recipeOrderedExpr) {
			if e.Call == "retains" {
				value := rovBool(false)
				*e = recipeOrderedExpr{Literal: &value}
				return
			}
			for i := range e.Args {
				expr(&e.Args[i])
			}
			for key, value := range e.Object {
				expr(&value)
				e.Object[key] = value
			}
		}
		var steps func([]recipeOrderedInstruction)
		steps = func(items []recipeOrderedInstruction) {
			for i := range items {
				expr(&items[i].Expr)
				expr(&items[i].Key)
				steps(items[i].Body)
				steps(items[i].Else)
			}
		}
		steps(p.ordered.Steps)
		var err error
		p.definition.Ordered, err = recipeOrderedProgramJSON(*p.ordered)
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(p.definition)
	if err != nil {
		t.Fatal(err)
	}
	return registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(raw), "")))
}

func TestRecipeOrderedPublicFiniteMixedAndPrefixAdmission(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	source := recipeCoordinatorSource(t)
	coins := saveOrderedRecipeForRun(t, a, "my-coins", false)
	empty := saveOrderedRecipeForRun(t, a, "empty-coins", true)
	txs := saveTransactionRecipeForRun(t, a, "my-transactions", false)
	if !coins.Runnable || !empty.Runnable {
		t.Fatal("ordered execution is missing from registry capabilities")
	}
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		return source[target.Height], nil
	}
	one, zero := int64(1), int64(0)
	missing := indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{{Index: coins.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, missing)
	if err != nil || plan.Ready || len(plan.Consumers) != 1 || !plan.Consumers[0].RequiresLeadIn || plan.Consumers[0].State != "needs_prerequisite" {
		t.Fatal("missing genesis prefix was admitted", plan, err)
	}
	p, _ := compileRecipe(coins.Recipe)
	g, _ := openRecipeOrderedGeneration(a.dataDir, p)
	if _, err := os.Stat(g.profileRoot); !os.IsNotExist(err) {
		t.Fatal("planning wrote a generation", err)
	}
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: coins.Recipe.ID, Armed: true}, {Index: empty.Recipe.ID, Armed: true}, {Index: txs.Recipe.ID, Armed: true}, {Index: "utxo-state", Armed: true}}}
	plan, err = a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal("mixed ordered plan", plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, ""); err == nil {
		t.Fatal("unreviewed ordered run started")
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" {
		t.Fatalf("mixed ordered run failed: %+v", done.Run)
	}
	if done.Run.Counts.SourceReads != 2 || done.Run.Counts.OrderedSourceVerifications != 2 || done.Run.Counts.DecodedBlocks != 4 {
		t.Fatal("ordered cohort did not share source authentication or account for its extra decode", done.Run.Counts)
	}
	for _, item := range []recipeRegistryView{coins, empty} {
		out, _, err := a.readRecipeOutput(ctx, item.Recipe.ID, item.Revision, item.SemanticHash, 1, 0, 50, "")
		want := "1"
		if item.Recipe.ID == empty.Recipe.ID {
			want = "0"
		}
		if err != nil || out["state"] != "covered" || out["live_coin_count"] != want {
			t.Fatal("custom composition output", item.Recipe.ID, out, err)
		}
	}
	custom, err := g.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	native, err := openUTXOStoreContext(ctx, a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	compareNativeRecipeUTXO(t, native, custom)
	// An existing complete prefix can satisfy an isolated review without
	// fetching or deriving it again. The first run's full output remains saved.
	plan, err = a.planIndexRun(ctx, missing)
	if err != nil || !plan.Ready || plan.Consumers[0].RecipeStateToken == "" {
		t.Fatal("saved prefix cannot be reused", plan, err)
	}
	if _, err = a.startIndexRun(ctx, missing, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	done = waitRun(t, a)
	if done.Run.State != "complete" || done.Run.Counts.SourceReads != 0 {
		t.Fatal("saved prefix reuse fetched hidden source work", done.Run)
	}
}

func TestRecipeOrderedPublicRebuildIsReviewedAndPreservesState(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	v := saveOrderedRecipeForRun(t, a, "reviewed-coins", false)
	source := recipeCoordinatorSource(t)
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		return source[target.Height], nil
	}
	zero := int64(0)
	req := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if d := waitRun(t, a); d.Run.State != "complete" {
		t.Fatal(d.Run)
	}
	p, _ := compileRecipe(v.Recipe)
	g, _ := openRecipeOrderedGeneration(a.dataDir, p)
	prior, _ := os.ReadFile(g.headPath())
	req.Consumers[0].Rebuild = true
	plan, err = a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready || plan.Consumers[0].RecipeStateToken == "" {
		t.Fatal("rebuild cannot be reviewed", plan, err)
	}
	if after, _ := os.ReadFile(g.headPath()); string(after) != string(prior) {
		t.Fatal("review performed a rebuild")
	}
	// A competing publication after review invalidates Start before any
	// archive/reset. This fixture guard exercises storage concurrency only.
	if err = g.apply(ctx, source[1], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err == nil {
		t.Fatal("changed generation accepted a stale rebuild review")
	}
	prior, _ = os.ReadFile(g.headPath())
	plan, err = a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal("changed generation could not be reviewed again", plan, err)
	}
	started, err := a.startIndexRun(ctx, req, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	if d := waitRun(t, a); d.Run.State != "complete" {
		t.Fatalf("rebuild failed: %+v", d.Run)
	}
	job := started.Run.ID + ":" + v.Recipe.ID
	key := indexDigest(struct{ Semantic, Job string }{g.identity.Semantic, job})
	archived, err := os.ReadFile(filepath.Join(filepath.Dir(g.profileRoot), "archives", key, "indexes", "utxo-state", "head.json"))
	if err != nil || string(archived) != string(prior) {
		t.Fatal("public rebuild lost prior state", err)
	}
}

func TestRecipeOrderedPublicLiveReorgPauseAndRestart(t *testing.T) {
	ctx := context.Background()
	a, source := transactionRunFixture(t)
	v := saveOrderedRecipeForRun(t, a, "live-coins", false)
	altHeader := append([]byte(nil), mainnetBlockOne(t)[:80]...)
	altHeader[79] ^= 1
	digest := hash256(altHeader)
	altHash := reverseHex(digest[:])
	requested := make(chan struct{})
	var once sync.Once
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height == 1 && target.HashDisplay == altHash {
			once.Do(func() { close(requested) })
			<-ctx.Done()
			return blockView{}, ctx.Err()
		}
		b, ok := source[target.Height]
		if !ok {
			return blockView{}, fmt.Errorf("unexpected ordered fixture block")
		}
		return b, nil
	}
	zero := int64(0)
	req := indexRunRequest{In: &zero, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true, Live: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	started, err := a.startIndexRun(ctx, req, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := compileRecipe(v.Recipe)
	g, _ := openRecipeOrderedGeneration(a.dataDir, p)
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		s, _, err := g.inspect(ctx)
		return err == nil && s != nil && s.head != nil && s.head.Checkpoint.Height == 0 && d.Run.State == "waiting"
	})
	a.headerChainMu.Lock()
	_, hash := installBlockOneHeader063(t, a)
	a.headerChainMu.Unlock()
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		s, _, err := g.inspect(ctx)
		return err == nil && s != nil && s.head != nil && s.head.Checkpoint.Height == 1 && d.Run.TargetHeight == 1
	})
	// Synthetic selected-header replacement exercises local recovery only.
	a.headerChainMu.Lock()
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err == nil {
		_, err = f.WriteAt(altHeader, 80)
		_ = f.Close()
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	a.setStatus(func(s *appStatus) { s.HeaderCount = 2; s.HeaderHeight = 1; s.TipHash = altHash })
	select {
	case <-requested:
	case <-time.After(15 * time.Second):
		t.Fatal("ordered reorg did not request replacement", a.indexRunSnapshot())
	}
	s, _, err := g.inspect(ctx)
	if err != nil || s.head.Checkpoint.Height != 0 {
		t.Fatal("ordered fork was not undone before source replacement", err)
	}
	if _, err = a.controlIndexRun(ctx, started.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	if paused := waitRun(t, a); paused.Run.State != "paused" {
		t.Fatal(paused.Run)
	}
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
	a.setStatus(func(s *appStatus) { s.HeaderCount = 2; s.HeaderHeight = 1; s.TipHash = hash })
	changed := v.Recipe
	changed.Name = "new display name"
	encoded, _ := json.Marshal(changed)
	registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(encoded), v.SemanticHash, v.Revision)))
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex(), coverage: newCoverageState()}
	restarted.indexRunSourceBlock = a.indexRunSourceBlock
	saved := restarted.indexRunSnapshot()
	if saved.Error != "" || saved.Run.Consumers[0].Recipe.Revision != v.Revision {
		t.Fatal("restart lost admitted historical revision", saved)
	}
	if _, err = restarted.resumeIndexRun(ctx, saved.Run); err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, restarted, func(d indexRunDocument) bool {
		s, _, err := g.inspect(ctx)
		return err == nil && s != nil && s.head != nil && s.head.Checkpoint.Height == 1 && d.Run.State == "waiting"
	})
	out, _, err := restarted.readRecipeOutput(ctx, v.Recipe.ID, v.Revision, v.SemanticHash, 1, 0, 50, "")
	if err != nil || out["live_coin_count"] != "1" {
		t.Fatal("restarted historical state query", out, err)
	}
	if _, err = restarted.controlIndexRun(ctx, saved.Run.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	_ = waitRun(t, restarted)
}
