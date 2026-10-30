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

func recipeDAGRenameSaved(t *testing.T, a *app, saved resolvedSavedRecipe) {
	t.Helper()
	definition := saved.Definition
	definition.Name += " updated"
	raw, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	view, canonical, err := recipeRegistryCompile(raw)
	if err != nil {
		t.Fatal(err)
	}
	recipeRegistryMu.Lock()
	_, err = recipeRegistrySave(context.Background(), a.dataDir, view, canonical, saved.SemanticHash, saved.Revision)
	recipeRegistryMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRecipeDAGPublicLiveReorgPauseAndRestart(t *testing.T) {
	ctx := context.Background()
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	a.setStatus(func(st *appStatus) {
		st.HeaderHeight = 0
		st.HeaderCount = 1
		st.TipHash = genesisHashDisplay
		st.Ready = true
	})
	parent := recipeDAGExecutionFixtureRecipe(t, a, "live-parent", nil)
	child := recipeDAGExecutionFixtureRecipe(t, a, "live-child", &parent)
	zero := int64(0)
	req := indexRunRequest{In: &zero, Consumers: []indexRunSelection{{Index: child.ID, Armed: true, Live: true}, {Index: parent.ID, Armed: true, Live: true}}}
	source := recipeCoordinatorSource(t)
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
			return blockView{}, fmt.Errorf("unexpected block height")
		}
		return b, nil
	}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("review: %+v %v", plan, err)
	}
	started, err := a.startIndexRun(ctx, req, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openRecipeStore(a.dataDir, child.ID, child.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		_, ok, e := a.recipeReadDAGUnit(ctx, child, store, 0, genesisHashDisplay, recipeTestGuard)
		return d.Run != nil && ok && e == nil
	})
	a.headerChainMu.Lock()
	_, hash := installBlockOneHeader063(t, a)
	a.headerChainMu.Unlock()
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		_, ok, e := a.recipeReadDAGUnit(ctx, child, store, 1, hash, recipeTestGuard)
		return d.Run != nil && ok && e == nil
	})
	// The alternate header has no body and is used only to exercise invalidation.
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
	a.setStatus(func(st *appStatus) { st.HeaderHeight = 1; st.HeaderCount = 2; st.TipHash = altHash })
	select {
	case <-requested:
	case <-time.After(15 * time.Second):
		t.Fatal("reorg did not request replacement")
	}
	path := fmt.Sprintf("/api/v1/index/recipes/output?id=%s&revision=%s&semantic_hash=%s&height=1", child.ID, child.Revision, child.SemanticHash)
	response := indexInterfaceCall(indexInterfaceHandler(a), "GET", path, "")
	var output map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output["state"] == "covered" {
		t.Fatalf("stale child exposed during reorg: %s", response.Body.String())
	}
	if _, err := a.controlIndexRun(ctx, started.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	if paused := waitRun(t, a); paused.Run.State != "paused" {
		t.Fatalf("pause: %+v", paused.Run)
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
	a.setStatus(func(st *appStatus) { st.HeaderHeight = 1; st.HeaderCount = 2; st.TipHash = hash })
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex(), coverage: newCoverageState()}
	restarted.indexRunSourceBlock = a.indexRunSourceBlock
	if _, err := restarted.controlIndexRun(ctx, started.Run.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, restarted, func(d indexRunDocument) bool {
		_, ok, e := restarted.recipeReadDAGUnit(ctx, child, store, 1, hash, recipeTestGuard)
		return d.Run != nil && d.Run.State == "waiting" && ok && e == nil
	})
	if _, err := restarted.controlIndexRun(ctx, started.Run.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, restarted); done.Run.State != "cancelled" {
		t.Fatalf("stop: %+v", done.Run)
	}
}

func TestRecipeDAGPublicReviewedPlanRejectsChangedRoot(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	parent := recipeDAGExecutionFixtureRecipe(t, a, "review-parent", nil)
	child := recipeDAGExecutionFixtureRecipe(t, a, "review-child", &parent)
	zero := int64(0)
	req := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: child.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("review: %+v %v", plan, err)
	}
	if len(plan.Consumers) != 1 || len(plan.Consumers[0].Dependencies) != 1 || plan.Consumers[0].Dependencies[0].State != "saved_coverage_unknown" {
		t.Fatalf("saved parent must remain unknown: %+v", plan.Consumers)
	}
	recipeDAGRenameSaved(t, a, child)
	if _, err := a.startIndexRun(ctx, req, plan.PlanID); err == nil {
		t.Fatal("stale review was admitted")
	}
}

