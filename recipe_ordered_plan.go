package main

import (
	"context"
	"fmt"
)

// Pure planning: report a missing prefix or damaged generation without
// creating state, hiding a backfill, reconciling a fork or starting a writer.
func (a *app) planOrderedRecipeState(ctx context.Context, resolved resolvedSavedRecipe, choice indexRunSelection, q *indexRunProgress) error {
	program, err := compileRecipe(resolved.Definition)
	if err != nil {
		return err
	}
	g, err := openRecipePrefixBackend(a.dataDir, program)
	if err != nil {
		return err
	}
	q.RangeMode, q.RequiresGenesis = "complete_prefix", true
	recipeOrderedGenerationMu.Lock()
	token, err := g.reviewToken(ctx)
	s := recipePrefixPoint{Height: -1}
	if err == nil && !choice.Rebuild {
		s, _, err = g.prefixSnapshot(ctx)
	}
	recipeOrderedGenerationMu.Unlock()
	q.RecipeStateToken = token
	if err != nil {
		q.State, q.AdmissionState = "rebuild_required", "blocked"
		q.Reason = "Saved ordered generation is unavailable; review an explicit rebuild: " + err.Error()
		return ctx.Err()
	}
	next := int64(0)
	if choice.Rebuild {
		q.Reason = "Rebuild preserves the prior generation and its facts in an archive; the new state begins at genesis."
	} else if s.Height >= 0 {
		cp := s
		q.Height, q.BlockHash, q.Commitment = cp.Height, cp.BlockHash, cp.Commitment
		next = cp.Height + 1
		header, err := a.readSelectedHeader(cp.Height)
		selected := ""
		if err == nil {
			digest := hash256(header)
			selected = reverseHex(digest[:])
		}
		if selected != cp.BlockHash {
			retained, err := g.prefixRetained(ctx)
			if err != nil {
				q.State, q.AdmissionState, q.Reason = "rebuild_required", "blocked", err.Error()
				return ctx.Err()
			}
			ancestor := int64(-1)
			for _, candidate := range retained {
				header, err := a.readSelectedHeader(candidate.Height)
				if err != nil {
					continue
				}
				digest := hash256(header)
				if reverseHex(digest[:]) == candidate.BlockHash {
					ancestor = candidate.Height
					break
				}
			}
			if ancestor < 0 {
				q.State, q.AdmissionState = "rebuild_required", "blocked"
				q.Reason = "The selected chain has no saved ancestor in the retained recovery window; review an explicit rebuild."
				return nil
			}
			next = ancestor + 1
			q.Reason = "The admitted run will undo disconnected transitions to the saved selected ancestor before appending."
		}
	}
	q.NextRequiredHeight = &next
	if next < q.From {
		q.State, q.AdmissionState, q.RequiresLeadIn = "needs_prerequisite", "blocked", true
		q.Reason = fmt.Sprintf("Ordered state needs the explicitly selected prefix from height %d before In %d. Review that earlier range; no hidden backfill will start.", next, q.From)
	}
	return nil
}
