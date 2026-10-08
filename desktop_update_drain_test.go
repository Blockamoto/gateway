package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func waitUpdateWriters064(t *testing.T, a *app) {
	t.Helper()
	done := make(chan struct{})
	go func() { a.updateBackgroundWork.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("profile data writers did not drain")
	}
}

func Test066UpdateDrainDoesNotAdmitLockedGraphWork(t *testing.T) {
	a, genesis := initGraphTestApp(t)
	a.asyncIndex = true
	// A saved development preference must not admit graph work, even while the
	// update barrier closes. Existing graph records are otherwise left intact.
	a.graphStoreMu.Lock()
	a.enqueueGraphIndex(genesis)
	a.stopKnowledgeWritersForUpdate()
	a.graphStoreMu.Unlock()
	waitUpdateWriters064(t, a)
	if got := a.graphBlockHash(0); got != "" {
		t.Fatalf("locked graph accepted a queued commit: %s", got)
	}
	if len(a.graphCoverage()) != 0 {
		t.Fatal("locked graph gained coverage")
	}
	a.enqueueGraphIndex(genesis) // A late canceled producer cannot panic/send to a closed queue.
	waitUpdateWriters064(t, a)
	if err := a.saveCacheIndex(); err != nil {
		t.Fatal(err)
	}
}

func Test064UpdateDrainFlushesDelayedKnowledgeAndStopsNewWriters(t *testing.T) {
	a := &app{dataDir: t.TempDir(), cacheIndex: newCacheIndex()}
	txid := strings.Repeat("1", 64)
	a.cacheIndex.Blocks[txid] = cachedBlockEntry{Hash: txid, Height: 0}
	a.scheduleKnowledgeSave()
	a.stopKnowledgeWritersForUpdate()
	waitUpdateWriters064(t, a)
	b, err := os.ReadFile(a.cacheIndexPath())
	if err != nil {
		t.Fatal(err)
	}
	var stored cacheIndex
	if err = json.Unmarshal(b, &stored); err != nil || stored.Blocks[txid].Hash != txid {
		t.Fatal("accepted delayed knowledge was lost", err)
	}
	late := strings.Repeat("2", 64)
	a.cacheMu.Lock()
	a.cacheIndex.Blocks[late] = cachedBlockEntry{Hash: late, Height: 1}
	a.cacheMu.Unlock()
	a.scheduleKnowledgeSave()
	a.knowledgeMu.Lock()
	pending := a.cacheWritePending
	a.knowledgeMu.Unlock()
	if pending {
		t.Fatal("a delayed data writer was added after the update barrier")
	}
	if err = a.saveCacheIndex(); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(a.cacheIndexPath())
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &stored); err != nil || stored.Blocks[late].Hash != late {
		t.Fatal("final synchronous snapshot lost canceled-producer knowledge", err)
	}
}

func Test064UpdateDrainCancelsCoreMaintenanceAndPreservesSource(t *testing.T) {
	data, blocks := t.TempDir(), t.TempDir()
	source := writeSyntheticCoreBlockFile(t, blocks, testGenesisBlockPayload(t), [8]byte{})
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	a := newCoreMountTestApp(t, data, blocks)
	a.settings.PrepareMountedFiles = true
	a.network = newBitcoinNetwork(a)
	a.refreshCoreBlockStoreAsync()
	waitUpdateWriters064(t, a)
	a.startUpdateAwareWorker(a.coreStoreMaintenanceLoop)
	a.network.stop()
	waitUpdateWriters064(t, a)
	after, err := os.ReadFile(source)
	if err != nil || string(after) != string(before) {
		t.Fatal("update drain modified external Core source", err)
	}
	// A bounded asynchronous scan is included in the same barrier even when the
	// periodic loop observed cancellation before its first tick.
	b, err := os.ReadFile(a.coreStoreManifestPath())
	if err != nil || !json.Valid(b) {
		t.Fatal("Core locator manifest was left partially written")
	}
	// An asynchronous refresh accepted just before cancellation cannot reset
	// the stop flag and begin another complete scan after the shutdown barrier.
	a.coreStoreMu.Lock()
	a.coreScanStop = true
	a.coreStoreMu.Unlock()
	a.refreshCoreBlockStoreAsync()
	waitUpdateWriters064(t, a)
	a.coreStoreMu.RLock()
	stopped := a.coreScanStop
	a.coreStoreMu.RUnlock()
	if !stopped {
		t.Fatal("a queued scan reset the update shutdown stop flag")
	}
}