func TestRecipeDAGPlanUsesOneSelectedIdentityPerReview(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	parent := recipeDAGExecutionFixtureRecipe(t, a, "coherent-parent", nil)
	child := recipeDAGExecutionFixtureRecipe(t, a, "coherent-child", &parent)
	zero := int64(0)
	req := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: child.ID, Armed: true}, {Index: parent.ID, Armed: true}}}
	calls := map[string]int{}
	plan, err := a.planIndexRunResolved(ctx, req, func(id string) (resolvedSavedRecipe, error) {
		calls[id]++
		if calls[id] > 1 {
			return resolvedSavedRecipe{}, fmt.Errorf("selected identity resolved more than once")
		}
		return a.ResolveSavedRecipe(ctx, id, "", "")
	})
	if err != nil || !plan.Ready || calls[parent.ID] != 1 || calls[child.ID] != 1 {
		t.Fatalf("incoherent review: %+v calls=%v err=%v", plan, calls, err)
	}
	if len(plan.Consumers[0].Dependencies) != 1 || plan.Consumers[0].Dependencies[0].State != "selected_coverage_unknown" {
		t.Fatalf("selected parent not recognized: %+v", plan.Consumers)
	}
}

func TestRecipeDAGPublicMissingParentDefinitionFailsClosed(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	parent := recipeDAGExecutionFixtureRecipe(t, a, "broken-parent", nil)
	child := recipeDAGExecutionFixtureRecipe(t, a, "broken-child", &parent)
	zero := int64(0)
	req := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: child.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("review: %+v %v", plan, err)
	}
	dir, err := recipeRegistryDir(a.dataDir, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "revisions", parent.Revision+".json")); err != nil {
		t.Fatal(err)
	}
	blocked, err := a.planIndexRun(ctx, req)
	if err != nil || blocked.Ready || len(blocked.Consumers) != 1 || blocked.Consumers[0].AdmissionState != "blocked" {
		t.Fatalf("missing definition was not blocked: %+v %v", blocked, err)
	}
	if _, err := a.startIndexRun(ctx, req, plan.PlanID); err == nil {
		t.Fatal("missing graph definition was admitted")
	}
	if d := a.indexRunSnapshot(); d.Run != nil {
		t.Fatalf("failed admission created a run: %+v", d.Run)
	}
}

func TestRecipeDAGAdmissionRechecksRegistryAtLockBoundary(t *testing.T) {
	for _, scenario := range []string{"selected-head", "saved-parent-revision"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			a := runTestApp(t)
			parent := recipeDAGExecutionFixtureRecipe(t, a, "fence-parent", nil)
			child := recipeDAGExecutionFixtureRecipe(t, a, "fence-child", &parent)
			zero := int64(0)
			req := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: child.ID, Armed: true}}}
			plan, err := a.planIndexRun(ctx, req)
			if err != nil || !plan.Ready {
				t.Fatalf("review: %+v %v", plan, err)
			}
			called := false
			_, err = a.startIndexRunWithAdmissionBoundary(ctx, req, plan.PlanID, func() {
				called = true
				if scenario == "selected-head" {
					recipeDAGRenameSaved(t, a, child)
					return
				}
				dir, e := recipeRegistryDir(a.dataDir, parent.ID)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.Remove(filepath.Join(dir, "revisions", parent.Revision+".json")); e != nil {
					t.Fatal(e)
				}
			})
			if !called || err == nil || !strings.Contains(err.Error(), "changed since review") {
				t.Fatalf("locked recheck did not refuse the change: called=%v err=%v", called, err)
			}
			if d := a.indexRunSnapshot(); d.Run != nil {
				t.Fatalf("failed admission created a run: %+v", d.Run)
			}
		})
	}
}

