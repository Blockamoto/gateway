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
	"time"
)

// This exercises the checked point/capture boundary with authentic witnesses.
// The three-block provider is deliberately injected, not admitted as a complete
// genesis prefix. The production factory's missing-prefix rejection is separate.
func recipeParentPointFixture(t *testing.T) (*app, blockView, *recipeProgram, *recipeTablePointLookups, string) {
	t.Helper()
	ctx := context.Background()
	a := runTestApp(t)
	dir := os.Getenv("GATEWAY_PARCEL_REFERENCE_DIR")
	if dir == "" {
		dir = filepath.Join("build", "parcel-reference-079")
	}
	refs := []struct {
		Height int64
		Hash   string
	}{{818117, "000000000000000000035ef07d180022e09a52f44e7587cbac23f25613d35fc2"}, {818174, "000000000000000000033aff8cd450e7a1a26d421a1d32e0d7701b1ea1c1a7c7"}, {818182, "00000000000000000002407a2a6bcca709f08aa425b65de3af9b58b295c62ae5"}}
	blocks := map[int64]blockView{}
	selected := map[int64]string{}
	parentRef, childRef := "", ""
	file, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, ref := range refs {
		raw, e := os.ReadFile(filepath.Join(dir, ref.Hash+".block"))
		if os.IsNotExist(e) {
			t.Skip("optional pinned parent reference blocks unavailable")
		}
		if e != nil {
			t.Fatal(e)
		}
		if len(raw) < 81 {
			t.Fatal("short reference block")
		}
		block, e := parseBlockDetailed(ref.Height, ref.Hash, raw[:80], raw, "parent point reference fixture", false)
		if e != nil || !integrityVerified(block) {
			t.Fatal(e)
		}
		block.Verification.HeaderChainMatch = true
		blocks[ref.Height], selected[ref.Height] = block, ref.Hash
		if _, e = file.WriteAt(raw[:80], ref.Height*80); e != nil {
			t.Fatal(e)
		}
		parsed, e := extractInscriptionOccurrences(block)
		if e != nil {
			t.Fatal(e)
		}
		for _, o := range parsed.Occurrences {
			if o.ID == "025107e06ac442f014c09a73cd97372f69619edd00dbeacca0aac55c75efe3ffi0" {
				parentRef = inscriptionCoordinate(uint32(o.TxIndex), o.Index, ref.Height)
			}
			if o.ID == "f87a6b16a1224b65f9c8ea55e1718585b3075373581689e343860c12a70d15c7i41" {
				childRef = inscriptionCoordinate(uint32(o.TxIndex), o.Index, ref.Height)
			}
		}
	}
	if childRef == "" || parentRef == "" {
		t.Fatal("pinned relationship missing")
	}
	child := blocks[818182]
	a.setStatus(func(status *appStatus) {
		status.HeaderCount = child.Height + 1
		status.HeaderHeight = child.Height
		status.TipHash = child.Hash
	})
	key := childRef + ";" + parentRef
	d := recipeTablesTestProgram(t, "parent-point", child.Height, true).definition
	d.Source = "bitcoin.reveal"
	d.Lookups = recipeInscriptionParentLookupBindings()[:1]
	program := recipeOrderedProgram{Steps: []recipeOrderedInstruction{{Op: "for_events", Body: []recipeOrderedInstruction{{Op: "if", Expr: roCall("eq", roField("ref"), roLit(rovText(childRef))), Body: []recipeOrderedInstruction{{Op: "read", Name: "relation", Table: d.Lookups[0].Name, Key: roCall("text_concat", roCall("text_concat", roField("ref"), roLit(rovText(";"))), roLit(rovText(parentRef)))}, {Op: "put", Table: "counts", Key: roLit(rovText("relation")), Expr: roVar("relation")}}}}}}}
	d.Ordered, err = recipeOrderedProgramJSON(program)
	if err != nil {
		t.Fatal(err)
	}
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	owned, err := recipeOrderedSourceFromBlock(ctx, child)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := newRecipeTablePointLookups(a, d, owned)
	if err != nil {
		t.Fatal(err)
	}
	backend := &parcelReplayBackend079{blocks: blocks}
	reader.parentSources = map[string]inscriptionParentReplaySource{"retained-raw": {app: a, root: a.dataDir, hashAt: func(h int64) (string, error) {
		hash, ok := selected[h]
		if !ok {
			return "", fmt.Errorf("fixture header unavailable")
		}
		return hash, nil
	}, blockAt: func(_ context.Context, h int64) (blockView, error) {
		block, ok := blocks[h]
		if !ok {
			return block, fmt.Errorf("fixture block unavailable")
		}
		return block, nil
	}, spender: func(_ context.Context, point satlinePoint, _ int64, _ parcelScanObserver079) (spenderLookupView, error) {
		return backend.Spender(point.TxID, point.Vout)
	}, transaction: func(_ context.Context, id string, before int64) (satlineTxEvidence, error) {
		return backend.Transaction(id, before)
	}}}
	a.updateBackgroundWork.Wait()
	return a, child, p, reader, key
}

