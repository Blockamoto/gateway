package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"./internal/updateapply"
)

// Local DAG reconciliation is serialized by its owner. Its durable fence is
// shared with ordinary recipe reads/writes; after interruption a new owner
// must audit the full fenced range again before clearing it.
type recipeLocalDAGReconciler struct {
	a       *app
	binding recipeRunBinding
	lineage *recipeLineagePlan
	store   *recipeStore
}

func (r *recipeLocalDAGReconciler) guard(ctx context.Context, plan recipeAdmissionPlan) recipeStoreGuard {
	work := recipeConsumerWork{PlanID: plan.PlanID, Height: plan.TargetHeight, ChainSnapshot: recipeLocalSnapshotToken(plan.TargetHeight, plan.TargetHash)}
	return r.a.recipeLocalDAGExecutionGuard(ctx, r.binding, plan, work, blockView{Height: plan.TargetHeight, Hash: plan.TargetHash}, r.lineage)
}
func (r *recipeLocalDAGReconciler) tokenLocked(ctx context.Context, expected string) (string, error) {
	token, err := r.lineage.headTokenLocked(ctx)
	if err != nil {
		return "", err
	}
	if expected != "" && expected != token {
		return "", fmt.Errorf("dependent parent coverage changed during reconciliation")
	}
	return token, nil
}
func (r *recipeLocalDAGReconciler) token(ctx context.Context, plan recipeAdmissionPlan, expected string) (string, error) {
	var token string
	err := recipeStoreGuarded(r.guard(ctx, plan), func() error {
		recipeStoreMutex.Lock()
		defer recipeStoreMutex.Unlock()
		var e error
		token, e = r.tokenLocked(ctx, expected)
		return e
	})
	return token, err
}
func (r *recipeLocalDAGReconciler) begin(ctx context.Context, plan recipeAdmissionPlan, revision uint64, token string) error {
	return recipeStoreGuarded(r.guard(ctx, plan), func() error {
		recipeStoreMutex.Lock()
		defer recipeStoreMutex.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := r.tokenLocked(ctx, token); err != nil {
			return err
		}
		head, err := r.store.head()
		if err != nil {
			return err
		}
		if head.Revision != revision {
			return fmt.Errorf("dependent coverage changed since audit")
		}
		existing, err := r.store.readReconcileFenceLocked()
		if err == nil {
			if plan.EffectiveFrom > existing.From || plan.TargetHeight < existing.To {
				return fmt.Errorf("reconciliation recovery requires the previously fenced range")
			}
			if plan.EffectiveFrom == existing.From && plan.TargetHeight == existing.To {
				return nil
			}
			existing.From, existing.To = plan.EffectiveFrom, plan.TargetHeight
			existing.Checksum = recipeReconcileFenceDigest(existing)
			return atomicWriteJSON(r.store.reconcileFencePath(), existing)
		}
		if !os.IsNotExist(err) {
			return err
		}
		if err = updateapply.CheckPath(r.store.reconcileFencePath()); err != nil {
			return err
		}
		if err = os.MkdirAll(r.store.dir, 0700); err != nil {
			return err
		}
		fence := recipeReconcileFence{Schema: 1, Instance: r.store.instance, Fingerprint: r.store.fingerprint, From: plan.EffectiveFrom, To: plan.TargetHeight}
		fence.Checksum = recipeReconcileFenceDigest(fence)
		return atomicWriteJSON(r.store.reconcileFencePath(), fence)
	})
}
func (r *recipeLocalDAGReconciler) batch(ctx context.Context, plan recipeAdmissionPlan, revision uint64, token string, anchors []recipeCoverageAnchor) error {
	if len(anchors) == 0 || len(anchors) > recipeDAGInspectMaxPage {
		return fmt.Errorf("invalid dependent reconciliation batch")
	}
	return recipeStoreGuarded(r.guard(ctx, plan), func() error {
		recipeStoreMutex.Lock()
		defer recipeStoreMutex.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		fence, err := r.store.readReconcileFenceLocked()
		if err != nil {
			return err
		}
		if _, err = r.tokenLocked(ctx, token); err != nil {
			return err
		}
		head, err := r.store.head()
		if err != nil {
			return err
		}
		if head.Revision != revision {
			return fmt.Errorf("dependent coverage changed since inspection")
		}
		tree := r.store.tree(ctx, head)
		totalBytes, totalRefs := 0, 0
		for _, anchor := range anchors {
			if err = ctx.Err(); err != nil {
				return err
			}
			if anchor.Height < fence.From || anchor.Height > fence.To {
				return fmt.Errorf("dependent reconciliation exceeds fenced range")
			}
			key := recipeStoreHeightKey(anchor.Height)
			var ref recipeStoreRef
			found, e := tree.get(key, &ref)
			if e != nil {
				return e
			}
			if !found || ref.BlockHash != anchor.BlockHash {
				return fmt.Errorf("dependent reconciliation anchor changed")
			}
			if _, e = r.store.readUnit(ref, anchor.Height); e != nil {
				return e
			}
			header, e := readHeaderAt(r.a.headersPath, anchor.Height)
			if e != nil {
				return e
			}
			digest := hash256(header)
			if reverseHex(digest[:]) == anchor.BlockHash {
				snapshot, e := r.lineage.captureParentsLocked(ctx, anchor.Height, anchor.BlockHash)
				if e != nil {
					return e
				}
				if len(snapshot.refs) > recipeDAGCoverageMaxRefs-totalRefs || snapshot.decodedBytes > recipeMaxDecodedBytes-totalBytes {
					return errRecipeDependencyBudget
				}
				totalRefs += len(snapshot.refs)
				totalBytes += snapshot.decodedBytes
				unit, present, e := r.lineage.readChildModeLocked(ctx, snapshot, anchor.Height, anchor.BlockHash, true)
				if e == nil || !errors.Is(e, errRecipeDAGChildStale) {
					if e != nil {
						return e
					}
					if !present {
						return fmt.Errorf("dependent unit disappeared")
					}
					_ = unit
					return fmt.Errorf("dependent unit is no longer stale")
				}
			}
			tree.remove(key)
		}
		root, err := tree.flush()
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		return r.store.publishHead(head, root)
	})
}
func (r *recipeLocalDAGReconciler) finish(ctx context.Context, plan recipeAdmissionPlan, revision uint64, token string) error {
	return recipeStoreGuarded(r.guard(ctx, plan), func() error {
		recipeStoreMutex.Lock()
		defer recipeStoreMutex.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		fence, err := r.store.readReconcileFenceLocked()
		if err != nil {
			return err
		}
		if plan.EffectiveFrom > fence.From || plan.TargetHeight < fence.To {
			return fmt.Errorf("reconciliation recovery requires the previously fenced range")
		}
		if _, err = r.tokenLocked(ctx, token); err != nil {
			return err
		}
		head, err := r.store.head()
		if err != nil {
			return err
		}
		if head.Revision != revision {
			return fmt.Errorf("dependent coverage changed during reconciliation")
		}
		if err = updateapply.CheckPath(r.store.reconcileFencePath()); err != nil {
			return err
		}
		if err = os.Remove(r.store.reconcileFencePath()); err != nil {
			return err
		}
		return syncDirectory(r.store.dir)
	})
}

