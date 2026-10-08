package main

import "testing"

func TestIndexPlanBoundaries(t *testing.T) {
	start := int64(850000)
	end := int64(850010)
	if _, err := planIndex(indexBuildRequest{Index: "bitmap", From: &start, To: &end}); err == nil {
		t.Fatal("accepted stateless mid-history Bitmap start")
	}
	p, err := planIndex(indexBuildRequest{Index: "inscriptions", From: &start, To: &end})
	if err != nil {
		t.Fatal(err)
	}
	if p.Retention != "ephemeral" || p.Sequential {
		t.Fatal(p)
	}
	p, err = planIndex(indexBuildRequest{Index: "bitmap", To: &end})
	if err != nil {
		t.Fatal(err)
	}
	if p.From != 792435 || len(p.Dependencies) != 3 || p.Dependencies[0].ID != "headers" || p.Dependencies[2].ID != "inscriptions" {
		t.Fatal(p)
	}
	if _, err = planIndex(indexBuildRequest{Index: "bitmap", Retention: "delete-external"}); err == nil {
		t.Fatal("invalid retention accepted")
	}
}
func TestIndexDefinitionsSeparateKnowledge(t *testing.T) {
	ids := map[string]bool{}
	for _, d := range indexDefinitions() {
		if ids[d.ID] || d.RuleHash == "" {
			t.Fatal(d)
		}
		ids[d.ID] = true
	}
	for _, id := range []string{"address-state", "address-history", "sat-state", "satline", "inscriptions"} {
		if !ids[id] {
			t.Fatal(id)
		}
	}
	if ids["inscription-numbering"] {
		t.Fatal("numbering must be integrated into Inscriptions, not a separate top-level index")
	}
	d, _ := findIndexDefinition("inscription-numbering")
	if d.Buildable || d.ArbitraryStart {
		t.Fatal("numbering claimed without historical implementation")
	}
}
func TestIndexCoverageHasRealGaps(t *testing.T) {
	g := indexCoverageGaps([]heightInterval{{10, 20}, {30, 35}}, 5, 40)
	want := []heightInterval{{5, 9}, {21, 29}, {36, 40}}
	if len(g) != len(want) {
		t.Fatal(g)
	}
	for i := range g {
		if g[i] != want[i] {
			t.Fatal(g)
		}
	}
}