func TestRecipeInscriptionParentPointOwnsProofAndMaintenance(t *testing.T) {
	ctx := context.Background()
	a, block, p, reader, key := recipeParentPointFixture(t)
	saved := saveTablesRecipeForRun(t, a, p)
	g, err := openRecipeTablesGeneration(a.dataDir, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.create(ctx); err != nil {
		t.Fatal(err)
	}
	set := &recipeTableDependencySet{a: a, sources: map[string]*recipeTableDependencySource{}, lookups: reader}
	runCtx := context.WithValue(ctx, recipeTableDependenciesKey{}, set)
	guard := func(publish func() error) error {
		if err := reader.checkHeadersLocked(ctx); err != nil {
			return err
		}
		return publish()
	}
	if err = g.apply(runCtx, block, guard); err != nil {
		t.Fatal(err)
	}
	_, point, err := g.snapshot(ctx)
	if err != nil || point.Imports == "" {
		t.Fatal(point, err)
	}
	var fact recipeImportedFact
	count := 0
	err = recipeTablesTree(ctx, g.path("pages"), point.Imports).walk(func(kv ordinalKV) error {
		var ref recipeImportedFactRef
		if err := json.Unmarshal(kv.Value, &ref); err != nil {
			return err
		}
		var e error
		fact, e = recipeReadImportedFact(ctx, g.profileRoot, kv.Key, ref)
		count++
		return e
	})
	if err != nil || count != 1 || fact.Key != key || len(fact.Blobs) != 4 || len(fact.Evidence) != 3 || fact.Value.Object["state"].Text != "verified" {
		t.Fatal(count, fact, err)
	}
	// Producer copies can be damaged and removed without affecting the child.
	result := reader.parentFacts[recipeParentPointCacheKey(p.definition.Lookups[0], key)]
	for _, input := range result.Blobs {
		if err = os.WriteFile(input.Path, []byte("producer changed"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.Remove(input.Path); err != nil {
			t.Fatal(err)
		}
	}
	if err = g.maintain(ctx); err != nil {
		t.Fatal("owned proof depends on producer files", err)
	}
	out, _, err := a.readRecipeOutput(ctx, saved.Recipe.ID, saved.Revision, saved.SemanticHash, block.Height, 0, 10, "")
	if err != nil || out["state"] != "covered" {
		t.Fatal(out, err)
	}
	rows := out["rows"].([]recipeStoreRow)
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	var value recipeOrderedValue
	if json.Unmarshal(rows[0].Value, &value) != nil || value.Object["state"].Text != "verified" {
		t.Fatal(rows)
	}
	// A corrupt required block must defer every deletion, including old orphans.
	orphanBytes := []byte("unreachable source evidence")
	orphanHash := recipeStoreHash(orphanBytes)
	orphanSource := filepath.Join(t.TempDir(), "orphan")
	if err = os.WriteFile(orphanSource, orphanBytes, 0600); err != nil {
		t.Fatal(err)
	}
	orphan := recipeSourceBlob{Role: "block", Hash: orphanHash, Bytes: int64(len(orphanBytes))}
	if err = recipeOwnSourceBlob(ctx, g.profileRoot, recipeSourceBlobInput{Ref: orphan, Path: orphanSource}); err != nil {
		t.Fatal(err)
	}
	orphanPath := recipeSourceBlobPath(g.profileRoot, orphanHash)
	old := time.Now().Add(-2 * time.Hour)
	if err = os.Chtimes(orphanPath, old, old); err != nil {
		t.Fatal(err)
	}
	var required recipeSourceBlob
	for _, blob := range fact.Blobs {
		if blob.Role == "block" {
			required = blob
			break
		}
	}
	path := recipeSourceBlobPath(g.profileRoot, required.Hash)
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(append([]byte{}, good...), byte(' ')), 0600); err != nil {
		t.Fatal(err)
	}
	if err = g.maintain(ctx); !errors.Is(err, ErrRecipeTablesCorrupt) {
		t.Fatal("corruption allowed collection", err)
	}
	if _, err = os.Stat(orphanPath); err != nil {
		t.Fatal("orphan deleted before required evidence check", err)
	}
	if err = os.WriteFile(path, good, 0600); err != nil {
		t.Fatal(err)
	}
	if err = g.maintain(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Fatal("unused evidence retained", err)
	}
	// Reuse needs only the child's evidence and local selected headers, even
	// when the source's complete-prefix admission is no longer available.
	owned, err := recipeOrderedSourceFromBlock(ctx, block)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := newRecipeTablePointLookups(a, p.definition, owned)
	if err != nil {
		t.Fatal(err)
	}
	edit := newRecipeTablesEdit(ctx, g.profileRoot, point.Root, p.definition.Tables, point.BlobPins)
	defer edit.AbortOrdered()
	imports := newRecipeTablesImportState(ctx, g.profileRoot, point.Imports, edit, &recipeTableDependencySet{a: a, sources: map[string]*recipeTableDependencySource{}, lookups: reopened})
	defer imports.AbortOrdered()
	if got, e := imports.ReadOrdered(ctx, recipeStateKey{Table: p.definition.Lookups[0].Name, Key: key}); e != nil || got.Object["state"].Text != "verified" {
		t.Fatal("owned replay fact needs producer", got, e)
	}
	token, err := g.reviewToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.resetForRebuild(ctx, "parent-point-rebuild", token, orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	archived, err := filepath.Glob(filepath.Join(filepath.Dir(g.profileRoot), "archives", "*", "source-blobs", required.Hash[:2], required.Hash+".blob"))
	if err != nil || len(archived) != 1 {
		t.Fatal("reviewed rebuild lost owned evidence", archived, err)
	}
	if err = recipeSparseVerifyFile(ctx, archived[0], required.Hash, required.Bytes); err != nil {
		t.Fatal(err)
	}
	// The source's intermediate movement header remains part of the final fence.
	header, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = header.WriteAt(make([]byte, 80), 818174*80)
	header.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err = reader.checkHeadersLocked(ctx); err == nil {
		t.Fatal("replaced movement source accepted")
	}
}

func TestRecipeInscriptionParentPointExplicitPrefixAndKeys(t *testing.T) {
	ctx := context.Background()
	_, block, _, reader, _ := recipeParentPointFixture(t)
	reader.parentSources = nil
	for _, lookup := range recipeInscriptionParentLookupBindings() {
		reader.declarations[lookup.Name] = lookup
		key := "0i0.818182;0i0.818117"
		if _, err := reader.read(ctx, lookup, key); !errors.Is(err, errRecipeDependencyUnavailable) {
			t.Fatal("partial source admitted", lookup.Provider, err)
		}
	}
	for _, key := range []string{"", "0i0.818182", "0i0.818181;0i0.0", "0i0.818182;0i0.818183", "00i0.818182;0i0.0", "0i0.818182;0i0.0;0i0.0"} {
		if _, _, ok := recipeInscriptionParentKey(key, block.Height); ok {
			t.Fatal("invalid pair key", key)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := reader.read(cancelled, recipeInscriptionParentLookupBindings()[0], "0i0.818182;0i0.0"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, lookup := range recipeInscriptionParentLookupBindings() {
		if !strings.Contains(lookup.Name, "parent_") || !validHash(lookup.Revision) || !validHash(lookup.SemanticHash) {
			t.Fatal(lookup)
		}
	}
}
