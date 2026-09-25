package main

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	gatewayWireVersion    = 1
	minGatewayWireVersion = 1

	// Gateway is the generic protocol-discovery substrate. BIP 434 confirms
	// Gateway itself; individual services are listed through gwmsg afterwards.
	gatewayFeatureID  = "8f01b6e7d640f7543b44c1d8a5f5d626559d512ff6d37d60d069761460250a7c"
	gatewayFeatureRef = "Gateway protocol | Gateway Client project | experimental BIP434 feature identity | pre-1.0"

	// Stable machine identity for the Bitcoin on Demand protocol family within Gateway.
	bodProtocolID = "53e14b718ca5ba11bff160a78e6528b890f7be5d20060e411ddaec3c1f23bdfe"

	maxGatewayMsgPayload = 16 * 1024
)

type gatewayProtocolSupport struct {
	ID       string `json:"id"`
	Versions []int  `json:"versions"`
	Name     string `json:"name,omitempty"`
}

type gatewayMessage struct {
	Wire      int                      `json:"wire"`
	Kind      string                   `json:"kind"` // hello|hello_ack
	ID        string                   `json:"id"`
	Protocols []gatewayProtocolSupport `json:"protocols,omitempty"`
	Error     string                   `json:"error,omitempty"`
}

func gatewayFeatureData() []byte {
	b, _ := json.Marshal(map[string]any{"wire": []int{gatewayWireVersion}})
	return b
}

func gatewayFeatureWireVersion(data []byte) (int, bool) {
	var x struct {
		Wire []int `json:"wire"`
	}
	if json.Unmarshal(data, &x) != nil {
		return 0, false
	}
	for _, w := range x.Wire {
		if w >= minGatewayWireVersion && w <= gatewayWireVersion {
			return w, true
		}
	}
	return 0, false
}

func localGatewayProtocols() []gatewayProtocolSupport {
	return []gatewayProtocolSupport{{ID: bodProtocolID, Versions: []int{bodWireVersion}, Name: "Bitcoin on Demand"}}
}

func protocolVersionSupported(list []gatewayProtocolSupport, id string, min, max int) (int, bool) {
	for _, p := range list {
		if p.ID != id {
			continue
		}
		for _, v := range p.Versions {
			if v >= min && v <= max {
				return v, true
			}
		}
	}
	return 0, false
}

func exchangeGatewayOutbound(p *peerConn) error {
	if !p.gateway {
		return nil
	}
	if p.localProtocols == nil {
		p.localProtocols = localGatewayProtocols()
	}
	req := gatewayMessage{Wire: gatewayWireVersion, Kind: "hello", ID: randomRequestID(), Protocols: p.localProtocols}
	raw, _ := json.Marshal(req)
	if len(raw) > maxGatewayMsgPayload {
		return fmt.Errorf("Gateway hello too large")
	}
	if err := writeMessage(p.conn, "gwmsg", raw); err != nil {
		return err
	}
	_ = p.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer p.conn.SetReadDeadline(time.Time{})
	for {
		m, err := readMessage(p.conn)
		if err != nil {
			return err
		}
		switch m.command {
		case "ping":
			_ = writeMessage(p.conn, "pong", m.payload)
		case "gwmsg":
			if len(m.payload) > maxGatewayMsgPayload {
				return fmt.Errorf("Gateway response too large")
			}
			var got gatewayMessage
			if json.Unmarshal(m.payload, &got) != nil || got.Kind != "hello_ack" || got.ID != req.ID {
				continue
			}
			if got.Wire != gatewayWireVersion {
				return fmt.Errorf("Gateway wire %d is incompatible", got.Wire)
			}
			p.gatewayProtocols = append([]gatewayProtocolSupport(nil), got.Protocols...)
			if w, ok := protocolVersionSupported(got.Protocols, bodProtocolID, minBODWireVersion, bodWireVersion); ok {
				p.bod, p.bodWire = true, w
			}
			return nil
		}
	}
}

func handleGatewayMessage(c interface{ Write([]byte) (int, error) }, payload []byte, p *peerConn) {
	if !p.gateway || len(payload) > maxGatewayMsgPayload {
		return
	}
	var got gatewayMessage
	if json.Unmarshal(payload, &got) != nil || got.Wire != gatewayWireVersion || got.Kind != "hello" || got.ID == "" {
		return
	}
	p.gatewayProtocols = append([]gatewayProtocolSupport(nil), got.Protocols...)
	if w, ok := protocolVersionSupported(got.Protocols, bodProtocolID, minBODWireVersion, bodWireVersion); ok {
		p.bod, p.bodWire = true, w
	}
	if p.localProtocols == nil {
		p.localProtocols = localGatewayProtocols()
	}
	out := gatewayMessage{Wire: gatewayWireVersion, Kind: "hello_ack", ID: got.ID, Protocols: p.localProtocols}
	raw, _ := json.Marshal(out)
	if len(raw) <= maxGatewayMsgPayload {
		_ = writeMessage(c, "gwmsg", raw)
	}
}
