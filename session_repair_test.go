package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sessionRepairManaged(t *testing.T, a *app, c net.Conn, address string) *bitcoinSession {
	t.Helper()
	protocols := append(localGatewayProtocols(), gatewayProtocolSupport{ID: satlineProtocolID, Versions: []int{1}})
	s := &bitcoinSession{n: a.network, peer: &peerConn{conn: c, addr: address, gateway: true, bod: true, gatewayProtocols: protocols, localProtocols: protocols}, done: make(chan struct{}), slot: make(chan struct{}, 1), serveQueue: make(chan message, 4), view: networkPeerView{Address: address, State: "connected", Direction: "outbound", Role: "gateway_member", Gateway: true, BOD: true, Connected: time.Now()}}
	a.network.mu.Lock()
	a.network.sessions[address] = s
	a.network.nextDial = time.Now().Add(24 * time.Hour)
	a.network.mu.Unlock()
	var loops sync.WaitGroup
	loops.Add(2)
	go func() { defer loops.Done(); s.readLoop() }()
	go func() { defer loops.Done(); s.serviceLoop() }()
	t.Cleanup(func() { s.close("test cleanup"); loops.Wait() })
	return s
}

func sessionRepairRaw(t *testing.T) (*app, *bitcoinSession, net.Conn) {
	t.Helper()
	a := independentTestApp(t)
	local, remote := net.Pipe()
	t.Cleanup(func() { remote.Close() })
	s := sessionRepairManaged(t, a, local, "127.0.0.1:60200")
	return a, s, remote
}

func sessionRepairPair(t *testing.T) (*app, *app, *bitcoinSession, *bitcoinSession) {
	t.Helper()
	a, b := independentTestApp(t), independentTestApp(t)
	a.settings.ServeData, b.settings.ServeData = true, true
	left, right := net.Pipe()
	return a, b, sessionRepairManaged(t, a, left, "127.0.0.1:60201"), sessionRepairManaged(t, b, right, "127.0.0.1:60202")
}

func sessionRepairBlockRequest(raw []byte) []byte {
	var b bytes.Buffer
	b.WriteByte(1)
	_ = binary.Write(&b, binary.LittleEndian, uint32(2))
	h := hash256(raw[:80])
	b.Write(h[:])
	return b.Bytes()
}

func TestSessionLateRepliesCannotCompleteNextRequest(t *testing.T) {
	for _, kind := range []string{"bodmsg", "satmsg", "block", "notfound"} {
		t.Run(kind, func(t *testing.T) {
			_, s, remote := sessionRepairRaw(t)
			_ = remote.SetDeadline(time.Now().Add(4 * time.Second))
			command := kind
			var requests [2][]byte
			var replies [2]message
			for i := range requests {
				id := fmt.Sprintf("%024x", i+1)
				switch kind {
				case "bodmsg":
					requests[i], _ = json.Marshal(bodEnvelope{Wire: 1, Kind: "request", Type: "status", ID: id})
					replies[i].payload, _ = json.Marshal(bodEnvelope{Wire: 1, Kind: "response", Type: "status", ID: id, Status: "ok", Payload: json.RawMessage(`{}`)})
				case "satmsg":
					requests[i], _ = json.Marshal(satlineEnvelope{Wire: 1, Network: "mainnet", Kind: "request", ID: id})
					replies[i].payload, _ = json.Marshal(satlineEnvelope{Wire: 1, Network: "mainnet", Kind: "response", ID: id, Status: "not_cached"})
				default:
					command = "getdata"
					raw := make([]byte, 81)
					raw[0] = byte(i + 1)
					requests[i] = sessionRepairBlockRequest(raw)
					replies[i].payload = raw
					if kind == "notfound" {
						replies[i].payload = requests[i]
					}
				}
				replies[i].command = kind
			}
			firstRead := make(chan struct{})
			remoteDone := make(chan error, 1)
			go func() {
				m, err := readMessage(remote)
				if err != nil || m.command != command {
					remoteDone <- fmt.Errorf("first request: %q %v", m.command, err)
					close(firstRead)
					return
				}
				close(firstRead)
				m, err = readMessage(remote)
				if err == nil && m.command != command {
					err = fmt.Errorf("second request: %q", m.command)
				}
				if err == nil {
					err = writeMessage(remote, replies[0].command, replies[0].payload)
				}
				if err == nil {
					err = writeMessage(remote, replies[1].command, replies[1].payload)
				}
				remoteDone <- err
			}()
			ctx, cancel := context.WithCancel(context.Background())
			firstDone := make(chan error, 1)
			go func() {
				_, err := s.request(ctx, command, requests[0], func(m message) bool { return true })
				firstDone <- err
			}()
			<-firstRead
			cancel()
			if err := <-firstDone; !errors.Is(err, context.Canceled) {
				t.Fatalf("first request: %v", err)
			}
			requireSessionOpen(t, s)
			second, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			got, err := s.request(second, command, requests[1], func(m message) bool { return true })
			if err != nil || !bytes.Equal(got.payload, replies[1].payload) {
				t.Fatalf("late response crossed request boundary: %s %v", got.payload, err)
			}
			if err := <-remoteDone; err != nil {
				t.Fatal(err)
			}
			remote.Close()
			eventually051(t, func() bool { return len(s.n.listSessions()) == 0 })
		})
	}
}

