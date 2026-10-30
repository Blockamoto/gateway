package main

import (
	"context"
	"fmt"
	"strings"
)

// Caller holds the local selected-header lock. Explicit marks are consent to
// specific blocks and must not silently follow a reorg.
func (a *app) recipeLocalMarksLocked(req indexRunRequest) error {
	for _, mark := range []struct {
		height *int64
		hash   string
	}{{req.In, req.InHash}, {req.Out, req.OutHash}} {
		if mark.hash == "" {
			continue
		}
		if mark.height == nil {
			return fmt.Errorf("invalid marked recipe boundary")
		}
		header, err := readHeaderAt(a.headersPath, *mark.height)
		if err != nil {
			return fmt.Errorf("marked recipe boundary unavailable: %w", err)
		}
		digest := hash256(header)
		if !strings.EqualFold(reverseHex(digest[:]), mark.hash) {
			return fmt.Errorf("marked recipe boundary changed; review the global window")
		}
	}
	return nil
}

// Each page is read while one selected endpoint and the selected header file
// are held. The coverage revision prevents mixing store generations.
func (c *recipeLocalConsumer) reconcilePage(ctx context.Context, plan recipeAdmissionPlan, after *int64, revision uint64, journal bool) (recipeCoveragePage, error) {
	if err := c.a.lockSelectedHeaders051(); err != nil {
		return recipeCoveragePage{}, err
	}
	defer c.a.headerChainMu.RUnlock()
	if _, err := c.a.recipeLocalSnapshotLocked(plan); err != nil {
		return recipeCoveragePage{}, err
	}
	if err := c.a.recipeLocalMarksLocked(plan.Request); err != nil {
		return recipeCoveragePage{}, err
	}
	page, err := c.consumer.store.coverage(ctx, plan.EffectiveFrom, plan.TargetHeight, after, revision, recipeCoverageMaxPage, journal)
	if err != nil {
		return recipeCoveragePage{}, err
	}
	affected := make([]recipeCoverageAnchor, 0, len(page.Anchors))
	for _, anchor := range page.Anchors {
		if err = ctx.Err(); err != nil {
			return recipeCoveragePage{}, err
		}
		header, e := readHeaderAt(c.a.headersPath, anchor.Height)
		if e != nil {
			return recipeCoveragePage{}, fmt.Errorf("selected-chain evidence unavailable at %d: %w", anchor.Height, e)
		}
		digest := hash256(header)
		if reverseHex(digest[:]) != anchor.BlockHash {
			affected = append(affected, anchor)
		}
	}
	page.Anchors = affected
	return page, nil
}

// Called with c.mu. A complete read-only audit precedes any mutation. If a
// mismatch exists, a durable fence hides this generation while bounded CAS
// batches remove stale refs. After interruption the next run re-audits the
// current head under the same fence and can resume without a progress counter.
func (c *recipeLocalConsumer) reconcileSelected(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("recipe context required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.consumer.planMu.RLock()
	plan := c.consumer.plan
	c.consumer.planMu.RUnlock()
	fence, pending, err := c.consumer.store.inspectReconcileFence()
	if err != nil {
		return err
	}
	if pending && (plan.NoWork || plan.EffectiveFrom > fence.From || plan.TargetHeight < fence.To) {
		return fmt.Errorf("reconciliation recovery requires the previously fenced range and selected evidence")
	}
	if plan.NoWork {
		return nil
	}
	token, _, err := c.a.recipeLocalObserve(ctx, plan, "", nil)
	if err != nil {
		return err
	}
	guard := func(commit func() error) error {
		work := recipeConsumerWork{PlanID: plan.PlanID, Height: plan.TargetHeight, ChainSnapshot: token}
		return c.a.recipeLocalConsumerGuard(ctx, c.binding)(plan, work, blockView{Height: plan.TargetHeight, Hash: plan.TargetHash}, commit)
	}
	if pending && (plan.EffectiveFrom < fence.From || plan.TargetHeight > fence.To) {
		revision, e := c.consumer.store.reconcileFenceRevision()
		if e != nil {
			return e
		}
		if e = c.consumer.store.beginReconcileFence(ctx, revision, plan.EffectiveFrom, plan.TargetHeight, guard); e != nil {
			return e
		}
	}
	probe, err := c.consumer.store.coverage(ctx, plan.EffectiveFrom, plan.TargetHeight, nil, 0, 1, pending)
	if err != nil {
		return err
	}
	if c.audited && !pending && c.auditSnapshot == token && c.auditRevision == probe.Revision && c.auditThrough >= plan.TargetHeight {
		return nil
	}
	// The selected endpoint commits the already audited prefix. On a pure
	// extension with the same store head, only newly included heights need audit.
	first := plan.EffectiveFrom
	if !pending && c.audited && c.auditSnapshot == token && c.auditRevision == probe.Revision && c.auditThrough >= first && c.auditThrough < plan.TargetHeight {
		first = c.auditThrough + 1
	}
	scanPlan := plan
	scanPlan.EffectiveFrom = first
	var after *int64
	revision := probe.Revision
	mismatch := false
	for {
		page, e := c.reconcilePage(ctx, scanPlan, after, revision, pending)
		if e != nil {
			return e
		}
		if page.Revision != revision {
			return fmt.Errorf("recipe coverage changed during reconciliation audit")
		}
		mismatch = mismatch || len(page.Anchors) > 0
		if page.Next == nil {
			break
		}
		next := *page.Next
		after = &next
	}
	if mismatch || pending {
		// A wider recovery must durably widen the journal before any batch;
		// otherwise a later narrower run could expose unfinished coverage.
		if err = c.consumer.store.beginReconcileFence(ctx, revision, plan.EffectiveFrom, plan.TargetHeight, guard); err != nil {
			return err
		}
		pending = true
	}
	if pending {
		// Always process the full active interval: an earlier partial transaction
		// or a changed snapshot may have left unrelated stale sparse anchors.
		after = nil
		for {
			page, e := c.reconcilePage(ctx, plan, after, revision, true)
			if e != nil {
				return e
			}
			if page.Revision != revision {
				return fmt.Errorf("recipe coverage changed during reconciliation")
			}
			if len(page.Anchors) > 0 {
				batch := make(map[int64]string, len(page.Anchors))
				for _, anchor := range page.Anchors {
					batch[anchor.Height] = anchor.BlockHash
				}
				if e = c.consumer.store.reconcile(ctx, revision, batch, guard, true); e != nil {
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
		if err = c.consumer.store.finishReconcileFence(ctx, revision, plan.EffectiveFrom, plan.TargetHeight, guard); err != nil {
			return err
		}
		c.consumer.mu.Lock()
		c.consumer.pending = nil
		c.consumer.cursor = plan.EffectiveFrom
		c.consumer.complete = false
		c.consumer.haveObservation = false
		c.consumer.mu.Unlock()
	}
	c.audited = true
	c.auditRevision = revision
	c.auditThrough = plan.TargetHeight
	c.auditSnapshot = token
	return nil
}
