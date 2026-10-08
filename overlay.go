package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// BOD deliberately uses its own listen port so it can coexist with Bitcoin
	// Core on 8333. The bytes on this port are ordinary Bitcoin P2P framing.
	overlayTCPPort = 48333
	overlayUDPPort = 48334 // LAN bootstrap discovery only; not BOD transport.
	discoveryMagic = "GATEWAY_DISCOVER_BIP434_V1"
	announceMagic  = "GATEWAY_PEER_BIP434_V1"

	maxBODMsgPayload  = 64 * 1024
	bodRequestTimeout = 18 * time.Second
)

var errBODUnknown = errors.New("Gateway peer does not know")

type overlayPeer struct {
	DiscoverySource string `json:"discovery_source,omitempty"`

	Protocol     int                      `json:"protocol"`
	Addr         string                   `json:"addr"`
	ID           string                   `json:"id"`
	Capabilities []string                 `json:"capabilities"`
	HeaderHeight int64                    `json:"header_height"`
	TipHash      string                   `json:"tip_hash,omitempty"`
	CacheBlocks  int                      `json:"cache_blocks"`
	LastSeen     time.Time                `json:"-"`
	BIP434       bool                     `json:"bip434"` // legacy alias: Gateway negotiated via BIP434
	Gateway      bool                     `json:"gateway"`
	GatewayWire  int                      `json:"gateway_wire,omitempty"`
	Protocols    []gatewayProtocolSupport `json:"protocols,omitempty"`
	Snapshot     *chainSnapshot           `json:"snapshot,omitempty"`
	Profiles     []string                 `json:"profiles,omitempty"`
	BlockRanges  []heightInterval         `json:"block_ranges,omitempty"`
	Extensions   []extensionCapability    `json:"extensions,omitempty"`
}

type blockLocation struct {
	Height    int64  `json:"height"`
	BlockHash string `json:"block_hash"`
}

type blockData struct {
	Height    int64  `json:"height"`
	BlockHash string `json:"block_hash"`
	Raw       []byte `json:"raw"`
}

type overlayRequest struct {
	IndexRange *indexPeerRangeRequest `json:"index_range,omitempty"`
	Version    int                    `json:"version,omitempty"` // legacy internal field; wire version lives in envelope
	Type       string                 `json:"type"`
	TxID       string                 `json:"txid,omitempty"`
	Vout       uint32                 `json:"vout,omitempty"`
	Address    string                 `json:"address,omitempty"`
	Height     int64                  `json:"height,omitempty"`
	BlockHash  string                 `json:"block_hash,omitempty"`
}

type overlayResponse struct {
	IndexManifests []indexPeerManifest `json:"index_manifests,omitempty"`
	IndexPage      *indexPeerPage      `json:"index_page,omitempty"`
	Version        int                 `json:"version"`
	OK             bool                `json:"ok"`
	Type           string              `json:"type,omitempty"`
	Error          string              `json:"error,omitempty"`
	PeerID         string              `json:"peer_id,omitempty"`
	Capabilities   []string            `json:"capabilities,omitempty"`
	HeaderHeight   int64               `json:"header_height,omitempty"`
	TipHash        string              `json:"tip_hash,omitempty"`
	CacheBlocks    int                 `json:"cache_blocks,omitempty"`
	BlockLocation  *blockLocation      `json:"block_location,omitempty"`
	BlockData      *blockData          `json:"block_data,omitempty"` // retained for internal compatibility; not sent on bodmsg
	TxLocation     *txLocation         `json:"tx_location,omitempty"`
	SpendLocation  *spendLocation      `json:"spend_location,omitempty"`
	AddressUTXOs   *addressUTXOSet     `json:"address_utxos,omitempty"`
	Status         *bodStatus          `json:"status,omitempty"`
	ClaimSnapshot  *chainSnapshot      `json:"claim_snapshot,omitempty"`
}

type bodEnvelope struct {
	Wire    int             `json:"wire"`
	Kind    string          `json:"kind"` // request|response
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Status  string          `json:"status,omitempty"` // ok|unknown|unsupported|invalid|busy|error
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type overlayServer struct {
	live map[string]*livePeerView
	mu   sync.Mutex
	tcp  net.Listener
	udp  *net.UDPConn
	stop chan struct{}
	id   string
	app  *app

	connections             chan struct{}
	standardGetDataRequests uint64
	standardBlocksServed    uint64
	standardBlockMisses     uint64
}

func newOverlayServer(a *app) *overlayServer {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return &overlayServer{id: hex.EncodeToString(b[:]), app: a, connections: make(chan struct{}, 64)}
}

func (s *overlayServer) running() bool  { s.mu.Lock(); defer s.mu.Unlock(); return s.tcp != nil }
func (s *overlayServer) peerID() string { return s.id }

func (s *overlayServer) start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tcp != nil {
		return nil
	}
	addr := s.app.gatewayListen
	if addr == "" {
		addr = fmt.Sprintf(":%d", overlayTCPPort)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	var udp *net.UDPConn
	s.app.settingsMu.RLock()
	lanEnabled := s.app.settings.LANDiscovery && releaseFeatureAvailable("gateway-peerhood")
	s.app.settingsMu.RUnlock()
	if s.app.gatewayListen == "" && lanEnabled {
		udp, err = net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: overlayUDPPort})
	}
	if err != nil {
		ln.Close()
		return err
	}
	s.tcp, s.udp, s.stop = ln, udp, make(chan struct{})
	go s.acceptLoop(ln, s.stop)
	if udp != nil {
		go s.discoveryLoop(udp, s.stop)
	}
	return nil
}

