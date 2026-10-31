package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRecipeOutputFactSemanticBinding(t *testing.T) {
	if _, err := builtinOutputFactRecipes(); err != nil {
		t.Fatal(err)
	}
	if err := recipeOutputFactCheckEquivalence(builtinOutputFactSemantic, 1); err != nil {
		t.Fatal(err)
	}
	if recipeOutputFactCheckEquivalence(builtinOutputFactSemantic, 2) == nil || recipeOutputFactCheckEquivalence(testID(998001), 1) == nil {
		t.Fatal("changed output fact semantics reused native schema")
	}
}

func TestRecipeOutputFactPhysicalParityAndSavedComposition(t *testing.T) {
	ctx := context.Background()
	block, _ := recipeDependencyReferenceBlock(t)
	programs, err := builtinOutputFactRecipes()
	if err != nil {
		t.Fatal(err)
	}
	for i, policy := range []struct{ values, scripts bool }{{true, false}, {false, true}, {true, true}} {
		nativeRoot, oracleRoot := t.TempDir(), t.TempDir()
		actual, err := storeBlockOutputFactLayersUncachedContext(ctx, nativeRoot, block, policy.values, policy.scripts)
		if err != nil {
			t.Fatal(err)
		}
		want, err := storeBlockOutputFactLayersUncachedOracle(ctx, oracleRoot, block, policy.values, policy.scripts)
		if err != nil || !reflect.DeepEqual(actual, want) {
			t.Fatal("output fact page identities changed", i, err)
		}
		checked := map[string]bool{}
		for pos, ref := range actual {
			if checked[ref.Page] {
				continue
			}
			checked[ref.Page] = true
			a, err := readOutputFactPageContext(ctx, nativeRoot, ref.Page)
			if err != nil {
				t.Fatal(err)
			}
			b, err := readOutputFactPageContext(ctx, oracleRoot, ref.Page)
			if err != nil || !reflect.DeepEqual(a, b) {
				t.Fatal(pos, "physical output page parity", err)
			}
		}
		// The same actual program runs independently through the saved
		// sparse executor; it does not dispatch a native retained-layer writer.
		a := runTestApp(t)
		_ = recipeCoordinatorSource(t)
		raw, _ := json.Marshal(programs[i].definition)
		v := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(raw), "")))
		if !v.Runnable {
			t.Fatal("retained layer copy is not runnable")
		}
		owned, err := recipeOrderedSourceFromBlock(ctx, block)
		if err != nil {
			t.Fatal(err)
		}
		rows := []recipeRowProposal{}
		if err = programs[i].evaluateOrderedSparseInto(ctx, owned.block, func(row recipeRowProposal) error { rows = append(rows, row); return nil }); err != nil || len(rows) != len(actual) {
			t.Fatal("saved output layer composition", i, len(rows), len(actual), err)
		}
		for _, row := range rows {
			_, value := row.Value["value_sats"]
			_, script := row.Value["script"]
			if value != policy.values || script != policy.scripts {
				t.Fatal("retained layer did not select declared fields", i, row)
			}
		}
	}
}

func TestRecipeOutputFactPublicFiniteLayers(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	programs, err := builtinOutputFactRecipes()
	if err != nil {
		t.Fatal(err)
	}
	views := []recipeRegistryView{}
	choices := []indexRunSelection{{Index: "txo-spender", Armed: true, indexRunOptions: indexRunOptions{RetainOutputValues: boolPointer(true), RetainOutputScripts: boolPointer(true)}}}
	for _, p := range programs {
		raw, _ := json.Marshal(p.definition)
		v := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(raw), "")))
		views = append(views, v)
		choices = append(choices, indexRunSelection{Index: v.Recipe.ID, Armed: true})
	}
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		return blocks[target.Height], nil
	}
	one := int64(1)
	req := indexRunRequest{In: &one, Out: &one, Consumers: choices}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready || plan.RequiresGenesis {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a); done.Run.State != "complete" || done.Run.Counts.SourceReads != 1 {
		t.Fatal(done.Run)
	}
	for i, v := range views {
		out, _, err := a.readRecipeOutput(ctx, v.Recipe.ID, v.Revision, v.SemanticHash, 1, 0, 10, "")
		if err != nil || out["state"] != "covered" {
			t.Fatal(out, err)
		}
		rows := out["rows"].([]recipeStoreRow)
		if len(rows) != 1 {
			t.Fatal("saved retained layer output count", i, rows)
		}
		var row map[string]json.RawMessage
		if json.Unmarshal(rows[0].Value, &row) != nil {
			t.Fatal("invalid saved retained row")
		}
		if (row["value_sats"] != nil) != (i != 1) || (row["script"] != nil) != (i != 0) {
			t.Fatal("saved layer policy", i, row)
		}
	}
	s, err := openIndexStore(a.dataDir, "txo-spender")
	if err != nil {
		t.Fatal(err)
	}
	if s.checkpoint == nil || s.checkpoint.Height != 1 {
		t.Fatal("native TXIO retained layer missing")
	}
}

