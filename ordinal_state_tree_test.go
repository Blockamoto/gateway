package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOrdinalTreeCacheRetainsHotPagesAndBoundsEntries(t *testing.T) {
	tree := newOrdinalTree(filepath.Join(t.TempDir(), "pages"), "")
	writePage := func(value int) string {
		t.Helper()
		raw := []byte(fmt.Sprintf(`{"entries":[{"key":"01","value":%d}]}`, value))
		hash := fmt.Sprintf("%x", sha256.Sum256(raw))
		path := tree.pagePath(hash)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return hash
	}
	hot := writePage(-1)
	first := ""
	for i := 0; i < 520; i++ {
		if _, err := tree.read(hot); err != nil {
			t.Fatal(err)
		}
		hash := writePage(i)
		if i == 0 {
			first = hash
		}
		if _, err := tree.read(hash); err != nil {
			t.Fatal(err)
		}
	}
	if len(tree.cache) != 512 || len(tree.cacheEntries) != 512 || tree.cacheOrder.Len() != 512 {
		t.Fatal("cache exceeded its bound or lost entries")
	}
	if _, ok := tree.cache[hot]; !ok {
		t.Fatal("frequently used page was evicted")
	}
	if _, ok := tree.cache[first]; ok {
		t.Fatal("least recently used page was not evicted")
	}
}

func TestOrdinalTreeReadEvidenceBoundsAndReopen(t *testing.T) {
	for _, test := range []struct {
		name, raw, wantError string
		damagedHash          bool
		oversized            bool
	}{
		{name: "valid", raw: `{"entries":[{"key":"01","value":{"n":7}}]}`},
		{name: "checksum", raw: `{"entries":[{"key":"01","value":7}]}`, damagedHash: true, wantError: "checksum mismatch"},
		{name: "authenticated_truncated_json", raw: `{"entries":[`, wantError: "unexpected end of JSON"},
		{name: "invalid_key", raw: `{"entries":[{"key":"UPPER","value":7}]}`, wantError: "invalid ordinal leaf"},
		{name: "invalid_branch", raw: `{"children":[""]}`, wantError: "invalid ordinal branch"},
		{name: "oversized_extent", raw: `{}`, oversized: true, wantError: "exceeds budget"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tree := newOrdinalTree(filepath.Join(t.TempDir(), "pages"), "")
			raw := []byte(test.raw)
			hash := fmt.Sprintf("%x", sha256.Sum256(raw))
			if test.damagedHash {
				hash = indexDigest("a different ordinal page")
			}
			path := tree.pagePath(hash)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if test.oversized {
				file, err := os.OpenFile(path, os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				err = file.Truncate(ordinalPageMaxBytes + 1)
				closeErr := file.Close()
				if err != nil || closeErr != nil {
					t.Fatal(err, closeErr)
				}
			}
			for attempt := 0; attempt < 2; attempt++ {
				page, err := newOrdinalTree(tree.dir, hash).read(hash)
				if test.wantError != "" {
					if err == nil || !strings.Contains(err.Error(), test.wantError) {
						t.Fatal("invalid page accepted or wrong failure", page, err)
					}
				} else if err != nil || len(page.Entries) != 1 || page.Entries[0].Key != "01" || string(page.Entries[0].Value) != `{"n":7}` {
					t.Fatal("valid page changed", page, err)
				}
				// Both success and early rejection must release the read handle.
				// Reopen through a fresh tree so its cache cannot hide disk access.
				if err := os.Rename(path, path+".moved"); err != nil {
					t.Fatal("page read left its file unavailable for rename", err)
				}
				if err := os.Rename(path+".moved", path); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	t.Run("exact_byte_budget", func(t *testing.T) {
		prefix, suffix := `{"entries":[{"key":"01","value":"`, `"}]}`
		raw := []byte(prefix + strings.Repeat("x", ordinalPageMaxBytes-len(prefix)-len(suffix)) + suffix)
		if len(raw) != ordinalPageMaxBytes {
			t.Fatal("boundary fixture size", len(raw))
		}
		tree := newOrdinalTree(filepath.Join(t.TempDir(), "pages"), "")
		hash := fmt.Sprintf("%x", sha256.Sum256(raw))
		path := tree.pagePath(hash)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		page, err := tree.read(hash)
		if err != nil || len(page.Entries) != 1 || len(page.Entries[0].Value) != len(raw)-len(prefix)-len(suffix)+2 {
			t.Fatal("maximum legal page rejected or truncated", err)
		}
	})
}

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
