package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func recipeDAGPublishEmptyAt(t *testing.T, store *recipeStore, height int64, hash string, lineage []recipeDependencyReceipt) recipeStoreRef {
	t.Helper()
	unit := recipeStoreUnit{Schema: 1, Fingerprint: store.fingerprint, Height: height, BlockHash: hash, Rows: []recipeStoreRow{}, Lineage: lineage}
	data, err := json.Marshal(unit)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.publish(context.Background(), unit, nil, recipeTestGuard, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	return recipeStoreRef{BlockHash: hash, Unit: recipeStoreHash(data)}
}
func TestRecipeDAGConsumerSavedParentBackfillAndReorg(t *testing.T) {
	ctx := context.Background()
	a, plan, binding, lineage, first, middle, child, firstRef := recipeDAGInspectFixture(t)
	consumer, err := a.openRecipeDAGRunConsumer(ctx, plan, binding)
	if err != nil {
		t.Fatal(err)
	}
	next, err := consumer.Next(ctx)
	if err != nil || next.Work == nil || next.Work.Height != 1 || next.VerifiedThrough == nil || *next.VerifiedThrough != 0 {
		t.Fatalf("initial next: %+v %v", next, err)
	}
	block := recipeCoordinatorSource(t)[1]
	inventory, e := extractInscriptionOccurrences(block)
	if e != nil {
		t.Fatal(e)
	}
	if err = consumer.Apply(ctx, *next.Work, block, &inventory); !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatalf("missing parent was not unknown: %v", err)
	}
	if _, found, err := child.Read(ctx, 1); err != nil || found {
		t.Fatalf("missing parent published child: %v %v", found, err)
	}
	rootNode := lineage.nodes[lineage.root]
	direct := rootNode.definition.Dependencies[0]
	middleID := recipeGraphIdentity{direct.Instance, direct.Revision, direct.SemanticHash}
	up := lineage.nodes[middleID].definition.Dependencies[0]
	firstAtOne := recipeDAGPublishEmptyAt(t, first, 1, block.Hash, nil)
	recipeDAGPublishEmptyAt(t, middle, 1, block.Hash, []recipeDependencyReceipt{{Name: up.Name, Instance: up.Instance, SemanticHash: up.SemanticHash, Height: 1, BlockHash: block.Hash, Unit: firstAtOne.Unit}})
	consumer.mu.Lock()
	_, same, observeErr := consumer.observe(ctx, consumer.plan(), &consumer.heads, consumer.auditedThrough)
	consumer.mu.Unlock()
	if observeErr != nil || !same {
		t.Fatalf("parent-only backfill at known hole invalidated checked prefix: %v %v", same, observeErr)
	}
	next, err = consumer.Next(ctx)
	if err != nil || next.Work == nil || next.Work.Height != 1 {
		t.Fatalf("saved parent backfill not resumed: %+v %v", next, err)
	}
	if err = consumer.Apply(ctx, *next.Work, block, &inventory); err != nil {
		t.Fatal(err)
	}
	complete, err := consumer.Next(ctx)
	if err != nil || complete.State != "range_complete" || complete.VerifiedThrough == nil || *complete.VerifiedThrough != 1 {
		t.Fatalf("completion: %+v %v", complete, err)
	}
	recipeDAGMakeChildStale(t, lineage, middle, firstRef)
	next, err = consumer.Next(ctx)
	if err != nil || next.Work == nil || next.Work.Height != 0 {
		t.Fatalf("changed parent did not reopen child hole: %+v %v", next, err)
	}
	if _, found, err := child.Read(ctx, 0); err != nil || found {
		t.Fatalf("stale child still visible: %v %v", found, err)
	}
}
func TestRecipeDAGConsumerPauseFencesPendingWork(t *testing.T) {
	ctx := context.Background()
	a, plan, binding, lineage, first, middle, child, _ := recipeDAGInspectFixture(t)
	block := recipeCoordinatorSource(t)[1]
	firstAtOne := recipeDAGPublishEmptyAt(t, first, 1, block.Hash, nil)
	direct := lineage.nodes[lineage.root].definition.Dependencies[0]
	up := lineage.nodes[recipeGraphIdentity{direct.Instance, direct.Revision, direct.SemanticHash}].definition.Dependencies[0]
	recipeDAGPublishEmptyAt(t, middle, 1, block.Hash, []recipeDependencyReceipt{{Name: up.Name, Instance: up.Instance, SemanticHash: up.SemanticHash, Height: 1, BlockHash: block.Hash, Unit: firstAtOne.Unit}})
	consumer, err := a.openRecipeDAGRunConsumer(ctx, plan, binding)
	if err != nil {
		t.Fatal(err)
	}
	next, err := consumer.Next(ctx)
	if err != nil || next.Work == nil {
		t.Fatalf("next: %+v %v", next, err)
	}
	a.indexRunMu.Lock()
	a.indexRunState.Run.State = "pausing"
	a.indexRunMu.Unlock()
	inventory, e := extractInscriptionOccurrences(block)
	if e != nil {
		t.Fatal(e)
	}
	if err = consumer.Apply(ctx, *next.Work, block, &inventory); err == nil {
		t.Fatal("paused DAG work accepted")
	}
	if _, found, err := child.Read(ctx, 1); err != nil || found {
		t.Fatalf("paused DAG published: %v %v", found, err)
	}
}

