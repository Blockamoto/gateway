package main

import (
	"context"
	"errors"
	"fmt"
)

// Stale means the currently available, complete parent receipt differs from
// the child's recorded lineage. It remains an unavailable dependency error to
// callers that do not explicitly handle stale-child reconciliation.
var errRecipeDAGChildStale = fmt.Errorf("%w: child lineage is stale", errRecipeDependencyUnavailable)

const recipeDAGInspectMaxPage = 8

// Caller holds recipeStoreMutex. This narrow recovery read is permitted only
// for the fenced root; every parent still uses the ordinary fenced read path.
func recipeReadJournalRootLocked(ctx context.Context, store *recipeStore, height int64) (recipeStoreUnit, recipeStoreRef, bool, error) {
	var ref recipeStoreRef
	fence, err := store.readReconcileFenceLocked()
	if err != nil {
		return recipeStoreUnit{}, ref, false, err
	}
	if height < fence.From || height > fence.To {
		return recipeStoreUnit{}, ref, false, fmt.Errorf("dependent inspection exceeds fenced range")
	}
	h, err := store.head()
	if err != nil {
		return recipeStoreUnit{}, ref, false, err
	}
	found, err := store.tree(ctx, h).get(recipeStoreHeightKey(height), &ref)
	if err != nil || !found {
		return recipeStoreUnit{}, ref, false, err
	}
	unit, err := store.readUnit(ref, height)
	return unit, ref, err == nil, err
}

// Inspect one page without mutation or partial results. A different selected
// block is stale immediately. At the same block, stale child lineage is
// identified only when every current parent and its ancestry is complete;
// unavailable parent evidence aborts as unknown. The selected snapshot, saved
// graph identities, run admission and every store ref are held through the
// final check. Journal mode is private to a fenced recovery transaction.
func (a *app) recipeLocalDAGInspectPage(ctx context.Context, binding recipeRunBinding, plan recipeAdmissionPlan, lineage *recipeLineagePlan, store *recipeStore, after *int64, revision uint64, journal bool) (recipeCoveragePage, error) {
	return a.recipeLocalDAGInspectRange(ctx, binding, plan, lineage, store, plan.EffectiveFrom, after, revision, journal)
}
func (a *app) recipeLocalDAGInspectRange(ctx context.Context, binding recipeRunBinding, plan recipeAdmissionPlan, lineage *recipeLineagePlan, store *recipeStore, from int64, after *int64, revision uint64, journal bool) (recipeCoveragePage, error) {
	var empty recipeCoveragePage
	if ctx == nil || lineage == nil || store == nil || plan.NoWork || from < plan.EffectiveFrom || from > plan.TargetHeight || lineage.root != (recipeGraphIdentity{plan.Index, plan.Revision, plan.SemanticHash}) {
		return empty, fmt.Errorf("invalid dependent DAG inspection")
	}
	root, ok := lineage.nodes[lineage.root]
	if !ok || root.store == nil || root.store.dir != store.dir {
		return empty, fmt.Errorf("invalid dependent DAG store")
	}
	page, err := store.coverageMode(ctx, from, plan.TargetHeight, after, revision, recipeDAGInspectMaxPage, journal, true)
	if err != nil {
		return empty, err
	}
	work := recipeConsumerWork{PlanID: plan.PlanID, Height: plan.TargetHeight, ChainSnapshot: recipeLocalSnapshotToken(plan.TargetHeight, plan.TargetHash)}
	target := blockView{Height: plan.TargetHeight, Hash: plan.TargetHash}
	affected := make([]recipeCoverageAnchor, 0, len(page.Anchors))
	guard := a.recipeLocalDAGExecutionGuard(ctx, binding, plan, work, target, lineage)
	err = recipeStoreGuarded(guard, func() error {
		recipeStoreMutex.Lock()
		defer recipeStoreMutex.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		if journal {
			if _, err := store.readReconcileFenceLocked(); err != nil {
				return err
			}
		} else if err := store.rejectReconcileFenceLocked(); err != nil {
			return err
		}
		head, err := store.head()
		if err != nil {
			return err
		}
		if head.Revision != page.Revision {
			return fmt.Errorf("dependent coverage changed during inspection")
		}
		totalBytes, totalRefs := 0, 0
		for _, anchor := range page.Anchors {
			if err = ctx.Err(); err != nil {
				return err
			}
			header, e := readHeaderAt(a.headersPath, anchor.Height)
			if e != nil {
				return e
			}
			digest := hash256(header)
			if reverseHex(digest[:]) != anchor.BlockHash {
				affected = append(affected, anchor)
				continue
			}
			snapshot, e := lineage.captureParentsLocked(ctx, anchor.Height, anchor.BlockHash)
			if e != nil {
				return e
			}
			if len(snapshot.refs) > recipeDAGCoverageMaxRefs-totalRefs || snapshot.decodedBytes > recipeMaxDecodedBytes-totalBytes {
				return errRecipeDependencyBudget
			}
			totalRefs += len(snapshot.refs)
			totalBytes += snapshot.decodedBytes
			unit, found, e := lineage.readChildModeLocked(ctx, snapshot, anchor.Height, anchor.BlockHash, journal)
			if e != nil {
				if errors.Is(e, errRecipeDAGChildStale) {
					affected = append(affected, anchor)
					continue
				}
				return e
			}
			if !found {
				return fmt.Errorf("dependent coverage disappeared during inspection")
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
	page.Anchors = affected
	return page, nil
}
