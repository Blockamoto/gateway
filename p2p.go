package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	mrand "math/rand"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	// BIP 434 requires implementing nodes to advertise >= 70017.
	protocolVersion = int32(70017)
	mainnetPort     = 8333
	// Bitcoin mainnet protocol ceiling, also bounding unknown post-handshake
	// commands. The unauthenticated handshake has a much smaller budget.
	maxMessageSize = 4 * 1000 * 1000
	headersPerMsg  = 2000

	nodeNetworkService        = uint64(1) << 0
	nodeWitnessService        = uint64(1) << 3
	nodeNetworkLimitedService = uint64(1) << 10
)

var (
	mainnetMagic = [4]byte{0xf9, 0xbe, 0xb4, 0xd9}
	dnsSeeds     = []string{
		"dnsseed.bluematt.me",
		"seed.bitcoin.jonasschnelli.ch",
		"seed.btc.petertodd.net",
		"seed.bitcoin.sprovoost.nl",
		"dnsseed.emzy.de",
		"seed.bitcoin.wiz.biz",
		"seed.mainnet.achownodes.xyz",
	}
)

type peerConn struct {
	wantsAddrV2      bool
	localProtocols   []gatewayProtocolSupport
	conn             net.Conn
	addr             string
	services         uint64
	version          int32
	startHeight      int64
	gateway          bool
	gatewayWire      int
	gatewayProtocols []gatewayProtocolSupport
	bod              bool
	bodWire          int
	featureData      []byte
}

type message struct {
	command string
	payload []byte
}

func fetchBlock(hash [32]byte, expectedHeader []byte, preferred []string) ([]byte, string, error) {
	candidates := append([]string{}, preferred...)
	discovered, _ := discoverPeers()
	candidates = append(candidates, discovered...)
	seen := map[string]bool{}
	attempts := 0
	for _, addr := range candidates {
		if seen[addr] {
			continue
		}
		seen[addr] = true
		attempts++
		if attempts > 64 {
			break
		}
		p, err := connectPeer(addr)
		if err != nil {
			continue
		}
		// Standard archival Bitcoin peers advertise NODE_NETWORK. BOD sparse
		// peers are tried separately by fetchBlockFromOverlay and need not lie
		// about NODE_NETWORK just to serve a cached block.
		if p.services&nodeNetworkService == 0 {
			_ = p.conn.Close()
			continue
		}
		invType := uint32(2)
		if p.services&nodeWitnessService != 0 {
			invType = 0x40000002
		}
		var req bytes.Buffer
		req.Write(encodeVarInt(1))
		_ = binary.Write(&req, binary.LittleEndian, invType)
		req.Write(hash[:])
		if err = writeMessage(p.conn, "getdata", req.Bytes()); err != nil {
			_ = p.conn.Close()
			continue
		}
		msg, err := waitForBlockOrNotFound(p, 22*time.Second)
		_ = p.conn.Close()
		if err != nil {
			continue
		}
		if len(msg.payload) < 81 {
			continue
		}
		gotHash := hash256(msg.payload[:80])
		if gotHash != hash {
			continue
		}
		if len(expectedHeader) == 80 && !bytes.Equal(msg.payload[:80], expectedHeader) {
			continue
		}
		return msg.payload, p.addr, nil
	}
	return nil, "", fmt.Errorf("no archival peer served the requested block; peer availability varies, so try again")
}

func waitForBlockOrNotFound(p *peerConn, timeout time.Duration) (message, error) {
	p.conn.SetReadDeadline(time.Now().Add(timeout))
	defer p.conn.SetReadDeadline(time.Time{})
	for {
		m, err := readMessage(p.conn)
		if err != nil {
			return message{}, err
		}
		switch m.command {
		case "block":
			return m, nil
		case "notfound":
			return message{}, fmt.Errorf("peer reported notfound")
		case "ping":
			_ = writeMessage(p.conn, "pong", m.payload)
		}
	}
}

