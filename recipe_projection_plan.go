package main

import (
	"context"
	"errors"
	"fmt"
)

// Pure review of child ownership and the declared input. The parent definition
// graph is reviewed separately; a missing checkpoint never starts its writer.
func (a *app) planProjectionRecipeState(ctx context.Context, resolved resolvedSavedRecipe, choice indexRunSelection, req indexRunRequest, q *indexRunProgress) error {
	program, err := compileRecipe(resolved.Definition)
	if err != nil {
		return err
	}
	g, err := openRecipeProjectionGeneration(a.dataDir, program)
	if err != nil {
		return err
	}
	q.RangeMode, q.RequiresGenesis = "checkpoint_projection", false
	recipeProjectionMu.Lock()
	token, err := g.reviewToken(ctx)
	var head recipeProjectionHead
	if err == nil && !choice.Rebuild {
		head, _, err = g.inspect(ctx)
		if err == nil && len(head.History) > 0 {
			_, err = g.snapshot(ctx, head.History[0])
		}
	}
	recipeProjectionMu.Unlock()
	q.RecipeStateToken = token
	if err != nil {
		q.State, q.AdmissionState = "rebuild_required", "blocked"
		q.Reason = "The child projection is unavailable; review an explicit rebuild preserving its prior files: " + err.Error()
		return ctx.Err()
	}
	if choice.Rebuild {
		q.Reason = "Rebuild preserves only this child generation in an archive and derives the admitted parent snapshot. It does not start or rebuild its parent."
	} else if len(head.History) > 0 {
		latest := head.History[0]
		q.Height, q.BlockHash, q.Commitment = latest.Height, latest.BlockHash, g.token(head)
		if q.From <= q.To && latest.Height > q.To {
			retained := false
			for _, point := range head.History {
				retained = retained || point.Height == q.To
			}
			if !retained {
				q.State, q.AdmissionState = "rebuild_required", "blocked"
				q.Reason = "The requested earlier child snapshot was not retained. Review a child rebuild to preserve the current generation before deriving that snapshot."
				return nil
			}
		}
	}
	next := q.To
	if q.From > q.To {
		next = q.From
		q.NextRequiredHeight = &next
		if choice.Rebuild {
			q.State, q.AdmissionState = "blocked", "blocked"
			q.Reason = "Rebuild needs an admitted snapshot at or above In. Clear the staged rebuild or review an available window."
		}
		return ctx.Err()
	}
	q.NextRequiredHeight = &next
	// Parent rebuild consent belongs to its own explicitly selected writer.
	if selected, ok := runSelection(req, g.identity.Dependency.Instance); ok && selected.Armed && selected.Rebuild {
		if g.identity.Dependency.Scope == "builtin" && !selected.Recipe {
			return ctx.Err()
		}
		if g.identity.Dependency.Scope != "builtin" && selected.Recipe {
			current, e := a.ResolveSavedRecipe(ctx, g.identity.Dependency.Instance, "", "")
			if e == nil && current.SemanticHash == g.identity.Dependency.SemanticHash {
				return ctx.Err()
			}
		}
	}
	parent, err := a.resolveRecipeProjectionParent(ctx, g.identity.Dependency)
	if err != nil {
		if errors.Is(err, errRecipeDependencyUnavailable) {
			return nil // explicitly selected producers may supply it; saved-only inputs wait
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		q.State, q.AdmissionState = "blocked", "blocked"
		q.Reason = "The declared parent checkpoint cannot be read; review its recovery: " + err.Error()
		return nil
	}
	if q.To < parent.store.head.RecoveryBoundaryHeight {
		q.State, q.AdmissionState = "needs_prerequisite", "blocked"
		q.Reason = fmt.Sprintf("The requested parent checkpoint %d is below its retained boundary %d. Select an available snapshot or explicitly rebuild the parent; a child rebuild cannot restore missing source evidence.", q.To, parent.store.head.RecoveryBoundaryHeight)
	}
	return ctx.Err()
}
