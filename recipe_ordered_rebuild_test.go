package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRecipeOrderedRebuildPreservesGenerationAndResumesIdempotently(t *testing.T) {
	ctx := context.Background()
	g := orderedGenerationTestOpen(t, t.TempDir(), "rebuilt-coins")
	if err := g.create(ctx); err != nil {
		t.Fatal(err)
	}
	blocks := recipeCoordinatorSource(t)
	for height := int64(0); height <= 1; height++ {
		if err := g.apply(ctx, blocks[height], orderedGenerationTestGuard); err != nil {
			t.Fatal(err)
		}
	}
	head, _ := os.ReadFile(g.headPath())
	if err := os.WriteFile(filepath.Join(g.profileRoot, "preserved-evidence.txt"), []byte("retain me"), 0600); err != nil {
		t.Fatal(err)
	}
	token, err := g.reviewToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const job = "reviewed-job"
	if err = g.resetForRebuild(ctx, job, token, orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(g.profileRoot)
	key := indexDigest(struct{ Semantic, Job string }{g.identity.Semantic, job})
	archive := filepath.Join(parent, "archives", key)
	markerPath := filepath.Join(parent, "rebuilds", key+".json")
	preserved, err := os.ReadFile(filepath.Join(archive, "indexes", "utxo-state", "head.json"))
	if err != nil || !bytes.Equal(preserved, head) {
		t.Fatal("rebuild changed preserved head bytes", err)
	}
	if evidence, err := os.ReadFile(filepath.Join(archive, "preserved-evidence.txt")); err != nil || string(evidence) != "retain me" {
		t.Fatal("rebuild did not preserve all old files", err)
	}
	if err = g.create(ctx); err != nil {
		t.Fatal(err)
	}
	if err = g.apply(ctx, blocks[0], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	fresh, _ := os.ReadFile(g.headPath())
	for _, incomplete := range []bool{false, true} {
		if incomplete {
			var marker recipeOrderedRebuildMarker
			if err = utxoReadJSON(ctx, markerPath, 16<<10, &marker); err != nil {
				t.Fatal(err)
			}
			marker.Complete = false
			marker.Checksum = recipeOrderedRebuildChecksum(marker)
			if err = atomicWriteJSON(markerPath, marker); err != nil {
				t.Fatal(err)
			}
		}
		if err = g.resetForRebuild(ctx, job, token, orderedGenerationTestGuard); err != nil {
			t.Fatal(err)
		}
		after, _ := os.ReadFile(g.headPath())
		if !bytes.Equal(fresh, after) {
			t.Fatal("resuming the same rebuild archived its replacement prefix")
		}
	}
	if err = g.maintain(ctx); err != nil {
		t.Fatal(err)
	}
	if after, err := os.ReadFile(filepath.Join(archive, "indexes", "utxo-state", "head.json")); err != nil || !bytes.Equal(after, head) {
		t.Fatal("replacement maintenance changed its preserved archive", err)
	}
}

func TestRecipeOrderedRebuildReviewAndAuthority(t *testing.T) {
	ctx := context.Background()
	g := orderedGenerationTestOpen(t, t.TempDir(), "review-coins")
	if err := g.create(ctx); err != nil {
		t.Fatal(err)
	}
	token, _ := g.reviewToken(ctx)
	blocks := recipeCoordinatorSource(t)
	if err := g.apply(ctx, blocks[0], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	if err := g.resetForRebuild(ctx, "stale", token, orderedGenerationTestGuard); err == nil {
		t.Fatal("stale generation review accepted")
	}
	token, _ = g.reviewToken(ctx)
	want := errors.New("authority rejected")
	if err := g.resetForRebuild(ctx, "denied", token, func(func() error) error { return want }); !errors.Is(err, want) {
		t.Fatal("rebuild ignored rejected authority", err)
	}
	if err := g.resetForRebuild(ctx, "not-called", token, func(func() error) error { return nil }); err == nil {
		t.Fatal("rebuild accepted authority without publication")
	}
	state, err := g.snapshot(ctx)
	if err != nil || state.head.Checkpoint.Height != 0 {
		t.Fatal("rejected rebuild changed committed state", err)
	}
	// Damaged prior publication metadata is preserved, not reinterpreted as
	// an empty origin and not required to be readable before explicit rebuild.
	broken := []byte("broken prior head")
	if err := os.WriteFile(g.headPath(), broken, 0600); err != nil {
		t.Fatal(err)
	}
	token, _ = g.reviewToken(ctx)
	if err = g.resetForRebuild(ctx, "damaged", token, orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	key := indexDigest(struct{ Semantic, Job string }{g.identity.Semantic, "damaged"})
	old, err := os.ReadFile(filepath.Join(filepath.Dir(g.profileRoot), "archives", key, "indexes", "utxo-state", "head.json"))
	if err != nil || !bytes.Equal(old, broken) {
		t.Fatal("damaged head was not preserved exactly", err)
	}
}
