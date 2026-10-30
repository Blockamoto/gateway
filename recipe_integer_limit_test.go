package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecipeIntegerWorkBound(t *testing.T) {
	tooLarge := strings.Repeat("9", recipeMaxIntegerDigits+1)
	if _, _, err := recipeLiteralType(json.RawMessage(tooLarge)); err == nil {
		t.Fatal("oversized integer literal was accepted")
	}
	if _, err := recipeCBORJSONKey(json.Number(tooLarge)); err == nil {
		t.Fatal("oversized CBOR key was accepted")
	}
	e := recipeExpr{Call: "eq", Args: []recipeExpr{{Field: []string{"n"}}, {Literal: json.RawMessage("0")}}}
	if _, err := recipeEvaluateExpr(e, map[string]any{"n": json.Number(tooLarge)}, nil); err == nil {
		t.Fatal("oversized dynamic integer was accepted")
	}
	if _, _, err := recipeLiteralType(json.RawMessage(strings.Repeat("9", recipeMaxIntegerDigits))); err != nil {
		t.Fatal(err)
	}
}
