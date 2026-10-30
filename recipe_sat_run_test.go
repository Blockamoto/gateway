package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func saveSatRecipeForRun(t *testing.T, a *app, id string, history bool) recipeRegistryView {
	t.Helper()
	p := recipeSatTestProgram(t, id, history)
	raw, err := json.Marshal(p.definition)
	if err != nil {
		t.Fatal(err)
	}
	return registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(raw), "")))
}

func TestRecipeSatPublicMixedPrefixQueriesReuseAndRebuild(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	current := saveSatRecipeForRun(t, a, "current-sats", false)
	history := saveSatRecipeForRun(t, a, "history-sats", true)
	if !current.Runnable || !history.Runnable {
		t.Fatal("FIFO execution unavailable")
	}
	p, _ := compileRecipe(history.Recipe)
	g, _ := openRecipeSatGeneration(a.dataDir, p)
	if out, _, err := a.readRecipeOutput(ctx, history.Recipe.ID, history.Revision, history.SemanticHash, 0, 0, 1, ""); err != nil || out["state"] != "uncovered" {
		t.Fatal("unbuilt FIFO became covered", out, err)
	}
	zero, one := int64(0), int64(1)
	missing := indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{{Index: history.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, missing)
	if err != nil || plan.Ready || !plan.Consumers[0].RequiresLeadIn {
		t.Fatal("missing prefix admitted", plan, err)
	}
	if _, err = os.Stat(g.profileRoot); !os.IsNotExist(err) {
		t.Fatal("planning/query created state", err)
	}
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		return blocks[target.Height], nil
	}
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: current.Recipe.ID, Armed: true}, {Index: history.Recipe.ID, Armed: true}, {Index: "sat-state", Armed: true}, {Index: "utxo-state", Armed: true}}}
	plan, err = a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
	if done.Run.Counts.SourceReads != 2 || done.Run.Counts.OrderedSourceVerifications != 2 || done.Run.Counts.InscriptionExtractions != 2 {
		t.Fatal("FIFO cohort source/inventory sharing", done.Run.Counts)
	}
	for _, v := range []recipeRegistryView{current, history} {
		first, _, err := a.readRecipeOutput(ctx, v.Recipe.ID, v.Revision, v.SemanticHash, 1, 0, 1, "")
		if err != nil || first["state"] != "covered" || first["live_output_count"] != "1" || first["row_format"] != recipeSatQueryProfile {
			t.Fatal(first, err)
		}
		unit, cursor := first["unit"].(string), first["next_cursor"].(string)
		count := len(first["rows"].([]recipeStoreRow))
		for cursor != "" {
			page, _, err := a.readRecipeOutputPage(ctx, v.Recipe.ID, v.Revision, v.SemanticHash, 1, 0, 1, unit, cursor)
			if err != nil || page["unit"] != unit {
				t.Fatal(page, err)
			}
			count += len(page["rows"].([]recipeStoreRow))
			cursor, _ = page["next_cursor"].(string)
		}
		want := 2
		if v.Recipe.ID == history.Recipe.ID {
			want = 4
		}
		if count != want {
			t.Fatal("FIFO output/history count", count, want)
		}
		if _, code, err := a.readRecipeOutputPage(ctx, v.Recipe.ID, v.Revision, v.SemanticHash, 1, 0, 1, "", first["next_cursor"].(string)); code != 400 || err == nil {
			t.Fatal("unbound FIFO cursor accepted", code, err)
		}
	}
	plan, err = a.planIndexRun(ctx, missing)
	if err != nil || !plan.Ready {
		t.Fatal("retained prefix not reusable", plan, err)
	}
	if _, err = a.startIndexRun(ctx, missing, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if d := waitRun(t, a); d.Run.State != "complete" || d.Run.Counts.SourceReads != 0 {
		t.Fatal("prefix reuse fetched source", d.Run)
	}
	prior, err := os.ReadFile(g.path("prefix-head.json"))
	if err != nil {
		t.Fatal(err)
	}
	native, err := os.ReadFile(filepath.Join(a.dataDir, "indexes", "sat-state", "head.json"))
	if err != nil {
		t.Fatal(err)
	}
	rebuild := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: history.Recipe.ID, Armed: true, indexRunOptions: indexRunOptions{Rebuild: true}}}}
	plan, err = a.planIndexRun(ctx, rebuild)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	started, err := a.startIndexRun(ctx, rebuild, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	if d := waitRun(t, a); d.Run.State != "complete" {
		t.Fatal(d.Run)
	}
	job := started.Run.ID + ":" + history.Recipe.ID
	key := indexDigest(struct{ Semantic, Job string }{g.identity.Semantic, job})
	archived, err := os.ReadFile(filepath.Join(filepath.Dir(g.profileRoot), "archives", key, "prefix-head.json"))
	if err != nil || string(archived) != string(prior) {
		t.Fatal("reviewed Sat rebuild lost prior receipt", err)
	}
	if after, _ := os.ReadFile(filepath.Join(a.dataDir, "indexes", "sat-state", "head.json")); string(after) != string(native) {
		t.Fatal("custom Sat rebuild changed native state")
	}
	params := url.Values{"id": {history.Recipe.ID}, "revision": {history.Revision}, "semantic_hash": {history.SemanticHash}, "height": {"1"}, "limit": {"1"}}
	response := indexInterfaceCall(indexInterfaceHandler(a), "GET", "/api/v1/index/recipes/output?"+params.Encode(), "")
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("public FIFO output route", response.Code, response.Body.String())
	}
	good, _ := os.ReadFile(g.path("prefix-head.json"))
	if err = os.Remove(g.path("prefix-head.json")); err != nil {
		t.Fatal(err)
	}
	if _, code, err := a.readRecipeOutput(ctx, history.Recipe.ID, history.Revision, history.SemanticHash, 1, 0, 1, ""); code != 409 || err == nil {
		t.Fatal("lost FIFO head became uncovered/zero", code, err)
	}
	if err = os.WriteFile(g.path("prefix-head.json"), good, 0600); err != nil {
		t.Fatal(err)
	}
	header, _ := os.ReadFile(a.headersPath)
	changed := append([]byte(nil), header...)
	changed[159] ^= 1
	if err = os.WriteFile(a.headersPath, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, code, err := a.readRecipeOutput(ctx, history.Recipe.ID, history.Revision, history.SemanticHash, 1, 0, 1, ""); code != 409 || err == nil {
		t.Fatal("disconnected FIFO checkpoint queried", code, err)
	}
	if err = os.WriteFile(a.headersPath, header, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRecipeSatPublicLiveReorgPauseAndRestart(t *testing.T) {
	ctx := context.Background()
	a, source := transactionRunFixture(t)
	v := saveSatRecipeForRun(t, a, "live-sats", false)
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
	g, _ := openRecipeSatGeneration(a.dataDir, p)
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		s, _, err := g.prefixSnapshot(ctx)
		return err == nil && s.Height == 0 && d.Run.State == "waiting"
	})
	a.headerChainMu.Lock()
	_, hash := installBlockOneHeader063(t, a)
	a.headerChainMu.Unlock()
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		s, _, err := g.prefixSnapshot(ctx)
		return err == nil && s.Height == 1 && d.Run.TargetHeight == 1
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
	s, _, err := g.prefixSnapshot(ctx)
	if err != nil || s.Height != 0 {
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
		s, _, err := g.prefixSnapshot(ctx)
		return err == nil && s.Height == 1 && d.Run.State == "waiting"
	})
	out, _, err := restarted.readRecipeOutput(ctx, v.Recipe.ID, v.Revision, v.SemanticHash, 1, 0, 50, "")
	if err != nil || out["live_output_count"] != "1" {
		t.Fatal("restarted historical state query", out, err)
	}
	if _, err = restarted.controlIndexRun(ctx, saved.Run.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	_ = waitRun(t, restarted)
}
