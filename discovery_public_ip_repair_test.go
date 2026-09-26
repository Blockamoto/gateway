package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// Parse the observed address from a real Bitcoin version/verack exchange over
// an in-memory socket. No public peer or wall-clock wait is involved.
func publicIPRepairHandshake(t *testing.T, address, observed string) *peerConn {
	t.Helper()
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	serverDone := make(chan error, 1)
	go func() {
		m, err := readMessage(remote)
		if err == nil && m.command != "version" {
			err = fmt.Errorf("expected version, got %s", m.command)
		}
		if err == nil {
			payload := makeVersionPayload(net.JoinHostPort(observed, "48333"), nodeNetworkService|nodeWitnessService)
			binary.LittleEndian.PutUint32(payload[:4], 70016)
			err = writeMessage(remote, "version", payload)
		}
		for err == nil {
			m, err = readMessage(remote)
			if err != nil || m.command == "verack" {
				break
			}
		}
		if err == nil {
			err = writeMessage(remote, "verack", nil)
		}
		serverDone <- err
	}()
	p, err := handshakeOutboundState(local, address, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	if !p.observedIP.Equal(net.ParseIP(observed)) {
		t.Fatalf("handshake observation=%s, want %s", p.observedIP, observed)
	}
	return p
}

func newPublicIPRepairFixture(t *testing.T) (*app, *time.Time) {
	t.Helper()
	a := independentTestApp(t)
	a.settings.ServeData = true
	if err := a.source.start(); err != nil {
		t.Fatal(err)
	}
	n := a.network
	n.allowPrivate = false
	now := time.Now().UTC()
	n.clock = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		local, remote := net.Pipe()
		t.Cleanup(func() { local.Close(); remote.Close() })
		address := fmt.Sprintf("12.%d.0.1:8333", i+1)
		s := &bitcoinSession{n: n, peer: &peerConn{addr: address, conn: local, services: nodeNetworkService}, slot: make(chan struct{}, 1), done: make(chan struct{}), view: networkPeerView{Address: address, Direction: "outbound", Role: "bitcoin_transport", Connected: now, State: "connected"}}
		if i >= 3 {
			s.view.Role, s.view.Gateway, s.view.BOD, s.peer.gateway, s.requestSuccess = "gateway_service", true, true, true, true
		}
		n.sessions[address] = s
		if i < 2 {
			p := publicIPRepairHandshake(t, address, "44.44.44.44")
			n.recordPublicIPObservationLocked(address, p.observedIP, now)
		}
	}
	for i := 0; i < 12; i++ {
		n.add(fmt.Sprintf("20.%d.0.1:8333", i+1), "bitcoin_addr:fixture", nodeNetworkService|nodeWitnessService)
	}
	return a, &now
}

func completePublicIPRepairProbe(t *testing.T, n *bitcoinNetwork, plan bitcoinAddress, observed string, now time.Time) {
	t.Helper()
	if !n.persistLedger() {
		t.Fatal(n.ledgerError)
	}
	p := publicIPRepairHandshake(t, plan.Address, observed)
	n.mu.Lock()
	n.recordPublicIPObservationLocked(plan.Address, p.observedIP, now)
	delete(n.dialing, plan.Address)
	x := n.ledger[plan.Address]
	x.InFlight = false
	x.LastCrawled = now
	x.RetryAfter = now.Add(n.policy.RevisitAfter)
	x.GatewayRetryAfter = now.Add(n.policy.RevisitAfter)
	n.touchLocked(x)
	n.mu.Unlock()
}

func TestPublicIPRepairFreshHandshakesMaintainNinetyMinutes(t *testing.T) {
	a, now := newPublicIPRepairFixture(t)
	n := a.network
	start := *now
	previous := time.Time{}
	probes := 0
	for minute := 0; minute <= 90; minute++ {
		*now = start.Add(time.Duration(minute) * time.Minute)
		allowed := a.needsPublicIPObservations054()
		n.mu.Lock()
		n.tokens = 3
		plans := n.planDiscoveryWithRefreshLocked(*now, false, false, allowed)
		n.mu.Unlock()
		if len(plans) > 1 {
			t.Fatalf("refresh started %d probes at minute %d", len(plans), minute)
		}
		for _, plan := range plans {
			if plan.LastRole != "gateway_search" || !previous.IsZero() && now.Sub(previous) < time.Minute {
				t.Fatalf("refresh role/rate bypass at minute %d: %+v", minute, plan)
			}
			previous = *now
			probes++
			completePublicIPRepairProbe(t, n, plan, "44.44.44.44", *now)
		}
		if endpoint, _ := a.advertisedGatewayEndpoint(); !strings.HasPrefix(endpoint, "44.44.44.44:") {
			t.Fatalf("auto endpoint expired at minute %d: %s", minute, endpoint)
		}
	}
	if probes != 8 {
		t.Fatalf("expected two real refresh handshakes per twenty-minute interval, got %d", probes)
	}
	if !n.observedAt["12.1"].Equal(start) || !n.observedAt["12.2"].Equal(start) {
		t.Fatal("live connection evidence was falsely renewed without a handshake")
	}
	if len(n.sessions) != 5 {
		t.Fatal("refresh replaced a stable Bitcoin or Gateway socket")
	}
	for _, s := range n.sessions {
		select {
		case <-s.done:
			t.Fatal("refresh closed a stable socket")
		default:
		}
	}
	t.Logf("90 fake-clock minutes: %d real version/verack refresh exchanges; stable sessions preserved", probes)
}