func connectAnyPeer(preferred []string, requireArchive bool) (*peerConn, error) {
	seen := map[string]bool{}
	try := func(candidates []string) *peerConn {
		for _, addr := range candidates {
			if seen[addr] {
				continue
			}
			seen[addr] = true
			p, err := connectPeer(addr)
			if err != nil {
				continue
			}
			if requireArchive && p.services&nodeNetworkService == 0 {
				_ = p.conn.Close()
				continue
			}
			return p
		}
		return nil
	}
	// Preferred peers really are a fast path now. In particular, a local
	// Bitcoin Core listener is attempted before any DNS-seed work is done.
	if p := try(preferred); p != nil {
		return p, nil
	}
	discovered, _ := discoverPeers()
	if p := try(discovered); p != nil {
		return p, nil
	}
	return nil, fmt.Errorf("could not connect to a suitable Bitcoin peer")
}

func discoverPeers() ([]string, error) {
	var all []string
	for _, seed := range dnsSeeds {
		ips, err := net.LookupIP(seed)
		if err != nil {
			continue
		}
		for _, ip := range ips {
			all = append(all, net.JoinHostPort(ip.String(), strconv.Itoa(mainnetPort)))
		}
		if len(all) >= 96 {
			break
		}
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("DNS seeds returned no peers")
	}
	r := mrand.New(mrand.NewSource(time.Now().UnixNano()))
	r.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	if len(all) > 96 {
		all = all[:96]
	}
	return all, nil
}

type advertisedPeer struct {
	Addr       string
	Services   uint64
	Timestamp  uint32
	Network    byte
	RawAddress []byte
	Port       uint16
	Invalid    string
}

// parseAddrPayload parses bounded legacy Bitcoin address records. Admission
// applies independent routability and standard Bitcoin service checks.
func parseAddrPayload(payload []byte) ([]advertisedPeer, error) {
	count, used, err := decodeCompactSizeMinimal(payload)
	if err != nil {
		return nil, err
	}
	if count > 1000 {
		return nil, fmt.Errorf("addr count too large: %d", count)
	}
	pos := used
	out := make([]advertisedPeer, 0, count)
	for i := uint64(0); i < count; i++ {
		if pos+30 > len(payload) {
			return nil, io.ErrUnexpectedEOF
		}
		timestamp := binary.LittleEndian.Uint32(payload[pos : pos+4])
		services := binary.LittleEndian.Uint64(payload[pos+4 : pos+12])
		ip16 := net.IP(append([]byte(nil), payload[pos+12:pos+28]...))
		port := binary.BigEndian.Uint16(payload[pos+28 : pos+30])
		pos += 30
		if port == 0 || ip16.IsUnspecified() || ip16.IsMulticast() {
			out = append(out, advertisedPeer{Invalid: "invalid legacy endpoint"})
			continue
		}
		ip := ip16
		if v4 := ip16.To4(); v4 != nil {
			ip = v4
		}
		out = append(out, advertisedPeer{Addr: net.JoinHostPort(ip.String(), strconv.Itoa(int(port))), Services: services, Timestamp: timestamp})
	}
	if pos != len(payload) {
		return nil, fmt.Errorf("trailing addr payload bytes")
	}
	return out, nil
}

func normalizePeer(s string) string {
	if _, _, err := net.SplitHostPort(s); err == nil {
		return s
	}
	return net.JoinHostPort(s, strconv.Itoa(mainnetPort))
}

// connectPeer establishes a normal Bitcoin P2P connection. If the peer negotiates
// Gateway via BIP 434, the Gateway hello advertises service protocols such as BOD.
// Lack of Gateway/BOD support never makes the ordinary Bitcoin connection unusable.
func connectPeer(addr string) (*peerConn, error) {
	c, err := net.DialTimeout("tcp", addr, 6*time.Second)
	if err != nil {
		return nil, err
	}
	return handshakeOutbound(c, addr)
}