func (s *overlayServer) stopServer() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tcp == nil {
		return
	}
	close(s.stop)
	_ = s.tcp.Close()
	if s.udp != nil {
		_ = s.udp.Close()
	}
	s.tcp, s.udp, s.stop = nil, nil, nil
}

func (s *overlayServer) acceptLoop(ln net.Listener, stop <-chan struct{}) {
	for {
		c, err := ln.Accept()
		if err != nil {
			select {
			case <-stop:
				return
			default:
				return
			}
		}
		select {
		case s.connections <- struct{}{}:
			go func() { defer func() { <-s.connections }(); s.handleConn(c) }()
		default:
			_ = c.Close()
		}
	}
}

func (a *app) peerCapabilitiesLegacy() ([]string, int64, string, int) {
	st := a.getStatus()
	height, tip := st.HeaderHeight, st.TipHash
	if height >= 0 && tip == "" && st.HeaderCount > 0 {
		if h, err := a.readSelectedHeader(st.HeaderCount - 1); err == nil {
			x := hash256(h)
			tip = reverseHex(x[:])
		}
	}
	cacheBlocks := a.publicCacheBlockCount()
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	core := inspectCore(settings)
	caps := []string{"capabilities"}
	if st.HeaderCount > 0 || core.Connected {
		caps = append(caps, "blockloc")
	}
	if cacheBlocks > 0 || core.Connected {
		// Raw block transfer itself remains standard Bitcoin getdata/block.
		caps = append(caps, "blockdata")
	}
	if core.Connected && core.TxIndex {
		caps = append(caps, "txloc")
	} else {
		a.cacheMu.RLock()
		has := false
		for txid := range a.cacheIndex.Tx {
			if !a.cacheIndex.PrivateTx[txid] {
				has = true
				break
			}
		}
		a.cacheMu.RUnlock()
		if has {
			caps = append(caps, "txloc")
		}
	}
	if core.Connected && core.SpenderIndexEnabled {
		caps = append(caps, "spendloc")
	} else if len(a.graphCoverage()) > 0 {
		caps = append(caps, "spendloc")
	} else {
		a.cacheMu.RLock()
		has := false
		for outpoint := range a.cacheIndex.Spends {
			if !a.cacheIndex.PrivateSpends[outpoint] {
				has = true
				break
			}
		}
		a.cacheMu.RUnlock()
		if has {
			caps = append(caps, "spendloc")
		}
	}
	if len(a.publishedIndexManifests()) > 0 {
		caps = append(caps, "index_manifest", "index_range")
	}
	return uniqueStrings(caps), height, tip, cacheBlocks
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// handshakeInbound speaks the same Bitcoin version/feature/verack sequence as
// a normal Bitcoin peer, with BIP434 negotiation inserted between version and
// verack. Ordinary non-BOD peers are allowed to complete the handshake.
func (s *overlayServer) handshakeInbound(c net.Conn) (*peerConn, error) {
	addr := c.RemoteAddr().String()
	p := &peerConn{conn: c, addr: addr, localProtocols: s.app.localGatewayProtocols()}
	_ = c.SetDeadline(time.Now().Add(12 * time.Second))
	flags, height := s.app.localBitcoinAdvertisement()
	if err := writeMessage(c, "version", makeVersionPayloadAtHeight(addr, flags, height)); err != nil {
		return nil, err
	}
	gotVersion, gotVerack := false, false
	sentVerack, sentFeature := false, false
	peerFeature, peerWire := false, 0
	for !(gotVersion && gotVerack && sentVerack) {
		m, err := readHandshakeMessage(c)
		if err != nil {
			return nil, err
		}
		switch m.command {
		case "version":
			if gotVersion || gotVerack || len(m.payload) < 12 {
				return nil, fmt.Errorf("short version message")
			}
			p.version = int32(binary.LittleEndian.Uint32(m.payload[0:4]))
			p.services = binary.LittleEndian.Uint64(m.payload[4:12])
			p.startHeight = parseVersionStartHeight(m.payload)
			gotVersion = true
			if p.version >= 70016 {
				if err := writeMessage(c, "sendaddrv2", nil); err != nil {
					return nil, err
				}
			}
			if releaseFeatureAvailable("gateway-peerhood") && p.version >= 70017 && !sentFeature {
				if err := writeMessage(c, "feature", makeFeaturePayload(gatewayFeatureID, gatewayFeatureData())); err != nil {
					return nil, err
				}
				sentFeature = true
			}
			if !sentVerack {
				if err := writeMessage(c, "verack", nil); err != nil {
					return nil, err
				}
				sentVerack = true
			}
		case "sendaddrv2":
			if gotVersion && !gotVerack && len(m.payload) == 0 {
				p.wantsAddrV2 = true
			}
		case "feature":
			if !gotVersion || gotVerack || p.version < 70017 {
				return nil, fmt.Errorf("feature outside the BIP434 negotiation window")
			}
			id, data, err := parseFeaturePayload(m.payload)
			if err != nil {
				return nil, fmt.Errorf("invalid feature message: %w", err)
			}
			if id == gatewayFeatureID {
				if w, ok := gatewayFeatureWireVersion(data); ok {
					peerFeature, peerWire = true, w
					p.featureData = append([]byte(nil), data...)
				}
			}
		case "verack":
			if !gotVersion || gotVerack || len(m.payload) != 0 {
				return nil, fmt.Errorf("invalid verack order or payload")
			}
			gotVerack = true
		case "ping":
			_ = writeMessage(c, "pong", m.payload)
		default:
			// Unknown messages during feature negotiation are ignored.
		}
	}
	p.gateway = sentFeature && peerFeature && peerWire >= minGatewayWireVersion && peerWire <= gatewayWireVersion
	p.gatewayWire = peerWire
	_ = c.SetDeadline(time.Time{})
	return p, nil
}

func (s *overlayServer) handleConn(c net.Conn) {
	s.observeConn(c, "handshake", "Bitcoin P2P")
	defer s.forgetConn(c)
	defer c.Close()
	p, err := s.handshakeInbound(c)
	if err != nil {
		return
	}
	s.observeConn(c, "connected", "Bitcoin P2P")
	s.mu.Lock()
	if v := s.live[c.RemoteAddr().String()]; v != nil {
		v.Services = p.services
		v.ProtocolVersion = p.version
	}
	s.mu.Unlock()
	if p.gateway {
		s.observeConn(c, "feature_negotiated", "Bitcoin P2P")
	}
	if p.gateway && s.app.network != nil {
		if err := exchangeGatewayInbound(p); err != nil {
			return
		}
		s.observeConn(c, "connected", "Gateway On Demand")
		s.mu.Lock()
		if v := s.live[c.RemoteAddr().String()]; v != nil {
			v.Gateway = true
		}
		s.mu.Unlock()
		managed := s.app.network.adoptInbound(p)
		if managed == nil {
			return
		}
		defer managed.close("Inbound connection ended")
		<-managed.done
		return
	}
	addrAnswered := false
	addressBudget := 5000
	var bodBudget, satBudget sessionServiceBudget
	seenRequestIDs := map[string]bool{}
	for {
		_ = c.SetReadDeadline(time.Now().Add(120 * time.Second))
		m, err := readMessage(c)
		if err != nil {
			return
		}
		_ = c.SetWriteDeadline(time.Now().Add(20 * time.Second))
		switch m.command {
		case "ping":
			if len(m.payload) == 8 {
				_ = writeMessage(c, "pong", m.payload)
			}
		case "feature":
			return // Late feature negotiation is a protocol violation.
		case "getaddr":
			if !addrAnswered {
				addrAnswered = true
				rows := []advertisedPeer{}
				if s.app.network != nil {
					rows = s.app.network.addressReplyRows()
				}
				command, payload := addressMessage(rows, p.wantsAddrV2)
				_ = writeMessage(c, command, payload)
			}
		case "addr", "addrv2":
			rows, e := parseAddressMessage(m)
			addressBudget -= len(rows)
			if e != nil || addressBudget < 0 {
				return
			}
		case "getheaders", "getblocks":
			s.app.settingsMu.RLock()
			allowed := s.app.settings.ServeData
			s.app.settingsMu.RUnlock()
			if allowed {
				s.handleChainRequest(c, m.command, m.payload)
			} else {
				writeBitcoinUnavailable(c, m)
			}
		case "getdata":
			s.app.settingsMu.RLock()
			serveBitcoin := s.app.settings.ServeData
			s.app.settingsMu.RUnlock()
			if !serveBitcoin {
				writeBitcoinUnavailable(c, m)
				continue
			}
			s.observeConn(c, "active", "Bitcoin P2P")
			s.handleGetData(c, m.payload)
		case "gwmsg":
			if !releaseFeatureAvailable("gateway-peerhood") {
				continue
			}
			handleGatewayMessage(c, m.payload, p)
			if p.gateway && len(p.gatewayProtocols) > 0 {
				s.observeConn(c, "connected", "Gateway On Demand")
				s.mu.Lock()
				if v := s.live[c.RemoteAddr().String()]; v != nil {
					v.Gateway = true
				}
				s.mu.Unlock()
			}
		case "satmsg":
			if !p.gateway {
				return
			}
			if _, ok := protocolVersionSupported(p.gatewayProtocols, satlineProtocolID, 1, 1); !ok {
				return
			}
			var env satlineEnvelope
			if strictSatlineJSON(m.payload, &env) != nil || !validSatlineRequestID(env.ID) {
				continue
			}
			previous := satBudget.started
			status := satBudget.take(time.Now(), 16, env.ID)
			if satBudget.started != previous {
				seenRequestIDs = map[string]bool{}
			}
			if !s.app.satlineNetworkingEnabled() {
				status = "unsupported"
			}
			if status != "" {
				writeSatlineUnavailable(c, env.ID, status)
				continue
			}
			s.observeConn(c, "active", "Satline")
			s.app.handleSatlineWire(c, m.payload, seenRequestIDs)
		case "bodmsg":
			if !p.gateway || !p.bod {
				// Feature specs based on BIP434 must not be used without negotiation.
				continue
			}
			if len(m.payload) > maxBODMsgPayload {
				return
			}
			var meta bodEnvelope
			if json.Unmarshal(m.payload, &meta) == nil && meta.ID != "" && len(meta.ID) <= 128 {
				if status := bodBudget.take(time.Now(), 64, meta.ID); status != "" {
					s.writeBODResponse(c, bodEnvelope{Wire: bodWireVersion, Kind: "response", Type: meta.Type, ID: meta.ID, Status: status, Error: "request budget or duplicate ID"})
					continue
				}
			}
			s.app.settingsMu.RLock()
			serveBOD := s.app.settings.ServeGatewayData
			s.app.settingsMu.RUnlock()
			if !serveBOD {
				s.writeBODResponse(c, bodEnvelope{Wire: bodWireVersion, Kind: "response", Type: meta.Type, ID: meta.ID, Status: "unsupported", Error: "Bitcoin on Demand serving is disabled"})
				continue
			}
			s.observeConn(c, "active", "Bitcoin on Demand")
			s.mu.Lock()
			if v := s.live[c.RemoteAddr().String()]; v != nil {
				v.BOD = true
			}
			s.mu.Unlock()
			s.handleBODMsg(c, m.payload)
		default:
			// This BOD listener is not a full Bitcoin node. Unknown/unsupported
			// standard messages are ignored rather than turning BOD into Core.
		}
	}
}

func (s *overlayServer) handleGetData(c net.Conn, payload []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s.handleGetDataContext(ctx, c, payload)
}

func (s *overlayServer) handleGetDataContext(ctx context.Context, c net.Conn, payload []byte) {
	count, used, err := decodeCompactSizeMinimal(payload)
	if err != nil || count > 128 || len(payload) != used+int(count)*36 {
		return
	}
	pos := used
	notFound := make([][]byte, 0)
	served := uint64(0)
	for i := uint64(0); i < count; i++ {
		if ctx.Err() != nil {
			return
		}
		s.app.settingsMu.RLock()
		allowed := s.app.settings.ServeData
		s.app.settingsMu.RUnlock()
		if !allowed {
			return
		}
		if pos+36 > len(payload) {
			return
		}
		item := append([]byte(nil), payload[pos:pos+36]...)
		typ := binary.LittleEndian.Uint32(item[:4])
		var rawHash [32]byte
		copy(rawHash[:], item[4:36])
		pos += 36

		if typ != 2 && typ != 0x40000002 { // MSG_BLOCK / MSG_WITNESS_BLOCK
			notFound = append(notFound, item)
			continue
		}
		hash := reverseHex(rawHash[:])
		bd, err := s.app.storageBlockForServing(ctx, hash)
		if err != nil || len(bd.Raw) < 81 {
			notFound = append(notFound, item)
			continue
		}
		response := bd.Raw
		if typ == 2 {
			response, err = stripBlockWitness(bd.Raw)
			if err != nil {
				notFound = append(notFound, item)
				continue
			}
		}
		s.app.settingsMu.RLock()
		allowed = s.app.settings.ServeData
		s.app.settingsMu.RUnlock()
		if !allowed || ctx.Err() != nil {
			return
		}
		if err := writeMessage(c, "block", response); err != nil {
			return
		}
		served++
	}

	if ctx.Err() != nil {
		return
	}
	if len(notFound) > 0 {
		var b bytes.Buffer
		b.Write(encodeVarInt(uint64(len(notFound))))
		for _, item := range notFound {
			b.Write(item)
		}
		_ = writeMessage(c, "notfound", b.Bytes())
	}

	s.mu.Lock()
	s.standardGetDataRequests += count
	s.standardBlocksServed += served
	s.standardBlockMisses += uint64(len(notFound))
	s.mu.Unlock()
}

func randomRequestID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *overlayServer) handleBODMsg(c net.Conn, payload []byte) {
	var env bodEnvelope
	if json.Unmarshal(payload, &env) != nil || env.Kind != "request" || env.ID == "" || len(env.ID) > 128 {
		s.writeBODResponse(c, bodEnvelope{Wire: bodWireVersion, Kind: "response", Type: env.Type, ID: env.ID, Status: "invalid", Error: "invalid BOD envelope"})
		return
	}
	if env.Wire != bodWireVersion {
		s.writeBODResponse(c, bodEnvelope{Wire: bodWireVersion, Kind: "response", Type: env.Type, ID: env.ID, Status: "unsupported", Error: "unsupported BOD wire version"})
		return
	}
	var req overlayRequest
	if len(env.Payload) > 0 && json.Unmarshal(env.Payload, &req) != nil {
		s.writeBODResponse(c, bodEnvelope{Wire: bodWireVersion, Kind: "response", Type: env.Type, ID: env.ID, Status: "invalid", Error: "invalid request payload"})
		return
	}
	req.Type = bodInternalType(env.Type)
	if req.Type == "address_utxos" {
		s.writeBODResponse(c, bodEnvelope{Wire: bodWireVersion, Kind: "response", Type: env.Type, ID: env.ID, Status: "unsupported", Error: "Remote address-wide UTXO scans are not supported"})
		return
	}
	resp, status := s.app.answerBODRequest(req)
	raw, _ := json.Marshal(resp)
	out := bodEnvelope{Wire: bodWireVersion, Kind: "response", Type: env.Type, ID: env.ID, Status: status, Payload: raw}
	if status != "ok" {
		out.Error = resp.Error
	}
	s.writeBODResponse(c, out)
}

