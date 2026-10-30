package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recipeLegacyDefinitionV4 struct {
	Format       string        `json:"format"`
	Version      int           `json:"version"`
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Source       string        `json:"source"`
	Origin       int64         `json:"origin"`
	History      string        `json:"history"`
	Dependencies []string      `json:"dependencies"`
	Tables       []recipeTable `json:"tables"`
	Steps        []recipeStep  `json:"steps"`
}

func TestRecipeDependencyPreservesUnrelatedV4Identity(t *testing.T) {
	for _, deps := range []string{"null", "[]"} {
		raw := strings.Replace(registryRecipe, `"dependencies":[]`, `"dependencies":`+deps, 1)
		var old recipeLegacyDefinitionV4
		if err := json.Unmarshal([]byte(raw), &old); err != nil {
			t.Fatal(err)
		}
		d, err := parseRecipe([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		for _, exports := range [][]recipeExport{nil, {}} {
			d.Exports = exports
			p, err := compileRecipe(d)
			if err != nil {
				t.Fatal(err)
			}
			legacyCanonical, _ := json.Marshal(old)
			newCanonical, _ := json.Marshal(p.definition)
			if string(legacyCanonical) != string(newCanonical) {
				t.Fatalf("canonical v4 bytes changed: %s vs %s", legacyCanonical, newCanonical)
			}
			semantic := old
			semantic.ID, semantic.Name = "", ""
			oldHash := indexDigest(struct {
				Engine          string                   `json:"engine"`
				Parser          string                   `json:"parser"`
				ParserReference string                   `json:"parser_reference"`
				Envelope        string                   `json:"envelope"`
				CBOR            string                   `json:"cbor"`
				Properties      string                   `json:"properties"`
				Definition      recipeLegacyDefinitionV4 `json:"definition"`
			}{recipeInterpreterProfile, inscriptionParserProfile, inscriptionReferenceCommit, ordInterpretationProfile, recipeCBORProfile, recipePropertiesProfile, semantic})
			if p.semanticHash() != oldHash || recipeRegistryRevisionHash(p.semanticHash(), newCanonical) != recipeRegistryRevisionHash(oldHash, legacyCanonical) {
				t.Fatal("unrelated v4 semantic hash or registry revision changed")
			}
		}
	}
}

func recipeDependencyReferenceBlock(t *testing.T) (blockView, *inscriptionBlockResult) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("docs", "bitmap-evidence", "genesis-block.raw"))
	if err != nil {
		t.Fatal(err)
	}
	hash := "000000000000000000035d4eee4c3b3864b503ed5c6b35a92de219529b34ca2c"
	block, err := parseBlockDetailed(792435, hash, nil, raw, "reference block", false)
	if err != nil || !integrityVerified(block) {
		t.Fatal("block verification", err)
	}
	inventory, err := extractInscriptionOccurrences(block)
	if err != nil {
		t.Fatal(err)
	}
	return block, &inventory
}

func recipeDependencyFixture(t *testing.T) (*app, *recipeProgram, *recipeStore, *recipeProgram, *recipeStore, blockView, *inscriptionBlockResult) {
	t.Helper()
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	parentJSON := `{"format":"gateway-recipe","version":1,"id":"parent","name":"Parent","source":"bitcoin.reveal","origin":792435,"history":"sparse","dependencies":[],"exports":[{"name":"kind","kind":"same_block","table":"items","column":"kind","type":"text"}],"tables":[{"name":"items","key":"ref"}],"steps":[{"op":"require","test":{"call":"eq","args":[{"field":["txid"]},{"literal":"86539aff946c437af8088955827b7e6ff48fc6192836d4071b697b5359b7a732"}]}},{"op":"write","table":"items","row":{"ref":{"field":["ref"]},"kind":{"literal":"alpha"}}}]}`
	view, canonical, err := recipeRegistryCompile([]byte(parentJSON))
	if err != nil {
		t.Fatal(err)
	}
	recipeRegistryMu.Lock()
	view, err = recipeRegistrySave(ctx, a.dataDir, view, canonical, "", "")
	recipeRegistryMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	parent, err := compileRecipe(view.Recipe)
	if err != nil {
		t.Fatal(err)
	}
	parentStore, err := openRecipeStore(a.dataDir, "parent", parent.semanticHash())
	if err != nil {
		t.Fatal(err)
	}
	childJSON := fmt.Sprintf(`{"format":"gateway-recipe","version":1,"id":"child","name":"Child","source":"bitcoin.reveal","origin":792435,"history":"sparse","dependencies":[{"name":"parent","instance":"parent","revision":%q,"semantic_hash":%q,"export":"kind","coverage":"same_block","type":"text"}],"tables":[{"name":"items","key":"ref"}],"steps":[{"op":"let","as":"kind","value":{"call":"dependency.lookup","args":[{"literal":"parent"},{"field":["ref"]}]}},{"op":"require","test":{"call":"and","args":[{"call":"valid","args":[{"variable":"kind"}]},{"call":"eq","args":[{"variable":"kind"},{"literal":"alpha"}]}]}},{"op":"write","table":"items","row":{"ref":{"field":["ref"]},"kind":{"variable":"kind"}}}]}`, view.Revision, view.SemanticHash)
	d, err := parseRecipe([]byte(childJSON))
	if err != nil {
		t.Fatal(err)
	}
	child, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	childStore, err := openRecipeStore(a.dataDir, "child", child.semanticHash())
	if err != nil {
		t.Fatal(err)
	}
	block, inventory := recipeDependencyReferenceBlock(t)
	return a, parent, parentStore, child, childStore, block, inventory
}

