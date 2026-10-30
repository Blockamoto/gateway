package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func saveTablesRecipeForRun(t *testing.T, a *app, p *recipeProgram) recipeRegistryView {
	t.Helper()
	raw, err := json.Marshal(p.definition)
	if err != nil {
		t.Fatal(err)
	}
	return registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(raw), "")))
}

func TestRecipeTablesPublicDeclaredOriginReuseAndArchivedRebuild(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	v := saveTablesRecipeForRun(t, a, recipeTablesTestProgram(t, "block-one-count", 1, false))
	if !v.Runnable {
		t.Fatal("typed table recipe is not runnable")
	}
	p, err := compileRecipe(v.Recipe)
	if err != nil {
		t.Fatal(err)
	}
	g, err := openRecipeTablesGeneration(a.dataDir, p)
	if err != nil {
		t.Fatal(err)
	}
	if out, _, err := a.readRecipeOutput(ctx, v.Recipe.ID, v.Revision, v.SemanticHash, 1, 0, 1, ""); err != nil || out["state"] != "uncovered" {
		t.Fatal(out, err)
	}
	one := int64(1)
	req := indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready || plan.RequiresGenesis || plan.Consumers[0].RangeMode != "continuous_prefix" || *plan.Consumers[0].NextRequiredHeight != 1 {
		t.Fatal(plan, err)
	}
	if _, err = os.Stat(g.profileRoot); !os.IsNotExist(err) {
		t.Fatal("read/plan created state", err)
	}
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != 1 {
			t.Errorf("hidden source read before declared origin: %d", target.Height)
		}
		return blocks[target.Height], nil
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if d := waitRun(t, a); d.Run.State != "complete" || d.Run.Counts.SourceReads != 1 {
		t.Fatal(d.Run)
	}
	recipeTablesTestCount(t, g, 1)
	out, _, err := a.readRecipeOutput(ctx, v.Recipe.ID, v.Revision, v.SemanticHash, 1, 0, 1, "")
	if err != nil || out["state"] != "covered" || out["state_entry_count"] != "1" || out["changed_key_count"] != "1" {
		t.Fatal(out, err)
	}
	rows := out["rows"].([]recipeStoreRow)
	var value recipeOrderedValue
	if len(rows) != 1 || rows[0].Key != `["counts","total"]` || json.Unmarshal(rows[0].Value, &value) != nil || value.Uint != 1 {
		t.Fatal(rows)
	}
	if out, _, err = a.readRecipeOutput(ctx, v.Recipe.ID, v.Revision, v.SemanticHash, 0, 0, 1, ""); err != nil || out["state"] != "uncovered" {
		t.Fatal("invented coverage before origin", out, err)
	}
	plan, err = a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if d := waitRun(t, a); d.Run.State != "complete" || d.Run.Counts.SourceReads != 0 {
		t.Fatal("prefix reuse fetched source", d.Run)
	}
	prior, err := os.ReadFile(g.path("table-head.json"))
	if err != nil {
		t.Fatal(err)
	}
	req.Consumers[0].Rebuild = true
	plan, err = a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if after, _ := os.ReadFile(g.path("table-head.json")); string(after) != string(prior) {
		t.Fatal("review changed saved table state")
	}
	started, err := a.startIndexRun(ctx, req, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	if d := waitRun(t, a); d.Run.State != "complete" {
		t.Fatal(d.Run)
	}
	key := indexDigest(struct{ Semantic, Job string }{g.identity.Semantic, started.Run.ID + ":" + v.Recipe.ID})
	archived, err := os.ReadFile(filepath.Join(filepath.Dir(g.profileRoot), "archives", key, "table-head.json"))
	if err != nil || string(archived) != string(prior) {
		t.Fatal("table rebuild lost archived state", err)
	}
	for _, native := range []string{"utxo-state", "sat-state"} {
		if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", native, "head.json")); !os.IsNotExist(err) {
			t.Fatal("table run created native state", native, err)
		}
	}
}