func (s *overlayServer) writeBODResponse(c net.Conn, env bodEnvelope) {
	raw, err := json.Marshal(env)
	if err != nil || len(raw) > maxBODMsgPayload {
		return
	}
	_ = writeMessage(c, "bodmsg", raw)
}

func (a *app) answerBODRequest(req overlayRequest) (overlayResponse, string) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		return overlayResponse{}, "unsupported"
	}
	if req.Type == "index_manifest" || req.Type == "index_range" {
		return a.answerIndexPeerRequest(req)
	}
	claim := a.bestClaimSnapshot()
	resp := overlayResponse{Version: bodWireVersion, OK: false, Type: req.Type, PeerID: a.source.peerID(), ClaimSnapshot: &claim}
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()

	unknown := func(err error) (overlayResponse, string) {
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Error = "unknown"
		}
		return resp, "unknown"
	}

	switch req.Type {
	case "status":
		st := a.buildBODStatus()
		resp.OK, resp.Status = true, &st
		resp.Capabilities, resp.HeaderHeight, resp.TipHash, resp.CacheBlocks = st.Capabilities, st.Snapshot.Height, st.Snapshot.Hash, st.CacheBlocks
		return resp, "ok"
	case "capabilities": // legacy alias; status is the implementation-independent form.
		st := a.buildBODStatus()
		resp.OK, resp.Status = true, &st
		resp.Capabilities, resp.HeaderHeight, resp.TipHash, resp.CacheBlocks = st.Capabilities, st.Snapshot.Height, st.Snapshot.Hash, st.CacheBlocks
		return resp, "ok"
	case "blockloc":
		var loc blockLocation
		var err error
		if core := inspectCore(settings); core.Connected {
			loc, err = coreBlockLocationByHeight(settings, req.Height)
		} else if loc, err = a.mountedCoreLocationByHeight(req.Height); err != nil {
			loc, err = a.cachedBlockLocationByHeight(req.Height)
		}
		if err != nil {
			return unknown(err)
		}
		resp.OK, resp.BlockLocation = true, &loc
		return resp, "ok"
	case "hashloc":
		var loc blockLocation
		var err error
		if core := inspectCore(settings); core.Connected {
			loc, err = coreBlockLocationByHash(settings, req.BlockHash)
		} else if loc, err = a.mountedCoreLocationByHash(req.BlockHash); err != nil {
			loc, err = a.cachedBlockLocationByHash(req.BlockHash)
		}
		if err != nil {
			return unknown(err)
		}
		resp.OK, resp.BlockLocation = true, &loc
		return resp, "ok"
	case "txloc":
		if !validHash(req.TxID) {
			resp.Error = "invalid txid"
			return resp, "invalid"
		}
		var loc txLocation
		var err error
		st := inspectCore(settings)
		if st.Connected && st.TxIndex {
			loc, err = coreTxLocation(settings, req.TxID)
		} else {
			loc, err = a.cachedTxLocationForServing(req.TxID)
		}
		if err != nil {
			if c, e := a.cachedTxLocationForServing(req.TxID); e == nil {
				loc, err = c, nil
			}
		}
		if err != nil {
			return unknown(err)
		}
		resp.OK, resp.TxLocation = true, &loc
		return resp, "ok"
	case "spendloc":
		if !validHash(req.TxID) {
			resp.Error = "invalid txid"
			return resp, "invalid"
		}
		var loc spendLocation
		var err error
		st := inspectCore(settings)
		if st.Connected && st.SpenderIndex {
			// A synced Core spender index can make an authoritative negative claim
			// for the exact Core chain snapshot advertised with this response.
			loc, err = coreSpendLocation(settings, req.TxID, req.Vout)
		} else if g, ok := a.graphFindSpend(req.TxID, req.Vout); ok {
			// Native graph sharing remains a positive locator hint on BOD wire v1.
			// The receiver still verifies the underlying Bitcoin transaction.
			loc = spendLocation{Outpoint: g.Outpoint, Found: true, SpendingTxID: g.SpendingTxID, BlockHash: g.BlockHash, Height: g.Height, InputIndex: g.SpendingVin}
		} else {
			// Legacy cache knowledge is also positive-only.
			loc, err = a.cachedSpendLocationForServing(req.TxID, req.Vout)
		}
		if err != nil {
			if c, e := a.cachedSpendLocationForServing(req.TxID, req.Vout); e == nil {
				loc, err = c, nil
			}
		}
		if err != nil {
			return unknown(err)
		}
		resp.OK, resp.SpendLocation = true, &loc
		return resp, "ok"
	case "address_utxos":
		set, err := coreAddressUTXOs(settings, req.Address)
		if err != nil {
			return unknown(err)
		}
		resp.OK, resp.AddressUTXOs = true, &set
		return resp, "ok"

	default:
		resp.Error = "unknown BOD request type"
		return resp, "unsupported"
	}
}

