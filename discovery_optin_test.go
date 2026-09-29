package main

import (
	"bytes"
	"encoding/binary"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBitcoinProbeDefaultsAndLegacyMigrationOff(t *testing.T) {
	if defaultSettings().BitcoinGatewayDiscovery {
		t.Fatal("new install opted in")
	}
	for _, legacy := range []string{`{}`, `{"discovery_paused":false,"serve_data":true,"lan_discovery":true}`, `{"bitcoin_gateway_discovery":true}`} {
		a := &app{dataDir: t.TempDir()}
		if err := os.WriteFile(filepath.Join(a.dataDir, "settings.json"), []byte(legacy), 0600); err != nil {
			t.Fatal(err)
		}
		s := a.loadSettings()
		expected := legacy == `{"bitcoin_gateway_discovery":true}`
		if s.BitcoinGatewayDiscovery != expected || s.public().BitcoinGatewayDiscovery != expected {
			t.Fatal("legacy settings silently enabled experiment", legacy)
		}
	}
}

func TestBitcoinProbeOffSchedulesNormalRoutesAndBitcoinData(t *testing.T) {
	a := independentTestApp(t)
	a.settings.BitcoinGatewayDiscovery = false
	n := a.network
	now := fakeClock053(n)
	n.add("11.1.1.1:8333", "bitcoin_dns:fixture", nodeNetworkService)
	n.add("11.1.1.2:48333", "bitcoin_addr:fixture", gatewayExperimentalServiceBit)
	n.add("11.1.1.3:48333", "manual_gateway", gatewayExperimentalServiceBit)
	n.mu.Lock()
	defer n.mu.Unlock()
	n.configuredGateways = []string{"11.1.1.3:48333"}
	n.tokens = 3
	plans := n.planDiscoveryLocked(now, true)
	bitcoin, configured := false, false
	for _, p := range plans {
		switch p.Address {
		case "11.1.1.1:8333":
			bitcoin = true
		case "11.1.1.3:48333":
			configured = true
		case "11.1.1.2:48333":
			t.Fatal("followed experimental hint while off")
		}
		if n.dialing[p.Address].Role == "gateway_search" {
			t.Fatal("opened scout while off")
		}
	}
	if !bitcoin || !configured {
		t.Fatalf("normal route or Bitcoin data blocked: %+v", plans)
	}
	finishPlans053(n, plans)
	n.ledger["11.1.1.2:48333"].GatewayVerified = true
	n.tokens = 1
	n.roleCursor = 1
	plans = n.planDiscoveryLocked(now, true)
	if len(plans) != 1 || plans[0].Address != "11.1.1.2:48333" {
		t.Fatal("remembered verified Gateway not recovered", plans)
	}
}

func TestBitcoinProbeExplicitStartStopAndResumeDoesNotOptIn(t *testing.T) {
	a := independentTestApp(t)
	a.settings.BitcoinGatewayDiscovery = false
	n := a.network
	n.tokens = 0
	n.policy.TokenInterval = time.Hour
	invoke := func(action string) {
		t.Helper()
		w := httptest.NewRecorder()
		a.handleDiscovery(w, httptest.NewRequest("POST", "/api/v1/discovery", bytes.NewBufferString(`{"action":"`+action+`"}`)))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	invoke("resume")
	if a.settings.BitcoinGatewayDiscovery {
		t.Fatal("legacy resume opted in")
	}
	invoke("bitcoin_probe_start")
	if !a.loadSettings().BitcoinGatewayDiscovery {
		t.Fatal("consent not persisted")
	}
	if !n.discoverySnapshot()["bitcoin_gateway_discovery"].(bool) {
		t.Fatal("network API missing consent")
	}
	stopped, normalCancelled, bitcoinCancelled := false, false, false
	n.mu.Lock()
	n.dialCancels["11.2.1.1:48333"] = func() { stopped = true }
	n.dialing["11.2.1.1:48333"] = networkPeerView{Role: "gateway_candidate"}
	n.dialCancels["11.2.1.2:48333"] = func() { normalCancelled = true }
	n.dialing["11.2.1.2:48333"] = networkPeerView{Role: "gateway_candidate"}
	n.configuredGateways = []string{"11.2.1.2:48333"}
	n.recordAddressLocked(advertisedPeer{Addr: "11.2.1.2:48333"}, "manual_gateway")
	n.dialCancels["11.2.1.3:8333"] = func() { bitcoinCancelled = true }
	n.dialing["11.2.1.3:8333"] = networkPeerView{Role: "bitcoin_transport"}
	n.mu.Unlock()
	invoke("bitcoin_probe_stop")
	if a.loadSettings().BitcoinGatewayDiscovery || !stopped || normalCancelled || bitcoinCancelled {
		t.Fatal("stop did not isolate experiment", stopped, normalCancelled, bitcoinCancelled)
	}
	if _, ok := n.announcementSnapshot054(); ok {
		t.Fatal("announcement allowed while off")
	}
	// Cleanup must not call test-only closures after assertions.
	n.mu.Lock()
	clear(n.dialCancels)
	n.mu.Unlock()
}

func TestBitcoinProbeOffConfiguredPeerNegotiatesOverTCP(t *testing.T) {
	server := independentTestApp(t)
	server.settings.BitcoinGatewayDiscovery = false
	server.settings.ServeData = true
	if e := server.source.start(); e != nil {
		t.Fatal(e)
	}
	client := independentTestApp(t)
	client.settings.BitcoinGatewayDiscovery = false
	client.settings.GatewayBootstrapPeers = []string{server.source.tcp.Addr().String()}
	fastDiscovery052(client.network)
	client.network.policy.BitcoinSlots = 0
	pump052(t, client.network, func() bool { return client.network.discoverySnapshot()["gateway_connected"].(int) > 0 })
}

func TestBitcoinDataHandshakeDoesNotOfferGatewayFeature(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()
	result := make(chan error, 1)
	go func() {
		p, e := handshakeOutboundState(local, "11.1.1.1:8333", nil, 0, 0)
		if p != nil {
			p.conn.Close()
		}
		result <- e
	}()
	remote.SetDeadline(time.Now().Add(3 * time.Second))
	if m, e := readMessage(remote); e != nil || m.command != "version" {
		t.Fatal("missing Bitcoin version", e)
	}
	payload := makeVersionPayload("11.1.1.1:8333", nodeNetworkService)
	binary.LittleEndian.PutUint32(payload[:4], 70017)
	if e := writeMessage(remote, "version", payload); e != nil {
		t.Fatal(e)
	}
	for {
		m, e := readMessage(remote)
		if e != nil {
			t.Fatal(e)
		}
		if m.command == "feature" {
			t.Fatal("ordinary Bitcoin connection offered Gateway experiment")
		}
		if m.command == "verack" {
			break
		}
	}
	if e := writeMessage(remote, "verack", nil); e != nil {
		t.Fatal(e)
	}
	if e := <-result; e != nil {
		t.Fatal(e)
	}
}
func TestBitcoinProbeOffGatewayIntroductionsRetainRouteProvenance(t *testing.T) {
	a := independentTestApp(t)
	a.settings.BitcoinGatewayDiscovery = false
	n := a.network
	now := fakeClock053(n)
	n.policy.BitcoinSlots = 0
	target := "11.4.1.1:48333"
	n.add(target, "bitcoin_addr:old", gatewayExperimentalServiceBit)
	supplier := &bitcoinSession{n: n, peer: &peerConn{addr: "11.4.2.1:48333", gateway: true}}
	n.receiveAddresses(supplier, []advertisedPeer{{Addr: target, Services: gatewayExperimentalServiceBit}})
	n.mu.Lock()
	defer n.mu.Unlock()
	n.tokens = 1
	n.roleCursor = 1
	plans := n.planDiscoveryLocked(now, true)
	if len(plans) != 1 || plans[0].Address != target {
		t.Fatal("Gateway introduction unavailable with Bitcoin experiment off", plans)
	}
	if n.ledger[target].Source != "bitcoin_addr:old" || n.dialing[target].Source != "gateway_addr:"+supplier.peer.addr {
		t.Fatal("first introduction overwritten or current route falsely presented as Bitcoin-only")
	}
}

func TestBitcoinProbeGeneralSettingsCannotChangeConsent(t *testing.T) {
	for _, consent := range []bool{false, true} {
		a := independentTestApp(t)
		a.settings.BitcoinGatewayDiscovery = consent
		// General settings may carry stale or arbitrary experiment values. Only Probe owns consent.
		body := `{"core_disabled":true,"core_mount_disabled":true,"network_disabled":true,"bitcoin_gateway_discovery":`
		if consent {
			body += "false}"
		} else {
			body += "true}"
		}
		w := httptest.NewRecorder()
		a.handleSettings(w, httptest.NewRequest("POST", "/api/settings", bytes.NewBufferString(body)))
		if w.Code != 200 || a.settings.BitcoinGatewayDiscovery != consent {
			t.Fatal("general settings changed Probe consent", w.Code, w.Body.String())
		}
	}
}
func TestBitcoinProbeOffBitcoinTransportDoesNotInventNegativeGatewayCheck(t *testing.T) {
	a := independentTestApp(t)
	a.settings.BitcoinGatewayDiscovery = false
	relay := ordinaryRelay052(t, nil, false)
	a.settings.BitcoinPeers = []string{relay.address}
	fastDiscovery052(a.network)
	pump052(t, a.network, func() bool {
		a.network.mu.Lock()
		defer a.network.mu.Unlock()
		return a.network.sessions[relay.address] != nil
	})
	a.network.mu.Lock()
	defer a.network.mu.Unlock()
	x := a.network.ledger[relay.address]
	if x.LastNegotiation != "not_requested" || x.GatewayChecks != 0 || !x.GatewayRetryAfter.IsZero() {
		t.Fatal("disabled negotiation recorded as a failed Gateway check", x.LastNegotiation, x.GatewayChecks)
	}
}
