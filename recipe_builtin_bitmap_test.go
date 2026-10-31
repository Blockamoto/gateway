package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestRecipeBitmapDistrictSemanticBinding(t *testing.T) {
	if _, err := builtinBitmapDistrictRecipe(); err != nil {
		t.Fatal(err)
	}
	if err := recipeBitmapDistrictCheckEquivalence(builtinBitmapDistrictSemantic); err != nil {
		t.Fatal(err)
	}
}

func TestRecipeBitmapDistrictCompletePhysicalParity(t *testing.T) {
	ctx := context.Background()
	leftRoot, rightRoot := t.TempDir(), t.TempDir()
	left := bitmapTestConsumer(t, leftRoot, bitmapStandardDistrictID)
	right := bitmapTestConsumer(t, rightRoot, bitmapStandardDistrictID)
	block := bitmapDevelopmentFixtureBlock(792435, "", "0.bitmap", "0.bitmap", "01.bitmap", "792436.bitmap", "0.BITMAP", "0.bitmap\n", " 0.bitmap", "0.bitmap.bitmap", "0.0.bitmap", "18446744073709551615.bitmap", "18446744073709551616.bitmap", ".bitmap", strings.Repeat("1", 129)+".bitmap", "\xff.bitmap", "unrelated")
	block.Transactions[1].Inputs[0].Witness = ordWitness050(inscriptionTestScript([]byte{1}, []byte("TEXT/PLAIN; charset=utf-16; x=y"), []byte{11}, []byte{1}, nil, []byte("0.bitmap")))
	block.Transactions[2].Inputs[0].Witness = ordWitness050(inscriptionTestScript([]byte{1}, []byte("image/png"), nil, []byte("0.bitmap")))
	compare := func(block blockView) {
		t.Helper()
		if err := left.Apply(ctx, block, nil); err != nil {
			t.Fatal(err)
		}
		if err := right.applyBitmapOracle(ctx, block, nil); err != nil {
			t.Fatal(err)
		}
		actual, aerr := json.Marshal(left.head)
		want, werr := json.Marshal(right.head)
		if aerr != nil || werr != nil || !reflect.DeepEqual(actual, want) {
			t.Fatalf("native Bitmap head/roots/reference changed\nactual %s\nwant %s\nerrors %v %v", actual, want, aerr, werr)
		}
	}
	compare(block)
	left = bitmapTestConsumer(t, leftRoot, bitmapStandardDistrictID)
	right = bitmapTestConsumer(t, rightRoot, bitmapStandardDistrictID)
	next := bitmapDevelopmentFixtureBlock(792436, block.Hash, "0.bitmap", "1.bitmap", "1.bitmap", "792436.bitmap", "1..bitmap", "not-a-claim")
	compare(next)
	if left.head.WinnerCount != 3 {
		t.Fatal("first eligible winners changed", left.head.WinnerCount)
	}
	for _, consumer := range []*bitmapRunConsumer{left, right} {
		if err := consumer.Reconcile(ctx, func(height int64) (string, error) {
			if height == block.Height {
				return block.Hash, nil
			}
			return testID(669301), nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := json.Marshal(left.head)
	b, _ := json.Marshal(right.head)
	if !reflect.DeepEqual(a, b) || left.head.Height != block.Height {
		t.Fatal("reorg physical parity changed")
	}
}

func TestRecipeBitmapDistrictRealReferencePhysicalParity(t *testing.T) {
	ctx := context.Background()
	block, _ := recipeDependencyReferenceBlock(t)
	block.Verification.HeaderChainMatch = true
	left := bitmapTestConsumer(t, t.TempDir(), bitmapStandardDistrictID)
	right := bitmapTestConsumer(t, t.TempDir(), bitmapStandardDistrictID)
	if err := left.Apply(ctx, block, nil); err != nil {
		t.Fatal(err)
	}
	if err := right.applyBitmapOracle(ctx, block, nil); err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(left.head)
	b, _ := json.Marshal(right.head)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("real reference Bitmap physical output changed")
	}
}

func TestRecipeBitmapDistrictSavedCompleteProgramMatchesNativeRows(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	block, _ := recipeDependencyReferenceBlock(t)
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
	d, err := parseRecipe(builtinBitmapDistrictJSON)
	if err != nil {
		t.Fatal(err)
	}
	d.ID, d.Name = "my-standard-districts", "My Standard Districts"
	encoded, _ := json.Marshal(d)
	v := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(encoded), "")))
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != block.Height {
			t.Error("hidden District prefix", target.Height)
		}
		return block, nil
	}
	height := block.Height
	req := indexRunRequest{In: &height, Out: &height, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true}}}
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
	p, err := compileRecipe(v.Recipe)
	if err != nil {
		t.Fatal(err)
	}
	g, err := openRecipeTablesGeneration(a.dataDir, p)
	if err != nil {
		t.Fatal(err)
	}
	_, point, err := g.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	edit := newRecipeTablesEdit(ctx, g.profileRoot, point.Root, p.definition.Tables, point.BlobPins)
	defer edit.AbortOrdered()
	native := bitmapTestConsumer(t, t.TempDir(), bitmapStandardDistrictID)
	if err = native.applyBitmapOracle(ctx, block, nil); err != nil {
		t.Fatal(err)
	}
	for _, table := range []struct{ name, root string }{{"winners", native.head.WinnerRoot}, {"candidates", native.head.CandidateRoot}} {
		err = newOrdinalTree(filepath.Join(native.dir, "pages"), table.root).withContext(ctx).walk(func(kv ordinalKV) error {
			var c bitmapDevelopmentCandidate
			if e := json.Unmarshal(kv.Value, &c); e != nil {
				return e
			}
			key := c.Blocator
			if table.name == "winners" {
				key = strconv.FormatUint(*c.District, 10)
			}
			actual, e := edit.ReadOrdered(ctx, recipeStateKey{Table: table.name, Key: key})
			if e != nil {
				return e
			}
			expected := bitmapRecipeValue(c)
			a, _ := json.Marshal(actual)
			b, _ := json.Marshal(expected)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("saved/native logical row differs %s %s\nactual %s\nwant %s", table.name, key, a, b)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if point.Entries != native.head.WinnerCount+native.head.CandidateCount {
		t.Fatal("saved candidate/winner inventory differs", point.Entries)
	}
	for _, id := range []string{bitmapStandardDistrictID, "inscriptions", "utxo-state", "sat-state"} {
		if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", id)); !os.IsNotExist(err) {
			t.Fatal("saved District started native writer", id, err)
		}
	}
	catalogue, ok := bitmapFixedIndexDefinition(bitmapStandardDistrictID)
	if !ok || catalogue.RuleHash != bitmapProfileHash(bitmapStandardProfile("district")) || catalogue.ExecutableRecipeHash != p.semanticHash() {
		t.Fatal("native identity or executable inspector binding changed")
	}
	customRaw := bytes.ReplaceAll(builtinBitmapDistrictJSON, []byte("LmJpdG1hcA=="), []byte("LmN1c3RvbQ=="))
	custom, err := parseRecipe(customRaw)
	if err != nil {
		t.Fatal(err)
	}
	custom.ID, custom.Name = "my-custom-districts", "My custom suffix claims"
	encoded, _ = json.Marshal(custom)
	customView := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(encoded), "")))
	if customView.SemanticHash == v.SemanticHash {
		t.Fatal("changing the claim grammar did not change its fingerprint")
	}
	req.Consumers = []indexRunSelection{{Index: customView.Recipe.ID, Armed: true}}
	plan, err = a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a); done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
	out, _, err := a.readRecipeOutput(ctx, customView.Recipe.ID, customView.Revision, customView.SemanticHash, height, 0, 10, "")
	if err != nil || out["state"] != "covered" || out["state_entry_count"] != "0" {
		t.Fatal("custom JSON grammar did not control saved state", out, err)
	}
}
