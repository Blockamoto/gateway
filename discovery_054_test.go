package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func Test054StablePublicElectionAndExpiry(t *testing.T) {
	a := independentTestApp(t)
	if e := a.source.start(); e != nil {
		t.Fatal(e)
	}
	n := a.network
	now := time.Now().UTC()
	n.clock = func() time.Time { return now }
	n.mu.Lock()
	n.observedIPs = map[string]string{"a": "8.8.4.4", "b": "8.8.4.4", "c": "1.1.1.1", "d": "1.1.1.1"}
	for k := range n.observedIPs {
		n.observedAt[k] = now
	}
	n.observedIPs["undated1"] = "4.4.4.4"
	n.observedIPs["undated2"] = "4.4.4.4"
	if n.publicIPVotesLocked(now)["4.4.4.4"] != 0 {
		t.Fatal("undated observation counted as fresh")
	}
	n.mu.Unlock()
	first, source := a.advertisedGatewayEndpoint()
	for i := 0; i < 2048; i++ {
		got, _ := a.advertisedGatewayEndpoint()
		if got != first {
			t.Fatalf("unstable selection %s -> %s", first, got)
		}
	}
	if source != "two_independent_bitcoin_peer_observations" || !strings.HasPrefix(first, "1.1.1.1:") {
		t.Fatalf("non-deterministic tie %s %s", first, source)
	}
	n.mu.Lock()
	n.observedIPs["e"] = "8.8.4.4"
	n.observedAt["e"] = now
	n.mu.Unlock()
	if got, _ := a.advertisedGatewayEndpoint(); got != first {
		t.Fatal("supported choice changed on vote count")
	}
	now = now.Add(31 * time.Minute)
	if got, _ := a.advertisedGatewayEndpoint(); got != "" {
		t.Fatalf("expired claims still advertised: %s", got)
	}
	n.mu.Lock()
	n.observedAt["a"] = now
	n.observedAt["b"] = now
	n.mu.Unlock()
	if got, _ := a.advertisedGatewayEndpoint(); got != "" {
		t.Fatal("replacement skipped stabilization")
	}
	now = now.Add(2 * time.Minute)
	if got, _ := a.advertisedGatewayEndpoint(); !strings.HasPrefix(got, "8.8.4.4:") {
		t.Fatalf("replacement never selected %s", got)
	}
	a.settingsMu.Lock()
	a.settings.AdvertiseAddr = "example.test:49123"
	a.settingsMu.Unlock()
	if got, _ := a.advertisedGatewayEndpoint(); got != "example.test:49123" {
		t.Fatal("explicit mapped port lost")
	}
	t.Log("2048 identical selection reads; supported choice retained; expired evidence withdrawn; replacement stabilized; explicit port preserved")
}

