package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRecipeTablePointRejectsUnknownContractsAndUnavailableBytes(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	p := recipeBlockContextTestProgram(t, "point-contract", 1, roLit(rovText("0")))
	owned, err := recipeOrderedSourceFromBlock(ctx, blocks[1])
	if err != nil {
		t.Fatal(err)
	}
	reader, err := newRecipeTablePointLookups(a, p.definition, owned)
	if err != nil {
		t.Fatal(err)
	}
	a.settingsMu.Lock()
	a.settings.NetworkDisabled = true
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settingsMu.Unlock()
	// The ordinary fixture already retains genesis. Use a fresh source profile
	// with the same selected headers to exercise genuinely unavailable bytes.
	reader.a = &app{dataDir: t.TempDir(), headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex(), coverage: newCoverageState()}
	if _, err = reader.read(ctx, p.definition.Lookups[0], "0"); !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatal("missing body became complete context", err)
	}
	for _, mutate := range []func(*recipeDefinition){func(d *recipeDefinition) { d.Lookups[0].Revision = testID(669621) }, func(d *recipeDefinition) { d.Lookups[0].Name = "counts" }, func(d *recipeDefinition) { d.Lookups[0].Provider = "unknown" }, func(d *recipeDefinition) { d.Lookups[0].Key = "txid" }} {
		raw, _ := json.Marshal(p.definition)
		d, err := parseRecipe(raw)
		if err != nil {
			t.Fatal(err)
		}
		mutate(&d)
		if _, err = compileRecipe(d); err == nil {
			t.Fatal("unsupported point source compiled", d.Lookups)
		}
	}
	program := *p.ordered
	program.Steps[0].Body[0].Op = "delete"
	program.Steps[0].Body[0].Name = ""
	d := p.definition
	d.Ordered, _ = recipeOrderedProgramJSON(program)
	if _, err = compileRecipe(d); err == nil {
		t.Fatal("point source acquired write access")
	}
}

func TestRecipeTablePointPublicationRechecksHistoricalHeader(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	if _, err := retainVerifiedRawBlock(ctx, a.dataDir, blocks[0], nil); err != nil {
		t.Fatal(err)
	}
	p := recipeBlockContextTestProgram(t, "point-fence", 1, roLit(rovText("0")))
	g, _ := openRecipeTablesGeneration(a.dataDir, p)
	if err := g.create(ctx); err != nil {
		t.Fatal(err)
	}
	owned, err := recipeOrderedSourceFromBlock(ctx, blocks[1])
	if err != nil {
		t.Fatal(err)
	}
	set, err := a.openRecipeTableDependencies(ctx, p.definition, 1, blocks[1].Hash)
	if err != nil {
		t.Fatal(err)
	}
	set.lookups, err = newRecipeTablePointLookups(a, p.definition, owned)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(g.path("table-head.json"))
	header, err := a.readSelectedHeader(0)
	if err != nil {
		t.Fatal(err)
	}
	changed := append([]byte{}, header...)
	changed[79] ^= 1
	err = g.apply(context.WithValue(ctx, recipeTableDependenciesKey{}, set), blocks[1], func(publish func() error) error {
		file, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = file.WriteAt(changed, 0)
		file.Close()
		if err != nil {
			return err
		}
		if err = set.checkRegistryLocked(ctx); err != nil {
			return err
		}
		return publish()
	})
	if err == nil {
		t.Fatal("changed historical header published")
	}
	after, _ := os.ReadFile(g.path("table-head.json"))
	if string(after) != string(before) {
		t.Fatal("stale point fact changed child head")
	}
	file, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteAt(header, 0)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
}