func TestRecipeDAGPublicSemanticGenerationReuse(t *testing.T) {
	for _, scenario := range []string{"name-only", "changed-predicate"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			a := runTestApp(t)
			parent := recipeDAGExecutionFixtureRecipe(t, a, "generation-parent", nil)
			child := recipeDAGExecutionFixtureRecipe(t, a, "generation-child", &parent)
			if scenario == "name-only" {
				recipeDAGRenameSaved(t, a, parent)
			} else {
				raw, err := json.Marshal(parent.Definition)
				if err != nil {
					t.Fatal(err)
				}
				// Change executable behavior while retaining the same export contract.
				changed := strings.Replace(string(raw), `"steps":[`, `"steps":[{"op":"require","test":{"call":"eq","args":[{"field":["content_type"]},{"literal":"image/png"}]}},`, 1)
				if changed == string(raw) {
					t.Fatal("fixture field was not replaced")
				}
				view, canonical, err := recipeRegistryCompile([]byte(changed))
				if err != nil {
					t.Fatal(err)
				}
				recipeRegistryMu.Lock()
				_, err = recipeRegistrySave(ctx, a.dataDir, view, canonical, parent.SemanticHash, parent.Revision)
				recipeRegistryMu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			selected, err := a.ResolveSavedRecipe(ctx, parent.ID, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if selected.Revision == parent.Revision || (selected.SemanticHash == parent.SemanticHash) != (scenario == "name-only") {
				t.Fatalf("unexpected revision identity: %+v", selected)
			}
			order, err := recipeOrderSelectedDAG(ctx, []resolvedSavedRecipe{child, selected}, a.ResolveSavedRecipe)
			if err != nil || len(order) != 2 {
				t.Fatalf("selected order: %+v %v", order, err)
			}
			if scenario == "name-only" && order[0].Instance != parent.ID || scenario != "name-only" && order[0].Instance != child.ID {
				t.Fatalf("wrong semantic-generation order: %+v", order)
			}
			zero := int64(0)
			req := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: child.ID, Armed: true}, {Index: parent.ID, Armed: true}}}
			plan, err := a.planIndexRun(ctx, req)
			if err != nil || !plan.Ready || len(plan.Consumers[0].Dependencies) != 1 {
				t.Fatalf("review: %+v %v", plan, err)
			}
			dep := plan.Consumers[0].Dependencies[0]
			if scenario == "name-only" && (dep.State != "selected_coverage_unknown" || !strings.Contains(dep.Reason, "semantic generation")) {
				t.Fatalf("reuse not explained: %+v", dep)
			}
			if scenario != "name-only" && (dep.State != "saved_coverage_unknown" || !strings.Contains(dep.Reason, "different semantic fingerprint")) {
				t.Fatalf("different generation not explained: %+v", dep)
			}
			source := recipeCoordinatorSource(t)
			a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) { return source[0], nil }
			started, err := a.startIndexRun(ctx, req, plan.PlanID)
			if err != nil {
				t.Fatal(err)
			}
			store, err := openRecipeStore(a.dataDir, child.ID, child.SemanticHash)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "name-only" {
				done := waitRun(t, a)
				if done.Run.State != "complete" {
					t.Fatalf("same generation run: %+v", done.Run)
				}
				if _, found, err := a.recipeReadDAGUnit(ctx, child, store, 0, genesisHashDisplay, recipeTestGuard); err != nil || !found {
					t.Fatalf("same generation child: found=%v err=%v", found, err)
				}
			} else {
				waitRunCondition(t, a, func(d indexRunDocument) bool {
					if d.Run == nil {
						return false
					}
					for _, p := range d.Run.Consumers {
						if p.Index == child.ID && p.State == "waiting" {
							return true
						}
					}
					return false
				})
				if _, found, err := a.recipeReadDAGUnit(ctx, child, store, 0, genesisHashDisplay, recipeTestGuard); err == nil && found {
					t.Fatal("different semantic generation supplied child")
				}
				if _, err := a.controlIndexRun(ctx, started.Run.ID, "cancel"); err != nil {
					t.Fatal(err)
				}
				if done := waitRun(t, a); done.Run.State != "cancelled" {
					t.Fatalf("cancel: %+v", done.Run)
				}
			}
		})
	}
}

func TestRecipeDAGPublicPauseResumePinsHistoricalGraph(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	parent := recipeDAGExecutionFixtureRecipe(t, a, "resume-parent", nil)
	child := recipeDAGExecutionFixtureRecipe(t, a, "resume-child", &parent)
	zero := int64(0)
	req := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: child.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("review: %+v %v", plan, err)
	}
	source := recipeCoordinatorSource(t)
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) { return source[0], nil }
	started, err := a.startIndexRun(ctx, req, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		return d.Run != nil && len(d.Run.Consumers) == 1 && d.Run.Consumers[0].State == "waiting"
	})
	if _, err := a.controlIndexRun(ctx, started.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	if paused := waitRun(t, a); paused.Run.State != "paused" {
		t.Fatalf("pause: %+v", paused.Run)
	}
	// Reload the durable run in a new application instance before resuming.
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus()}
	restarted.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) { return source[0], nil }
	a = restarted
	recipeDAGRenameSaved(t, a, parent)
	recipeDAGRenameSaved(t, a, child)
	store, err := openRecipeStore(a.dataDir, parent.ID, parent.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(ctx, recipeStoreUnit{Schema: 1, Fingerprint: parent.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Rows: []recipeStoreRow{}}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if _, err := a.controlIndexRun(ctx, started.Run.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" || done.Run.Consumers[0].Recipe.Revision != child.Revision {
		t.Fatalf("historical run: %+v", done.Run)
	}
	childStore, err := openRecipeStore(a.dataDir, child.ID, child.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := a.recipeReadDAGUnit(ctx, child, childStore, 0, genesisHashDisplay, recipeTestGuard); err != nil || !found {
		t.Fatalf("historical output: found=%v err=%v", found, err)
	}
}
