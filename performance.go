package main

import (
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
	done chan struct{}
	v    blockView
	e    error
}

func (a *app) coalescedBlock(t blockTarget) (blockView, error) {
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
		a.objectsMu.Unlock()
		<-f.done
		return f.v, f.e
	}
	f := &objectFetch{done: make(chan struct{})}
	a.objects[key] = f
	a.objectsMu.Unlock()
	f.v, f.e = a.fetchAndDecodeTargetUncached(t)
	a.objectsMu.Lock()
	delete(a.objects, key)
	if f.e == nil {
		a.decoded[key] = f.v
		a.decodedOrder = append(a.decodedOrder, key)
		for len(a.decodedOrder) > 4 {
			delete(a.decoded, a.decodedOrder[0])
			a.decodedOrder = a.decodedOrder[1:]
		}
	}
	close(f.done)
	a.objectsMu.Unlock()
	return f.v, f.e
}
func (a *app) scheduleKnowledgeSave() {
	a.knowledgeMu.Lock()
	if a.cacheWritePending {
		a.knowledgeMu.Unlock()
		return
	}
	a.cacheWritePending = true
	a.knowledgeMu.Unlock()
	go func() {
		time.Sleep(500 * time.Millisecond)
		a.knowledgeMu.Lock()
		a.cacheWritePending = false
		a.knowledgeMu.Unlock()
		_ = a.saveCacheIndex()
	}()
}

// Ordinary UI reads do not wait for hundreds of graph-shard fsyncs. The decoded
// block is queued without refetching; coverage advances only after graph commit.
// A full queue never invents coverage: the block can be indexed by an explicit build.
func (a *app) enqueueGraphIndex(v blockView) {
	if !a.asyncIndex {
		_ = a.graphIndexBlock(v, false)
		return
	}
	a.knowledgeMu.Lock()
	if a.ingest == nil {
		a.ingest = make(chan blockView, 2)
		queue := a.ingest
		go func() {
			for block := range queue {
				if e := a.graphIndexBlock(block, false); e != nil {
					a.knowledgeMu.Lock()
					delete(a.knowledgeSeen, block.Hash)
					a.knowledgeMu.Unlock()
				}
			}
		}()
	}
	select {
	case a.ingest <- v:
	default:
		delete(a.knowledgeSeen, v.Hash)
	}
	a.knowledgeMu.Unlock()
}