func TestSessionHeaderTimeoutPreservesOtherServices(t *testing.T) {
	a, s, remote := sessionRepairRaw(t)
	s.peer.services = nodeNetworkService
	_ = remote.SetDeadline(time.Now().Add(4 * time.Second))
	firstRead := make(chan struct{})
	remoteDone := make(chan error, 1)
	go func() {
		m, err := readMessage(remote)
		close(firstRead)
		if err == nil && m.command != "getheaders" {
			err = fmt.Errorf("first command %s", m.command)
		}
		if err == nil {
			m, err = readMessage(remote)
		}
		var request []byte
		if err == nil && m.command != "getdata" {
			err = fmt.Errorf("next command %s", m.command)
		}
		request = append([]byte(nil), m.payload...)
		if err == nil {
			err = writeMessage(remote, "headers", []byte{0})
		}
		if err == nil {
			err = writeMessage(remote, "notfound", request)
		}
		remoteDone <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err := s.request(ctx, "getheaders", nil, func(m message) bool { return true })
	<-firstRead
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	requireSessionOpen(t, s)
	if _, err := s.request(context.Background(), "getheaders", nil, func(m message) bool { return true }); err == nil {
		t.Fatal("timed-out uncorrelated header role was reused")
	}
	selectCtx, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if _, err := a.network.headerSession(selectCtx); err == nil {
		t.Fatal("header selector chose a quarantined session")
	}
	if !s.transportEligible(true) {
		t.Fatal("header timeout disabled correlated archival block requests")
	}
	bodCtx, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if _, err := s.request(bodCtx, "getdata", sessionRepairBlockRequest(mainnetBlockOne(t)), func(m message) bool { return m.command == "notfound" }); err != nil {
		t.Fatalf("late headers poisoned Bitcoin block request: %v", err)
	}
	if err := <-remoteDone; err != nil {
		t.Fatal(err)
	}
}

func TestSessionProviderSelectionRequiresBitcoinService(t *testing.T) {
	a, s, _ := sessionRepairRaw(t)
	ctx, stop := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer stop()
	if _, err := a.network.headerSession(ctx); err == nil {
		t.Fatal("receive-only Gateway selected as a Bitcoin header provider")
	}
	a.network.mu.Lock()
	s.peer.services = nodeNetworkLimitedService
	a.network.mu.Unlock()
	selected, err := a.network.headerSession(context.Background())
	if err != nil || selected != s || s.transportEligible(true) {
		t.Fatalf("limited header provider eligibility: %v, %v", selected == s, err)
	}
}

func TestSessionInboundIsFullDuplexAndCountedOnce(t *testing.T) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Gateway peerhood is locked in this public-testing release; end-to-end serving resumes with feature promotion")
	}
	a, b := independentTestApp(t), independentTestApp(t)
	a.settings.ServeData, b.settings.ServeData = true, true
	if err := b.source.start(); err != nil {
		t.Fatal(err)
	}
	endpoint := b.source.tcp.Addr().String()
	a.network.add(endpoint, "manual_gateway", 0)
	a.network.maintain()
	eventually051(t, func() bool {
		for _, n := range []*bitcoinNetwork{a.network, b.network} {
			n.mu.Lock()
			ready := len(n.sessions) == 1
			for _, s := range n.sessions {
				ready = ready && s.view.BOD && !s.healthChecking
			}
			n.mu.Unlock()
			if !ready {
				return false
			}
		}
		return true
	})
	left, right := a.network.listSessions()[0], b.network.listSessions()[0]
	if left.peer.conn.LocalAddr().String() != right.peer.conn.RemoteAddr().String() || right.peer.conn.LocalAddr().String() != left.peer.conn.RemoteAddr().String() {
		t.Fatal("bidirectional peer views are not the same physical TCP connection")
	}
	requests := make(chan error, 2)
	for _, n := range []*bitcoinNetwork{a.network, b.network} {
		go func(n *bitcoinNetwork) {
			peers := n.gatewayPeers()
			if len(peers) != 1 {
				requests <- fmt.Errorf("expected one managed provider, got %d", len(peers))
				return
			}
			r, err := n.query(peers[0], overlayRequest{Type: "blockloc", Height: 0})
			if err == nil && (r.BlockLocation == nil || r.BlockLocation.BlockHash != genesisHashDisplay) {
				err = fmt.Errorf("bad reverse lookup")
			}
			requests <- err
		}(n)
	}
	for i := 0; i < 2; i++ {
		if err := <-requests; err != nil {
			t.Fatal(err)
		}
	}
	for i, app := range []*app{a, b} {
		snapshot := app.network.snapshot()
		if snapshot["connected"].(int) != 1 || snapshot["gateway_connected"].(int) != 1 || snapshot["bod_available"].(int) != 1 || snapshot["gateway_inbound"].(int) != i {
			t.Fatalf("duplicate/missing managed inventory: %+v", snapshot)
		}
		w := httptest.NewRecorder()
		app.handlePeers(w, httptest.NewRequest("GET", "/api/v1/peers", nil))
		var inventory struct {
			Connections []json.RawMessage `json:"connections"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &inventory); err != nil || len(inventory.Connections) != 1 {
			t.Fatalf("combined inventory: %s (%v)", w.Body.String(), err)
		}
	}
	b.network.mu.Lock()
	ephemeralQueued := b.network.addresses[right.peer.addr] != nil
	b.network.mu.Unlock()
	if ephemeralQueued {
		t.Fatal("inbound source port became a discovery target")
	}
	left.close("test dead transport")
	eventually051(t, func() bool { return len(b.network.listSessions()) == 0 })
}

func TestSessionBODBudgetRenewsWithoutDisconnect(t *testing.T) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Gateway peerhood is locked in this public-testing release; end-to-end serving resumes with feature promotion")
	}
	_, b, client, server := sessionRepairPair(t)
	var clock int64
	atomic.StoreInt64(&clock, time.Now().UnixNano())
	b.network.clock = func() time.Time { return time.Unix(0, atomic.LoadInt64(&clock)) }
	call := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := client.bodRequest(ctx, overlayRequest{Type: "status"})
		return err
	}
	for i := 0; i < 64; i++ {
		if err := call(); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	if err := call(); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("exhausted budget: %v", err)
	}
	requireSessionOpen(t, server)
	atomic.AddInt64(&clock, int64(time.Minute))
	for i := 0; i < 32; i++ {
		if err := call(); err != nil {
			t.Fatalf("renewed request %d: %v", i+65, err)
		}
	}
}

func TestSessionReceiveOnlyRepliesAndControlPlaneStayAvailable(t *testing.T) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Gateway peerhood is locked in this public-testing release; end-to-end serving resumes with feature promotion")
	}
	_, b, client, server := sessionRepairPair(t)
	b.settingsMu.Lock()
	b.settings.ServeData, b.settings.SatlineServePublished = false, false
	b.settingsMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := client.bodRequest(ctx, overlayRequest{Type: "status"}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("BOD unavailable response: %v", err)
	}
	env := satlineEnvelope{Wire: 1, Network: "mainnet", Kind: "request", ID: randomRequestID()}
	raw, _ := json.Marshal(env)
	m, err := client.request(ctx, "satmsg", raw, func(m message) bool { return true })
	var got satlineEnvelope
	if err != nil || json.Unmarshal(m.payload, &got) != nil || got.Status != "unsupported" {
		t.Fatalf("Satline unavailable: %s %v", m.payload, err)
	}
	block := make([]byte, 81)
	m, err = client.request(ctx, "getdata", sessionRepairBlockRequest(block), func(m message) bool { return true })
	if err != nil || m.command != "notfound" {
		t.Fatalf("Bitcoin unavailable: %s %v", m.command, err)
	}
	// The reverse direction still serves on this same connection.
	if _, err := server.bodRequest(ctx, overlayRequest{Type: "status"}); err != nil {
		t.Fatalf("receive-only node lost its client role: %v", err)
	}
	requireSessionOpen(t, client)
	requireSessionOpen(t, server)
}

func TestSessionRemoteAddressScanIsUnsupported(t *testing.T) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Gateway peerhood is locked in this public-testing release; end-to-end serving resumes with feature promotion")
	}
	_, _, client, _ := sessionRepairPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := client.bodRequest(ctx, overlayRequest{Type: "address_utxos", Address: "not-an-address"})
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("remote scan was dispatched: %v", err)
	}
}

func TestSessionSatlineBudgetRenewsWithoutDisconnect(t *testing.T) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Gateway peerhood is locked in this public-testing release; end-to-end serving resumes with feature promotion")
	}
	_, b, client, _ := sessionRepairPair(t)
	_, query, result := satlinePeerFixture(t)
	b.settingsMu.Lock()
	b.settings.SatlineEnabled, b.settings.SatlineServePublished = true, true
	b.settingsMu.Unlock()
	publication := satlinePublication{VerifierVersion: blockVerifierVersion, Schema: 1, Query: query, Start: *result.BirthSatpoint, Hops: result.Hops}
	if err := atomicWriteJSON(b.satlinePublicationPath(query), publication); err != nil {
		t.Fatal(err)
	}
	var clock int64
	atomic.StoreInt64(&clock, time.Now().UnixNano())
	b.network.clock = func() time.Time { return time.Unix(0, atomic.LoadInt64(&clock)) }
	request := satlineSegmentRequest{Query: query, Start: *result.BirthSatpoint, Limit: 1}
	for window := 0; window < 3; window++ {
		if window > 0 {
			atomic.AddInt64(&clock, int64(time.Minute))
			// Advance the independent existing global/IP token buckets too;
			// those remain a separate real-time limit from the session window.
			b.satlineNetMu.Lock()
			for key, bucket := range b.satlineRates {
				bucket.Updated = time.Now().Add(-time.Minute)
				b.satlineRates[key] = bucket
			}
			b.satlineNetMu.Unlock()
		}
		for i := 0; i < 8; i++ {
			env := satlineEnvelope{Wire: 1, Network: "mainnet", Kind: "request", ID: randomRequestID(), Request: &request}
			raw, _ := json.Marshal(env)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			m, err := client.request(ctx, "satmsg", raw, func(m message) bool { return true })
			cancel()
			var got satlineEnvelope
			if err != nil || strictSatlineJSON(m.payload, &got) != nil || got.Status != "ok" || got.Segment == nil || len(got.Segment.Hops) != 1 {
				t.Fatalf("Satline request %d: %s %v", window*8+i+1, m.payload, err)
			}
		}
	}
}

func TestSessionReceiveOnlyGatewayControlReplies(t *testing.T) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Gateway peerhood is locked in this public-testing release; end-to-end serving resumes with feature promotion")
	}
	a, s, remote := sessionRepairRaw(t)
	a.settingsMu.Lock()
	a.settings.ServeData = false
	a.settingsMu.Unlock()
	_ = remote.SetDeadline(time.Now().Add(2 * time.Second))
	hello := gatewayMessage{Wire: 1, Kind: "hello", ID: randomRequestID(), Protocols: localGatewayProtocols()}
	raw, _ := json.Marshal(hello)
	if err := writeMessage(remote, "gwmsg", raw); err != nil {
		t.Fatal(err)
	}
	m, err := readMessage(remote)
	var reply gatewayMessage
	if err != nil || json.Unmarshal(m.payload, &reply) != nil || reply.Kind != "hello_ack" || reply.ID != hello.ID {
		t.Fatalf("Gateway control reply: %s %v", m.payload, err)
	}
	if err := writeMessage(remote, "getaddr", nil); err != nil {
		t.Fatal(err)
	}
	m, err = readMessage(remote)
	if err != nil || m.command != "addr" {
		t.Fatalf("discovery control reply: %s %v", m.command, err)
	}
	requireSessionOpen(t, s)
}

func TestSessionOutboundDisableKeepsInboundServing(t *testing.T) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Gateway peerhood is locked in this public-testing release; end-to-end serving resumes with feature promotion")
	}
	_, b, client, server := sessionRepairPair(t)
	b.network.mu.Lock()
	server.view.Direction, server.view.Role = "inbound", "gateway_inbound"
	b.network.mu.Unlock()
	b.settingsMu.Lock()
	b.settings.NetworkDisabled = true
	b.settingsMu.Unlock()
	b.network.maintain()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.bodRequest(ctx, overlayRequest{Type: "status"}); err != nil {
		t.Fatalf("outbound disable stopped independent inbound serving: %v", err)
	}
	requireSessionOpen(t, server)
}

func TestSessionCancelledRequestDoesNotWaitForServiceWriter(t *testing.T) {
	_, s, remote := sessionRepairRaw(t)
	s.writeMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := s.request(ctx, "getheaders", nil, func(m message) bool { return true })
		finished <- err
	}()
	select {
	case err := <-finished:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("cancelled writer wait: %v", err)
		}
	case <-time.After(time.Second):
		s.writeMu.Unlock()
		t.Fatal("request cancellation waited behind the serving writer")
	}
	s.writeMu.Unlock()
	_ = remote.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	if _, err := readMessage(remote); err == nil {
		t.Fatal("cancelled request was written after its deadline")
	}
	requireSessionOpen(t, s)
}

func TestSessionSlowProviderDoesNotBlockPingOrReverseRequest(t *testing.T) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Gateway peerhood is locked in this public-testing release; end-to-end serving resumes with feature promotion")
	}
	a, s, remote := sessionRepairRaw(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
			ID     any    `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request.Method == "getblockhash" {
			enterOnce.Do(func() { close(entered) })
			<-release
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": request.ID, "result": genesisHashDisplay, "error": nil})
	}))
	t.Cleanup(rpc.Close)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(rpc.URL, "http://"))
	numericPort, _ := strconv.Atoi(port)
	a.settingsMu.Lock()
	a.settings.CoreDisabled, a.settings.ServeData = false, true
	a.settings.BitcoinDataDir, a.settings.RPCPort = t.TempDir(), numericPort
	a.settings.RPCAuthMode, a.settings.RPCUser, a.settings.RPCPassword = "userpass", "fixture", "fixture"
	settings := a.settings
	a.settingsMu.Unlock()
	key, _ := json.Marshal([]any{settings.CoreDisabled, settings.BitcoinDataDir, settings.RPCPort, settings.RPCAuthMode, settings.RPCUser, settings.RPCPassword})
	coreStatusCache.Lock()
	coreStatusCache.entries[string(key)] = coreStatusEntry{at: time.Now(), value: coreStatus{Connected: true, Height: 0, BestBlockHash: genesisHashDisplay}}
	coreStatusCache.Unlock()
	_ = remote.SetDeadline(time.Now().Add(3 * time.Second))
	incoming := bodEnvelope{Wire: 1, Kind: "request", Type: "block_location", ID: randomRequestID(), Payload: json.RawMessage(`{"height":0}`)}
	raw, _ := json.Marshal(incoming)
	if err := writeMessage(remote, "bodmsg", raw); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("provider did not enter controlled slow RPC")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := s.bodRequest(ctx, overlayRequest{Type: "status"}); finished <- err }()
	m, err := readMessage(remote)
	var outgoing bodEnvelope
	if err != nil || m.command != "bodmsg" || json.Unmarshal(m.payload, &outgoing) != nil || outgoing.Kind != "request" {
		t.Fatalf("reverse request blocked by provider: %s %v", m.payload, err)
	}
	if err := writeMessage(remote, "ping", make([]byte, 8)); err != nil {
		t.Fatal(err)
	}
	m, err = readMessage(remote)
	if err != nil || m.command != "pong" {
		t.Fatalf("slow provider blocked ping: %s %v", m.command, err)
	}
	raw, _ = json.Marshal(bodEnvelope{Wire: 1, Kind: "response", Type: outgoing.Type, ID: outgoing.ID, Status: "ok", Payload: json.RawMessage(`{}`)})
	if err := writeMessage(remote, "bodmsg", raw); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatalf("slow provider blocked opposite response: %v", err)
	}
	releaseOnce.Do(func() { close(release) })
	m, err = readMessage(remote)
	var reply bodEnvelope
	if err != nil || json.Unmarshal(m.payload, &reply) != nil || reply.ID != incoming.ID || reply.Status != "ok" {
		t.Fatalf("released provider response: %s %v", m.payload, err)
	}
}

func TestSessionIdleRepliesAreDiscarded(t *testing.T) {
	_, s, remote := sessionRepairRaw(t)
	_ = remote.SetDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < 12; i++ {
		if err := writeMessage(remote, "block", make([]byte, 81)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeMessage(remote, "ping", make([]byte, 8)); err != nil {
		t.Fatal(err)
	}
	m, err := readMessage(remote)
	if err != nil || m.command != "pong" {
		t.Fatalf("idle processing barrier: %s %v", m.command, err)
	}
	s.replyMu.Lock()
	pending := s.pendingReply
	s.replyMu.Unlock()
	if pending != nil {
		t.Fatal("unsolicited replies created a request mailbox")
	}
	requireSessionOpen(t, s)
}

func requireSessionOpen(t *testing.T, s *bitcoinSession) {
	t.Helper()
	select {
	case <-s.done:
		t.Fatal("session unexpectedly closed")
	default:
	}
}