func handshakeOutbound(c net.Conn, addr string) (*peerConn, error) {
	return handshakeOutboundProtocols(c, addr, localGatewayProtocols())
}
func handshakeOutboundProtocols(c net.Conn, addr string, protocols []gatewayProtocolSupport) (*peerConn, error) {
	return handshakeOutboundState(c, addr, protocols, 0, 0)
}
func handshakeOutboundState(c net.Conn, addr string, protocols []gatewayProtocolSupport, services uint64, height int64) (*peerConn, error) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		protocols = nil
		services &= nodeNetworkService | nodeNetworkLimitedService | nodeWitnessService
	}
	p := &peerConn{conn: c, addr: addr, localProtocols: protocols}
	c.SetDeadline(time.Now().Add(12 * time.Second))
	if err := writeMessage(c, "version", makeVersionPayloadAtHeight(addr, services, height)); err != nil {
		c.Close()
		return nil, err
	}
	gotVersion, gotVerack := false, false
	sentVerack, sentFeature := false, false
	peerFeature := false
	peerWire := 0
	for !(gotVersion && gotVerack && sentVerack) {
		m, err := readHandshakeMessage(c)
		if err != nil {
			c.Close()
			return nil, err
		}
		switch m.command {
		case "version":
			if gotVersion || gotVerack || len(m.payload) < 12 {
				c.Close()
				return nil, fmt.Errorf("short version message")
			}
			p.version = int32(binary.LittleEndian.Uint32(m.payload[0:4]))
			p.services = binary.LittleEndian.Uint64(m.payload[4:12])
			p.startHeight = parseVersionStartHeight(m.payload)
			if p.version >= 70016 {
				_ = writeMessage(c, "sendaddrv2", nil)
			}
			gotVersion = true
			if p.version >= 70017 && !sentFeature && len(protocols) > 0 {
				if err := writeMessage(c, "feature", makeFeaturePayload(gatewayFeatureID, gatewayFeatureData())); err != nil {
					c.Close()
					return nil, err
				}
				sentFeature = true
			}
			if !sentVerack {
				if err := writeMessage(c, "verack", nil); err != nil {
					c.Close()
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
				c.Close()
				return nil, fmt.Errorf("feature outside the BIP434 negotiation window")
			}
			id, data, err := parseFeaturePayload(m.payload)
			if err != nil {
				c.Close()
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
				c.Close()
				return nil, fmt.Errorf("invalid verack order or payload")
			}
			gotVerack = true
		case "ping":
			_ = writeMessage(c, "pong", m.payload)
		default:
			// Unknown pre-verack messages are intentionally ignored, matching
			// BIP434's extensibility guidance.
		}
	}
	p.gateway = sentFeature && peerFeature && peerWire >= minGatewayWireVersion && peerWire <= gatewayWireVersion
	p.gatewayWire = peerWire
	c.SetDeadline(time.Time{})
	if p.gateway {
		if err := exchangeGatewayOutbound(p); err != nil {
			c.Close()
			return nil, err
		}
	}
	return p, nil
}

func parseVersionStartHeight(payload []byte) int64 {
	// version(4) + services(8) + timestamp(8) + addr_recv(26) + addr_from(26) + nonce(8)
	const base = 80
	if len(payload) <= base {
		return -1
	}
	uaLen, used, err := decodeVarInt(payload[base:])
	if err != nil {
		return -1
	}
	pos := base + used + int(uaLen)
	if pos+4 > len(payload) {
		return -1
	}
	return int64(int32(binary.LittleEndian.Uint32(payload[pos : pos+4])))
}

func makeVersionPayloadAtHeight(addr string, services uint64, height int64) []byte {
	p := makeVersionPayload(addr, services)
	if height < 0 {
		height = 0
	}
	if height > 2147483647 {
		height = 2147483647
	}
	binary.LittleEndian.PutUint32(p[len(p)-5:len(p)-1], uint32(height))
	return p
}
func makeVersionPayload(addr string, services uint64) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, protocolVersion)
	_ = binary.Write(&b, binary.LittleEndian, services)
	_ = binary.Write(&b, binary.LittleEndian, time.Now().Unix())
	writeNetAddr(&b, addr)
	writeNetAddr(&b, "0.0.0.0:0")
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	b.Write(nonce[:])
	ua := []byte("/GatewayClient:" + appVersion + "/")
	b.Write(encodeVarInt(uint64(len(ua))))
	b.Write(ua)
	_ = binary.Write(&b, binary.LittleEndian, int32(0))
	b.WriteByte(0)
	return b.Bytes()
}

