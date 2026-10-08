package main

import (
	"encoding/json"
	"testing"
)

func TestV039GatewayFeatureIdentity(t *testing.T) {
	raw := makeFeaturePayload(gatewayFeatureID, gatewayFeatureData())
	id, data, err := parseFeaturePayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if id != gatewayFeatureID {
		t.Fatalf("feature id = %q", id)
	}
	if w, ok := gatewayFeatureWireVersion(data); !ok || w != 1 {
		t.Fatalf("gateway wire negotiation = %d %v", w, ok)
	}
}

func TestV039GatewayListsBODAsService(t *testing.T) {
	protocols := localGatewayProtocols()
	if len(protocols) == 0 {
		t.Fatal("no Gateway protocols")
	}
	if w, ok := protocolVersionSupported(protocols, bodProtocolID, 1, 1); !ok || w != 1 {
		t.Fatalf("BOD protocol not listed: %+v", protocols)
	}
	b, err := json.Marshal(gatewayMessage{Wire: 1, Kind: "hello", ID: "abc", Protocols: protocols})
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > maxGatewayMsgPayload {
		t.Fatalf("Gateway hello too large: %d", len(b))
	}
}

func TestV039CompatibilityKeepsExpensiveData(t *testing.T) {
	c := currentCompatibility()
	if c.AppVersion != appVersion || c.GatewayProtocol != 1 || c.WireProtocol != 1 {
		t.Fatalf("unexpected compatibility: %+v", c)
	}
	if c.HeaderResyncRequired || c.RawBlockRedownload || c.IndexRebuild || c.CacheMetadataRebuild {
		t.Fatalf("0.3.9 should reuse 0.3.8 verified data: %+v", c)
	}
}

func TestVersionStartHeightParsing(t *testing.T) {
	p := makeVersionPayload("127.0.0.1:8333", 0)
	// makeVersionPayload writes a start height of zero.
	if got := parseVersionStartHeight(p); got != 0 {
		t.Fatalf("start height = %d", got)
	}
}
