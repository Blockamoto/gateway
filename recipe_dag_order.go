package main

import (
	"context"
	"fmt"
)

// recipeOrderSelectedDAG orders only explicitly selected definitions. Saved
// ancestors are traversed for dependency reachability but are never silently
// added as writers. Exact revision and semantic identities must match.
func recipeOrderSelectedDAG(ctx context.Context, selected []resolvedSavedRecipe, resolve recipeGraphResolver) ([]recipeGraphIdentity, error) {
	if ctx == nil || resolve == nil || len(selected) > recipeGraphMaxNodes {
		return nil, fmt.Errorf("invalid selected recipe graph")
	}
	selected = append([]resolvedSavedRecipe(nil), selected...)
	byInstance := make(map[string]resolvedSavedRecipe, len(selected))
	positions := make(map[recipeGraphIdentity]int, len(selected))
	cache := make(map[recipeGraphIdentity]resolvedSavedRecipe)
	memoResolve := func(ctx context.Context, id, revision, semantic string) (resolvedSavedRecipe, error) {
		key := recipeGraphIdentity{id, revision, semantic}
		if value, ok := cache[key]; ok {
			return value, nil
		}
		value, err := resolve(ctx, id, revision, semantic)
		if err == nil {
			cache[key] = value
		}
		return value, err
	}
	for i, r := range selected {
		id := recipeGraphID(r)
		if _, ok := byInstance[id.Instance]; ok {
			return nil, fmt.Errorf("duplicate selected recipe instance")
		}
		canonical, err := memoResolve(ctx, id.Instance, id.Revision, id.SemanticHash)
		if err != nil || recipeGraphID(canonical) != id || indexDigest(canonical.Definition) != indexDigest(r.Definition) || canonical.Source != r.Source || canonical.History != r.History || canonical.Origin != r.Origin {
			return nil, fmt.Errorf("selected recipe revision unavailable or changed")
		}
		selected[i] = canonical
		byInstance[id.Instance] = canonical
		positions[id] = i
	}
	edges := make([]map[int]bool, len(selected))
	indegree := make([]int, len(selected))
	for childIndex, root := range selected {
		if root.Source == "bitcoin.transaction" {
			if len(root.Definition.Dependencies) != 0 {
				return nil, fmt.Errorf("transaction recipe dependencies are not supported")
			}
			continue // independent transaction writers are unrelated leaves
		}
		graph, err := recipeBuildDependencyGraph(ctx, root, byInstance, memoResolve)
		if err != nil {
			return nil, err
		}
		for _, node := range graph.Nodes {
			parentIndex, ok := positions[node.Identity]
			if !ok || parentIndex == childIndex {
				continue
			}
			if edges[parentIndex] == nil {
				edges[parentIndex] = map[int]bool{}
			}
			if !edges[parentIndex][childIndex] {
				edges[parentIndex][childIndex] = true
				indegree[childIndex]++
			}
		}
	}
	result := make([]recipeGraphIdentity, 0, len(selected))
	used := make([]bool, len(selected))
	for len(result) < len(selected) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		next := -1
		for i := range selected {
			if !used[i] && indegree[i] == 0 {
				next = i
				break
			}
		}
		if next < 0 {
			return nil, fmt.Errorf("selected recipe dependency cycle detected")
		}
		used[next] = true
		result = append(result, recipeGraphID(selected[next]))
		for child := range edges[next] {
			indegree[child]--
		}
	}
	return result, nil
}
