package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func projectionTestGeneration(t *testing.T, root, id string, legacy bool) *recipeProjectionGeneration {
	t.Helper()
	raw := builtinAddressStateJSON
	if legacy {
		raw = builtinAddressStateLegacyJSON
	}
	d, err := parseRecipe(raw)
	if err != nil {
		t.Fatal(err)
	}
	d.ID, d.Name = id, id
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	g, err := openRecipeProjectionGeneration(root, p)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func projectionTestLatest(t *testing.T, g *recipeProjectionGeneration) (recipeProjectionHead, addressStateCheckpoint) {
	t.Helper()
	h, err := g.head(context.Background())
	if err != nil || h.Revision == 0 {
		t.Fatal(h, err)
	}
	cp, err := g.snapshot(context.Background(), h.History[0])
	if err != nil {
		t.Fatal(err)
	}
	return h, cp
}

func TestRecipeProjectionMaterializeFullDeltaReorgAndOwnedFacts(t *testing.T) {
	ctx := context.Background()
	u, b0, b1 := address076Source(t, []uint64{10, 20, 3}, []string{address076P2PKH, address076P2SH, "51"})
	a := &app{dataDir: u.root}
	g := projectionTestGeneration(t, u.root, "private-addresses", false)
	hashes := map[int64]string{0: b0.Hash, 1: b1.Hash}
	canonical := func(height int64) (string, error) { return hashes[height], nil }
	apply := func(height int64) addressStateCheckpoint {
		t.Helper()
		if err := g.applySnapshot(ctx, a, height, "", canonical, projectionGenerationFixtureGuard, nil); err != nil {
			t.Fatal(err)
		}
		_, cp := projectionTestLatest(t, g)
		return cp
	}
	initial := apply(1)
	if initial.OutputCount != 2 || initial.ValueSats != 30 || initial.ExcludedOutputCount != 1 || initial.ExcludedValueSats != 3 {
		t.Fatal(initial)
	}
	baseline := newAddressStateEditor(ctx, g.profileRoot, addressStateCheckpoint{DestinationVersion: 1, PageGeneration: initial.PageGeneration})
	if err := u.trees(ctx, u.head.Checkpoint)["live"].walk(func(kv ordinalKV) error {
		var member utxoMember
		if err := json.Unmarshal(kv.Value, &member); err != nil {
			return err
		}
		return baseline.legacyChange(member, true)
	}); err != nil {
		t.Fatal(err)
	}
	if err := baseline.flush(); err != nil {
		t.Fatal(err)
	}
	if err := baseline.bind(u.head.Checkpoint); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(initial, baseline.cp) {
		t.Fatal("custom checkpoint differs from native oracle", initial, baseline.cp)
	}
	spend := utxoFixtureSpend(87001, []inputView{{PrevTxID: b1.Transactions[0].TxID}}, 6)
	spend.Outputs[0].ScriptPubKey = address076P2SH
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(87002, 5), spend)
	hashes[2] = b2.Hash
	utxoApply(t, u, b2)
	second := apply(2)
	if second.OutputCount != 2 || second.ValueSats != 26 || second.ExcludedOutputCount != 2 || second.ExcludedValueSats != 8 {
		t.Fatal("delta accounting", second)
	}
	head, _ := projectionTestLatest(t, g)
	if len(head.History) != 2 {
		t.Fatal("missing retained checkpoint", head)
	}
	// A stale observation cannot overwrite the committed projection.
	if err := g.applySnapshot(ctx, a, 2, strings.Repeat("a", 64), canonical, projectionGenerationFixtureGuard, nil); !errors.Is(err, errRecipeStateConflict) {
		t.Fatal("stale work accepted", err)
	}
	if err := u.ReconcileSelected(ctx, 1, func(_ context.Context, h int64) (string, error) { return hashes[h], nil }); err != nil {
		t.Fatal(err)
	}
	rolled := apply(1)
	if !reflect.DeepEqual(rolled, initial) {
		t.Fatal("rollback differs from initial checkpoint", rolled, initial)
	}
	b2b := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(87003, 4))
	b2b.Transactions[0].Outputs[0].ScriptPubKey = address076P2PKH
	hashes[2] = b2b.Hash
	utxoApply(t, u, b2b)
	if cp := apply(2); cp.OutputCount != 3 || cp.ValueSats != 34 {
		t.Fatal("replacement branch accounting", cp)
	}
	if _, err := os.Stat(filepath.Join(u.root, "indexes", "address-state")); !os.IsNotExist(err) {
		t.Fatal("custom projection created native address state", err)
	}
	// Removing a parent's fact name cannot invalidate the child's hard link.
	var first utxoMember
	if err := u.trees(ctx, u.head.Checkpoint)["live"].walk(func(kv ordinalKV) error {
		return json.Unmarshal(kv.Value, &first)
	}); err != nil {
		t.Fatal(err)
	}
	sourcePath, err := outputFactPath(u.root, first.FactRef.Page)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}
	if _, err = readOutputFactContext(ctx, g.profileRoot, first.FactRef); err != nil {
		t.Fatal("parent unlink broke derived facts", err)
	}
	// A committed no-op needs no traversal or source fact access.
	before, _ := projectionTestLatest(t, g)
	apply(2)
	after, _ := projectionTestLatest(t, g)
	if before.Checksum != after.Checksum {
		t.Fatal("no-op changed receipt")
	}
}

