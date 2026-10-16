package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func prefetchTestBlock(t *testing.T) (blockTarget, blockView) {
	t.Helper()
	raw := testGenesisBlockPayload(t)
	v, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := displayHashRaw(genesisHashDisplay)
	return blockTarget{Height: 0, HashDisplay: genesisHashDisplay, HashRaw: hash, ExpectedHeader: append([]byte(nil), raw[:80]...)}, v
}

func TestIndexRunPrefetchSharesExactResultAndBoundsQueue(t *testing.T) {
	target, v := prefetchTestBlock(t)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	p := newIndexRunPrefetch(context.Background(), func(ctx context.Context, got blockTarget) (blockView, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return blockView{}, ctx.Err()
		}
		if got.HashDisplay != target.HashDisplay {
			return blockView{}, errors.New("wrong target")
		}
		return v, nil
	})
	defer p.Close()
	if !p.Schedule(target) {
		t.Fatal("not scheduled")
	}
	<-started
	if p.Schedule(target) {
		t.Fatal("duplicate acquisition")
	}
	other := target
	other.Height++
	if p.Schedule(other) {
		t.Fatal("queue exceeded one upcoming block")
	}
	if _, _, matched, _ := p.Take(context.Background(), other); matched {
		t.Fatal("wrong block consumed result")
	}
	close(release)
	got, elapsed, matched, err := p.Take(context.Background(), target)
	if err != nil || !matched || got.Hash != v.Hash || elapsed < 0 || calls.Load() != 1 {
		t.Fatalf("prefetch result: %v %v %v", matched, elapsed, err)
	}
	if _, _, matched, _ := p.Take(context.Background(), target); matched {
		t.Fatal("result consumed twice")
	}
}

func TestIndexRunPrefetchResetCannotReviveOldBranch(t *testing.T) {
	target, v := prefetchTestBlock(t)
	started, finished := make(chan struct{}), make(chan struct{})
	p := newIndexRunPrefetch(context.Background(), func(ctx context.Context, _ blockTarget) (blockView, error) {
		close(started)
		<-ctx.Done()
		defer close(finished)
		return v, nil
	})
	defer p.Close()
	if !p.Schedule(target) {
		t.Fatal("not scheduled")
	}
	<-started
	p.Reset()
	<-finished
	if _, _, matched, _ := p.Take(context.Background(), target); matched {
		t.Fatal("reset branch returned old body")
	}
	p.Close()
	if p.Schedule(target) {
		t.Fatal("closed pipeline scheduled work")
	}
}

func TestIndexRunPrefetchCancellationAndWrongSource(t *testing.T) {
	target, v := prefetchTestBlock(t)
	p := newIndexRunPrefetch(context.Background(), func(context.Context, blockTarget) (blockView, error) { v.Height = 1; return v, nil })
	defer p.Close()
	p.Schedule(target)
	if got, _, matched, err := p.Take(context.Background(), target); !matched || err == nil || got.Hash != "" {
		t.Fatal("mismatched source accepted")
	}
	started := make(chan struct{})
	p2 := newIndexRunPrefetch(context.Background(), func(ctx context.Context, _ blockTarget) (blockView, error) {
		close(started)
		<-ctx.Done()
		return blockView{}, ctx.Err()
	})
	defer p2.Close()
	p2.Schedule(target)
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, _, matched, err := p2.Take(ctx, target); !matched || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("consumer cancellation ignored", err)
	}
}
