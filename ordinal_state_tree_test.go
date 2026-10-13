package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOrdinalTreePagedCopyOnWriteAndPredecessor(t *testing.T) {
	tree := newOrdinalTree(filepath.Join(t.TempDir(), "pages"), "")
	for i := 0; i < 300; i++ {
		if e := tree.put(satRangeKey(uint64(i*10)), i); e != nil {
			t.Fatal(e)
		}
	}
	root, e := tree.flush()
	if e != nil {
		t.Fatal(e)
	}
	old := newOrdinalTree(tree.dir, root)
	for i := 0; i < 300; i++ {
		var value int
		ok, e := old.get(satRangeKey(uint64(i*10)), &value)
		if e != nil || !ok || value != i {
			t.Fatal(i, value, e)
		}
		ok, e = old.predecessor(satRangeKey(uint64(i*10+9)), &value)
		if e != nil || !ok || value != i {
			t.Fatal(i, value, e)
		}
	}
	tree.remove(satRangeKey(100))
	if e = tree.put(satRangeKey(102), 999); e != nil {
		t.Fatal(e)
	}
	next, e := tree.flush()
	if e != nil || next == root {
		t.Fatal(next, e)
	}
	var value int
	ok, e := old.get(satRangeKey(100), &value)
	if e != nil || !ok || value != 10 {
		t.Fatal("old snapshot mutated", value, e)
	}
	ok, e = tree.predecessor(satRangeKey(101), &value)
	if e != nil || !ok || value != 9 {
		t.Fatal(value, e)
	}
	if e = collectOrdinalPages(tree.dir, []string{next}); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(tree.pagePath(root)); !os.IsNotExist(e) {
		t.Fatal("unretained old page survived", e)
	}
	if e = os.WriteFile(tree.pagePath(next), []byte(`{}`), 0600); e != nil {
		t.Fatal(e)
	}
	corrupt := newOrdinalTree(tree.dir, next)
	if _, e = corrupt.get(satRangeKey(102), &value); e == nil {
		t.Fatal("corrupt current page trusted")
	}
}

func TestOrdinalCollectionCancelledMarkDoesNotSweepAndRestartRemarksChildren(t *testing.T) {
	tree := newOrdinalTree(filepath.Join(t.TempDir(), "pages"), "")
	for i := 0; i < 200; i++ {
		_ = tree.put(satRangeKey(uint64(i)), i)
	}
	root, e := tree.flush()
	if e != nil {
		t.Fatal(e)
	}
	page, e := tree.read(root)
	if e != nil {
		t.Fatal(e)
	}
	child := ""
	for _, hash := range page.Children {
		if hash != "" {
			child = hash
			break
		}
	}
	if child == "" {
		t.Fatal("expected paged root")
	}
	// Simulate power loss: persisted pending generation and parent metadata,
	// but one child's mark was not durable. A new process must not trust it.
	oldOwner := ordinalGCProcessOwner
	defer func() { ordinalGCProcessOwner = oldOwner }()
	ordinalGCProcessOwner = "new-test-process"
	generation := ordinalGCGeneration{Next: 1, Pending: true, Owner: "old-test-process"}
	if e = atomicWriteJSON(filepath.Join(tree.dir, "gc-generation.json"), generation); e != nil {
		t.Fatal(e)
	}
	mark := time.Unix(946684801, 0)
	if e = os.Chtimes(tree.pagePath(root), mark, mark); e != nil {
		t.Fatal(e)
	}
	if e = collectOrdinalPages(tree.dir, []string{root}); e != nil {
		t.Fatal(e)
	}
	fresh := newOrdinalTree(tree.dir, root)
	var value int
	if ok, e := fresh.get(satRangeKey(10), &value); e != nil || !ok || value != 10 {
		t.Fatal("restart deleted reachable child", value, e)
	}
	if _, e = os.Stat(tree.pagePath(child)); e != nil {
		t.Fatal(e)
	}
	_ = tree.put(satRangeKey(1000), "orphan")
	orphan, e := tree.flush()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	e = collectOrdinalPagesContext(ctx, tree.dir, func(visit func(string) error) error {
		if e := visit(root); e != nil {
			return e
		}
		cancel()
		return ctx.Err()
	})
	if e == nil {
		t.Fatal("cancelled mark succeeded")
	}
	if _, e = os.Stat(tree.pagePath(orphan)); e != nil {
		t.Fatal("incomplete marking swept pages", e)
	}
	data, e := os.ReadFile(filepath.Join(tree.dir, "gc-generation.json"))
	if e != nil {
		t.Fatal(e)
	}
	if json.Unmarshal(data, &generation) != nil || !generation.Pending {
		t.Fatal("incremental progress was not persisted")
	}
}
func TestOrdinalTreeInterruptedPagesNeverSelectRoot(t *testing.T) {
	tree := newOrdinalTree(filepath.Join(t.TempDir(), "pages"), "")
	_ = tree.put(fmt.Sprintf("%064x", 1), "committed")
	root, e := tree.flush()
	if e != nil {
		t.Fatal(e)
	}
	_ = tree.put(fmt.Sprintf("%064x", 2), "orphan")
	if _, e = tree.flush(); e != nil {
		t.Fatal(e)
	}
	restarted := newOrdinalTree(tree.dir, root)
	var value string
	if ok, e := restarted.get(fmt.Sprintf("%064x", 2), &value); e != nil || ok {
		t.Fatal("orphan became visible", value, e)
	}
}

