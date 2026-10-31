package main

import (
	"context"
	"fmt"
	"path/filepath"
)

type recipeSparseGenerationIdentity struct{ Instance, Semantic string }

func (s *recipeStore) reviewToken(ctx context.Context) (string, error) {
	return recipeReviewGenerationMetadata(ctx, s.dir, "gateway-sparse-source-generation-review-v1", recipeSparseGenerationIdentity{s.instance, s.fingerprint}, []string{filepath.Join(s.dir, "initialized.json"), filepath.Join(s.dir, "head.json")})
}

// The shared archive protocol makes a resumed job idempotent. The same mutex
// protects sparse publication and archive ownership; a review performs no writes.
func (s *recipeStore) resetForRebuild(ctx context.Context, job, reviewed string, authority recipeStoreGuard) error {
	return recipeGuardedGenerationRebuild(ctx, job, reviewed, authority, func() error {
		recipeStoreMutex.Lock()
		defer recipeStoreMutex.Unlock()
		return recipeResetGenerationForRebuild(ctx, s.dir, recipeSparseGenerationIdentity{s.instance, s.fingerprint}, s.fingerprint, job, reviewed, s.reviewToken)
	})
}

func (a *app) planSourceLookupRecipe(ctx context.Context, resolved resolvedSavedRecipe, choice indexRunSelection, q *indexRunProgress) error {
	s, err := openRecipeStore(a.dataDir, resolved.ID, resolved.SemanticHash)
	if err != nil {
		return err
	}
	recipeStoreMutex.Lock()
	token, err := s.reviewToken(ctx)
	if err == nil && !choice.Rebuild {
		_, err = s.head()
	}
	recipeStoreMutex.Unlock()
	q.RecipeStateToken = token
	if err != nil {
		q.State, q.AdmissionState = "rebuild_required", "blocked"
		q.Reason = "Saved source-evidence output is unavailable; review an explicit rebuild: " + err.Error()
		return ctx.Err()
	}
	if choice.Rebuild {
		if q.From > q.To {
			q.State, q.AdmissionState, q.Reason = "blocked", "blocked", "Source-evidence rebuild requires a nonempty reviewed range."
		} else {
			q.Reason = fmt.Sprintf("Rebuild archives the entire prior output generation, then re-evaluates only the reviewed range from height %d using currently available source evidence. Unavailable evidence remains explicit.", q.From)
		}
	}
	return nil
}