func (s *overlayServer) discoveryLoop(udp *net.UDPConn, stop <-chan struct{}) {
	buf := make([]byte, 1024)
	for {
		_ = udp.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, addr, err := udp.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				select {
				case <-stop:
					return
				default:
					continue
				}
			}
			return
		}
		if strings.TrimSpace(string(buf[:n])) != discoveryMagic {
			continue
		}
		msg := fmt.Sprintf("%s|%d|%s", announceMagic, overlayTCPPort, s.id)
		_, _ = udp.WriteToUDP([]byte(msg), addr)
	}
}

func discoverOverlayPeers(manual string) []overlayPeer {
	if !releaseFeatureAvailable("gateway-peerhood") {
		return nil
	}
	found := map[string]overlayPeer{}
	if strings.TrimSpace(manual) != "" {
		a := strings.TrimSpace(manual)
		if !strings.Contains(a, ":") {
			a = net.JoinHostPort(a, fmt.Sprint(overlayTCPPort))
		}
		found[a] = overlayPeer{Protocol: bodWireVersion, Addr: a, ID: "manual", HeaderHeight: -1, LastSeen: time.Now()}
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err == nil {
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(650 * time.Millisecond))
		targets := []*net.UDPAddr{{IP: net.IPv4bcast, Port: overlayUDPPort}}
		if ifaces, e := net.Interfaces(); e == nil {
			for _, ifi := range ifaces {
				addrs, _ := ifi.Addrs()
				for _, a := range addrs {
					ipnet, ok := a.(*net.IPNet)
					if !ok {
						continue
					}
					ip := ipnet.IP.To4()
					if ip == nil || len(ipnet.Mask) != 4 {
						continue
					}
					m := ipnet.Mask
					b := net.IPv4(ip[0]|^m[0], ip[1]|^m[1], ip[2]|^m[2], ip[3]|^m[3])
					targets = append(targets, &net.UDPAddr{IP: b, Port: overlayUDPPort})
				}
			}
		}
		sent := map[string]bool{}
		for _, t := range targets {
			if !sent[t.String()] {
				sent[t.String()] = true
				_, _ = conn.WriteToUDP([]byte(discoveryMagic), t)
			}
		}
		buf := make([]byte, 2048)
		for {
			n, src, e := conn.ReadFromUDP(buf)
			if e != nil {
				break
			}
			parts := strings.Split(strings.TrimSpace(string(buf[:n])), "|")
			if len(parts) < 3 || parts[0] != announceMagic {
				continue
			}
			addr := net.JoinHostPort(src.IP.String(), parts[1])
			found[addr] = overlayPeer{Protocol: bodWireVersion, Addr: addr, ID: parts[2], HeaderHeight: -1, LastSeen: time.Now()}
		}
	}
	out := make([]overlayPeer, 0, len(found))
	for _, p := range found {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr < out[j].Addr })
	return out
}