func TestRecipeTablesPublicFutureOriginAndExplicitLeadIn(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	_ = recipeCoordinatorSource(t)
	v := saveTablesRecipeForRun(t, a, recipeTablesTestProgram(t, "future-count", 10, false))
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready || plan.RequiresGenesis || !plan.Consumers[0].Recipe.NoWork {
		t.Fatal(plan, err)
	}
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) {
		t.Error("no-work run fetched source")
		return blockView{}, nil
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if d := waitRun(t, a); d.Run.State != "complete" || d.Run.Counts.SourceReads != 0 {
		t.Fatal(d.Run)
	}
	p, _ := compileRecipe(v.Recipe)
	g, _ := openRecipeTablesGeneration(a.dataDir, p)
	if _, err = os.Stat(g.profileRoot); !os.IsNotExist(err) {
		t.Fatal("no-work created a generation", err)
	}
	v = saveTablesRecipeForRun(t, a, recipeTablesTestProgram(t, "needs-lead-in", 0, false))
	req = indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true}}}
	plan, err = a.planIndexRun(ctx, req)
	if err != nil || plan.Ready || !plan.Consumers[0].RequiresLeadIn {
		t.Fatal("missing declared prefix admitted", plan, err)
	}
}

func TestRecipeTablesPublicLiveCrossesDeclaredOrigin(t *testing.T) {
	ctx := context.Background()
	a, source := transactionRunFixture(t)
	v := saveTablesRecipeForRun(t, a, recipeTablesTestProgram(t, "live-origin-one", 1, false))
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != 1 {
			t.Errorf("Live fetched before its declared origin: %d", target.Height)
		}
		return source[target.Height], nil
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
	waitRunCondition(t, a, func(d indexRunDocument) bool { return d.Run.State == "waiting" })
	p, _ := compileRecipe(v.Recipe)
	g, _ := openRecipeTablesGeneration(a.dataDir, p)
	if _, err = os.Stat(g.profileRoot); !os.IsNotExist(err) {
		t.Fatal("future Live origin created state early", err)
	}
	a.headerChainMu.Lock()
	installBlockOneHeader063(t, a)
	a.headerChainMu.Unlock()
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		s, initialized, err := g.prefixSnapshot(ctx)
		return err == nil && initialized && s.Commitment != "" && s.Height == 1 && d.Run.State == "waiting"
	})
	if _, err = a.controlIndexRun(ctx, started.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool { return d.Run.State == "paused" })
	recipeTablesTestCount(t, g, 1)
}

func TestRecipeTablesPublicPlanRecoversItsDeclaredEmptySeed(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	v := saveTablesRecipeForRun(t, a, recipeTablesTestProgram(t, "forked-origin-one", 1, false))
	p, _ := compileRecipe(v.Recipe)
	g, _ := openRecipeTablesGeneration(a.dataDir, p)
	if err := g.create(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.apply(ctx, blocks[1], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	header := append([]byte(nil), mainnetBlockOne(t)[:80]...)
	header[79] ^= 1
	digest := hash256(header)
	altHash := reverseHex(digest[:])
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(header, 80)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	a.setStatus(func(s *appStatus) { s.HeaderCount = 2; s.HeaderHeight = 1; s.TipHash = altHash })
	one := int64(1)
	req := indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready || *plan.Consumers[0].NextRequiredHeight != 1 {
		t.Fatal("retained empty seed requires unnecessary rebuild", plan, err)
	}
	if !strings.Contains(plan.Consumers[0].Reason, "undo disconnected") {
		t.Fatal(plan.Consumers[0].Reason)
	}
	// The plan itself must not have performed recovery.
	state, _, err := g.prefixSnapshot(ctx)
	if err != nil || state.Commitment == "" || state.BlockHash != blocks[1].Hash {
		t.Fatal(state, err)
	}
}
