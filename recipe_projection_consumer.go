package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

type recipeProjectionRunConsumer struct {
	mu          sync.Mutex
	a           *app
	plan        recipeAdmissionPlan
	binding     recipeRunBinding
	generation  *recipeProjectionGeneration
	initialized bool
}

func recipeProjectionStateReadError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("%w: %v", ErrAddressStateCorrupt, err)
}

func (a *app) recipeCheckpointConsumerGuard(ctx context.Context, plan recipeAdmissionPlan, binding recipeRunBinding) recipeStoreGuard {
	work := recipeConsumerWork{PlanID: plan.PlanID, Height: plan.TargetHeight, ChainSnapshot: recipeLocalSnapshotToken(plan.TargetHeight, plan.TargetHash)}
	anchor := blockView{Height: plan.TargetHeight, Hash: plan.TargetHash}
	return func(publish func() error) error {
		return a.recipeLocalConsumerGuard(ctx, binding)(plan, work, anchor, publish)
	}
}

func (a *app) openRecipeProjectionRunConsumer(ctx context.Context, plan recipeAdmissionPlan, binding recipeRunBinding, reviewed ...string) (*recipeProjectionRunConsumer, error) {
	if plan.Index != binding.Index || plan.Revision != binding.Revision || plan.SemanticHash != binding.Fingerprint {
		return nil, fmt.Errorf("projection binding differs from admitted plan")
	}
	if err := a.CheckAdmittedRecipeBinding(ctx, plan); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	var owned recipeAdmissionPlan
	if err = json.Unmarshal(raw, &owned); err != nil {
		return nil, err
	}
	resolved, err := a.ResolveSavedRecipe(ctx, plan.Index, plan.Revision, plan.SemanticHash)
	if err != nil {
		return nil, err
	}
	program, err := compileRecipe(resolved.Definition)
	if err != nil {
		return nil, err
	}
	g, err := openRecipeProjectionGeneration(a.dataDir, program)
	if err != nil {
		return nil, err
	}
	selection, _ := runSelection(plan.Request, plan.Index)
	if selection.Rebuild {
		if plan.NoWork || len(reviewed) != 1 {
			return nil, fmt.Errorf("projection rebuild requires a reviewed generation and an admitted snapshot")
		}
		if err = g.resetForRebuild(ctx, binding.RunID+":"+binding.Index, reviewed[0], a.recipeCheckpointConsumerGuard(ctx, owned, binding)); err != nil {
			return nil, err
		}
	}
	recipeProjectionMu.Lock()
	_, initialized, err := g.inspect(ctx)
	recipeProjectionMu.Unlock()
	if err != nil {
		return nil, recipeProjectionStateReadError(ctx, err)
	}
	if !initialized && !selection.Rebuild {
		a.indexRunMu.Lock()
		_, progress, err := recipeCheckRunIdentity(a.indexRunState.Run, binding)
		a.indexRunMu.Unlock()
		if err != nil {
			return nil, err
		}
		if progress.Height >= 0 {
			return nil, fmt.Errorf("%w: committed projection generation missing", ErrAddressStateCorrupt)
		}
	}
	return &recipeProjectionRunConsumer{a: a, plan: owned, binding: binding, generation: g, initialized: initialized}, nil
}

func (c *recipeProjectionRunConsumer) Rebind(ctx context.Context, height int64, hash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	plan, err := c.a.recipeRebindTarget(ctx, c.plan, c.binding, height, hash)
	if err == nil {
		c.plan = plan
	}
	return err
}

