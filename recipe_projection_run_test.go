package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRecipeProjectionPublicSelectedSourcesSavedReuseAndChildRebuild(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	privateCoins := saveOrderedRecipeForRun(t, a, "projection-source-coins", true)
	nativeGen := projectionTestGeneration(t, a.dataDir, "public-native-addresses", false)
	native := projectionTestSave(t, a, nativeGen.program.definition)
	savedDefinition := nativeGen.program.definition
	savedDefinition.ID, savedDefinition.Name = "public-saved-addresses", "Public saved addresses"
	savedDefinition.Dependencies = []recipeDependency{{Name: "coins", Scope: "saved", Instance: privateCoins.Recipe.ID, Revision: privateCoins.Revision, SemanticHash: privateCoins.SemanticHash, Export: "coins", Coverage: "snapshot_delta", Type: "coin"}}
	saved := projectionTestSave(t, a, savedDefinition)
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{
		{Index: saved.ID, Armed: true}, {Index: native.ID, Armed: true},
		{Index: privateCoins.Recipe.ID, Armed: true}, {Index: "utxo-state", Armed: true},
	}}
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		return blocks[target.Height], nil
	}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal("public checkpoint plan", plan, err)
	}
	for _, progress := range plan.Consumers {
		if progress.Index == saved.ID || progress.Index == native.ID {
			if progress.RequiresGenesis || progress.RangeMode != "checkpoint_projection" || len(progress.Dependencies) != 1 || progress.Dependencies[0].Kind != "snapshot_delta" {
				t.Fatal("checkpoint view borrowed a prefix/same-block contract", progress)
			}
		}
	}
	if _, err = os.Stat(nativeGen.profileRoot); !os.IsNotExist(err) {
		t.Fatal("review created child state", err)
	}
	if _, err = a.startIndexRun(ctx, req, ""); err == nil {
		t.Fatal("unreviewed checkpoint run started")
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" || done.Run.Counts.SourceReads != 2 {
		t.Fatalf("checkpoint cohort: %+v", done.Run)
	}
	for _, expected := range []struct {
		resolved resolvedSavedRecipe
		count    string
	}{{native, "1"}, {saved, "0"}} {
		output, _, err := a.readRecipeOutput(ctx, expected.resolved.ID, expected.resolved.Revision, expected.resolved.SemanticHash, 1, 0, 10, "")
		if err != nil || output["state"] != "covered" || output["included_output_count"] != expected.count {
			t.Fatal("declared source was not used", output, err)
		}
		// The initial snapshot was evaluated at one, not as a hidden child
		// prefix. Parent genesis cannot substitute for an unevaluated child.
		older, _, err := a.readRecipeOutput(ctx, expected.resolved.ID, expected.resolved.Revision, expected.resolved.SemanticHash, 0, 0, 10, "")
		if err != nil || older["state"] != "uncovered" {
			t.Fatal("child genesis was fabricated", older, err)
		}
	}
	if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", "address-state")); !os.IsNotExist(err) {
		t.Fatal("custom views created native address state", err)
	}
	nativeSourcePath := filepath.Join(a.dataDir, "indexes", "utxo-state", "head.json")
	parentBefore, err := os.ReadFile(nativeSourcePath)
	if err != nil {
		t.Fatal(err)
	}
	// Both parents are now saved-only/off. Reading their exact checkpoints
	// must neither fetch bodies nor start/advance a producer.
	reuse := indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{{Index: native.ID, Armed: true}, {Index: saved.ID, Armed: true}}}
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) {
		return blockView{}, fmt.Errorf("checkpoint reuse requested a body")
	}
	plan, err = a.planIndexRun(ctx, reuse)
	if err != nil || !plan.Ready {
		t.Fatal("saved-only checkpoint plan", plan, err)
	}
	if _, err = a.startIndexRun(ctx, reuse, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	done = waitRun(t, a)
	if done.Run.State != "complete" || done.Run.Counts.SourceReads != 0 || done.Run.Counts.DecodedBlocks != 0 {
		t.Fatalf("saved-only reuse fetched bodies: %+v", done.Run)
	}
	prior, err := os.ReadFile(nativeGen.path("projection-head.json"))
	if err != nil {
		t.Fatal(err)
	}
	rebuild := indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{{Index: native.ID, Armed: true, indexRunOptions: indexRunOptions{Rebuild: true}}}}
	plan, err = a.planIndexRun(ctx, rebuild)
	if err != nil || !plan.Ready {
		t.Fatal("child snapshot rebuild wrongly needs genesis", plan, err)
	}
	started, err := a.startIndexRun(ctx, rebuild, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	done = waitRun(t, a)
	if done.Run.State != "complete" || done.Run.Counts.SourceReads != 0 {
		t.Fatalf("child rebuild did not use saved parent: %+v", done.Run)
	}
	archiveKey := indexDigest(struct{ Semantic, Job string }{native.SemanticHash, started.Run.ID + ":" + native.ID})
	archived, err := os.ReadFile(filepath.Join(filepath.Dir(nativeGen.profileRoot), "archives", archiveKey, "projection-head.json"))
	if err != nil || !bytes.Equal(prior, archived) {
		t.Fatal("child rebuild did not preserve prior receipt bytes", err)
	}
	parentAfter, err := os.ReadFile(nativeSourcePath)
	if err != nil || !bytes.Equal(parentBefore, parentAfter) {
		t.Fatal("child-only runs changed the native source", err)
	}
}

