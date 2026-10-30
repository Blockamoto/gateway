package main

import (
	"context"
	"fmt"
	"testing"
)

func TestRecipeSelectedDAGOrderIncludesTransitiveEdgesWithoutHiddenWriters(t *testing.T) {
	leaf := recipeGraphFixtureNode("delta")
	middle := recipeGraphFixtureNode("bravo", recipeGraphFixtureEdge("up", leaf))
	root := recipeGraphFixtureNode("alpha", recipeGraphFixtureEdge("mid", middle))
	other := recipeGraphFixtureNode("charlie")
	nodes := map[string]resolvedSavedRecipe{leaf.ID: leaf, middle.ID: middle, root.ID: root, other.ID: other}
	resolver := func(_ context.Context, id, revision, semantic string) (resolvedSavedRecipe, error) {
		n, ok := nodes[id]
		if !ok || n.Revision != revision || n.SemanticHash != semantic {
			return resolvedSavedRecipe{}, fmt.Errorf("missing")
		}
		return n, nil
	}
	// middle is saved-only. Its selected ancestor must nevertheless precede root.
	order, err := recipeOrderSelectedDAG(context.Background(), []resolvedSavedRecipe{root, other, leaf}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 3 || order[0].Instance != "charlie" || order[1].Instance != "delta" || order[2].Instance != "alpha" {
		t.Fatalf("wrong transitive selection order: %+v", order)
	}
	order, err = recipeOrderSelectedDAG(context.Background(), []resolvedSavedRecipe{root, middle, leaf}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 3 || order[0].Instance != "delta" || order[1].Instance != "bravo" || order[2].Instance != "alpha" {
		t.Fatalf("wrong topological order: %+v", order)
	}
	stale := leaf
	stale.Revision = recipeTestHash("e")
	resolverWithOtherRevision := func(ctx context.Context, id, revision, semantic string) (resolvedSavedRecipe, error) {
		if id == stale.ID && revision == stale.Revision && semantic == stale.SemanticHash {
			return stale, nil
		}
		return resolver(ctx, id, revision, semantic)
	}
	order, err = recipeOrderSelectedDAG(context.Background(), []resolvedSavedRecipe{root, stale}, resolverWithOtherRevision)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0].Instance != "delta" {
		t.Fatalf("selected equivalent generation was not ordered first: %+v", order)
	}
	different := stale
	different.SemanticHash = recipeTestHash("f")
	resolverWithDifferentGeneration := func(ctx context.Context, id, revision, semantic string) (resolvedSavedRecipe, error) {
		if id == different.ID && revision == different.Revision && semantic == different.SemanticHash {
			return different, nil
		}
		return resolver(ctx, id, revision, semantic)
	}
	order, err = recipeOrderSelectedDAG(context.Background(), []resolvedSavedRecipe{root, different}, resolverWithDifferentGeneration)
	if err != nil || len(order) != 2 || order[0].Instance != "alpha" {
		t.Fatalf("different semantic generation substituted for saved ancestor: %+v %v", order, err)
	}
}
