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
	done        chan struct{}
	v           blockView
	e           error
	cancel      context.CancelFunc
	waiters     int
	finished    bool
	fetch       func(context.Context) (blockView, error)
	settingsKey string
}

func (a *app) coalescedBlock(t blockTarget) (blockView, error) {
	return a.coalescedBlockContext(context.Background(), t)
}

// Compatibility entry point. Every source caller now enters the same base
// acquisition inside fetchBlockWithPolicyContext; an outer Explorer cache would
// bypass per-caller evidence and the current Raw retention intent.
func (a *app) coalescedBlockContext(ctx context.Context, t blockTarget) (blockView, error) {
	return a.fetchBlockWithPolicyContext(ctx, t, "legacy")
}

func blockObjectKey(t blockTarget, settings appSettings) string {
	return fmt.Sprintf("mainnet:%s:%x:%d:%s:%t:%t:%x:%d:%s", t.HashDisplay, t.HashRaw, t.Height, t.ChainAuthority, t.ConsensusAuthority, t.LocalHeader, t.ExpectedHeader, blockVerifierVersion, indexDigest(settings))
}

func (a *app) coalescedBlockBaseContext(ctx context.Context, t blockTarget, settings appSettings, fetch func(context.Context) (blockView, error)) (blockView, error) {
	if err := ctx.Err(); err != nil {
		return blockView{}, err
	}
	if a.blockBaseAcquire != nil {
		fetch = func(work context.Context) (blockView, error) { return a.blockBaseAcquire(work, t, settings) }
	}
	key := blockObjectKey(t, settings)
	a.objectsMu.Lock()
	if a.objects == nil {
		a.objects = map[string]*objectFetch{}
	}
	if f := a.objects[key]; f != nil {
		f.waiters++
		a.objectsMu.Unlock()
		return a.waitForBlockObject(ctx, key, f)
	}
	work, cancel := context.WithCancel(context.Background())
	f := &objectFetch{done: make(chan struct{}), cancel: cancel, waiters: 1, fetch: fetch, settingsKey: indexDigest(settings)}
	a.objects[key] = f
	a.objectsMu.Unlock()
	go a.fetchBlockObject(work, key, f, t)
	return a.waitForBlockObject(ctx, key, f)
}

func (a *app) waitForBlockObject(ctx context.Context, key string, f *objectFetch) (blockView, error) {
	defer func() {
		a.objectsMu.Lock()
		f.waiters--
		if f.waiters == 0 && a.objects[key] == f {
			delete(a.objects, key)
			if f.cancel != nil {
				f.cancel()
			}
		}
		a.objectsMu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return blockView{}, ctx.Err()
	case <-f.done:
	}
	if err := ctx.Err(); err != nil {
		return blockView{}, err
	}
	if f.e != nil {
		return blockView{}, f.e
	}
	return cloneAcquiredBlock(f.v), nil
}

func (a *app) blockSourceSettingsKey() string {
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	return indexDigest(settings)
}

func (a *app) fetchBlockObject(ctx context.Context, key string, f *objectFetch, t blockTarget) {
	if f.cancel != nil {
		defer f.cancel()
	}
	if err := ctx.Err(); err != nil {
		f.e = err
	} else if f.settingsKey != "" && a.blockSourceSettingsKey() != f.settingsKey {
		f.e = blockUnavailableError{fmt.Errorf("block source settings changed; retry the request")}
	} else if f.fetch == nil {
		f.e = fmt.Errorf("missing base block acquisition")
	} else {
		f.v, f.e = f.fetch(ctx)
		if f.e == nil {
			f.e = ctx.Err()
		}
		if f.e == nil && a.blockSourceSettingsKey() != f.settingsKey {
			f.e = blockUnavailableError{fmt.Errorf("block source settings changed; retry the request")}
		}
	}
	a.objectsMu.Lock()
	f.finished = true
	close(f.done)
	if f.waiters == 0 && a.objects[key] == f {
		delete(a.objects, key)
	}
	a.objectsMu.Unlock()
}

// Parsed body slices and private byte receipts are owned by each caller. This
// allows resource decoration without changing another consumer's exact proof.
func cloneAcquiredBlock(v blockView) blockView {
	out := v
	out.verifiedRaw = append([]byte(nil), v.verifiedRaw...)
	if v.verifiedRawProof != nil {
		proof := *v.verifiedRawProof
		out.verifiedRawProof = &proof
	}
	if v.Evidence != nil {
		evidence := *v.Evidence
		out.Evidence = &evidence
	}
	out.Transactions = append([]transactionView(nil), v.Transactions...)
	for i := range out.Transactions {
		tx := &out.Transactions[i]
		tx.Inputs = append([]inputView(nil), tx.Inputs...)
		for j := range tx.Inputs {
			tx.Inputs[j].Witness = append([]string(nil), tx.Inputs[j].Witness...)
		}
		tx.Outputs = append([]outputView(nil), tx.Outputs...)
	}
	return out
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
	if !a.legacyKnowledgeEnabled() {
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
