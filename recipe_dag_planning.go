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
	checkOrigin := from <= to
	if choice, ok := runSelection(req, root.ID); ok && choice.Live && req.Out == nil {
		// Even when the selected tip is currently below In/origin, unbounded
		// Live will eventually attempt the first admitted height.
		checkOrigin = true
	}
	if checkOrigin {
		for _, node := range graph.Nodes {
			if node.Identity != graph.Root && node.Origin > from {
				return nil, fmt.Errorf("same-block parent %q begins at %d, after requested start %d", node.Identity.Instance, node.Origin, from)
			}
		}
	}
	dependencies := make([]indexRunDependency, 0, len(graph.Edges))
	for _, edge := range graph.Edges {
		if edge.Coverage == recipeTableSnapshotCoverage {
			state, source := "saved_checkpoint_unknown", "saved"
			selected := edge.ParentSelected || edge.SelectedSameGeneration
			if edge.Scope == "builtin" {
				source = "built-in"
				choice, ok := runSelection(req, edge.Parent.Instance)
				selected = ok && choice.Armed && !choice.Recipe
			}
			if selected {
				state = "selected_checkpoint_unknown"
			}
			dependencies = append(dependencies, indexRunDependency{Index: edge.Parent.Instance, Kind: recipeTableSnapshotCoverage, State: state, Reason: fmt.Sprintf("%s reads typed table %s at the same-block complete checkpoint of %s %s revision %s. Missing or expired coverage waits; this child starts no parent writer.", edge.Child.Instance, edge.Export, source, edge.Parent.Instance, edge.Parent.Revision)})
			continue
		}
		if edge.Coverage == "snapshot_delta" {
			state, source := "saved_checkpoint_unknown", "saved"
			selected := edge.ParentSelected || edge.SelectedSameGeneration
			if edge.Scope == "builtin" {
				source = "built-in"
				choice, ok := runSelection(req, edge.Parent.Instance)
				selected = ok && choice.Armed && !choice.Recipe
			}
			if selected {
				state = "selected_checkpoint_unknown"
			}
			reason := fmt.Sprintf("%s requires the exact coins checkpoint and checked deltas from %s %s revision %s. The input prefix must already exist or be explicitly selected; this child does not start its parent.", edge.Child.Instance, source, edge.Parent.Instance, edge.Parent.Revision)
			dependencies = append(dependencies, indexRunDependency{Index: edge.Parent.Instance, Kind: "snapshot_delta", State: state, Reason: reason})
			continue
		}
		state := "saved_coverage_unknown"
		reason := fmt.Sprintf("%s requires same-block export %s from saved %s revision %s; missing evidence waits and never starts a hidden writer.", edge.Child.Instance, edge.Export, edge.Parent.Instance, edge.Parent.Revision)
		if edge.ParentSelected {
			state = "selected_coverage_unknown"
			reason = fmt.Sprintf("%s requires same-block export %s from the selected exact revision of %s; missing evidence waits.", edge.Child.Instance, edge.Export, edge.Parent.Instance)
		} else if edge.SelectedSameGeneration {
			state = "selected_coverage_unknown"
			reason = fmt.Sprintf("%s requires same-block export %s from %s revision %s. The selected revision shares its semantic generation; coverage can be reused, while the pinned historical definition remains required. Missing evidence waits.", edge.Child.Instance, edge.Export, edge.Parent.Instance, edge.Parent.Revision)
		} else if edge.SelectedOtherRevision {
			reason += " The selected instance has a different semantic fingerprint and cannot supply this generation."
		}
		dependencies = append(dependencies, indexRunDependency{Index: edge.Parent.Instance, Kind: "same_block", State: state, Reason: reason})
	}
	return dependencies, nil
}
