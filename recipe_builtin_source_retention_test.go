package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRecipeSourceRetentionSemanticBinding(t *testing.T) {
	for _, source := range []string{recipeHeaderSource, recipeBlockSource, recipeBlockBodySource} {
		p, semantic, err := builtinSourceRetentionRecipe(source)
		if err != nil {
			t.Fatal(err)
		}
		t.Log(source, "program", p.semanticHash(), "native bundle", semantic)
		if err = recipeSourceRetentionCheck(source, semantic); err != nil {
			t.Fatal(err)
		}
		if err = recipeSourceRetentionCheck(source, testID(888300)); err == nil {
			t.Fatal("unreviewed source semantics admitted")
		}
	}
}

func TestRecipeNativeHeaderAppendPhysicalParity(t *testing.T) {
	ctx := context.Background()
	raw := mainnetBlockOne(t)
	genesis := testGenesisBlockPayload(t)
	roots := []string{t.TempDir(), t.TempDir()}
	paths := []string{filepath.Join(roots[0], "headers.bin"), filepath.Join(roots[1], "headers.bin")}
	for _, path := range paths {
		if err := os.WriteFile(path, genesis[:80], 0600); err != nil {
			t.Fatal(err)
		}
	}
	for i, path := range paths {
		file, err := os.OpenFile(path, os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		var count int64
		if i == 0 {
			count, err = appendCheckedHeaderBatch051(ctx, path, 1, [][]byte{raw[:80]}, file)
		} else {
			count, err = recipeLegacyAppendHeaders(ctx, path, 1, [][]byte{raw[:80]}, file)
		}
		file.Close()
		if err != nil || count != 2 {
			t.Fatal(count, err)
		}
	}
	left, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	right, err := os.ReadFile(paths[1])
	if err != nil || !bytes.Equal(left, right) || len(left) != 160 {
		t.Fatal("native header file changed", err)
	}
	for _, path := range paths {
		if _, err = os.Stat(headerAppendMarkerPath051(path)); !os.IsNotExist(err) {
			t.Fatal("committed header marker remains", err)
		}
	}
}

func TestRecipeNativeRawFullPhysicalParityAndReferenceCost(t *testing.T) {
	ctx := context.Background()
	block, _ := recipeDependencyReferenceBlock(t)
	if _, _, err := builtinSourceRetentionRecipe(recipeBlockSource); err != nil {
		t.Fatal(err)
	}
	roots := []string{t.TempDir(), t.TempDir()}
	var records [2]*bitcoinIndexBlock
	for i, root := range roots {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		start := time.Now()
		var err error
		if i == 0 {
			records[i], err = retainVerifiedRawBlock(ctx, root, block, nil)
		} else {
			records[i], err = recipeLegacyRetainRaw(ctx, root, block, nil)
		}
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("source writer %s: %v, %d cumulative allocated bytes; one already-parsed authentic block, fresh root, excludes acquisition; not peak/full-chain", []string{"recipe", "frozen-native"}[i], elapsed, after.TotalAlloc-before.TotalAlloc)
	}
	left, _ := json.Marshal(records[0])
	right, _ := json.Marshal(records[1])
	if !bytes.Equal(left, right) {
		t.Fatalf("Raw reference changed\n%s\n%s", left, right)
	}
	for i, record := range records {
		frame, err := os.ReadFile(rawSegmentPath(roots[i], record.RawBody.Segment.File))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			left = frame
		} else if !bytes.Equal(left, frame) {
			t.Fatal("Raw frame bytes changed")
		}
		row, have, err := rawBodyCatalogRow(ctx, roots[i], blockTarget{Height: block.Height, HashDisplay: block.Hash})
		if err != nil || !have || row.Count != block.TransactionCount {
			t.Fatal(row, have, err)
		}
	}
}
func TestRecipeBlockBodySourceDoesNotInventHeight(t *testing.T) {
	ctx := context.Background()
	raw := mainnetBlockOne(t)
	hash := hash256(raw[:80])
	block, err := parseBlockDetailed(-1, reverseHex(hash[:]), nil, raw, "unlocated body source", false)
	if err != nil {
		t.Fatal(err)
	}
	event, err := recipeBlockBodySourceEvent(ctx, block)
	if err != nil {
		t.Fatal(err)
	}
	if _, have := event["height"]; have {
		t.Fatal("hash-only body acquired a false height")
	}
	if event["block"].Text != block.Hash || !bytes.Equal(event["raw_block"].Bytes, raw) || event["transaction_count"].Uint != block.TransactionCount {
		t.Fatal(event)
	}
	p, _, err := builtinSourceRetentionRecipe(recipeBlockBodySource)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	if err = p.evaluateOrderedSparseTypedEventsInto(ctx, func(sink func(map[string]recipeOrderedValue) error) error { return sink(event) }, func(table, key string, value recipeOrderedValue) error {
		count++
		if table != "bodies" || key != block.Hash || value.Object["height"].Kind != "" {
			t.Fatal("unlocated projection claims height", table, key, value)
		}
		return nil
	}); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
