package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// Bound aggregate COW/journal memory while independent tracks reuse one owned
// source. This limits concurrent state work, never rows or processed blocks.
var recipeOrderedWorkSlots = make(chan struct{}, 1)

type recipeOrderedRunConsumer struct {
	mu         sync.Mutex
	a          *app
	plan       recipeAdmissionPlan
	binding    recipeRunBinding
	generation recipePrefixBackend
	pending    *recipeConsumerWork
}

func (a *app) openRecipeOrderedRunConsumer(ctx context.Context, plan recipeAdmissionPlan, binding recipeRunBinding, reviewed ...string) (*recipeOrderedRunConsumer, error) {
	if plan.Index != binding.Index || plan.Revision != binding.Revision || plan.SemanticHash != binding.Fingerprint {
		return nil, fmt.Errorf("ordered recipe binding differs from admitted plan")
	}
	if err := a.CheckAdmittedRecipeBinding(ctx, plan); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	var owned recipeAdmissionPlan
	if err = json.Unmarshal(encoded, &owned); err != nil {
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
	if program.ordered == nil || program.semanticHash() != binding.Fingerprint {
		return nil, fmt.Errorf("ordered program identity is unavailable")
	}
	generation, err := openRecipePrefixBackend(a.dataDir, program)
	if err != nil {
		return nil, err
	}
	originHash := func() (string, error) {
		header, err := a.readSelectedHeader(owned.Origin)
		if err != nil {
			return "", err
		}
		digest := hash256(header)
		return reverseHex(digest[:]), nil
	}
	selection, _ := runSelection(owned.Request, owned.Index)
	if selection.Rebuild {
		if owned.EffectiveFrom != owned.Origin || owned.NoWork || len(reviewed) != 1 {
			return nil, fmt.Errorf("ordered rebuild requires a reviewed generation and an explicit range from its origin")
		}
		hash, err := originHash()
		if err != nil {
			return nil, err
		}
		if err = generation.resetForRebuild(ctx, binding.RunID+":"+binding.Index, reviewed[0], a.recipeLocalPublicationGuard(ctx, binding, owned.Origin, hash)); err != nil {
			return nil, err
		}
	}
	_, initialized, err := generation.prefixSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	if !initialized && !owned.NoWork {
		a.indexRunMu.Lock()
		_, progress, bindingErr := recipeCheckRunIdentity(a.indexRunState.Run, binding)
		a.indexRunMu.Unlock()
		if bindingErr != nil {
			return nil, bindingErr
		}
		if progress.Height >= 0 {
			return nil, fmt.Errorf("%w: committed ordered generation is missing", ErrUTXOStateCorrupt)
		}
		if owned.EffectiveFrom > owned.Origin {
			return nil, fmt.Errorf("ordered state needs an explicitly admitted prefix from its declared origin before height %d", owned.EffectiveFrom)
		}
		hash, err := originHash()
		if err != nil {
			return nil, err
		}
		if err = a.recipeLocalPublicationGuard(ctx, binding, owned.Origin, hash)(func() error { return generation.create(ctx) }); err != nil {
			return nil, err
		}
	}
	return &recipeOrderedRunConsumer{a: a, plan: owned, binding: binding, generation: generation}, nil
}

func (c *recipeOrderedRunConsumer) Rebind(ctx context.Context, height int64, hash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	plan, err := c.a.recipeRebindTarget(ctx, c.plan, c.binding, height, hash)
	if err != nil {
		return err
	}
	if plan.PlanID != c.plan.PlanID {
		c.pending = nil
	}
	c.plan = plan
	return nil
}

func (c *recipeOrderedRunConsumer) selectedTip(ctx context.Context) (int64, string, error) {
	if err := ctx.Err(); err != nil {
		return -1, "", err
	}
	if err := c.a.lockSelectedHeaders051(); err != nil {
		return -1, "", err
	}
	defer c.a.headerChainMu.RUnlock()
	if _, err := c.a.recipeLocalSnapshotLocked(c.plan); err != nil {
		return -1, "", err
	}
	if err := c.a.recipeLocalMarksLocked(c.plan.Request); err != nil {
		return -1, "", err
	}
	info, err := os.Stat(c.a.headersPath)
	if err != nil {
		return -1, "", err
	}
	if info.Size() < 80 || info.Size()%80 != 0 {
		return -1, "", fmt.Errorf("selected local header extent is incomplete")
	}
	height := info.Size()/80 - 1
	header, err := readHeaderAt(c.a.headersPath, height)
	if err != nil {
		return -1, "", err
	}
	digest := hash256(header)
	return height, reverseHex(digest[:]), nil
}

func (c *recipeOrderedRunConsumer) selectedAt(ctx context.Context, height, tip int64, tipHash string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if height < 0 || height > tip {
		return "", fmt.Errorf("ordered anchor is outside captured selected prefix")
	}
	if err := c.a.lockSelectedHeaders051(); err != nil {
		return "", err
	}
	defer c.a.headerChainMu.RUnlock()
	if _, err := c.a.recipeLocalSnapshotLocked(c.plan); err != nil {
		return "", err
	}
	if err := c.a.recipeLocalMarksLocked(c.plan.Request); err != nil {
		return "", err
	}
	header, err := readHeaderAt(c.a.headersPath, tip)
	if err != nil {
		return "", err
	}
	digest := hash256(header)
	if reverseHex(digest[:]) != tipHash {
		return "", fmt.Errorf("ordered selected prefix changed")
	}
	header, err = readHeaderAt(c.a.headersPath, height)
	if err != nil {
		return "", err
	}
	digest = hash256(header)
	return reverseHex(digest[:]), nil
}

// Undo may publish intermediate old ancestors. Bind its captured selected tip,
// exact admitted identity and authenticated native undo/CAS, without presenting
// an intermediate disconnected root as a selected query snapshot.
func (c *recipeOrderedRunConsumer) recoveryGuard(ctx context.Context, tip int64, tipHash string) recipeStoreGuard {
	return func(publish func() error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.a.lockSelectedHeaders051(); err != nil {
			return err
		}
		defer c.a.headerChainMu.RUnlock()
		if _, err := c.a.recipeLocalSnapshotLocked(c.plan); err != nil {
			return err
		}
		if err := c.a.recipeLocalMarksLocked(c.plan.Request); err != nil {
			return err
		}
		header, err := readHeaderAt(c.a.headersPath, tip)
		if err != nil {
			return err
		}
		digest := hash256(header)
		if reverseHex(digest[:]) != tipHash {
			return fmt.Errorf("ordered recovery selected prefix changed")
		}
		c.a.indexRunMu.Lock()
		defer c.a.indexRunMu.Unlock()
		if err = c.a.loadIndexRunLocked(); err != nil {
			return err
		}
		run := c.a.indexRunState.Run
		selection, _, err := recipeCheckRunIdentity(run, c.binding)
		if err != nil {
			return err
		}
		if selection.Live && run.Request.Out == nil {
			if run.TargetHeight != c.plan.TargetHeight || run.TargetHash != c.plan.TargetHash {
				return fmt.Errorf("ordered recovery target changed")
			}
		} else if run.InitialTargetHeight != c.plan.TargetHeight {
			return fmt.Errorf("ordered finite recovery target changed")
		}
		recipeRegistryMu.Lock()
		defer recipeRegistryMu.Unlock()
		view, err := recipeRegistryRead(c.a.dataDir, c.binding.Index, c.binding.Revision)
		if err != nil {
			return err
		}
		if !view.Validated || view.SemanticHash != c.binding.Fingerprint || view.Revision != c.binding.Revision {
			return fmt.Errorf("ordered recovery recipe identity changed")
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		return publish()
	}
}

func (c *recipeOrderedRunConsumer) Next(ctx context.Context) (recipeConsumerProgress, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = nil
	if err := c.a.CheckAdmittedRecipeBinding(ctx, c.plan); err != nil {
		return recipeConsumerProgress{}, err
	}
	tip, tipHash, err := c.selectedTip(ctx)
	if err != nil {
		return recipeConsumerProgress{}, err
	}
	state, initialized, err := c.generation.prefixSnapshot(ctx)
	if err != nil {
		return recipeConsumerProgress{}, err
	}
	out := recipeConsumerProgress{ChainSnapshot: recipeLocalSnapshotToken(c.plan.TargetHeight, c.plan.TargetHash), Evidence: "selected_local_header_as_of_read"}
	if !initialized {
		if c.plan.NoWork {
			out.State = "no_work"
			return out, nil
		}
		// A Live run can be admitted before its declared origin exists. Its
		// first nonempty target creates the generation under the same origin,
		// registry and run fences as initial admission, without fetching lead-in.
		c.a.indexRunMu.Lock()
		_, progress, bindingErr := recipeCheckRunIdentity(c.a.indexRunState.Run, c.binding)
		c.a.indexRunMu.Unlock()
		if bindingErr != nil {
			return out, bindingErr
		}
		if progress.Height >= 0 || c.plan.EffectiveFrom > c.plan.Origin {
			return out, fmt.Errorf("%w: committed ordered generation is missing", ErrRecipeTablesCorrupt)
		}
		hash, err := c.selectedAt(ctx, c.plan.Origin, tip, tipHash)
		if err != nil {
			return out, err
		}
		if err = c.a.recipeLocalPublicationGuard(ctx, c.binding, c.plan.Origin, hash)(func() error { return c.generation.create(ctx) }); err != nil {
			return out, err
		}
		state, initialized, err = c.generation.prefixSnapshot(ctx)
		if err != nil || !initialized {
			return out, fmt.Errorf("ordered state is not initialized: %v", err)
		}
	}
	if state.Commitment != "" {
		err = c.generation.reconcile(ctx, tip, func(work context.Context, height int64) (string, error) {
			return c.selectedAt(work, height, tip, tipHash)
		}, c.recoveryGuard(ctx, tip, tipHash))
		if err != nil {
			return out, err
		}
		state, _, err = c.generation.prefixSnapshot(ctx)
		if err != nil {
			return out, err
		}
	}
	height := state.Height
	token := state.Token
	if state.Commitment != "" {
		height = state.Height
		token = state.Token
		hash, err := c.selectedAt(ctx, height, tip, tipHash)
		if err != nil || hash != state.BlockHash {
			return out, fmt.Errorf("ordered checkpoint is not on the captured selected prefix: %v", err)
		}
		through := height
		if through > c.plan.TargetHeight {
			through = c.plan.TargetHeight
		}
		selected, err := c.selectedAt(ctx, through, tip, tipHash)
		if err != nil {
			return out, err
		}
		out.VerifiedThrough, out.VerifiedHash = &through, selected
	}
	out.StateCommitment = token
	out.CoverageRevision = uint64(height + 1)
	if c.plan.NoWork {
		out.State = "no_work"
		return out, nil
	}
	if height >= c.plan.TargetHeight {
		out.State = "range_complete"
		return out, nil
	}
	next := height + 1
	if next < c.plan.EffectiveFrom {
		return out, fmt.Errorf("ordered state needs explicitly admitted lead-in at height %d before this range", next)
	}
	work := recipeConsumerWork{PlanID: c.plan.PlanID, Height: next, CoverageRevision: out.CoverageRevision, ChainSnapshot: out.ChainSnapshot, StateCommitment: token}
	if tables, ok := c.generation.(*recipeTablesGeneration); ok && len(tables.program.definition.Dependencies) > 0 {
		hash, err := c.selectedAt(ctx, next, tip, tipHash)
		if err != nil {
			return out, err
		}
		if _, err = c.a.openRecipeTableDependencies(ctx, tables.program.definition, next, hash); err != nil {
			return out, err
		}
	}
	c.pending = &work
	out.State, out.Work = "ready", &work
	return out, nil
}

func (c *recipeOrderedRunConsumer) Apply(ctx context.Context, work recipeConsumerWork, block blockView, _ *inscriptionBlockResult, _ ...*recipeTransactionSource) error {
	if ctx == nil {
		return fmt.Errorf("ordered work context required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil || *c.pending != work || work.Height != block.Height || work.StateCommitment == "" {
		return fmt.Errorf("ordered work is stale or was not observed")
	}
	if err := checkIndexRunPublication(ctx); err != nil {
		return err
	}
	select {
	case recipeOrderedWorkSlots <- struct{}{}:
		defer func() { <-recipeOrderedWorkSlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := checkIndexRunPublication(ctx); err != nil {
		return err
	}
	var source *recipeOrderedOwnedSource
	if prepared := runPrepared(ctx); prepared != nil && prepared.block.Height == block.Height && prepared.block.Hash == block.Hash {
		if prepared.orderedErr != nil {
			return prepared.orderedErr
		}
		source = prepared.orderedSource
	}
	var err error
	if source == nil {
		source, err = recipeOrderedSourceFromBlock(ctx, block)
		if err != nil {
			return err
		}
	}
	checked := source.block
	if checked.Height != work.Height || checked.Hash != block.Hash {
		return fmt.Errorf("ordered source differs from admitted work")
	}
	header, err := c.a.readSelectedHeader(checked.Height)
	if err != nil {
		return err
	}
	digest := hash256(header)
	if reverseHex(digest[:]) != checked.Hash {
		return fmt.Errorf("ordered source is not the selected local block")
	}
	checked.Verification.HeaderChainMatch = true
	var dependencies *recipeTableDependencySet
	if tables, ok := c.generation.(*recipeTablesGeneration); ok && len(tables.program.definition.Dependencies)+len(tables.program.definition.Lookups) > 0 {
		dependencies, err = c.a.openRecipeTableDependencies(ctx, tables.program.definition, checked.Height, checked.Hash)
		if err != nil {
			return err
		}
		if len(tables.program.definition.Lookups) > 0 {
			dependencies.lookups, err = newRecipeTablePointLookups(c.a, tables.program.definition, source)
			if err != nil {
				return err
			}
		}
		ctx = context.WithValue(ctx, recipeTableDependenciesKey{}, dependencies)
	}
	guard := func(publish func() error) error {
		return c.a.recipeLocalConsumerGuard(ctx, c.binding)(c.plan, work, checked, func() error {
			if dependencies != nil {
				if err := dependencies.checkRegistryLocked(ctx); err != nil {
					return err
				}
			}
			return publish()
		})
	}
	if err = guard(func() error { return nil }); err != nil {
		return err
	}
	if err = c.generation.applyFromToken(ctx, checked, work.StateCommitment, guard); err != nil {
		return err
	}
	c.pending = nil
	return nil
}
