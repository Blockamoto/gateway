package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func recipeRevealTablesTestProgram(t *testing.T, id string, origin int64) *recipeProgram {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("recipes", "reveal-count-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := parseRecipe(raw)
	if err != nil {
		t.Fatal(err)
	}
	d.ID, d.Name, d.Origin = id, id, origin
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRecipeRevealTablesOwnedInventoryAndModeFields(t *testing.T) {
	ctx := context.Background()
	block, _ := recipeDependencyReferenceBlock(t)
	inventory, err := extractInscriptionOccurrences(block)
	if err != nil || len(inventory.Occurrences) == 0 {
		t.Fatal(err)
	}
	expected := map[string]inscriptionOccurrence{}
	for _, o := range inventory.Occurrences {
		expected[inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height)] = o
	}
	seen := 0
	err = recipeRevealTablesWalk(ctx, block, func(event map[string]recipeOrderedValue) error {
		seen++
		o, ok := expected[event["ref"].Text]
		if !ok || !bytes.Equal(event["body"].Bytes, o.Body) || !bytes.Equal(event["content_type"].Bytes, []byte(o.Envelope.ContentType)) || event["reveal_index"].Uint != uint64(o.Index) || event["parent_count"].Uint != uint64(len(o.Envelope.Parents)) {
			t.Fatal("typed source differs from authenticated inventory")
		}
		return nil
	})
	if err != nil || seen != len(expected) {
		t.Fatal(seen, err)
	}
	// Decoded fields outside the raw-owned source cannot substitute identities.
	block.Transactions = append([]transactionView(nil), block.Transactions...)
	block.Transactions[inventory.Occurrences[0].TxIndex].TxID = testID(669201)
	if err = recipeRevealTablesWalk(ctx, block, func(event map[string]recipeOrderedValue) error {
		if event["txid"].Text == testID(669201) {
			t.Fatal("caller-edited decoded transaction used")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bad := inventory
	bad.Occurrences = append([]inscriptionOccurrence(nil), inventory.Occurrences...)
	bad.Occurrences[0].Body = []byte("forged")
	block.inscriptionExtraction = &bad
	seen = 0
	if err = recipeRevealTablesWalk(ctx, block, func(map[string]recipeOrderedValue) error { seen++; return nil }); err == nil || seen != 0 {
		t.Fatal("forged inventory reached the program", seen, err)
	}
	p := recipeRevealTablesTestProgram(t, "mode-fields", block.Height)
	d := p.definition
	d.Ordered = json.RawMessage(`{"steps":[{"op":"for_events","body":[{"op":"let","name":"wrong","expr":{"field":"script"}}]}]}`)
	if _, err = compileRecipe(d); err == nil {
		t.Fatal("transaction script field leaked into reveal mode")
	}
	d = p.definition
	d.Source = "bitcoin.ordered"
	d.Ordered = json.RawMessage(`{"steps":[{"op":"for_events","body":[{"op":"let","name":"wrong","expr":{"field":"body"}}]}]}`)
	if _, err = compileRecipe(d); err == nil {
		t.Fatal("reveal body field leaked into transaction mode")
	}
}

func TestRecipeRevealTablesPublicSharedReferenceReuseQueryAndRebuild(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	block, _ := recipeDependencyReferenceBlock(t)
	inventory, err := extractInscriptionOccurrences(block)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := exactVerifiedBlockPayload(block)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(raw[:80], block.Height*80)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	a.setStatus(func(s *appStatus) {
		s.HeaderCount = block.Height + 1
		s.HeaderHeight = block.Height
		s.TipHash = block.Hash
	})
	block.Verification.HeaderChainMatch = true
	block.inscriptionExtraction = nil
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != block.Height {
			t.Error("hidden reveal prefix", target.Height)
		}
		return block, nil
	}
	v := saveTablesRecipeForRun(t, a, recipeRevealTablesTestProgram(t, "reveal-state-a", block.Height))
	other := saveTablesRecipeForRun(t, a, recipeRevealTablesTestProgram(t, "reveal-state-b", block.Height))
	sparse := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(registryRecipe, "")))
	resolved, err := a.ResolveSavedRecipe(ctx, v.Recipe.ID, "", "")
	if err != nil || resolved.Execution != "ordered_finite_and_live" {
		t.Fatal(resolved, err)
	}
	fields := map[string]bool{}
	for _, field := range resolved.SourceCapability.Fields {
		fields[field.Path[0]] = true
	}
	if !fields["body"] || fields["body_hex"] || fields["script"] {
		t.Fatal("source mode mixed generic and typed fields", fields)
	}
	height := block.Height
	req := indexRunRequest{In: &height, Out: &height, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true}, {Index: other.Recipe.ID, Armed: true}, {Index: sparse.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready || plan.RequiresGenesis {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a); done.Run.State != "complete" || done.Run.Counts.SourceReads != 1 || done.Run.Counts.DecodedBlocks != 2 || done.Run.Counts.OrderedSourceVerifications != 1 || done.Run.Counts.InscriptionExtractions != 1 {
		t.Fatal("reveal cohort did not share source/inventory", done.Run)
	}
	for _, view := range []recipeRegistryView{v, other} {
		out, _, e := a.readRecipeOutput(ctx, view.Recipe.ID, view.Revision, view.SemanticHash, height, 0, 10, "")
		if e != nil || out["state"] != "covered" || out["state_entry_count"] != "1" {
			t.Fatal(out, e)
		}
		rows := out["rows"].([]recipeStoreRow)
		var value recipeOrderedValue
		if len(rows) != 1 || json.Unmarshal(rows[0].Value, &value) != nil || value.Uint != uint64(len(inventory.Occurrences)) {
			t.Fatal("saved reveal count", rows)
		}
	}
	plan, err = a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a); done.Run.State != "complete" || done.Run.Counts.SourceReads != 0 {
		t.Fatal("saved reveal prefix fetched source again", done.Run)
	}
	req.Consumers = []indexRunSelection{{Index: v.Recipe.ID, Armed: true, indexRunOptions: indexRunOptions{Rebuild: true}}}
	plan, err = a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a); done.Run.State != "complete" || done.Run.Counts.SourceReads != 1 {
		t.Fatal(done.Run)
	}
	for _, id := range []string{"inscriptions", "utxo-state", "sat-state"} {
		if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", id)); !os.IsNotExist(err) {
			t.Fatal("reveal table started a native index", id, err)
		}
	}
}

