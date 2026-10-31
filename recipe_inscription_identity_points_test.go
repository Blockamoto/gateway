package main

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

func recipeIdentityPointFixture(t *testing.T) (*app, blockView, inscriptionBlockResult, *recipeTablePointLookups) {
	t.Helper()
	ctx := context.Background()
	a := runTestApp(t)
	block, inventory := recipeDependencyReferenceBlock(t)
	raw, _, err := exactVerifiedBlockPayload(block)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteAt(raw[:80], block.Height*80)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	a.setStatus(func(status *appStatus) {
		status.HeaderCount = block.Height + 1
		status.HeaderHeight = block.Height
		status.TipHash = block.Hash
	})
	block.Verification.HeaderChainMatch = true
	owned, err := recipeOrderedSourceFromBlock(ctx, block)
	if err != nil {
		t.Fatal(err)
	}
	d := recipeTablesTestProgram(t, "identity-point", block.Height, true).definition
	d.Source = "bitcoin.reveal"
	d.Lookups = recipeTablePointLookupBindings()[1:2]
	reader, err := newRecipeTablePointLookups(a, d, owned)
	if err != nil {
		t.Fatal(err)
	}
	// Fixture startup schedules a delayed cache-index save. Settle accepted
	// startup work before comparing source files around the read-only port.
	a.updateBackgroundWork.Wait()
	return a, block, *inventory, reader
}

func TestRecipeInscriptionIdentityPointCompleteReferenceAndMissing(t *testing.T) {
	ctx := context.Background()
	a, block, inventory, reader := recipeIdentityPointFixture(t)
	binding := recipeTablePointLookupBindings()[1]
	before := recipeDependencyFiles(t, a.dataDir)
	for _, o := range inventory.Occurrences {
		key := inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height)
		value, err := reader.read(ctx, binding, key)
		if err != nil || value.Kind != "object" || value.Object["id"].Text != o.ID || value.Object["ref"].Text != key || value.Object["block"].Text != block.Hash || value.Object["txid"].Text != o.TxID {
			t.Fatal(key, value, err)
		}
	}
	for _, key := range []string{"4294967295i4294967295." + strconv.FormatInt(block.Height, 10), "0i0.0"} {
		value, err := reader.read(ctx, binding, key)
		if err != nil || value.Kind != "missing" {
			t.Fatal("complete inventory absence changed", key, value, err)
		}
	}
	after := recipeDependencyFiles(t, a.dataDir)
	if indexDigest(before) != indexDigest(after) {
		for path, digest := range after {
			if before[path] != digest {
				t.Log("source file changed", path)
			}
		}
		for path := range before {
			if after[path] == "" {
				t.Log("source file removed", path)
			}
		}
		t.Fatal("identity lookup changed source files")
	}
	for _, key := range []string{"01i0." + strconv.FormatInt(block.Height, 10), "1i00." + strconv.FormatInt(block.Height, 10), "1i0." + strconv.FormatInt(block.Height+1, 10), inventory.Occurrences[0].ID, "i0.1.0", "4294967296i0.0"} {
		if _, err := reader.read(ctx, binding, key); err == nil {
			t.Fatal("noncanonical/future identity accepted", key)
		}
	}
}

func TestRecipeInscriptionIdentityPointPublicOwnsIdentityAndAbsence(t *testing.T) {
	ctx := context.Background()
	a, block, inventory, _ := recipeIdentityPointFixture(t)
	o := inventory.Occurrences[0]
	key := inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height)
	d := recipeTablesTestProgram(t, "identity-public", block.Height, true).definition
	d.Source = "bitcoin.reveal"
	d.Lookups = recipeTablePointLookupBindings()[1:2]
	program := recipeOrderedProgram{Steps: []recipeOrderedInstruction{{Op: "for_events", Body: []recipeOrderedInstruction{{Op: "read", Name: "identity", Table: d.Lookups[0].Name, Key: roLit(rovText(key))}, {Op: "put", Table: "counts", Key: roLit(rovText("identity")), Expr: roVar("identity")}, {Op: "read", Name: "absent", Table: d.Lookups[0].Name, Key: roLit(rovText("4294967295i4294967295." + strconv.FormatInt(block.Height, 10)))}}}}}
	d.Ordered, _ = recipeOrderedProgramJSON(program)
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	saved := saveTablesRecipeForRun(t, a, p)
	height := block.Height
	req := indexRunRequest{In: &height, Out: &height, Consumers: []indexRunSelection{{Index: saved.Recipe.ID, Armed: true}}}
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != height {
			t.Errorf("hidden range %d", target.Height)
		}
		return block, nil
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
	if err != nil {
		t.Fatal(err)
	}
	facts, missing := 0, 0
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
		if len(fact.Evidence) != 1 || fact.Evidence[0].BlockHash != block.Hash {
			t.Fatal("absence/identity lacks complete source anchor", fact)
		}
		return nil
	}); err != nil || facts != 2 || missing != 1 {
		t.Fatal(facts, missing, err)
	}
	if err = g.maintain(ctx); err != nil {
		t.Fatal(err)
	}
	out, _, err := a.readRecipeOutput(ctx, saved.Recipe.ID, saved.Revision, saved.SemanticHash, height, 0, 10, "")
	if err != nil || out["state"] != "covered" {
		t.Fatal(out, err)
	}
	rows := out["rows"].([]recipeStoreRow)
	var value recipeOrderedValue
	if len(rows) != 1 || json.Unmarshal(rows[0].Value, &value) != nil || value.Object["id"].Text != o.ID {
		t.Fatal(rows)
	}
}
