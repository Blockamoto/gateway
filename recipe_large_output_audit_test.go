package main

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Optional allocation/timing report for the same verified block that formerly
// hit the whole-unit cap. Normal spool and public-coordinator tests also cover
// this functionality; this report is diagnostic, not an acceptance gate.
func TestRecipeLargeOutputFormerBoundaryAudit(t *testing.T) {
	if os.Getenv("GATEWAY_RECIPE_LARGE_OUTPUT_AUDIT") != "1" {
		t.Skip("opt-in real-source large sparse-output boundary audit")
	}
	ctx := context.Background()
	block, _ := recipeDependencyReferenceBlock(t)
	base := locatorRecipeProgram(t).definition
	base.ID, base.Name, base.Origin = "large-output-audit", "Large output audit", block.Height
	annotation, _ := json.Marshal(strings.Repeat("a", 16<<10))
	base.Steps[len(base.Steps)-1].Row["annotation"] = recipeExpr{Literal: annotation}
	p, err := compileRecipe(base)
	if err != nil {
		t.Fatal(err)
	}
	source, err := recipeTransactionSourceFromBlock(ctx, block)
	if err != nil {
		t.Fatal(err)
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	store, err := openRecipeStore(t.TempDir(), base.ID, p.semanticHash())
	if err != nil {
		t.Fatal(err)
	}
	spool, err := newRecipeSparseSpool(ctx, store.dir, p)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	if err = p.evaluateTransactionSourceInto(ctx, block, source, nil, spool.Add, spool); err != nil {
		t.Fatal(err)
	}
	staged, err := spool.Seal(recipeStoreUnit{Schema: 1, Fingerprint: p.semanticHash(), Height: block.Height, BlockHash: block.Hash})
	if err != nil {
		t.Fatal(err)
	}
	if staged.Bytes != 52463224 || staged.Rows != 3139 || staged.Digest != "60d405ebf28a790218357d8196666a5d53587d539736a3cbb59028519ddce862" {
		t.Fatal("canonical legacy bytes changed", staged)
	}
	if err = store.publishSparseStaged(ctx, staged, 0, recipeTestGuard, nil); err != nil {
		t.Fatal(err)
	}
	info, err := store.readSparseInfo(ctx, recipeStoreRef{BlockHash: block.Hash, Unit: staged.Digest}, block.Height)
	if err != nil || info.Rows != 3139 {
		t.Fatal(info, err)
	}
	runtime.ReadMemStats(&after)
	t.Logf("verified real block %d: transactions=%d annotation_bytes=%d rows=%d canonical_unit_bytes=%d unit_digest=%s stage_publish_verify=%s total_allocated_bytes=%d (cumulative, not peak memory)", block.Height, len(block.Transactions), 16<<10, info.Rows, staged.Bytes, staged.Digest, time.Since(started), after.TotalAlloc-before.TotalAlloc)
}