func TestRecipeRevealTablesEvaluatedEmptyBlock(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	source := recipeCoordinatorSource(t)
	v := saveTablesRecipeForRun(t, a, recipeRevealTablesTestProgram(t, "empty-reveal-state", 1))
	one := int64(1)
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		return source[target.Height], nil
	}
	req := indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready || plan.RequiresGenesis {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a); done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
	out, _, err := a.readRecipeOutput(ctx, v.Recipe.ID, v.Revision, v.SemanticHash, 1, 0, 10, "")
	if err != nil || out["state"] != "covered" || out["state_entry_count"] != "0" {
		t.Fatal("empty reveal inventory became a gap", out, err)
	}
}

func TestRecipeRevealTablesLiveOriginAndRestart(t *testing.T) {
	ctx := context.Background()
	a, source := transactionRunFixture(t)
	v := saveTablesRecipeForRun(t, a, recipeRevealTablesTestProgram(t, "live-reveals", 1))
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != 1 {
			t.Error("source before declared reveal origin", target.Height)
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
	p, _ := compileRecipe(v.Recipe)
	g, _ := openRecipeTablesGeneration(a.dataDir, p)
	waitRunCondition(t, a, func(d indexRunDocument) bool { return d.Run.State == "waiting" })
	if _, err = os.Stat(g.profileRoot); !os.IsNotExist(err) {
		t.Fatal("future reveal origin created state", err)
	}
	a.headerChainMu.Lock()
	installBlockOneHeader063(t, a)
	a.headerChainMu.Unlock()
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		point, initialized, e := g.prefixSnapshot(ctx)
		return e == nil && initialized && point.Height == 1 && point.Commitment != "" && d.Run.State == "waiting"
	})
	if _, err = a.controlIndexRun(ctx, started.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	waitRun(t, a)
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex(), coverage: newCoverageState()}
	restarted.indexRunSourceBlock = a.indexRunSourceBlock
	saved := restarted.indexRunSnapshot()
	if saved.Error != "" || saved.Run.Consumers[0].Recipe.Revision != v.Revision {
		t.Fatal(saved)
	}
	if _, err = restarted.resumeIndexRun(ctx, saved.Run); err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, restarted, func(d indexRunDocument) bool {
		point, _, e := g.prefixSnapshot(ctx)
		return e == nil && point.Height == 1 && d.Run.State == "waiting"
	})
	out, _, err := restarted.readRecipeOutput(ctx, v.Recipe.ID, v.Revision, v.SemanticHash, 1, 0, 10, "")
	if err != nil || out["state"] != "covered" || out["state_entry_count"] != "0" {
		t.Fatal(out, err)
	}
	if _, err = restarted.controlIndexRun(ctx, saved.Run.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	waitRun(t, restarted)
}

func TestRecipeRevealTablesRetainedReorgRoots(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	source := recipeCoordinatorSource(t)
	p := recipeRevealTablesTestProgram(t, "reveal-recovery", 0)
	g, err := openRecipeTablesGeneration(a.dataDir, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.create(ctx); err != nil {
		t.Fatal(err)
	}
	for _, height := range []int64{0, 1} {
		if err = g.apply(ctx, source[height], orderedGenerationTestGuard); err != nil {
			t.Fatal(err)
		}
	}
	if err = g.reconcile(ctx, 1, func(_ context.Context, height int64) (string, error) {
		if height == 0 {
			return source[0].Hash, nil
		}
		return testID(669211), nil
	}, orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	point, _, err := g.prefixSnapshot(ctx)
	if err != nil || point.Height != 0 || point.BlockHash != source[0].Hash {
		t.Fatal(point, err)
	}
	if err = g.maintain(ctx); err != nil {
		t.Fatal("retained reveal generation maintenance", err)
	}
}
