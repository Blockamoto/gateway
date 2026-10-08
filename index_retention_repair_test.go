package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestIndexRetainedCorruptSourceFallsBackAndRepairs(t *testing.T) {
	for _, damage := range []string{"truncated", "body_corruption", "oversize"} {
		t.Run(damage, func(t *testing.T) {
			a, raw := testAppWithGenesis(t)
			a.settings.CoreDisabled = true
			a.settings.CoreMountDisabled = true
			target, err := a.localBlockTarget(0)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(a.dataDir, "indexes", "sources", genesisHashDisplay+".block")
			bad := append([]byte(nil), raw...)
			switch damage {
			case "truncated":
				bad = bad[:100]
			case "body_corruption":
				bad[len(bad)-1] ^= 1 // Same header, invalid transaction Merkle root.
			case "oversize":
				bad = append(bad, make([]byte, maxMessageSize+1-len(bad))...)
			}
			if err := atomicWriteBytes(path, bad); err != nil {
				t.Fatal(err)
			}
			view, err := a.fetchBlockWithPolicy(target, "retain")
			if err != nil || !integrityVerified(view) || view.SourceNetwork != "cache" {
				t.Fatalf("damaged retained bytes blocked healthy local provider: source=%q err=%v", view.SourceNetwork, err)
			}
			repaired, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(repaired, raw) {
				t.Fatalf("retain did not replace damaged source: %v", err)
			}
			if temporary, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".gateway-*.tmp")); len(temporary) != 0 {
				t.Fatalf("source replacement left temporary files: %v", temporary)
			}
		})
	}
}

