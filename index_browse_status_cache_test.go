package main

import (
	"container/list"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

func TestBrowseStatusCacheRevalidatesBytesMembershipAndHeaders(t *testing.T) {
	a, hashes := browseTXPointFixture(t, 3)
	batch := browseTXPointBatch(t, 1, hashes[1])
	if added, err := a.persistBrowseBatch(context.Background(), "tx-locator", batch); err != nil || !added {
		t.Fatal(added, err)
	}
	want := []heightInterval{{From: 1, To: 1}}
	for i := 0; i < 2; i++ {
		got := a.browseProviderSource("tx-locator")
		if !got.Complete || !reflect.DeepEqual(got.Ranges, want) {
			t.Fatal("valid coverage", got)
		}
	}
	path := filepath.Join(a.dataDir, "indexes", "browse", "tx-locator", "commits", batch.Checkpoint.Commitment+".json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	damaged := append([]byte(nil), original...)
	damaged[len(damaged)/2] ^= 1
	if err := os.WriteFile(path, damaged, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got := a.browseProviderSource("tx-locator"); got.Complete || len(got.Ranges) != 0 {
		t.Fatal("same-length/time corruption must fail closed", got)
	}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if got := a.browseProviderSource("tx-locator"); !got.Complete || !reflect.DeepEqual(got.Ranges, want) {
		t.Fatal("restored bytes", got)
	}
	headers, err := os.ReadFile(a.headersPath)
	if err != nil {
		t.Fatal(err)
	}
	changed := append([]byte(nil), headers...)
	changed[80] ^= 1
	if err := os.WriteFile(a.headersPath, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if got := a.browseProviderSource("tx-locator"); len(got.Ranges) != 0 {
		t.Fatal("changed header anchor must not retain coverage", got)
	}
	if err := os.WriteFile(a.headersPath, headers, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := a.browseProviderSource("tx-locator"); got.Complete || len(got.Ranges) != 0 {
		t.Fatal("missing payload must fail closed", got)
	}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(browseTXPointPath(a.dataDir, 1)); err != nil {
		t.Fatal(err)
	}
	if got := a.browseProviderSource("tx-locator"); !got.Complete || len(got.Ranges) != 0 {
		t.Fatal("removed membership must not survive a cached payload", got)
	}
}

func TestBrowseStatusCacheEvictionIsBounded(t *testing.T) {
	a, hashes := browseTXPointFixture(t, 3)
	batch := browseTXPointBatch(t, 1, hashes[1])
	if added, err := a.persistBrowseBatch(context.Background(), "tx-locator", batch); err != nil || !added {
		t.Fatal(added, err)
	}
	c := &browseStatusBatchCache
	c.Lock()
	c.items = make(map[browseStatusCacheKey]*list.Element)
	c.order.Init()
	var oldest browseStatusCacheKey
	for i := 0; i < browseStatusCacheEntries; i++ {
		key := browseStatusCacheKey{dir: strconv.Itoa(i)}
		if i == 0 {
			oldest = key
		}
		c.items[key] = c.order.PushFront(browseStatusCacheEntry{key: key})
	}
	c.Unlock()
	t.Cleanup(func() {
		c.Lock()
		c.items = make(map[browseStatusCacheKey]*list.Element)
		c.order.Init()
		c.Unlock()
	})
	if _, err := browseStatusTXCheckpoint(a.newBrowseStore("tx-locator"), batch.Checkpoint.Commitment); err != nil {
		t.Fatal(err)
	}
	c.Lock()
	defer c.Unlock()
	if c.order.Len() != browseStatusCacheEntries || len(c.items) != browseStatusCacheEntries || c.items[oldest] != nil {
		t.Fatal("cache must evict its oldest entry at the bound")
	}
}