func Test054AnnouncementSnapshotAndRateLimit(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	now := time.Now().UTC()
	n.clock = func() time.Time { return now }
	sessions := []*bitcoinSession{}
	received := make(chan []advertisedPeer, 8)
	for i := 0; i < 2; i++ {
		c, remote := net.Pipe()
		t.Cleanup(func() { c.Close(); remote.Close() })
		p := &peerConn{conn: c, addr: fmt.Sprintf("127.0.0.1:%d", 12300+i), wantsAddrV2: true}
		session := &bitcoinSession{n: n, peer: p, done: make(chan struct{}), slot: make(chan struct{}, 1), view: networkPeerView{Address: p.addr, Role: "bitcoin_transport"}}
		n.sessions[p.addr] = session
		sessions = append(sessions, session)
		go func() {
			for {
				m, e := readMessage(remote)
				if e != nil {
					return
				}
				rows, e := parseAddressMessage(m)
				if e == nil {
					received <- rows
				}
			}
		}()
	}
	row := advertisedPeer{Addr: "8.8.4.4:48333", Services: gatewayExperimentalServiceBit | nodeNetworkService | nodeWitnessService}
	for _, s := range sessions {
		if !n.sendAnnouncement054(s, row) {
			t.Fatal("first announcement missing")
		}
	}
	for range sessions {
		select {
		case got := <-received:
			if len(got) != 1 || got[0].Addr != row.Addr || got[0].Services != row.Services {
				t.Fatal("snapshot changed per recipient")
			}
		case <-time.After(time.Second):
			t.Fatal("no wire announcement")
		}
	}
	row.Addr = "1.1.1.1:48333"
	now = now.Add(time.Minute)
	original := sessions[0]
	replacement := &bitcoinSession{n: n, peer: original.peer, slot: make(chan struct{}, 1), done: make(chan struct{}), view: original.view}
	n.sessions[original.peer.addr] = replacement
	if n.sendAnnouncement054(replacement, row) {
		t.Fatal("reconnect bypassed recipient announcement limit")
	}
	n.sessions[original.peer.addr] = original
	for _, s := range sessions {
		if n.sendAnnouncement054(s, row) {
			t.Fatal("changed endpoint bypassed rate limit")
		}
	}
	now = now.Add(5 * time.Minute)
	for _, s := range sessions {
		if !n.sendAnnouncement054(s, row) {
			t.Fatal("bounded replacement blocked permanently")
		}
	}
	t.Log("Two actual Bitcoin address messages used identical flags/endpoint; changed endpoint suppressed inside five-minute window")
}
func Test054NegativeCheckSurvivesEOFAndGossip(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	now := time.Now().UTC()
	n.clock = func() time.Time { return now }
	n.policy.BitcoinSlots = 0
	n.policy.ScoutSlots = 0
	n.policy.GatewaySlots = 1
	n.add("8.8.8.8:48333", "bitcoin_addr:first", ^uint64(0))
	n.mu.Lock()
	x := n.ledger["8.8.8.8:48333"]
	x.Attempts = 1
	x.GatewayChecks = 1
	x.GatewayAttempts = 1
	x.DirectAt = now
	x.LastNegotiation = "no_gateway"
	x.DirectServices = nodeNetworkService
	n.recordFailureLocked(x, "connection_failed", "EOF")
	deadline := n.gatewayRecheckAfterLocked(x)
	n.mu.Unlock()
	now = now.Add(time.Minute)
	n.add(x.Address, "bitcoin_addr:repeat", gatewayExperimentalServiceBit|nodeWitnessService)
	n.mu.Lock()
	n.tokens = 1
	plans := n.planDiscoveryLocked(now, true)
	n.mu.Unlock()
	if len(plans) != 0 {
		t.Fatal("EOF or precise rumor bypassed a completed negative Gateway check")
	}
	n.mu.Lock()
	n.policy.ScoutSlots = 1
	n.tokens = 1
	plans = n.planDiscoveryLocked(now, true)
	n.policy.ScoutSlots = 0
	n.mu.Unlock()
	if len(plans) != 0 {
		t.Fatal("candidate recheck bypassed by relabelling it ordinary exploration")
	}
	if !deadline.Equal(x.DirectAt.Add(30 * time.Minute)) {
		t.Fatal("wrong migrated negative gate")
	}
	if !n.persistLedger() {
		t.Fatal(n.ledgerError)
	}
	// Persistence of independent state and fallback derivation is exercised by
	// reopening the committed journal, not clearing history to pass the test.
	restored := newBitcoinNetwork(a)
	defer restored.cancel()
	restored.clock = n.clock
	restored.policy = n.policy
	restored.mu.Lock()
	if !restored.gatewayRecheckAfterLocked(restored.ledger[x.Address]).Equal(deadline) {
		t.Fatal("restart lost negative gate")
	}
	restored.mu.Unlock()
	now = now.Add(30 * time.Minute)
	n.mu.Lock()
	n.tokens = 1
	plans = n.planDiscoveryLocked(now, true)
	n.mu.Unlock()
	if len(plans) != 1 {
		t.Fatal("due Gateway recheck lost forever")
	}
	t.Log("EOF remains recorded, but a 30-minute direct-negative check gate survives rumors and journal restart")
}
func Test054HintClassFairnessAndProvenance(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	n.policy.BitcoinSlots = 0
	n.policy.ScoutSlots = 0
	n.policy.GatewaySlots = 1
	now := time.Now().UTC()
	n.clock = func() time.Time { return now }
	for i := 0; i < 100; i++ {
		n.add(fmt.Sprintf("11.0.0.%d:48333", i+1), "bitcoin_addr:weak", ^uint64(0))
		n.add(fmt.Sprintf("12.0.0.%d:48333", i+1), "bitcoin_addr:specific", gatewayExperimentalServiceBit|nodeWitnessService)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	specific, weak := 0, 0
	for i := 0; i < 32; i++ {
		n.tokens = 1
		plans := n.planDiscoveryLocked(now, true)
		if len(plans) != 1 {
			t.Fatalf("no plan at %d", i)
		}
		x := n.ledger[plans[0].Address]
		if x.SpecificHint {
			specific++
		} else {
			weak++
		}
		delete(n.dialing, x.Address)
		x.InFlight = false
		x.RetryAfter = now.Add(time.Hour)
	}
	if specific != 24 || weak != 8 {
		t.Fatalf("fairness specific=%d weak=%d", specific, weak)
	}
	x := n.ledger["11.0.0.1:48333"]
	if x.FirstHintMask != "0xffffffffffffffff" || x.FirstHintSource != "bitcoin_addr:weak" {
		t.Fatal("exact claim provenance missing")
	}
	if hintQuality054(x) != "weak_all_bits" {
		t.Fatal("all-ones mistaken for specific hint")
	}
	t.Logf("Saturated queues: %d specific and %d weak first attempts under the same 32 global tokens", specific, weak)
}
func Test054ImportedUnknownAndCandidatePagination(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	for i := 0; i < 112; i++ {
		n.add(fmt.Sprintf("13.0.0.%d:48333", i+1), "bitcoin_addr:fixture", ^uint64(0))
	}
	n.mu.Lock()
	x := n.ledger["13.0.0.1:48333"]
	x.Imported = true
	x.Attempts = 1
	x.LastSuccess = n.now()
	x.LastOutcome = "imported_attempt_count_lower_bound"
	n.touchLocked(x)
	summary := n.ledgerSummaryLocked(n.now())
	n.mu.Unlock()
	if summary["hint_outcomes"].(map[string]int)["historical_unknown"] != 1 {
		t.Fatal("legacy success labelled unreachable")
	}
	seen := map[string]bool{}
	offset := 0
	for {
		w := httptest.NewRecorder()
		a.handleDiscovery(w, httptest.NewRequest("GET", fmt.Sprintf("/api/v1/discovery?view=ledger&filter=hints&limit=25&offset=%d", offset), nil))
		var page struct {
			Rows []struct {
				Record bitcoinAddress `json:"record"`
			} `json:"rows"`
			Next int `json:"next_offset"`
		}
		if json.Unmarshal(w.Body.Bytes(), &page) != nil || w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		for _, r := range page.Rows {
			if seen[r.Record.Address] {
				t.Fatal("duplicate page item")
			}
			seen[r.Record.Address] = true
		}
		if page.Next < 0 {
			break
		}
		offset = page.Next
	}
	if len(seen) != 112 {
		t.Fatalf("pagination silently truncated %d", len(seen))
	}
	w := httptest.NewRecorder()
	a.handleDiscovery(w, httptest.NewRequest("GET", "/api/v1/discovery?export=diagnostics", nil))
	if !strings.Contains(w.Body.String(), "18446744073709551615") || strings.Contains(w.Body.String(), "18446744073709552000") {
		t.Fatal("server export lost uint64 precision")
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("not a lossless download")
	}
	t.Log("112 unique candidate rows paginated; imported ambiguity retained; exact uint64 diagnostics bypass browser rounding")
}
func Test054GenesisOnlyGatewayRemainsConnected(t *testing.T) {
	server := independentTestApp(t)
	server.settings.ServeData = true
	if e := server.source.start(); e != nil {
		t.Fatal(e)
	}
	relay := ordinaryRelay052(t, []advertisedPeer{{Addr: server.source.tcp.Addr().String(), Services: gatewayExperimentalServiceBit | nodeWitnessService}}, false)
	client := independentTestApp(t)
	n := client.network
	fastDiscovery052(n)
	n.policy.ProbeWindow = time.Minute
	n.add(relay.address, "ordinary_bitcoin_fixture", nodeNetworkService)
	endpoint := server.source.tcp.Addr().String()
	pump052(t, n, func() bool {
		n.mu.Lock()
		defer n.mu.Unlock()
		x := n.ledger[endpoint]
		return x != nil && x.LastNegotiation == "gateway_negotiated" && x.LastServiceOutcome == "no_non_genesis_sample"
	})
	n.mu.Lock()
	s := n.sessions[endpoint]
	if s == nil || s.scout {
		n.mu.Unlock()
		t.Fatal("negotiated Gateway not retained")
	}
	s.view.Connected = n.now().Add(-3 * time.Minute)
	n.mu.Unlock()
	n.maintain()
	snap := n.discoverySnapshot()
	if snap["gateway_connected"].(int) < 1 || snap["useful_gateway_peers"].(int) != 0 {
		t.Fatal("membership conflated with useful BOD service")
	}
	n.mu.Lock()
	if n.sessions[endpoint] != s || s.scout {
		t.Fatal("empty Gateway retired as scout")
	}
	n.mu.Unlock()
	if x := snap["discovery"].(discoveryState); x.Mode != "seeking_services" {
		t.Fatalf("wrong partial state %s", x.Mode)
	}
	t.Log("Unknown genesis-only Gateway learned via ordinary Bitcoin TCP address exchange, negotiated and retained beyond scout timeout without a false useful-service receipt")
}
func Test054SampleSkipsLeadingGenesisRange(t *testing.T) {
	resp := overlayResponse{Status: &bodStatus{Snapshot: chainSnapshot{Height: 100}, BlockRanges: []heightInterval{{From: 0, To: 0}, {From: 17, To: 20}}}}
	height, fetch := serviceSample054(resp, 100)
	if height != 17 || !fetch {
		t.Fatalf("ignored later range: %d %v", height, fetch)
	}
	resp.Status.BlockRanges = []heightInterval{{From: 0, To: 0}}
	height, fetch = serviceSample054(resp, 100)
	if height != 1 || fetch {
		t.Fatal("lost header-only locator sample")
	}
}

func Test054OverflowMemberCompletesBoundedServiceProbe(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	now := time.Now().UTC()
	n.clock = func() time.Time { return now }
	n.noBootstrap = true
	n.tokens = 0
	n.tokenAt = now
	n.policy.BitcoinSlots = 0
	n.policy.ScoutSlots = 0
	n.policy.GatewaySlots = 2
	rows := []*bitcoinSession{}
	for i := 0; i < 3; i++ {
		c, r := net.Pipe()
		t.Cleanup(func() { c.Close(); r.Close() })
		addr := fmt.Sprintf("127.0.0.1:%d", 14000+i)
		s := &bitcoinSession{n: n, peer: &peerConn{conn: c, addr: addr, gateway: true}, done: make(chan struct{}), slot: make(chan struct{}, 1), scout: i == 2, healthChecking: i == 2, lastServiceCheck: now, view: networkPeerView{Address: addr, Gateway: true, Connected: now.Add(time.Duration(i-180) * time.Second), Role: "gateway_member"}}
		n.sessions[addr] = s
		rows = append(rows, s)
	}
	n.maintain()
	n.mu.Lock()
	retained := n.sessions[rows[2].peer.addr] != nil
	rows[2].healthChecking = false
	n.mu.Unlock()
	if !retained {
		t.Fatal("in-progress bounded service probe retired between requests")
	}
	n.maintain()
	n.mu.Lock()
	_, stillPresent := n.sessions[rows[2].peer.addr]
	n.mu.Unlock()
	if stillPresent {
		t.Fatal("overflow membership not bounded after service check completed")
	}
	t.Log("Two retained members plus temporary service probe; probe survives discovery window, then excess connection retires without increasing persistent membership cap")
}
