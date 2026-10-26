// Package pipeline is an in-memory scheduling experiment, not a verified or durable index engine.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
)

var (
	ErrUnknownConsumer = errors.New("unknown consumer")
	ErrStopped         = errors.New("consumer stopped")
	ErrBackpressure    = errors.New("consumer queue full")
	ErrOrder           = errors.New("unexpected block height")
	ErrHeightOverflow  = errors.New("next block height overflows")
)

// Block owns one immutable copy of raw input shared by consumers. Bytes returns
// a copy, so one callback cannot alter data observed by another callback.
type Block struct {
	height uint64
	body   string
}

func NewBlock(height uint64, body []byte) Block { return Block{height: height, body: string(body)} }
func (b Block) Height() uint64                  { return b.height }
func (b Block) Bytes() []byte                   { return []byte(b.body) }

// Consumer processes a contiguous sequence serially. A callback must cooperate
// with context cancellation. Queue is a finite count of waiting blocks, not bytes.
type Consumer struct {
	Name       string
	NextHeight uint64
	Queue      int
	Process    func(context.Context, Block) error
}

// Status exposes separate accepted and successfully processed cursors. These
// are volatile: neither cursor represents a persisted checkpoint.
type Status struct {
	NextAccepted  uint64
	NextProcessed uint64
	Stopped       bool
	Err           error
}

type worker struct {
	mu      sync.Mutex
	queue   chan Block
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	nextIn  uint64
	nextOut uint64
	stopped bool
	err     error
}

// Pipeline owns a fixed set of independent consumers.
type Pipeline struct {
	workers map[string]*worker
	cancel  context.CancelFunc
	close   sync.Once
}

// New creates workers with finite queues. The caller is responsible for feeding
// relevant heights to each worker, including retries after backpressure.
func New(ctx context.Context, consumers []Consumer) (*Pipeline, error) {
	if ctx == nil {
		return nil, errors.New("nil context")
	}
	seen := make(map[string]bool, len(consumers))
	for _, c := range consumers {
		if c.Name == "" || c.Queue < 1 || c.Process == nil || seen[c.Name] {
			return nil, fmt.Errorf("invalid or duplicate consumer %q", c.Name)
		}
		seen[c.Name] = true
	}
	ctx, cancel := context.WithCancel(ctx)
	p := &Pipeline{workers: make(map[string]*worker, len(consumers)), cancel: cancel}
	for _, c := range consumers {
		workerCtx, workerCancel := context.WithCancel(ctx)
		w := &worker{queue: make(chan Block, c.Queue), ctx: workerCtx, cancel: workerCancel, done: make(chan struct{}), nextIn: c.NextHeight, nextOut: c.NextHeight}
		p.workers[c.Name] = w
		go w.run(workerCtx, c.Process)
	}
	return p, nil
}

func (w *worker) run(ctx context.Context, process func(context.Context, Block) error) {
	defer close(w.done)
	defer func() {
		if recovered := recover(); recovered != nil {
			w.fail(fmt.Errorf("consumer panic: %v", recovered))
		}
		w.mu.Lock()
		w.stopped = true
		w.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			w.fail(ctx.Err())
			return
		case b := <-w.queue:
			if err := ctx.Err(); err != nil {
				w.fail(err)
				return
			}
			if err := process(ctx, b); err != nil {
				w.fail(err)
				return
			}
			w.mu.Lock()
			w.nextOut = b.height + 1 // Offer rejects MaxUint64.
			w.mu.Unlock()
		}
	}
}

func (w *worker) fail(err error) {
	w.mu.Lock()
	if !w.stopped {
		w.stopped = true
		w.err = err
	}
	w.mu.Unlock()
}

// Offer is nonblocking. ErrBackpressure means the block was not accepted and
// must be retried for this consumer before offering its next height. An offer
// accepted before a callback failure may remain unprocessed; use Status.
func (p *Pipeline) Offer(name string, b Block) error {
	w, ok := p.workers[name]
	if !ok {
		return ErrUnknownConsumer
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return ErrStopped
	}
	if err := w.ctx.Err(); err != nil {
		return ErrStopped
	}
	if b.height != w.nextIn {
		return fmt.Errorf("%w: got %d, want %d", ErrOrder, b.height, w.nextIn)
	}
	if b.height == math.MaxUint64 {
		return ErrHeightOverflow
	}
	select {
	case w.queue <- b:
		w.nextIn++
		return nil
	default:
		return ErrBackpressure
	}
}

// Broadcast offers the same block independently. Per-consumer results must be
// inspected: a full or out-of-order queue does not stall other consumers.
func (p *Pipeline) Broadcast(b Block) map[string]error {
	results := make(map[string]error, len(p.workers))
	for name := range p.workers {
		results[name] = p.Offer(name, b)
	}
	return results
}

func (p *Pipeline) Status(name string) (Status, error) {
	w, ok := p.workers[name]
	if !ok {
		return Status{}, ErrUnknownConsumer
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return Status{NextAccepted: w.nextIn, NextProcessed: w.nextOut, Stopped: w.stopped, Err: w.err}, nil
}

// Stop cancels only one consumer and waits for its callback to return.
func (p *Pipeline) Stop(name string) error {
	w, ok := p.workers[name]
	if !ok {
		return ErrUnknownConsumer
	}
	w.fail(context.Canceled)
	w.cancel()
	<-w.done
	return nil
}

// Close cancels and joins all workers. A callback that ignores cancellation can
// delay Close indefinitely; production code must enforce an appropriate policy.
func (p *Pipeline) Close() {
	p.close.Do(func() {
		p.cancel()
		for _, w := range p.workers {
			<-w.done
		}
	})
}