func recipeBlockContextTestProgram(t *testing.T, id string, origin int64, key recipeOrderedExpr) *recipeProgram {
	t.Helper()
	d := recipeTablesTestProgram(t, id, origin, true).definition
	d.Lookups = recipeTablePointLookupBindings()
	program := recipeOrderedProgram{Steps: []recipeOrderedInstruction{{Op: "for_events", Body: []recipeOrderedInstruction{{Op: "read", Name: "context", Table: d.Lookups[0].Name, Key: key}, {Op: "put", Table: "counts", Key: roLit(rovText("context")), Expr: roVar("context")}}}}}
	d.Ordered, _ = recipeOrderedProgramJSON(program)
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRecipeTablePointCurrentAndHistoricalContextReadOnly(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	if _, err := retainVerifiedRawBlock(ctx, a.dataDir, blocks[0], nil); err != nil {
		t.Fatal(err)
	}
	p := recipeBlockContextTestProgram(t, "point-reader", 1, roLit(rovText("0")))
	owned, err := recipeOrderedSourceFromBlock(ctx, blocks[1])
	if err != nil {
		t.Fatal(err)
	}
	reader, err := newRecipeTablePointLookups(a, p.definition, owned)
	if err != nil {
		t.Fatal(err)
	}
	binding := p.definition.Lookups[0]
	before := recipeDependencyFiles(t, a.dataDir)
	for _, height := range []string{"0", "1", "0"} {
		value, err := reader.read(ctx, binding, height)
		if err != nil || value.Kind != "object" || value.Object["transaction_count"].Uint != 1 || value.Object["block"].Text != blocks[int64(value.Object["height"].Uint)].Hash {
			t.Fatal(value, err)
		}
	}
	if indexDigest(before) != indexDigest(recipeDependencyFiles(t, a.dataDir)) {
		t.Fatal("source point read changed profile files")
	}
	for _, key := range []string{"-1", "01", "2", "18446744073709551615"} {
		if _, err = reader.read(ctx, binding, key); err == nil {
			t.Fatal("invalid/future height accepted", key)
		}
	}
	if err = reader.checkHeadersLocked(ctx); err != nil {
		t.Fatal(err)
	}
	reader.anchors[0] = testID(669601)
	if err = reader.checkHeadersLocked(ctx); err == nil {
		t.Fatal("changed selected source anchor accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = reader.read(cancelled, binding, "0"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRecipeTablePointPublicOwnsContextWithoutNativeWriters(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	if _, err := retainVerifiedRawBlock(ctx, a.dataDir, blocks[0], nil); err != nil {
		t.Fatal(err)
	}
	p := recipeBlockContextTestProgram(t, "point-public", 1, roLit(rovText("0")))
	saved := saveTablesRecipeForRun(t, a, p)
	one := int64(1)
	req := indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{{Index: saved.Recipe.ID, Armed: true}}}
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != 1 {
			t.Errorf("hidden source range %d", target.Height)
		}
		return blocks[target.Height], nil
	}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a); done.Run.State != "complete" || done.Run.Counts.SourceReads != 1 {
		t.Fatal(done.Run)
	}
	g, _ := openRecipeTablesGeneration(a.dataDir, p)
	_, point, err := g.snapshot(ctx)
	if err != nil || point.Imports == "" || len(point.Lineage) != 0 {
		t.Fatal(point, err)
	}
	facts := 0
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
		if fact.Schema != 2 || fact.Lookup == nil || fact.Value.Object["block"].Text != blocks[0].Hash {
			t.Fatal(fact)
		}
		return nil
	}); err != nil || facts != 1 {
		t.Fatal(facts, err)
	}
	for _, native := range []string{"tx-locator", "txo-spender", "inscriptions", "utxo-state", "sat-state"} {
		if _, err = os.Stat(filepath.Join(g.root, "indexes", native, "head.json")); !os.IsNotExist(err) {
			t.Fatal("point source created native writer", native, err)
		}
	}
	if err = g.maintain(ctx); err != nil {
		t.Fatal(err)
	}
	out, _, err := a.readRecipeOutput(ctx, saved.Recipe.ID, saved.Revision, saved.SemanticHash, 1, 0, 10, "")
	if err != nil || out["state"] != "covered" || out["imported_fact_root"] != point.Imports {
		t.Fatal(out, err)
	}
	rows := out["rows"].([]recipeStoreRow)
	var value recipeOrderedValue
	if len(rows) != 1 || json.Unmarshal(rows[0].Value, &value) != nil || value.Object["height"].Uint != 0 || value.Object["transaction_count"].Uint != 1 {
		t.Fatal(rows)
	}
}