func writeNetAddr(b *bytes.Buffer, addr string) {
	_ = binary.Write(b, binary.LittleEndian, uint64(0))
	host, portS, _ := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	if ip == nil {
		ip = net.IPv6zero
	}
	ip16 := ip.To16()
	if ip16 == nil {
		ip16 = net.IPv6zero
	}
	b.Write(ip16)
	port, _ := strconv.Atoi(portS)
	_ = binary.Write(b, binary.BigEndian, uint16(port))
}

func waitForCommand(p *peerConn, command string, timeout time.Duration) (message, error) {
	p.conn.SetReadDeadline(time.Now().Add(timeout))
	defer p.conn.SetReadDeadline(time.Time{})
	for {
		m, err := readMessage(p.conn)
		if err != nil {
			return message{}, err
		}
		if m.command == command {
			return m, nil
		}
		if m.command == "ping" {
			_ = writeMessage(p.conn, "pong", m.payload)
		}
	}
}

func writeMessage(w io.Writer, command string, payload []byte) error {
	// Managed serving serializes complete frames, without holding the socket
	// writer while a provider reads disk or answers an RPC request.
	if framed, ok := w.(interface{ WriteBitcoinMessage(string, []byte) error }); ok {
		return framed.WriteBitcoinMessage(command, payload)
	}
	if len(command) > 12 {
		return fmt.Errorf("command too long")
	}
	var h [24]byte
	copy(h[0:4], mainnetMagic[:])
	copy(h[4:16], []byte(command))
	binary.LittleEndian.PutUint32(h[16:20], uint32(len(payload)))
	sum := hash256(payload)
	copy(h[20:24], sum[0:4])
	if n, err := w.Write(h[:]); err != nil {
		return err
	} else if n != len(h) {
		return io.ErrShortWrite
	}
	if len(payload) > 0 {
		n, err := w.Write(payload)
		if err == nil && n != len(payload) {
			return io.ErrShortWrite
		}
		return err
	}
	return nil
}

func readMessage(r io.Reader) (message, error)          { return readMessageBounded(r, maxMessageSize) }
func readHandshakeMessage(r io.Reader) (message, error) { return readMessageBounded(r, 16*1024) }
func readMessageBounded(r io.Reader, limit uint32) (message, error) {
	var h [24]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return message{}, err
	}
	if !bytes.Equal(h[0:4], mainnetMagic[:]) {
		return message{}, fmt.Errorf("unexpected network magic")
	}
	cmd := strings.TrimRight(string(h[4:16]), "\x00")
	n := binary.LittleEndian.Uint32(h[16:20])
	commandLimit := uint32(maxMessageSize)
	switch cmd {
	case "version":
		commandLimit = 1024
	case "verack", "sendaddrv2", "getaddr", "sendheaders":
		commandLimit = 0
	case "ping", "pong":
		commandLimit = 8
	case "inv", "getdata", "notfound":
		commandLimit = 9 + 50000*36
	}
	if n > commandLimit {
		return message{}, fmt.Errorf("%s message too large: %d", cmd, n)
	}
	if (cmd == "addr" && n > 30003) || (cmd == "addrv2" && n > 600000) || (cmd == "getaddr" && n != 0) || ((cmd == "getheaders" || cmd == "getblocks") && n > 3273) || (cmd == "headers" && n > 162003) || (cmd == "feature" && n > 600) || (cmd == "satmsg" && n > maxSatlineWireBytes) || (cmd == "gwmsg" && n > maxGatewayMsgPayload) || (cmd == "bodmsg" && n > maxBODMsgPayload) || n > maxMessageSize || n > limit {
		return message{}, fmt.Errorf("message too large: %d", n)
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return message{}, err
	}
	sum := hash256(p)
	if !bytes.Equal(h[20:24], sum[0:4]) {
		return message{}, fmt.Errorf("bad message checksum")
	}
	return message{command: cmd, payload: p}, nil
}

