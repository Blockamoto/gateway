package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

var errRecipeConsumerReorg = errors.New("recipe saved anchor differs from selected chain; review reconciliation")

type recipeConsumerWork struct {
	StateCommitment  string `json:"state_commitment,omitempty"`
	PlanID           string `json:"plan_id"`
	Height           int64  `json:"height"`
	CoverageRevision uint64 `json:"coverage_revision"`
	ChainSnapshot    string `json:"chain_snapshot"`
}
type recipeConsumerProgress struct {
	StateCommitment  string              `json:"state_commitment,omitempty"`
	State            string              `json:"state"` // ready, range_complete, or no_work
	Work             *recipeConsumerWork `json:"work,omitempty"`
	CoverageRevision uint64              `json:"coverage_revision"`
	ChainSnapshot    string              `json:"chain_snapshot"`
	Evidence         string              `json:"evidence"` // caller-supplied, not independent chain proof
	VerifiedThrough  *int64              `json:"verified_through,omitempty"`
	VerifiedHash     string              `json:"verified_hash,omitempty"`
}

// The guard must synchronously invoke publish while holding the authority for
// this exact admitted plan, chain snapshot/block hash, run/consent state and
// dependency lineage. The consumer cannot itself prove any of those facts.
type recipeConsumerGuard func(plan recipeAdmissionPlan, work recipeConsumerWork, block blockView, publish func() error) error

type recipeSparseConsumer struct {
	mu               sync.Mutex
	planMu           sync.RWMutex // permits snapshot inspection without waiting for a running consumer
	a                *app
	plan             recipeAdmissionPlan
	program          *recipeProgram
	store            *recipeStore
	cursor           int64
	complete         bool
	haveObservation  bool
	observedRevision uint64
	observedChain    string
	pending          *recipeConsumerWork
}

// openRecipeSparseConsumer opens the exact already-admitted revision in its
// own semantic store generation. The caller still owns durable run admission.
func (a *app) openRecipeSparseConsumer(ctx context.Context, admitted recipeAdmissionPlan) (*recipeSparseConsumer, error) {
	if err := a.CheckAdmittedRecipeBinding(ctx, admitted); err != nil {
		return nil, err
	}
	b, err := json.Marshal(admitted)
	if err != nil {
		return nil, err
	}
	var owned recipeAdmissionPlan
	if err = json.Unmarshal(b, &owned); err != nil {
		return nil, err
	}
	resolved, err := a.ResolveSavedRecipe(ctx, owned.Index, owned.Revision, owned.SemanticHash)
	if err != nil {
		return nil, err
	}
	program, err := compileRecipe(resolved.Definition)
	if err != nil {
		return nil, err
	}
	if len(program.definition.Dependencies) != 0 {
		return nil, fmt.Errorf("dependency-aware scheduling and coverage reuse are not integrated")
	}
	if program.semanticHash() != owned.SemanticHash {
		return nil, fmt.Errorf("admitted recipe semantic identity changed")
	}
	store, err := openRecipeStore(a.dataDir, owned.Index, owned.SemanticHash)
	if err != nil {
		return nil, err
	}
	return &recipeSparseConsumer{a: a, plan: owned, program: program, store: store, cursor: owned.EffectiveFrom}, nil
}

