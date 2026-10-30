package main

import "context"

type recipeOrderedRebuildMarker = recipeGenerationRebuildMarker[recipeOrderedGenerationIdentity]

func recipeOrderedRebuildChecksum(m recipeOrderedRebuildMarker) string {
	return recipeGenerationRebuildChecksum(m)
}

// Only publication metadata is reviewed. Damaged payloads are preserved in
// the archive without interpretation, and a resumed job keeps its replacement.
func (g *recipeOrderedGeneration) reviewToken(ctx context.Context) (string, error) {
	return recipeReviewGenerationMetadata(ctx, g.profileRoot, "gateway-ordered-generation-review-v1", g.identity, []string{g.markerPath(), g.headPath()})
}

// Work slot precedes header/run/registry authority and generation ownership.
func (g *recipeOrderedGeneration) resetForRebuild(ctx context.Context, jobID, reviewed string, authority recipeStoreGuard) error {
	return recipeGuardedGenerationRebuild(ctx, jobID, reviewed, authority, func() error {
		return g.resetForRebuildLocked(ctx, jobID, reviewed)
	})
}

func (g *recipeOrderedGeneration) resetForRebuildLocked(ctx context.Context, jobID, reviewed string) error {
	recipeOrderedGenerationMu.Lock()
	defer recipeOrderedGenerationMu.Unlock()
	return recipeResetGenerationForRebuild(ctx, g.profileRoot, g.identity, g.identity.Semantic, jobID, reviewed, g.reviewToken)
}