func bodWireType(internal string) string {
	switch internal {
	case "txloc":
		return "tx_location"
	case "spendloc":
		return "spender_location"
	case "blockloc":
		return "block_location"
	case "hashloc":
		return "hash_location"
	case "address_utxos":
		return "address_utxos"
	case "capabilities":
		return "capabilities"
	default:
		return internal
	}
}

func bodInternalType(wire string) string {
	switch wire {
	case "tx_location":
		return "txloc"
	case "spender_location":
		return "spendloc"
	case "block_location":
		return "blockloc"
	case "hash_location":
		return "hashloc"
	default:
		return wire
	}
}

func queryOverlayPeer(peer overlayPeer, req overlayRequest) (overlayResponse, error) {
	if err := requireReleaseFeature("gateway-peerhood"); err != nil {
		return overlayResponse{}, err
	}
	c, err := net.DialTimeout("tcp", peer.Addr, 4*time.Second)
	if err != nil {
		return overlayResponse{}, err
	}
	p, err := handshakeOutbound(c, peer.Addr)
	if err != nil {
		return overlayResponse{}, err
	}
	defer p.conn.Close()
	if !p.bod {
		return overlayResponse{}, fmt.Errorf("peer completed Bitcoin handshake but did not negotiate Gateway+BOD")
	}
	typ := bodWireType(req.Type)
	rawPayload, _ := json.Marshal(req)
	env := bodEnvelope{Wire: bodWireVersion, Kind: "request", Type: typ, ID: randomRequestID(), Payload: rawPayload}
	raw, _ := json.Marshal(env)
	if len(raw) > maxBODMsgPayload {
		return overlayResponse{}, fmt.Errorf("BOD request too large")
	}
	_ = p.conn.SetDeadline(time.Now().Add(bodRequestTimeout))
	if err := writeMessage(p.conn, "bodmsg", raw); err != nil {
		return overlayResponse{}, err
	}
	for {
		m, err := readMessage(p.conn)
		if err != nil {
			return overlayResponse{}, err
		}
		switch m.command {
		case "ping":
			_ = writeMessage(p.conn, "pong", m.payload)
		case "bodmsg":
			if len(m.payload) > maxBODMsgPayload {
				return overlayResponse{}, fmt.Errorf("BOD response too large")
			}
			var got bodEnvelope
			if json.Unmarshal(m.payload, &got) != nil || got.Kind != "response" || got.ID != env.ID || got.Type != env.Type {
				continue
			}
			if got.Wire != bodWireVersion {
				return overlayResponse{}, fmt.Errorf("peer BOD wire %d is incompatible", got.Wire)
			}
			var resp overlayResponse
			_ = json.Unmarshal(got.Payload, &resp)
			resp.Version = got.Wire
			switch got.Status {
			case "ok":
				resp.OK = true
				return resp, nil
			case "unknown":
				return resp, fmt.Errorf("%w: %s", errBODUnknown, got.Error)
			case "unsupported", "invalid", "busy", "error":
				return resp, fmt.Errorf("BOD %s: %s", got.Status, got.Error)
			default:
				return resp, fmt.Errorf("invalid BOD response status")
			}
		}
	}
}

