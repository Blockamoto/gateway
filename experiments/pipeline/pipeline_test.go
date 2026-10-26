package pipeline

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func receive(t *testing.T, ch <-chan uint64, want uint64) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("received height %d, want %d", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for height %d", want)
	}
}

func eventually(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func TestBoundedBackpressureAndIndependentProgress(t *testing.T) {
	slowStarted := make(chan uint64, 3)
	fastDone := make(chan uint64, 3)
	release := make(chan struct{})
	p, err := New(context.Background(), []Consumer{
		{Name: "slow", Queue: 1, Process: func(ctx context.Context, b Block) error {
			slowStarted <- b.Height()
			if b.Height() == 0 {
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		}},
		{Name: "fast", Queue: 1, Process: func(_ context.Context, b Block) error {
			fastDone <- b.Height()
			return nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if got := p.Broadcast(NewBlock(0, []byte("block zero"))); got["slow"] != nil || got["fast"] != nil {
		t.Fatal(got)
	}
	receive(t, slowStarted, 0)
	receive(t, fastDone, 0)
	if got := p.Broadcast(NewBlock(1, nil)); got["slow"] != nil || got["fast"] != nil {
		t.Fatal(got)
	}
	receive(t, fastDone, 1)
	result := p.Broadcast(NewBlock(2, nil))
	if !errors.Is(result["slow"], ErrBackpressure) || result["fast"] != nil {
		t.Fatalf("unexpected result: %v", result)
	}
	receive(t, fastDone, 2)
	if err := p.Offer("slow", NewBlock(3, nil)); !errors.Is(err, ErrOrder) {
		t.Fatalf("later height: %v", err)
	}
	s, _ := p.Status("slow")
	if s.NextAccepted != 2 || s.NextProcessed != 0 {
		t.Fatalf("slow cursor: %+v", s)
	}
	close(release)
	receive(t, slowStarted, 1)
	eventually(t, func() bool { s, _ := p.Status("slow"); return s.NextProcessed == 2 })
	if err := p.Offer("slow", NewBlock(2, nil)); err != nil {
		t.Fatal(err)
	}
	receive(t, slowStarted, 2)
	eventually(t, func() bool { s, _ := p.Status("slow"); return s.NextProcessed == 3 })
	eventually(t, func() bool { s, _ := p.Status("fast"); return s.NextProcessed == 3 })
}

func TestFailureAndCancellationAreIsolated(t *testing.T) {
	badStarted := make(chan uint64, 1)
	releaseBad := make(chan struct{})
	goodDone := make(chan uint64, 2)
	boom := errors.New("decode failure")
	p, err := New(context.Background(), []Consumer{
		{Name: "bad", Queue: 1, Process: func(_ context.Context, b Block) error { badStarted <- b.Height(); <-releaseBad; return boom }},
		{Name: "good", Queue: 2, Process: func(_ context.Context, b Block) error { goodDone <- b.Height(); return nil }},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if got := p.Broadcast(NewBlock(0, nil)); got["bad"] != nil || got["good"] != nil {
		t.Fatal(got)
	}
	receive(t, badStarted, 0)
	if err := p.Offer("bad", NewBlock(1, nil)); err != nil {
		t.Fatal(err)
	}
	close(releaseBad)
	eventually(t, func() bool { s, _ := p.Status("bad"); return s.Stopped })
	s, _ := p.Status("bad")
	if !errors.Is(s.Err, boom) || s.NextAccepted != 2 || s.NextProcessed != 0 {
		t.Fatalf("failure cursors: %+v", s)
	}
	if err := p.Offer("bad", NewBlock(2, nil)); !errors.Is(err, ErrStopped) {
		t.Fatal(err)
	}
	if err := p.Offer("good", NewBlock(1, nil)); err != nil {
		t.Fatal(err)
	}
	receive(t, goodDone, 0)
	receive(t, goodDone, 1)
	eventually(t, func() bool { s, _ := p.Status("good"); return s.NextProcessed == 2 })
	if err := p.Stop("good"); err != nil {
		t.Fatal(err)
	}
	if err := p.Offer("good", NewBlock(2, nil)); !errors.Is(err, ErrStopped) {
		t.Fatal(err)
	}
	if err := p.Stop("bad"); err != nil {
		t.Fatal(err)
	}
	s, _ = p.Status("bad")
	if !errors.Is(s.Err, boom) {
		t.Fatalf("stop overwrote original error: %v", s.Err)
	}
}

func TestCancelInFlightAndRecoverPanic(t *testing.T) {
	started := make(chan uint64, 1)
	p, err := New(context.Background(), []Consumer{
		{Name: "cancel", Queue: 1, Process: func(ctx context.Context, b Block) error { started <- b.Height(); <-ctx.Done(); return ctx.Err() }},
		{Name: "panic", Queue: 1, Process: func(context.Context, Block) error { panic("broken consumer") }},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if got := p.Broadcast(NewBlock(0, nil)); got["cancel"] != nil || got["panic"] != nil {
		t.Fatal(got)
	}
	receive(t, started, 0)
	if err := p.Stop("cancel"); err != nil {
		t.Fatal(err)
	}
	s, _ := p.Status("cancel")
	if s.NextProcessed != 0 || !errors.Is(s.Err, context.Canceled) {
		t.Fatalf("canceled checkpoint: %+v", s)
	}
	eventually(t, func() bool { s, _ := p.Status("panic"); return s.Stopped })
	s, _ = p.Status("panic")
	if s.Err == nil || !strings.Contains(s.Err.Error(), "broken consumer") || s.NextProcessed != 0 {
		t.Fatalf("panic status: %+v", s)
	}
}

func TestBlockOwnsBytesAndInputValidation(t *testing.T) {
	original := []byte("shared")
	b := NewBlock(7, original)
	original[0] = 'X'
	copy := b.Bytes()
	copy[0] = 'Y'
	if !reflect.DeepEqual(b.Bytes(), []byte("shared")) {
		t.Fatal("shared block mutated")
	}
	if _, err := New(context.Background(), []Consumer{{Name: "x", Queue: 0}}); err == nil {
		t.Fatal("accepted invalid worker")
	}
	process := func(context.Context, Block) error { return nil }
	if _, err := New(context.Background(), []Consumer{{Name: "x", Queue: 1, Process: process}, {Name: "x", Queue: 1, Process: process}}); err == nil {
		t.Fatal("accepted duplicate worker")
	}
	p, err := New(context.Background(), []Consumer{{Name: "x", NextHeight: 7, Queue: 1, Process: process}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Offer("missing", b); !errors.Is(err, ErrUnknownConsumer) {
		t.Fatal(err)
	}
	if err := p.Offer("x", NewBlock(8, nil)); !errors.Is(err, ErrOrder) {
		t.Fatal(err)
	}
	if err := p.Offer("x", b); err != nil {
		t.Fatal(err)
	}
	if err := p.Offer("x", b); !errors.Is(err, ErrOrder) {
		t.Fatal(err)
	}
}
