package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pagePackTestDir(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	dir := filepath.Join(parent, "packs")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := syncDirectory(parent); e != nil {
		t.Fatal(e)
	}
	return dir
}
func pagePackFixture(t *testing.T) [][]byte {
	t.Helper()
	tree := newOrdinalTree(filepath.Join(t.TempDir(), "ordinal-pages"), "")
	for i := 0; i < 150; i++ {
		key := indexDigest(fmt.Sprintf("pack-key-%d", i))
		if e := tree.put(key, map[string]int{"value": i}); e != nil {
			t.Fatal(e)
		}
	}
	root, e := tree.flush()
	if e != nil {
		t.Fatal(e)
	}
	var result [][]byte
	seen := map[string]bool{}
	var walk func(string)
	walk = func(hash string) {
		if hash == "" || seen[hash] {
			return
		}
		seen[hash] = true
		p, e := tree.read(hash)
		if e != nil {
			t.Fatal(e)
		}
		data, e := os.ReadFile(tree.pagePath(hash))
		if e != nil {
			t.Fatal(e)
		}
		result = append(result, data)
		for _, child := range p.Children {
			walk(child)
		}
	}
	walk(root)
	if len(result) < 2 {
		t.Fatal("fixture did not create multiple physical pages")
	}
	return result
}
func TestRecipePagePackRoundTripOrdinalPages(t *testing.T) {
	pages := pagePackFixture(t)
	dir := pagePackTestDir(t)
	ctx := context.Background()
	id, e := recipePagePackPublish(ctx, dir, pages, nil)
	if e != nil {
		t.Fatal(e)
	}
	pack, e := recipePagePackOpen(ctx, dir, id)
	if e != nil {
		t.Fatal(e)
	}
	if len(pack.index) != len(pages) {
		t.Fatalf("page count %d != %d", len(pack.index), len(pages))
	}
	for _, data := range pages {
		digest := recipeStoreHash(data)
		got, e := pack.page(ctx, digest)
		if e != nil || !bytes.Equal(got, data) {
			t.Fatalf("page mismatch %s: %v", digest, e)
		}
		got[0] ^= 1
		again, e := pack.page(ctx, digest)
		if e != nil || !bytes.Equal(again, data) {
			t.Fatal("page caller mutated shared pack")
		}
	}
	reverse := append([][]byte(nil), pages...)
	for i, j := 0, len(reverse)-1; i < j; i, j = i+1, j-1 {
		reverse[i], reverse[j] = reverse[j], reverse[i]
	}
	reverse = append(reverse, reverse[0])
	_, same, e := recipePagePackEncode(ctx, reverse)
	if e != nil || id != same {
		t.Fatalf("pack not canonical/deduplicated: %s %s %v", id, same, e)
	}
	if _, e = pack.page(ctx, strings.Repeat("0", 64)); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("missing page: %v", e)
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 1 {
		t.Fatalf("expected one immutable pack: %v %v", entries, e)
	}
}
func TestRecipePagePackRejectsCorruptOrMalformedBytes(t *testing.T) {
	pages := pagePackFixture(t)
	ctx := context.Background()
	encoded, id, e := recipePagePackEncode(ctx, pages)
	if e != nil {
		t.Fatal(e)
	}
	bad := append([]byte(nil), encoded...)
	bad[len(bad)-1] ^= 1
	if _, e = recipePagePackDecode(ctx, bad, id); e == nil {
		t.Fatal("accepted wrong pack digest")
	}
	// Rehashing corrupt page data cannot bypass the page's own digest.
	if _, e = recipePagePackDecode(ctx, bad, recipeStoreHash(bad)); e == nil {
		t.Fatal("accepted corrupt page under new pack digest")
	}
	bad = append([]byte(nil), encoded...)
	binary.BigEndian.PutUint32(bad[8:12], recipePagePackMaxPages+1)
	if _, e = recipePagePackDecode(ctx, bad, recipeStoreHash(bad)); e == nil {
		t.Fatal("accepted overlarge count")
	}
	bad = append([]byte(nil), encoded...)
	binary.BigEndian.PutUint32(bad[44:48], 0xffffffff)
	if _, e = recipePagePackDecode(ctx, bad, recipeStoreHash(bad)); e == nil {
		t.Fatal("accepted invalid extent")
	}
	bad = append(append([]byte(nil), encoded...), 0)
	if _, e = recipePagePackDecode(ctx, bad, recipeStoreHash(bad)); e == nil {
		t.Fatal("accepted trailing bytes")
	}
	bad = append([]byte(nil), encoded...)
	copy(bad[48:80], bad[12:44])
	if _, e = recipePagePackDecode(ctx, bad, recipeStoreHash(bad)); e == nil {
		t.Fatal("accepted duplicate directory key")
	}
	invalid := []byte(`{"entries":[{"key":"xyz","value":1}]}`)
	if _, _, e = recipePagePackEncode(ctx, [][]byte{invalid}); e == nil {
		t.Fatal("accepted invalid ordinal page")
	}
}
func TestRecipePagePackInterruptedPublicationAndRetry(t *testing.T) {
	pages := pagePackFixture(t)
	ctx := context.Background()
	_, expected, e := recipePagePackEncode(ctx, pages)
	if e != nil {
		t.Fatal(e)
	}
	for _, step := range []string{"write", "sync", "link"} {
		t.Run(step, func(t *testing.T) {
			dir := pagePackTestDir(t)
			failure := func() error { return fmt.Errorf("injected interruption") }
			hooks := &recipePagePackHooks{}
			switch step {
			case "write":
				hooks.afterWrite = failure
			case "sync":
				hooks.afterSync = failure
			case "link":
				hooks.afterLink = failure
			}
			if id, e := recipePagePackPublish(ctx, dir, pages, hooks); e == nil || id != "" {
				t.Fatalf("interrupted publication returned id: %q %v", id, e)
			}
			path := filepath.Join(dir, expected+".pack")
			_, e := os.Stat(path)
			if step != "link" && !os.IsNotExist(e) {
				t.Fatalf("premature visible pack: %v", e)
			}
			if step == "link" && e != nil {
				t.Fatalf("linked pack missing: %v", e)
			}
			// An orphaned temp file from a real crash is never a published pack.
			if e = os.WriteFile(filepath.Join(dir, ".pack-stage-orphan.tmp"), []byte("partial"), 0600); e != nil {
				t.Fatal(e)
			}
			id, e := recipePagePackPublish(ctx, dir, pages, nil)
			if e != nil || id != expected {
				t.Fatalf("safe retry: %s %v", id, e)
			}
			if _, e = recipePagePackOpen(ctx, dir, id); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestRecipePagePackCancellationAndExistingCorruption(t *testing.T) {
	pages := pagePackFixture(t)
	dir := pagePackTestDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	if _, e := recipePagePackPublish(ctx, dir, pages, &recipePagePackHooks{afterSync: func() error { cancel(); return nil }}); !errors.Is(e, context.Canceled) {
		t.Fatalf("cancellation: %v", e)
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 0 {
		t.Fatalf("cancelled write visible: %v %v", entries, e)
	}
	ctx = context.Background()
	id, e := recipePagePackPublish(ctx, dir, pages, nil)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, id+".pack")
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	raw[len(raw)-1] ^= 1
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = recipePagePackOpen(ctx, dir, id); e == nil {
		t.Fatal("accepted corrupt existing pack")
	}
	if _, e = recipePagePackPublish(ctx, dir, pages, nil); e == nil {
		t.Fatal("overwrote corrupt immutable pack")
	}
	after, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(raw, after) {
		t.Fatal("existing corrupt evidence changed")
	}
}
func TestRecipePagePackBoundsAndDecodeCancellation(t *testing.T) {
	pages := pagePackFixture(t)
	missing := filepath.Join(t.TempDir(), "missing")
	if _, e := recipePagePackPublish(context.Background(), missing, pages, nil); e == nil {
		t.Fatal("created unowned pack directory")
	}
	if _, e := os.Stat(missing); !os.IsNotExist(e) {
		t.Fatal("missing pack directory was changed")
	}
	repeated := []byte(`{"entries":[{"key":"01","value":"` + strings.Repeat("a", 4<<20) + `"}]}`)
	if _, _, e := recipePagePackEncode(context.Background(), [][]byte{repeated, repeated, repeated, repeated}); e == nil {
		t.Fatal("duplicate input bypassed aggregate work limit")
	}
	if _, _, e := recipePagePackEncode(context.Background(), make([][]byte, recipePagePackMaxPages+1)); e == nil {
		t.Fatal("accepted page count beyond bound")
	}
	if _, _, e := recipePagePackEncode(context.Background(), [][]byte{bytes.Repeat([]byte("a"), ordinalPageMaxBytes+1)}); e == nil {
		t.Fatal("accepted oversized page")
	}
	raw, id, e := recipePagePackEncode(context.Background(), pages)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = recipePagePackDecode(ctx, raw, id); !errors.Is(e, context.Canceled) {
		t.Fatalf("decode cancellation: %v", e)
	}
	// A small explicit page digest checks the format keeps the same SHA identity.
	page, _ := json.Marshal(ordinalPage{Entries: []ordinalKV{{Key: "01", Value: json.RawMessage(`1`)}}})
	encoded, _, e := recipePagePackEncode(context.Background(), [][]byte{page})
	if e != nil {
		t.Fatal(e)
	}
	if got := hex.EncodeToString(encoded[12:44]); got != recipeStoreHash(page) {
		t.Fatalf("page identity changed: %s", got)
	}
}
