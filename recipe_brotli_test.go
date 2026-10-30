package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRecipeOrdBrotliProperties(t *testing.T) {
	// Node's standard Brotli encoder, applied to {1:{0:"Hi",1:{"rare":true}}}.
	fields := map[string][]string{"11": {"0b0780a101a200", "62486901a16472617265f503"}, "13": {"6272"}}
	r, err := recipeOrdDecodeProperties(fields)
	if err != nil || r.Status != recipeOrdPropertiesKnown || r.Properties.Attributes.Title == nil || *r.Properties.Attributes.Title != "Hi" {
		t.Fatalf("compressed properties: %#v %v", r, err)
	}
	if trait, status := recipeOrdTraitLookup(r.Properties.Attributes, "rare"); status != recipeCBORFound || trait.Kind != recipeCBORBool || !trait.Bool {
		t.Fatalf("compressed trait: %#v %v", trait, status)
	}
	// Pinned Ord accepts trailing bytes after the first complete Brotli stream.
	fields["11"] = []string{"0b0780a101a20062486901a16472617265f50300"}
	r, err = recipeOrdDecodeProperties(fields)
	if err != nil || r.Status != recipeOrdPropertiesKnown {
		t.Fatalf("trailing bytes: %#v %v", r, err)
	}
	fields["11"] = []string{"0b0780a101a20062486901a16472617265f5"}
	r, err = recipeOrdDecodeProperties(fields)
	if err != nil || r.Status != recipeOrdPropertiesInvalid {
		t.Fatalf("truncated stream: %#v %v", r, err)
	}
	// 10,000 zero bytes compressed to 13 bytes exceeds Ord's 30:1 limit.
	fields["11"] = []string{"1b0f27f82700e2b14020d90300"}
	r, err = recipeOrdDecodeProperties(fields)
	if err != nil || r.Status != recipeOrdPropertiesInvalid {
		t.Fatalf("expansion limit: %#v %v", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := recipeOrdDecodePropertiesContext(ctx, fields); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	// Waiting for a decoder slot is cancellable and does not leak capacity.
	recipeBrotliSlots <- struct{}{}
	recipeBrotliSlots <- struct{}{}
	ctx, cancel = context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := recipeOrdDecodePropertiesContext(ctx, fields); done <- e }()
	cancel()
	err = <-done
	<-recipeBrotliSlots
	<-recipeBrotliSlots
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("slot cancellation: %v", err)
	}
}

func recipeBeforeBrotliHash(d recipeDefinition) string {
	d.ID, d.Name = "", ""
	return indexDigest(struct {
		Engine          string           `json:"engine"`
		Parser          string           `json:"parser"`
		ParserReference string           `json:"parser_reference"`
		Envelope        string           `json:"envelope"`
		CBOR            string           `json:"cbor"`
		Properties      string           `json:"properties"`
		Lookup          string           `json:"lookup,omitempty"`
		Definition      recipeDefinition `json:"definition"`
	}{recipeInterpreterProfile, inscriptionParserProfile, inscriptionReferenceCommit, ordInterpretationProfile, recipeCBORProfile, recipePropertiesProfile, "", d})
}

func TestRecipeBrotliProfileChangesOnlyPropertyReaders(t *testing.T) {
	base := recipeDefinition{Format: "gateway-recipe", Version: 1, ID: "profile", Name: "Profile", Source: "bitcoin.reveal", History: "sparse", Tables: []recipeTable{{Name: "items", Key: "ref"}}, Steps: []recipeStep{{Op: "write", Table: "items", Row: map[string]recipeExpr{"ref": {Field: []string{"ref"}}}}}}
	p, err := compileRecipe(base)
	if err != nil {
		t.Fatal(err)
	}
	if recipeUsesProperties(p.definition) || p.semanticHash() != recipeBeforeBrotliHash(p.definition) {
		t.Fatal("unrelated recipe identity changed")
	}
	base.Steps[0].Row["status"] = recipeExpr{Field: []string{"properties", "status"}}
	q, err := compileRecipe(base)
	if err != nil {
		t.Fatal(err)
	}
	if !recipeUsesProperties(q.definition) || q.semanticHash() == recipeBeforeBrotliHash(q.definition) {
		t.Fatal("updated property profile was not bound")
	}
	root := t.TempDir()
	raw, err := json.Marshal(q.definition)
	if err != nil {
		t.Fatal(err)
	}
	oldSemantic := recipeBeforeBrotliHash(q.definition)
	oldRevision := recipeRegistryRevisionHash(oldSemantic, raw)
	dir, err := recipeRegistryDir(root, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"revisions", "receipts"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "revisions", oldRevision+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	marker := recipeRegistryMarker{Schema: 1, ID: base.ID}
	marker.Checksum = recipeRegistryMarkerDigest(marker)
	receipt := recipeRegistryReceipt{Schema: 1, ID: base.ID, Revision: oldRevision, SemanticHash: oldSemantic}
	receipt.Checksum = recipeRegistryReceiptDigest(receipt)
	head := recipeRegistryHead{Schema: 1, ID: base.ID, Revision: oldRevision, SemanticHash: oldSemantic}
	head.Checksum = recipeRegistryHeadDigest(head)
	for path, value := range map[string]any{filepath.Join(dir, "initialized.json"): marker, filepath.Join(dir, "receipts", oldRevision+".json"): receipt, filepath.Join(dir, "head.json"): head} {
		data, _ := json.Marshal(value)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	old, err := recipeRegistryRead(root, base.ID, "")
	if err != nil || old.Validated || old.SemanticHash != oldSemantic {
		t.Fatalf("old property generation was relabelled: %#v %v", old, err)
	}
	newView, canonical, err := recipeRegistryCompile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = recipeRegistrySave(context.Background(), root, newView, canonical, oldSemantic, oldRevision); err != nil {
		t.Fatal(err)
	}
	old, err = recipeRegistryRead(root, base.ID, oldRevision)
	if err != nil || old.Validated || old.SemanticHash != oldSemantic {
		t.Fatalf("old property generation lost: %#v %v", old, err)
	}
}
