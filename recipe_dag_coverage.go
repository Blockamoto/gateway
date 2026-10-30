package main

// Bounded same-block DAG coverage proof. The guard is a trusted synchronous
// authority: it must check every supplied anchor against one selected-chain
// snapshot, hold that authority throughout the callback, and fence any run or
// registry conditions the caller needs. Generic coverage stays unavailable.
import (
	"context"
	"fmt"
)

const recipeDAGCoverageMaxPage = 32
const recipeDAGCoverageMaxRefs = 4096

type recipeDAGCoverageGuard func([]recipeCoverageAnchor, func() error) error

func (a *app) recipeDAGCoverage(ctx context.Context, root resolvedSavedRecipe, store *recipeStore, from, to int64, after *int64, revision uint64, limit int, guard recipeDAGCoverageGuard) (recipeCoveragePage, error) {
	var empty recipeCoveragePage
	if ctx == nil || store == nil || guard == nil || limit < 1 || limit > recipeDAGCoverageMaxPage || len(root.Definition.Dependencies) == 0 || store.instance != root.ID || store.fingerprint != root.SemanticHash {
		return empty, fmt.Errorf("invalid dependent DAG coverage request")
	}
	plan, err := a.prepareRecipeLineage(ctx, root)
	if err != nil {
		return empty, err
	}
	if plan.nodes[plan.root].store.dir != store.dir {
		return empty, fmt.Errorf("dependent DAG store belongs to a different profile")
	}
	page, err := store.dependencyCoverageRefs(ctx, from, to, after, revision, limit)
	if err != nil {
		return empty, err
	}
	snapshots := make([]*recipeLineageSnapshot, 0, len(page.Anchors))
	totalRefs := 0
	totalBytes := 0
	for _, anchor := range page.Anchors {
		snapshot, e := plan.captureParents(ctx, anchor.Height, anchor.BlockHash)
		if e != nil {
			return empty, e
		}
		if len(snapshot.refs) > recipeDAGCoverageMaxRefs-totalRefs {
			return empty, fmt.Errorf("dependent DAG coverage lineage budget exceeded")
		}
		totalRefs += len(snapshot.refs)
		if snapshot.decodedBytes > recipeMaxDecodedBytes-totalBytes {
			return empty, errRecipeDependencyBudget
		}
		totalBytes += snapshot.decodedBytes
		// Only immutable receipt identities are retained across the page guard.
		snapshot.direct = nil
		snapshots = append(snapshots, snapshot)
	}
	guardAnchors := append([]recipeCoverageAnchor(nil), page.Anchors...)
	err = recipeStoreGuarded(func(commit func() error) error { return guard(guardAnchors, commit) }, func() error {
		recipeStoreMutex.Lock()
		defer recipeStoreMutex.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := store.rejectReconcileFenceLocked(); err != nil {
			return err
		}
		head, err := store.head()
		if err != nil {
			return err
		}
		if head.Revision != page.Revision {
			return fmt.Errorf("dependent coverage changed during proof")
		}
		for i, anchor := range page.Anchors {
			unit, found, e := plan.readChildLocked(ctx, snapshots[i], anchor.Height, anchor.BlockHash)
			if e != nil {
				return e
			}
			if !found {
				return fmt.Errorf("dependent coverage disappeared during proof")
			}
			weight := recipeLineageUnitWeight(unit)
			if weight > recipeMaxDecodedBytes-totalBytes {
				return errRecipeDependencyBudget
			}
			totalBytes += weight
		}
		return nil
	})
	if err != nil {
		return empty, err
	}
	return page, nil
}
