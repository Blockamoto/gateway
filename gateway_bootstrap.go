package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

const maxGatewayBootstrapPeers = 16

// A configured name is a dial target, never a resolved-IP identity or proof of
// Gateway support. The normal bounded dial and handshake still establish that.
func normalizeGatewayBootstrapPeer(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" || len(input) > 300 || strings.ContainsAny(input, "/\\@?# \t\r\n\x00") {
		return "", fmt.Errorf("use a Gateway host or IP, optionally followed by :48333; URLs are not peer endpoints")
	}
	host, port, err := net.SplitHostPort(input)
	if err != nil {
		host, port = input, strconv.Itoa(overlayTCPPort)
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
		}
		if strings.Contains(host, ":") && net.ParseIP(host) == nil {
			return "", fmt.Errorf("invalid Gateway host or port")
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else {
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		if len(host) == 0 || len(host) > 253 {
			return "", fmt.Errorf("invalid Gateway hostname length")
		}
		allNumeric := true
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", fmt.Errorf("invalid Gateway hostname label")
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return "", fmt.Errorf("Gateway hostnames must use ASCII DNS labels; use punycode for international names")
				}
				if c < '0' || c > '9' {
					allNumeric = false
				}
			}
		}
		if allNumeric || strings.HasSuffix(host, ".onion") || strings.HasSuffix(host, ".i2p") {
			return "", fmt.Errorf("use a valid IP or ordinary DNS hostname; onion/I2P transports are not configured")
		}
	}
	address := net.JoinHostPort(host, port)
	if !validPeerEndpoint(address) {
		return "", fmt.Errorf("invalid Gateway endpoint")
	}
	return address, nil
}

func normalizeGatewayBootstrapPeers(peers []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	if len(peers) > maxGatewayBootstrapPeers*2 {
		return nil, fmt.Errorf("configure at most %d Gateway bootstrap peers", maxGatewayBootstrapPeers)
	}
	for _, peer := range peers {
		address, err := normalizeGatewayBootstrapPeer(peer)
		if err != nil {
			return nil, err
		}
		if !seen[address] {
			if len(out) >= maxGatewayBootstrapPeers {
				return nil, fmt.Errorf("configure at most %d Gateway bootstrap peers", maxGatewayBootstrapPeers)
			}
			seen[address] = true
			out = append(out, address)
		}
	}
	return out, nil
}

// Old settings/CLI retain their one-peer meaning. Reading this union neither
// rewrites settings nor silently converts a configured endpoint into evidence.
func configuredGatewayBootstrapPeers(s appSettings) []string {
	out := []string{}
	for _, input := range append([]string{s.ManualPeer}, s.GatewayBootstrapPeers...) {
		if address, err := normalizeGatewayBootstrapPeer(input); err == nil && !containsString(out, address) {
			out = append(out, address)
		}
		if len(out) >= maxGatewayBootstrapPeers {
			break
		}
	}
	return out
}

func (a *app) setGatewayBootstrapPeers(peers []string, appendPeer bool) error {
	if err := requireReleaseFeature("gateway-peerhood"); err != nil {
		return err
	}
	a.settingsMu.Lock()
	next := a.settings
	if appendPeer {
		peers = append(configuredGatewayBootstrapPeers(next), peers...)
	}
	canonical, err := normalizeGatewayBootstrapPeers(peers)
	if err == nil {
		next.GatewayBootstrapPeers = canonical
		next.ManualPeer = "" // The editable list now owns the legacy entry too.
		err = a.saveSettings(next)
	}
	if err == nil {
		a.settings = next
	}
	a.settingsMu.Unlock()
	if err != nil {
		return err
	}
	if a.network != nil {
		a.network.mu.Lock()
		a.network.eventLocked("bootstrap_configuration", "", fmt.Sprintf("%d configured Gateway entrances; verification and retry budgets retained", len(canonical)))
		a.network.mu.Unlock()
		a.network.maintain()
	}
	return nil
}