// Next examines committed sparse receipts in order and checks every skipped
// anchor through selectedHash; it returns the earliest uncovered height. The
// caller must keep chainSnapshot and selectedHash tied to one stable selected
// chain; changing that token invalidates cached checks. No result is published.
// A changed store revision also invalidates cached checks. The cache belongs
// to this consumer only and is lost on reopen.
func (c *recipeSparseConsumer) Next(ctx context.Context, chainSnapshot string, selectedHash func(int64) (string, error)) (recipeConsumerProgress, error) {
	var out recipeConsumerProgress
	if ctx == nil || chainSnapshot == "" || len(chainSnapshot) > 1024 || selectedHash == nil {
		return out, fmt.Errorf("recipe chain snapshot and evidence are required")
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = nil
	if err := c.a.CheckAdmittedRecipeBinding(ctx, c.plan); err != nil {
		return out, err
	}
	out.ChainSnapshot = chainSnapshot
	out.Evidence = "caller_supplied_anchor_checks"
	if c.plan.NoWork {
		out.State = "no_work"
		out.Evidence = "none_no_work"
		return out, nil
	}
	if !c.haveObservation || c.observedChain != chainSnapshot {
		c.cursor = c.plan.EffectiveFrom
		c.complete = false
		c.haveObservation = false
	}
	// Probe the current revision before trusting a cached prefix. Coverage has
	// bounded page reads and verifies the receipts it returns.
	probe, err := c.store.Coverage(ctx, c.plan.EffectiveFrom, c.plan.TargetHeight, nil, 0, 1)
	if err != nil {
		return recipeConsumerProgress{}, err
	}
	if c.haveObservation && c.observedRevision != probe.Revision {
		c.cursor = c.plan.EffectiveFrom
		c.complete = false
	}
	c.observedRevision = probe.Revision
	c.observedChain = chainSnapshot
	c.haveObservation = true
	out.CoverageRevision = probe.Revision
	if c.complete {
		out.State = "range_complete"
		return out, nil
	}
	candidate := c.cursor
	var after *int64
	for {
		page, err := c.store.Coverage(ctx, c.cursor, c.plan.TargetHeight, after, probe.Revision, recipeCoverageMaxPage)
		if err != nil {
			return recipeConsumerProgress{}, err
		}
		if page.Revision != probe.Revision {
			return recipeConsumerProgress{}, fmt.Errorf("recipe coverage changed during planning")
		}
		for _, anchor := range page.Anchors {
			if err := ctx.Err(); err != nil {
				return recipeConsumerProgress{}, err
			}
			if anchor.Height > candidate {
				break
			}
			if anchor.Height != candidate {
				return recipeConsumerProgress{}, fmt.Errorf("invalid sparse recipe coverage ordering")
			}
			hash, err := selectedHash(anchor.Height)
			if err != nil {
				return recipeConsumerProgress{}, fmt.Errorf("selected-chain evidence unavailable at %d: %w", anchor.Height, err)
			}
			if !recipeStoreHashValid(hash) {
				return recipeConsumerProgress{}, fmt.Errorf("selected-chain evidence invalid at %d", anchor.Height)
			}
			if hash != anchor.BlockHash {
				return recipeConsumerProgress{}, fmt.Errorf("%w at %d", errRecipeConsumerReorg, anchor.Height)
			}
			if candidate == c.plan.TargetHeight {
				c.complete = true
				out.State = "range_complete"
				return out, nil
			}
			candidate++
		}
		// If the next stored height is beyond candidate, candidate is a hole.
		if len(page.Anchors) > 0 && page.Anchors[len(page.Anchors)-1].Height >= candidate {
			break
		}
		if page.Next == nil {
			break
		}
		after = page.Next
	}
	c.cursor = candidate
	work := recipeConsumerWork{PlanID: c.plan.PlanID, Height: candidate, CoverageRevision: probe.Revision, ChainSnapshot: chainSnapshot}
	c.pending = &work
	out.State = "ready"
	copyWork := work
	out.Work = &copyWork
	return out, nil
}

// Apply consumes one pending block and publishes all its rows, including an
// explicit empty result, with atomic coverage revision comparison. The input
// must be the scheduler's immutable verified parsed block and complete shared
// extraction; callers must not mutate either while this operation runs.
func (c *recipeSparseConsumer) Apply(ctx context.Context, work recipeConsumerWork, block blockView, extraction *inscriptionBlockResult, guard recipeConsumerGuard, transactionSources ...*recipeTransactionSource) error {
	if ctx == nil || guard == nil {
		return fmt.Errorf("recipe context and authority guard are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil || *c.pending != work || work.PlanID != c.plan.PlanID || block.Height != work.Height || block.Height < c.plan.EffectiveFrom || block.Height > c.plan.TargetHeight {
		return fmt.Errorf("recipe work is stale or outside admitted range")
	}
	c.pending = nil
	if err := c.a.CheckAdmittedRecipeBinding(ctx, c.plan); err != nil {
		return err
	}
	if !integrityVerified(block) {
		return fmt.Errorf("recipe block is not integrity verified")
	}
	if _, _, err := exactVerifiedBlockPayload(block); err != nil {
		return fmt.Errorf("recipe verified block evidence: %w", err)
	}

	spool, err := newRecipeSparseSpool(ctx, c.store.dir, c.program)
	if err != nil {
		return err
	}
	defer spool.Close()
	if c.program.definition.Source == "bitcoin.transaction" {
		var source *recipeTransactionSource
		if len(transactionSources) > 1 {
			return fmt.Errorf("multiple transaction sources")
		}
		if len(transactionSources) == 1 {
			source = transactionSources[0]
		}
		if source == nil {
			source, err = recipeTransactionSourceFromBlock(ctx, block)
		}
		if err == nil {
			err = c.program.evaluateTransactionSourceInto(ctx, block, source, nil, spool.Add, spool)
		}
	} else {
		err = c.program.evaluateInscriptionSourceInto(ctx, block, extraction, nil, spool.Add, spool)
	}
	if err != nil {
		return err
	}
	unit, err := spool.Seal(recipeStoreUnit{Schema: 1, Fingerprint: c.plan.SemanticHash, Height: block.Height, BlockHash: block.Hash})
	if err != nil {
		return err
	}

	guardPlan := c.plan
	guardPlan.Request, err = recipeAdmissionCloneRequest(c.plan.Request)
	if err != nil {
		return err
	}
	storeGuard := func(publish func() error) error { return guard(guardPlan, work, block, publish) }
	if err = c.store.publishSparseStaged(ctx, unit, work.CoverageRevision, storeGuard, nil); err != nil {
		return err
	}
	c.observedRevision = work.CoverageRevision + 1
	if work.CoverageRevision == 0 {
		c.observedRevision = 2
	} // initialization plus first receipt
	c.observedChain = work.ChainSnapshot
	c.haveObservation = true
	if work.Height == c.plan.TargetHeight {
		c.complete = true
	} else {
		c.cursor = work.Height + 1
		c.complete = false
	}
	return nil
}

// ReorgPlan is read-only and covers exactly one bounded page of the admitted
// window. The caller must hold a consistent chain snapshot across every page;
// affected anchors are never removed implicitly.
func (c *recipeSparseConsumer) ReorgPlan(ctx context.Context, after *int64, expectedRevision uint64, limit int, selectedHash func(int64) (string, error)) (recipeCoveragePage, error) {
	if ctx == nil || selectedHash == nil {
		return recipeCoveragePage{}, fmt.Errorf("recipe context and selected-chain evidence are required")
	}
	if err := ctx.Err(); err != nil {
		return recipeCoveragePage{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.plan.NoWork {
		return recipeCoveragePage{Anchors: []recipeCoverageAnchor{}}, nil
	}
	return c.store.PlanReorg(ctx, c.plan.EffectiveFrom, c.plan.TargetHeight, after, expectedRevision, limit, selectedHash)
}