func TestRecipeProjectionPublicLiveForkPauseAndHistoricalRestart(t *testing.T) {
	ctx := context.Background()
	a, blocks := transactionRunFixture(t)
	g := projectionTestGeneration(t, a.dataDir, "live-checkpoint-addresses", false)
	resolved := projectionTestSave(t, a, g.program.definition)
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
		return blocks[target.Height], nil
	}
	zero := int64(0)
	req := indexRunRequest{In: &zero, Consumers: []indexRunSelection{{Index: resolved.ID, Armed: true, Live: true}, {Index: "utxo-state", Armed: true, Live: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	started, err := a.startIndexRun(ctx, req, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	childAt := func(height int64) bool {
		recipeProjectionMu.Lock()
		defer recipeProjectionMu.Unlock()
		head, initialized, err := g.inspect(ctx)
		return err == nil && initialized && len(head.History) > 0 && head.History[0].Height == height
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool { return d.Run != nil && d.Run.State == "waiting" && childAt(0) })
	a.headerChainMu.Lock()
	_, hash := installBlockOneHeader063(t, a)
	a.headerChainMu.Unlock()
	waitRunCondition(t, a, func(d indexRunDocument) bool { return d.Run != nil && d.Run.TargetHeight == 1 && childAt(1) })
	a.headerChainMu.Lock()
	file, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err == nil {
		_, err = file.WriteAt(altHeader, 80)
		_ = file.Close()
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	a.setStatus(func(status *appStatus) { status.HeaderCount = 2; status.HeaderHeight = 1; status.TipHash = altHash })
	select {
	case <-requested:
	case <-time.After(15 * time.Second):
		t.Fatal("selected parent did not request replacement", a.indexRunSnapshot())
	}
	output, code, err := a.readRecipeOutput(ctx, resolved.ID, resolved.Revision, resolved.SemanticHash, 1, 0, 10, "")
	if err == nil && output["state"] == "covered" {
		t.Fatal("disconnected child snapshot remained queryable", output, code)
	}
	if _, err = a.controlIndexRun(ctx, started.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	if paused := waitRun(t, a); paused.Run.State != "paused" {
		t.Fatal(paused.Run)
	}
	a.headerChainMu.Lock()
	file, err = os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err == nil {
		_, err = file.WriteAt(mainnetBlockOne(t)[:80], 80)
		_ = file.Close()
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	a.setStatus(func(status *appStatus) { status.HeaderCount = 2; status.HeaderHeight = 1; status.TipHash = hash })
	changed := resolved.Definition
	changed.Name = "Updated checkpoint display name"
	raw, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(raw), resolved.SemanticHash, resolved.Revision)))
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex(), coverage: newCoverageState()}
	restarted.indexRunSourceBlock = a.indexRunSourceBlock
	saved := restarted.indexRunSnapshot()
	if saved.Error != "" || saved.Run == nil {
		t.Fatal(saved)
	}
	if _, err = restarted.resumeIndexRun(ctx, saved.Run); err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, restarted, func(d indexRunDocument) bool {
		if d.Run == nil || d.Run.State != "waiting" {
			return false
		}
		out, _, err := restarted.readRecipeOutput(ctx, resolved.ID, resolved.Revision, resolved.SemanticHash, 1, 0, 10, "")
		return err == nil && out["state"] == "covered" && out["included_output_count"] == "1"
	})
	current := restarted.indexRunSnapshot()
	for _, progress := range current.Run.Consumers {
		if progress.Index == resolved.ID && progress.RecipeRevision != resolved.Revision {
			t.Fatal("resume substituted a newer saved revision", progress)
		}
	}
	if _, err = restarted.controlIndexRun(ctx, saved.Run.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	_ = waitRun(t, restarted)
}

func TestRecipeProjectionPublicMissingSavedParentNeverStartsPrefix(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	coins := saveOrderedRecipeForRun(t, a, "unbuilt-projection-coins", false)
	g := projectionTestGeneration(t, a.dataDir, "waiting-checkpoint-addresses", false)
	d := g.program.definition
	d.Dependencies = []recipeDependency{{Name: "coins", Scope: "saved", Instance: coins.Recipe.ID, Revision: coins.Revision, SemanticHash: coins.SemanticHash, Export: "coins", Coverage: "snapshot_delta", Type: "coin"}}
	child := projectionTestSave(t, a, d)
	program, err := compileRecipe(child.Definition)
	if err != nil {
		t.Fatal(err)
	}
	g, err = openRecipeProjectionGeneration(a.dataDir, program)
	if err != nil {
		t.Fatal(err)
	}
	parentProgram, err := compileRecipe(coins.Recipe)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := openRecipeOrderedGeneration(a.dataDir, parentProgram)
	if err != nil {
		t.Fatal(err)
	}
	one := int64(1)
	req := indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{{Index: child.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal("saved-only missing input should wait", plan, err)
	}
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) {
		return blockView{}, fmt.Errorf("a waiting child requested a body")
	}
	started, err := a.startIndexRun(ctx, req, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		return d.Run != nil && len(d.Run.Consumers) == 1 && d.Run.Consumers[0].State == "waiting"
	})
	if _, err = a.controlIndexRun(ctx, started.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	paused := waitRun(t, a)
	if paused.Run.State != "paused" || paused.Run.Counts.SourceReads != 0 || paused.Run.Counts.DecodedBlocks != 0 {
		t.Fatal("missing dependency triggered source work", paused.Run)
	}
	for _, path := range []string{g.profileRoot, parent.profileRoot, filepath.Join(a.dataDir, "indexes", "utxo-state")} {
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("waiting created an index generation", path, err)
		}
	}
}
