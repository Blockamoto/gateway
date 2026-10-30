package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The fixture guard tests storage/CAS only. Public source/admission authority
// must be supplied by the integrated selected-header/run host separately.
func orderedGenerationTestGuard(publish func() error) error { return publish() }
func orderedGenerationTestProgram(t *testing.T, id string) *recipeProgram {
	t.Helper()
	d, err := parseRecipe(builtinUTXODefinitionJSON)
	if err != nil {
		t.Fatal(err)
	}
	d.ID, d.Name = id, id
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func orderedGenerationTestOpen(t *testing.T, root, id string) *recipeOrderedGeneration {
	t.Helper()
	g, err := openRecipeOrderedGeneration(root, orderedGenerationTestProgram(t, id))
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestRecipeOrderedGenerationNativeParityIsolationAndRecovery(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	g := orderedGenerationTestOpen(t, root, "my-coins")
	if state, initialized, err := g.inspect(ctx); err != nil || initialized || state != nil {
		t.Fatal("new generation inspection failed", initialized, err)
	}
	if _, err := os.Stat(g.profileRoot); !os.IsNotExist(err) {
		t.Fatal("opening a generation performed a write", err)
	}
	if err := g.create(ctx); err != nil {
		t.Fatal(err)
	}
	origin, err := g.snapshot(ctx)
	if err != nil || origin.head != nil {
		t.Fatal("origin falsely reports completed genesis", err)
	}
	legacy, err := openUTXOStoreContext(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	blocks := recipeCoordinatorSource(t)
	for height := int64(0); height <= 1; height++ {
		if err = legacy.applyBlockLegacyOracle(ctx, blocks[height]); err != nil {
			t.Fatal(err)
		}
		if err = g.apply(ctx, blocks[height], orderedGenerationTestGuard); err != nil {
			t.Fatal(err)
		}
		snapshot, err := g.snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		compareNativeRecipeUTXO(t, legacy, snapshot)
	}
	if _, err = os.Stat(filepath.Join(root, "indexes", "utxo-state", "head.json")); !os.IsNotExist(err) {
		t.Fatal("custom state wrote native profile data", err)
	}
	if _, err = os.Stat(filepath.Join(g.profileRoot, "indexes", "address-state")); !os.IsNotExist(err) {
		t.Fatal("custom state started a native address projection", err)
	}
	g, err = openRecipeOrderedGeneration(root, orderedGenerationTestProgram(t, "my-coins"))
	if err != nil {
		t.Fatal(err)
	}
	if err = g.reconcile(ctx, 0, func(_ context.Context, height int64) (string, error) { return blocks[height].Hash, nil }, orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	snapshot, err := g.snapshot(ctx)
	if err != nil || snapshot.head == nil || snapshot.head.Checkpoint.Height != 0 {
		t.Fatal("custom state did not recover its own genesis", err)
	}
	if err = g.apply(ctx, blocks[1], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	other := orderedGenerationTestOpen(t, root, "other-coins")
	if other.profileRoot == g.profileRoot {
		t.Fatal("instance generations share writable state")
	}
}

func TestRecipeOrderedGenerationPublicationCASAndAuthority(t *testing.T) {
	ctx := context.Background()
	g := orderedGenerationTestOpen(t, t.TempDir(), "cas-coins")
	if err := g.create(ctx); err != nil {
		t.Fatal(err)
	}
	blocks := recipeCoordinatorSource(t)
	if err := g.apply(ctx, blocks[0], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	snapshot, err := g.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.publicationGuard = g.publisher(ctx, snapshot, orderedGenerationTestGuard)
	snapshot.publicationCheck = func(work context.Context) error { return work.Err() }
	competed := false
	snapshot.commitHook = func(stage string) error {
		if stage == "before_head" && !competed {
			competed = true
			return g.apply(ctx, blocks[1], orderedGenerationTestGuard)
		}
		return nil
	}
	if err = snapshot.applyOrderedBlockSnapshot(ctx, blocks[1], *g.program.ordered); !errors.Is(err, errRecipeStateConflict) {
		t.Fatal("stale private COW roots overwrote a competing publication", err)
	}
	current, err := g.snapshot(ctx)
	if err != nil || current.head.Checkpoint.Height != 1 {
		t.Fatal("competing committed state lost", err)
	}
	if err = g.apply(ctx, blocks[1], nil); err == nil {
		t.Fatal("missing authority accepted")
	}
}

func TestRecipeOrderedGenerationDoesNotHoldNativeLocks(t *testing.T) {
	ctx := context.Background()
	g := orderedGenerationTestOpen(t, t.TempDir(), "private-locks")
	if err := g.create(ctx); err != nil {
		t.Fatal(err)
	}
	block := recipeCoordinatorSource(t)[0]
	utxoWriterMu.Lock()
	utxoStateMu.Lock()
	defer utxoWriterMu.Unlock()
	defer utxoStateMu.Unlock()
	done := make(chan error, 1)
	go func() { done <- g.apply(ctx, block, orderedGenerationTestGuard) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("custom snapshot borrowed native locks before its authority guard")
	}
}

func TestRecipeOrderedGenerationMarkerAndMissingHeadFailClosed(t *testing.T) {
	ctx := context.Background()
	g := orderedGenerationTestOpen(t, t.TempDir(), "guarded-coins")
	if err := g.create(ctx); err != nil {
		t.Fatal(err)
	}
	blocks := recipeCoordinatorSource(t)
	if err := g.apply(ctx, blocks[0], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(g.headPath())
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("not admitted")
	if err = g.apply(ctx, blocks[1], func(func() error) error { return stop }); !errors.Is(err, stop) {
		t.Fatal("authority rejection lost", err)
	}
	if err = g.apply(ctx, blocks[1], func(func() error) error { return nil }); err == nil {
		t.Fatal("authority without an actual commit reported success")
	}
	after, _ := os.ReadFile(g.headPath())
	if !bytes.Equal(before, after) {
		t.Fatal("rejected authority changed the head")
	}
	if err = os.Remove(g.headPath()); err != nil {
		t.Fatal(err)
	}
	if err = g.create(ctx); err == nil {
		t.Fatal("missing head was reinterpreted as an empty generation")
	}
	if _, err = g.snapshot(ctx); err == nil {
		t.Fatal("missing head supplied a snapshot")
	}
	if err = os.Remove(g.markerPath()); err != nil {
		t.Fatal(err)
	}
	if err = g.create(ctx); err == nil {
		t.Fatal("unbound retained records were overwritten by a new origin")
	}
}

func TestRecipeOrderedGenerationOriginInitializationAndSemanticOwnership(t *testing.T) {
	ctx := context.Background()
	g := orderedGenerationTestOpen(t, t.TempDir(), "owned-coins")
	if err := os.MkdirAll(filepath.Dir(g.headPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteJSON(g.headPath(), g.origin()); err != nil {
		t.Fatal(err)
	}
	if err := g.create(ctx); err != nil {
		t.Fatal("exact interrupted origin could not finish initialization", err)
	}
	d := g.program.definition
	d.Name = "Renamed coins"
	same, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := openRecipeOrderedGeneration(g.root, same)
	if err != nil || renamed.profileRoot != g.profileRoot {
		t.Fatal("display edit changed the state generation", err)
	}
	var wire any
	if err = json.Unmarshal(d.Ordered, &wire); err != nil {
		t.Fatal(err)
	}
	changed := false
	var visit func(any)
	visit = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			if value["call"] == "retains" {
				delete(value, "call")
				delete(value, "args")
				value["literal"] = map[string]any{"kind": "bool", "bool": false}
				changed = true
			}
			for _, child := range value {
				visit(child)
			}
		case []any:
			for _, child := range value {
				visit(child)
			}
		}
	}
	visit(wire)
	if !changed {
		t.Fatal("fixture did not change a real predicate")
	}
	d.Ordered, _ = json.Marshal(wire)
	different, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	other, err := openRecipeOrderedGeneration(g.root, different)
	if err != nil || other.profileRoot == g.profileRoot {
		t.Fatal("semantic edit reused state", err)
	}
	if err = other.create(ctx); err != nil {
		t.Fatal(err)
	}
	blocks := recipeCoordinatorSource(t)
	for height := int64(0); height <= 1; height++ {
		if err = g.apply(ctx, blocks[height], orderedGenerationTestGuard); err != nil {
			t.Fatal(err)
		}
		if err = other.apply(ctx, blocks[height], orderedGenerationTestGuard); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := g.snapshot(ctx)
	b, _ := other.snapshot(ctx)
	if a.head.Checkpoint.LiveOutputs != 1 || b.head.Checkpoint.LiveOutputs != 0 {
		t.Fatal("custom predicate did not execute independently")
	}
}

func TestRecipeOrderedGenerationPrevoutsDoNotLeakPrivateFactReferences(t *testing.T) {
	// Synthetic storage/relay fixture, not a Bitcoin-integrity assertion.
	ctx := context.Background()
	g := orderedGenerationTestOpen(t, t.TempDir(), "private-prevouts")
	if err := g.create(ctx); err != nil {
		t.Fatal(err)
	}
	b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(77900, 50))
	coinbase := utxoFixtureCoinbase(77901, 10)
	b1 := utxoFixtureBlock(1, b0.Hash, coinbase)
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(77902, 1), utxoFixtureSpend(77903, []inputView{{PrevTxID: coinbase.TxID}}, 9))
	for _, block := range []blockView{b0, b1} {
		if err := g.apply(ctx, block, orderedGenerationTestGuard); err != nil {
			t.Fatal(err)
		}
	}
	prepared := &indexRunPrepared{block: b2, prevouts: map[string]utxoCoin{}}
	work := context.WithValue(ctx, indexRunPreparedKey{}, prepared)
	if err := g.apply(work, b2, orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	coin, found := runPrevout(work, 1, 0)
	if !found || coin.TxID != coinbase.TxID || coin.ValueSats != 10 || coin.Position != outputCoordinate(0, 0, 1) || coin.FactRef != (outputFactRef{}) {
		t.Fatal("cohort relay exposed a private fact pin or lost its authenticated creation", coin, found)
	}
}
