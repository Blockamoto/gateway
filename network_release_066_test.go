package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func Test066BitcoinHandshakeNeverAdvertisesGateway(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	result := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			result <- err
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		m, err := readMessage(c)
		if err != nil || m.command != "version" || len(m.payload) < 12 {
			result <- fmt.Errorf("missing version: %v", err)
			return
		}
		if got := binary.LittleEndian.Uint64(m.payload[4:12]); got != nodeWitnessService {
			result <- fmt.Errorf("unexpected local service advertisement %x", got)
			return
		}
		if err = writeMessage(c, "version", makeVersionPayload(ln.Addr().String(), nodeNetworkService|nodeWitnessService)); err != nil {
			result <- err
			return
		}
		// An unsolicited Gateway offer cannot activate a locked protocol.
		_ = writeMessage(c, "feature", makeFeaturePayload(gatewayFeatureID, gatewayFeatureData()))
		_ = writeMessage(c, "verack", nil)
		for {
			m, err = readMessage(c)
			if err != nil {
				result <- err
				return
			}
			if m.command != "verack" && m.command != "sendaddrv2" {
				result <- fmt.Errorf("unexpected handshake command %s", m.command)
				return
			}
			if m.command == "verack" {
				result <- nil
				return
			}
		}
	}()
	c, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	p, err := handshakeOutboundState(c, ln.Addr().String(), localGatewayProtocols(), nodeWitnessService, 100)
	if err != nil {
		t.Fatal(err)
	}
	if p.gateway || p.bod || len(p.localProtocols) != 0 {
		t.Fatal("locked protocol negotiated")
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func Test066BitcoinPoolRetainsRoomForHistoricalBlocks(t *testing.T) {
	n := &bitcoinNetwork{sessions: map[string]*bitcoinSession{}}
	limited := nodeNetworkLimitedService | nodeWitnessService
	if !n.canKeepBitcoinPeerLocked(limited) {
		t.Fatal("a recent-block node should remain useful for headers")
	}
	for _, address := range []string{"one", "two"} {
		n.sessions[address] = &bitcoinSession{peer: &peerConn{services: limited}}
	}
	if n.canKeepBitcoinPeerLocked(limited) || !n.canKeepBitcoinPeerLocked(nodeNetworkService) {
		t.Fatal("recent-block nodes must leave the last slot for historical retrieval")
	}
	n.sessions["one"].peer.services = nodeNetworkService
	if !n.canKeepBitcoinPeerLocked(limited) || n.needsArchivalPeerLocked() {
		t.Fatal("one archival peer satisfies historical-block capacity")
	}
}

func Test066SavedPeerhoodCannotOpenListenerOrDial(t *testing.T) {
	a := independentTestApp(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	address := ln.Addr().String()
	a.settings.ManualPeer = address
	a.settings.GatewayBootstrapPeers = []string{address}
	a.settings.LANDiscovery, a.settings.ServeData, a.settings.SatlineEnabled, a.settings.SatlineServePublished = true, true, true, true
	a.gatewayListen = "127.0.0.1:0"
	if err := a.source.start(); err != nil {
		t.Fatal(err)
	}
	defer a.source.stopServer()
	if a.source.udp != nil {
		t.Fatal("locked LAN discovery started")
	}
	if bitcoinListenerEnabled(appSettings{ServeGatewayData: true, SatlineEnabled: true, SatlineServePublished: true}) {
		t.Fatal("locked Gateway serving opened listener")
	}
	a.network.add(address, "manual_gateway", ^uint64(0))
	a.network.maintain()
	a.network.mu.Lock()
	queued := len(a.network.addresses) + len(a.network.dialing) + len(a.network.sessions)
	a.network.mu.Unlock()
	if queued != 0 {
		t.Fatal("Gateway settings scheduled an ordinary Bitcoin connection")
	}
	_ = ln.(*net.TCPListener).SetDeadline(time.Now().Add(150 * time.Millisecond))
	if c, err := ln.Accept(); err == nil {
		c.Close()
		t.Fatal("Gateway endpoint was dialed")
	}
	local, remote := net.Pipe()
	defer remote.Close()
	if got := a.network.adoptInbound(&peerConn{conn: local, addr: address, gateway: true}); got != nil {
		t.Fatal("adopted locked inbound connection")
	}
	if _, err := a.queryGatewayPeer(overlayPeer{Addr: address}, overlayRequest{Type: "status"}); err == nil {
		t.Fatal("Gateway query bypassed lock")
	}
	if _, err := a.knownIndexClaimPeers(); err == nil {
		t.Fatal("index peer query bypassed lock")
	}
	if _, err := a.setIndexPublication("bitmap", true); err == nil {
		t.Fatal("index publication bypassed lock")
	}
	w := httptest.NewRecorder()
	a.handlePeers(w, httptest.NewRequest("POST", "/api/v1/peers", bytes.NewBufferString(`{"action":"add","address":"127.0.0.1:48333"}`)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("peer add returned %d", w.Code)
	}
}

func Test066BitcoinAddressesRetainOnlyOrdinaryRoutes(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	n.allowPrivate = false
	n.learn([]advertisedPeer{{Addr: "8.8.8.8:8333", Services: nodeNetworkService}, {Addr: "9.9.9.9:48333", Services: uint64(1) << 25}, {Addr: "127.0.0.1:8333", Services: nodeNetworkService}}, "11.12.13.14:8333")
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.addresses) != 1 || n.addresses["8.8.8.8:8333"] == nil {
		t.Fatalf("address selection promoted unsupported/private routes: %+v", n.addresses)
	}
}

func Test066BitcoinAddressMigrationPreservesLegacyFiles(t *testing.T) {
	a := independentTestApp(t)
	legacy := []byte(`[{"address":"127.0.0.1:8333","source":"manual_bitcoin","services":9},{"address":"127.0.0.1:48333","source":"manual_gateway","services":33554432}]`)
	old := filepath.Join(a.dataDir, "bitcoin-peers.json")
	journal := filepath.Join(a.dataDir, "discovery-ledger-v1.wal")
	if err := os.WriteFile(old, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journal, []byte("historical state retained"), 0600); err != nil {
		t.Fatal(err)
	}
	n := newBitcoinNetwork(a)
	defer n.stop()
	if len(n.addresses) != 1 || n.addresses["127.0.0.1:8333"] == nil {
		t.Fatal("ordinary address migration failed")
	}
	n.persist()
	if b, _ := os.ReadFile(old); !bytes.Equal(b, legacy) {
		t.Fatal("legacy address file changed")
	}
	if b, _ := os.ReadFile(journal); string(b) != "historical state retained" {
		t.Fatal("legacy journal changed")
	}
	var rows []bitcoinAddress
	b, err := os.ReadFile(filepath.Join(a.dataDir, bitcoinAddressFile))
	if err != nil || json.Unmarshal(b, &rows) != nil || len(rows) != 1 {
		t.Fatal("ordinary address store not saved", err)
	}
}

func Test066RemovedDiscoveryRoutesAndAssets(t *testing.T) {
	a := independentTestApp(t)
	mux := http.NewServeMux()
	a.registerGatewayRoutes(mux)
	for _, path := range []string{"/api/v1/discovery", "/shell/discovery.js", "/shell/discovery.css"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("retired route %s returned %d", path, w.Code)
		}
	}
}

func Test066HeaderTimeoutRotatesOnlyAfterCurrentRequestFinishes(t *testing.T) {
	a, s, _ := sessionRepairRaw(t)
	a.network.mu.Lock()
	s.headerUnavailable = true
	a.network.mu.Unlock()
	s.slot <- struct{}{}
	a.network.maintain()
	requireSessionOpen(t, s)
	<-s.slot
	a.network.maintain()
	select {
	case <-s.done:
	default:
		t.Fatal("idle quarantined socket prevented header recovery")
	}
	if len(a.network.listSessions()) != 0 {
		t.Fatal("retired socket still occupies the pool")
	}
}

func Test066CorruptBitcoinAddressFileIsPreserved(t *testing.T) {
	a := independentTestApp(t)
	path := filepath.Join(a.dataDir, bitcoinAddressFile)
	want := []byte("truncated saved addresses")
	if err := os.WriteFile(path, want, 0600); err != nil {
		t.Fatal(err)
	}
	n := newBitcoinNetwork(a)
	defer n.stop()
	n.add("127.0.0.1:8333", "manual_bitcoin", 0)
	n.persist()
	if got, _ := os.ReadFile(path); !bytes.Equal(got, want) {
		t.Fatal("damaged address evidence was overwritten")
	}
	if n.bitcoinSnapshot()["persistence_error"] == "" {
		t.Fatal("read failure is invisible")
	}
}
