package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func Test052AddressV2NegotiatedEncoding(t *testing.T) {
	rows := []advertisedPeer{{Addr: "8.8.4.4:48333", Services: gatewayExperimentalServiceBit | nodeNetworkService | nodeWitnessService}, {Addr: "[2606:4700:4700::1111]:49000", Services: nodeNetworkService}}
	for _, v2 := range []bool{false, true} {
		command, payload := addressMessage(rows, v2)
		if (command == "addrv2") != v2 {
			t.Fatal("ignored negotiated preference")
		}
		decoded, e := parseAddressMessage(message{command: command, payload: payload})
		if e != nil || len(decoded) != len(rows) {
			t.Fatalf("roundtrip %v %+v", e, decoded)
		}
		for i := range rows {
			if decoded[i].Addr != rows[i].Addr || decoded[i].Services != rows[i].Services {
				t.Fatalf("port or service field changed: %+v", decoded)
			}
		}
	}
	// Known non-IP networks have exact size rules even though this client cannot dial them.
	malformed := []byte{1, 0, 0, 0, 0, 1, 4, 1, 0, 0, 1}
	if _, e := parseAddressMessage(message{command: "addrv2", payload: malformed}); e == nil {
		t.Fatal("bad Tor v3 size accepted")
	}
}
func Test052DiscoveryControlsPreserveConsent(t *testing.T) {
	a := independentTestApp(t)
	a.settings.NetworkDisabled = true
	a.settings.BitcoinPeers = []string{"8.8.8.8:8333"}
	a.settings.PrivacyMode = true
	a.settings.ServeData = false
	for _, action := range []string{"pause", "resume", "advertise"} {
		body, _ := json.Marshal(map[string]string{"action": action, "address": "example.org:49000"})
		w := httptest.NewRecorder()
		a.handleDiscovery(w, httptest.NewRequest("POST", "/api/v1/discovery", bytes.NewReader(body)))
		if w.Code != 200 {
			t.Fatalf("%s: %s", action, w.Body.String())
		}
		if a.settings.DiscoveryPaused != (action == "pause") || !a.settings.PrivacyMode || a.settings.ServeData || !a.settings.NetworkDisabled || len(a.settings.BitcoinPeers) != 1 {
			t.Fatal("discovery action changed unrelated consent")
		}
	}
	if a.settings.AdvertiseAddr != "example.org:49000" {
		t.Fatal("external custom port lost")
	}
	for _, body := range []string{`{"action":"advertise","address":"http://bad"}`, `{"action":"invent"}`} {
		w := httptest.NewRecorder()
		a.handleDiscovery(w, httptest.NewRequest("POST", "/api/v1/discovery", bytes.NewBufferString(body)))
		if w.Code != 400 {
			t.Fatalf("invalid control accepted: %s", w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	a.handleDiscovery(w, httptest.NewRequest("GET", "/api/v1/discovery", nil))
	if bytes.Contains(w.Body.Bytes(), []byte("rpc_password")) {
		t.Fatal("discovery exposed RPC credentials")
	}
}
func Test052MountedPreparationDoesNotRequireCore(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "blk00000.dat"), []byte("fixture bytes not scanned by detection"), 0600); e != nil {
		t.Fatal(e)
	}
	s := defaultSettings()
	s.CoreDisabled = true
	s.CoreMountDisabled = true
	s.BitcoinBlocksDir = dir
	v := mountedPreparationInfo(s)
	if v["available"] != false || v["block_files_found"] != true {
		t.Fatalf("detection changed mount consent: %+v", v)
	}
	s.CoreMountDisabled = false
	v = mountedPreparationInfo(s)
	if v["available"] != true || v["record_bytes"] != coreIndexRecordSize {
		t.Fatalf("stopped Core blocked locator preparation: %+v", v)
	}
	s.BitcoinBlocksDir = filepath.Join(dir, "missing")
	if mountedPreparationInfo(s)["available"] != false {
		t.Fatal("missing files offered preparation")
	}
}
func Test052SetupRetainsReviewedIntegrationChoices(t *testing.T) {
	a := independentTestApp(t)
	a.settings.NetworkDisabled = true
	w := httptest.NewRecorder()
	a.handleSetup(w, httptest.NewRequest("POST", "/api/v1/setup", bytes.NewBufferString(`{"action":"save","stage":3,"integration_reviewed":true}`)))
	if w.Code != 200 || !a.readSetup().IntegrationReviewed {
		t.Fatalf("review not retained: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	a.handleSetup(w, httptest.NewRequest("POST", "/api/v1/setup", bytes.NewBufferString(`{"action":"save","stage":4}`)))
	if w.Code != 200 || !a.readSetup().IntegrationReviewed {
		t.Fatal("next stage reset integration consent")
	}
}
func Test052DataCoverageSeparatesPrivatePossession(t *testing.T) {
	a := independentTestApp(t)
	a.settings.NetworkDisabled = true
	seedBlockOne052(t, a, true)
	w := httptest.NewRecorder()
	a.handleDataCoverage(w, httptest.NewRequest("GET", "/api/v1/data-coverage", nil))
	var v struct {
		Cache struct {
			Blocks  int              `json:"blocks"`
			Private int              `json:"private_blocks"`
			Share   []heightInterval `json:"sharing_eligible_ranges"`
		} `json:"cache"`
	}
	if json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Cache.Blocks != 1 || v.Cache.Private != 1 || len(v.Cache.Share) != 0 {
		t.Fatalf("private cache misrepresented: %s", w.Body.String())
	}
}

func Test052ServiceLossRecoversWithoutSocketLoss(t *testing.T) {
	server := independentTestApp(t)
	server.settings.ServeData = true
	server.settings.ShareCache = true
	seedBlockOne052(t, server, false)
	if e := server.source.start(); e != nil {
		t.Fatal(e)
	}
	relay := ordinaryRelay052(t, []advertisedPeer{{Addr: server.source.tcp.Addr().String(), Services: gatewayExperimentalServiceBit | nodeWitnessService}}, false)
	client := independentTestApp(t)
	fastDiscovery052(client.network)
	client.network.add(relay.address, "bitcoin_seed_fixture", nodeNetworkService)
	pump052(t, client.network, func() bool { return useful052(client.network) })
	var session *bitcoinSession
	for _, s := range client.network.listSessions() {
		if s.peer.gateway {
			session = s
			break
		}
	}
	if session == nil {
		t.Fatal("missing Gateway session")
	}
	server.settingsMu.Lock()
	server.settings.ServeData = false
	server.settingsMu.Unlock()
	client.network.checkServiceHealth(session)
	client.network.maintain()
	if mode := client.network.snapshot()["discovery"].(discoveryState).Mode; mode != "recover" {
		t.Fatalf("service loss did not restart search: %s", mode)
	}
}

func Test052RecoveryFindsAnotherLearnedGateway(t *testing.T) {
	servers := []*app{independentTestApp(t), independentTestApp(t)}
	rows := []advertisedPeer{}
	for _, server := range servers {
		server.settings.ServeData = true
		server.settings.ShareCache = true
		seedBlockOne052(t, server, false)
		if e := server.source.start(); e != nil {
			t.Fatal(e)
		}
		rows = append(rows, advertisedPeer{Addr: server.source.tcp.Addr().String(), Services: gatewayExperimentalServiceBit | nodeWitnessService})
	}
	relay := ordinaryRelay052(t, rows, false)
	client := independentTestApp(t)
	fastDiscovery052(client.network)
	client.network.add(relay.address, "bitcoin_seed_fixture", nodeNetworkService)
	pump052(t, client.network, func() bool { return useful052(client.network) })
	first := client.network.snapshot()["discovery"].(discoveryState).FirstUseful.Peer
	var session *bitcoinSession
	for _, s := range client.network.listSessions() {
		if s.peer.addr == first {
			session = s
		}
	}
	for _, server := range servers {
		if server.source.tcp.Addr().String() == first {
			server.settingsMu.Lock()
			server.settings.ServeData = false
			server.settingsMu.Unlock()
		}
	}
	client.network.checkServiceHealth(session)
	pump052(t, client.network, func() bool {
		d := client.network.snapshot()["discovery"].(discoveryState)
		return d.LastUseful != nil && d.LastUseful.Peer != first && useful052(client.network)
	})
	d := client.network.snapshot()["discovery"].(discoveryState)
	if d.FirstUseful.Peer != first || d.LastUseful.Source != "bitcoin_addr:"+relay.address || d.LastUseful.Bytes <= 0 {
		t.Fatalf("recovery lost route/proof: %+v", d)
	}
	t.Logf("Recovered from %s to %s, both learned through %s; checked block bytes %d", first, d.LastUseful.Peer, relay.address, d.LastUseful.Bytes)
}
