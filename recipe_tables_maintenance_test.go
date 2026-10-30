package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecipeTablesPublicationRechecksPredecessorMetadata(t *testing.T) {
	ctx := context.Background()
	g, err := openRecipeTablesGeneration(t.TempDir(), recipeTablesTestProgram(t, "table-fence", 0, false))
	if err != nil {
		t.Fatal(err)
	}
	if err = g.create(ctx); err != nil {
		t.Fatal(err)
	}
	blocks := recipeCoordinatorSource(t)
	if err = g.apply(ctx, blocks[0], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	h, _, err := g.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	head, _ := os.ReadFile(g.path("table-head.json"))
	pointPath := g.path(filepath.Join("points", h.History[0].Commitment+".json"))
	good, err := os.ReadFile(pointPath)
	if err != nil {
		t.Fatal(err)
	}
	err = g.apply(ctx, blocks[1], func(publish func() error) error {
		if err := os.WriteFile(pointPath, []byte("damaged"), 0600); err != nil {
			return err
		}
		return publish()
	})
	if !errors.Is(err, ErrRecipeTablesCorrupt) {
		t.Fatal("damaged predecessor published", err)
	}
	if after, _ := os.ReadFile(g.path("table-head.json")); string(after) != string(head) {
		t.Fatal("failed predecessor fence changed the head")
	}
	if err = os.WriteFile(pointPath, good, 0600); err != nil {
		t.Fatal(err)
	}
	if err = g.apply(ctx, blocks[1], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	recipeTablesTestCount(t, g, 2)
}

func TestRecipeTablesMaintenancePinsLargeValuesAndDefersCorruption(t *testing.T) {
	ctx := context.Background()
	p := recipeTablesTestProgram(t, "table-pins", 0, false)
	program := *p.ordered
	payload := rovText(strings.Repeat("large-owned-value", 6000))
	program.Steps[0].Body[0].Body = append(program.Steps[0].Body[0].Body, recipeOrderedInstruction{Op: "put", Table: "counts", Key: roLit(rovText("payload")), Expr: roLit(payload)})
	raw, err := recipeOrderedProgramJSON(program)
	if err != nil {
		t.Fatal(err)
	}
	p.definition.Ordered = raw
	p, err = compileRecipeTablesContract(p.definition)
	if err != nil {
		t.Fatal(err)
	}
	g, err := openRecipeTablesGeneration(t.TempDir(), p)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.create(ctx); err != nil {
		t.Fatal(err)
	}
	blocks := recipeCoordinatorSource(t)
	for h := int64(0); h <= 1; h++ {
		if err = g.apply(ctx, blocks[h], orderedGenerationTestGuard); err != nil {
			t.Fatal(err)
		}
	}
	h, point, err := g.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if point.BlobPins == "" || point.PreviousBlobPins == "" {
		t.Fatal("large values lack current/undo ownership")
	}
	e := newRecipeTablesEdit(ctx, g.profileRoot, point.Root, p.definition.Tables, point.BlobPins)
	entry, err := e.entry(recipeStateKey{Table: "counts", Key: "payload"})
	if err != nil || entry == nil || entry.Blob == "" {
		t.Fatal(entry, err)
	}
	path := recipeTablesBlobPath(g.profileRoot, entry.Blob)
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	orphanRaw, err := json.Marshal(rovText(strings.Repeat("unreachable", 7000)))
	if err != nil {
		t.Fatal(err)
	}
	orphanHash := recipeStoreHash(orphanRaw)
	orphanPath := recipeTablesBlobPath(g.profileRoot, orphanHash)
	if err = recipeStorePutImmutable(filepath.Dir(orphanPath), orphanHash, orphanRaw); err != nil {
		t.Fatal(err)
	}
	orphanPoint := point
	orphanPoint.BlockHash = testID(782291)
	orphanPoint.Commitment = g.pointDigest(orphanPoint)
	orphanMetadata, err := json.Marshal(orphanPoint)
	if err != nil {
		t.Fatal(err)
	}
	if err = recipeStorePutImmutable(g.path("points"), orphanPoint.Commitment, orphanMetadata); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err = filepath.Walk(g.profileRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return os.Chtimes(path, old, old)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(append([]byte{}, good...), byte(' ')), 0600); err != nil {
		t.Fatal(err)
	}
	if err = g.maintain(ctx); !errors.Is(err, ErrRecipeTablesCorrupt) {
		t.Fatal("required blob corruption did not defer collection", err)
	}
	if _, err = os.Stat(orphanPath); err != nil {
		t.Fatal("corruption allowed orphan-value deletion", err)
	}
	if err = os.WriteFile(path, good, 0600); err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 100; pass++ {
		err = g.maintain(ctx)
		if !errors.Is(err, errOrdinalMaintenancePending) {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Fatal("unreachable large value retained", err)
	}
	if _, err = os.Stat(g.path(filepath.Join("points", orphanPoint.Commitment+".json"))); !os.IsNotExist(err) {
		t.Fatal("unpublished old point retained", err)
	}
	for _, ref := range h.History {
		p, err := g.readPoint(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		read := newRecipeTablesEdit(ctx, g.profileRoot, p.Root, g.program.definition.Tables, p.BlobPins)
		v, err := read.ReadOrdered(ctx, recipeStateKey{Table: "counts", Key: "payload"})
		if err != nil || v.Text != payload.Text {
			t.Fatal("retained snapshot value collected", err)
		}
	}
}