func TestPublicIPRepairPauseAndConflictsDoNotRenewEvidence(t *testing.T) {
	a, now := newPublicIPRepairFixture(t)
	n := a.network
	start := *now
	*now = start.Add(20 * time.Minute)
	for i, host := range []string{"8.8.4.4", "1.1.1.1"} {
		*now = start.Add(time.Duration(20+i) * time.Minute)
		n.mu.Lock()
		n.tokens = 3
		plans := n.planDiscoveryWithRefreshLocked(*now, false, false, true)
		n.mu.Unlock()
		if len(plans) != 1 {
			t.Fatalf("expected one conflicting observation probe: %+v", plans)
		}
		completePublicIPRepairProbe(t, n, plans[0], host, *now)
		n.mu.Lock()
		n.tokens = 3
		again := n.planDiscoveryWithRefreshLocked(now.Add(30*time.Second), false, false, true)
		n.mu.Unlock()
		if len(again) != 0 {
			t.Fatal("conflict bypassed refresh rate limit")
		}
	}
	a.settings.DiscoveryPaused = true
	*now = start.Add(31 * time.Minute)
	if a.needsPublicIPObservations054() {
		t.Fatal("paused discovery requested a refresh")
	}
	n.mu.Lock()
	n.tokens = 3
	plans := n.planDiscoveryWithRefreshLocked(*now, false, false, false)
	n.mu.Unlock()
	if len(plans) != 0 {
		t.Fatal("paused discovery opened a refresh probe")
	}
	if endpoint, _ := a.advertisedGatewayEndpoint(); endpoint != "" {
		t.Fatalf("conflicting/expired observations retained an endpoint: %s", endpoint)
	}
	if !n.observedAt["12.1"].Equal(start) {
		t.Fatal("pause refreshed stale evidence")
	}
}

func TestPublicIPRepairEndpointSourcesSuppressProbes(t *testing.T) {
	a, _ := newPublicIPRepairFixture(t)
	if !a.needsPublicIPObservations054() {
		t.Fatal("automatic endpoint fixture did not require observations")
	}
	for _, gate := range []string{"offline", "paused", "serving_disabled", "explicit", "core"} {
		a.settings.NetworkDisabled, a.settings.DiscoveryPaused = false, false
		a.settings.ServeData, a.settings.SatlineServePublished = true, false
		a.settings.AdvertiseAddr = ""
		a.servingReadiness = bitcoinServingReadiness{}
		switch gate {
		case "offline":
			a.settings.NetworkDisabled = true
		case "paused":
			a.settings.DiscoveryPaused = true
		case "serving_disabled":
			a.settings.ServeData = false
		case "explicit":
			a.settings.AdvertiseAddr = "public.example:48333"
		case "core":
			a.servingReadiness = bitcoinServingReadiness{Key: servingSettingsKey(a.settings), LocalAddresses: []string{"8.8.8.8"}}
		}
		if a.needsPublicIPObservations054() {
			t.Fatalf("refresh bypassed endpoint source/setting: %s", gate)
		}
	}
}

