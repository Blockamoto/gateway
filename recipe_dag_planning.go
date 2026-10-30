package main

import (
	"context"
	"fmt"
)

const recipePlanMaxDependencies = 64

// Build review diagnostics from semantic edges, never from display hierarchy.
// A selected or saved definition is not evidence that any block is covered.
func (a *app) recipePlanDependencies(ctx context.Context, root resolvedSavedRecipe, req indexRunRequest, from, to int64, resolveSelected func(string) (resolvedSavedRecipe, error)) ([]indexRunDependency, error) {
	if ctx == nil || resolveSelected == nil || from < 0 {
		return nil, fmt.Errorf("invalid recipe dependency planning request")
	}
	selected := map[string]resolvedSavedRecipe{}
	for _, choice := range req.Consumers {
		if !choice.Recipe || !choice.Armed {
			continue
		}
		r, err := resolveSelected(choice.Index)
		if err != nil {
			return nil, err
		}
		if r.ID != choice.Index || !recipeStoreHashValid(r.Revision) || !recipeStoreHashValid(r.SemanticHash) {
			return nil, fmt.Errorf("selected recipe identity unavailable")
		}
		selected[choice.Index] = r
	}
	graph, err := a.buildRecipeDependencyGraph(ctx, root, selected)
	if err != nil {
		return nil, err
	}
	if len(graph.Edges) > recipePlanMaxDependencies {
		return nil, fmt.Errorf("recipe dependency review exceeds the bounded edge limit")
	}
	if from <= to {
		for _, node := range graph.Nodes {
			if node.Identity != graph.Root && node.Origin > from {
				return nil, fmt.Errorf("same-block parent %q begins at %d, after requested start %d", node.Identity.Instance, node.Origin, from)
			}
		}
	}
	dependencies := make([]indexRunDependency, 0, len(graph.Edges))
	for _, edge := range graph.Edges {
		state := "saved_coverage_unknown"
		reason := fmt.Sprintf("%s requires same-block export %s from saved %s revision %s; missing evidence waits and never starts a hidden writer.", edge.Child.Instance, edge.Export, edge.Parent.Instance, edge.Parent.Revision)
		if edge.ParentSelected {
			state = "selected_coverage_unknown"
			reason = fmt.Sprintf("%s requires same-block export %s from the selected exact revision of %s; missing evidence waits.", edge.Child.Instance, edge.Export, edge.Parent.Instance)
		} else if edge.SelectedOtherRevision {
			reason += " The selected instance is a different revision and does not substitute for this parent."
		}
		dependencies = append(dependencies, indexRunDependency{Index: edge.Parent.Instance, Kind: "same_block", State: state, Reason: reason})
	}
	return dependencies, nil
}
