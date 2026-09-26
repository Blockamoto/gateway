package main

import (
	"fmt"
	"testing"
	"time"
)

func TestDiscoveryRecoveryAtHistoryLimit(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	now := fakeClock053(n)
	n.policy.BitcoinSlots, n.policy.ScoutSlots, n.policy.GatewaySlots = 0, 0, 1
	n.add("12.13.14.15:48333", "bitcoin_addr:verified", gatewayExperimentalServiceBit)
	n.add("13.14.15.16:48333", "bitcoin_addr:fresh", gatewayExperimentalServiceBit)
	n.mu.Lock()
	defer n.mu.Unlock()
	x := n.ledger["12.13.14.15:48333"]
	x.GatewayVerified, x.Attempts = true, 1
	n.ledgerLimited = true
	if !n.intakeBlockedLocked() {
		t.Fatal("fixture did not close intake")
	}
	for _, gate := range []string{"paused", "cooldown", "gateway_cooldown", "disk_error", "allowed"} {
		n.tokens = 1
		x.RetryAfter, x.GatewayRetryAfter = time.Time{}, time.Time{}
		n.ledgerError = ""
		if gate == "cooldown" {
			x.RetryAfter = now.Add(time.Minute)
		}
		if gate == "gateway_cooldown" {
			x.GatewayRetryAfter = now.Add(time.Minute)
		}
		if gate == "disk_error" {
			n.ledgerError = "fixture persistence failure"
		}
		plans := n.planDiscoveryWithRecoveryLocked(now, false, gate != "paused")
		if gate != "allowed" && len(plans) != 0 {
			t.Fatalf("recovery bypassed %s", gate)
		}
		if gate == "allowed" && (len(plans) != 1 || plans[0].Address != x.Address || plans[0].LastRole != "gateway_candidate") {
			t.Fatalf("known recovery missing at history cap: %+v", plans)
		}
	}
	if !x.LastBODSuccess.IsZero() {
		t.Fatal("fixture must be membership-only")
	}
}

func TestDiscoveryRecoveryInboundDoesNotConsumeOutboundBudget(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	now := fakeClock053(n)
	n.policy.BitcoinSlots, n.policy.ScoutSlots, n.policy.GatewaySlots = 0, 0, 1
	n.add("12.13.14.15:48333", "bitcoin_addr:verified", gatewayExperimentalServiceBit)
	n.mu.Lock()
	defer n.mu.Unlock()
	defer func() { n.sessions = make(map[string]*bitcoinSession) }()
	n.ledger["12.13.14.15:48333"].GatewayVerified = true
	for i := 0; i < 12; i++ {
		address := fmt.Sprintf("8.8.8.8:%d", 50000+i)
		n.sessions[address] = &bitcoinSession{n: n, peer: &peerConn{addr: address, gateway: true}, view: networkPeerView{Direction: "inbound", Role: "gateway_inbound", Gateway: true}, slot: make(chan struct{}, 1)}
	}
	n.retainGatewaySessionsLocked()
	for _, s := range n.sessions {
		if s.scout || s.view.Role != "gateway_inbound" {
			t.Fatal("inbound reclassified as outbound scout")
		}
	}
	n.tokens = 1
	if plans := n.planDiscoveryWithRecoveryLocked(now, false, true); len(plans) != 1 {
		t.Fatalf("inbound exhausted outbound recovery budget: %v", plans)
	}
}

func TestDiscoveryRecoveryHeaderReplacement(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	now := fakeClock053(n)
	n.policy.BitcoinSlots, n.policy.ScoutSlots, n.policy.GatewaySlots = 1, 1, 0
	n.add("12.13.14.15:8333", "bitcoin_addr:replacement", nodeNetworkService)
	n.mu.Lock()
	defer n.mu.Unlock()
	defer func() { n.sessions = make(map[string]*bitcoinSession) }()
	old := &bitcoinSession{n: n, peer: &peerConn{addr: "11.12.13.14:8333", services: nodeNetworkService}, headerUnavailable: true, view: networkPeerView{Role: "bitcoin_transport", Direction: "outbound"}, slot: make(chan struct{}, 1)}
	n.sessions[old.peer.addr] = old
	if len(n.retireHeaderTransportsLocked(now)) != 0 {
		t.Fatal("closed socket before replacement")
	}
	n.tokens = 1
	plans := n.planDiscoveryLocked(now, true)
	if len(plans) != 1 || plans[0].LastRole != "bitcoin_transport" {
		t.Fatalf("quarantined provider prevented replacement: %+v", plans)
	}
	delete(n.dialing, plans[0].Address)
	n.sessions[plans[0].Address] = &bitcoinSession{n: n, peer: &peerConn{addr: plans[0].Address, services: nodeNetworkService}, view: networkPeerView{Role: "bitcoin_transport", Direction: "outbound"}, slot: make(chan struct{}, 1)}
	old.slot <- struct{}{}
	if len(n.retireHeaderTransportsLocked(now)) != 0 {
		t.Fatal("closed socket during correlated request")
	}
	<-old.slot
	old.view.Gateway = true
	if len(n.retireHeaderTransportsLocked(now)) != 0 {
		t.Fatal("closed Gateway member for header-only timeout")
	}
	old.view.Gateway = false
	if got := n.retireHeaderTransportsLocked(now); len(got) != 1 || got[0] != old || !old.retired {
		t.Fatal("failed to retire replaced ordinary transport")
	}
}

func TestDiscoveryRecoveryAllHeaderSlotsUnavailable(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	now := fakeClock053(n)
	n.policy.BitcoinSlots, n.policy.GatewaySlots, n.policy.ScoutSlots = 1, 1, 1
	n.mu.Lock()
	defer n.mu.Unlock()
	defer func() { n.sessions = make(map[string]*bitcoinSession) }()
	for i := 0; i < 3; i++ {
		addr := fmt.Sprintf("11.12.%d.1:8333", i)
		n.sessions[addr] = &bitcoinSession{n: n, peer: &peerConn{addr: addr, services: nodeNetworkService}, headerUnavailable: true, view: networkPeerView{Role: "bitcoin_transport", Direction: "outbound"}, slot: make(chan struct{}, 1)}
	}
	retired := n.retireHeaderTransportsLocked(now)
	if len(retired) != 1 {
		t.Fatalf("full unusable pool must release exactly one slot, got %d", len(retired))
	}
	delete(n.sessions, retired[0].peer.addr)
	if len(n.retireHeaderTransportsLocked(now)) != 0 {
		t.Fatal("released extra sockets before attempting replacement")
	}
}

func TestDiscoveryRecoveryMembershipBackoff(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	now := fakeClock053(n)
	n.add("12.13.14.15:48333", "bitcoin_addr:verified", gatewayExperimentalServiceBit)
	n.mu.Lock()
	defer n.mu.Unlock()
	x := n.ledger["12.13.14.15:48333"]
	x.GatewayVerified = true
	n.recordFailureLocked(x, "connection_failed", "fixture")
	if d := x.RetryAfter.Sub(now); d < 10*time.Second || d > 12*time.Second {
		t.Fatalf("membership-only peer did not get recovery backoff: %v", d)
	}
}
