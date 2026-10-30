package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecipeTablesCOWTypedValuesNetDeltasAndDamagedBlob(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tables := []recipeTable{{Name: "counts", Key: "name"}, {Name: "payload", Key: "id"}}
	e := newRecipeTablesEdit(ctx, root, "", tables)
	key := recipeStateKey{Table: "counts", Key: "total"}
	if err := e.PutOrdered(ctx, key, rovUint(1)); err != nil {
		t.Fatal(err)
	}
	if v, err := e.ReadOrdered(ctx, key); err != nil || v.Uint != 1 {
		t.Fatal(v, err)
	}
	if err := e.PutOrdered(ctx, key, rovUint(2)); err != nil {
		t.Fatal(err)
	}
	if err := e.DeleteOrdered(ctx, recipeStateKey{Table: "counts", Key: "absent"}); err != nil {
		t.Fatal(err)
	}
	large := recipeStateKey{Table: "payload", Key: "large"}
	bytes := []byte(strings.Repeat("owned", 40000))
	if err := e.PutOrdered(ctx, large, rovBytes(bytes)); err != nil {
		t.Fatal(err)
	}
	state, journal, changes, entries, err := e.flush(0)
	if err != nil || state == "" || journal == "" || changes != 2 || entries != 2 {
		t.Fatal(state, journal, changes, entries, err)
	}
	next := newRecipeTablesEdit(ctx, root, state, tables, e.pinRoot)
	if v, err := next.ReadOrdered(ctx, large); err != nil || string(v.Bytes) != string(bytes) {
		t.Fatal("large typed value", err)
	}
	if err = next.PutOrdered(ctx, key, rovUint(3)); err != nil {
		t.Fatal(err)
	}
	if err = next.PutOrdered(ctx, key, rovUint(2)); err != nil {
		t.Fatal(err)
	}
	same, delta, changes, entries, err := next.flush(entries)
	if err != nil || same != state || delta != "" || changes != 0 || entries != 2 {
		t.Fatal("intermediate writes leaked into net delta", same, delta, changes, entries, err)
	}
	entry, err := next.entry(large)
	if err != nil || entry == nil || entry.Blob == "" {
		t.Fatal(entry, err)
	}
	path := recipeTablesBlobPath(root, entry.Blob)
	if err = os.WriteFile(path, []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = newRecipeTablesEdit(ctx, root, state, tables).ReadOrdered(ctx, large); err == nil {
		t.Fatal("damaged blob became a value/miss")
	}
	next.AbortOrdered()
	if _, _, _, _, err = next.flush(2); err == nil {
		t.Fatal("aborted table edits flushed")
	}
	if _, err = os.Stat(filepath.Join(root, "head.json")); !os.IsNotExist(err) {
		t.Fatal("private row staging published a head", err)
	}
}

func TestRecipeTablesCOWLargeValuePinsFollowNetReferences(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tables := []recipeTable{{Name: "payload", Key: "id"}}
	value := rovText(strings.Repeat("shared", 20000))
	a, b := recipeStateKey{Table: "payload", Key: "a"}, recipeStateKey{Table: "payload", Key: "b"}
	first := newRecipeTablesEdit(ctx, root, "", tables)
	for _, key := range []recipeStateKey{a, b} {
		if err := first.PutOrdered(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	state, _, _, entries, err := first.flush(0)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := first.entry(a)
	if err != nil {
		t.Fatal(err)
	}
	var pin recipeTablesBlobPin
	if have, err := recipeTablesTree(ctx, filepath.Join(root, "pages"), first.pinRoot).get(entry.Blob, &pin); err != nil || !have || pin.References != 2 {
		t.Fatal(pin, err)
	}
	second := newRecipeTablesEdit(ctx, root, state, tables, first.pinRoot)
	if err = second.DeleteOrdered(ctx, a); err != nil {
		t.Fatal(err)
	}
	state, _, _, entries, err = second.flush(entries)
	if err != nil {
		t.Fatal(err)
	}
	if have, err := recipeTablesTree(ctx, filepath.Join(root, "pages"), second.pinRoot).get(entry.Blob, &pin); err != nil || !have || pin.References != 1 {
		t.Fatal(pin, err)
	}
	third := newRecipeTablesEdit(ctx, root, state, tables, second.pinRoot)
	if err = third.DeleteOrdered(ctx, b); err != nil {
		t.Fatal(err)
	}
	state, _, _, entries, err = third.flush(entries)
	if err != nil || entries != 0 || state != "" || third.pinRoot != "" {
		t.Fatal("unreferenced value retained a live pin", state, entries, third.pinRoot, err)
	}
}

func TestRecipeTablesCOWBatchBeyondOldPrototypeTouchedLimit(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	e := newRecipeTablesEdit(ctx, root, "", []recipeTable{{Name: "items", Key: "id"}})
	started := time.Now()
	for n := 0; n < 4100; n++ {
		key := recipeStateKey{Table: "items", Key: fmt.Sprintf("item-%05d", n)}
		if err := e.PutOrdered(ctx, key, rovUint(uint64(n))); err != nil {
			t.Fatal(n, err)
		}
	}
	state, journal, count, entries, err := e.flush(0)
	if err != nil || state == "" || journal == "" || count != 4100 || entries != 4100 {
		t.Fatal(count, entries, err)
	}
	t.Logf("4100 small typed values plus checked net delta: %s", time.Since(started))
	read := newRecipeTablesEdit(ctx, root, state, []recipeTable{{Name: "items", Key: "id"}})
	for _, n := range []int{0, 2048, 4099} {
		v, err := read.ReadOrdered(ctx, recipeStateKey{Table: "items", Key: fmt.Sprintf("item-%05d", n)})
		if err != nil || v.Uint != uint64(n) {
			t.Fatal(n, v, err)
		}
	}
}
