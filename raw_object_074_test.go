package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func rawObjectTarget074(raw []byte) blockTarget {
	h := hash256(raw[:80])
	return blockTarget{Height: 0, HashDisplay: reverseHex(h[:]), HashRaw: h, ExpectedHeader: raw[:80]}
}

func requireSameRawAllocation074(t *testing.T, paths ...string) {
	t.Helper()
	first, err := os.Stat(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths[1:] {
		info, err := os.Stat(path)
		if err != nil || !os.SameFile(first, info) {
			t.Fatal("raw retention paths do not share one filesystem object", path, err)
		}
	}
}

func Test074NewBlocksSegmentsAreIndependentOfCacheRetention(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	a.settings.NetworkDisabled, a.settings.CoreDisabled, a.settings.CoreMountDisabled = true, true, true
	target := rawObjectTarget074(raw)
	view, err := a.fetchBlockWithPolicy(target, "ephemeral")
	if err != nil {
		t.Fatal(err)
	}
	s, err := openIndexStore(a.dataDir, "blocks")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.appendBlock(view, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(a.dataDir, "blocks", "raw", "0-"+genesisHashDisplay+".block")
	if err = atomicWriteBytes(cache, []byte("damaged cache fixture")); err != nil {
		t.Fatal(err)
	}
	if stored, _, err := readIndexedRawBody(a.dataDir, target, nil); err != nil || !bytes.Equal(stored, raw) {
		t.Fatal("cache replacement changed segment bytes", err)
	}
	if err = os.Remove(cache); err != nil {
		t.Fatal(err)
	}
	if stored, _, err := readIndexedRawBody(a.dataDir, target, nil); err != nil || !bytes.Equal(stored, raw) {
		t.Fatal("cache unlink removed durable bytes", err)
	}
	a.settings.ShareCache = true
	if _, err = a.storageBlockForServing(context.Background(), genesisHashDisplay); err == nil {
		t.Fatal("private segment exposed to serving")
	}
}

func Test074RawObjectUnsupportedLinksRetainIndependentCopy(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	target := rawObjectTarget074(raw)
	cache := filepath.Join(a.dataDir, "blocks", "raw", "0-"+genesisHashDisplay+".block")
	durable, _ := indexedRawBodyPath(a.dataDir, genesisHashDisplay)
	calls := 0
	unsupported := func(string, string) error { calls++; return errors.New("filesystem does not support hard links") }
	if err := writeSharedRawObjectWithLink(a.dataDir, durable, target, raw, unsupported); err != nil || calls != 1 {
		t.Fatal("unsupported links must fall back to durable bytes", calls, err)
	}
	left, _ := os.Stat(cache)
	right, _ := os.Stat(durable)
	if os.SameFile(left, right) || left.Size() != int64(len(raw)) || right.Size() != int64(len(raw)) {
		t.Fatal("fallback byte accounting or independent ownership is wrong")
	}
	t.Logf("unsupported hard-link fallback: %d copies, %d logical/unique serialized bytes", 2, 2*len(raw))
	retained := filepath.Join(a.dataDir, "indexes", "sources", genesisHashDisplay+".block")
	if err := writeSharedRawObjectWithLink(a.dataDir, retained, target, raw, unsupported); err != nil {
		t.Fatal("explicit retained source fallback", err)
	}
	third, err := os.Stat(retained)
	if err != nil || third.Size() != int64(len(raw)) || os.SameFile(left, third) || os.SameFile(right, third) {
		t.Fatal("retained fallback did not preserve independent durable bytes", err)
	}
	t.Logf("unsupported links with explicit retain: %d copies, %d logical/unique serialized bytes", 3, 3*len(raw))
	if err := os.Remove(cache); err != nil {
		t.Fatal(err)
	}
	if stored, _, err := readIndexedRawBody(a.dataDir, target, nil); err != nil || !bytes.Equal(stored, raw) {
		t.Fatal("fallback depended on evicted cache", err)
	}
	if stored, _, err := readRetainedIndexBlock(retained, target); err != nil || !bytes.Equal(stored, raw) {
		t.Fatal("retained fallback depended on evicted cache", err)
	}
}

func Test074RawObjectPreservesExistingIndependentCopies(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	target := rawObjectTarget074(raw)
	cache := filepath.Join(a.dataDir, "blocks", "raw", "0-"+genesisHashDisplay+".block")
	durable, _ := indexedRawBodyPath(a.dataDir, genesisHashDisplay)
	if err := atomicWriteBytes(durable, raw); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(durable)
	// Windows lazily obtains the file identity. Capture it before the
	// operation so a replaced name cannot make both later Stats look equal.
	if !os.SameFile(before, before) {
		t.Fatal("could not capture original durable file identity")
	}
	if err := writeSharedRawObject(a.dataDir, durable, target, raw); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(durable)
	cacheInfo, _ := os.Stat(cache)
	if !os.SameFile(before, after) || os.SameFile(cacheInfo, after) {
		t.Fatal("deduplication rewrote an existing valid retention copy")
	}
	for _, path := range []string{cache, durable} {
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, raw) {
			t.Fatal("legacy raw copy changed", path, err)
		}
	}
}
