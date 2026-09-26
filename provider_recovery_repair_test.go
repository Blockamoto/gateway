package main

import (
	"testing"
	"time"
)

func TestProviderRecoveryReusesGracefullyRetiredStandby(t *testing.T) {
	servers := []*app{independentTestApp(t), independentTestApp(t)}
	rows := []advertisedPeer{}
	for _, server := range servers {
		server.settings.ServeData, server.settings.ShareCache = true, true
		seedBlockOne052(t, server, false)
		if err := server.source.start(); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, advertisedPeer{Addr: server.source.tcp.Addr().String(), Services: gatewayExperimentalServiceBit | nodeWitnessService})
	}
	relay := ordinaryRelay052(t, rows, false)
	client := independentTestApp(t)
	n := client.network
	fastDiscovery052(n)
	// Force both capability checks to complete before the surplus-retirement
	// decision. This models the real concurrent discovery race deterministically.
	n.policy.GatewaySlots = 2
	n.add(relay.address, "bitcoin_seed_fixture", nodeNetworkService)
	pump052(t, n, func() bool { n.mu.Lock(); defer n.mu.Unlock(); return n.usefulCountLocked() == 2 })
	n.mu.Lock()
	n.policy.GatewaySlots = 1
	n.mu.Unlock()
	n.maintain()
	var member *bitcoinSession
	var standby string
	n.mu.Lock()
	for _, s := range n.sessions {
		if s.view.Gateway {
			member = s
		}
	}
	for _, row := range rows {
		if x := n.ledger[row.Addr]; x != nil && x.LastOutcome == "gateway_standby" && x.RetryAfter.After(n.now()) {
			standby = row.Addr
		}
	}
	n.mu.Unlock()
	if member == nil || standby == "" {
		t.Fatal("fixture did not retire one proven surplus provider")
	}
	for _, server := range servers {
		if server.source.tcp.Addr().String() == member.peer.addr {
			server.settingsMu.Lock()
			server.settings.ServeData = false
			server.settingsMu.Unlock()
		}
	}
	n.checkServiceHealth(member)
	n.mu.Lock()
	memberRetained := n.sessions[member.peer.addr] == member && !member.scout && member.view.Role == "gateway_member"
	eligible := !n.ledger[standby].RetryAfter.After(n.now()) && n.ledger[standby].LastOutcome == "gateway_standby_recovery"
	n.mu.Unlock()
	if !memberRetained || !eligible {
		t.Fatalf("service loss: member retained=%v standby eligible=%v", memberRetained, eligible)
	}
	pump052(t, n, func() bool {
		n.mu.Lock()
		defer n.mu.Unlock()
		s := n.sessions[standby]
		return s != nil && s.requestSuccess && !s.scout
	})
	n.maintain()
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.sessions[member.peer.addr] != member || member.scout || n.sessions[standby].view.Role != "gateway_provider" {
		t.Fatal("useful replacement evicted retained Gateway membership")
	}
	if len(n.sessions)+len(n.dialing) > n.policy.BitcoinSlots+n.policy.GatewaySlots+n.policy.ScoutSlots {
		t.Fatal("provider replacement exceeded physical connection budget")
	}
}

func TestProviderRecoveryPreservesFailureCooldownsAndDialBudget(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	now := fakeClock053(n)
	n.add("12.13.14.15:48333", "bitcoin_addr:verified", gatewayExperimentalServiceBit)
	n.mu.Lock()
	defer n.mu.Unlock()
	x := n.ledger["12.13.14.15:48333"]
	x.GatewayVerified, x.LastBODSuccess = true, now
	for _, gate := range []string{"failed_dial", "negative_negotiation", "not_standby", "allowed"} {
		x.LastOutcome, x.Failures, x.GatewayRetryAfter = "gateway_standby", 0, time.Time{}
		x.RetryAfter = now.Add(30 * time.Minute)
		if gate == "failed_dial" {
			x.Failures = 1
		}
		if gate == "negative_negotiation" {
			x.GatewayRetryAfter = now.Add(time.Hour)
		}
		if gate == "not_standby" {
			x.LastOutcome = "connection_failed"
		}
		n.wakeStandbyProviderLocked(now)
		if gate != "allowed" && !x.RetryAfter.Equal(now.Add(30*time.Minute)) {
			t.Fatalf("recovery erased %s cooldown", gate)
		}
		if gate == "allowed" && (x.RetryAfter.After(now) || x.LastOutcome != "gateway_standby_recovery") {
			t.Fatal("graceful standby did not become eligible")
		}
	}
	n.tokens = 0
	if plans := n.planDiscoveryWithRecoveryLocked(now, true, true); len(plans) != 0 {
		t.Fatal("standby recovery bypassed token budget")
	}
	n.tokens = 1
	if plans := n.planDiscoveryWithRecoveryLocked(now, true, true); len(plans) != 1 || plans[0].Address != x.Address {
		t.Fatalf("eligible standby was not scheduled: %+v", plans)
	}
}

func TestProviderRecoveryRetainsOnlyOneSupplementalProvider(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	n.policy.GatewaySlots, n.policy.ScoutSlots = 1, 2
	n.mu.Lock()
	defer n.mu.Unlock()
	defer func() { n.sessions = make(map[string]*bitcoinSession) }()
	member := &bitcoinSession{n: n, peer: &peerConn{addr: "member", gateway: true}, view: networkPeerView{Role: "gateway_member", Gateway: true, Direction: "outbound"}}
	n.sessions["member"] = member
	for _, address := range []string{"provider-a", "provider-b"} {
		n.sessions[address] = &bitcoinSession{n: n, peer: &peerConn{addr: address, gateway: true}, requestSuccess: true, scout: true, view: networkPeerView{Role: "gateway_candidate", Gateway: true, BOD: true, Direction: "outbound"}}
	}
	n.retainGatewaySessionsLocked()
	providers, scouts := 0, 0
	for _, s := range n.sessions {
		if s.view.Role == "gateway_provider" {
			providers++
		}
		if s.scout {
			scouts++
		}
	}
	if member.scout || providers != 1 || scouts != 1 {
		t.Fatalf("retention exceeded one supplemental slot: memberScout=%v providers=%d scouts=%d", member.scout, providers, scouts)
	}
}
