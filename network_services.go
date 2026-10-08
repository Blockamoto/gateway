package main

import (
	"encoding/json"
)

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
	gatewayAllowed := releaseFeatureAvailable("gateway-peerhood") && a.settings.ServeGatewayData
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
		if !releaseFeatureAvailable("gateway-peerhood") {
			return
		}
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
		if releaseFeatureAvailable("gateway-peerhood") && s.peer.gateway && s.peer.bod {
			var env bodEnvelope
			if json.Unmarshal(m.payload, &env) != nil || env.Kind != "request" || env.ID == "" || len(env.ID) > 128 {
				a.source.handleBODMsg(c, m.payload)
				return
			}
			status := s.bodBudget.take(s.n.now(), 64, env.ID)
			if !gatewayAllowed {
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
		if !releaseFeatureAvailable("gateway-peerhood") || !s.peer.gateway {
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
