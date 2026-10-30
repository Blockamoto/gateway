package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func saveTransactionRecipeForRun(t *testing.T, a *app, id string, filter bool) recipeRegistryView {
	t.Helper()
	p := locatorRecipeProgram(t)
	d := p.definition
	d.ID = id
	d.Name = id
	if filter {
		d.Steps = append([]recipeStep{{Op: "require", Test: &recipeExpr{Call: "eq", Args: []recipeExpr{{Field: []string{"txid"}}, {Literal: json.RawMessage(`"no-match"`)}}}}}, d.Steps...)
	}
	raw, e := json.Marshal(d)
	if e != nil {
		t.Fatal(e)
	}
	return registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(raw), "")))
}
func transactionRunFixture(t *testing.T) (*app, map[int64]blockView) {
	t.Helper()
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = true
	a.setStatus(func(s *appStatus) {
		s.HeaderCount = 1
		s.HeaderHeight = 0
		s.TipHash = genesisHashDisplay
		s.Ready = true
	})
	return a, recipeCoordinatorSource(t)
}
func TestRecipeTransactionCoordinatorFiniteMixedAndEmpty(t *testing.T) {
	ctx := context.Background()
	a, source := transactionRunFixture(t)
	tx := saveTransactionRecipeForRun(t, a, "tx-scan", false)
	empty := saveTransactionRecipeForRun(t, a, "tx-empty", true)
	reveal := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(registryRecipe, "")))
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		return source[target.Height], nil
	}
	z := int64(0)
	req := indexRunRequest{In: &z, Out: &z, Consumers: []indexRunSelection{{Index: tx.Recipe.ID, Armed: true}, {Index: empty.Recipe.ID, Armed: true}, {Index: reveal.Recipe.ID, Armed: true}}}
	plan, e := a.planIndexRun(ctx, req)
	if e != nil || !plan.Ready {
		t.Fatalf("plan: %+v %v", plan, e)
	}
	if _, e = a.startIndexRun(ctx, req, plan.PlanID); e != nil {
		t.Fatal(e)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" {
		t.Fatalf("run: %+v", done.Run)
	}
	for _, v := range []recipeRegistryView{tx, empty, reveal} {
		result, _, e := a.readRecipeOutput(ctx, v.Recipe.ID, v.Revision, v.SemanticHash, 0, 0, 50, "")
		if e != nil {
			t.Fatal(e)
		}
		want := 0
		if v.Recipe.ID == tx.Recipe.ID {
			want = 1
		}
		if result["state"] != "covered" || result["row_count"] != want || result["selected_block_hash"] != genesisHashDisplay {
			t.Fatalf("%s output: %+v", v.Recipe.ID, result)
		}
	}
}
func TestRecipeTransactionIgnoresRevealExtractionFailure(t *testing.T) {
	ctx := context.Background()
	a, source := transactionRunFixture(t)
	tx := saveTransactionRecipeForRun(t, a, "tx-with-broken-reveal", false)
	reveal := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(registryRecipe, "")))
	b := source[0]
	b.Transactions = append([]transactionView(nil), b.Transactions...)
	b.Transactions[0].Inputs = append([]inputView(nil), b.Transactions[0].Inputs...)
	b.Transactions[0].Inputs[0].Witness = []string{"00"}
	b.Verification.WitnessCommitment = false
	if _, e := extractInscriptionOccurrences(b); e == nil {
		t.Fatal("fixture does not trigger extraction failure")
	}
	a.indexRunSourceBlock = func(_ context.Context, _ blockTarget, _ string) (blockView, error) { return b, nil }
	z := int64(0)
	req := indexRunRequest{In: &z, Out: &z, Consumers: []indexRunSelection{{Index: tx.Recipe.ID, Armed: true}, {Index: reveal.Recipe.ID, Armed: true}}}
	plan, e := a.planIndexRun(ctx, req)
	if e != nil || !plan.Ready {
		t.Fatalf("plan: %+v %v", plan, e)
	}
	if _, e = a.startIndexRun(ctx, req, plan.PlanID); e != nil {
		t.Fatal(e)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete_with_errors" {
		t.Fatalf("expected reveal error: %+v", done.Run)
	}
	result, _, e := a.readRecipeOutput(ctx, tx.Recipe.ID, tx.Revision, tx.SemanticHash, 0, 0, 50, "")
	if e != nil || result["row_count"] != 1 {
		t.Fatalf("transaction blocked by extraction failure: %+v %v", result, e)
	}
}
func TestRecipeTransactionLiveReorgPauseRestart(t *testing.T) {
	ctx := context.Background()
	a, source := transactionRunFixture(t)
	v := saveTransactionRecipeForRun(t, a, "tx-live", false)
	altHeader := append([]byte(nil), mainnetBlockOne(t)[:80]...)
	altHeader[79] ^= 1
	altDigest := hash256(altHeader)
	altHash := reverseHex(altDigest[:])
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
			return blockView{}, fmt.Errorf("unexpected block")
		}
		return b, nil
	}
	z := int64(0)
	req := indexRunRequest{In: &z, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true, Live: true}}}
	plan, e := a.planIndexRun(ctx, req)
	if e != nil || !plan.Ready {
		t.Fatalf("plan: %+v %v", plan, e)
	}
	started, e := a.startIndexRun(ctx, req, plan.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	store, e := openRecipeStore(a.dataDir, v.Recipe.ID, v.SemanticHash)
	if e != nil {
		t.Fatal(e)
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		_, found, e := store.Read(ctx, 0)
		return e == nil && found && d.Run.State == "waiting"
	})
	a.headerChainMu.Lock()
	_, hash := installBlockOneHeader063(t, a)
	a.headerChainMu.Unlock()
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		u, found, e := store.Read(ctx, 1)
		return e == nil && found && len(u.Rows) == 1 && u.BlockHash == hash && d.Run.TargetHeight == 1
	})
	a.headerChainMu.Lock()
	f, e := os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if e == nil {
		_, e = f.WriteAt(altHeader, 80)
		_ = f.Close()
	}
	a.headerChainMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	a.setStatus(func(s *appStatus) { s.HeaderCount = 2; s.HeaderHeight = 1; s.TipHash = altHash })
	select {
	case <-requested:
	case <-time.After(15 * time.Second):
		t.Fatal("reorg did not request replacement")
	}
	if _, found, e := store.Read(ctx, 1); e != nil || found {
		t.Fatalf("stale unit: %v %v", found, e)
	}
	if _, e = a.controlIndexRun(ctx, started.Run.ID, "pause"); e != nil {
		t.Fatal(e)
	}
	paused := waitRun(t, a)
	if paused.Run.State != "paused" {
		t.Fatalf("pause: %+v", paused.Run)
	}
	a.headerChainMu.Lock()
	f, e = os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if e == nil {
		_, e = f.WriteAt(mainnetBlockOne(t)[:80], 80)
		_ = f.Close()
	}
	a.headerChainMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	a.setStatus(func(s *appStatus) { s.HeaderCount = 2; s.HeaderHeight = 1; s.TipHash = hash })
	changed := strings.Replace(stringMustJSON(t, v.Recipe), `"name":"tx-live"`, `"name":"tx-live-updated"`, 1)
	registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(changed, v.SemanticHash, v.Revision)))
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex(), coverage: newCoverageState()}
	restarted.indexRunSourceBlock = a.indexRunSourceBlock
	snap := restarted.indexRunSnapshot()
	if snap.Error != "" || snap.Run.Consumers[0].Recipe.Revision != v.Revision {
		t.Fatalf("saved binding: %+v", snap)
	}
	if _, e = restarted.resumeIndexRun(ctx, snap.Run); e != nil {
		t.Fatal(e)
	}
	waitRunCondition(t, restarted, func(d indexRunDocument) bool {
		u, found, e := store.Read(ctx, 1)
		return e == nil && found && len(u.Rows) == 1 && u.BlockHash == hash && d.Run.State == "waiting"
	})
	out, _, e := restarted.readRecipeOutput(ctx, v.Recipe.ID, v.Revision, v.SemanticHash, 1, 0, 50, "")
	if e != nil || out["row_count"] != 1 {
		t.Fatalf("resumed output: %+v %v", out, e)
	}
	if _, e = restarted.controlIndexRun(ctx, snap.Run.ID, "pause"); e != nil {
		t.Fatal(e)
	}
	waitRun(t, restarted)
}
func stringMustJSON(t *testing.T, v any) string {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