// Audit all pages before mutation. If the dependency vector or selected
// target changes, leave any durable fence in place and retry from the start.
func (r *recipeLocalDAGReconciler) reconcile(ctx context.Context, plan recipeAdmissionPlan) error {
	if r == nil || r.a == nil || r.lineage == nil || r.store == nil || ctx == nil || plan.NoWork || r.lineage.root != (recipeGraphIdentity{plan.Index, plan.Revision, plan.SemanticHash}) {
		return fmt.Errorf("invalid dependent reconciliation")
	}
	root, ok := r.lineage.nodes[r.lineage.root]
	if !ok || root.store == nil || root.store.dir != r.store.dir {
		return fmt.Errorf("invalid dependent reconciliation store")
	}
	fence, pending, err := r.store.inspectReconcileFence()
	if err != nil {
		return err
	}
	if pending && (plan.EffectiveFrom > fence.From || plan.TargetHeight < fence.To) {
		return fmt.Errorf("reconciliation recovery requires the previously fenced range")
	}
	token, err := r.token(ctx, plan, "")
	if err != nil {
		return err
	}
	if pending && (plan.EffectiveFrom < fence.From || plan.TargetHeight > fence.To) {
		rev, e := r.store.reconcileFenceRevision()
		if e != nil {
			return e
		}
		if e = r.begin(ctx, plan, rev, token); e != nil {
			return e
		}
	}
	var after *int64
	var revision uint64
	mismatch := false
	for {
		page, e := r.a.recipeLocalDAGInspectPage(ctx, r.binding, plan, r.lineage, r.store, after, revision, pending)
		if e != nil {
			return e
		}
		if after == nil {
			revision = page.Revision
		} else if page.Revision != revision {
			return fmt.Errorf("dependent coverage changed during audit")
		}
		if _, e = r.token(ctx, plan, token); e != nil {
			return e
		}
		mismatch = mismatch || len(page.Anchors) > 0
		if page.Next == nil {
			break
		}
		next := *page.Next
		after = &next
	}
	if mismatch || pending {
		if err = r.begin(ctx, plan, revision, token); err != nil {
			return err
		}
		pending = true
	}
	if !pending {
		return nil
	}
	after = nil
	for {
		page, e := r.a.recipeLocalDAGInspectPage(ctx, r.binding, plan, r.lineage, r.store, after, revision, true)
		if e != nil {
			return e
		}
		if page.Revision != revision {
			return fmt.Errorf("dependent coverage changed during reconciliation")
		}
		if len(page.Anchors) > 0 {
			if e = r.batch(ctx, plan, revision, token, page.Anchors); e != nil {
				return e
			}
			revision++
		}
		if page.Next == nil {
			break
		}
		next := *page.Next
		after = &next
	}
	return r.finish(ctx, plan, revision, token)
}
