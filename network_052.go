package main

// Active, bounded Bitcoin address crawling. This shares the existing connection
// manager with Bitcoin data jobs; it is not an IP scanner or a Gateway directory.
import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"sort"
	"strings"
	"time"
)

type discoveryPolicy struct {
	BitcoinSlots, GatewaySlots, ScoutSlots, DialMax int
	TokenInterval, ProbeWindow, RevisitAfter        time.Duration
}

func defaultDiscoveryPolicy() discoveryPolicy {
	return discoveryPolicy{BitcoinSlots: 3, GatewaySlots: 2, ScoutSlots: 2, DialMax: 3, TokenInterval: 5 * time.Second, ProbeWindow: 8 * time.Second, RevisitAfter: 30 * time.Minute}
}

type discoveryEvent struct {
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	Endpoint string    `json:"endpoint,omitempty"`
	Detail   string    `json:"detail"`
}
type discoveryProof struct {
	Peer         string    `json:"peer"`
	Source       string    `json:"discovery_source"`
	Type         string    `json:"request_type"`
	Height       int64     `json:"height"`
	Hash         string    `json:"block_hash"`
	Bytes        int       `json:"block_bytes_received,omitempty"`
	Verification string    `json:"verification"`
	At           time.Time `json:"at"`
}
type discoveryState struct {
	Started             time.Time       `json:"started"`
	Mode                string          `json:"mode"`
	Attempts            uint64          `json:"connection_attempts"`
	Handshakes          uint64          `json:"bitcoin_handshakes"`
	AddressRequests     uint64          `json:"address_requests"`
	AddressResponses    uint64          `json:"address_responses"`
	AddressesReceived   uint64          `json:"addresses_received"`
	AddressesAccepted   uint64          `json:"addresses_admitted"`
	AddressesRejected   uint64          `json:"addresses_rejected"`
	AddressesEvicted    uint64          `json:"addresses_evicted"`
	CandidateHints      uint64          `json:"gateway_hints_admitted"`
	GatewayNegotiations uint64          `json:"gateway_negotiations"`
	UsefulRequests      uint64          `json:"useful_requests"`
	Failed              uint64          `json:"failed_attempts"`
	EverUseful          bool            `json:"ever_useful"`
	FirstUseful         *discoveryProof `json:"first_useful_request,omitempty"`
	LastUseful          *discoveryProof `json:"last_useful_request,omitempty"`
	LastProgress        time.Time       `json:"last_progress,omitempty"`
}

func (n *bitcoinNetwork) eventLocked(kind, endpoint, detail string) {
	if len(detail) > 280 {
		detail = detail[:280]
	}
	n.events = append(n.events, discoveryEvent{At: time.Now().UTC(), Kind: kind, Endpoint: endpoint, Detail: detail})
	if len(n.events) > 120 {
		n.events = append([]discoveryEvent(nil), n.events[len(n.events)-120:]...)
	}
}
func publicBitcoinIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return false
	}
	if x := ip.To4(); x != nil {
		if x[0] == 0 || x[0] >= 224 || (x[0] == 100 && x[1] >= 64 && x[1] <= 127) || (x[0] == 198 && (x[1] == 18 || x[1] == 19)) {
			return false
		}
		for _, subnet := range []string{"192.0.0.0/24", "192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24"} {
			_, r, _ := net.ParseCIDR(subnet)
			if r.Contains(ip) {
				return false
			}
		}
	} else {
		_, r, _ := net.ParseCIDR("2001:db8::/32")
		if r.Contains(ip) {
			return false
		}
	}
	return true
}
func (n *bitcoinNetwork) admitAddress(address, source string, services uint64) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.recordAddressLocked(advertisedPeer{Addr: address, Services: services}, source)
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
func (n *bitcoinNetwork) learnAddresses(rows []advertisedPeer, supplier string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, row := range rows {
		n.recordAddressLocked(row, "bitcoin_addr:"+supplier)
	}
}

