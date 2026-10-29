package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestBlockPeerChoiceCapacityRefillWithoutSpeedRanking(t *testing.T) {
	n := &bitcoinNetwork{sessions: map[string]*bitcoinSession{}}
	for _, addr := range []string{"a", "b"} {
		n.sessions[addr] = &bitcoinSession{n: n, peer: &peerConn{addr: addr, services: nodeNetworkService}, slot: make(chan struct{}, 1), view: networkPeerView{State: "connected"}}
	}
	a, b := n.sessions["a"], n.sessions["b"]
	a.blockWork.successes = 1
	a.blockWork.latency = time.Hour
	b.blockWork.successes = 1
	b.blockWork.latency = time.Microsecond
	for i := 0; i < 2*coreBlocksPerPeer; i++ {
		if n.reserveBlockSession(nil) == nil {
			t.Fatal("capacity unavailable", i)
		}
	}
	if a.blockWork.active != coreBlocksPerPeer || b.blockWork.active != coreBlocksPerPeer || n.reserveBlockSession(nil) != nil {
		t.Fatal("per-peer limit not enforced")
	}
	n.finishBlockSession(a, time.Second, true, false)
	if n.reserveBlockSession(nil) != a {
		t.Fatal("free capacity was not refilled independently of speed")
	}
	n.finishBlockSession(b, 0, false, true)
	if n.reserveBlockSession(map[string]bool{"b": true}) != nil {
		t.Fatal("tried peer selected again")
	}
	if !b.blockWork.retryAfter.IsZero() {
		t.Fatal("custom cooldown reintroduced")
	}
}
func TestBlockPeerNotFoundFallsBackAndNextBlockSkipsFailedServer(t *testing.T) {
	a := independentTestApp(t)
	badLocal, badRemote := net.Pipe()
	goodLocal, goodRemote := net.Pipe()
	t.Cleanup(func() { badRemote.Close(); goodRemote.Close() })
	bad := sessionRepairManaged(t, a, badLocal, "127.0.0.1:60310")
	good := sessionRepairManaged(t, a, goodLocal, "127.0.0.1:60311")
	a.network.mu.Lock()
	bad.peer.services, good.peer.services = nodeNetworkService|nodeWitnessService, nodeNetworkService|nodeWitnessService
	bad.view.Connected, good.view.Connected = time.Now().Add(-time.Hour), time.Now()
	a.network.mu.Unlock()
	raw := make([]byte, 81)
	raw[0] = 1
	hash := hash256(raw[:80])
	var badCalls, goodCalls atomic.Int32
	completed := make(chan error, 2)
	go func() {
		m, err := readMessage(badRemote)
		if err == nil && m.command != "getdata" {
			err = fmt.Errorf("unexpected command %s", m.command)
		}
		if err == nil {
			badCalls.Add(1)
			err = writeMessage(badRemote, "notfound", m.payload)
		}
		completed <- err
	}()
	go func() {
		var err error
		for i := 0; i < 2; i++ {
			var m message
			m, err = readMessage(goodRemote)
			if err != nil {
				break
			}
			if m.command != "getdata" {
				err = fmt.Errorf("unexpected command %s", m.command)
				break
			}
			goodCalls.Add(1)
			if err = writeMessage(goodRemote, "block", raw); err != nil {
				break
			}
		}
		completed <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		got, provider, err := a.network.fetchBlockContext(ctx, hash, raw[:80])
		if err != nil || provider != good.peer.addr || !bytes.Equal(got, raw) {
			t.Fatalf("fetch %d: %s %v", i, provider, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := <-completed; err != nil {
			t.Fatal(err)
		}
	}
	if badCalls.Load() != 1 || goodCalls.Load() != 2 {
		t.Fatal("failed first server was retried", badCalls.Load(), goodCalls.Load())
	}
}

func TestBlockPeerChoiceCancellationDoesNotPoisonServing(t *testing.T) {
	n := &bitcoinNetwork{sessions: map[string]*bitcoinSession{}}
	s := &bitcoinSession{n: n, peer: &peerConn{addr: "peer", services: nodeNetworkService}, slot: make(chan struct{}, 1)}
	n.sessions["peer"] = s
	if n.reserveBlockSession(nil) != s {
		t.Fatal("missing provider")
	}
	n.finishBlockSession(s, time.Millisecond, false, false)
	if s.blockWork.failures != 0 || !s.blockWork.retryAfter.IsZero() || s.blockWork.active != 0 {
		t.Fatal("caller cancellation penalized peer")
	}
	if n.reserveBlockSession(map[string]bool{"peer": true}) != nil {
		t.Fatal("tried peer selected twice")
	}
}

func TestBlockPeerLocalQueueTimeoutIsNotDispatchedOrPenalized(t *testing.T) {
	for _, busy := range []string{"request_slot", "writer"} {
		t.Run(busy, func(t *testing.T) {
			a, s, _ := sessionRepairRaw(t)
			a.network.mu.Lock()
			s.peer.services = nodeNetworkService
			a.network.mu.Unlock()
			if a.network.reserveBlockSession(nil) != s {
				t.Fatal("missing block session reservation")
			}
			if busy == "request_slot" {
				s.slot <- struct{}{}
				defer func() { <-s.slot }()
			} else {
				s.writeMu.Lock()
				defer s.writeMu.Unlock()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
			defer cancel()
			raw := make([]byte, 81)
			var dispatched time.Time
			_, err := s.requestWithDispatch(ctx, "getdata", sessionRepairBlockRequest(raw), func(message) bool { return true }, &dispatched)
			if !errors.Is(err, context.DeadlineExceeded) || !dispatched.IsZero() {
				t.Fatalf("local %s timeout counted as sent: %v, %v", busy, dispatched, err)
			}
			a.network.finishBlockSession(s, 0, false, !dispatched.IsZero())
			if s.blockWork.active != 0 || s.blockWork.failures != 0 || !s.blockWork.retryAfter.IsZero() {
				t.Fatal("local contention penalized the peer")
			}
		})
	}
}
