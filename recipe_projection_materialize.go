package main

// The dependency supplies checked immutable coins and net deltas. JSON selects
// their destinations; the same COW editor used by native Current Addresses
// maintains its reverse rows. No block body or hidden parent work is required.
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (g *recipeProjectionGeneration) editor(ctx context.Context, parent *recipeProjectionParent, cp addressStateCheckpoint) *addressStateEditor {
	e := newAddressStateEditor(ctx, g.profileRoot, cp)
	e.projection = g.program
	e.importFact = newRecipeProjectionFactImporter(parent.root, g.profileRoot).copy
	return e
}

// applySnapshot serializes with saved coin writers/GC. Native source reads are
// also pinned against its writers/GC while staging. Release that native lock
// before entering the header/run/registry publication fence; the final parent
// receipt check permits an outside append while rejecting a fork or expiry.
func (g *recipeProjectionGeneration) applySnapshot(ctx context.Context, a *app, height int64, expected string, canonical func(int64) (string, error), guard recipeStoreGuard, progress func(addressStateBuildProgress)) error {
	if ctx == nil || a == nil || height < 0 || canonical == nil || guard == nil {
		return fmt.Errorf("projection work evidence required")
	}
	select {
	case recipeOrderedWorkSlots <- struct{}{}:
		defer func() { <-recipeOrderedWorkSlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	locked := false
	if g.identity.Dependency.Scope == "builtin" {
		if err := lockAddressSourceContext(ctx); err != nil {
			return err
		}
		locked = true
	}
	defer func() {
		if locked {
			utxoWriterMu.Unlock()
		}
	}()
	parent, err := a.resolveRecipeProjectionParent(ctx, g.identity.Dependency)
	if err != nil {
		return err
	}
	if err = parent.atHeight(ctx, height); err != nil {
		return err
	}
	if err = g.create(ctx); err != nil {
		return err
	}
	head, err := g.head(ctx)
	if err != nil {
		return err
	}
	if expected != "" && expected != g.token(head) {
		return errRecipeStateConflict
	}
	points, err := g.materialize(ctx, parent, head, progress)
	if err != nil {
		return err
	}
	if locked {
		utxoWriterMu.Unlock()
		locked = false
	}
	if err = g.publish(ctx, a, g.token(head), parent, points, canonical, guard); err != nil {
		return err
	}
	// A committed receipt owns the roots now. A stale full-traversal manifest
	// must not pin an obsolete build forever during later maintenance.
	if err = os.Remove(filepath.Join(addressStateDirectory(g.profileRoot), "build.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncDirectory(addressStateDirectory(g.profileRoot))
}

func (g *recipeProjectionGeneration) materialize(ctx context.Context, parent *recipeProjectionParent, head recipeProjectionHead, progress func(addressStateBuildProgress)) ([]recipeProjectionPoint, error) {
	if head.Revision == 0 {
		cp, err := g.buildSnapshot(ctx, parent, progress)
		if err != nil {
			return nil, err
		}
		point, err := g.writeSnapshot(ctx, cp, parent.point.Namespace)
		return []recipeProjectionPoint{point}, err
	}
	latest := head.History[0]
	if latest.Namespace != parent.point.Namespace {
		return nil, fmt.Errorf("projection parent namespace changed; reviewed rebuild required")
	}
	cp, err := g.snapshot(ctx, latest)
	if err != nil {
		return nil, err
	}
	if cp.Source.Commitment == parent.point.Checkpoint.Commitment {
		return []recipeProjectionPoint{latest}, nil
	}
	e := g.editor(ctx, parent, cp)
	selected, err := parent.store.retainedCheckpoints(ctx)
	if err != nil {
		return nil, err
	}
	// Only the path ending at the requested retained checkpoint is eligible.
	start := -1
	for i, candidate := range selected {
		if candidate.Commitment == parent.point.Checkpoint.Commitment {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, errRecipeDependencyUnavailable
	}
	selected = selected[start:]
	byCommit := map[string]int{}
	for i, candidate := range selected {
		byCommit[candidate.Commitment] = i
	}
	points := []recipeProjectionPoint{}
	transition := func(before, after utxoCheckpoint) error {
		if err := e.transition(ctx, parent.store, before, after); err != nil {
			return err
		}
		point, err := g.writeSnapshot(ctx, e.cp, parent.point.Namespace)
		if err == nil {
			points = append(points, point)
		}
		return err
	}
	old := cp.Source
	for steps := 0; ; steps++ {
		if target, ok := byCommit[old.Commitment]; ok {
			for i := target - 1; i >= 0; i-- {
				if err = transition(old, selected[i]); err != nil {
					return nil, err
				}
				old = selected[i]
			}
			break
		}
		if steps >= utxoRecoveryTransitions || old.PreviousCommitment == "" {
			return nil, fmt.Errorf("%w: projection needs a reviewed rebuild outside source recovery", ErrAddressStateStale)
		}
		previous, err := parent.store.readCheckpoint(ctx, old.PreviousCommitment)
		if err != nil {
			return nil, fmt.Errorf("%w: projection recovery unavailable (%v)", ErrAddressStateStale, err)
		}
		if err = transition(old, previous); err != nil {
			return nil, err
		}
		old = previous
	}
	if len(points) == 0 || old.Commitment != parent.point.Checkpoint.Commitment {
		return nil, fmt.Errorf("projection did not reach its declared source point")
	}
	return points, ctx.Err()
}

func (g *recipeProjectionGeneration) buildSnapshot(ctx context.Context, parent *recipeProjectionParent, progress func(addressStateBuildProgress)) (addressStateCheckpoint, error) {
	source := parent.point.Checkpoint
	pageGeneration := indexDigest(fmt.Sprintf("%s:%s:%d", g.identity.Semantic, source.Commitment, time.Now().UnixNano()))
	e := g.editor(ctx, parent, addressStateCheckpoint{PageGeneration: pageGeneration, DestinationVersion: 1})
	trees := parent.store.trees(ctx, source)
	reader := newUTXOFactReader(ctx, parent.root)
	checked := uint64(0)
	after, resumed := "", false
	staged, err := readAddressBuildContext(ctx, g.profileRoot)
	if err != nil {
		return addressStateCheckpoint{}, err
	}
	if staged != nil {
		if staged.ProjectionParent == nil || indexDigest(*staged.ProjectionParent) != indexDigest(parent.point) || staged.Source.Commitment != source.Commitment {
			if err = archiveAddressBuildContext(ctx, g.profileRoot); err != nil {
				return addressStateCheckpoint{}, err
			}
		} else {
			e = g.editor(ctx, parent, staged.Projection)
			if _, err = e.summary.read(e.cp.Root); err != nil {
				return addressStateCheckpoint{}, err
			}
			if staged.After != "" {
				var member utxoMember
				found, err := trees["live"].get(staged.After, &member)
				if err != nil || !found {
					return addressStateCheckpoint{}, fmt.Errorf("invalid projection traversal cursor (%v)", err)
				}
			}
			checked, after, resumed = staged.OutputsChecked, staged.After, true
		}
	}
	last := after
	report := func() {
		if progress != nil {
			progress(addressStateBuildProgress{Height: source.Height, BlockHash: source.BlockHash, UTXOCommitment: source.Commitment, OutputsChecked: checked, OutputsTotal: source.LiveOutputs, Included: e.cp.OutputCount, Excluded: e.cp.ExcludedOutputCount, Resumed: resumed})
		}
	}
	save := func() error {
		if err := e.flush(); err != nil {
			return err
		}
		point := parent.point
		return saveAddressBuildContext(ctx, g.profileRoot, addressStateBuildManifest{Source: source, Projection: e.cp, OutputsChecked: checked, After: last, ProjectionParent: &point})
	}
	report()
	err = addressWalkAfter(trees["live"], after, func(kv ordinalKV) error {
		var member utxoMember
		if err := json.Unmarshal(kv.Value, &member); err != nil {
			return err
		}
		coin, err := parent.store.coinUsingReader(ctx, trees, member, reader)
		if err != nil {
			return err
		}
		if kv.Key != utxoPositionKey(coin.Height, coin.TxIndex, int(coin.Vout)) {
			return fmt.Errorf("%w: projection live key differs from its coin", ErrUTXOStateCorrupt)
		}
		if err = e.change(member, true); err != nil {
			return err
		}
		checked++
		last = kv.Key
		if checked%512 == 0 {
			if err = save(); err != nil {
				return err
			}
		}
		if checked%128 == 0 {
			report()
		}
		return ctx.Err()
	})
	if err != nil {
		return addressStateCheckpoint{}, err
	}
	if checked != source.LiveOutputs {
		return addressStateCheckpoint{}, fmt.Errorf("%w: incomplete projection traversal", ErrUTXOStateCorrupt)
	}
	if err = e.flush(); err == nil {
		err = e.bind(source)
	}
	if err == nil {
		err = save()
	}
	if err != nil {
		return addressStateCheckpoint{}, err
	}
	report()
	return e.cp, ctx.Err()
}
