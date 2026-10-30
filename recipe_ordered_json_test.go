package main

import (
	"context"
	"strings"
	"testing"
)

func TestRecipeOrderedJSONStrictStructureAndTypedLiterals(t *testing.T) {
	valid := `{"steps":[{"op":"for_events","body":[{"op":"require","expr":{"literal":{"kind":"bool","bool":true}}}]}]}`
	if _, err := parseOrderedRecipeProgram([]byte(valid), true); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		strings.Replace(valid, `"op"`, `"Op"`, 1),
		strings.Replace(valid, `"op":"for_events"`, `"op":"for_events","op":"for_events"`, 1),
		strings.Replace(valid, `"steps"`, `"Steps"`, 1),
		strings.Replace(valid, `"kind":"bool","bool":true`, `"kind":"uint","uint":"01"`, 1),
		strings.Replace(valid, `"kind":"bool","bool":true`, `"kind":"uint","uint":9007199254740993`, 1),
		strings.Replace(valid, `"kind":"bool","bool":true`, `"Kind":"bool","bool":true`, 1),
		strings.Replace(valid, `"kind":"bool","bool":true`, `"kind":"bool","bool":true,"text":""`, 1),
	} {
		if _, err := parseOrderedRecipeProgram([]byte(raw), true); err == nil {
			t.Fatalf("accepted ambiguous or invalid ordered JSON: %s", raw)
		}
	}
	if _, err := parseOrderedRecipeProgram(builtinUTXOProgramJSON, false); err == nil {
		t.Fatal("resumable stage silently accepted unavailable output sink")
	}
}

func TestRecipeOrderedStreamHasNoFixedBlockEventQuota(t *testing.T) {
	// Pure executor vector, not a Bitcoin integrity/selected-chain fixture.
	ctx := context.Background()
	p, err := parseOrderedRecipeProgram([]byte(`{"steps":[{"op":"for_events","body":[{"op":"require","expr":{"literal":{"kind":"bool","bool":true}}}]}]}`), true)
	if err != nil {
		t.Fatal(err)
	}
	backend := &recipeStateMemoryTest{version: recipeStateVersion{Checkpoint: "origin"}, values: map[recipeStateKey]recipeStateValue{}}
	tx, err := recipeStateBegin(backend, backend.version, "next", recipeStateTestLimits())
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	source := func(emit func(map[string]recipeOrderedValue) error) error {
		for i := 0; i < 80000; i++ {
			if err := emit(map[string]recipeOrderedValue{}); err != nil {
				return err
			}
			count++
		}
		return nil
	}
	if err = recipeOrderedExecuteStream(ctx, p, tx, source, nil); err != nil || count != 80000 {
		t.Fatalf("complete event source rejected: %d %v", count, err)
	}
	if _, err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
