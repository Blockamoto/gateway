package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSavedRecipeResolutionIsProfileScopedAndReadOnly(t *testing.T) {
	ctx := context.Background()
	root1, root2 := t.TempDir(), t.TempDir()
	a, b := &app{dataDir: root1}, &app{dataDir: root2}
	empty, err := a.ListSavedRecipes(ctx)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty profile: %v %v", empty, err)
	}
	if _, err = os.Stat(filepath.Join(root1, "indexes")); !os.IsNotExist(err) {
		t.Fatalf("read created files: %v", err)
	}
	handler := indexInterfaceHandler(a)
	saved := registryView(t, indexInterfaceCall(handler, "POST", "/api/v1/index/recipes", registryPayload(registryRecipe, "")))
	got, err := a.ResolveSavedRecipe(ctx, "custom-one", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != saved.Revision || got.SemanticHash != saved.SemanticHash || got.Source != "bitcoin.reveal" || got.Origin != 0 || got.History != "sparse" || got.SourceCapability.ID != got.Source || len(got.SourceCapability.Fields) == 0 || len(got.Functions) == 0 || len(got.Steps) == 0 || got.Execution != "finite_independent" || got.LegacyCompatibility != "unverified" {
		t.Fatalf("bad resolved definition: %+v", got)
	}
	list, err := a.ListSavedRecipes(ctx)
	if err != nil || len(list) != 1 || list[0].Revision != saved.Revision {
		t.Fatalf("list: %+v %v", list, err)
	}
	got.Definition.Steps[0].Op = "changed"
	got.SourceCapability.Fields[0].Path[0] = "changed"
	again, err := a.ResolveSavedRecipe(ctx, "custom-one", "", "")
	if err != nil || again.Definition.Steps[0].Op == "changed" || again.SourceCapability.Fields[0].Path[0] == "changed" {
		t.Fatalf("resolver leaked mutable state: %+v %v", again, err)
	}
	if _, err := b.ResolveSavedRecipe(ctx, "custom-one", "", ""); err == nil {
		t.Fatal("other profile saw recipe")
	}
	other, err := b.ListSavedRecipes(ctx)
	if err != nil || len(other) != 0 {
		t.Fatalf("other profile list: %+v %v", other, err)
	}
}

func TestSavedRecipeResolutionRequiresHistoricalSemanticBinding(t *testing.T) {
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	h := indexInterfaceHandler(a)
	first := registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(registryRecipe, "")))
	changed := strings.Replace(registryRecipe, "text/plain", "image/png", 1)
	second := registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(changed, first.SemanticHash, first.Revision)))
	if _, err := a.ResolveSavedRecipe(ctx, "custom-one", first.Revision, ""); err == nil {
		t.Fatal("historical recipe accepted without semantic binding")
	}
	old, err := a.ResolveSavedRecipe(ctx, "custom-one", first.Revision, first.SemanticHash)
	if err != nil || old.Revision != first.Revision || old.SemanticHash != first.SemanticHash {
		t.Fatalf("historical lookup: %+v %v", old, err)
	}
	if _, err := a.ResolveSavedRecipe(ctx, "custom-one", first.Revision, second.SemanticHash); err == nil {
		t.Fatal("wrong historical semantic hash accepted")
	}
	if _, err := a.ResolveSavedRecipe(ctx, "custom-one", "", first.SemanticHash); err == nil {
		t.Fatal("stale current semantic hash accepted")
	}
	if _, err := a.ResolveSavedRecipe(ctx, "custom-one", strings.Repeat("a", 64), first.SemanticHash); err == nil {
		t.Fatal("unknown revision accepted")
	}
	if _, err := a.ResolveSavedRecipe(ctx, "custom-one", "../bad", ""); err == nil {
		t.Fatal("invalid revision accepted")
	}
}

func TestSavedRecipeResolutionFailsClosedOnCorruptionAndCancellation(t *testing.T) {
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	h := indexInterfaceHandler(a)
	saved := registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(registryRecipe, "")))
	dir, err := recipeRegistryDir(a.dataDir, "custom-one")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "revisions", saved.Revision+".json")
	if err = os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = a.ResolveSavedRecipe(ctx, "custom-one", "", ""); err == nil {
		t.Fatal("corrupt definition accepted")
	}
	if list, err := a.ListSavedRecipes(ctx); err == nil || list != nil {
		t.Fatal("corrupt registry partially listed")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.ListSavedRecipes(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := a.ResolveSavedRecipe(cancelled, "custom-one", "", ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestSavedRecipeResolutionRejectsOtherInterpreterProfile(t *testing.T) {
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	handler := indexInterfaceHandler(a)
	saved := registryView(t, indexInterfaceCall(handler, "POST", "/api/v1/index/recipes", registryPayload(registryRecipe, "")))
	dir, err := recipeRegistryDir(a.dataDir, "custom-one")
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(dir, "revisions", saved.Revision+".json"))
	if err != nil {
		t.Fatal(err)
	}
	oldSemantic := strings.Repeat("f", 64)
	oldRevision := recipeRegistryRevisionHash(oldSemantic, source)
	if err = os.WriteFile(filepath.Join(dir, "revisions", oldRevision+".json"), source, 0600); err != nil {
		t.Fatal(err)
	}
	receipt := recipeRegistryReceipt{Schema: 1, ID: "custom-one", Revision: oldRevision, SemanticHash: oldSemantic}
	receipt.Checksum = recipeRegistryReceiptDigest(receipt)
	if err = atomicWriteJSON(filepath.Join(dir, "receipts", oldRevision+".json"), receipt); err != nil {
		t.Fatal(err)
	}
	historical, err := recipeRegistryRead(a.dataDir, "custom-one", oldRevision)
	if err != nil || historical.SemanticHash != oldSemantic || historical.Validated {
		t.Fatalf("old receipt not inspectable: %+v %v", historical, err)
	}
	if _, err = a.ResolveSavedRecipe(ctx, "custom-one", oldRevision, oldSemantic); err == nil {
		t.Fatal("old interpreter profile resolved for planning")
	}
	head := recipeRegistryHead{Schema: 1, ID: "custom-one", Revision: oldRevision, SemanticHash: oldSemantic}
	head.Checksum = recipeRegistryHeadDigest(head)
	if err = atomicWriteJSON(filepath.Join(dir, "head.json"), head); err != nil {
		t.Fatal(err)
	}
	if _, err = a.ResolveSavedRecipe(ctx, "custom-one", "", ""); err == nil {
		t.Fatal("current head with old profile resolved")
	}
	if list, err := a.ListSavedRecipes(ctx); err == nil || list != nil {
		t.Fatal("old profile was silently listed as usable")
	}
}