func (a *app) cachedOverlayPeers() []overlayPeer {
	if !releaseFeatureAvailable("gateway-peerhood") {
		return nil
	}
	if a.network != nil {
		return a.network.gatewayPeers()
	}
	a.peerMu.RLock()
	defer a.peerMu.RUnlock()
	return append([]overlayPeer{}, a.overlayPeers...)
}

// refreshPeersAsync keeps UI/status calls non-blocking. Bitcoin address-table
// discovery can take seconds; it must never sit on the onboarding or settings
// request path.
func (a *app) refreshPeersAsync() {
	if !releaseFeatureAvailable("gateway-peerhood") {
		return
	}
	if a.network != nil {
		return
	}
	a.peerMu.Lock()
	if a.peerDiscovering || (!a.peerScanAt.IsZero() && time.Since(a.peerScanAt) < 20*time.Second) {
		a.peerMu.Unlock()
		return
	}
	a.peerDiscovering = true
	a.peerMu.Unlock()
	go func() {
		_ = a.getOverlayPeers()
		a.peerMu.Lock()
		a.peerDiscovering = false
		a.peerMu.Unlock()
	}()
}

func (a *app) getOverlayPeers() []overlayPeer {
	if !releaseFeatureAvailable("gateway-peerhood") {
		return nil
	}
	if a.network != nil {
		return a.network.gatewayPeers()
	}
	// Discovery can involve Bitcoin getaddr round-trips; cache a confirmed peer
	// set briefly so ordinary queries do not re-scan the network each time.
	a.peerMu.RLock()
	if len(a.overlayPeers) > 0 && time.Since(a.peerScanAt) < 30*time.Second {
		out := append([]overlayPeer(nil), a.overlayPeers...)
		a.peerMu.RUnlock()
		return out
	}
	a.peerMu.RUnlock()

	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	seeds := []overlayPeer{}
	for _, address := range configuredGatewayBootstrapPeers(settings) {
		seeds = append(seeds, overlayPeer{Protocol: bodWireVersion, Addr: address, ID: "configured", HeaderHeight: -1, DiscoverySource: "manual_gateway"})
	}
	if settings.LANDiscovery {
		seeds = append(seeds, discoverOverlayPeers("")...)
	}
	selfID := a.source.peerID()
	peers := make([]overlayPeer, 0, len(seeds))
	for _, p := range seeds {
		if p.ID == selfID {
			continue
		}
		resp, err := a.queryGatewayPeer(p, overlayRequest{Type: "status"})
		if err != nil {
			continue
		}
		p.Protocol = bodWireVersion
		p.BIP434 = true
		p.Gateway = true
		p.GatewayWire = gatewayWireVersion
		if resp.PeerID != "" {
			p.ID = resp.PeerID
		}
		p.Protocols = []gatewayProtocolSupport{{ID: bodProtocolID, Versions: []int{bodWireVersion}}} // Only BOD was observed; do not copy our own Satline capabilities onto a peer.
		p.Capabilities = resp.Capabilities
		p.HeaderHeight = resp.HeaderHeight
		p.TipHash = resp.TipHash
		p.CacheBlocks = resp.CacheBlocks
		if resp.Status != nil {
			p.Snapshot = &resp.Status.Snapshot
			p.Profiles = append([]string(nil), resp.Status.Profiles...)
			p.BlockRanges = append([]heightInterval(nil), resp.Status.BlockRanges...)
			p.Extensions = append([]extensionCapability(nil), resp.Status.Extensions...)
		}
		peers = append(peers, p)
	}
	a.peerMu.Lock()
	a.overlayPeers = peers
	a.peerScanAt = time.Now()
	a.peerMu.Unlock()
	return peers
}

