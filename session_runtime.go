package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

type sessionWriteConn struct {
	net.Conn
	s   *bitcoinSession
	err error
}

func (c *sessionWriteConn) WriteBitcoinMessage(command string, payload []byte) error {
	c.s.writeMu.Lock()
	err := c.s.sendLocked(command, payload, time.Now().Add(15*time.Second))
	c.s.writeMu.Unlock()
	if err != nil && c.err == nil {
		c.err = err
	}
	return err
}

// Service helpers may set deadlines; only the actual frame writer owns them.
func (c *sessionWriteConn) SetWriteDeadline(time.Time) error { return nil }

type sessionPendingReply struct {
	match func(message) bool
	reply chan message
}

func (s *bitcoinSession) deliverReply(m message) {
	s.replyMu.Lock()
	pending := s.pendingReply
	s.replyMu.Unlock()
	if pending == nil || !pending.match(m) {
		return // unsolicited or late frames never accumulate in an idle mailbox
	}
	s.replyMu.Lock()
	defer s.replyMu.Unlock()
	if s.pendingReply != pending {
		return
	}
	pending.reply <- m // capacity one; this pending request can deliver only once
	s.pendingReply = nil
}

func writeSatlineUnavailable(c net.Conn, id, status string) {
	raw, _ := json.Marshal(satlineEnvelope{Wire: 1, Network: "mainnet", Kind: "response", ID: id, Status: status})
	_ = writeMessage(c, "satmsg", raw)
}

func writeBitcoinUnavailable(c net.Conn, m message) {
	switch m.command {
	case "getheaders":
		_ = writeMessage(c, "headers", []byte{0})
	case "getblocks":
		_ = writeMessage(c, "inv", []byte{0})
	case "getdata":
		count, used, err := decodeCompactSizeMinimal(m.payload)
		if err == nil && count <= 128 && uint64(len(m.payload)-used) == count*36 {
			_ = writeMessage(c, "notfound", m.payload)
		}
	}
}

// The serialized request slot bounds concurrency. Correlation still matters:
// a timed-out response may arrive after the next request has acquired the slot.
func sessionReplyMatcher(command string, payload []byte) (func(message) bool, error) {
	switch command {
	case "bodmsg":
		var req bodEnvelope
		if json.Unmarshal(payload, &req) != nil || req.Kind != "request" || req.ID == "" {
			return nil, fmt.Errorf("invalid BOD request envelope")
		}
		return func(m message) bool {
			var got bodEnvelope
			return m.command == command && json.Unmarshal(m.payload, &got) == nil && got.Kind == "response" && got.ID == req.ID && got.Type == req.Type
		}, nil
	case "satmsg":
		var req satlineEnvelope
		if strictSatlineJSON(payload, &req) != nil || req.Kind != "request" || req.ID == "" {
			return nil, fmt.Errorf("invalid Satline request envelope")
		}
		return func(m message) bool {
			var got satlineEnvelope
			return m.command == command && strictSatlineJSON(m.payload, &got) == nil && got.Kind == "response" && got.ID == req.ID
		}, nil
	case "getdata":
		count, used, err := decodeCompactSizeMinimal(payload)
		if err != nil || count != 1 || len(payload) != used+36 {
			return nil, fmt.Errorf("managed getdata requires one inventory item")
		}
		item := append([]byte(nil), payload[used:]...)
		var hash [32]byte
		copy(hash[:], item[4:])
		return func(m message) bool {
			if m.command == "block" {
				return len(m.payload) >= 80 && hash256(m.payload[:80]) == hash
			}
			if m.command == "notfound" {
				count, used, err := decodeCompactSizeMinimal(m.payload)
				if err != nil || count > 128 || uint64(len(m.payload)-used) != count*36 {
					return false
				}
				for ; used < len(m.payload); used += 36 {
					if bytes.Equal(m.payload[used:used+36], item) {
						return true
					}
				}
			}
			return false
		}, nil
	case "getheaders":
		return func(m message) bool { return m.command == "headers" }, nil
	default:
		return nil, fmt.Errorf("unsupported managed request %s", command)
	}
}

func (s *bitcoinSession) transportEligible(archive bool) bool {
	s.n.mu.Lock()
	defer s.n.mu.Unlock()
	return s.transportEligibleLocked(archive)
}

func (s *bitcoinSession) transportEligibleLocked(archive bool) bool {
	if s.view.State == "disconnected" {
		return false
	}
	if archive {
		return s.peer.services&nodeNetworkService != 0
	}
	return !s.headerUnavailable && s.peer.services&(nodeNetworkService|nodeNetworkLimitedService) != 0
}

// Fixed windows renew legitimate long-lived sessions while bounding work and
// replay memory. The service loop is the sole owner. Busy is a response, not EOF.
type sessionServiceBudget struct {
	started time.Time
	count   int
	seen    map[string]bool
}

func (b *sessionServiceBudget) take(now time.Time, limit int, id string) string {
	if b.started.IsZero() || now.Sub(b.started) >= time.Minute {
		b.started, b.count, b.seen = now, 0, make(map[string]bool)
	}
	if b.seen[id] {
		return "invalid"
	}
	if b.count >= limit {
		return "busy"
	}
	b.count++
	b.seen[id] = true
	return ""
}

// Adoption never promotes a remote source port into the saved address book.
// The accept loop already holds one of its bounded inbound connection slots.
func (n *bitcoinNetwork) adoptInbound(p *peerConn) *bitcoinSession {
	if !releaseFeatureAvailable("gateway-peerhood") {
		p.conn.Close()
		return nil
	}
	now := n.now()
	s := &bitcoinSession{n: n, peer: p, done: make(chan struct{}), slot: make(chan struct{}, 1), serveQueue: make(chan message, 4)}
	s.view = networkPeerView{ID: "inbound:" + p.addr, Address: p.addr, Direction: "inbound", Owner: "Gateway", Role: "gateway_inbound", Source: "incoming_connection", Transport: "Bitcoin P2P v1", State: "connected", Services: p.services, ServiceHex: fmt.Sprintf("0x%016x", p.services), ProtocolVersion: p.version, Gateway: true, Negotiated: append([]gatewayProtocolSupport(nil), p.gatewayProtocols...), Protocols: []string{"Bitcoin P2P", "Gateway On Demand"}, TargetHeight: p.startHeight, Connected: now, Updated: now}
	n.mu.Lock()
	if n.sessions[p.addr] != nil || n.ctx.Err() != nil {
		n.mu.Unlock()
		return nil
	}
	n.sessions[p.addr] = s
	n.eventLocked("gateway_inbound", p.addr, "Inbound Gateway negotiation completed; using this socket in both directions")
	n.mu.Unlock()
	go s.readLoop()
	go s.serviceLoop()
	return s
}
