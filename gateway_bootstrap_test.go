package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGatewayBootstrapNormalizesDomainsAndPorts(t *testing.T) {
	for input, want := range map[string]string{
		" Example.COM. ": "example.com:48333", "example.com:49000": "example.com:49000",
		"127.0.0.1": "127.0.0.1:48333", "::1": "[::1]:48333", "[::1]": "[::1]:48333", "[::1]:49000": "[::1]:49000",
		"localhost": "localhost:48333",
	} {
		got, err := normalizeGatewayBootstrapPeer(input)
		if err != nil || got != want {
			t.Fatalf("%q became %q (%v), want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "https://example.com", "user@example.com", "bad host", "example.com:0", "example.com:65536", "example.com:no", "example..com", "-bad.example", "bad-.example", "999.999.999.999", "[fe80::1%3]:48333", "é.example", "name.onion"} {
		if _, err := normalizeGatewayBootstrapPeer(input); err == nil {
			t.Fatalf("accepted malformed/unsupported peer %q", input)
		}
	}
	got, err := normalizeGatewayBootstrapPeers([]string{"example.com", "EXAMPLE.COM.:48333", "127.0.0.1"})
	if err != nil || len(got) != 2 {
		t.Fatalf("canonical deduplication failed: %v %v", got, err)
	}
	tooMany := []string{}
	for i := 0; i <= maxGatewayBootstrapPeers; i++ {
		tooMany = append(tooMany, "peer"+strings.Repeat("x", i)+".example")
	}
	if _, err := normalizeGatewayBootstrapPeers(tooMany); err == nil {
		t.Fatal("bootstrap limit ignored")
	}
}

func TestGatewayBootstrapSettingsAppendReplaceAndPersist(t *testing.T) {
	a := independentTestApp(t)
	a.settings.NetworkDisabled = true // Saving settings must not dial anything.
	a.settings.ManualPeer = "localhost:49001"
	a.settings.BitcoinPeers = []string{"127.0.0.1:8333"}
	for _, peer := range []string{"localhost:49002", "localhost:49003"} {
		body, _ := json.Marshal(map[string]string{"action": "add", "address": peer})
		w := httptest.NewRecorder()
		a.handlePeers(w, httptest.NewRequest("POST", "/api/v1/peers", bytes.NewReader(body)))
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if len(a.settings.GatewayBootstrapPeers) != 3 || a.settings.ManualPeer != "" || len(a.settings.BitcoinPeers) != 1 || !a.settings.PrivacyMode || !a.settings.NetworkDisabled {
		t.Fatalf("add replaced a prior entrance or altered unrelated policy: %+v", a.settings.public())
	}
	restored := a.loadSettings()
	if len(restored.GatewayBootstrapPeers) != 3 || restored.GatewayBootstrapPeers[0] != "localhost:49001" {
		t.Fatalf("configured entrances did not persist: %v", restored.GatewayBootstrapPeers)
	}
	public := a.settings.public()
	public.GatewayBootstrapPeers[0] = "changed.example"
	if a.settings.GatewayBootstrapPeers[0] != "localhost:49001" {
		t.Fatal("public settings aliased mutable configuration")
	}
	w := httptest.NewRecorder()
	a.handleDiscovery(w, httptest.NewRequest("POST", "/api/v1/discovery", strings.NewReader(`{"action":"bootstrap_peers","peers":["localhost"]}`)))
	if w.Code != 200 || len(a.settings.GatewayBootstrapPeers) != 1 || a.settings.GatewayBootstrapPeers[0] != "localhost:48333" {
		t.Fatalf("replace failed: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	a.handleDiscovery(w, httptest.NewRequest("POST", "/api/v1/discovery", strings.NewReader(`{"action":"bootstrap_peers","peers":["https://invalid.example"]}`)))
	if w.Code != 400 || a.settings.GatewayBootstrapPeers[0] != "localhost:48333" {
		t.Fatal("invalid replacement changed configuration")
	}
}

func TestGatewayBootstrapConfigurationKeepsLedgerEvidence(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	n.allowPrivate = false
	now := fakeClock053(n)
	address := "127.0.0.1:49001"
	n.add(address, "bitcoin_addr:fixture", 0)
	n.mu.Lock()
	x := n.ledger[address]
	if x.Exclusion == "" {
		t.Fatal("fixture should begin as excluded private gossip")
	}
	x.Failures, x.Attempts = 3, 4
	x.RetryAfter, x.GatewayRetryAfter = now.Add(time.Minute), now.Add(time.Hour)
	x.LastNegotiation = "no_gateway"
	n.mu.Unlock()
	n.ensureConfiguredAddress(address, "manual_gateway", gatewayExperimentalServiceBit)
	n.mu.Lock()
	if x.Exclusion != "" || !x.EverHint || x.GatewayVerified || x.Source != "bitcoin_addr:fixture" || x.Failures != 3 || x.Attempts != 4 || !x.RetryAfter.Equal(now.Add(time.Minute)) || !x.GatewayRetryAfter.Equal(now.Add(time.Hour)) || x.LastNegotiation != "no_gateway" {
		t.Fatalf("configuration overwrote evidence or failed explicit admission: %+v", x)
	}
	observations := x.Observations
	n.mu.Unlock()
	n.ensureConfiguredAddress(address, "manual_gateway", gatewayExperimentalServiceBit)
	n.mu.Lock()
	defer n.mu.Unlock()
	if x.Observations != observations {
		t.Fatal("settings polling invented new endpoint observations")
	}
}

func TestGatewayBootstrapPriorityDoesNotStarveBitcoinHints(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	now := fakeClock053(n)
	n.policy.BitcoinSlots, n.policy.GatewaySlots, n.policy.ScoutSlots = 0, 2, 0
	n.add("11.1.2.3:48333", "bitcoin_addr:fixture", gatewayExperimentalServiceBit)
	n.add("localhost:49001", "manual_gateway", gatewayExperimentalServiceBit)
	n.mu.Lock()
	defer n.mu.Unlock()
	n.configuredGateways = []string{"localhost:49001"}
	n.tokens, n.roleCursor = 1, 1
	plans := n.planDiscoveryLocked(now, true)
	if len(plans) != 1 || plans[0].Address != "localhost:49001" {
		t.Fatalf("configured entrance did not get bounded priority: %+v", plans)
	}
	finishPlans053(n, plans)
	deadline := n.ledger["localhost:49001"].RetryAfter
	n.tokens, n.roleCursor = 1, 1
	plans = n.planDiscoveryLocked(now, true)
	if len(plans) != 1 || plans[0].Address != "11.1.2.3:48333" {
		t.Fatalf("Bitcoin hint lost its turn: %+v", plans)
	}
	finishPlans053(n, plans)
	n.tokens = 3
	if plans = n.planDiscoveryLocked(now, true); len(plans) != 0 || !n.ledger["localhost:49001"].RetryAfter.Equal(deadline) {
		t.Fatal("configured peers bypassed failure backoff")
	}
}

func TestGatewayBootstrapRememberedPeerPrecedesConfiguredHint(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	now := fakeClock053(n)
	n.policy.BitcoinSlots, n.policy.GatewaySlots, n.policy.ScoutSlots = 0, 2, 0
	n.add("localhost:49001", "manual_gateway", gatewayExperimentalServiceBit)
	n.add("11.2.3.4:48333", "bitcoin_addr:fixture", gatewayExperimentalServiceBit)
	n.mu.Lock()
	defer n.mu.Unlock()
	n.configuredGateways = []string{"localhost:49001"}
	known := n.ledger["11.2.3.4:48333"]
	known.GatewayVerified = true
	n.tokens, n.roleCursor = 1, 1
	plans := n.planDiscoveryLocked(now, true)
	if len(plans) != 1 || plans[0].Address != known.Address {
		t.Fatalf("remembered verification lost priority: %+v", plans)
	}
	finishPlans053(n, plans)
	if delay := known.RetryAfter.Sub(now); delay < 10*time.Second || delay > 12*time.Second {
		t.Fatalf("remembered reconnect no longer bounded: %s", delay)
	}
}

func TestGatewayBootstrapMeshLossResumesScouting(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	now := fakeClock053(n)
	n.policy.BitcoinSlots, n.policy.GatewaySlots, n.policy.ScoutSlots = 0, 1, 1
	n.tokens, n.tokenAt = 0, now // Observe maintenance without opening sockets.
	s := &bitcoinSession{n: n, peer: &peerConn{addr: "11.2.3.4:48333", gateway: true, bod: true}, view: networkPeerView{Gateway: true, BOD: true, Role: "gateway_service", Direction: "outbound", Connected: now}, slot: make(chan struct{}, 1), requestSuccess: true, healthChecking: true}
	n.sessions[s.peer.addr] = s
	defer func() { n.sessions = map[string]*bitcoinSession{} }()
	n.discovery.EverUseful = true
	n.maintain()
	state := n.discoverySnapshot()
	if n.discovery.Mode != "maintain" || state["discovery_policy"].(map[string]any)["normal_scouting"].(bool) {
		t.Fatalf("healthy mesh did not step back: %s", n.discovery.Mode)
	}
	n.mu.Lock()
	s.requestSuccess = false
	n.mu.Unlock()
	n.maintain()
	state = n.discoverySnapshot()
	if n.discovery.Mode != "recover" || !state["discovery_policy"].(map[string]any)["normal_scouting"].(bool) || s.scout {
		t.Fatalf("mesh loss failed recovery or evicted retained membership: %s", n.discovery.Mode)
	}
}

func TestGatewayBootstrapLoopbackDomainSurvivesFailedEntrance(t *testing.T) {
	server := independentTestApp(t)
	server.settings.ServeData = true
	if err := server.source.start(); err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(server.source.tcp.Addr().String())
	dead, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddress := dead.Addr().String()
	dead.Close()
	client := independentTestApp(t)
	n := client.network
	fastDiscovery052(n)
	n.policy.BitcoinSlots, n.policy.GatewaySlots = 0, 1
	client.settings.GatewayBootstrapPeers = []string{deadAddress, "localhost:" + port}
	pump052(t, n, func() bool {
		n.mu.Lock()
		defer n.mu.Unlock()
		x := n.ledger["localhost:"+port]
		return x != nil && x.GatewayVerified && n.sessions[x.Address] != nil
	})
	n.mu.Lock()
	defer n.mu.Unlock()
	failed := n.ledger[deadAddress]
	if failed.Attempts == 0 || failed.Failures == 0 || failed.RetryAfter.IsZero() {
		t.Fatalf("failed entrance did not retain bounded retry evidence: %+v", failed)
	}
}