func peerHasCap(p overlayPeer, cap string) bool {
	for _, c := range p.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

func (a *app) queryPeers(req overlayRequest, capability string) (overlayResponse, overlayPeer, error) {
	// Capability locks also cover the Core/cache shortcuts used by CLI location
	// commands; those are distinct from known-block positional navigation.
	for _, requested := range []string{capability, req.Type} {
		feature := map[string]string{"txloc": "tx-locator", "spendloc": "txo-spender", "utxos": "address-state"}[requested]
		if feature != "" {
			if err := requireReleaseFeature(feature); err != nil {
				return overlayResponse{}, overlayPeer{}, err
			}
		}
	}
	// Local Core/cache are legitimate local knowledge sources. The result still
	// gets verified by the normal resolution path before it becomes trusted.
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	st := inspectCore(settings)
	localPeer := overlayPeer{Protocol: bodWireVersion, Addr: "local", ID: a.source.peerID(), HeaderHeight: a.getStatus().HeaderHeight, BIP434: true, Gateway: true, GatewayWire: gatewayWireVersion, Protocols: a.localGatewayProtocols()}
	if capability == "blockloc" {
		var loc blockLocation
		var err error
		provider := "local Core"
		if st.Connected {
			if req.Type == "hashloc" {
				loc, err = coreBlockLocationByHash(settings, req.BlockHash)
			} else {
				loc, err = coreBlockLocationByHeight(settings, req.Height)
			}
		} else {
			provider = "mounted Core block store"
			if req.Type == "hashloc" {
				loc, err = a.mountedCoreLocationByHash(req.BlockHash)
			} else {
				loc, err = a.mountedCoreLocationByHeight(req.Height)
			}
		}
		if err == nil {
			localPeer.Addr, localPeer.Capabilities = provider, []string{"blockloc"}
			return overlayResponse{Version: bodWireVersion, OK: true, Type: req.Type, PeerID: a.source.peerID(), BlockLocation: &loc}, localPeer, nil
		}
	}
	if capability == "txloc" && st.Connected && st.TxIndex {
		if loc, err := coreTxLocation(settings, req.TxID); err == nil {
			localPeer.Addr, localPeer.Capabilities = "local Core", []string{"txloc"}
			return overlayResponse{Version: bodWireVersion, OK: true, Type: req.Type, PeerID: a.source.peerID(), TxLocation: &loc}, localPeer, nil
		}
	}
	if capability == "spendloc" && st.Connected && st.SpenderIndex {
		if loc, err := coreSpendLocation(settings, req.TxID, req.Vout); err == nil {
			localPeer.Addr, localPeer.Capabilities = "local Core", []string{"spendloc"}
			return overlayResponse{Version: bodWireVersion, OK: true, Type: req.Type, PeerID: a.source.peerID(), SpendLocation: &loc}, localPeer, nil
		}
	}
	if capability == "utxos" && st.Connected {
		if set, err := coreAddressUTXOs(settings, req.Address); err == nil {
			localPeer.Addr, localPeer.Capabilities = "local Core", []string{"utxos"}
			return overlayResponse{Version: bodWireVersion, OK: true, Type: req.Type, PeerID: a.source.peerID(), AddressUTXOs: &set}, localPeer, nil
		}
	}
	if capability == "txloc" {
		if loc, err := a.cachedTxLocation(req.TxID); err == nil {
			localPeer.Addr, localPeer.Capabilities = "local verified Gateway index", []string{"txloc"}
			return overlayResponse{Version: bodWireVersion, OK: true, Type: req.Type, PeerID: a.source.peerID(), TxLocation: &loc}, localPeer, nil
		}
	}
	if capability == "spendloc" {
		if g, ok := a.graphFindSpend(req.TxID, req.Vout); ok {
			loc := spendLocation{Outpoint: g.Outpoint, Found: true, SpendingTxID: g.SpendingTxID, BlockHash: g.BlockHash, Height: g.Height, InputIndex: g.SpendingVin}
			localPeer.Addr, localPeer.Capabilities = "local Bitcoin on Demand native graph", []string{"spendloc"}
			return overlayResponse{Version: bodWireVersion, OK: true, Type: req.Type, PeerID: a.source.peerID(), SpendLocation: &loc}, localPeer, nil
		}
		if loc, err := a.cachedSpendLocation(req.TxID, req.Vout); err == nil {
			localPeer.Addr, localPeer.Capabilities = "local verified Gateway index", []string{"spendloc"}
			return overlayResponse{Version: bodWireVersion, OK: true, Type: req.Type, PeerID: a.source.peerID(), SpendLocation: &loc}, localPeer, nil
		}
	}

	peers := a.getOverlayPeers()
	var errs []string
	for _, p := range peers {
		if capability != "" && !peerHasCap(p, capability) {
			continue
		}
		resp, err := a.queryGatewayPeer(p, req)
		if err == nil {
			return resp, p, nil
		}
		errs = append(errs, p.Addr+": "+err.Error())
	}
	if len(peers) == 0 {
		return overlayResponse{}, overlayPeer{}, fmt.Errorf("no Gateway peer advertising Bitcoin on Demand was found; leave header sync running, enable serving on another Gateway node, or set a manual Gateway peer")
	}
	return overlayResponse{}, overlayPeer{}, fmt.Errorf("Gateway peers could not answer: %s", strings.Join(errs, "; "))
}

// fetchBlockFromOverlay deliberately uses normal Bitcoin getdata/block. The
// BOD protocol supplies knowledge; Bitcoin P2P supplies raw blocks.
func (a *app) fetchBlockFromOverlay(hash string) (blockData, overlayPeer, error) {
	if a.network != nil {
		return a.network.fetchModuleBlock(hash)
	}
	peers := a.getOverlayPeers()
	rawHash, err := displayHashRaw(hash)
	if err != nil {
		return blockData{}, overlayPeer{}, err
	}
	var errs []string
	for _, p := range peers {
		if !peerHasCap(p, "blockdata") {
			continue
		}
		c, err := net.DialTimeout("tcp", p.Addr, 4*time.Second)
		if err != nil {
			errs = append(errs, p.Addr+": "+err.Error())
			continue
		}
		pc, err := handshakeOutbound(c, p.Addr)
		if err != nil {
			_ = c.Close()
			errs = append(errs, p.Addr+": "+err.Error())
			continue
		}
		var req bytes.Buffer
		req.Write(encodeVarInt(1))
		_ = binary.Write(&req, binary.LittleEndian, uint32(2))
		req.Write(rawHash[:])
		if err = writeMessage(pc.conn, "getdata", req.Bytes()); err == nil {
			var m message
			m, err = waitForBlockOrNotFound(pc, 22*time.Second)
			if err == nil && len(m.payload) >= 81 {
				_ = pc.conn.Close()
				height := int64(-1)
				if loc, _, e := a.queryBlockLocationByHash(hash); e == nil {
					height = loc.Height
				}
				return blockData{Height: height, BlockHash: strings.ToLower(hash), Raw: m.payload}, p, nil
			}
		}
		_ = pc.conn.Close()
		if err != nil {
			errs = append(errs, p.Addr+": "+err.Error())
		}
	}
	if len(errs) == 0 {
		return blockData{}, overlayPeer{}, fmt.Errorf("no Gateway peer advertised a local block store")
	}
	return blockData{}, overlayPeer{}, fmt.Errorf("%s", strings.Join(errs, "; "))
}

func (a *app) queryBlockLocationByHash(hash string) (blockLocation, overlayPeer, error) {
	resp, peer, err := a.queryPeers(overlayRequest{Type: "hashloc", BlockHash: strings.ToLower(hash)}, "blockloc")
	if err != nil || resp.BlockLocation == nil {
		return blockLocation{}, overlayPeer{}, err
	}
	return *resp.BlockLocation, peer, nil
}

func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// strconv is retained for compatibility with source readers/tools that expect
// it in this file's import closure when LAN bootstrap formats evolve.
var _ = strconv.Itoa
