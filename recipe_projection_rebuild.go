package main

import "context"

type recipeProjectionRebuildMarker = recipeGenerationRebuildMarker[recipeProjectionIdentity]

func (g *recipeProjectionGeneration) reviewToken(ctx context.Context) (string, error) {
	return recipeReviewGenerationMetadata(ctx, g.profileRoot, "gateway-projection-generation-review-v2", g.identity, []string{
		g.path("recipe-generation.json"), g.path("origin.json"), g.path("published.json"), g.path("projection-head.json"),
	})
}

func (g *recipeProjectionGeneration) resetForRebuild(ctx context.Context, jobID, reviewed string, authority recipeStoreGuard) error {
	return recipeGuardedGenerationRebuild(ctx, jobID, reviewed, authority, func() error {
		recipeProjectionMu.Lock()
		defer recipeProjectionMu.Unlock()
		return recipeResetGenerationForRebuild(ctx, g.profileRoot, g.identity, g.identity.Semantic, jobID, reviewed, g.reviewToken)
	})
}