func TestRecipeDependencyRealBlockFoundAbsentAndLineage(t *testing.T) {
	ctx := context.Background()
	a, parent, parentStore, child, childStore, block, inventory := recipeDependencyFixture(t)
	if err := a.recipeExecuteDependentBlock(ctx, child, childStore, block, inventory, 0, recipeTestGuard); !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatal("missing parent is not unresolved", err)
	}
	if err := parent.commitInscriptionBlock(ctx, parentStore, block, inventory, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if err := a.recipeExecuteDependentBlock(ctx, child, childStore, block, inventory, 0, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	u, found, err := a.recipeReadDependentUnit(ctx, child, childStore, block.Height, block.Hash, recipeTestGuard)
	if err != nil || !found || len(u.Rows) != 1 || len(u.Lineage) != 1 {
		t.Fatalf("child result: %+v %v %v", u, found, err)
	}
	if _, _, err = childStore.Read(ctx, block.Height); err == nil {
		t.Fatal("unverified dependent read was allowed")
	}
	if _, err = childStore.Coverage(ctx, block.Height, block.Height, nil, 0, 10); err == nil {
		t.Fatal("unverified dependent coverage was allowed")
	}
	if err = parentStore.Reconcile(ctx, map[int64]string{block.Height: block.Hash}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.recipeReadDependentUnit(ctx, child, childStore, block.Height, block.Hash, recipeTestGuard); !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatalf("stale parent lineage served: %v", err)
	}
}

func TestRecipeDependencyPredicateAndConcurrentParentRemoval(t *testing.T) {
	ctx := context.Background()
	a, parent, parentStore, child, childStore, block, inventory := recipeDependencyFixture(t)
	renamed := child.definition
	renamed.ID = "renamed-child"
	renamedProgram, err := compileRecipe(renamed)
	if err != nil || renamedProgram.semanticHash() != child.semanticHash() {
		t.Fatal("renaming child changed semantics", err)
	}
	if err := parent.commitInscriptionBlock(ctx, parentStore, block, inventory, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	changed := child.definition
	changed.Steps = append([]recipeStep(nil), child.definition.Steps...)
	// Reparse a modified definition to avoid sharing nested expression storage.
	raw, _ := json.Marshal(changed)
	raw = []byte(strings.Replace(string(raw), `"alpha"`, `"beta"`, 1))
	d, err := parseRecipe(raw)
	if err != nil {
		t.Fatal(err)
	}
	changedProgram, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	changedStore, err := openRecipeStore(a.dataDir, "child", changedProgram.semanticHash())
	if err != nil {
		t.Fatal(err)
	}
	if err = a.recipeExecuteDependentBlock(ctx, changedProgram, changedStore, block, inventory, 0, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	u, found, err := a.recipeReadDependentUnit(ctx, changedProgram, changedStore, block.Height, block.Hash, recipeTestGuard)
	if err != nil || !found || len(u.Rows) != 0 {
		t.Fatalf("changed predicate: %+v %v %v", u, found, err)
	}
	guard := func(publish func() error) error {
		if err := parentStore.Reconcile(ctx, map[int64]string{block.Height: block.Hash}, recipeTestGuard); err != nil {
			return err
		}
		return publish()
	}
	if err = a.recipeExecuteDependentBlock(ctx, child, childStore, block, inventory, 0, guard); !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatalf("parent changed before child commit: %v", err)
	}
	if _, found, err = childStore.Read(ctx, block.Height); err != nil || found {
		t.Fatalf("stale child committed: %v %v", found, err)
	}
}

func TestRecipeDependencyRequiresExplicitCoverageKind(t *testing.T) {
	_, parent, _, child, _, _, _ := recipeDependencyFixture(t)
	for _, invalid := range []string{"", "snapshot", "delta"} {
		d := parent.definition
		d.Exports = append([]recipeExport(nil), d.Exports...)
		d.Exports[0].Kind = invalid
		if _, err := compileRecipe(d); err == nil || !strings.Contains(err.Error(), "same_block") {
			t.Fatalf("export kind %q accepted: %v", invalid, err)
		}
		d = child.definition
		d.Dependencies = append([]recipeDependency(nil), d.Dependencies...)
		d.Dependencies[0].Coverage = invalid
		if _, err := compileRecipe(d); err == nil || !strings.Contains(err.Error(), "same_block") {
			t.Fatalf("dependency coverage %q accepted: %v", invalid, err)
		}
	}
}

func TestRecipeDependencyRejectsPreOriginEvidence(t *testing.T) {
	a, _, _, child, store, block, _ := recipeDependencyFixture(t)
	if _, err := a.recipeDependencyInputs(context.Background(), child.definition, block.Height-1, block.Hash); !errors.Is(err, errRecipeDependencyUnavailable) {
		t.Fatal("parent pre-origin evidence accepted", err)
	}
	if _, _, err := a.recipeReadDependentUnit(context.Background(), child, store, block.Height-1, block.Hash, recipeTestGuard); err == nil {
		t.Fatal("child pre-origin read accepted")
	}
}

func TestRecipeDependencyCancellationDoesNotPublish(t *testing.T) {
	a, parent, parentStore, child, store, block, inventory := recipeDependencyFixture(t)
	if err := parent.commitInscriptionBlock(context.Background(), parentStore, block, inventory, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	guard := func(publish func() error) error { cancel(); return publish() }
	if err := a.recipeExecuteDependentBlock(ctx, child, store, block, inventory, 0, guard); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	if _, found, err := store.Read(context.Background(), block.Height); err != nil || found {
		t.Fatalf("cancelled child published: %v %v", found, err)
	}
}
