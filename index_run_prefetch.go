package main

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"
)

// One upcoming block overlaps acquisition/validation with the current block's
// consumers. It is not a disk cache and never invokes a publication operation.
// The existing parser bounds a single serialized block; retaining one result
// avoids accumulating multiple oversized decoded blocks beside the work cache.
type indexRunPrefetch struct {
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	current *indexRunPrefetchEntry
	closed  bool
	slot    chan struct{}
	fetch   func(context.Context, blockTarget) (blockView, error)
}

type indexRunPrefetchEntry struct {
	target  blockTarget
	cancel  context.CancelFunc
	done    chan struct{}
	view    blockView
	err     error
	elapsed time.Duration
}

func newIndexRunPrefetch(ctx context.Context, fetch func(context.Context, blockTarget) (blockView, error)) *indexRunPrefetch {
	ctx, cancel := context.WithCancel(ctx)
	return &indexRunPrefetch{ctx: ctx, cancel: cancel, slot: make(chan struct{}, 1), fetch: fetch}
}

func (p *indexRunPrefetch) Schedule(target blockTarget) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.ctx.Err() != nil || p.current != nil || p.fetch == nil {
		return false
	}
	select {
	case p.slot <- struct{}{}:
	default:
		return false
	}
	ctx, cancel := context.WithCancel(p.ctx)
	// The caller may reuse its header buffer after returning.
	target.ExpectedHeader = append([]byte(nil), target.ExpectedHeader...)
	e := &indexRunPrefetchEntry{target: target, cancel: cancel, done: make(chan struct{})}
	p.current = e
	go func() {
		defer func() { <-p.slot; close(e.done) }()
		started := time.Now()
		e.view, e.err = p.fetch(ctx, e.target)
		e.elapsed = time.Since(started)
		if e.err == nil {
			e.err = ctx.Err()
		}
		if e.err == nil && (e.view.Height != target.Height || e.view.Hash != target.HashDisplay || e.view.SerializedBytes > maxMessageSize || !integrityVerified(e.view)) {
			e.err = fmt.Errorf("prefetched block does not match verified selected target")
		}
		if e.err != nil {
			e.view = blockView{}
		}
	}()
	return true
}

// Take returns matched=false when this block was not queued. A matched result
// is consumed once. The caller must still reauthenticate its selected branch
// and current intent immediately before every consumer publication.
func (p *indexRunPrefetch) Take(ctx context.Context, target blockTarget) (blockView, time.Duration, bool, error) {
	p.mu.Lock()
	e := p.current
	if e == nil || e.target.Height != target.Height || e.target.HashDisplay != target.HashDisplay || !bytes.Equal(e.target.ExpectedHeader, target.ExpectedHeader) {
		p.mu.Unlock()
		return blockView{}, 0, false, nil
	}
	p.mu.Unlock()
	select {
	case <-ctx.Done():
		p.Reset()
		return blockView{}, 0, true, ctx.Err()
	case <-e.done:
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current != e || p.closed || p.ctx.Err() != nil {
		return blockView{}, e.elapsed, true, context.Canceled
	}
	p.current = nil
	e.cancel()
	return e.view, e.elapsed, true, e.err
}

func (p *indexRunPrefetch) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current != nil {
		p.current.cancel()
		p.current = nil
	}
}

func (p *indexRunPrefetch) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.cancel()
	if p.current != nil {
		p.current.cancel()
		p.current = nil
	}
}
