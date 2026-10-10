package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

var coreTransport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext, MaxIdleConns: 32, MaxIdleConnsPerHost: 16, MaxConnsPerHost: 16, IdleConnTimeout: 90 * time.Second}
var coreStatusCache = struct {
	sync.Mutex
	entries map[string]coreStatusEntry
}{entries: map[string]coreStatusEntry{}}

type coreStatusEntry struct {
	at    time.Time
	value coreStatus
}

func inspectCore(s appSettings) coreStatus {
	b, _ := json.Marshal([]any{s.CoreDisabled, s.BitcoinDataDir, s.RPCPort, s.RPCAuthMode, s.RPCUser, s.RPCPassword})
	key := string(b)
	coreStatusCache.Lock()
	x, ok := coreStatusCache.entries[key]
	coreStatusCache.Unlock()
	ttl := 2 * time.Second
	if !x.value.Connected {
		ttl = 500 * time.Millisecond
	}
	if ok && time.Since(x.at) < ttl {
		return x.value
	}
	value := inspectCoreFresh(s)
	coreStatusCache.Lock()
	if len(coreStatusCache.entries) > 32 {
		coreStatusCache.entries = map[string]coreStatusEntry{}
	}
	coreStatusCache.entries[key] = coreStatusEntry{time.Now(), value}
	coreStatusCache.Unlock()
	return value
}

type objectFetch struct {
	done    chan struct{}
	v       blockView
	e       error
	cancel  context.CancelFunc
	waiters int
}

func (a *app) coalescedBlock(t blockTarget) (blockView, error) {
	return a.coalescedBlockContext(context.Background(), t)
}

func (a *app) coalescedBlockContext(ctx context.Context, t blockTarget) (blockView, error) {
	if err := ctx.Err(); err != nil {
		return blockView{}, err
	}
	// Key includes anchor authority. Reorg membership is checked by target resolution,
	// never inferred from an old decoded object.
	key := fmt.Sprintf("%s:%d:%s:%t:%x", t.HashDisplay, t.Height, t.ChainAuthority, t.ConsensusAuthority, t.ExpectedHeader)
	a.objectsMu.Lock()
	if a.objects == nil {
		a.objects = map[string]*objectFetch{}
		a.decoded = map[string]blockView{}
	}
	if v, ok := a.decoded[key]; ok {
		a.objectsMu.Unlock()
		return v, nil
	}
	if f, ok := a.objects[key]; ok {
		f.waiters++
		a.objectsMu.Unlock()
		return a.waitForBlockObject(ctx, key, f)
	}
	// Work belongs to the set of consumers, rather than the first caller's
	// request. An obsolete viewer cannot cancel a still-interested index job.
	work, cancel := context.WithCancel(context.Background())
	f := &objectFetch{done: make(chan struct{}), cancel: cancel, waiters: 1}
	a.objects[key] = f
	a.objectsMu.Unlock()
	go a.fetchBlockObject(work, key, f, t)
	return a.waitForBlockObject(ctx, key, f)
}

func (a *app) waitForBlockObject(ctx context.Context, key string, f *objectFetch) (blockView, error) {
	select {
	case <-ctx.Done():
		a.objectsMu.Lock()
		f.waiters--
		if f.waiters == 0 && a.objects[key] == f {
			delete(a.objects, key)
			f.cancel()
		}
		a.objectsMu.Unlock()
		return blockView{}, ctx.Err()
	case <-f.done:
		return f.v, f.e
	}
}

func (a *app) fetchBlockObject(ctx context.Context, key string, f *objectFetch, t blockTarget) {
	defer f.cancel()
	f.v, f.e = a.fetchAndDecodeTargetUncachedContext(ctx, t)
	a.objectsMu.Lock()
	// The final waiter may already have abandoned this object and a fresh
	// consumer may have started a replacement. Never delete that new fetch.
	if a.objects[key] == f {
		delete(a.objects, key)
	}
	if f.e == nil && ctx.Err() == nil {
		a.decoded[key] = f.v
		a.decodedOrder = append(a.decodedOrder, key)
		for len(a.decodedOrder) > 4 {
			delete(a.decoded, a.decodedOrder[0])
			a.decodedOrder = a.decodedOrder[1:]
		}
	}
	close(f.done)
	a.objectsMu.Unlock()
}
func (a *app) scheduleKnowledgeSave() {
	a.knowledgeMu.Lock()
	if a.cacheWritePending || a.knowledgeClosing {
		a.knowledgeMu.Unlock()
		return
	}
	a.cacheWritePending = true
	// Register while holding the admission lock: shutdown cannot close the
	// barrier and finish its Wait before this accepted save has been counted.
	a.startUpdateAwareWorker(func() {
		time.Sleep(500 * time.Millisecond)
		a.knowledgeMu.Lock()
		a.cacheWritePending = false
		a.knowledgeMu.Unlock()
		_ = a.saveCacheIndex()
	})
	a.knowledgeMu.Unlock()
}

// Ordinary UI reads do not wait for hundreds of graph-shard fsyncs. The decoded
// block is queued without refetching; coverage advances only after graph commit.
// A full queue never invents coverage: the block can be indexed by an explicit build.
func (a *app) enqueueGraphIndex(v blockView) {
	if !releaseFeatureAvailable("txo-spender") {
		return
	}
	if !a.asyncIndex {
		_ = a.graphIndexBlock(v, false)
		return
	}
	a.knowledgeMu.Lock()
	if a.knowledgeClosing {
		delete(a.knowledgeSeen, v.Hash)
		a.knowledgeMu.Unlock()
		return
	}
	if a.ingest == nil {
		a.ingest = make(chan blockView, 2)
		queue := a.ingest
		a.startUpdateAwareWorker(func() {
			for block := range queue {
				if e := a.graphIndexBlock(block, false); e != nil {
					a.knowledgeMu.Lock()
					delete(a.knowledgeSeen, block.Hash)
					a.knowledgeMu.Unlock()
				}
			}
		})
	}
	select {
	case a.ingest <- v:
	default:
		delete(a.knowledgeSeen, v.Hash)
	}
	a.knowledgeMu.Unlock()
}

// The update barrier closes admissions first. Drain queued graph commits and
// track delayed cache writes before the final cache snapshot and process exit.
// Post-cancellation producer work can still update memory; the final synchronous
// save preserves it without adding a new delayed writer after the barrier.
func (a *app) stopKnowledgeWritersForUpdate() {
	a.knowledgeMu.Lock()
	defer a.knowledgeMu.Unlock()
	if a.knowledgeClosing {
		return
	}
	a.knowledgeClosing = true
	if a.ingest != nil {
		close(a.ingest)
	}
}
