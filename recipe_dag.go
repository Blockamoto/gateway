package main

// A bounded, read-only graph of exact saved definitions. A saved definition
// does not establish block coverage, and a selected parent does not authorize
// hidden writes outside its run.
import (
	"context"
	"fmt"
)

const recipeGraphMaxNodes = 256
const recipeGraphMaxEdges = 4096
const recipeGraphMaxDepth = 64

type recipeGraphIdentity struct {
	Instance     string `json:"instance"`
	Revision     string `json:"revision"`
	SemanticHash string `json:"semantic_hash"`
}
type recipeGraphNode struct {
	Identity recipeGraphIdentity `json:"identity"`
	Source   string              `json:"source"`
	History  string              `json:"history"`
	Origin   int64               `json:"origin"`
}
type recipeGraphEdge struct {
	Child                  recipeGraphIdentity `json:"child"`
	Parent                 recipeGraphIdentity `json:"parent"`
	Name                   string              `json:"name"`
	Export                 string              `json:"export"`
	Type                   string              `json:"type"`
	Coverage               string              `json:"coverage"`
	ParentSelected         bool                `json:"parent_selected"`
	SelectedOtherRevision  bool                `json:"selected_other_revision"`
	SelectedSameGeneration bool                `json:"selected_same_generation"`
	CoverageState          string              `json:"coverage_state"`
}
type recipeDependencyGraph struct {
	Root  recipeGraphIdentity `json:"root"`
	Nodes []recipeGraphNode   `json:"nodes"` // topological: parents before children
	Edges []recipeGraphEdge   `json:"edges"`
}
type recipeGraphResolver func(context.Context, string, string, string) (resolvedSavedRecipe, error)

func recipeGraphID(r resolvedSavedRecipe) recipeGraphIdentity {
	return recipeGraphIdentity{r.ID, r.Revision, r.SemanticHash}
}

func (a *app) buildRecipeDependencyGraph(ctx context.Context, root resolvedSavedRecipe, selected map[string]resolvedSavedRecipe) (recipeDependencyGraph, error) {
	return recipeBuildDependencyGraph(ctx, root, selected, a.ResolveSavedRecipe)
}
func recipeBuildDependencyGraph(ctx context.Context, root resolvedSavedRecipe, selected map[string]resolvedSavedRecipe, resolve recipeGraphResolver) (recipeDependencyGraph, error) {
	var empty recipeDependencyGraph
	if ctx == nil || resolve == nil {
		return empty, fmt.Errorf("dependency graph requires context and resolver")
	}
	graph := recipeDependencyGraph{Root: recipeGraphID(root), Nodes: []recipeGraphNode{}, Edges: []recipeGraphEdge{}}
	color := map[recipeGraphIdentity]uint8{}
	resolved := map[recipeGraphIdentity]resolvedSavedRecipe{recipeGraphID(root): root}
	var visit func(resolvedSavedRecipe, int) error
	visit = func(node resolvedSavedRecipe, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > recipeGraphMaxDepth {
			return fmt.Errorf("recipe dependency depth exceeds limit")
		}
		id := recipeGraphID(node)
		if !recipeRegistryID.MatchString(id.Instance) || !recipeStoreHashValid(id.Revision) || !recipeStoreHashValid(id.SemanticHash) || node.Definition.ID != node.ID || node.Definition.Source != node.Source || node.Definition.History != node.History || node.Definition.Origin != node.Origin || node.Source != "bitcoin.reveal" || node.History != "sparse" || node.Origin < 0 {
			return fmt.Errorf("invalid saved recipe dependency identity")
		}
		if color[id] == 1 {
			return fmt.Errorf("recipe dependency cycle detected")
		}
		if color[id] == 2 {
			return nil
		}
		if len(color) >= recipeGraphMaxNodes {
			return fmt.Errorf("recipe dependency node limit exceeded")
		}
		if err := recipeValidateDependencyDeclarations(node.Definition); err != nil {
			return err
		}
		color[id] = 1
		for _, dep := range node.Definition.Dependencies {
			if len(graph.Edges) >= recipeGraphMaxEdges {
				return fmt.Errorf("recipe dependency edge limit exceeded")
			}
			wanted := recipeGraphIdentity{dep.Instance, dep.Revision, dep.SemanticHash}
			parent, ok := resolved[wanted]
			if !ok {
				var err error
				parent, err = resolve(ctx, dep.Instance, dep.Revision, dep.SemanticHash)
				if err != nil {
					return fmt.Errorf("resolve dependency %q: %w", dep.Name, err)
				}
				resolved[wanted] = parent
			}
			parentID := recipeGraphID(parent)
			if parentID != (recipeGraphIdentity{dep.Instance, dep.Revision, dep.SemanticHash}) || parent.Source != node.Source || parent.History != "sparse" {
				return fmt.Errorf("dependency %q resolved to an incompatible identity or source", dep.Name)
			}
			found := false
			for _, e := range parent.Definition.Exports {
				if e.Name == dep.Export && e.Kind == dep.Coverage && e.Type == dep.Type {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("dependency %q export contract is unavailable", dep.Name)
			}
			choice, selectedID := selected[parent.ID]
			exact := selectedID && recipeGraphID(choice) == parentID
			graph.Edges = append(graph.Edges, recipeGraphEdge{Child: id, Parent: parentID, Name: dep.Name, Export: dep.Export, Type: dep.Type, Coverage: dep.Coverage, ParentSelected: exact, SelectedOtherRevision: selectedID && !exact, SelectedSameGeneration: selectedID && choice.SemanticHash == parentID.SemanticHash, CoverageState: "unknown"})
			if err := visit(parent, depth+1); err != nil {
				return err
			}
		}
		color[id] = 2
		graph.Nodes = append(graph.Nodes, recipeGraphNode{Identity: id, Source: node.Source, History: node.History, Origin: node.Origin})
		return nil
	}
	if err := visit(root, 0); err != nil {
		return empty, err
	}
	return graph, nil
}
