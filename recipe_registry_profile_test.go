package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecipeRegistryPreservesProfileIdentity(t *testing.T) {
	root := t.TempDir()
	current, bytes, err := recipeRegistryCompile([]byte(registryRecipe))
	if err != nil {
		t.Fatal(err)
	}
	oldSemantic := strings.Repeat("a", 64)
	if oldSemantic == current.SemanticHash {
		t.Fatal("invalid test fingerprint")
	}
	oldRevision := recipeRegistryRevisionHash(oldSemantic, bytes)
	if oldRevision == current.Revision {
		t.Fatal("same source with different interpreter had same revision")
	}
	dir, err := recipeRegistryDir(root, current.Recipe.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(dir, "revisions"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(dir, "receipts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "revisions", oldRevision+".json"), bytes, 0600); err != nil {
		t.Fatal(err)
	}
	marker := recipeRegistryMarker{Schema: 1, ID: current.Recipe.ID}
	marker.Checksum = recipeRegistryMarkerDigest(marker)
	receipt := recipeRegistryReceipt{Schema: 1, ID: current.Recipe.ID, Revision: oldRevision, SemanticHash: oldSemantic}
	receipt.Checksum = recipeRegistryReceiptDigest(receipt)
	head := recipeRegistryHead{Schema: 1, ID: current.Recipe.ID, Revision: oldRevision, SemanticHash: oldSemantic}
	head.Checksum = recipeRegistryHeadDigest(head)
	for path, value := range map[string]any{filepath.Join(dir, "initialized.json"): marker, filepath.Join(dir, "receipts", oldRevision+".json"): receipt, filepath.Join(dir, "head.json"): head} {
		raw, _ := json.Marshal(value)
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := recipeRegistryRead(root, current.Recipe.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Validated || got.SemanticHash != oldSemantic || got.Revision != oldRevision || got.RecipeJSON != string(bytes) || !strings.Contains(got.Note, "different interpreter") {
		t.Fatalf("old profile was silently reinterpreted: %+v", got)
	}
	newView, err := recipeRegistrySave(context.Background(), root, current, bytes, oldSemantic, oldRevision)
	if err != nil {
		t.Fatal(err)
	}
	if !newView.Validated || newView.Revision == oldRevision {
		t.Fatal("new profile revision was not distinct")
	}
	prior, err := recipeRegistryRead(root, current.Recipe.ID, oldRevision)
	if err != nil || prior.Validated || prior.SemanticHash != oldSemantic {
		t.Fatalf("old revision not preserved: %+v %v", prior, err)
	}
	if err = os.Remove(filepath.Join(dir, "receipts", oldRevision+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err = recipeRegistryRead(root, current.Recipe.ID, oldRevision); err == nil {
		t.Fatal("missing new-format receipt accepted")
	}
}

func TestRecipeRegistryRejectsLossyUnicode(t *testing.T) {
	for _, raw := range [][]byte{[]byte(`{"name":"\ud800"}`), []byte(`{"name":"\udc00"}`), []byte(`{"name":"\ud800x"}`), append([]byte(`{"name":"`), 0xff, '"', '}')} {
		if err := recipeValidateJSON(raw); err == nil {
			t.Fatalf("accepted lossy string %q", raw)
		}
	}
	if err := recipeValidateJSON([]byte(`{"name":"\ud83d\ude00"}`)); err != nil {
		t.Fatalf("valid surrogate pair: %v", err)
	}
	if err := recipeValidateJSON([]byte(`{"name":"\\ud800"}`)); err != nil {
		t.Fatalf("literal escape text: %v", err)
	}
}

func TestRecipeParserRejectsLossyUnicodeAndDuplicateKeys(t *testing.T) {
	for _, raw := range []string{strings.Replace(registryRecipe, `"Custom"`, `"\ud800"`, 1), strings.Replace(registryRecipe, `"id":"custom-one"`, `"id":"custom-one","id":"other"`, 1)} {
		if _, err := parseRecipe([]byte(raw)); err == nil {
			t.Fatalf("parser accepted lossy JSON: %s", raw)
		}
	}
}

func TestRecipeRegistryBackfillsLegacyHeadIdentity(t *testing.T) {
	root := t.TempDir()
	current, raw, err := recipeRegistryCompile([]byte(registryRecipe))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := recipeRegistryDir(root, current.Recipe.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(dir, "revisions"), 0700); err != nil {
		t.Fatal(err)
	}
	legacyRevision := recipeStoreHash(raw)
	if err = os.WriteFile(filepath.Join(dir, "revisions", legacyRevision+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	marker := recipeRegistryMarker{Schema: 1, ID: current.Recipe.ID}
	marker.Checksum = recipeRegistryMarkerDigest(marker)
	head := recipeRegistryHead{Schema: 1, ID: current.Recipe.ID, Revision: legacyRevision, SemanticHash: current.SemanticHash}
	head.Checksum = recipeRegistryHeadDigest(head)
	for path, v := range map[string]any{filepath.Join(dir, "initialized.json"): marker, filepath.Join(dir, "head.json"): head} {
		b, _ := json.Marshal(v)
		if err = os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	prior, err := recipeRegistryRead(root, current.Recipe.ID, "")
	if err != nil || !prior.Validated {
		t.Fatalf("legacy read: %+v %v", prior, err)
	}
	if _, err = recipeRegistrySave(context.Background(), root, current, raw, current.SemanticHash, legacyRevision); err != nil {
		t.Fatal(err)
	}
	prior, err = recipeRegistryRead(root, current.Recipe.ID, legacyRevision)
	if err != nil || !prior.Validated || prior.SemanticHash != current.SemanticHash {
		t.Fatalf("legacy identity lost: %+v %v", prior, err)
	}
}
