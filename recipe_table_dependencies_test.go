package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func recipeDependencyFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			raw, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			result[path] = recipeStoreHash(raw)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRecipeTableDependencySavedExactSnapshotAndReadOnly(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	source := recipeCoordinatorSource(t)
	parent := saveTablesRecipeForRun(t, a, recipeTablesTestProgram(t, "snapshot-parent", 0, false))
	p, err := compileRecipe(parent.Recipe)
	if err != nil {
		t.Fatal(err)
	}
	g, err := openRecipeTablesGeneration(a.dataDir, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.create(ctx); err != nil {
		t.Fatal(err)
	}
	for _, h := range []int64{0, 1} {
		if err = g.apply(ctx, source[h], orderedGenerationTestGuard); err != nil {
			t.Fatal(err)
		}
	}
	dep := recipeDependency{Name: "upstream", Scope: "saved", Instance: parent.Recipe.ID, Revision: parent.Revision, SemanticHash: parent.SemanticHash, Export: "counts", Coverage: recipeTableSnapshotCoverage, Type: recipeTableSnapshotType}
	before := recipeDependencyFiles(t, g.profileRoot)
	reader, err := a.openRecipeTableDependency(ctx, dep, 1, source[1].Hash)
	if err != nil {
		t.Fatal(err)
	}
	v, err := reader.read(ctx, "total")
	if err != nil || v.Kind != "uint" || v.Uint != 2 {
		t.Fatal(v, err)
	}
	v, err = reader.read(ctx, "absent")
	if err != nil || v.Kind != "missing" {
		t.Fatal("complete snapshot absence", v, err)
	}
	older, err := a.openRecipeTableDependency(ctx, dep, 0, source[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	v, err = older.read(ctx, "total")
	if err != nil || v.Uint != 1 {
		t.Fatal("historical snapshot substituted latest value", v, err)
	}
	if _, err = a.openRecipeTableDependency(ctx, dep, 2, testID(669401)); err == nil {
		t.Fatal("unbuilt snapshot became an empty table")
	}
	if _, err = a.openRecipeTableDependency(ctx, dep, 1, testID(669402)); err == nil {
		t.Fatal("forked snapshot accepted")
	}
	bad := dep
	bad.SemanticHash = testID(669403)
	if _, err = a.openRecipeTableDependency(ctx, bad, 1, source[1].Hash); err == nil {
		t.Fatal("changed semantic identity accepted")
	}
	if err = a.checkRecipeTableDependency(ctx, reader); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, recipeDependencyFiles(t, g.profileRoot)) {
		t.Fatal("parent point reads wrote source files")
	}
	child := recipeTablesTestProgram(t, "snapshot-child", 0, true).definition
	child.Dependencies = []recipeDependency{dep}
	child.Ordered = json.RawMessage(`{"steps":[{"op":"for_events","body":[{"op":"read","name":"value","table":"upstream","key":{"literal":{"kind":"text","text":"total"}}}]}]}`)
	if _, err = compileRecipe(child); err != nil {
		t.Fatal(err)
	}
	child.Ordered = json.RawMessage(`{"steps":[{"op":"for_events","body":[{"op":"delete","table":"upstream","key":{"literal":{"kind":"text","text":"total"}}}]}]}`)
	if _, err = compileRecipe(child); err == nil {
		t.Fatal("child acquired write access to a dependency")
	}
}

func TestRecipeTableDependencyBuiltinBitmapCheckpoint(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	revision, semantic, err := recipeNativeBitmapDistrictRevision()
	if err != nil {
		t.Fatal(err)
	}
	dep := recipeDependency{Name: "districts", Scope: "builtin", Instance: bitmapStandardDistrictID, Revision: revision, SemanticHash: semantic, Export: "winners", Coverage: recipeTableSnapshotCoverage, Type: recipeTableSnapshotType}
	if _, err = a.openRecipeTableDependency(ctx, dep, 792435, testID(669411)); err == nil {
		t.Fatal("missing native checkpoint became empty")
	}
	dir := bitmapDevelopmentDir(a.dataDir, bitmapStandardDistrictID)
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("lookup initialized native Districts", err)
	}
	parent := bitmapTestConsumer(t, a.dataDir, bitmapStandardDistrictID)
	block := bitmapDevelopmentFixtureBlock(792435, "", "0.bitmap")
	if err = parent.Apply(ctx, block, nil); err != nil {
		t.Fatal(err)
	}
	before := recipeDependencyFiles(t, dir)
	reader, err := a.openRecipeTableDependency(ctx, dep, block.Height, block.Hash)
	if err != nil {
		t.Fatal(err)
	}
	v, err := reader.read(ctx, "0")
	if err != nil || v.Kind != "object" || v.Object["ref"].Text != "1i0.792435" {
		t.Fatal(v, err)
	}
	v, err = reader.read(ctx, "1")
	if err != nil || v.Kind != "missing" {
		t.Fatal(v, err)
	}
	if !reflect.DeepEqual(before, recipeDependencyFiles(t, dir)) {
		t.Fatal("native snapshot reads changed producer files")
	}
	next := bitmapDevelopmentFixtureBlock(792436, block.Hash, "1.bitmap")
	if err = parent.Apply(ctx, next, nil); err != nil {
		t.Fatal(err)
	}
	if err = a.checkRecipeTableDependency(ctx, reader); err != nil {
		t.Fatal("parent extension invalidated an immutable retained checkpoint", err)
	}
	if err = parent.Reconcile(ctx, func(int64) (string, error) { return testID(669412), nil }); err != nil {
		t.Fatal(err)
	}
	if err = a.checkRecipeTableDependency(ctx, reader); err == nil {
		t.Fatal("withdrawn parent checkpoint remained current")
	}
}