// Dependency-only evaluation stays out of the raw-body work queue. Like
// ordered recovery, Next can publish authorized state work; it returns a
// complete snapshot only after the source, child CAS and authority are fenced.
func (c *recipeProjectionRunConsumer) Next(ctx context.Context) (recipeConsumerProgress, error) {
	if ctx == nil {
		return recipeConsumerProgress{}, fmt.Errorf("projection work context required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := recipeConsumerProgress{ChainSnapshot: recipeLocalSnapshotToken(c.plan.TargetHeight, c.plan.TargetHash), Evidence: "selected_local_header_as_of_read"}
	if err := checkIndexRunPublication(ctx); err != nil {
		return out, err
	}
	if err := c.a.CheckAdmittedRecipeBinding(ctx, c.plan); err != nil {
		return out, err
	}
	if c.plan.NoWork {
		out.State = "no_work"
		return out, nil
	}
	if _, _, err := c.a.recipeLocalObserve(ctx, c.plan, out.ChainSnapshot, nil); err != nil {
		return out, err
	}
	guard := c.a.recipeCheckpointConsumerGuard(ctx, c.plan, c.binding)
	if err := recipeStoreGuarded(guard, func() error { return ctx.Err() }); err != nil {
		return out, err
	}
	parent, err := c.a.resolveRecipeProjectionParent(ctx, c.generation.identity.Dependency)
	if err != nil {
		return out, err
	}
	if c.plan.TargetHeight < parent.store.head.RecoveryBoundaryHeight {
		return out, fmt.Errorf("%w: the requested parent checkpoint has expired; select an available snapshot or explicitly rebuild its parent", ErrUTXORebuildRequired)
	}
	if err = parent.atHeight(ctx, c.plan.TargetHeight); err != nil {
		return out, err
	}
	if parent.point.Checkpoint.BlockHash != c.plan.TargetHash {
		return out, fmt.Errorf("%w: the declared parent checkpoint is not on the selected target", errRecipeDependencyUnavailable)
	}
	recipeProjectionMu.Lock()
	head, initialized, err := c.generation.inspect(ctx)
	recipeProjectionMu.Unlock()
	if err != nil {
		return out, recipeProjectionStateReadError(ctx, err)
	}
	if !initialized && c.initialized {
		return out, fmt.Errorf("%w: projection ownership disappeared", ErrAddressStateCorrupt)
	}
	reused, err := c.generation.reusePoint(ctx, c.a, parent, head, c.plan.TargetHash, guard)
	if err != nil {
		return out, err
	}
	if !reused {
		if len(head.History) > 0 && head.History[0].Height > c.plan.TargetHeight {
			return out, fmt.Errorf("%w: the requested earlier child snapshot was not retained; review a child rebuild to preserve its current generation", ErrUTXORebuildRequired)
		}
		err = c.generation.applySnapshot(ctx, c.a, c.plan.TargetHeight, c.generation.token(head), func(height int64) (string, error) {
			// Called only inside the publication guard's existing header lock.
			header, err := readHeaderAt(c.a.headersPath, height)
			if err != nil {
				return "", err
			}
			digest := hash256(header)
			return reverseHex(digest[:]), nil
		}, guard, nil)
		if errors.Is(err, ErrAddressStateStale) {
			err = fmt.Errorf("%w: projection recovery unavailable (%v)", ErrUTXORebuildRequired, err)
		}
		if err != nil {
			return out, err
		}
		recipeProjectionMu.Lock()
		head, err = c.generation.head(ctx)
		recipeProjectionMu.Unlock()
		if err != nil {
			return out, recipeProjectionStateReadError(ctx, err)
		}
	}
	c.initialized = true
	through := c.plan.TargetHeight
	out.State, out.StateCommitment, out.CoverageRevision = "range_complete", c.generation.token(head), head.Revision
	out.VerifiedThrough, out.VerifiedHash = &through, c.plan.TargetHash
	return out, nil
}

func (g *recipeProjectionGeneration) reusePoint(ctx context.Context, a *app, parent *recipeProjectionParent, observed recipeProjectionHead, hash string, guard recipeStoreGuard) (bool, error) {
	var candidate recipeProjectionPoint
	found := false
	for _, point := range observed.History {
		if point.Height == parent.point.Checkpoint.Height && point.BlockHash == hash && point.Namespace == parent.point.Namespace {
			candidate, found = point, true
			break
		}
	}
	if !found {
		return false, nil
	}
	recipeProjectionMu.Lock()
	cp, err := g.snapshot(ctx, candidate)
	recipeProjectionMu.Unlock()
	if err != nil {
		return false, recipeProjectionStateReadError(ctx, err)
	}
	if cp.Source.Commitment != parent.point.Checkpoint.Commitment {
		return false, nil
	}
	err = recipeStoreGuarded(guard, func() error {
		recipeProjectionMu.Lock()
		defer recipeProjectionMu.Unlock()
		current, err := g.head(ctx)
		if err != nil {
			return recipeProjectionStateReadError(ctx, err)
		}
		if g.token(current) != g.token(observed) {
			return errRecipeStateConflict
		}
		return parent.checkLocked(ctx, a)
	})
	return err == nil, err
}

func (c *recipeProjectionRunConsumer) Apply(context.Context, recipeConsumerWork, blockView, *inscriptionBlockResult, ...*recipeTransactionSource) error {
	return fmt.Errorf("checkpoint projections consume declared dependency state, not block bodies")
}

func (c *recipeProjectionRunConsumer) Maintain(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation.maintain(ctx)
}
