package main

// The DAG consumer is an internal coordinated adapter. Opening it does not
// authorize scheduling; the scheduler must still admit the exact graph and use
// topological dispatch. Missing saved parent receipts remain unavailable.
import (
	"context"
	"fmt"
	"sync"
)

type recipeDAGObservedHeads struct {
	child   recipeStoreHead
	parents []recipeLineageHead
	token   string
}
type recipeDAGRunConsumer struct {
	mu             sync.Mutex
	a              *app
	local          *recipeLocalConsumer
	lineage        *recipeLineagePlan
	root           resolvedSavedRecipe
	store          *recipeStore
	reconciler     *recipeLocalDAGReconciler
	cursor         int64
	complete       bool
	knownHole      bool
	pending        *recipeConsumerWork
	audited        bool
	auditedThrough int64
	heads          recipeDAGObservedHeads
}

func (a *app) openRecipeDAGRunConsumer(ctx context.Context, admitted recipeAdmissionPlan, binding recipeRunBinding) (*recipeDAGRunConsumer, error) {
	if err := a.CheckAdmittedRecipeBinding(ctx, admitted); err != nil {
		return nil, err
	}
	if admitted.Index != binding.Index || admitted.Revision != binding.Revision || admitted.SemanticHash != binding.Fingerprint {
		return nil, fmt.Errorf("dependent recipe plan differs from admission")
	}
	root, err := a.ResolveSavedRecipe(ctx, admitted.Index, admitted.Revision, admitted.SemanticHash)
	if err != nil {
		return nil, err
	}
	if len(root.Definition.Dependencies) == 0 {
		return nil, fmt.Errorf("dependent recipe has no dependencies")
	}
	lineage, err := a.prepareRecipeLineage(ctx, root)
	if err != nil {
		return nil, err
	}
	program, err := compileRecipe(root.Definition)
	if err != nil {
		return nil, err
	}
	store, err := openRecipeStore(a.dataDir, root.ID, root.SemanticHash)
	if err != nil {
		return nil, err
	}
	owned, err := recipeAdmissionCloneRequest(admitted.Request)
	if err != nil {
		return nil, err
	}
	admitted.Request = owned
	base := &recipeSparseConsumer{a: a, plan: admitted, program: program, store: store, cursor: admitted.EffectiveFrom}
	local := &recipeLocalConsumer{a: a, consumer: base, binding: binding}
	return &recipeDAGRunConsumer{a: a, local: local, lineage: lineage, root: root, store: store, reconciler: &recipeLocalDAGReconciler{a: a, binding: binding, lineage: lineage, store: store}, cursor: admitted.EffectiveFrom}, nil
}
func (c *recipeDAGRunConsumer) plan() recipeAdmissionPlan {
	c.local.consumer.planMu.RLock()
	defer c.local.consumer.planMu.RUnlock()
	return c.local.consumer.plan
}
func (c *recipeDAGRunConsumer) Rebind(ctx context.Context, height int64, hash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	old := c.plan()
	wasComplete := c.complete
	if err := c.local.Rebind(ctx, height, hash); err != nil {
		return err
	}
	if old.TargetHeight == height && old.TargetHash == hash {
		return nil
	}
	c.pending = nil
	c.complete = false
	c.knownHole = false
	if wasComplete && height > old.TargetHeight {
		c.cursor = old.TargetHeight + 1
	}
	if height < old.TargetHeight {
		c.audited = false
		c.cursor = c.plan().EffectiveFrom
		return nil
	}
	if _, _, err := c.a.recipeLocalObserve(ctx, old, "", nil); err != nil {
		c.audited = false
		c.cursor = c.plan().EffectiveFrom
	}
	return nil
}

