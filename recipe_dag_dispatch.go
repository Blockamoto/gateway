package main

import (
	"context"
	"fmt"
)

type indexRunRecipeDispatchOrderKey struct{}

// Resolve the exact admitted identities, then derive ordering from declared
// edges. Saved-only ancestors remain read-only and are never added as writers.
func (a *app) recipeRunDispatchOrder(ctx context.Context, run *indexRunRecord) ([]string, error) {
	if run == nil {
		return nil, fmt.Errorf("missing recipe run")
	}
	selected := make([]resolvedSavedRecipe, 0)
	for _, choice := range run.Request.Consumers {
		if !choice.Recipe || !choice.Armed {
			continue
		}
		progress, ok := indexRunProgressFor(run, choice.Index)
		if !ok || progress.AdmissionState == "blocked" || progress.AdmissionState == "off" {
			continue
		}
		if progress.Recipe == nil || progress.RecipeRevision != progress.Recipe.Revision || progress.RecipeSemanticHash != progress.Recipe.SemanticHash {
			return nil, fmt.Errorf("missing admitted recipe identity")
		}
		resolved, err := a.ResolveSavedRecipe(ctx, choice.Index, progress.Recipe.Revision, progress.Recipe.SemanticHash)
		if err != nil {
			return nil, err
		}
		selected = append(selected, resolved)
	}
	order, err := recipeOrderSelectedDAG(ctx, selected, a.ResolveSavedRecipe)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(order))
	for _, id := range order {
		result = append(result, id.Instance)
	}
	return result, nil
}
