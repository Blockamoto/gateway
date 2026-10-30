package main

import (
	"context"
	"errors"
	"os"
	"testing"
)

func recipeTablesTestProgram(t *testing.T, id string, origin int64, empty bool) *recipeProgram {
	t.Helper()
	key := roLit(rovText("total"))
	body := []recipeOrderedInstruction{
		{Op: "read", Table: "counts", Name: "old", Key: key},
		{Op: "let", Name: "count", Expr: roLit(rovUint(0))},
		{Op: "if", Expr: roCall("present", roVar("old")), Body: []recipeOrderedInstruction{{Op: "let", Name: "count", Expr: roVar("old")}}},
		{Op: "put", Table: "counts", Key: key, Expr: roCall("add_money", roVar("count"), roLit(rovUint(1)))},
	}
	if empty {
		body = []recipeOrderedInstruction{}
	}
	program := recipeOrderedProgram{Steps: []recipeOrderedInstruction{{Op: "for_events", Body: []recipeOrderedInstruction{{Op: "if", Expr: roCall("eq", roField("kind"), roLit(rovText("output"))), Body: body}}}}}
	raw, err := recipeOrderedProgramJSON(program)
	if err != nil {
		t.Fatal(err)
	}
	d := recipeDefinition{Format: "gateway-recipe", Version: 1, ID: id, Name: id, Source: "bitcoin.ordered", Origin: origin, History: "ordered", Storage: recipeTablesStorage, Tables: []recipeTable{{Name: "counts", Key: "name"}}, Ordered: raw}
	p, err := compileRecipeTablesContract(d)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func recipeTablesTestCount(t *testing.T, g *recipeTablesGeneration, want uint64) recipeTablesPoint {
	t.Helper()
	ctx := context.Background()
	_, p, err := g.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e := newRecipeTablesEdit(ctx, g.profileRoot, p.Root, g.program.definition.Tables)
	v, err := e.ReadOrdered(ctx, recipeStateKey{Table: "counts", Key: "total"})
	if err != nil || v.Kind != "uint" || v.Uint != want {
		t.Fatal(v, err)
	}
	return p
}

func TestRecipeTablesGenerationOwnedPublicationCASAndRecovery(t *testing.T) {
	ctx := context.Background()
	g, err := openRecipeTablesGeneration(t.TempDir(), recipeTablesTestProgram(t, "table-counts", 0, false))
	if err != nil {
		t.Fatal(err)
	}
	if point, initialized, err := g.prefixSnapshot(ctx); err != nil || initialized || point.Height != -1 {
		t.Fatal(point, initialized, err)
	}
	if _, err = os.Stat(g.profileRoot); !os.IsNotExist(err) {
		t.Fatal("read created table state", err)
	}
	if err = g.create(ctx); err != nil {
		t.Fatal(err)
	}
	initial, _, err := g.prefixSnapshot(ctx)
	if err != nil || initial.Commitment != "" || initial.Token == "" {
		t.Fatal(initial, err)
	}
	blocks := recipeCoordinatorSource(t)
	if err = g.apply(ctx, blocks[0], func(func() error) error { return nil }); err == nil {
		t.Fatal("stage published without authority callback")
	}
	if after, _, _ := g.prefixSnapshot(ctx); after != initial {
		t.Fatal("stage changed visible table state")
	}
	if err = g.applyFromToken(ctx, blocks[0], initial.Token, orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	recipeTablesTestCount(t, g, 1)
	if err = g.applyFromToken(ctx, blocks[1], initial.Token, orderedGenerationTestGuard); !errors.Is(err, errRecipeStateConflict) {
		t.Fatal("stale table head accepted", err)
	}
	if err = g.apply(ctx, blocks[1], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	before := recipeTablesTestCount(t, g, 2)
	if err = g.reconcile(ctx, 1, func(_ context.Context, h int64) (string, error) {
		if h == 1 {
			return testID(731991), nil
		}
		return blocks[0].Hash, nil
	}, orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	after := recipeTablesTestCount(t, g, 1)
	if after.Height != 0 {
		t.Fatal(after)
	}
	if err = g.apply(ctx, blocks[1], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	replayed := recipeTablesTestCount(t, g, 2)
	if replayed != before {
		t.Fatal("replay changed deterministic table point", replayed, before)
	}
	if err = os.Remove(g.path("table-head.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err = g.prefixSnapshot(ctx); !errors.Is(err, ErrRecipeTablesCorrupt) {
		t.Fatal("missing table head became empty state", err)
	}
	if err = g.create(ctx); err == nil {
		t.Fatal("creation repaired a lost committed table head")
	}
}

func TestRecipeTablesGenerationDeclaredOriginAndEvaluatedEmpty(t *testing.T) {
	ctx := context.Background()
	blocks := recipeCoordinatorSource(t)
	g, err := openRecipeTablesGeneration(t.TempDir(), recipeTablesTestProgram(t, "origin-one", 1, false))
	if err != nil {
		t.Fatal(err)
	}
	if err = g.create(ctx); err != nil {
		t.Fatal(err)
	}
	initial, _, err := g.prefixSnapshot(ctx)
	if err != nil || initial.Height != 0 || initial.Commitment != "" {
		t.Fatal(initial, err)
	}
	if err = g.apply(ctx, blocks[0], orderedGenerationTestGuard); err == nil {
		t.Fatal("work before declared table origin accepted")
	}
	if err = g.apply(ctx, blocks[1], orderedGenerationTestGuard); err != nil {
		t.Fatal("origin requires a hidden genesis build", err)
	}
	if p := recipeTablesTestCount(t, g, 1); p.Height != 1 {
		t.Fatal(p)
	}
	points, err := g.prefixRetained(ctx)
	if err != nil || len(points) != 2 || points[1].Height != 0 || points[1].Commitment != "" {
		t.Fatal("declared empty seed recovery", points, err)
	}
	if err = g.reconcile(ctx, 1, func(context.Context, int64) (string, error) { return testID(736455), nil }, orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	if state, _, err := g.prefixSnapshot(ctx); err != nil || state.Height != 0 || state.Commitment != "" {
		t.Fatal("origin fork did not restore its empty seed", state, err)
	}
	if err = g.apply(ctx, blocks[1], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	recipeTablesTestCount(t, g, 1)
	empty, err := openRecipeTablesGeneration(t.TempDir(), recipeTablesTestProgram(t, "empty-tables", 0, true))
	if err != nil {
		t.Fatal(err)
	}
	if err = empty.create(ctx); err != nil {
		t.Fatal(err)
	}
	if err = empty.apply(ctx, blocks[0], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	h, p, err := empty.snapshot(ctx)
	if err != nil || len(h.History) != 1 || p.Height != 0 || p.Commitment == "" || p.Root != "" || p.Entries != 0 || p.Changes != 0 {
		t.Fatal("evaluated empty block is not a committed point", h, p, err)
	}
}