func TestPublicIPRepairRespectsBudgetsAndBackoff(t *testing.T) {
	for _, gate := range []string{"tokens", "dial_max", "global_slots", "history_limit", "history_error", "backoff", "gateway_backoff", "revisit", "excluded", "fresh_group", "pending_group"} {
		t.Run(gate, func(t *testing.T) {
			a := independentTestApp(t)
			n := a.network
			now := fakeClock053(n)
			n.policy.BitcoinSlots, n.policy.GatewaySlots, n.policy.ScoutSlots = 0, 0, 1
			n.add("8.8.8.8:8333", "bitcoin_addr:fixture", nodeNetworkService)
			n.mu.Lock()
			defer n.mu.Unlock()
			defer func() { n.sessions = map[string]*bitcoinSession{} }()
			n.tokens = 3
			x := n.ledger["8.8.8.8:8333"]
			switch gate {
			case "tokens":
				n.tokens = 0
			case "dial_max":
				n.policy.DialMax = 0
			case "global_slots":
				n.sessions["1.1.1.1:8333"] = &bitcoinSession{peer: &peerConn{addr: "1.1.1.1:8333"}, view: networkPeerView{Direction: "outbound", Role: "gateway_member"}}
			case "history_limit":
				n.ledgerLimited = true
			case "history_error":
				n.ledgerError = "injected persistence failure"
			case "backoff":
				x.RetryAfter = now.Add(time.Minute)
			case "gateway_backoff":
				x.GatewayRetryAfter = now.Add(time.Minute)
			case "revisit":
				x.LastCrawled = now
			case "excluded":
				x.Exclusion = "manual"
			case "fresh_group":
				n.recordPublicIPObservationLocked("8.8.9.9:8333", net.ParseIP("44.44.44.44"), now)
			case "pending_group":
				n.policy.ScoutSlots = 2
				n.dialing["8.8.9.9:8333"] = networkPeerView{Address: "8.8.9.9:8333", Role: "gateway_candidate"}
			}
			if plans := n.planDiscoveryWithRefreshLocked(now, false, false, true); len(plans) != 0 {
				t.Fatalf("refresh bypassed %s: %+v", gate, plans)
			}
		})
	}
}

func TestPublicIPRepairFailedHandshakeDoesNotRefresh(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	now := fakeClock053(n)
	n.policy.BitcoinSlots, n.policy.GatewaySlots, n.policy.ScoutSlots = 0, 0, 2
	n.add("8.8.8.8:8333", "bitcoin_addr:fixture", nodeNetworkService)
	n.add("1.1.1.1:8333", "bitcoin_addr:fixture", nodeNetworkService)
	n.mu.Lock()
	n.tokens = 3
	plans := n.planDiscoveryWithRefreshLocked(now, false, false, true)
	n.mu.Unlock()
	if len(plans) != 1 {
		t.Fatalf("first refresh: %+v", plans)
	}
	if !n.persistLedger() {
		t.Fatal(n.ledgerError)
	}
	local, remote := net.Pipe()
	remote.Close()
	_, err := handshakeOutboundState(local, plans[0].Address, nil, 0, 0)
	if err == nil {
		t.Fatal("broken handshake unexpectedly succeeded")
	}
	n.failedOutcome(plans[0].Address, err, "bitcoin_handshake_failed")
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.observedAt) != 0 || !n.ledger[plans[0].Address].RetryAfter.After(now) {
		t.Fatal("failed handshake refreshed evidence or lost failure backoff")
	}
	n.tokens = 3
	if plans := n.planDiscoveryWithRefreshLocked(now.Add(30*time.Second), false, false, true); len(plans) != 0 {
		t.Fatal("failed handshake bypassed refresh minute limit")
	}
}

func TestDiscoveryRecoveryPreservesSpecificAndWeakHintTurns(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	now := fakeClock053(n)
	n.policy.BitcoinSlots, n.policy.ScoutSlots, n.policy.GatewaySlots = 0, 0, 1
	for i := 1; i <= 32; i++ {
		n.add(fmt.Sprintf("13.1.0.%d:8333", i), "bitcoin_addr:known", gatewayExperimentalServiceBit)
		n.add(fmt.Sprintf("14.1.0.%d:8333", i), "bitcoin_addr:specific", gatewayExperimentalServiceBit)
		n.add(fmt.Sprintf("15.1.0.%d:8333", i), "bitcoin_addr:weak", ^uint64(0))
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, x := range n.ledger {
		if strings.Contains(x.Source, "known") {
			x.GatewayVerified, x.Attempts = true, 1
		}
	}
	counts := map[string]int{}
	for turn := 0; turn < 16; turn++ {
		n.tokens = 1
		plans := n.planDiscoveryWithRecoveryLocked(now, true, true)
		if len(plans) != 1 {
			t.Fatalf("no candidate opportunity %d", turn)
		}
		x := plans[0]
		class := "specific"
		if x.GatewayVerified {
			class = "known"
		} else if hintQuality054(&x) == "weak_all_bits" {
			class = "weak"
		}
		counts[class]++
		finishPlans053(n, plans)
	}
	if counts["known"] < 8 || counts["specific"] == 0 || counts["weak"] == 0 {
		t.Fatalf("continuous backlog starved a candidate class: %+v", counts)
	}
}
