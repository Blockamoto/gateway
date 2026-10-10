package main

// The independent connection pool owns its sockets. Header, block and BOD
// requests share those sessions; Core RPC and inbound serving are independent.
import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxBitcoinAddresses = 4096

type bitcoinAddress struct {
	Address     string    `json:"address"`
	Source      string    `json:"source"`
	Services    uint64    `json:"services"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSuccess time.Time `json:"last_success,omitempty"`
	RetryAfter  time.Time `json:"retry_after,omitempty"`
	Failures    int       `json:"failures"`
	LastError   string    `json:"last_error,omitempty"`
	LastAttempt time.Time `json:"last_attempt,omitempty"`
}
type networkPeerView struct {
	Role            string                   `json:"role,omitempty"`
	ID              string                   `json:"id"`
	Address         string                   `json:"address"`
	Owner           string                   `json:"owner"`
	Direction       string                   `json:"direction"`
	Transport       string                   `json:"transport"`
	Source          string                   `json:"discovery_source"`
	State           string                   `json:"state"`
	Services        uint64                   `json:"services"`
	ServiceHex      string                   `json:"services_hex"`
	ProtocolVersion int32                    `json:"protocol_version"`
	Protocols       []string                 `json:"protocols"`
	Negotiated      []gatewayProtocolSupport `json:"negotiated_protocols,omitempty"`
	Gateway         bool                     `json:"gateway"`
	BOD             bool                     `json:"bod"`
	TargetHeight    int64                    `json:"target_height_estimate"`
	Connected       time.Time                `json:"connected,omitempty"`
	Updated         time.Time                `json:"updated"`
	LastWork        string                   `json:"last_work,omitempty"`
	Received        uint64                   `json:"bytes_received"`
	Sent            uint64                   `json:"bytes_sent"`
	LastError       string                   `json:"last_error,omitempty"`
}
type bitcoinNetwork struct {
	app              *app
	ctx              context.Context
	cancel           context.CancelFunc
	mu               sync.Mutex
	addresses        map[string]*bitcoinAddress
	sessions         map[string]*bitcoinSession
	dialing          map[string]networkPeerView
	dialCancels      map[string]context.CancelFunc
	recent           []networkPeerView
	events           []bitcoinNetworkEvent
	bootstrapAt      time.Time
	bootstrapBusy    bool
	bootstrapError   string
	saveMu           sync.Mutex
	persistenceError string
	headerCursor     uint64
	nextDial         time.Time
	clock            func() time.Time
	noBootstrap      bool
	allowPrivate     bool
}
type bitcoinSession struct {
	addrReceived      bool // protected by n.mu
	addrAsked         bool // protected by n.mu
	inboundAddrAsked  bool // protected by n.mu
	serveQueue        chan message
	retired           bool                 // protected by n.mu; shutdown is not a peer failure
	headerUnavailable bool                 // protected by n.mu; uncorrelated headers timed out
	bodBudget         sessionServiceBudget // used only by serviceLoop
	satBudget         sessionServiceBudget // used only by serviceLoop
	satIDs            map[string]bool      // used only by serviceLoop
	requestSuccess    bool                 // protected by n.mu
	healthChecking    bool                 // protected by n.mu
	n                 *bitcoinNetwork
	peer              *peerConn
	view              networkPeerView  // protected by n.mu
	status            *overlayResponse // observed BOD status, protected by n.mu
	done              chan struct{}
	once              sync.Once
	writeMu           sync.Mutex
	replyMu           sync.Mutex
	pendingReply      *sessionPendingReply // protected by replyMu
	slot              chan struct{}
}

func newBitcoinNetwork(a *app) *bitcoinNetwork {
	ctx, cancel := context.WithCancel(context.Background())
	n := &bitcoinNetwork{app: a, ctx: ctx, cancel: cancel, addresses: map[string]*bitcoinAddress{}, sessions: map[string]*bitcoinSession{}, dialing: map[string]networkPeerView{}, dialCancels: map[string]context.CancelFunc{}}
	n.loadBitcoinAddresses()
	return n
}
func validPeerEndpoint(address string) bool {
	host, port, e := net.SplitHostPort(address)
	if e != nil || host == "" || len(host) > 253 || strings.ContainsAny(host, "/\\ ?#\r\n\x00") {
		return false
	}
	p, e := strconv.Atoi(port)
	return e == nil && p > 0 && p < 65536
}
func (n *bitcoinNetwork) enabled() bool {
	n.app.settingsMu.RLock()
	defer n.app.settingsMu.RUnlock()
	return !n.app.settings.NetworkDisabled
}
func (n *bitcoinNetwork) add(address, source string, services uint64) {
	n.admitAddress(address, source, services)
}
func (n *bitcoinNetwork) learn(rows []advertisedPeer, supplier string) {
	n.learnAddresses(rows, supplier)
}
func (n *bitcoinNetwork) start() { go n.run() }
func (n *bitcoinNetwork) stop() {
	n.cancel()
	for _, s := range n.listSessions() {
		n.mu.Lock()
		s.retired = true
		n.mu.Unlock()
		s.close("Client stopped")
	}
	n.persist()
}
func (n *bitcoinNetwork) persist() { n.persistBitcoinAddresses() }

func (n *bitcoinNetwork) run() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	ping := time.NewTicker(35 * time.Second)
	defer ping.Stop()
	save := time.NewTicker(10 * time.Second)
	defer save.Stop()
	n.maintain()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-tick.C:
			n.maintain()
		case <-save.C:
			n.persist()
		case <-ping.C:
			for _, session := range n.listSessions() {
				go func(s *bitcoinSession) {
					var payload [8]byte
					binary.LittleEndian.PutUint64(payload[:], uint64(time.Now().UnixNano()))
					_ = s.send("ping", payload[:])
				}(session)
			}
		}
	}
}
func (n *bitcoinNetwork) bootstrap() {
	defer func() { n.mu.Lock(); n.bootstrapBusy = false; n.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(n.ctx, 8*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	var found int
	var mu sync.Mutex
	for _, seed := range dnsSeeds {
		seed := seed
		wg.Add(1)
		go func() {
			defer wg.Done()
			ips, e := net.DefaultResolver.LookupIPAddr(ctx, seed)
			if e != nil {
				return
			}
			if len(ips) > 24 {
				ips = ips[:24]
			}
			for _, ip := range ips {
				n.add(net.JoinHostPort(ip.IP.String(), "8333"), "dns_seed:"+seed, 0)
				mu.Lock()
				found++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	n.mu.Lock()
	defer n.mu.Unlock()
	if found == 0 {
		n.bootstrapError = "Bitcoin DNS bootstrap found no addresses. Use a saved or manually supplied Bitcoin peer, or retry connections."
	} else {
		n.bootstrapError = ""
	}
}
func (n *bitcoinNetwork) maintain() { n.maintainBitcoin() }
func peerNetworkGroup(addr string) string {
	host, _, _ := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	if p := ip.To4(); p != nil {
		return fmt.Sprintf("%d.%d", p[0], p[1])
	}
	if p := ip.To16(); p != nil {
		return fmt.Sprintf("%x", p[:4])
	}
	return host
}
func (n *bitcoinNetwork) dial(x bitcoinAddress) { n.dialBitcoinPeer(x) }
func (n *bitcoinNetwork) failed(addr string, err error) {
	n.failedOutcome(addr, err, "connection_failed")
}

func (n *bitcoinNetwork) failedOutcome(addr string, err error, outcome string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	v, ok := n.dialing[addr]
	delete(n.dialing, addr)
	delete(n.dialCancels, addr)
	if ok {
		v.State = "failed"
		v.LastError = err.Error()
		v.Updated = time.Now().UTC()
		n.appendRecent(v)
	}
	n.eventLocked("dial_failed", addr, err.Error())
	if x := n.addresses[addr]; x != nil {
		n.recordFailureLocked(x, outcome, err.Error())
	}
}
func (n *bitcoinNetwork) appendRecent(v networkPeerView) {
	n.recent = append(n.recent, v)
	if len(n.recent) > 80 {
		n.recent = n.recent[len(n.recent)-80:]
	}
}
func (n *bitcoinNetwork) backoff(addr, reason string) {
	if x := n.addresses[addr]; x != nil {
		n.recordFailureLocked(x, "connection_failed", reason)
	}
}

func (s *bitcoinSession) close(reason string) {
	s.once.Do(func() {
		close(s.done)
		s.peer.conn.Close()
		s.n.mu.Lock()
		defer s.n.mu.Unlock()
		if s.n.sessions[s.peer.addr] == s {
			delete(s.n.sessions, s.peer.addr)
		}
		s.view.State = "disconnected"
		s.view.LastError = reason
		s.view.Updated = time.Now().UTC()
		s.n.appendRecent(s.view)
		if !s.retired && s.view.Direction != "inbound" {
			s.n.backoff(s.peer.addr, reason)
		}
		s.n.eventLocked("disconnected", s.peer.addr, reason)
	})
}
func (s *bitcoinSession) send(command string, payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.sendLocked(command, payload, time.Now().Add(8*time.Second))
}
func (s *bitcoinSession) sendRequest(ctx context.Context, command string, payload []byte) error {
	// A serving handler can own the writer while this direction waits. Do not
	// emit a request whose caller has already cancelled during that wait.
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for !s.writeMu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.done:
			return fmt.Errorf("peer disconnected")
		case <-tick.C:
		}
	}
	defer s.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(8 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	return s.sendLocked(command, payload, deadline)
}
func (s *bitcoinSession) sendLocked(command string, payload []byte, deadline time.Time) error {
	_ = s.peer.conn.SetWriteDeadline(deadline)
	e := writeMessage(s.peer.conn, command, payload)
	_ = s.peer.conn.SetWriteDeadline(time.Time{})
	s.n.mu.Lock()
	s.view.Sent += uint64(len(payload) + 24)
	s.view.Updated = time.Now().UTC()
	s.n.mu.Unlock()
	if e != nil {
		s.close(e.Error())
	}
	return e
}
func (s *bitcoinSession) readLoop() {
	for {
		_ = s.peer.conn.SetReadDeadline(time.Now().Add(110 * time.Second))
		m, e := readMessage(s.peer.conn)
		if e != nil {
			s.close(e.Error())
			return
		}
		s.n.mu.Lock()
		s.view.Received += uint64(len(m.payload) + 24)
		s.view.Updated = time.Now().UTC()
		s.n.mu.Unlock()
		switch m.command {
		case "ping":
			if len(m.payload) == 8 {
				_ = s.send("pong", m.payload)
			}
		case "addr", "addrv2":
			rows, e := parseAddressMessage(m)
			if e != nil {
				s.close("Malformed address message: " + e.Error())
				return
			}
			s.n.receiveAddresses(s, rows)
		case "getaddr", "getheaders", "getblocks", "getdata", "gwmsg":
			s.queueService(m)
		case "feature":
			s.close("Late feature message after verack")
			return
		case "bodmsg":
			var envelope bodEnvelope
			if json.Unmarshal(m.payload, &envelope) == nil && envelope.Kind == "request" {
				s.queueService(m)
				continue
			}
			s.deliverReply(m)
		case "satmsg":
			var envelope satlineEnvelope
			if json.Unmarshal(m.payload, &envelope) == nil && envelope.Kind == "request" {
				s.queueService(m)
				continue
			}
			s.deliverReply(m)
		case "headers", "block", "notfound":
			s.deliverReply(m)
		case "inv": // A new tip is discovered by the periodic header request.
		}
	}
}
func (s *bitcoinSession) request(ctx context.Context, command string, payload []byte, accept func(message) bool) (message, error) {
	if e := ctx.Err(); e != nil {
		return message{}, e
	}
	select {
	case s.slot <- struct{}{}:
	case <-ctx.Done():
		return message{}, ctx.Err()
	case <-s.done:
		return message{}, fmt.Errorf("peer disconnected")
	}
	defer func() { <-s.slot }()
	if e := ctx.Err(); e != nil {
		return message{}, e
	}
	s.n.mu.Lock()
	headersUnavailable := command == "getheaders" && s.headerUnavailable
	s.n.mu.Unlock()
	if headersUnavailable {
		return message{}, fmt.Errorf("headers unavailable on this session after an uncorrelated request timed out")
	}
	match, e := sessionReplyMatcher(command, payload)
	if e != nil {
		return message{}, e
	}
	pending := &sessionPendingReply{match: func(m message) bool { return match(m) && accept(m) }, reply: make(chan message, 1)}
	s.replyMu.Lock()
	s.pendingReply = pending
	s.replyMu.Unlock()
	defer func() {
		s.replyMu.Lock()
		if s.pendingReply == pending {
			s.pendingReply = nil
		}
		s.replyMu.Unlock()
	}()
	s.n.mu.Lock()
	s.view.State = "fetching"
	s.view.LastWork = command
	s.n.mu.Unlock()
	defer func() {
		s.n.mu.Lock()
		if s.view.State != "disconnected" {
			s.view.State = "connected"
		}
		s.n.mu.Unlock()
	}()
	if e := s.sendRequest(ctx, command, payload); e != nil {
		return message{}, e
	}
	for {
		select {
		case <-ctx.Done():
			// Cancellation is a request outcome, not evidence of dead transport.
			// Headers have no wire request ID: quarantine that role so a late
			// batch cannot satisfy a future locator request on this socket.
			if command == "getheaders" {
				s.n.mu.Lock()
				s.headerUnavailable = true
				s.n.eventLocked("headers_unavailable", s.peer.addr, "Header request stopped; this socket remains available for correlated requests")
				s.n.mu.Unlock()
			}
			return message{}, ctx.Err()
		case <-s.done:
			return message{}, fmt.Errorf("peer disconnected")
		case m := <-pending.reply:
			return m, nil
		}
	}
}
func (s *bitcoinSession) bodRequest(ctx context.Context, req overlayRequest) (overlayResponse, error) {
	if err := requireReleaseFeature("gateway-peerhood"); err != nil {
		return overlayResponse{}, err
	}
	if !s.peer.gateway || !s.peer.bod {
		return overlayResponse{}, fmt.Errorf("Gateway not negotiated")
	}
	body, _ := json.Marshal(req)
	env := bodEnvelope{Wire: bodWireVersion, Kind: "request", Type: bodWireType(req.Type), ID: randomRequestID(), Payload: body}
	raw, _ := json.Marshal(env)
	var out bodEnvelope
	_, e := s.request(ctx, "bodmsg", raw, func(m message) bool {
		return m.command == "bodmsg" && len(m.payload) <= maxBODMsgPayload && json.Unmarshal(m.payload, &out) == nil && out.ID == env.ID && out.Kind == "response" && out.Type == env.Type
	})
	if e != nil {
		return overlayResponse{}, e
	}
	if out.Wire != bodWireVersion {
		return overlayResponse{}, fmt.Errorf("incompatible Gateway response")
	}
	var resp overlayResponse
	if len(out.Payload) > 0 {
		if e = json.Unmarshal(out.Payload, &resp); e != nil {
			return resp, e
		}
	}
	resp.Version = out.Wire
	if out.Status == "unknown" {
		return resp, fmt.Errorf("%w: %s", errBODUnknown, out.Error)
	}
	if out.Status != "ok" {
		return resp, fmt.Errorf("Gateway %s: %s", out.Status, out.Error)
	}
	resp.OK = true
	return resp, nil
}
func (n *bitcoinNetwork) listSessions() []*bitcoinSession {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]*bitcoinSession, 0, len(n.sessions))
	for _, s := range n.sessions {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].view.Connected.Before(out[j].view.Connected) })
	return out
}
func (n *bitcoinNetwork) waitSession(ctx context.Context, archive bool) (*bitcoinSession, error) {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if !n.enabled() {
			return nil, fmt.Errorf("WAITING_FOR_PEERS: outbound networking is disabled")
		}
		for _, s := range n.listSessions() {
			if s.transportEligible(archive) {
				return s, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("WAITING_FOR_PEERS: no suitable Bitcoin connection: %w", ctx.Err())
		case <-n.ctx.Done():
			return nil, fmt.Errorf("network stopped")
		case <-tick.C:
		}
	}
}
func (n *bitcoinNetwork) fetchBlock(hash [32]byte, header []byte) ([]byte, string, error) {
	return n.fetchBlockContext(n.ctx, hash, header)
}
func (n *bitcoinNetwork) fetchBlockContext(parent context.Context, hash [32]byte, header []byte) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	stop := context.AfterFunc(n.ctx, cancel)
	defer stop()
	var last error
	tried := map[string]bool{}
	for attempt := 0; attempt < 4; attempt++ {
		s, e := n.waitSession(ctx, true)
		if e != nil {
			return nil, "", e
		}
		sessions := n.listSessions()
		for _, x := range sessions {
			if x.transportEligible(true) && !tried[x.peer.addr] {
				s = x
				break
			}
		}
		if tried[s.peer.addr] {
			break
		}
		tried[s.peer.addr] = true
		inv := uint32(2)
		if s.peer.services&nodeWitnessService != 0 {
			inv = 0x40000002
		}
		var req bytes.Buffer
		req.Write(encodeVarInt(1))
		_ = binary.Write(&req, binary.LittleEndian, inv)
		req.Write(hash[:])
		sub, done := context.WithTimeout(ctx, 15*time.Second)
		m, e := s.request(sub, "getdata", req.Bytes(), func(m message) bool { return m.command == "block" || m.command == "notfound" })
		done()
		if e != nil {
			last = e
			continue
		}
		if m.command == "notfound" {
			last = fmt.Errorf("peer does not have this block")
			continue
		}
		if len(m.payload) < 81 || hash256(m.payload[:80]) != hash || (len(header) == 80 && !bytes.Equal(header, m.payload[:80])) {
			last = fmt.Errorf("peer supplied the wrong block")
			s.close(last.Error())
			continue
		}
		// Full Merkle/witness verification is still done by the existing parser.
		return m.payload, s.peer.addr, nil
	}
	return nil, "", fmt.Errorf("WAITING_FOR_PEERS: no connected archival peer supplied the block: %v", last)
}
func (n *bitcoinNetwork) gatewayPeers() []overlayPeer {
	if !releaseFeatureAvailable("gateway-peerhood") {
		return []overlayPeer{}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	out := []overlayPeer{}
	for _, s := range n.sessions {
		if !s.peer.gateway || (s.status == nil && s.healthChecking) {
			continue
		}
		r := s.status
		if r == nil || !s.view.BOD {
			r = &overlayResponse{HeaderHeight: -1}
		}
		p := overlayPeer{Addr: s.peer.addr, ID: r.PeerID, Protocol: bodWireVersion, Gateway: true, BIP434: true, GatewayWire: s.peer.gatewayWire, Protocols: append([]gatewayProtocolSupport(nil), s.peer.gatewayProtocols...), Capabilities: append([]string(nil), r.Capabilities...), HeaderHeight: r.HeaderHeight, TipHash: r.TipHash, CacheBlocks: r.CacheBlocks, LastSeen: s.view.Updated, DiscoverySource: s.view.Source}
		if r.Status != nil {
			p.Snapshot = &r.Status.Snapshot
			p.Profiles = r.Status.Profiles
			p.BlockRanges = r.Status.BlockRanges
			p.Extensions = r.Status.Extensions
		}
		out = append(out, p)
	}
	return out
}
func (n *bitcoinNetwork) query(peer overlayPeer, req overlayRequest) (overlayResponse, error) {
	return n.queryContext(n.ctx, peer, req)
}
func (n *bitcoinNetwork) queryContext(parent context.Context, peer overlayPeer, req overlayRequest) (overlayResponse, error) {
	if err := requireReleaseFeature("gateway-peerhood"); err != nil {
		return overlayResponse{}, err
	}
	if !n.enabled() {
		return overlayResponse{}, fmt.Errorf("outbound networking is disabled")
	}
	n.mu.Lock()
	s := n.sessions[peer.Addr]
	n.mu.Unlock()
	if s == nil {
		return overlayResponse{}, fmt.Errorf("Gateway peer is no longer connected")
	}
	ctx, cancel := context.WithTimeout(parent, bodRequestTimeout)
	defer cancel()
	stop := context.AfterFunc(n.ctx, cancel)
	defer stop()
	return s.bodRequest(ctx, req)
}
func (n *bitcoinNetwork) snapshot() map[string]any { return n.bitcoinSnapshot() }
func (n *bitcoinNetwork) retry() {
	n.mu.Lock()
	n.eventLocked("retry_requested", "", "Retry eligible peers; retained outcomes, cooldowns and global budgets are unchanged")
	n.mu.Unlock()
	n.maintain()
}

func (a *app) queryGatewayPeer(peer overlayPeer, req overlayRequest) (overlayResponse, error) {
	if err := requireReleaseFeature("gateway-peerhood"); err != nil {
		return overlayResponse{}, err
	}
	if a.network != nil {
		return a.network.query(peer, req)
	}
	return queryOverlayPeer(peer, req)
}

func (n *bitcoinNetwork) headerSession(ctx context.Context) (*bitcoinSession, error) {
	if _, e := n.waitSession(ctx, false); e != nil {
		return nil, e
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	rows := make([]*bitcoinSession, 0, len(n.sessions))
	for _, s := range n.sessions {
		if s.transportEligibleLocked(false) {
			rows = append(rows, s)
		}
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("WAITING_FOR_PEERS: connection closed")
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].peer.addr < rows[j].peer.addr })
	selected := rows[n.headerCursor%uint64(len(rows))]
	n.headerCursor++
	return selected, nil
}
func (n *bitcoinNetwork) disconnect(addr string) bool {
	n.mu.Lock()
	s := n.sessions[addr]
	n.mu.Unlock()
	if s == nil {
		return false
	}
	s.close("Disconnected by user; automatic retry is delayed")
	n.mu.Lock()
	if x := n.addresses[addr]; x != nil {
		x.RetryAfter = n.now().Add(10 * time.Minute)
	}
	n.mu.Unlock()
	return true
}

func (n *bitcoinNetwork) fetchModuleBlock(hash string) (blockData, overlayPeer, error) {
	return n.fetchModuleBlockContext(n.ctx, hash)
}
func (n *bitcoinNetwork) fetchModuleBlockContext(parent context.Context, hash string) (blockData, overlayPeer, error) {
	if err := requireReleaseFeature("gateway-peerhood"); err != nil {
		return blockData{}, overlayPeer{}, err
	}
	if !n.enabled() {
		return blockData{}, overlayPeer{}, fmt.Errorf("WAITING_FOR_PEERS: outbound networking disabled")
	}
	raw, e := displayHashRaw(hash)
	if e != nil {
		return blockData{}, overlayPeer{}, e
	}
	var last error
	for _, peer := range n.gatewayPeers() {
		if !peerHasCap(peer, "blockdata") {
			continue
		}
		n.mu.Lock()
		s := n.sessions[peer.Addr]
		n.mu.Unlock()
		if s == nil {
			continue
		}
		var req bytes.Buffer
		req.WriteByte(1)
		inv := uint32(2)
		if s.peer.services&nodeWitnessService != 0 {
			inv = 0x40000002
		}
		_ = binary.Write(&req, binary.LittleEndian, inv)
		req.Write(raw[:])
		ctx, cancel := context.WithTimeout(parent, 15*time.Second)
		stop := context.AfterFunc(n.ctx, cancel)
		m, e := s.request(ctx, "getdata", req.Bytes(), func(m message) bool { return m.command == "block" || m.command == "notfound" })
		cancel()
		stop()
		if e != nil {
			last = e
			continue
		}
		if m.command == "notfound" {
			last = fmt.Errorf("peer cache miss")
			continue
		}
		if len(m.payload) < 81 || hash256(m.payload[:80]) != raw {
			s.close("module peer supplied wrong block")
			last = fmt.Errorf("wrong block")
			continue
		}
		return blockData{Height: -1, BlockHash: strings.ToLower(hash), Raw: m.payload}, peer, nil
	}
	return blockData{}, overlayPeer{}, fmt.Errorf("no connected Gateway peer supplied block bytes: %v", last)
}
func (a *app) queryManagedSatline(ctx context.Context, addr string, req satlineSegmentRequest) (satlineSegment, error) {
	if err := requireReleaseFeature("gateway-peerhood"); err != nil {
		return satlineSegment{}, err
	}
	if a.network == nil {
		return querySatlinePeer(ctx, addr, req, a.localGatewayProtocols())
	}
	n := a.network
	if !n.enabled() {
		return satlineSegment{}, fmt.Errorf("outbound networking disabled")
	}
	n.mu.Lock()
	connected := n.sessions[addr] != nil
	n.mu.Unlock()
	// An inbound peer's source port identifies a live socket, not a listener.
	if !connected {
		n.add(addr, "manual_satline", 0)
		n.maintain()
	}
	wait, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var s *bitcoinSession
	for {
		n.mu.Lock()
		s = n.sessions[addr]
		n.mu.Unlock()
		if s != nil {
			break
		}
		select {
		case <-wait.Done():
			return satlineSegment{}, fmt.Errorf("WAITING_FOR_PEERS: %w", wait.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !s.peer.gateway {
		return satlineSegment{}, fmt.Errorf("peer did not negotiate Gateway")
	}
	if _, ok := protocolVersionSupported(s.peer.gatewayProtocols, satlineProtocolID, 1, 1); !ok {
		return satlineSegment{}, fmt.Errorf("peer does not advertise Satline wire v1")
	}
	env := satlineEnvelope{Wire: 1, Network: "mainnet", Kind: "request", ID: randomRequestID(), Request: &req}
	raw, e := json.Marshal(env)
	if e != nil || len(raw) > maxSatlineRequestBytes {
		return satlineSegment{}, fmt.Errorf("request exceeds wire budget")
	}
	response, e := s.request(wait, "satmsg", raw, func(m message) bool { return m.command == "satmsg" })
	if e != nil {
		return satlineSegment{}, e
	}
	var got satlineEnvelope
	if len(response.payload) > maxSatlineWireBytes || strictSatlineJSON(response.payload, &got) != nil {
		return satlineSegment{}, fmt.Errorf("invalid Satline response")
	}
	if got.Wire != 1 || got.Network != "mainnet" || got.Kind != "response" || got.ID != env.ID || got.Request != nil {
		return satlineSegment{}, fmt.Errorf("response envelope mismatch")
	}
	if got.Status != "ok" || got.Segment == nil {
		return satlineSegment{}, fmt.Errorf("peer response: %s (not an unspent claim)", got.Status)
	}
	result := *got.Segment
	if result.Query != req.Query || result.After != req.After || !sameSatlinePoint(result.Start, req.Start) || len(result.Hops) == 0 || len(result.Hops) > req.Limit || result.Next != result.After+len(result.Hops) {
		return satlineSegment{}, fmt.Errorf("segment does not match requested continuation")
	}
	n.mu.Lock()
	found := false
	for _, p := range s.view.Protocols {
		if p == "Satline" {
			found = true
		}
	}
	if !found {
		s.view.Protocols = append(s.view.Protocols, "Satline")
	}
	s.view.LastWork = "Bounded Satline segment received; independent verification required"
	n.mu.Unlock()
	return result, nil
}
