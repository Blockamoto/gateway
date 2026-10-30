package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func projectionGenerationFixture(t *testing.T, id string) (*app, *recipeProjectionGeneration, *recipeProjectionParent, addressStateCheckpoint) {
	t.Helper()
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	d, err := parseRecipe(builtinAddressStateJSON)
	if err != nil {
		t.Fatal(err)
	}
	d.ID, d.Name = id, id
	program, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	g, err := openRecipeProjectionGeneration(a.dataDir, program)
	if err != nil {
		t.Fatal(err)
	}
	u, err := openUTXOStoreContext(ctx, a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	utxoApply(t, u, recipeCoordinatorSource(t)[0])
	parent, err := a.resolveRecipeProjectionParent(ctx, d.Dependencies[0])
	if err != nil {
		t.Fatal(err)
	}
	editor := newAddressStateEditor(ctx, g.profileRoot, addressStateCheckpoint{DestinationVersion: 1, PageGeneration: recipeTestHash("a")})
	if err = editor.flush(); err != nil {
		t.Fatal(err)
	}
	if err = editor.bind(parent.point.Checkpoint); err != nil {
		t.Fatal(err)
	}
	return a, g, parent, editor.cp
}
func projectionGenerationFixtureGuard(publish func() error) error {
	recipeRegistryMu.Lock()
	defer recipeRegistryMu.Unlock()
	return publish()
}
func projectionGenerationFixtureHash(height int64) (string, error) {
	if height != 0 {
		return "", errors.New("no fixture header")
	}
	return genesisHashDisplay, nil
}

func TestRecipeProjectionGenerationIsolationCASAndLostHead(t *testing.T) {
	ctx := context.Background()
	a, g, parent, checkpoint := projectionGenerationFixture(t, "my-addresses")
	if _, initialized, err := g.inspect(ctx); err != nil || initialized {
		t.Fatal(initialized, err)
	}
	if _, err := os.Stat(g.profileRoot); !os.IsNotExist(err) {
		t.Fatal("planning created projection state", err)
	}
	if err := g.create(ctx); err != nil {
		t.Fatal(err)
	}
	head, err := g.head(ctx)
	if err != nil || head.Revision != 0 {
		t.Fatal(head, err)
	}
	point, err := g.writeSnapshot(ctx, checkpoint, parent.point.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	origin := g.token(head)
	if err = g.publish(ctx, a, origin, parent, []recipeProjectionPoint{point}, projectionGenerationFixtureHash, projectionGenerationFixtureGuard); err != nil {
		t.Fatal(err)
	}
	head, err = g.head(ctx)
	if err != nil || head.Revision != 1 || len(head.History) != 1 {
		t.Fatal(head, err)
	}
	if err = g.publish(ctx, a, g.token(head), parent, []recipeProjectionPoint{point}, projectionGenerationFixtureHash, projectionGenerationFixtureGuard); err != nil {
		t.Fatal(err)
	}
	again, err := g.head(ctx)
	if err != nil || again.Checksum != head.Checksum {
		t.Fatal("idempotent publication changed receipt", err)
	}
	if err = g.publish(ctx, a, origin, parent, []recipeProjectionPoint{point}, projectionGenerationFixtureHash, projectionGenerationFixtureGuard); !errors.Is(err, errRecipeStateConflict) {
		t.Fatal("stale CAS accepted", err)
	}
	if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", "address-state")); !os.IsNotExist(err) {
		t.Fatal("private projection wrote native address data", err)
	}
	if err = os.Remove(g.path("projection-head.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err = g.inspect(ctx); err == nil {
		t.Fatal("lost committed head became an unbuilt projection")
	}
}

func TestRecipeProjectionGenerationRejectsSourceAndGuardFailures(t *testing.T) {
	for _, mode := range []string{"denied", "no-callback", "cancel", "namespace", "identity", "payload"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a, g, parent, checkpoint := projectionGenerationFixture(t, "my-addresses")
			if err := g.create(ctx); err != nil {
				t.Fatal(err)
			}
			head, err := g.head(ctx)
			if err != nil {
				t.Fatal(err)
			}
			guard := recipeStoreGuard(projectionGenerationFixtureGuard)
			switch mode {
			case "denied":
				guard = func(func() error) error { return errors.New("revoked") }
			case "no-callback":
				guard = func(func() error) error { return nil }
			case "cancel":
				guard = func(publish func() error) error { cancel(); return projectionGenerationFixtureGuard(publish) }
			case "namespace":
				parent.point.Namespace = recipeTestHash("b")
			case "identity":
				parent.point.Dependency.Name = "other"
			case "payload":
				// Syntactically valid metadata with a different parent receipt
				// must not be accepted merely because its height/hash match.
				checkpoint.Source.TransactionRoot = recipeTestHash("b")
				checkpoint.Source.Commitment = utxoCheckpointDigest(checkpoint.Source)
				checkpoint.UTXOCommitment = checkpoint.Source.Commitment
				checkpoint.Commitment = addressStateDigest(checkpoint)
			}
			point, err := g.writeSnapshot(ctx, checkpoint, parent.point.Namespace)
			if err != nil {
				t.Fatal(err)
			}
			if err = g.publish(ctx, a, g.token(head), parent, []recipeProjectionPoint{point}, projectionGenerationFixtureHash, guard); err == nil {
				t.Fatal("invalid publication accepted", mode)
			}
			got, err := g.head(context.Background())
			if err != nil || got.Revision != 0 {
				t.Fatal("failed publication advanced head", got, err)
			}
		})
	}
}
