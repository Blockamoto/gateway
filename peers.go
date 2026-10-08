package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

type livePeerView struct {
	Gateway         bool      `json:"gateway"`
	BOD             bool      `json:"bod"`
	Services        uint64    `json:"services"`
	ProtocolVersion int32     `json:"protocol_version"`
	Source          string    `json:"discovery_source"`
	Owner           string    `json:"owner"`
	Role            string    `json:"role"`
	ID              string    `json:"id"`
	Address         string    `json:"address"`
	Direction       string    `json:"direction"`
	State           string    `json:"state"`
	Protocols       []string  `json:"protocols"`
	Connected       time.Time `json:"connected"`
	Messages        uint64    `json:"messages"`
	conn            net.Conn
}

func (s *overlayServer) observeConn(c net.Conn, state string, protocol string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.live == nil {
		s.live = map[string]*livePeerView{}
	}
	id := c.RemoteAddr().String()
	p, ok := s.live[id]
	if !ok {
		p = &livePeerView{ID: id, Address: id, Direction: "inbound", Source: "incoming_connection", Owner: "Gateway", Role: "inbound", Connected: time.Now(), conn: c}
		s.live[id] = p
	}
	p.State = state
	p.Messages++
	if protocol != "" {
		found := false
		for _, x := range p.Protocols {
			if x == protocol {
				found = true
			}
		}
		if !found {
			p.Protocols = append(p.Protocols, protocol)
		}
	}
}
func (s *overlayServer) forgetConn(c net.Conn) {
	s.mu.Lock()
	delete(s.live, c.RemoteAddr().String())
	s.mu.Unlock()
}
func (a *app) handlePeers(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var q struct {
			Action  string `json:"action"`
			Address string `json:"address"`
		}
		if !satlineBody(w, r, &q) {
			return
		}
		switch q.Action {
		case "refresh", "retry":
			if a.network != nil {
				a.network.retry()
			}
			a.refreshPeersAsync()
		case "add":
			if !releaseHTTPFeature(w, "gateway-peerhood") {
				return
			}
			if e := a.setGatewayBootstrapPeers([]string{q.Address}, true); e != nil {
				jsonError(w, 400, e)
				return
			}
			a.peerMu.Lock()
			a.peerScanAt = time.Time{}
			a.peerMu.Unlock()
			a.refreshPeersAsync()
		case "disconnect":
			if a.network != nil && a.network.disconnect(q.Address) {
				break
			}
			if a.source == nil {
				jsonError(w, 404, fmt.Errorf("connection not found"))
				return
			}
			a.source.mu.Lock()
			p, ok := a.source.live[q.Address]
			if ok && p.conn != nil {
				p.conn.Close()
			}
			a.source.mu.Unlock()
			if !ok {
				jsonError(w, 404, fmt.Errorf("this connection is not owned by Gateway; Core peers are read-only here"))
				return
			}
		default:
			jsonError(w, 400, fmt.Errorf("unsupported peer operation"))
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	if r.Method != "GET" {
		http.Error(w, "GET required", 405)
		return
	}
	live := []livePeerView{}
	if a.source != nil {
		a.source.mu.Lock()
		for _, p := range a.source.live {
			v := *p
			v.Protocols = append([]string(nil), p.Protocols...)
			live = append(live, v)
		}
		a.source.mu.Unlock()
	}
	discovered := a.cachedOverlayPeers()
	a.settingsMu.RLock()
	s := a.settings
	a.settingsMu.RUnlock()
	var corePeers []map[string]any
	var coreErr string
	if r.URL.Query().Get("core") == "1" {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		c, e := newCoreRPC(s)
		if e == nil {
			e = c.callContext(ctx, "getpeerinfo", nil, &corePeers)
		}
		if e != nil {
			coreErr = e.Error()
		}
	}
	network := map[string]any{"connections": []networkPeerView{}, "recent": []networkPeerView{}, "connected": 0}
	if a.network != nil {
		network = a.network.snapshot()
	}
	rows := []any{}
	managed := make(map[string]bool)
	if outbound, ok := network["connections"].([]networkPeerView); ok {
		for _, p := range outbound {
			rows = append(rows, p)
			managed[p.Address] = true
		}
	}
	for _, p := range live {
		if !managed[p.Address] {
			rows = append(rows, p)
		}
	}
	var listener any
	if a.source != nil {
		listener = a.source.bitcoinServingStatus()
	}
	writeJSON(w, map[string]any{"connections": rows, "network": network, "discovered": discovered, "core_peers": corePeers, "core_error": coreErr, "header_connection": a.getStatus().HeaderSource, "listener": listener, "protocols": []gatewayProtocolSupport{}, "gateway_locked": true, "gateway_lock_reason": releaseLockReason("gateway-peerhood"), "note": "Bitcoin peers supply headers and requested blocks. Core peers are shown separately. Gateway peerhood is locked in this testing build."})
}