func hash256(b []byte) [32]byte {
	a := sha256.Sum256(b)
	return sha256.Sum256(a[:])
}

func reverseHex(b []byte) string {
	r := make([]byte, len(b))
	for i := range b {
		r[len(b)-1-i] = b[i]
	}
	return hex.EncodeToString(r)
}

func encodeVarInt(n uint64) []byte {
	var b bytes.Buffer
	switch {
	case n < 0xfd:
		b.WriteByte(byte(n))
	case n <= 0xffff:
		b.WriteByte(0xfd)
		_ = binary.Write(&b, binary.LittleEndian, uint16(n))
	case n <= 0xffffffff:
		b.WriteByte(0xfe)
		_ = binary.Write(&b, binary.LittleEndian, uint32(n))
	default:
		b.WriteByte(0xff)
		_ = binary.Write(&b, binary.LittleEndian, n)
	}
	return b.Bytes()
}

func decodeVarInt(p []byte) (uint64, int, error) {
	if len(p) < 1 {
		return 0, 0, io.ErrUnexpectedEOF
	}
	switch p[0] {
	case 0xfd:
		if len(p) < 3 {
			return 0, 0, io.ErrUnexpectedEOF
		}
		return uint64(binary.LittleEndian.Uint16(p[1:3])), 3, nil
	case 0xfe:
		if len(p) < 5 {
			return 0, 0, io.ErrUnexpectedEOF
		}
		return uint64(binary.LittleEndian.Uint32(p[1:5])), 5, nil
	case 0xff:
		if len(p) < 9 {
			return 0, 0, io.ErrUnexpectedEOF
		}
		return binary.LittleEndian.Uint64(p[1:9]), 9, nil
	default:
		return uint64(p[0]), 1, nil
	}
}

func decodeCompactSizeMinimal(p []byte) (uint64, int, error) {
	n, used, err := decodeVarInt(p)
	if err != nil {
		return 0, 0, err
	}
	if (used == 3 && n < 0xfd) || (used == 5 && n <= 0xffff) || (used == 9 && n <= 0xffffffff) {
		return 0, 0, fmt.Errorf("non-minimal CompactSize")
	}
	return n, used, nil
}

// Deprecated pre-0.3.9 aliases retained for source/tests during the pre-1.0 transition.
const bodFeatureID = gatewayFeatureID

func bodFeatureData() []byte                     { return gatewayFeatureData() }
func featureWireVersion(data []byte) (int, bool) { return gatewayFeatureWireVersion(data) }

func makeFeaturePayload(id string, data []byte) []byte {
	var b bytes.Buffer
	b.Write(encodeVarInt(uint64(len(id))))
	b.WriteString(id)
	b.Write(encodeVarInt(uint64(len(data))))
	b.Write(data)
	return b.Bytes()
}

func parseFeaturePayload(p []byte) (string, []byte, error) {
	n, used, err := decodeCompactSizeMinimal(p)
	if err != nil {
		return "", nil, err
	}
	if n < 4 || n > 80 || uint64(len(p)-used) < n {
		return "", nil, fmt.Errorf("invalid featureid length")
	}
	id := string(p[used : used+int(n)])
	pos := used + int(n)
	dlen, duse, err := decodeCompactSizeMinimal(p[pos:])
	if err != nil {
		return "", nil, err
	}
	pos += duse
	if dlen > 512 || uint64(len(p)-pos) != dlen {
		return "", nil, fmt.Errorf("invalid featuredata length")
	}
	return id, append([]byte(nil), p[pos:]...), nil
}
