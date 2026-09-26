package main

import (
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the real address decoder/read loop without opening a TCP listener.
// The clock advances only when the test asks; no maintenance run may dial.
func newScoutRepairSession(t *testing.T) (*bitcoinNetwork, *bitcoinSession, net.Conn, *int64) {
	t.Helper()
	a := independentTestApp(t)
	n := a.network
	clock := new(int64)
	atomic.StoreInt64(clock, time.Now().UTC().UnixNano())
	n.clock = func() time.Time { return time.Unix(0, atomic.LoadInt64(clock)).UTC() }
	n.tokens = 0
	n.tokenAt = n.now().Add(24 * time.Hour)
	local, remote := net.Pipe()
	s := &bitcoinSession{
		n: n, peer: &peerConn{addr: "127.0.0.1:60114", conn: local},
		scout: true, addrAsked: true,
		done: make(chan struct{}), slot: make(chan struct{}, 1),
		serveQueue: make(chan message, 4),
		view:       networkPeerView{State: "connected", Role: "gateway_search", Connected: n.now()},
	}
	n.add(s.peer.addr, "test", nodeNetworkService|nodeWitnessService)
	n.mu.Lock()
	n.sessions[s.peer.addr] = s
	n.mu.Unlock()
	readerDone := make(chan struct{})
	go func() { defer close(readerDone); s.readLoop() }()
	t.Cleanup(func() {
		s.close("test cleanup")
		remote.Close()
		select {
		case <-readerDone:
		case <-time.After(3 * time.Second):
			t.Error("scout reader did not stop")
		}
	})
	return n, s, remote, clock
}

func sendScoutRepairAddresses(t *testing.T, remote net.Conn, rows []advertisedPeer, v2 bool) {
	t.Helper()
	if err := remote.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	defer remote.SetDeadline(time.Time{})
	command, payload := addressMessage(rows, v2)
	if err := writeMessage(remote, command, payload); err != nil {
		t.Fatal(err)
	}
	// Pong is a processing barrier: the preceding addresses have been learned
	// and persisted before maintenance decides whether this visit is complete.
	if err := writeMessage(remote, "ping", make([]byte, 8)); err != nil {
		t.Fatal(err)
	}
	m, err := readMessage(remote)
	if err != nil || m.command != "pong" {
		t.Fatalf("address processing barrier: command=%q err=%v", m.command, err)
	}
}

func requireScoutRepairOpen(t *testing.T, s *bitcoinSession) {
	t.Helper()
	select {
	case <-s.done:
		t.Fatal("scout closed before a multi-address sample or its probe deadline")
	default:
	}
}

func requireScoutRepairRetired(t *testing.T, n *bitcoinNetwork, s *bitcoinSession) {
	t.Helper()
	select {
	case <-s.done:
	default:
		t.Fatal("completed scout did not close")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	x := n.ledger[s.peer.addr]
	if !s.retired || x == nil || x.Failures != 0 || x.LastError != "" {
		t.Fatalf("scheduled retirement became a failure: retired=%v ledger=%+v", s.retired, x)
	}
	if !x.RetryAfter.Equal(n.now().Add(n.policy.RevisitAfter)) {
		t.Fatalf("retirement revisit=%v, want %v", x.RetryAfter, n.now().Add(n.policy.RevisitAfter))
	}
}

func TestDiscoveryScoutWaitsPastSingletonForDelayedSample(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		name := "addr"
		if v2 {
			name = "addrv2"
		}
		t.Run(name, func(t *testing.T) {
			n, s, remote, clock := newScoutRepairSession(t)
			started := n.now()
			announcement := advertisedPeer{Addr: s.peer.addr, Services: nodeNetworkService | nodeWitnessService}
			// Two separate self-announcements are still ambiguous; they must not
			// be accumulated into an apparent two-address getaddr response.
			for i := 0; i < 2; i++ {
				atomic.StoreInt64(clock, started.Add(time.Duration(i)*time.Second).UnixNano())
				sendScoutRepairAddresses(t, remote, []advertisedPeer{announcement}, v2)
				n.maintain()
				requireScoutRepairOpen(t, s)
			}
			atomic.StoreInt64(clock, started.Add(2*time.Second).UnixNano())
			rows := []advertisedPeer{
				{Addr: "127.0.0.1:60115", Services: nodeNetworkService | nodeWitnessService},
				{Addr: "127.0.0.1:60116", Services: gatewayExperimentalServiceBit | nodeNetworkService | nodeWitnessService},
			}
			sendScoutRepairAddresses(t, remote, rows, v2)
			n.maintain()
			requireScoutRepairRetired(t, n, s)
			n.mu.Lock()
			defer n.mu.Unlock()
			for _, row := range rows {
				if n.ledger[row.Addr] == nil {
					t.Errorf("delayed sample lost address %s", row.Addr)
				}
			}
			if x := n.ledger[rows[1].Addr]; x == nil || !x.EverHint {
				t.Error("delayed Gateway hint was not retained")
			}
		})
	}
}

func TestDiscoveryScoutAmbiguousRepliesKeepOriginalDeadline(t *testing.T) {
	for _, reply := range []string{"none", "empty", "singleton", "repeated_singletons"} {
		t.Run(reply, func(t *testing.T) {
			n, s, remote, clock := newScoutRepairSession(t)
			started := n.now()
			row := advertisedPeer{Addr: "127.0.0.1:60115", Services: gatewayExperimentalServiceBit}
			for i, elapsed := range []time.Duration{0, n.policy.ProbeWindow / 2, n.policy.ProbeWindow - time.Nanosecond} {
				atomic.StoreInt64(clock, started.Add(elapsed).UnixNano())
				if reply == "repeated_singletons" || (reply == "singleton" && i == 0) {
					sendScoutRepairAddresses(t, remote, []advertisedPeer{row}, false)
				} else if reply == "empty" && i == 0 {
					sendScoutRepairAddresses(t, remote, nil, false)
				}
				n.maintain()
				requireScoutRepairOpen(t, s)
			}
			atomic.StoreInt64(clock, started.Add(n.policy.ProbeWindow+time.Nanosecond).UnixNano())
			n.maintain()
			requireScoutRepairRetired(t, n, s)
			if reply == "singleton" || reply == "repeated_singletons" {
				n.mu.Lock()
				retained := n.ledger[row.Addr] != nil && n.ledger[row.Addr].EverHint
				n.mu.Unlock()
				if !retained {
					t.Fatal("legitimate singleton response was discarded")
				}
			}
		})
	}
}

func TestDiscoveryScoutCompletionRespectsActiveRequest(t *testing.T) {
	n, s, remote, _ := newScoutRepairSession(t)
	s.slot <- struct{}{}
	sendScoutRepairAddresses(t, remote, []advertisedPeer{
		{Addr: "127.0.0.1:60115", Services: nodeNetworkService},
		{Addr: "127.0.0.1:60116", Services: nodeNetworkService},
	}, false)
	n.maintain()
	requireScoutRepairOpen(t, s)
	<-s.slot
	n.maintain()
	requireScoutRepairRetired(t, n, s)
}
