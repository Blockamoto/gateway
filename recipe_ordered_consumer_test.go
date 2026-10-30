package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// Installs an admitted run directly for deterministic consumer guard/race tests.
// Separate public Start/Resume tests exercise the actual coordinator entry.
func orderedConsumerFixture(t *testing.T, live bool) (*app, *recipeOrderedRunConsumer, map[int64]blockView) {
	t.Helper()
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	definition := orderedGenerationTestProgram(t, "ordered-coins").definition
	raw, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	view, canonical, err := recipeRegistryCompile(raw)
	if err != nil {
		t.Fatal(err)
	}
	recipeRegistryMu.Lock()
	view, err = recipeRegistrySave(ctx, a.dataDir, view, canonical, "", "")
	recipeRegistryMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := a.ResolveSavedRecipe(ctx, view.Recipe.ID, view.Revision, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Consumers: []indexRunSelection{{Index: view.Recipe.ID, Recipe: true, Armed: true, Live: live}}}
	if !live {
		req.Out = &one
	}
	plan, err := recipeAdmissionBuild(resolved, req, 1, blocks[1].Hash)
	if err != nil {
		t.Fatal(err)
	}
	compact := compactIndexRunRecipeBinding(plan)
	progress := indexRunProgress{Index: view.Recipe.ID, State: "ready", AdmissionState: "admitted", From: 0, To: 1, Height: -1, ConfigurationRevision: 1, ProfileHash: recipeAdmissionProfile(view.Recipe.ID, view.SemanticHash, view.Revision), RecipeRevision: view.Revision, RecipeSemanticHash: view.SemanticHash, Recipe: &compact, RangeMode: "complete_prefix", RequiresGenesis: true}
	run := &indexRunRecord{ID: "ordered-private-fixture", State: "running", AdmissionRevision: 1, Request: req, TargetHeight: 1, InitialTargetHeight: 1, TargetHash: blocks[1].Hash, Consumers: []indexRunProgress{progress}}
	a.indexRunMu.Lock()
	a.indexRunLoaded = true
	a.indexRunState = indexRunDocument{Schema: 1, Configured: true, Prepared: req, Run: run}
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	binding, err := recipeBindRun(run, view.Recipe.ID, view.SemanticHash, view.Revision)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := a.openRecipeOrderedRunConsumer(ctx, plan, binding)
	if err != nil {
		t.Fatal(err)
	}
	return a, consumer, blocks
}

func TestRecipeOrderedConsumerFiniteRestartAndObservedWork(t *testing.T) {
	ctx := context.Background()
	a, c, blocks := orderedConsumerFixture(t, false)
	for height := int64(0); height <= 1; height++ {
		p, err := c.Next(ctx)
		if err != nil || p.Work == nil || p.Work.Height != height || p.Work.StateCommitment == "" {
			t.Fatalf("ordered next %d: %+v %v", height, p, err)
		}
		work := *p.Work
		if err = c.Apply(ctx, work, blocks[height], nil); err != nil {
			t.Fatal(err)
		}
		if err = c.Apply(ctx, work, blocks[height], nil); err == nil {
			t.Fatal("observed work replayed after commit")
		}
		if height == 0 {
			c, err = a.openRecipeOrderedRunConsumer(ctx, c.plan, c.binding)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	progress, err := c.Next(ctx)
	if err != nil || progress.State != "range_complete" || progress.VerifiedThrough == nil || *progress.VerifiedThrough != 1 || progress.VerifiedHash != blocks[1].Hash {
		t.Fatal("ordered completion was not verified", progress, err)
	}
	state, err := c.generation.snapshot(ctx)
	if err != nil || state.head.Checkpoint.LiveOutputs != 1 {
		t.Fatal("ordered durable state unavailable", err)
	}
}

func TestRecipeOrderedConsumerRejectsChangedStateSnapshot(t *testing.T) {
	ctx := context.Background()
	_, c, blocks := orderedConsumerFixture(t, false)
	p, err := c.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.generation.apply(ctx, blocks[0], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	if err = c.Apply(ctx, *p.Work, blocks[0], nil); !errors.Is(err, errRecipeStateConflict) {
		t.Fatal("work borrowed changed state", err)
	}
	p, err = c.Next(ctx)
	if err != nil || p.Work == nil || p.Work.Height != 1 {
		t.Fatal("changed state did not reopen at its real frontier", p, err)
	}
}

func TestRecipeOrderedConsumerRejectsPauseAndChangedTarget(t *testing.T) {
	for _, change := range []string{"pause", "target"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			a, c, blocks := orderedConsumerFixture(t, false)
			p, err := c.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if change == "pause" {
				a.indexRunMu.Lock()
				a.indexRunState.Run.State = "pausing"
				a.indexRunMu.Unlock()
			} else {
				header, err := a.readSelectedHeader(0)
				if err != nil {
					t.Fatal(err)
				}
				a.headerChainMu.Lock()
				f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0)
				if err == nil {
					_, err = f.WriteAt(header, 80)
					_ = f.Close()
				}
				a.headerChainMu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			if err = c.Apply(ctx, *p.Work, blocks[0], nil); err == nil {
				t.Fatal("changed authority published ordered state")
			}
			state, err := c.generation.snapshot(ctx)
			if err != nil || state.head != nil {
				t.Fatal("rejected work advanced the ordered origin", err)
			}
		})
	}
}

func TestRecipeOrderedConsumerLiveSyntheticLocalReorg(t *testing.T) {
	ctx := context.Background()
	a, c, blocks := orderedConsumerFixture(t, true)
	for height := int64(0); height <= 1; height++ {
		p, err := c.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = c.Apply(ctx, *p.Work, blocks[height], nil); err != nil {
			t.Fatal(err)
		}
	}
	// Synthetic selected-header replacement, not a valid external Bitcoin fork.
	header, err := a.readSelectedHeader(0)
	if err != nil {
		t.Fatal(err)
	}
	a.headerChainMu.Lock()
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err == nil {
		_, err = f.WriteAt(header, 80)
		_ = f.Close()
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	newHash := blocks[0].Hash
	a.indexRunMu.Lock()
	a.indexRunState.Run.TargetHash = newHash
	a.indexRunMu.Unlock()
	if err = c.Rebind(ctx, 1, newHash); err != nil {
		t.Fatal(err)
	}
	p, err := c.Next(ctx)
	if err != nil || p.Work == nil || p.Work.Height != 1 || p.VerifiedThrough == nil || *p.VerifiedThrough != 0 {
		t.Fatal("ordered reorg did not recover to the real ancestor", p, err)
	}
	state, err := c.generation.snapshot(ctx)
	if err != nil || state.head.Checkpoint.Height != 0 || state.head.Checkpoint.LiveOutputs != 0 {
		t.Fatal("disconnected state remained queryable", err)
	}
	if err = c.Apply(ctx, *p.Work, blocks[1], nil); err == nil {
		t.Fatal("old branch source was reapplied")
	}
}