func TestRecipeOutputFactCataloguePreservesFrozenNativeRules(t *testing.T) {
	programs, err := builtinOutputFactRecipes()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"blocks", "txo-spender"} {
		d, err := findIndexDefinition(id)
		if err != nil {
			t.Fatal(err)
		}
		offset := 0
		if id == "txo-spender" {
			offset = 2
		}
		if len(d.ExecutableRecipeVariants) != 3+offset {
			t.Fatal("retained layer recipes absent", id)
		}
		for i, variant := range d.ExecutableRecipeVariants[offset:] {
			parsed, err := parseRecipe(variant.Definition)
			if err != nil {
				t.Fatal(err)
			}
			p, err := compileRecipe(parsed)
			if err != nil || p.semanticHash() != programs[i].semanticHash() || variant.SemanticHash != p.semanticHash() {
				t.Fatal("catalogue recipe is not actual program", id, i, err)
			}
		}
		original := d
		original.ExecutableRecipe, original.ExecutableRecipeHash, original.ExecutableRecipeVariants = nil, "", nil
		original.RuleHash, original.LockReason = "", ""
		original.Locked = false
		if id == "txo-spender" {
			original.Name = "TXO spender"
			original.Theory = "Outpoint to spender; absent knowledge is not proof of unspent."
		}
		want := indexDigest(struct {
			Definition        indexDefinition
			Parser, Reference string
		}{original, inscriptionParserProfile, inscriptionReferenceCommit})
		if d.RuleHash != want {
			t.Fatal("display recipes changed frozen native rule identity", id, d.RuleHash, want)
		}
	}
}

func TestRecipeOutputFactLargeEmptyExactAndCancellation(t *testing.T) {
	ctx := context.Background()
	block := inscriptionTestBlock(transactionView{TxID: strings.Repeat("a", 64), Inputs: []inputView{{PrevTxID: strings.Repeat("b", 64)}}, Outputs: []outputView{{N: 0, ValueSats: ^uint64(0), ScriptPubKey: ""}, {N: 1, ValueSats: 0, ScriptPubKey: strings.Repeat("ab", 520<<10)}, {N: 2, ValueSats: 1, ScriptPubKey: "51"}}})
	for _, policy := range []struct{ values, scripts bool }{{true, false}, {false, true}, {true, true}} {
		actual, err := storeBlockOutputFactLayersUncachedContext(ctx, t.TempDir(), block, policy.values, policy.scripts)
		if err != nil {
			t.Fatal(err)
		}
		want, err := storeBlockOutputFactLayersUncachedOracle(ctx, t.TempDir(), block, policy.values, policy.scripts)
		if err != nil || !reflect.DeepEqual(actual, want) {
			t.Fatal("large/empty/exact output fact parity", policy, err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	seen := 0
	err := recipeOutputFactVisit(cancelled, block, true, true, func(outputFactRow) error { seen++; cancel(); return nil })
	if !errors.Is(err, context.Canceled) || seen != 1 {
		t.Fatal("fact source did not stop at cancellation", seen, err)
	}
	block.Transactions[1].Outputs[1].ScriptPubKey = "AA"
	if _, err = storeBlockOutputFactLayersUncachedContext(ctx, t.TempDir(), block, false, true); err == nil {
		t.Fatal("noncanonical exact script retained")
	}
	if _, err = storeBlockOutputFactLayersUncachedContext(ctx, t.TempDir(), block, true, false); err != nil {
		t.Fatal("value-only layer demanded unused script", err)
	}
}
