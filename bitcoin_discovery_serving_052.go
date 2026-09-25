package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This is a serving capability, not a claim that Gateway independently executes
// Bitcoin consensus. The optional local Core is the archival backing provider.
type bitcoinServingReadiness struct {
	Checked        time.Time `json:"checked,omitempty"`
	Archival       bool      `json:"archival_ready"`
	Height         int64     `json:"height"`
	Tip            string    `json:"tip_hash,omitempty"`
	Reason         string    `json:"reason"`
	Provider       string    `json:"provider"`
	LocalAddresses []string  `json:"core_reported_public_addresses,omitempty"`
	Key            string    `json:"-"`
}

func servingSettingsKey(s appSettings) string {
	b, _ := json.Marshal(struct {
		Disabled, Serve                   bool
		Dir, Blocks, Mode, User, Password string
		Port                              int
	}{s.CoreDisabled, s.ServeData, s.BitcoinDataDir, s.BitcoinBlocksDir, s.RPCAuthMode, s.RPCUser, s.RPCPassword, s.RPCPort})
	h := hash256(b)
	return hex.EncodeToString(h[:])
}
func (a *app) refreshBitcoinServingAsync() {
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	key := servingSettingsKey(settings)
	a.servingMu.Lock()
	if a.servingRefreshing || (a.servingReadiness.Key == key && time.Since(a.servingChecked) < 5*time.Second) {
		a.servingMu.Unlock()
		return
	}
	a.servingRefreshing = true
	a.servingMu.Unlock()
	go func() {
		a.refreshBitcoinServing(settings)
		a.servingMu.Lock()
		a.servingRefreshing = false
		a.servingMu.Unlock()
	}()
}
func (a *app) refreshBitcoinServing(settings appSettings) {
	r := bitcoinServingReadiness{Height: -1, Key: servingSettingsKey(settings), Provider: "none", Reason: "Inbound Bitcoin serving is disabled"}
	defer func() {
		r.Checked = time.Now().UTC()
		a.servingMu.Lock()
		a.servingReadiness = r
		a.servingChecked = r.Checked
		a.servingMu.Unlock()
	}()
	if !settings.ServeData {
		return
	}
	r.Reason = "Sparse/local storage remains available, but complete archival serving is not established"
	if settings.CoreDisabled {
		return
	}
	c, e := newCoreRPC(settings)
	if e != nil {
		r.Reason = "Core archival provider unavailable: " + e.Error()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var chain struct {
		Chain   string `json:"chain"`
		Blocks  int64  `json:"blocks"`
		Headers int64  `json:"headers"`
		Best    string `json:"bestblockhash"`
		Pruned  bool   `json:"pruned"`
		IBD     bool   `json:"initialblockdownload"`
	}
	if e = c.callContext(ctx, "getblockchaininfo", []any{}, &chain); e != nil {
		r.Reason = "Core archival provider unavailable: " + e.Error()
		return
	}
	r.Height = chain.Blocks
	r.Tip = chain.Best
	r.Provider = "configured_local_core_rpc"
	if chain.Chain != "main" {
		r.Reason = "Core is not on Bitcoin mainnet; no mainnet archival service is advertised"
		return
	}
	var network struct {
		Services  string `json:"localservices"`
		Addresses []struct {
			Address string `json:"address"`
			Port    int    `json:"port"`
		} `json:"localaddresses"`
	}
	if e = c.callContext(ctx, "getnetworkinfo", []any{}, &network); e != nil {
		r.Reason = "Core network/service readiness could not be checked: " + e.Error()
		return
	}
	for _, addr := range network.Addresses {
		if publicBitcoinIP(net.ParseIP(addr.Address)) && !containsString(r.LocalAddresses, addr.Address) {
			r.LocalAddresses = append(r.LocalAddresses, addr.Address)
		}
	}
	if chain.Pruned {
		r.Reason = "Core is pruned; Gateway does not advertise complete archival service"
		return
	}
	if chain.IBD || chain.Blocks < chain.Headers || !validHash(chain.Best) {
		r.Reason = "Core is still catching up; archival advertisement waits for a ready mainnet provider"
		return
	}
	flags, e := strconv.ParseUint(strings.TrimPrefix(network.Services, "0x"), 16, 64)
	if e != nil || flags&(nodeNetworkService|nodeWitnessService) != (nodeNetworkService|nodeWitnessService) {
		r.Reason = "Core did not report the required archival and witness service capabilities"
		return
	}
	r.Archival = true
	r.Reason = "The configured, unpruned Core supplies the complete chain and witness blocks. Gateway serves that data on its own listener."
}
func (a *app) currentBitcoinServing() bitcoinServingReadiness {
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	a.servingMu.Lock()
	r := a.servingReadiness
	r.LocalAddresses = append([]string(nil), r.LocalAddresses...)
	a.servingMu.Unlock()
	if r.Key != servingSettingsKey(settings) || !settings.ServeData || time.Since(r.Checked) > 12*time.Second {
		r.Archival = false
		if !settings.ServeData {
			r.Reason = "Inbound Bitcoin serving is disabled"
		} else if settings.CoreDisabled {
			r.Reason = "Core is disabled; no full-chain provider has been established"
		} else {
			r.Reason = "Archival provider readiness is being checked"
		}
	}
	return r
}
func (a *app) invalidateBitcoinServing(reason string) {
	a.servingMu.Lock()
	a.servingReadiness.Archival = false
	a.servingReadiness.Reason = reason
	a.servingChecked = time.Time{}
	a.servingMu.Unlock()
}
func (a *app) localBitcoinAdvertisement() (uint64, int64) {
	a.settingsMu.RLock()
	serve := a.settings.ServeData
	a.settingsMu.RUnlock()
	flags := gatewayExperimentalServiceBit
	height := a.getStatus().HeaderHeight
	if serve {
		flags |= nodeWitnessService
	}
	r := a.currentBitcoinServing()
	if serve && r.Archival {
		flags |= nodeNetworkService
		height = r.Height
	}
	return flags, height
}
func (a *app) gatewayListenerAddress() string {
	if a.source == nil {
		return ""
	}
	a.source.mu.Lock()
	defer a.source.mu.Unlock()
	if a.source.tcp == nil {
		return ""
	}
	return a.source.tcp.Addr().String()
}
func (a *app) advertisedGatewayEndpoint() (string, string) {
	listen := a.gatewayListenerAddress()
	if listen == "" {
		return "", "listener_not_running"
	}
	_, port, e := net.SplitHostPort(listen)
	if e != nil {
		return "", "listener_address_invalid"
	}
	a.settingsMu.RLock()
	explicit := strings.TrimSpace(a.settings.AdvertiseAddr)
	a.settingsMu.RUnlock()
	if explicit != "" {
		if !validPeerEndpoint(explicit) {
			return "", "invalid_explicit_endpoint"
		}
		// Explicit addresses are preserved exactly, including custom external ports.
		return explicit, "explicit_advertise_address"
	}
	r := a.currentBitcoinServing()
	coreIPs := append([]string(nil), r.LocalAddresses...)
	sort.Strings(coreIPs)
	for _, host := range coreIPs {
		if publicBitcoinIP(net.ParseIP(host)) {
			return net.JoinHostPort(host, port), "core_reported_public_ip_gateway_port"
		}
	}
	if a.network != nil {
		n := a.network
		n.mu.Lock()
		host := n.selectPublicIPLocked(n.now())
		n.mu.Unlock()
		if host != "" {
			return net.JoinHostPort(host, port), "two_independent_bitcoin_peer_observations"
		}
	}
	host, _, _ := net.SplitHostPort(listen)
	if publicBitcoinIP(net.ParseIP(host)) {
		return listen, "public_listener_address"
	}
	return "", "public_endpoint_unknown"
}
func (a *app) bitcoinAdvertisementStatus() map[string]any {
	endpoint, source := a.advertisedGatewayEndpoint()
	flags, _ := a.localBitcoinAdvertisement()
	r := a.currentBitcoinServing()
	listen := a.gatewayListenerAddress()
	a.settingsMu.RLock()
	allowed := a.settings.ServeData || a.settings.SatlineServePublished
	a.settingsMu.RUnlock()
	state := "disabled"
	note := "Enable inbound serving only to offer data. Outbound discovery does not require it."
	if allowed {
		state = "listener_unavailable"
		note = "Gateway's listener has not opened. Check its port; the Core listener is a separate endpoint."
		if listen != "" {
			state = "public_endpoint_unknown"
			note = "Listening, but no public endpoint is known. A Core-reported address or agreement from two Bitcoin peers can supply the public IP. An external port mapping may still be needed."
		}
		if endpoint != "" {
			state = "sparse_not_stock_gossip_eligible"
			note = "The endpoint can speak Gateway, but its sparse Bitcoin service flags do not qualify for stock Core address retention."
		}
		if endpoint != "" && r.Archival {
			state = "ready_to_announce"
			note = "Archival service is ready. Reachability of the Gateway port is not yet established."
		}
	}
	var last time.Time
	var lastKey string
	observations := []map[string]any{}
	if a.network != nil {
		a.network.mu.Lock()
		last = a.network.lastAnnounce
		lastKey = a.network.lastAnnounceKey
		observations = a.network.publicIPObservationsLocked(a.network.now())
		a.network.mu.Unlock()
	}
	if state == "ready_to_announce" && !last.IsZero() && lastKey == fmt.Sprintf("%s/%d", endpoint, flags) {
		state = "announced_reachability_unknown"
		note = "Endpoint announced through Bitcoin. An announcement is not a reachability test; confirm inbound Gateway connections."
	}
	inbound := 0
	if a.source != nil {
		a.source.mu.Lock()
		for _, p := range a.source.live {
			host, _, _ := net.SplitHostPort(p.Address)
			if p.Gateway && publicBitcoinIP(net.ParseIP(host)) {
				inbound++
			}
		}
		a.source.mu.Unlock()
	}
	if inbound > 0 && r.Archival {
		state = "public_inbound_observed"
		note = "A public inbound Gateway session has been observed. This is not a universal reachability guarantee."
	}
	return map[string]any{"state": state, "listener": listen, "public_ip_observations": observations, "conflicting_public_ip_observations": len(observations) > 1, "observation_ttl_seconds": 1800, "announcement_min_change_seconds": 300, "endpoint": endpoint, "endpoint_source": source, "services": flags, "services_hex": fmt.Sprintf("0x%016x", flags), "gossip_eligible": flags&nodeNetworkService != 0, "last_announcement": last, "public_inbound_gateway_sessions": inbound, "provider": r, "note": note}
}

// Each cycle resolves one immutable endpoint/flags snapshot for every recipient.
func (n *bitcoinNetwork) announcementSnapshot054() (advertisedPeer, bool) {
	endpoint, _ := n.app.advertisedGatewayEndpoint()
	flags, _ := n.app.localBitcoinAdvertisement()
	if endpoint == "" {
		return advertisedPeer{}, false
	}
	host, port, e := net.SplitHostPort(endpoint)
	if e != nil {
		return advertisedPeer{}, false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		ctx, cancel := context.WithTimeout(n.ctx, 2*time.Second)
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		cancel()
		if err != nil {
			return advertisedPeer{}, false
		}
		sort.Slice(ips, func(i, j int) bool { return ips[i].IP.String() < ips[j].IP.String() })
		for _, candidate := range ips {
			if publicBitcoinIP(candidate.IP) {
				ip = candidate.IP
				break
			}
		}
	}
	if ip == nil || (!n.allowPrivate && !publicBitcoinIP(ip)) {
		return advertisedPeer{}, false
	}
	return advertisedPeer{Addr: net.JoinHostPort(ip.String(), port), Services: flags, Timestamp: uint32(n.now().Unix())}, true
}

type announcementRecord054 struct {
	At  time.Time
	Key string
}

func (n *bitcoinNetwork) sendAnnouncement054(s *bitcoinSession, row advertisedPeer) bool {
	if row.Services&nodeNetworkService == 0 && !s.peer.gateway {
		return false
	}
	key := fmt.Sprintf("%s/%d", row.Addr, row.Services)
	n.mu.Lock()
	now := n.now()
	if n.announcementHistory == nil {
		n.announcementHistory = map[string]announcementRecord054{}
	}
	for addr, record := range n.announcementHistory {
		if now.Sub(record.At) >= 30*time.Minute {
			delete(n.announcementHistory, addr)
		}
	}
	prior := n.announcementHistory[s.peer.addr]
	wait := 30 * time.Minute
	if prior.Key != key {
		wait = 5 * time.Minute
	}
	if n.sessions[s.peer.addr] != s || s.scout || (!prior.At.IsZero() && now.Sub(prior.At) < wait) {
		n.mu.Unlock()
		return false
	}
	// Reserve before I/O so concurrent status/maintenance calls cannot duplicate.
	s.lastAnnouncement, s.lastAnnouncementKey = now, key
	n.announcementHistory[s.peer.addr] = announcementRecord054{now, key}
	n.mu.Unlock()
	command, payload := addressMessage([]advertisedPeer{row}, s.peer.wantsAddrV2)
	if e := s.send(command, payload); e != nil {
		return false
	}
	n.mu.Lock()
	n.lastAnnounce = now
	n.lastAnnounceKey = key
	n.mu.Unlock()
	return true
}
func (n *bitcoinNetwork) announceToSession(s *bitcoinSession) {
	// Short-lived scouts do not broadcast our address. Stable Bitcoin and
	// Gateway members receive at most one snapshot per allowed interval.
	if s.isScout() {
		return
	}
	row, ok := n.announcementSnapshot054()
	if !ok {
		return
	}
	if n.sendAnnouncement054(s, row) {
		n.mu.Lock()
		n.eventLocked("announced", row.Addr, "Endpoint sent to retained Bitcoin/Gateway peer "+s.peer.addr+"; inbound reachability remains unverified")
		n.mu.Unlock()
	}
}
func (n *bitcoinNetwork) announceOnManagedPeers() {
	n.mu.Lock()
	now := n.now()
	if !n.lastAnnounceAttempt.IsZero() && now.Sub(n.lastAnnounceAttempt) < time.Minute {
		n.mu.Unlock()
		return
	}
	n.lastAnnounceAttempt = now
	n.mu.Unlock()
	row, ok := n.announcementSnapshot054()
	if !ok {
		return
	}
	sent := 0
	for _, s := range n.listSessions() {
		if n.sendAnnouncement054(s, row) {
			sent++
		}
	}
	if sent > 0 {
		n.mu.Lock()
		n.eventLocked("announced", row.Addr, fmt.Sprintf("One stable endpoint snapshot sent to %d retained peers; reachability is still unverified", sent))
		n.mu.Unlock()
	}
}