func TestRecipeDAGConsumerHoleOptimizationStillDetectsEarlierParentChange(t *testing.T) {
	ctx := context.Background()
	for _, unavailable := range []bool{false, true} {
		t.Run(fmt.Sprintf("unavailable_%v", unavailable), func(t *testing.T) {
			a, plan, binding, lineage, first, middle, child, firstRef := recipeDAGInspectFixture(t)
			consumer, err := a.openRecipeDAGRunConsumer(ctx, plan, binding)
			if err != nil {
				t.Fatal(err)
			}
			next, err := consumer.Next(ctx)
			if err != nil || next.Work == nil || next.Work.Height != 1 {
				t.Fatalf("first hole: %+v %v", next, err)
			}
			block := recipeCoordinatorSource(t)[1]
			atOne := recipeDAGPublishEmptyAt(t, first, 1, block.Hash, nil)
			direct := lineage.nodes[lineage.root].definition.Dependencies[0]
			up := lineage.nodes[recipeGraphIdentity{direct.Instance, direct.Revision, direct.SemanticHash}].definition.Dependencies[0]
			recipeDAGPublishEmptyAt(t, middle, 1, block.Hash, []recipeDependencyReceipt{{Name: up.Name, Instance: up.Instance, SemanticHash: up.SemanticHash, Height: 1, BlockHash: block.Hash, Unit: atOne.Unit}})
			if unavailable {
				if err = first.Reconcile(ctx, map[int64]string{0: genesisHashDisplay}, recipeTestGuard); err != nil {
					t.Fatal(err)
				}
			} else {
				recipeDAGMakeChildStale(t, lineage, middle, firstRef)
			}
			if unavailable {
				if _, err = consumer.Next(ctx); !errors.Is(err, errRecipeDependencyUnavailable) {
					t.Fatalf("unavailable earlier parent misclassified: %v", err)
				}
				if _, pending, err := child.inspectReconcileFence(); err != nil || pending {
					t.Fatalf("unknown evidence mutated/fenced child: %v %v", pending, err)
				}
			} else {
				recovered, err := consumer.Next(ctx)
				if err != nil || recovered.Work == nil || recovered.Work.Height != 0 {
					t.Fatalf("earlier changed parent skipped: %+v %v", recovered, err)
				}
				if _, found, err := child.Read(ctx, 0); err != nil || found {
					t.Fatalf("stale child survived: %v %v", found, err)
				}
			}
		})
	}
}