func TestOrdinalCollectionUnrepresentableMarkNeverSweeps(t *testing.T) {
	tree := newOrdinalTree(filepath.Join(t.TempDir(), "pages"), "")
	_ = tree.put(fmt.Sprintf("%064x", 1), "live")
	root, e := tree.flush()
	if e != nil {
		t.Fatal(e)
	}
	_ = tree.put(fmt.Sprintf("%064x", 2), "orphan")
	orphan, e := tree.flush()
	if e != nil {
		t.Fatal(e)
	}
	original := ordinalGCSetTimes
	defer func() { ordinalGCSetTimes = original }()
	ordinalGCSetTimes = func(name string, access, modified time.Time) error {
		// A successful setter which stores a rounded/different timestamp must
		// never allow sweep. This models filesystem precision loss portably.
		return os.Chtimes(name, access, modified.Add(time.Second))
	}
	if e = collectOrdinalPages(tree.dir, []string{root}); e == nil {
		t.Fatal("unrepresentable timestamp permitted collection")
	}
	for _, hash := range []string{root, orphan} {
		if _, e = os.Stat(tree.pagePath(hash)); e != nil {
			t.Fatal("failed marking swept pages", hash, e)
		}
	}
}

func TestOrdinalCollectionCoarseTimestampFilesystem(t *testing.T) {
	base := os.Getenv("GATEWAY_TEST_GC_DIRECTORY")
	if base == "" {
		t.Skip("set GATEWAY_TEST_GC_DIRECTORY for an actual coarse-timestamp filesystem")
	}
	dir, e := os.MkdirTemp(base, "gateway-gc-filesystem-")
	if e != nil {
		t.Fatal(e)
	}
	// Keep the fixture and logged path for release evidence.
	t.Logf("filesystem collection fixture retained at %s", dir)
	tree := newOrdinalTree(filepath.Join(dir, "pages"), "")
	_ = tree.put(fmt.Sprintf("%064x", 1), "live")
	root, e := tree.flush()
	if e != nil {
		t.Fatal(e)
	}
	_ = tree.put(fmt.Sprintf("%064x", 2), "orphan")
	orphan, e := tree.flush()
	if e != nil {
		t.Fatal(e)
	}
	if e = collectOrdinalPages(tree.dir, []string{root}); e != nil {
		t.Fatal(e)
	}
	var value string
	if found, e := newOrdinalTree(tree.dir, root).get(fmt.Sprintf("%064x", 1), &value); e != nil || !found || value != "live" {
		t.Fatal("live data lost during collection", found, value, e)
	}
	if _, e = os.Stat(tree.pagePath(orphan)); !os.IsNotExist(e) {
		t.Fatal("orphan was not reclaimed", e)
	}
}
