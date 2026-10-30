package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Storage/authority fixture with an explicitly installed admitted run. Public
// Start/Resume coverage is separate from this deterministic consumer fixture.
func projectionConsumerFixture(t *testing.T) (*app, *recipeProjectionRunConsumer, map[int64]blockView) {
	t.Helper()
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	g := projectionTestGeneration(t, a.dataDir, "consumer-addresses", false)
	resolved := projectionTestSave(t, a, g.program.definition)
	one := int64(1)
	req := indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{{Index: resolved.ID, Recipe: true, Armed: true}}}
	plan, err := recipeAdmissionBuild(resolved, req, 1, blocks[1].Hash)
	if err != nil {
		t.Fatal(err)
	}
	compact := compactIndexRunRecipeBinding(plan)
	progress := indexRunProgress{Index: resolved.ID, State: "ready", AdmissionState: "admitted", From: 1, To: 1, Height: -1, ConfigurationRevision: 1, ProfileHash: recipeAdmissionProfile(resolved.ID, resolved.SemanticHash, resolved.Revision), RecipeRevision: resolved.Revision, RecipeSemanticHash: resolved.SemanticHash, Recipe: &compact, RangeMode: "checkpoint_projection"}
	run := &indexRunRecord{ID: "projection-consumer-fixture", State: "running", AdmissionRevision: 1, Request: req, TargetHeight: 1, InitialTargetHeight: 1, TargetHash: blocks[1].Hash, Consumers: []indexRunProgress{progress}}
	a.indexRunMu.Lock()
	a.indexRunLoaded = true
	a.indexRunState = indexRunDocument{Schema: 1, Configured: true, Prepared: req, Run: run}
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	binding, err := recipeBindRun(run, resolved.ID, resolved.SemanticHash, resolved.Revision)
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.openRecipeProjectionRunConsumer(ctx, plan, binding)
	if err != nil {
		t.Fatal(err)
	}
	return a, c, blocks
}

func TestRecipeProjectionConsumerWaitsWithoutStartingParent(t *testing.T) {
	ctx := context.Background()
	a, c, blocks := projectionConsumerFixture(t)
	if _, err := c.Next(ctx); !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatal("missing prefix was not unavailable", err)
	}
	for _, path := range []string{c.generation.profileRoot, filepath.Join(a.dataDir, "indexes", "utxo-state")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("dependency wait created state", path, err)
		}
	}
	u, err := openUTXOStoreContext(ctx, a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	// Explicit fixture source writer, separate from the child-only admitted
	// run. Production source consent is covered by coordinator tests.
	u.publicationCheck = func(ctx context.Context) error { return ctx.Err() }
	utxoApply(t, u, blocks[0])
	if _, err = c.Next(ctx); !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatal("parent genesis substituted for requested block one", err)
	}
	utxoApply(t, u, blocks[1])
	if err = os.Remove(filepath.Join(a.dataDir, "indexes", "sources", blocks[1].Hash+".block")); err != nil {
		t.Fatal(err)
	}
	reads := 0
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) {
		reads++
		return blockView{}, errors.New("body source must not be used")
	}
	result, err := c.Next(ctx)
	if err != nil || result.State != "range_complete" || result.Work != nil || result.VerifiedThrough == nil || *result.VerifiedThrough != 1 || result.VerifiedHash != blocks[1].Hash || reads != 0 {
		t.Fatal("checkpoint-only evaluation failed", result, reads, err)
	}
	head, _ := projectionTestLatest(t, c.generation)
	c, err = a.openRecipeProjectionRunConsumer(ctx, c.plan, c.binding)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Next(ctx); err != nil {
		t.Fatal("committed snapshot could not resume", err)
	}
	again, _ := projectionTestLatest(t, c.generation)
	if again.Checksum != head.Checksum || reads != 0 {
		t.Fatal("reuse changed state or fetched bodies")
	}
	if err = c.Rebind(ctx, 0, blocks[0].Hash); err == nil {
		t.Fatal("finite consumer accepted a different target")
	}
	if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", "address-state")); !os.IsNotExist(err) {
		t.Fatal("custom checkpoint consumer wrote native addresses", err)
	}
}

func TestRecipeProjectionConsumerReusesEarlierPublishedSnapshotWithoutRewind(t *testing.T) {
	ctx := context.Background()
	a, c, blocks := projectionConsumerFixture(t)
	u, err := openUTXOStoreContext(ctx, a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	u.publicationCheck = func(ctx context.Context) error { return ctx.Err() }
	utxoApply(t, u, blocks[0])
	utxoApply(t, u, blocks[1])
	if _, err = c.Next(ctx); err != nil {
		t.Fatal(err)
	}
	b2 := utxoFixtureBlock(2, blocks[1].Hash, utxoFixtureCoinbase(90001, 3))
	utxoApply(t, u, b2)
	// Fixture-only physical publication of a later checkpoint. The finite
	// admitted run still ends at one and must preserve this later saved point.
	if err = c.generation.applySnapshot(ctx, a, 2, "", func(h int64) (string, error) {
		if h == 2 {
			return b2.Hash, nil
		}
		return blocks[h].Hash, nil
	}, projectionGenerationFixtureGuard, nil); err != nil {
		t.Fatal(err)
	}
	head, _ := projectionTestLatest(t, c.generation)
	result, err := c.Next(ctx)
	if err != nil || result.VerifiedThrough == nil || *result.VerifiedThrough != 1 {
		t.Fatal("finite historical reuse failed", result, err)
	}
	after, _ := projectionTestLatest(t, c.generation)
	if after.Checksum != head.Checksum || after.History[0].Height != 2 {
		t.Fatal("finite reuse rewound later saved state")
	}
}

func TestRecipeProjectionConsumerHonorsStopAndCancellation(t *testing.T) {
	for _, mode := range []string{"stop", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			a, c, _ := projectionConsumerFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			} else {
				a.indexRunMu.Lock()
				a.indexRunState.Run.State = "paused"
				a.indexRunMu.Unlock()
			}
			if _, err := c.Next(ctx); err == nil {
				t.Fatal("unavailable authority was accepted")
			}
			if _, err := os.Stat(c.generation.profileRoot); !os.IsNotExist(err) {
				t.Fatal("rejected work created child state", err)
			}
		})
	}
}