func TestRecipeProjectionMaterializeResumeAndCustomPolicy(t *testing.T) {
	ctx := context.Background()
	values, scripts := make([]uint64, 640), make([]string, 640)
	for i := range values {
		values[i], scripts[i] = 1, address076P2PKH
	}
	scripts[0] = "21" + address076PublicKey + "ac"
	u, b0, b1 := address076Source(t, values, scripts)
	a := &app{dataDir: u.root}
	g := projectionTestGeneration(t, u.root, "private-legacy-addresses", true)
	canonical := func(height int64) (string, error) {
		if height == 0 {
			return b0.Hash, nil
		}
		return b1.Hash, nil
	}
	interrupted, cancel := context.WithCancel(ctx)
	err := g.applySnapshot(interrupted, a, 1, "", canonical, projectionGenerationFixtureGuard, func(p addressStateBuildProgress) {
		if p.OutputsChecked == 512 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("traversal did not stop", err)
	}
	h, err := g.head(ctx)
	if err != nil || h.Revision != 0 {
		t.Fatal("partial traversal became visible", h, err)
	}
	manifest, err := readAddressBuildContext(ctx, g.profileRoot)
	if err != nil || manifest == nil || manifest.OutputsChecked != 512 || manifest.ProjectionParent == nil {
		t.Fatal("durable traversal point missing", manifest, err)
	}
	// Reopen the generation as a fresh process would.
	g = projectionTestGeneration(t, u.root, "private-legacy-addresses", true)
	resumed := false
	err = g.applySnapshot(ctx, a, 1, "", canonical, projectionGenerationFixtureGuard, func(p addressStateBuildProgress) { resumed = resumed || p.Resumed })
	if err != nil || !resumed {
		t.Fatal("traversal did not resume", err)
	}
	_, cp := projectionTestLatest(t, g)
	if cp.OutputCount != 639 || cp.ExcludedOutputCount != 1 || cp.ValueSats != 639 || cp.ExcludedValueSats != 1 {
		t.Fatal("custom JSON policy did not select membership", cp)
	}
	current := projectionTestGeneration(t, u.root, "private-current-addresses", false)
	if err = current.applySnapshot(ctx, a, 1, "", canonical, projectionGenerationFixtureGuard, nil); err != nil {
		t.Fatal(err)
	}
	_, cp = projectionTestLatest(t, current)
	if cp.OutputCount != 640 || cp.ExcludedOutputCount != 0 || current.profileRoot == g.profileRoot {
		t.Fatal("recipe policies shared state", cp)
	}
}

func TestRecipeProjectionMaterializeUsesDeclaredSavedCoins(t *testing.T) {
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	view := saveOrderedRecipeForRun(t, a, "empty-source-coins", true)
	program, err := compileRecipe(view.Recipe)
	if err != nil {
		t.Fatal(err)
	}
	coins, err := openRecipeOrderedGeneration(a.dataDir, program)
	if err != nil {
		t.Fatal(err)
	}
	if err = coins.create(ctx); err != nil {
		t.Fatal(err)
	}
	native, err := openUTXOStoreContext(ctx, a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	blocks := recipeCoordinatorSource(t)
	for _, block := range []blockView{blocks[0], blocks[1]} {
		if err = coins.apply(ctx, block, orderedGenerationTestGuard); err != nil {
			t.Fatal(err)
		}
		utxoApply(t, native, block)
	}
	d, err := parseRecipe(builtinAddressStateJSON)
	if err != nil {
		t.Fatal(err)
	}
	d.ID, d.Name = "declared-saved-addresses", "Declared saved addresses"
	d.Dependencies[0] = recipeDependency{Name: "coins", Scope: "saved", Instance: view.Recipe.ID, Revision: view.Revision, SemanticHash: view.SemanticHash, Export: "coins", Type: "coin", Coverage: "snapshot_delta"}
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	g, err := openRecipeProjectionGeneration(a.dataDir, p)
	if err != nil {
		t.Fatal(err)
	}
	canonical := func(height int64) (string, error) { return blocks[height].Hash, nil }
	if err = g.applySnapshot(ctx, a, 1, "", canonical, projectionGenerationFixtureGuard, nil); err != nil {
		t.Fatal(err)
	}
	head, cp := projectionTestLatest(t, g)
	if cp.Source.LiveOutputs != 0 || cp.OutputCount != 0 || cp.ExcludedOutputCount != 0 || head.History[0].Namespace == indexDigest(a.dataDir) || native.head.Checkpoint.LiveOutputs != 1 {
		t.Fatal("projection substituted native coins for declared saved generation", cp)
	}
}
