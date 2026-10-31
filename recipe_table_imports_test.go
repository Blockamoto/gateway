package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func recipeTableImportProgram(t *testing.T, id string, origin int64, dep recipeDependency) *recipeProgram {
	t.Helper()
	d := recipeTablesTestProgram(t, id, origin, true).definition
	d.Dependencies = []recipeDependency{dep}
	program := recipeOrderedProgram{Steps: []recipeOrderedInstruction{{Op: "for_events", Body: []recipeOrderedInstruction{
		{Op: "read", Name: "parent", Table: dep.Name, Key: roLit(rovText("total"))},
		{Op: "if", Expr: roCall("present", roVar("parent")), Body: []recipeOrderedInstruction{{Op: "put", Table: "counts", Key: roLit(rovText("total")), Expr: roVar("parent")}}},
		{Op: "read", Name: "absent", Table: dep.Name, Key: roLit(rovText("absent"))},
	}}}}
	var err error
	d.Ordered, err = recipeOrderedProgramJSON(program)
	if err != nil {
		t.Fatal(err)
	}
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRecipeTableImportsPublicTopologicalRunAndSavedOnlyReuse(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	parent := saveTablesRecipeForRun(t, a, recipeTablesTestProgram(t, "import-parent", 0, false))
	dep := recipeDependency{Name: "upstream", Scope: "saved", Instance: parent.Recipe.ID, Revision: parent.Revision, SemanticHash: parent.SemanticHash, Export: "counts", Coverage: "snapshot", Type: "typed"}
	child := saveTablesRecipeForRun(t, a, recipeTableImportProgram(t, "import-child", 0, dep))
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: child.Recipe.ID, Armed: true}, {Index: parent.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		return blocks[target.Height], nil
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if d := waitRun(t, a); d.Run.State != "complete" || d.Run.Counts.SourceReads != 2 {
		t.Fatal(d.Run)
	}
	p, _ := compileRecipe(child.Recipe)
	g, _ := openRecipeTablesGeneration(a.dataDir, p)
	point := recipeTablesTestCount(t, g, 2)
	if point.Imports == "" || len(point.Lineage) != 1 || point.Lineage[0].Height != 1 {
		t.Fatal("imports not bound to child checkpoint", point)
	}
	facts := 0
	missing := 0
	if err = recipeTablesTree(ctx, g.path("pages"), point.Imports).walk(func(kv ordinalKV) error {
		var ref recipeImportedFactRef
		if err := json.Unmarshal(kv.Value, &ref); err != nil {
			return err
		}
		fact, err := recipeReadImportedFact(ctx, g.profileRoot, kv.Key, ref)
		if err != nil {
			return err
		}
		facts++
		if fact.Value.Kind == "missing" {
			missing++
		}
		return nil
	}); err != nil || facts != 4 || missing != 2 {
		t.Fatal(facts, missing, err)
	}
	parentProgram, _ := compileRecipe(parent.Recipe)
	parentGeneration, _ := openRecipeTablesGeneration(a.dataDir, parentProgram)
	before := recipeDependencyFiles(t, parentGeneration.profileRoot)
	req.Consumers = req.Consumers[:1]
	req.Consumers[0].Rebuild = true
	plan, err = a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if d := waitRun(t, a); d.Run.State != "complete" {
		t.Fatal(d.Run)
	}
	if indexDigest(before) != indexDigest(recipeDependencyFiles(t, parentGeneration.profileRoot)) {
		t.Fatal("saved-only parent was changed")
	}
	archived, _ := filepath.Glob(filepath.Join(filepath.Dir(g.profileRoot), "archives", "*", "table-head.json"))
	if len(archived) != 1 {
		t.Fatal("missing imported generation archive", archived)
	}
	// The child owns its used facts, including known absence. Removing the
	// producer generation cannot invalidate its completed result or maintenance.
	if err = os.RemoveAll(parentGeneration.profileRoot); err != nil {
		t.Fatal(err)
	}
	out, _, err := a.readRecipeOutput(ctx, child.Recipe.ID, child.Revision, child.SemanticHash, 1, 0, 10, "")
	if err != nil || out["state"] != "covered" {
		t.Fatal(out, err)
	}
	if err = g.maintain(ctx); err != nil {
		t.Fatal("parent cleanup broke child-owned facts", err)
	}
	recipeTablesTestCount(t, g, 2)
}

func TestRecipeTableImportsRejectWithdrawnParentBeforePublication(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	parent := saveTablesRecipeForRun(t, a, recipeTablesTestProgram(t, "withdraw-parent", 0, false))
	pp, _ := compileRecipe(parent.Recipe)
	pg, _ := openRecipeTablesGeneration(a.dataDir, pp)
	if err := pg.create(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pg.apply(ctx, blocks[0], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	dep := recipeDependency{Name: "upstream", Instance: parent.Recipe.ID, Revision: parent.Revision, SemanticHash: parent.SemanticHash, Export: "counts", Coverage: "snapshot", Type: "typed"}
	p := recipeTableImportProgram(t, "withdraw-child", 0, dep)
	g, _ := openRecipeTablesGeneration(a.dataDir, p)
	if err := g.create(ctx); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(g.path("table-head.json"))
	set, err := a.openRecipeTableDependencies(ctx, p.definition, 0, blocks[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	work := context.WithValue(ctx, recipeTableDependenciesKey{}, set)
	err = g.apply(work, blocks[0], func(publish func() error) error {
		if err := pg.reconcile(ctx, 0, func(context.Context, int64) (string, error) { return testID(669490), nil }, orderedGenerationTestGuard); err != nil {
			return err
		}
		return publish()
	})
	if !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatal("withdrawn parent published", err)
	}
	after, _ := os.ReadFile(g.path("table-head.json"))
	if string(after) != string(before) {
		t.Fatal("stale child changed head")
	}
}

func TestRecipeTableImportsLargeFactsCorruptionDefersCollection(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	p := recipeTablesTestProgram(t, "large-import-parent", 0, false)
	program := *p.ordered
	program.Steps[0].Body[0].Body = []recipeOrderedInstruction{{Op: "put", Table: "counts", Key: roLit(rovText("total")), Expr: roLit(rovText(strings.Repeat("owned-parent-payload", 5000)))}}
	p.definition.Ordered, _ = recipeOrderedProgramJSON(program)
	p, _ = compileRecipe(p.definition)
	parent := saveTablesRecipeForRun(t, a, p)
	pg, _ := openRecipeTablesGeneration(a.dataDir, p)
	if err := pg.create(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pg.apply(ctx, blocks[0], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	dep := recipeDependency{Name: "upstream", Instance: parent.Recipe.ID, Revision: parent.Revision, SemanticHash: parent.SemanticHash, Export: "counts", Coverage: "snapshot", Type: "typed"}
	child := recipeTableImportProgram(t, "large-import-child", 0, dep)
	g, _ := openRecipeTablesGeneration(a.dataDir, child)
	if err := g.create(ctx); err != nil {
		t.Fatal(err)
	}
	set, err := a.openRecipeTableDependencies(ctx, child.definition, 0, blocks[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.apply(context.WithValue(ctx, recipeTableDependenciesKey{}, set), blocks[0], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	_, point, err := g.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	required := ""
	if err = recipeTablesTree(ctx, g.path("pages"), point.Imports).walk(func(kv ordinalKV) error {
		var ref recipeImportedFactRef
		if err := json.Unmarshal(kv.Value, &ref); err != nil {
			return err
		}
		if ref.Blob != "" {
			required = recipeImportedFactPath(g.profileRoot, ref.Blob)
		}
		return nil
	}); err != nil || required == "" {
		t.Fatal(required, err)
	}
	orphan := []byte(`{"unreachable":true}`)
	hash := recipeStoreHash(orphan)
	path := recipeImportedFactPath(g.profileRoot, hash)
	if err = recipeStorePutImmutable(filepath.Dir(path), hash, orphan); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err = os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(required)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(required, []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = g.maintain(ctx); !errors.Is(err, ErrRecipeTablesCorrupt) {
		t.Fatal("corrupt required fact was not fenced", err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("collection ran before required fact validation", err)
	}
	if err = os.WriteFile(required, good, 0600); err != nil {
		t.Fatal(err)
	}
	if err = g.maintain(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("unreachable fact not collected", err)
	}
	if _, err = os.Stat(required); err != nil {
		t.Fatal(err)
	}
}

func TestRecipeTableImportsMissingParentWaitsWithoutSourceOrWriter(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	_ = recipeCoordinatorSource(t)
	parent := saveTablesRecipeForRun(t, a, recipeTablesTestProgram(t, "unbuilt-table-parent", 0, false))
	dep := recipeDependency{Name: "parent", Instance: parent.Recipe.ID, Revision: parent.Revision, SemanticHash: parent.SemanticHash, Export: "counts", Coverage: "snapshot", Type: "typed"}
	child := saveTablesRecipeForRun(t, a, recipeTableImportProgram(t, "waiting-table-child", 0, dep))
	zero := int64(0)
	req := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: child.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) {
		t.Error("waiting child fetched source before parent coverage")
		return blockView{}, errors.New("unexpected source read")
	}
	started, err := a.startIndexRun(ctx, req, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		if d.Run == nil {
			return false
		}
		for _, p := range d.Run.Consumers {
			if p.Index == child.Recipe.ID {
				return p.State == "waiting"
			}
		}
		return false
	})
	if _, err = a.controlIndexRun(ctx, started.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "paused" || done.Run.Counts.SourceReads != 0 {
		t.Fatal(done.Run)
	}
	p, _ := compileRecipe(parent.Recipe)
	g, _ := openRecipeTablesGeneration(a.dataDir, p)
	if _, err = os.Stat(g.profileRoot); !os.IsNotExist(err) {
		t.Fatal("child started parent state", err)
	}
}

func TestRecipeTableImportsLiveOriginPauseRestartAndRecovery(t *testing.T) {
	ctx := context.Background()
	a, blocks := transactionRunFixture(t)
	parent := saveTablesRecipeForRun(t, a, recipeTablesTestProgram(t, "live-table-parent", 1, false))
	dep := recipeDependency{Name: "parent", Instance: parent.Recipe.ID, Revision: parent.Revision, SemanticHash: parent.SemanticHash, Export: "counts", Coverage: "snapshot", Type: "typed"}
	child := saveTablesRecipeForRun(t, a, recipeTableImportProgram(t, "live-table-child", 1, dep))
	zero := int64(0)
	req := indexRunRequest{In: &zero, Consumers: []indexRunSelection{{Index: child.Recipe.ID, Armed: true, Live: true}, {Index: parent.Recipe.ID, Armed: true, Live: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != 1 {
			t.Errorf("unexpected lead-in %d", target.Height)
		}
		return blocks[target.Height], nil
	}
	started, err := a.startIndexRun(ctx, req, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool { return d.Run.State == "waiting" })
	a.headerChainMu.Lock()
	installBlockOneHeader063(t, a)
	a.headerChainMu.Unlock()
	p, _ := compileRecipe(child.Recipe)
	g, _ := openRecipeTablesGeneration(a.dataDir, p)
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		recipeOrderedGenerationMu.Lock()
		defer recipeOrderedGenerationMu.Unlock()
		_, point, err := g.snapshot(ctx)
		return err == nil && point.Height == 1 && point.Imports != "" && d.Run.State == "waiting"
	})
	if _, err = a.controlIndexRun(ctx, started.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a); done.Run.State != "paused" {
		t.Fatal(done.Run)
	}
	before := recipeTablesTestCount(t, g, 1)
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex(), coverage: newCoverageState()}
	restarted.indexRunSourceBlock = a.indexRunSourceBlock
	saved := restarted.indexRunSnapshot()
	if _, err = restarted.resumeIndexRun(ctx, saved.Run); err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, restarted, func(d indexRunDocument) bool { return d.Run.State == "waiting" })
	if _, err = restarted.controlIndexRun(ctx, saved.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	_ = waitRun(t, restarted)
	after := recipeTablesTestCount(t, g, 1)
	if indexDigest(before) != indexDigest(after) {
		t.Fatal("restart changed imported state")
	}
	// Selecting the retained empty seed restores state and fact roots together;
	// deterministic replay against the same parent recreates the original point.
	if err = g.reconcile(ctx, 1, func(context.Context, int64) (string, error) { return testID(669495), nil }, orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	_, seed, err := g.snapshot(ctx)
	if err != nil || seed.Imports != "" || seed.Root != "" || seed.Height != 0 {
		t.Fatal(seed, err)
	}
	sources, err := restarted.openRecipeTableDependencies(ctx, p.definition, 1, blocks[1].Hash)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.apply(context.WithValue(ctx, recipeTableDependenciesKey{}, sources), blocks[1], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	replayed := recipeTablesTestCount(t, g, 1)
	if indexDigest(before) != indexDigest(replayed) {
		t.Fatal("replay changed imported point")
	}
}

func TestRecipeTableImportsNativeDistrictOwnsTypedWinner(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	native := bitmapTestConsumer(t, a.dataDir, bitmapStandardDistrictID)
	block, _ := recipeDependencyReferenceBlock(t)
	block.Verification.HeaderChainMatch = true
	if err := native.Apply(ctx, block, nil); err != nil {
		t.Fatal(err)
	}
	revision, semantic, err := recipeNativeBitmapDistrictRevision()
	if err != nil {
		t.Fatal(err)
	}
	dep := recipeDependency{Name: "districts", Scope: "builtin", Instance: bitmapStandardDistrictID, Revision: revision, SemanticHash: semantic, Export: "winners", Coverage: "snapshot", Type: "typed"}
	p := recipeTableImportProgram(t, "native-table-child", block.Height, dep)
	p.definition.Source = "bitcoin.reveal"
	program := *p.ordered
	program.Steps[0].Body = program.Steps[0].Body[:2]
	program.Steps[0].Body[0].Key = roLit(rovText("0"))
	p.definition.Ordered, _ = recipeOrderedProgramJSON(program)
	p, err = compileRecipe(p.definition)
	if err != nil {
		t.Fatal(err)
	}
	g, _ := openRecipeTablesGeneration(a.dataDir, p)
	if err = g.create(ctx); err != nil {
		t.Fatal(err)
	}
	sources, err := a.openRecipeTableDependencies(ctx, p.definition, block.Height, block.Hash)
	if err != nil {
		t.Fatal(err)
	}
	work := context.WithValue(ctx, recipeTableDependenciesKey{}, sources)
	before, _ := os.ReadFile(g.path("table-head.json"))
	err = g.apply(work, block, func(publish func() error) error {
		bitmapSnapshotMu.Lock()
		defer bitmapSnapshotMu.Unlock()
		return publish()
	})
	if !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatal("busy native snapshot did not wait", err)
	}
	if after, _ := os.ReadFile(g.path("table-head.json")); string(after) != string(before) {
		t.Fatal("busy snapshot published child")
	}
	if err = g.apply(work, block, orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	_, point, err := g.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	edit := newRecipeTablesEdit(ctx, g.profileRoot, point.Root, p.definition.Tables)
	value, err := edit.ReadOrdered(ctx, recipeStateKey{Table: "counts", Key: "total"})
	expected, readErr := sources.sources[dep.Name].read(ctx, "0")
	if err != nil || readErr != nil || value.Kind != "object" || indexDigest(value) != indexDigest(expected) {
		t.Fatal(value, err, readErr)
	}
	if err = os.RemoveAll(native.dir); err != nil {
		t.Fatal(err)
	}
	if err = g.maintain(ctx); err != nil {
		t.Fatal("native parent cleanup broke owned fact", err)
	}
}
