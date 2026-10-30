package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRecipeProjectionRebuildArchivesOnlyChildAndResumes(t *testing.T) {
	ctx := context.Background()
	u, b0, b1 := address076Source(t, []uint64{7}, []string{address076P2PKH})
	a := &app{dataDir: u.root}
	g := projectionTestGeneration(t, u.root, "rebuilt-addresses", false)
	canonical := func(height int64) (string, error) {
		if height == 0 {
			return b0.Hash, nil
		}
		return b1.Hash, nil
	}
	if err := g.applySnapshot(ctx, a, 1, "", canonical, projectionGenerationFixtureGuard, nil); err != nil {
		t.Fatal(err)
	}
	prior, err := os.ReadFile(g.path("projection-head.json"))
	if err != nil {
		t.Fatal(err)
	}
	parentHead := filepath.Join(u.dir, "head.json")
	sourceBytes, err := os.ReadFile(parentHead)
	if err != nil {
		t.Fatal(err)
	}
	token, err := g.reviewToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const job = "snapshot-rebuild-job"
	if err = g.resetForRebuild(ctx, job, token, projectionGenerationFixtureGuard); err != nil {
		t.Fatal(err)
	}
	key := indexDigest(struct{ Semantic, Job string }{g.identity.Semantic, job})
	parent := filepath.Dir(g.profileRoot)
	archive := filepath.Join(parent, "archives", key)
	markerPath := filepath.Join(parent, "rebuilds", key+".json")
	if kept, err := os.ReadFile(filepath.Join(archive, "projection-head.json")); err != nil || !bytes.Equal(kept, prior) {
		t.Fatal("old child head changed", err)
	}
	if _, initialized, err := g.inspect(ctx); err != nil || initialized {
		t.Fatal("rebuild fabricated a replacement checkpoint", initialized, err)
	}
	if err = g.applySnapshot(ctx, a, 0, "", canonical, projectionGenerationFixtureGuard, nil); err != nil {
		t.Fatal(err)
	}
	_, replacement := projectionTestLatest(t, g)
	if replacement.Height != 0 || replacement.OutputCount != 0 {
		t.Fatal("replacement did not use the requested saved snapshot", replacement)
	}
	for _, incomplete := range []bool{false, true} {
		if incomplete {
			var marker recipeProjectionRebuildMarker
			if err = utxoReadJSON(ctx, markerPath, 16<<10, &marker); err != nil {
				t.Fatal(err)
			}
			marker.Complete = false
			marker.Checksum = recipeGenerationRebuildChecksum(marker)
			if err = atomicWriteJSON(markerPath, marker); err != nil {
				t.Fatal(err)
			}
		}
		if err = g.resetForRebuild(ctx, job, token, projectionGenerationFixtureGuard); err != nil {
			t.Fatal(err)
		}
		_, current := projectionTestLatest(t, g)
		if current.Commitment != replacement.Commitment {
			t.Fatal("resumed rebuild archived the replacement")
		}
	}
	if err = g.maintain(ctx); err != nil {
		t.Fatal(err)
	}
	if current, err := os.ReadFile(parentHead); err != nil || !bytes.Equal(current, sourceBytes) {
		t.Fatal("child rebuild changed its parent", err)
	}
	if kept, err := os.ReadFile(filepath.Join(archive, "projection-head.json")); err != nil || !bytes.Equal(kept, prior) {
		t.Fatal("replacement collection touched the archive", err)
	}
}

func TestRecipeProjectionRebuildReviewAuthorityAndOldBinding(t *testing.T) {
	ctx := context.Background()
	a, g, parent, cp := projectionGenerationFixture(t, "reviewed-addresses")
	if err := g.create(ctx); err != nil {
		t.Fatal(err)
	}
	stale, err := g.reviewToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	point, err := g.writeSnapshot(ctx, cp, parent.point.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	head, _ := g.head(ctx)
	if err = g.publish(ctx, a, g.token(head), parent, []recipeProjectionPoint{point}, projectionGenerationFixtureHash, projectionGenerationFixtureGuard); err != nil {
		t.Fatal(err)
	}
	if err = g.resetForRebuild(ctx, "stale-review", stale, projectionGenerationFixtureGuard); err == nil {
		t.Fatal("stale metadata review accepted")
	}
	token, err := g.reviewToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	denied := errors.New("revoked")
	if err = g.resetForRebuild(ctx, "denied", token, func(func() error) error { return denied }); !errors.Is(err, denied) {
		t.Fatal("rebuild ignored authority", err)
	}
	if err = g.resetForRebuild(ctx, "not-called", token, func(func() error) error { return nil }); err == nil {
		t.Fatal("rebuild accepted missing authority callback")
	}
	old := g.marker("projection-generation-v1")
	old.Identity.Profile = recipeUTXOProjectionProfile // the earlier internal v1 binding
	old.Checksum = recipeProjectionMarkerDigest(old)
	if err = atomicWriteJSON(g.path("recipe-generation.json"), old); err != nil {
		t.Fatal(err)
	}
	if _, _, err = g.inspect(ctx); err == nil {
		t.Fatal("old internal ownership binding was silently adopted")
	}
	token, err = g.reviewToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.resetForRebuild(ctx, "old-binding", token, projectionGenerationFixtureGuard); err != nil {
		t.Fatal("reviewed old binding could not be preserved for rebuild", err)
	}
	if err = g.create(ctx); err != nil {
		t.Fatal(err)
	}
}
