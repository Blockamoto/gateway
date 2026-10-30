package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func recipeGraphFixtureNode(id string, deps ...recipeDependency) resolvedSavedRecipe {
	hash := recipeTestHash(string(id[0]))
	revision := recipeTestHash("f")
	d := recipeDefinition{ID: id, Source: "bitcoin.reveal", History: "sparse", Tables: []recipeTable{{Name: "items", Key: "ref"}}, Exports: []recipeExport{{Name: "kind", Kind: "same_block", Table: "items", Column: "kind", Type: "text"}}, Dependencies: deps}
	return resolvedSavedRecipe{ID: id, Revision: revision, SemanticHash: hash, Source: d.Source, History: d.History, Definition: d}
}
func recipeGraphFixtureEdge(name string, parent resolvedSavedRecipe) recipeDependency {
	return recipeDependency{Name: name, Instance: parent.ID, Revision: parent.Revision, SemanticHash: parent.SemanticHash, Export: "kind", Coverage: "same_block", Type: "text"}
}
func TestRecipeDependencyGraphTopologicalSharedAndSelection(t *testing.T) {
	leaf := recipeGraphFixtureNode("delta")
	left := recipeGraphFixtureNode("bravo", recipeGraphFixtureEdge("d", leaf))
	right := recipeGraphFixtureNode("charlie", recipeGraphFixtureEdge("d", leaf))
	root := recipeGraphFixtureNode("alpha", recipeGraphFixtureEdge("b", left), recipeGraphFixtureEdge("c", right))
	nodes := map[string]resolvedSavedRecipe{leaf.ID: leaf, left.ID: left, right.ID: right}
	resolver := func(_ context.Context, id, revision, semantic string) (resolvedSavedRecipe, error) {
		n, ok := nodes[id]
		if !ok || n.Revision != revision || n.SemanticHash != semantic {
			return resolvedSavedRecipe{}, fmt.Errorf("not found")
		}
		return n, nil
	}
	other := right
	other.Revision = recipeTestHash("e")
	graph, err := recipeBuildDependencyGraph(context.Background(), root, map[string]resolvedSavedRecipe{left.ID: left, right.ID: other}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 4 || len(graph.Edges) != 4 {
		t.Fatalf("bad graph: %+v", graph)
	}
	positions := map[string]int{}
	for i, n := range graph.Nodes {
		positions[n.Identity.Instance] = i
	}
	for _, e := range graph.Edges {
		if positions[e.Parent.Instance] >= positions[e.Child.Instance] || e.CoverageState != "unknown" {
			t.Fatalf("bad order or inferred coverage: %+v", e)
		}
	}
	if !graph.Edges[0].ParentSelected || graph.Edges[0].SelectedOtherRevision {
		t.Fatal("exact selected parent not identified")
	}
	var conflict bool
	for _, e := range graph.Edges {
		if e.Parent.Instance == right.ID {
			conflict = e.SelectedOtherRevision && !e.ParentSelected
		}
	}
	if !conflict {
		t.Fatal("different selected revision silently substituted")
	}
}
func TestRecipeDependencyGraphFailsClosed(t *testing.T) {
	left := recipeGraphFixtureNode("alpha")
	right := recipeGraphFixtureNode("bravo")
	left.Definition.Dependencies = []recipeDependency{recipeGraphFixtureEdge("b", right)}
	right.Definition.Dependencies = []recipeDependency{recipeGraphFixtureEdge("a", left)}
	nodes := map[string]resolvedSavedRecipe{left.ID: left, right.ID: right}
	resolver := func(_ context.Context, id, _, _ string) (resolvedSavedRecipe, error) {
		n, ok := nodes[id]
		if !ok {
			return resolvedSavedRecipe{}, fmt.Errorf("missing")
		}
		return n, nil
	}
	if _, err := recipeBuildDependencyGraph(context.Background(), left, nil, resolver); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle accepted: %v", err)
	}
	right.Definition.Dependencies = nil
	right.Definition.Exports[0].Type = "bool"
	nodes[right.ID] = right
	if _, err := recipeBuildDependencyGraph(context.Background(), left, nil, resolver); err == nil {
		t.Fatal("wrong export type accepted")
	}
	right.Definition.Exports[0].Type = "text"
	right.SemanticHash = recipeTestHash("e")
	nodes[right.ID] = right
	if _, err := recipeBuildDependencyGraph(context.Background(), left, nil, resolver); err == nil {
		t.Fatal("mismatched immutable identity accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := recipeBuildDependencyGraph(ctx, left, nil, resolver); err != context.Canceled {
		t.Fatalf("cancellation ignored: %v", err)
	}
}
