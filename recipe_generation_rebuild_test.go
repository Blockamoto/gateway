package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestRecipeGenerationRebuildPreservesOrderedWireContract(t *testing.T) {
	ctx := context.Background()
	g := orderedGenerationTestOpen(t, t.TempDir(), "compatible-coins")
	if err := g.create(ctx); err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(g.markerPath())
	if err != nil {
		t.Fatal(err)
	}
	origin, err := os.ReadFile(g.headPath())
	if err != nil {
		t.Fatal(err)
	}
	// Original public review layout, including ordered metadata-file order.
	want := indexDigest(struct {
		Profile  string
		Identity recipeOrderedGenerationIdentity
		Exists   bool
		Files    []string
	}{"gateway-ordered-generation-review-v1", g.identity, true, []string{recipeStoreHash(marker), recipeStoreHash(origin)}})
	got, err := g.reviewToken(ctx)
	if err != nil || got != want {
		t.Fatal("existing ordered review token changed", got, want, err)
	}
	id, err := json.Marshal(g.identity)
	if err != nil {
		t.Fatal(err)
	}
	legacy := []byte(fmt.Sprintf(`{"Schema":1,"Identity":%s,"JobID":"job","Reviewed":"%s","Prior":true,"Complete":false,"Checksum":""}`, id, want))
	encoded, err := json.Marshal(recipeOrderedRebuildMarker{Schema: 1, Identity: g.identity, JobID: "job", Reviewed: want, Prior: true})
	if err != nil || !bytes.Equal(encoded, legacy) {
		t.Fatal("existing ordered rebuild marker encoding changed", string(encoded), err)
	}
}