// Observe heads and optionally prove every changed immutable root has exactly
// the same keys/values in the previously audited range. The caller's current
// local target and all registry revisions are fenced through this comparison.
func (c *recipeDAGRunConsumer) observe(ctx context.Context, plan recipeAdmissionPlan, compare *recipeDAGObservedHeads, through int64) (recipeDAGObservedHeads, bool, error) {
	var result recipeDAGObservedHeads
	equal := compare != nil
	guard := c.reconciler.guard(ctx, plan)
	err := recipeStoreGuarded(guard, func() error {
		recipeStoreMutex.Lock()
		defer recipeStoreMutex.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.store.rejectReconcileFenceLocked(); err != nil {
			return err
		}
		head, err := c.store.head()
		if err != nil {
			return err
		}
		parents, err := c.lineage.headsLocked(ctx)
		if err != nil {
			return err
		}
		result = recipeDAGObservedHeads{child: head, parents: parents, token: recipeLineageHeadsToken(c.lineage.root, parents)}
		if compare == nil {
			return nil
		}
		if len(compare.parents) != len(parents) {
			equal = false
			return nil
		}
		if through < plan.EffectiveFrom {
			return nil
		}
		same, err := recipeStoreRootsEqualRange(ctx, c.store, compare.child.Root, head.Root, plan.EffectiveFrom, through)
		if err != nil {
			return err
		}
		if !same {
			equal = false
			return nil
		}
		for i, parent := range parents {
			old := compare.parents[i]
			if old.Identity != parent.Identity {
				equal = false
				return nil
			}
			node, ok := c.lineage.nodes[parent.Identity]
			if !ok {
				return fmt.Errorf("missing dependent ancestor")
			}
			// When the child root is unchanged and this exact height was
			// already proved uncovered, a parent backfill at that hole cannot
			// invalidate any existing child receipt. Compare both other ranges.
			ranges := [][2]int64{{plan.EffectiveFrom, through}}
			if c.knownHole && !c.complete && c.cursor >= plan.EffectiveFrom && c.cursor <= through {
				ranges = nil
				if c.cursor > plan.EffectiveFrom {
					ranges = append(ranges, [2]int64{plan.EffectiveFrom, c.cursor - 1})
				}
				if c.cursor < through {
					ranges = append(ranges, [2]int64{c.cursor + 1, through})
				}
			}
			for _, span := range ranges {
				same, err = recipeStoreRootsEqualRange(ctx, node.store, old.Root, parent.Root, span[0], span[1])
				if err != nil {
					return err
				}
				if !same {
					equal = false
					return nil
				}
			}
		}
		return nil
	})
	return result, equal, err
}
func (c *recipeDAGRunConsumer) Next(ctx context.Context) (recipeConsumerProgress, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = nil
	plan := c.plan()
	if ctx == nil {
		return recipeConsumerProgress{}, fmt.Errorf("missing dependent context")
	}
	_, pendingFence, err := c.store.inspectReconcileFence()
	if err != nil {
		return recipeConsumerProgress{}, err
	}
	if plan.NoWork {
		if pendingFence {
			return recipeConsumerProgress{}, fmt.Errorf("dependent reconciliation requires its fenced range")
		}
		if _, _, err = c.a.recipeLocalObserve(ctx, plan, "", nil); err != nil {
			return recipeConsumerProgress{}, err
		}
		return recipeConsumerProgress{State: "no_work", Evidence: "none_no_work"}, nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		if pendingFence {
			c.audited = false
			c.cursor = plan.EffectiveFrom
			c.complete = false
			c.knownHole = false
			if _, _, err = c.reconciler.auditFrom(ctx, plan, plan.EffectiveFrom); err != nil {
				return recipeConsumerProgress{}, err
			}
			pendingFence = false
		}
		var old *recipeDAGObservedHeads
		if c.audited {
			old = &c.heads
		}
		observed, same, e := c.observe(ctx, plan, old, c.auditedThrough)
		if e != nil {
			return recipeConsumerProgress{}, e
		}
		if c.audited && !same {
			c.audited = false
			c.cursor = plan.EffectiveFrom
			c.complete = false
			c.knownHole = false
		}
		from := plan.EffectiveFrom
		if c.audited && c.auditedThrough >= from {
			from = c.auditedThrough + 1
		}
		if !c.audited || from <= plan.TargetHeight {
			if from > plan.TargetHeight {
				from = plan.EffectiveFrom
			}
			rev, token, e := c.reconciler.auditFromExpected(ctx, plan, from, &observed.child.Revision, observed.token)
			if e != nil {
				return recipeConsumerProgress{}, e
			}
			observed, _, e = c.observe(ctx, plan, nil, 0)
			if e != nil {
				return recipeConsumerProgress{}, e
			}
			if observed.child.Revision != rev || observed.token != token {
				c.audited = false
				continue
			}
			c.audited = true
			c.auditedThrough = plan.TargetHeight
		}
		c.heads = observed
		result, e := c.nextCovered(ctx, plan, observed)
		if e != nil {
			return recipeConsumerProgress{}, e
		}
		return result, nil
	}
	return recipeConsumerProgress{}, fmt.Errorf("dependent coverage changed repeatedly during planning")
}
func (c *recipeDAGRunConsumer) nextCovered(ctx context.Context, plan recipeAdmissionPlan, heads recipeDAGObservedHeads) (recipeConsumerProgress, error) {
	out := recipeConsumerProgress{CoverageRevision: heads.child.Revision, ChainSnapshot: recipeLocalSnapshotToken(plan.TargetHeight, plan.TargetHash), Evidence: "selected_local_headers_and_transitive_receipts_as_of_snapshot"}
	if c.complete {
		out.State = "range_complete"
		h := plan.TargetHeight
		out.VerifiedThrough = &h
		out.VerifiedHash = plan.TargetHash
		return out, nil
	}
	candidate := c.cursor
	var after *int64
	for {
		guard := c.a.recipeLocalDAGCoverageGuard(ctx, c.local.binding, plan, c.lineage)
		page, err := c.a.recipeDAGCoverage(ctx, c.root, c.store, c.cursor, plan.TargetHeight, after, heads.child.Revision, recipeDAGCoverageMaxPage, heads.token, guard)
		if err != nil {
			return recipeConsumerProgress{}, err
		}
		if page.Revision != heads.child.Revision {
			return recipeConsumerProgress{}, fmt.Errorf("dependent coverage changed during work planning")
		}
		for _, anchor := range page.Anchors {
			if anchor.Height > candidate {
				break
			}
			if anchor.Height != candidate {
				return recipeConsumerProgress{}, fmt.Errorf("invalid dependent coverage ordering")
			}
			if candidate == plan.TargetHeight {
				c.complete = true
				out.State = "range_complete"
				h := candidate
				out.VerifiedThrough = &h
				out.VerifiedHash = plan.TargetHash
				return out, nil
			}
			candidate++
		}
		if len(page.Anchors) > 0 && page.Anchors[len(page.Anchors)-1].Height >= candidate {
			break
		}
		if page.Next == nil {
			break
		}
		after = page.Next
	}
	c.cursor = candidate
	if candidate > plan.EffectiveFrom {
		h := candidate - 1
		_, hash, err := c.a.recipeLocalObserve(ctx, plan, out.ChainSnapshot, &h)
		if err != nil {
			return recipeConsumerProgress{}, err
		}
		out.VerifiedThrough = &h
		out.VerifiedHash = hash
	}
	work := recipeConsumerWork{PlanID: plan.PlanID, Height: candidate, CoverageRevision: heads.child.Revision, ChainSnapshot: out.ChainSnapshot}
	c.pending = &work
	c.knownHole = true
	out.State = "ready"
	copyWork := work
	out.Work = &copyWork
	return out, nil
}
func (c *recipeDAGRunConsumer) Apply(ctx context.Context, work recipeConsumerWork, block blockView, inventory *inscriptionBlockResult, transactionSources ...*recipeTransactionSource) error {
	if len(transactionSources) > 1 {
		return fmt.Errorf("multiple transaction sources")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	plan := c.plan()
	if c.pending == nil || *c.pending != work || work.PlanID != plan.PlanID || work.Height != block.Height {
		return fmt.Errorf("dependent work is stale")
	}
	c.pending = nil
	guard := c.a.recipeLocalDAGExecutionGuard(ctx, c.local.binding, plan, work, block, c.lineage)
	if err := c.a.recipeExecuteDAGBlock(ctx, c.root, c.store, block, inventory, work.CoverageRevision, guard, transactionSources...); err != nil {
		return err
	}
	expected := work.CoverageRevision + 1
	if work.CoverageRevision == 0 {
		expected = 2
	}
	observed, _, err := c.observe(ctx, plan, nil, 0)
	if err != nil || observed.child.Revision != expected || observed.token != c.heads.token {
		c.audited = false
	} else {
		c.heads = observed
	}
	c.knownHole = false
	if work.Height == plan.TargetHeight {
		c.complete = true
	} else {
		c.cursor = work.Height + 1
		c.complete = false
	}
	return nil
}