func (n *bitcoinNetwork) receiveAddresses(s *bitcoinSession, rows []advertisedPeer) {
	n.mu.Lock()
	// Bitcoin peers may gossip unsolicited addresses, but repeated address payloads
	// cannot continually reset this temporary connection's search deadline.
	if s.addressBudget+len(rows) > 5000 {
		n.ledgerMetaDirty = true
		n.ledgerMeta.Observations += uint64(len(rows))
		n.ledgerMeta.Unretained += uint64(len(rows))
		n.ledgerMeta.RetentionComplete = false
		n.discovery.AddressesReceived += uint64(len(rows))
		n.eventLocked("intake_budget", s.peer.addr, "Excess address batch rejected by per-connection processing budget; unretained arrivals counted")
		n.mu.Unlock()
		s.close("Address processing budget exceeded")
		return
	}
	s.addressBudget += len(rows)
	n.ledgerMetaDirty = true
	n.ledgerMeta.AddressMessages++
	first := !s.addrReceived
	s.addrReceived = true
	// addr/addrv2 have no request ID: a singleton can be an unsolicited
	// self-announcement before the getaddr response. As with Core's AddrFetch,
	// only a multi-record message permits early completion. Genuine singleton
	// or empty replies are still learned, with the original probe deadline.
	if len(rows) > 1 {
		s.addrSampleReceived = true
	}
	if first {
		n.discovery.AddressResponses++
		n.discovery.LastProgress = time.Now().UTC()
		n.eventLocked("addresses_received", s.peer.addr, fmt.Sprintf("Received %d address records; retaining eligible candidates", len(rows)))
	}
	if x := n.ledger[s.peer.addr]; x != nil {
		x.LastCrawled = n.now()
		x.AddressMessages++
		if first && s.addrAsked {
			x.RequestedReplies++
		}
		x.LastAddrOutcome = "address_message_received"
		n.touchLocked(x)
	}
	if s.peer.gateway {
		for _, row := range rows {
			n.recordAddressLocked(row, "gateway_addr:"+s.peer.addr)
		}
		n.mu.Unlock()
	} else {
		n.mu.Unlock()
		n.learn(rows, s.peer.addr)
	}
	n.persist()
}
func (s *bitcoinSession) isScout() bool { s.n.mu.Lock(); defer s.n.mu.Unlock(); return s.scout }
func (n *bitcoinNetwork) usefulCountLocked() int {
	count := 0
	for _, s := range n.sessions {
		if s.view.BOD && s.requestSuccess {
			count++
		}
	}
	return count
}
func (n *bitcoinNetwork) maintainDiscovery() {
	n.app.settingsMu.RLock()
	settings := n.app.settings
	n.app.settingsMu.RUnlock()
	n.app.refreshBitcoinServingAsync()
	if settings.NetworkDisabled {
		n.mu.Lock()
		for _, cancel := range n.dialCancels {
			cancel()
		}
		if n.discovery.Mode != "offline" {
			n.discovery.Mode = "offline"
			n.eventLocked("offline", "", "Outbound networking is disabled")
		}
		n.mu.Unlock()
		for _, s := range n.listSessions() {
			if s.view.Direction != "inbound" {
				s.close("Outbound networking disabled")
			}
		}
		return
	}
	for _, p := range n.app.preferred {
		n.ensureConfiguredAddress(normalizePeer(p), "manual_bitcoin", 0)
	}
	for _, p := range settings.BitcoinPeers {
		n.ensureConfiguredAddress(normalizePeer(p), "manual_bitcoin", 0)
	}
	configuredGateways := configuredGatewayBootstrapPeers(settings)
	for _, address := range configuredGateways {
		n.ensureConfiguredAddress(address, "manual_gateway", gatewayExperimentalServiceBit)
	}
	// A configured Core is just another ordinary Bitcoin introduction, not a
	// preloaded Gateway endpoint. Its separate Gateway listener must be learned.
	if !settings.CoreDisabled {
		if p, ok := coreP2PAddr(settings); ok {
			n.ensureConfiguredAddress(p, "local_core_bitcoin", 0)
		}
	}
	refreshPublicIP := settings.BitcoinGatewayDiscovery && !settings.DiscoveryPaused && n.app.needsPublicIPObservations054()
	n.mu.Lock()
	n.configuredGateways = configuredGateways
	now := n.now()
	outbound := 0
	for _, s := range n.sessions {
		if s.view.Direction != "inbound" {
			outbound++
		}
	}
	if !n.intakeBlockedLocked() && !n.noBootstrap && !n.bootstrapBusy && outbound < 2 && (n.bootstrapAt.IsZero() || now.Sub(n.bootstrapAt) > 5*time.Minute) {
		n.bootstrapBusy = true
		n.bootstrapAt = now
		n.eventLocked("bootstrap", "", "Asking Bitcoin DNS seeds for ordinary Bitcoin introductions")
		go n.bootstrap()
	}
	useful := n.usefulCountLocked()
	seek := !settings.DiscoveryPaused && !n.intakeBlockedLocked() && useful < n.policy.GatewaySlots
	mode := "maintain"
	if n.ledgerError != "" {
		mode = "history_error"
	} else if n.intakeBlockedLocked() {
		mode = "history_limit"
	} else if settings.DiscoveryPaused {
		mode = "paused"
	} else if len(n.sessions) == 0 {
		mode = "bootstrap"
	} else if useful == 0 && n.discovery.EverUseful {
		mode = "recover"
	} else if useful == 0 && n.gatewayCountLocked() > 0 {
		mode = "seeking_services"
	} else if useful == 0 {
		mode = "seek"
	} else if seek {
		mode = "diversify"
	}
	if mode != n.discovery.Mode {
		n.discovery.Mode = mode
		n.eventLocked("mode", "", mode)
	}
	// Close scouts after a multi-address sample or timeout, and never while a
	// data request owns their slot. A negotiated peer is promoted first.
	retire := n.retireHeaderTransportsLocked(now)
	health := []*bitcoinSession{}
	n.retainGatewaySessionsLocked()
	for _, s := range n.sessions {
		if !s.scout && s.peer.bod && !s.healthChecking && len(s.slot) == 0 && now.Sub(s.lastServiceCheck) > 2*time.Minute {
			s.healthChecking = true
			s.lastServiceCheck = now
			health = append(health, s)
		}
		if s.scout && !s.healthChecking && len(s.slot) == 0 && (!seek || (!settings.BitcoinGatewayDiscovery && !s.peer.gateway) || now.Sub(s.view.Connected) > n.policy.ProbeWindow || (s.addrSampleReceived && !s.peer.gateway)) {
			s.retired = true
			if x := n.ledger[s.peer.addr]; x != nil {
				x.RetryAfter = now.Add(n.policy.RevisitAfter)
				if !s.addrReceived {
					x.LastAddrOutcome = "no_address_reply"
				}
				if x.LastServiceOutcome == "" {
					x.LastOutcome = "visit_complete"
				}
				if x.LastNegotiation == "no_gateway" {
					x.LastOutcome = "bitcoin_without_gateway"
				}
				if s.peer.gateway && s.requestSuccess && s.view.BOD {
					x.LastOutcome = "gateway_standby"
				}
				x.OutcomeAt = now
				x.InFlight = false
				n.touchLocked(x)
			}
			retire = append(retire, s)
		}
	}
	if elapsed := now.Sub(n.tokenAt); elapsed > 0 {
		n.tokens += float64(elapsed) / float64(n.policy.TokenInterval)
		if n.tokens > 3 {
			n.tokens = 3
		}
		n.tokenAt = now
	}
	plans := n.planDiscoveryWithRefreshLocked(now, seek, !settings.DiscoveryPaused && n.ledgerError == "", refreshPublicIP)
	n.mu.Unlock()
	for _, s := range retire {
		s.close("Discovery visit complete; address retained for a later search")
	}
	// Commit attempts BEFORE network side effects. A crash then restores them as
	// interrupted, never as fresh untried work. Disk failure halts new dialing.
	if n.persistLedger() {
		for _, x := range plans {
			go n.dial(x)
		}
	} else {
		for _, x := range plans {
			n.failedOutcome(x.Address, fmt.Errorf("discovery history could not be committed; socket not opened"), "history_commit_failed_before_dial")
		}
	}
	for _, s := range health {
		go n.checkServiceHealth(s)
	}
	n.announceOnManagedPeers()
}
func (n *bitcoinNetwork) dialDiscoveryPeer(x bitcoinAddress) {
	n.app.settingsMu.RLock()
	allowedProbe := n.app.settings.BitcoinGatewayDiscovery && !n.app.settings.DiscoveryPaused
	n.app.settingsMu.RUnlock()
	n.mu.Lock()
	role := n.dialing[x.Address].Role
	normal := normalGatewayCandidate053(&x) || containsString(n.configuredGateways, x.Address)
	n.mu.Unlock()
	if !allowedProbe && !normal && (role == "gateway_search" || role == "gateway_candidate") {
		n.failedOutcome(x.Address, fmt.Errorf("Bitcoin discovery experiment is off"), "experiment_stopped_before_dial")
		return
	}
	ctx, cancel := context.WithTimeout(n.ctx, 20*time.Second)
	defer cancel()
	n.mu.Lock()
	n.dialCancels[x.Address] = cancel
	n.mu.Unlock()
	c, err := (&net.Dialer{Timeout: 6 * time.Second}).DialContext(ctx, "tcp", x.Address)
	if err != nil {
		n.failed(x.Address, err)
		return
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			c.Close()
		case <-done:
		}
	}()
	flags, height := n.app.localBitcoinAdvertisement()
	n.app.settingsMu.RLock()
	probe := n.app.settings.BitcoinGatewayDiscovery && !n.app.settings.DiscoveryPaused
	n.app.settingsMu.RUnlock()
	n.mu.Lock()
	directGateway := normalGatewayCandidate053(&x) || containsString(n.configuredGateways, x.Address)
	n.mu.Unlock()
	protocols := n.app.localGatewayProtocols()
	if !probe && !directGateway {
		protocols = nil
		flags &^= gatewayExperimentalServiceBit
	}
	p, err := handshakeOutboundState(c, x.Address, protocols, flags, height)
	close(done)
	if err != nil {
		c.Close()
		n.failedOutcome(x.Address, err, "bitcoin_handshake_failed")
		return
	}
	if n.ctx.Err() != nil || !n.enabled() {
		c.Close()
		n.failed(x.Address, fmt.Errorf("networking stopped"))
		return
	}
	now := time.Now().UTC()
	s := &bitcoinSession{n: n, peer: p, done: make(chan struct{}), slot: make(chan struct{}, 1), serveQueue: make(chan message, 4), satIDs: map[string]bool{}}
	n.mu.Lock()
	v := n.dialing[x.Address]
	delete(n.dialing, x.Address)
	delete(n.dialCancels, x.Address)
	v.State = "connected"
	v.Services = p.services
	v.ServiceHex = fmt.Sprintf("0x%016x", p.services)
	v.ProtocolVersion = p.version
	v.Negotiated = append([]gatewayProtocolSupport(nil), p.gatewayProtocols...)
	v.Gateway = p.gateway
	v.TargetHeight = p.startHeight
	v.Connected = now
	v.Updated = now
	if p.gateway {
		v.Protocols = append(v.Protocols, "Gateway On Demand")
		v.Role = "gateway_candidate"
		n.discovery.GatewayNegotiations++
		n.eventLocked("gateway_negotiated", x.Address, "BIP 434 and Gateway protocol exchange completed")
	} else if p.services&(nodeNetworkService|nodeNetworkLimitedService) == 0 {
		// A successful Bitcoin handshake alone does not make a header provider.
		// Keep its bounded address visit, without occupying a transport slot.
		v.Role = "gateway_search"
	}
	s.view = v
	s.scout = v.Role == "gateway_search" || v.Role == "gateway_candidate"
	n.sessions[x.Address] = s
	n.retainGatewaySessionsLocked()
	s.lastServiceCheck = now
	n.discovery.Handshakes++
	n.discovery.LastProgress = now
	if a := n.ledger[x.Address]; a != nil {
		a.LastSuccess = now
		a.Services = p.services
		a.Failures = 0
		a.LastError = ""
		a.GatewayVerified = a.GatewayVerified || p.gateway
		a.DirectServices = p.services
		a.DirectVersion = p.version
		a.DirectAt = now
		a.InFlight = false
		a.OutcomeAt = now
		a.LastOutcome = "bitcoin_connected"
		a.Negotiated = append([]gatewayProtocolSupport(nil), p.gatewayProtocols...)
		if len(protocols) > 0 && (a.EverHint || p.gateway) {
			a.GatewayChecks++
		}
		a.LastNegotiation = "not_requested"
		if p.gateway {
			a.LastNegotiation = "gateway_negotiated"
			a.GatewayRetryAfter = time.Time{}
		} else if len(protocols) > 0 {
			a.LastNegotiation = "no_gateway"
			a.GatewayRetryAfter = now.Add(n.policy.RevisitAfter)
		}
		n.touchLocked(a)
	}
	n.recordPublicIPObservationLocked(x.Address, p.observedIP, now)
	n.eventLocked("bitcoin_connected", x.Address, "Bitcoin handshake completed; inspecting addresses and negotiated capabilities")
	canAsk := !n.intakeBlockedLocked()
	s.addrAsked = canAsk
	s.healthChecking = p.bod // Reserve the bounded initial service probe before its goroutine starts.
	if canAsk {
		n.discovery.AddressRequests++
	}
	n.mu.Unlock()
	n.persist()
	go s.readLoop()
	go s.serviceLoop()
	if canAsk {
		_ = s.send("getaddr", nil)
	}
	n.announceToSession(s)
	if p.bod {
		go n.checkGatewayService(s)
	}
}
func (n *bitcoinNetwork) checkGatewayService(s *bitcoinSession) {
	n.mu.Lock()
	s.healthChecking = true
	n.mu.Unlock()
	defer func() { n.mu.Lock(); s.healthChecking = false; n.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(n.ctx, 15*time.Second)
	defer cancel()
	resp, err := s.bodRequest(ctx, overlayRequest{Type: "status"})
	if err != nil {
		n.mu.Lock()
		s.view.LastError = "BOD capability request: " + err.Error()
		n.serviceOutcomeLocked(s, "capability_request_failed", s.view.LastError)
		n.eventLocked("capabilities_failed", s.peer.addr, s.view.LastError)
		n.mu.Unlock()
		return
	}
	if resp.Status != nil && (resp.Status.Wire != bodWireVersion || resp.Status.ProtocolID != bodProtocolID || resp.Status.Snapshot.Network != "mainnet") {
		n.serviceOutcome(s, "incompatible_bod_status", "Incompatible BOD status identity or network")
		s.close("Incompatible BOD status identity or network")
		return
	}
	// Status is evidence of a supported exchange, not evidence that the peer has
	// all advertised data. A separate useful request earns the last milestone.
	n.mu.Lock()
	s.status = &resp
	s.view.BOD = true
	s.view.Protocols = append(s.view.Protocols, "Bitcoin on Demand")
	s.view.LastWork = "BOD capability exchange completed"
	n.eventLocked("bod_capabilities", s.peer.addr, "Peer reported Bitcoin on Demand capabilities; testing one supported lookup")
	n.mu.Unlock()
	if !containsString(resp.Capabilities, "blockloc") {
		n.serviceOutcome(s, "no_block_locator_capability", "Gateway negotiated, but blockloc capability is unavailable")
		return
	}
	height := resp.HeaderHeight
	if resp.Status != nil {
		height = resp.Status.Snapshot.Height
	}
	if height < 0 {
		n.serviceOutcome(s, "no_service_snapshot", "Peer did not supply a usable nonnegative snapshot height")
		return
	}
	// The peer's selected tip is a supported lookup, not a global height authority.
	if height > 1 {
		height = 1
	}
	height, fetchSample := serviceSample054(resp, height)
	if height <= 0 {
		n.mu.Lock()
		n.serviceOutcomeLocked(s, "no_non_genesis_sample", "No non-genesis lookup sample advertised; Gateway connection remains useful for membership")
		n.eventLocked("limited_service", s.peer.addr, "Gateway connected; no non-genesis BOD sample advertised. Still seeking data providers.")
		n.mu.Unlock()
		return
	}
	answer, err := s.bodRequest(ctx, overlayRequest{Type: "blockloc", Height: height})
	if err != nil || answer.BlockLocation == nil || answer.BlockLocation.Height != height || !validHash(answer.BlockLocation.BlockHash) {
		reason := "Peer did not return a valid block locator for the requested height"
		if err != nil {
			reason = err.Error()
		}
		n.mu.Lock()
		s.view.LastError = reason
		n.serviceOutcomeLocked(s, "block_locator_failed", reason)
		n.eventLocked("useful_request_failed", s.peer.addr, reason)
		n.mu.Unlock()
		return
	}
	loc := answer.BlockLocation
	verification := "Peer locator; pending local header anchoring"
	if header, e := n.app.readSelectedHeader(height); e == nil {
		raw := hash256(header)
		if reverseHex(raw[:]) != strings.ToLower(loc.BlockHash) {
			n.serviceOutcome(s, "locator_conflicts_with_headers", "Peer locator disagrees with selected local headers")
			s.close("Peer locator conflicts with the selected local header chain")
			return
		}
		verification = "Locator matches the selected local header chain; block bytes not requested by this probe"
	}
	sampleBytes := 0
	if fetchSample {
		hash, e := displayHashRaw(loc.BlockHash)
		if e != nil {
			return
		}
		var request bytes.Buffer
		request.WriteByte(1)
		typ := uint32(2)
		if s.peer.services&nodeWitnessService != 0 {
			typ = 0x40000002
		}
		_ = binary.Write(&request, binary.LittleEndian, typ)
		request.Write(hash[:])
		m, e := s.request(ctx, "getdata", request.Bytes(), func(m message) bool { return m.command == "block" || m.command == "notfound" })
		if e != nil || m.command != "block" {
			n.mu.Lock()
			n.serviceOutcomeLocked(s, "sample_unavailable", "Advertised block unavailable; only locator capability demonstrated")
			n.eventLocked("sample_unavailable", s.peer.addr, "Advertised block sample was unavailable; keeping only the demonstrated locator capability")
			n.mu.Unlock()
		} else {
			header, headerError := n.app.readSelectedHeader(height)
			if headerError != nil {
				header = nil
			}
			view, err := parseBlockDetailed(height, loc.BlockHash, header, m.payload, s.peer.addr, false)
			if err != nil || !integrityVerified(view) {
				n.serviceOutcome(s, "block_integrity_failed", "Returned block failed integrity verification")
				s.close("Discovery block sample failed integrity verification")
				return
			}
			sampleBytes = len(m.payload)
			if len(header) == 80 {
				verification = "Block identity, Merkle/witness integrity and selected-header anchoring checked; not full consensus validation"
			} else {
				verification = "Block identity and Merkle/witness integrity checked; pending selected-header anchoring"
			}
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.sessions[s.peer.addr] != s {
		return
	}
	s.scout = false
	s.requestSuccess = true
	s.lastServiceCheck = time.Now().UTC()
	n.retainGatewaySessionsLocked()
	s.view.LastWork = "Useful block-location request completed"
	s.view.LastError = ""
	n.discovery.UsefulRequests++
	n.discovery.EverUseful = true
	n.discovery.LastProgress = time.Now().UTC()
	proof := &discoveryProof{Peer: s.peer.addr, Source: s.view.Source, Type: "block_location", Height: height, Hash: loc.BlockHash, Bytes: sampleBytes, Verification: verification, At: time.Now().UTC()}
	if sampleBytes > 0 {
		proof.Type = "block_location_and_block"
		s.view.LastWork = "Located and integrity-checked a block through this Gateway peer"
	}
	n.discovery.LastUseful = proof
	if n.discovery.FirstUseful == nil {
		n.discovery.FirstUseful = proof
	}
	if x := n.ledger[s.peer.addr]; x != nil {
		outcome := "useful_locator"
		if sampleBytes > 0 {
			outcome = "useful_block"
		}
		n.serviceOutcomeLocked(s, outcome, "")
		x.LastBODSuccess = time.Now().UTC()
		x.GatewayVerified = true
		x.LastOutcome = "useful_service"
		x.OutcomeAt = n.now()
		n.touchLocked(x)
	}
	n.eventLocked("useful_request", s.peer.addr, fmt.Sprintf("Block %d located through a negotiated BOD request. %s", height, verification))
}
func (n *bitcoinNetwork) discoverySnapshot() map[string]any {
	n.app.settingsMu.RLock()
	enabled := !n.app.settings.NetworkDisabled
	paused := n.app.settings.DiscoveryPaused
	probe := n.app.settings.BitcoinGatewayDiscovery
	lan := n.app.settings.LANDiscovery
	configuredGateways := configuredGatewayBootstrapPeers(n.app.settings)
	n.app.settingsMu.RUnlock()
	n.mu.Lock()
	rows := []networkPeerView{}
	gateway, bod, stable, scouts, managedIncoming := 0, 0, 0, 0, 0
	managedAddresses := make(map[string]bool)
	for _, s := range n.sessions {
		v := s.view
		managedAddresses[v.Address] = true
		v.Protocols = append([]string(nil), v.Protocols...)
		v.Negotiated = append([]gatewayProtocolSupport(nil), v.Negotiated...)
		rows = append(rows, v)
		if v.Gateway {
			gateway++
			if v.Direction == "inbound" {
				managedIncoming++
			}
		}
		if v.BOD {
			bod++
		}
		if s.scout {
			scouts++
		} else {
			stable++
		}
	}
	for _, v := range n.dialing {
		rows = append(rows, v)
	}
	candidates := []bitcoinAddress{}
	hinted, verified := 0, 0
	for _, addr := range n.ledgerOrder {
		x := n.ledger[addr]
		if x.EverHint {
			hinted++
		}
		if x.GatewayVerified {
			verified++
		}
		if len(candidates) < 80 && (x.EverHint || x.GatewayVerified) {
			v := *x
			v.Suppliers = append([]string(nil), x.Suppliers...)
			candidates = append(candidates, v)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Address < rows[j].Address })
	state := n.discovery
	useful := n.usefulCountLocked()
	snapshot := map[string]any{"connections": rows, "connected": len(n.sessions), "stable_connections": stable, "search_connections": scouts, "gateway_connected": gateway, "bod_available": bod, "useful_gateway_peers": useful, "address_count": len(n.addresses), "gateway_hint_count": hinted, "previously_negotiated_count": verified, "candidates": candidates, "recent": append([]networkPeerView(nil), n.recent...), "bootstrap_error": n.bootstrapError, "bootstrap_running": n.bootstrapBusy, "experimental_service_bit": 25, "enabled": enabled, "discovery_paused": paused, "lan_discovery": lan, "discovery": state, "events": append([]discoveryEvent(nil), n.events...), "gateway_target": n.policy.GatewaySlots, "limits": map[string]any{"bitcoin_slots": n.policy.BitcoinSlots, "gateway_slots": n.policy.GatewaySlots, "search_slots": n.policy.ScoutSlots, "address_capacity": maxBitcoinAddresses, "dial_interval_seconds": n.policy.TokenInterval.Seconds(), "revisit_minutes": n.policy.RevisitAfter.Minutes()}, "transport_note": "Bitcoin P2P v1. This is not a VPN or encrypted transport. A peer locator is not full consensus validation."}
	snapshot["candidate_details_truncated"] = hinted > len(candidates)
	snapshot["configured_gateway_peers"] = configuredGateways
	snapshot["bitcoin_gateway_discovery"] = probe
	snapshot["experiment"] = map[string]any{"enabled": probe, "active": probe && enabled && !paused, "default": "off", "fresh_trial": false, "note": "Opt-in controls Bitcoin hint seeking and address announcements. Existing history is retained; use an isolated fresh profile to test Bitcoin-only first contact. Ordinary Bitcoin data access remains independent."}
	snapshot["discovery_policy"] = map[string]any{"healthy_target": n.policy.GatewaySlots, "normal_gateway_seeking": enabled && !paused && useful < n.policy.GatewaySlots, "normal_scouting": probe && enabled && !paused && !n.intakeBlockedLocked() && useful < n.policy.GatewaySlots, "remembered_peer_priority": true, "configured_peer_limit": maxGatewayBootstrapPeers, "note": "Remembered verified peers and configured entrances share bounded dial budgets. Configured and remembered routes resume after loss. Experimental Bitcoin scouting is opt-in and steps back at the useful-provider target. Bitcoin transport remains independent; extra public-IP probes require experimental consent."}
	snapshot["ledger"] = n.ledgerSummaryLocked(n.now())
	snapshot["candidate_states"] = n.candidateViewsLocked(candidates, n.now())
	n.mu.Unlock()
	// Inbound negotiated sessions are visible too; capability support does not
	// imply that an outbound-only client is offering BOD data to this server.
	incoming := 0
	if n.app.source != nil {
		n.app.source.mu.Lock()
		for _, p := range n.app.source.live {
			if p.Gateway && !managedAddresses[p.Address] {
				incoming++
			}
		}
		n.app.source.mu.Unlock()
	}
	snapshot["gateway_inbound"] = incoming + managedIncoming
	snapshot["gateway_outbound"] = gateway - managedIncoming
	snapshot["gateway_connected"] = gateway + incoming
	snapshot["advertisement"] = n.app.bitcoinAdvertisementStatus()
	return snapshot
}
func (s *bitcoinSession) queueService(m message) {
	select {
	case s.serveQueue <- m:
	default:
		s.close("Inbound request queue exceeded its budget")
	}
}
func (s *bitcoinSession) serviceLoop() {
	for {
		select {
		case <-s.done:
			return
		case m := <-s.serveQueue:
			s.serveMessage(m)
		}
	}
}
func (s *bitcoinSession) serveMessage(m message) {
	a := s.n.app
	if a.source == nil {
		return
	}
	if m.command == "getaddr" {
		s.n.mu.Lock()
		already := s.inboundAddrAsked
		s.inboundAddrAsked = true
		s.n.mu.Unlock()
		if already {
			return
		}
		command, payload := addressMessage(s.n.addressReplyRows(), s.peer.wantsAddrV2)
		_ = s.send(command, payload)
		return
	}
	a.settingsMu.RLock()
	allowed := a.settings.ServeData
	sat := a.settings.SatlineEnabled && a.settings.SatlineServePublished
	a.settingsMu.RUnlock()
	// Provider work never owns the writer. The wrapper serializes each complete
	// Bitcoin frame, leaving ping and opposite-direction replies independent.
	c := &sessionWriteConn{Conn: s.peer.conn, s: s}
	defer func() {
		if c.err != nil {
			s.close(c.err.Error())
		}
	}()
	switch m.command {
	case "gwmsg":
		// Inventory exchange is control traffic, including receive-only nodes.
		// A new hello gets an acknowledgement without mutating the immutable
		// protocol list negotiated before the managed reader started.
		p := *s.peer
		handleGatewayMessage(c, m.payload, &p)
	case "getdata":
		if allowed {
			a.source.handleGetData(c, m.payload)
		} else {
			writeBitcoinUnavailable(c, m)
		}
	case "getheaders", "getblocks":
		if allowed {
			a.source.handleChainRequest(c, m.command, m.payload)
		} else {
			writeBitcoinUnavailable(c, m)
		}
	case "bodmsg":
		if s.peer.gateway && s.peer.bod {
			var env bodEnvelope
			if json.Unmarshal(m.payload, &env) != nil || env.Kind != "request" || env.ID == "" || len(env.ID) > 128 {
				a.source.handleBODMsg(c, m.payload)
				return
			}
			status := s.bodBudget.take(s.n.now(), 64, env.ID)
			if !allowed {
				status = "unsupported"
			}
			if status != "" {
				a.source.writeBODResponse(c, bodEnvelope{Wire: bodWireVersion, Kind: "response", Type: env.Type, ID: env.ID, Status: status, Error: "Serving unavailable, request budget exhausted, or duplicate request ID", Payload: json.RawMessage(`{}`)})
				return
			}
			a.source.handleBODMsg(c, m.payload)
		}
	// Satline keeps its own rate limits and explicit publication controls.
	case "satmsg":
		if !s.peer.gateway {
			return
		}
		if _, ok := protocolVersionSupported(s.peer.gatewayProtocols, satlineProtocolID, 1, 1); !ok {
			return
		}
		var env satlineEnvelope
		if strictSatlineJSON(m.payload, &env) != nil || env.Kind != "request" || !validSatlineRequestID(env.ID) {
			return
		}
		previous := s.satBudget.started
		status := s.satBudget.take(s.n.now(), 16, env.ID)
		if previous != s.satBudget.started {
			s.satIDs = make(map[string]bool)
		}
		if !sat {
			status = "unsupported"
		}
		if status != "" {
			writeSatlineUnavailable(c, env.ID, status)
			return
		}
		a.handleSatlineWire(c, m.payload, s.satIDs)
	}
}
func (n *bitcoinNetwork) addressReply() []byte { return encodeAddressList(n.addressReplyRows()) }
func (n *bitcoinNetwork) addressReplyRows() []advertisedPeer {
	n.mu.Lock()
	defer n.mu.Unlock()
	rows := []advertisedPeer{}
	seen := 0
	// Share only public, directly observed working endpoints. Never publish
	// ledger histories, excluded addresses, failed probes or arbitrary rumors.
	for _, x := range n.ledger {
		host, _, _ := net.SplitHostPort(x.Address)
		if !publicBitcoinIP(net.ParseIP(host)) && !n.allowPrivate {
			continue
		}
		if x.Exclusion != "" || x.LastSuccess.IsZero() || n.now().Sub(x.LastSuccess) > 24*time.Hour {
			continue
		}
		row := advertisedPeer{Addr: x.Address, Services: x.DirectServices, Timestamp: uint32(x.LastSuccess.Unix())}
		seen++
		if len(rows) < 64 {
			rows = append(rows, row)
		} else if j := rand.Intn(seen); j < 64 {
			rows[j] = row
		}
	}
	return rows
}

// Refresh service readiness without downloading another sample block. A lost
// capability must restart discovery even when its underlying socket stays open.
func (n *bitcoinNetwork) checkServiceHealth(s *bitcoinSession) {
	defer func() { n.mu.Lock(); s.healthChecking = false; n.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(n.ctx, 8*time.Second)
	defer cancel()
	response, err := s.bodRequest(ctx, overlayRequest{Type: "status"})
	if err != nil || !containsString(response.Capabilities, "blockloc") {
		reason := "BOD locator capability is no longer available"
		if err != nil {
			reason = err.Error()
		}
		n.mu.Lock()
		n.eventLocked("service_lost", s.peer.addr, reason)
		wasUseful := s.requestSuccess
		s.requestSuccess = false
		s.view.BOD = false
		s.view.LastWork = "Gateway connected; BOD service unavailable"
		n.serviceOutcomeLocked(s, "service_unavailable", reason)
		n.retainGatewaySessionsLocked()
		if wasUseful {
			n.wakeStandbyProviderLocked(n.now())
		}
		n.mu.Unlock()
		return
	}
	if response.Status != nil && (response.Status.Wire != bodWireVersion || response.Status.ProtocolID != bodProtocolID || response.Status.Snapshot.Network != "mainnet") {
		s.close("BOD status identity changed")
		return
	}
	n.mu.Lock()
	retrySample := false
	if n.sessions[s.peer.addr] == s {
		s.status = &response
		s.view.BOD = true
		s.view.LastWork = "BOD capability health check completed"
		retrySample = !s.requestSuccess
	}
	n.mu.Unlock()
	if retrySample {
		n.checkGatewayService(s)
	}
}