func TestIndexRetainedValidSourceSkipsCoreAndRewrite(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	if err := os.Remove(filepath.Join(a.dataDir, "blocks", "raw", "0-"+genesisHashDisplay+".block")); err != nil {
		t.Fatal(err)
	}
	a.cacheMu.Lock()
	a.cacheIndex = newCacheIndex()
	a.cacheMu.Unlock()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected Core lookup", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	a.settings.BitcoinDataDir = t.TempDir()
	a.settings.RPCPort, _ = strconv.Atoi(port)
	a.settings.RPCAuthMode = "userpass"
	a.settings.RPCUser, a.settings.RPCPassword = "fixture", "fixture"
	a.settings.CoreMountDisabled = true
	target, err := a.localBlockTarget(0)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.dataDir, "indexes", "sources", genesisHashDisplay+".block")
	if err := atomicWriteBytes(path, raw); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1600000000, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []string{"ephemeral", "retain"} {
		view, err := a.fetchBlockWithPolicy(target, policy)
		if err != nil || !integrityVerified(view) || view.SourceNetwork != "index_retained" || view.CacheVisibility != "private" {
			t.Fatalf("retained source unavailable for %s: source=%q err=%v", policy, view.SourceNetwork, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("valid retained source still triggered %d Core calls", calls.Load())
	}
	info, err := os.Stat(path)
	if err != nil || !info.ModTime().Equal(stamp) {
		t.Fatalf("unchanged retained source was rewritten: %v", err)
	}
	a.cacheMu.RLock()
	defer a.cacheMu.RUnlock()
	if len(a.cacheIndex.Blocks) != 0 || len(a.cacheIndex.Tx) != 0 || len(a.cacheIndex.Spends) != 0 {
		t.Fatal("retained read implicitly created unrelated cache/index knowledge")
	}
}

func TestIndexRetainedReadRejectsNonFile(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	path := filepath.Join(a.dataDir, "indexes", "sources", genesisHashDisplay+".block")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	target, err := a.localBlockTarget(0)
	if err != nil {
		t.Fatal(err)
	}
	view, err := a.fetchBlockWithPolicy(target, "ephemeral")
	if err != nil || !integrityVerified(view) || view.SourceNetwork != "cache" {
		t.Fatalf("non-file retained source blocked fallback: %v", err)
	}
}

func TestIndexExplicitCacheRespectsCapWhenDefaultDisabled(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	a.settings.CacheBlocks = false
	a.settings.StorageCapMB = 1
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	retained := filepath.Join(a.dataDir, "indexes", "sources", genesisHashDisplay+".block")
	if err := atomicWriteBytes(retained, raw); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(a.dataDir, "blocks", "raw", "old-fixture.block")
	if err := os.WriteFile(old, make([]byte, 2*1024*1024), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1600000000, 0)
	if err := os.Chtimes(old, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	a.enforceCacheLimit()
	if _, err := os.Stat(old); err != nil {
		t.Fatal("legacy disabled-cache policy unexpectedly evicted the fixture")
	}
	target, err := a.localBlockTarget(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.fetchBlockWithPolicy(target, "cache"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("explicit cache policy skipped eviction: %v", err)
	}
	if usage, _ := a.cacheStats(); usage > 1024*1024 {
		t.Fatalf("explicit cache exceeded its cap: %d", usage)
	}
	if a.settings.CacheBlocks {
		t.Fatal("explicit retention changed the global cache setting")
	}
}

func TestIndexExplicitCacheRecordsOnlySourcePossessionAcrossRestart(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.cacheMu.Lock()
	a.cacheIndex = newCacheIndex()
	a.cacheMu.Unlock()
	path := filepath.Join(a.dataDir, "indexes", "sources", genesisHashDisplay+".block")
	if err := atomicWriteBytes(path, raw); err != nil {
		t.Fatal(err)
	}
	target, err := a.localBlockTarget(0)
	if err != nil {
		t.Fatal(err)
	}
	view, err := a.fetchBlockWithPolicy(target, "cache")
	if err != nil || !integrityVerified(view) {
		t.Fatalf("explicit cache fetch: %v", err)
	}
	if err := a.saveCacheIndex(); err != nil {
		t.Fatal(err)
	}
	restarted := &app{dataDir: a.dataDir}
	restarted.loadCacheIndex()
	idx := restarted.cacheIndex
	if len(idx.Blocks) != 1 || !idx.Blocks[genesisHashDisplay].Private || idx.Heights["0"] != genesisHashDisplay {
		t.Fatalf("source possession not retained privately: %+v", idx.Blocks)
	}
	if len(idx.Tx) != 0 || len(idx.Spends) != 0 || len(idx.PrivateTx) != 0 || len(idx.PrivateSpends) != 0 {
		t.Fatal("explicit source caching built unrelated tx/spender indexes")
	}
	// The normal lookup path keeps its existing derived-index behavior.
	legacy := newCacheIndex()
	indexBlockInto(&legacy, view, "0-"+genesisHashDisplay)
	if len(legacy.Tx) != len(view.Transactions) || len(legacy.Tx) == 0 {
		t.Fatal("legacy cache wrapper stopped retaining verified transaction knowledge")
	}
}

func TestIndexExplicitCachePreservesExistingVisibility(t *testing.T) {
	for _, private := range []bool{false, true} {
		name := "public"
		if private {
			name = "private"
		}
		t.Run(name, func(t *testing.T) {
			a, raw := testAppWithGenesis(t)
			a.settings.CoreDisabled = true
			a.settings.CoreMountDisabled = true
			a.cacheMu.Lock()
			entry := a.cacheIndex.Blocks[genesisHashDisplay]
			entry.Private = private
			a.cacheIndex.Blocks[genesisHashDisplay] = entry
			a.cacheMu.Unlock()
			// The private retained provider wins even when raw cache possession
			// already exists. Reading it must not change that cache's policy.
			path := filepath.Join(a.dataDir, "indexes", "sources", genesisHashDisplay+".block")
			if err := atomicWriteBytes(path, raw); err != nil {
				t.Fatal(err)
			}
			target, err := a.localBlockTarget(0)
			if err != nil {
				t.Fatal(err)
			}
			view, err := a.fetchBlockWithPolicy(target, "cache")
			if err != nil || view.SourceNetwork != "index_retained" {
				t.Fatalf("retained source was not used: %v", err)
			}
			if err := a.saveCacheIndex(); err != nil {
				t.Fatal(err)
			}
			restarted := &app{dataDir: a.dataDir}
			restarted.loadCacheIndex()
			if got := restarted.cacheIndex.Blocks[genesisHashDisplay]; got.Private != private {
				t.Fatalf("existing %s cache visibility changed to private=%v", name, got.Private)
			}
		})
	}
}
