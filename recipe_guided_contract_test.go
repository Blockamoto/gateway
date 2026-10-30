package main

import (
	"strings"
	"testing"
)

// This is the exact AST produced by the guided property-trait control. The
// test exercises evaluator behavior, rather than claiming scheduler execution.
func TestRecipeGuidedPropertyTraitOutcomes(t *testing.T) {
	text := `{"format":"gateway-recipe","version":1,"id":"traits","name":"Traits","source":"bitcoin.reveal","origin":0,"history":"sparse","dependencies":[],"tables":[{"name":"reveals","key":"id"}],"steps":[{"op":"require","test":{"call":"and","args":[{"call":"present","args":[{"field":["properties","attributes","traits","rare"]}]},{"call":"eq","args":[{"field":["properties","attributes","traits","rare"]},{"literal":true}]}]}},{"op":"write","table":"reveals","row":{"id":{"field":["ref"]}}}]}`
	d, err := parseRecipe([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name               string
		fields             map[string][]string
		match, unsupported bool
	}{
		{"matching", map[string][]string{"11": {"a101a101a16472617265f5"}}, true, false},
		{"different", map[string][]string{"11": {"a101a101a16472617265f4"}}, false, false},
		{"absent", nil, false, false},
		{"invalid", map[string][]string{"11": {"ff"}}, false, false},
		{"unsupported", map[string][]string{"11": {"a0"}, "13": {"6272"}}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event, err := recipeRevealEvent(inscriptionOccurrence{Envelope: ordEnvelope{Fields: tc.fields}})
			if err != nil {
				t.Fatal(err)
			}
			rows, err := p.evaluate(event)
			if tc.unsupported {
				if err == nil || !strings.Contains(err.Error(), "unsupported") {
					t.Fatalf("unsupported was not an error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (len(rows["reveals"]) == 1) != tc.match {
				t.Fatalf("unexpected rows: %#v", rows)
			}
		})
	}
}
