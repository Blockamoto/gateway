package main

// The public-testing connection pool only acquires Bitcoin headers and blocks.
// It never interprets address service flags as an invitation to another protocol.
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const bitcoinConnectionLimit = 3
const bitcoinAddressFile = "bitcoin-peers-v2.json"

type bitcoinNetworkEvent struct {
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	Endpoint string    `json:"endpoint,omitempty"`
	Detail   string    `json:"detail,omitempty"`
}

func (n *bitcoinNetwork) now() time.Time {
	if n.clock != nil {
		return n.clock().UTC()
	}
	return time.Now().UTC()
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func publicBitcoinIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if v := ip.To4(); v != nil {
		return v[0] != 0 && v[0] != 127 && !(v[0] == 100 && v[1] >= 64 && v[1] <= 127) && !(v[0] == 192 && v[1] == 0 && v[2] == 0) && !(v[0] == 192 && v[1] == 0 && v[2] == 2) && !(v[0] == 198 && (v[1] == 18 || v[1] == 19 || v[1] == 51 && v[2] == 100)) && !(v[0] == 203 && v[1] == 0 && v[2] == 113)
	}
	return !(ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x0d && ip[3] == 0xb8)
}

func ordinaryBitcoinSource(source string) bool {
	return source == "manual_bitcoin" || source == "local_core_bitcoin" || source == "saved_bitcoin" || source == "fixture_bootstrap" || strings.HasPrefix(source, "dns_seed:") || strings.HasPrefix(source, "bitcoin_addr:")
}

func (n *bitcoinNetwork) admitAddress(address, source string, services uint64) bool {
	if !validPeerEndpoint(address) || !ordinaryBitcoinSource(source) {
		return false
	}
	host, _, _ := net.SplitHostPort(address)
	explicit := source == "manual_bitcoin" || source == "local_core_bitcoin"
	if !explicit && !n.allowPrivate && !publicBitcoinIP(net.ParseIP(host)) {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if old := n.addresses[address]; old != nil {
		// Third-party address gossip must not replace a direct observation.
		if old.LastSuccess.IsZero() {
			old.Services = services
		}
		if explicit {
			old.Source = source
		}
		return true
	}
	if len(n.addresses) >= maxBitcoinAddresses {
		return false
	}
	n.addresses[address] = &bitcoinAddress{Address: address, Source: source, Services: services, FirstSeen: n.now()}
	return true
}

func (n *bitcoinNetwork) learnAddresses(rows []advertisedPeer, supplier string) {
	for _, row := range rows {
		if row.Services&(nodeNetworkService|nodeNetworkLimitedService) == 0 {
			continue
		}
		n.admitAddress(row.Addr, "bitcoin_addr:"+supplier, row.Services)
	}
}

func (n *bitcoinNetwork) receiveAddresses(s *bitcoinSession, rows []advertisedPeer) {
	n.mu.Lock()
	if !s.addrAsked || s.addrReceived {
		n.mu.Unlock()
		return
	}
	s.addrReceived = true
	n.mu.Unlock()
	n.learnAddresses(rows, s.peer.addr)
}

func (n *bitcoinNetwork) eventLocked(kind, endpoint, detail string) {
	n.events = append(n.events, bitcoinNetworkEvent{n.now(), kind, endpoint, detail})
	if len(n.events) > 80 {
		n.events = append([]bitcoinNetworkEvent(nil), n.events[len(n.events)-80:]...)
	}
}

func (n *bitcoinNetwork) recordFailureLocked(x *bitcoinAddress, outcome, reason string) {
	x.Failures++
	x.LastError = reason
	delay := time.Duration(x.Failures) * time.Minute
	if delay > 30*time.Minute {
		delay = 30 * time.Minute
	}
	x.RetryAfter = n.now().Add(delay)
}

func (n *bitcoinNetwork) loadBitcoinAddresses() {
	f, err := os.Open(filepath.Join(n.app.dataDir, bitcoinAddressFile))
	if os.IsNotExist(err) {
		// Import ordinary Bitcoin entries only, preserving the legacy file.
		f, err = os.Open(filepath.Join(n.app.dataDir, "bitcoin-peers.json"))
	}
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		n.persistenceError = err.Error()
		return
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 8*1024*1024+1))
	var rows []bitcoinAddress
	if err != nil || len(b) > 8*1024*1024 || json.Unmarshal(b, &rows) != nil {
		n.persistenceError = "Saved Bitcoin addresses are unreadable; the original file is preserved"
		return
	}
	for _, row := range rows {
		if len(n.addresses) >= maxBitcoinAddresses {
			break
		}
		if !validPeerEndpoint(row.Address) || !ordinaryBitcoinSource(row.Source) {
			continue
		}
		host, _, _ := net.SplitHostPort(row.Address)
		if row.Source != "manual_bitcoin" && row.Source != "local_core_bitcoin" && !publicBitcoinIP(net.ParseIP(host)) {
			continue
		}
		copy := row
		n.addresses[row.Address] = &copy
	}
}

func (n *bitcoinNetwork) persistBitcoinAddresses() {
	n.saveMu.Lock()
	defer n.saveMu.Unlock()
	n.mu.Lock()
	if n.persistenceError != "" {
		n.mu.Unlock()
		return
	}
	rows := make([]bitcoinAddress, 0, len(n.addresses))
	for _, row := range n.addresses {
		rows = append(rows, *row)
	}
	n.mu.Unlock()
	sort.Slice(rows, func(i, j int) bool { return rows[i].Address < rows[j].Address })
	b, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(n.app.dataDir, bitcoinAddressFile)
	err = atomicWriteBytes(path, b)
	if err != nil {
		n.mu.Lock()
		n.persistenceError = err.Error()
		n.mu.Unlock()
	}
}

// Keep a slot available for historical block retrieval when the connected
// nodes only retain recent blocks. Called with n.mu held.
func (n *bitcoinNetwork) needsArchivalPeerLocked() bool {
	for _, session := range n.sessions {
		if session.peer.services&nodeNetworkService != 0 {
			return false
		}
	}
	return true
}

func (n *bitcoinNetwork) canKeepBitcoinPeerLocked(services uint64) bool {
	return services&nodeNetworkService != 0 || len(n.sessions) < bitcoinConnectionLimit-1 || !n.needsArchivalPeerLocked()
}

func (n *bitcoinNetwork) maintainBitcoin() {
	n.app.refreshBitcoinServingAsync()
	if n.ctx.Err() != nil {
		return
	}
	n.app.settingsMu.RLock()
	settings := n.app.settings
	n.app.settingsMu.RUnlock()
	if settings.NetworkDisabled {
		for _, s := range n.listSessions() {
			s.close("Outbound Bitcoin networking disabled")
		}
		n.mu.Lock()
		for _, cancel := range n.dialCancels {
			cancel()
		}
		n.mu.Unlock()
		return
	}
	for _, address := range settings.BitcoinPeers {
		n.add(normalizePeer(address), "manual_bitcoin", 0)
	}
	for _, address := range n.app.preferred {
		n.add(normalizePeer(address), "manual_bitcoin", 0)
	}
	if !settings.CoreDisabled {
		if address, ok := coreP2PAddr(settings); ok {
			n.add(address, "local_core_bitcoin", 0)
		}
	}
	// A timed-out getheaders reply cannot be correlated to a later request.
	// Replace that socket once its current block request has finished, so all
	// quarantined header roles cannot permanently stall synchronization.
	for _, session := range n.listSessions() {
		n.mu.Lock()
		replace := session.headerUnavailable && len(session.slot) == 0
		if replace {
			session.retired = true
		}
		n.mu.Unlock()
		if replace {
			session.close("Replacing a connection after an uncorrelated header request")
		}
	}
	n.mu.Lock()
	now := n.now()
	needArchive := n.needsArchivalPeerLocked()
	if !n.noBootstrap && !n.bootstrapBusy && needArchive && (n.bootstrapAt.IsZero() || now.Sub(n.bootstrapAt) >= 5*time.Minute) {
		n.bootstrapAt, n.bootstrapBusy = now, true
		go n.bootstrap()
	}
	if len(n.sessions)+len(n.dialing) >= bitcoinConnectionLimit || now.Before(n.nextDial) {
		n.mu.Unlock()
		return
	}
	rows := make([]*bitcoinAddress, 0, len(n.addresses))
	for _, row := range n.addresses {
		if n.sessions[row.Address] != nil || n.dialing[row.Address].Address != "" || row.RetryAfter.After(now) {
			continue
		}
		if needArchive && len(n.sessions) >= bitcoinConnectionLimit-1 && row.Services&nodeNetworkService == 0 && row.Services&nodeNetworkLimitedService != 0 {
			continue
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		am, bm := a.Source == "manual_bitcoin" || a.Source == "local_core_bitcoin", b.Source == "manual_bitcoin" || b.Source == "local_core_bitcoin"
		if am != bm {
			return am
		}
		if needArchive && (a.Services&nodeNetworkService != 0) != (b.Services&nodeNetworkService != 0) {
			return a.Services&nodeNetworkService != 0
		}
		if !a.LastSuccess.Equal(b.LastSuccess) {
			return a.LastSuccess.After(b.LastSuccess)
		}
		if !a.LastAttempt.Equal(b.LastAttempt) {
			return a.LastAttempt.Before(b.LastAttempt)
		}
		return a.Address < b.Address
	})
	if len(rows) == 0 {
		n.mu.Unlock()
		return
	}
	chosen := rows[0]
	// Prefer a distinct address group where available without rejecting explicit
	// local peers or making the pool unusable on a small/private network.
	for _, row := range rows {
		if chosen.Source == "manual_bitcoin" || chosen.Source == "local_core_bitcoin" {
			break
		}
		used := false
		for addr := range n.sessions {
			if peerNetworkGroup(addr) == peerNetworkGroup(row.Address) {
				used = true
			}
		}
		for addr := range n.dialing {
			if peerNetworkGroup(addr) == peerNetworkGroup(row.Address) {
				used = true
			}
		}
		if !used {
			chosen = row
			break
		}
	}
	chosen.LastAttempt = now
	x := *chosen
	n.dialing[x.Address] = networkPeerView{ID: x.Address, Address: x.Address, Source: x.Source, Owner: "Gateway", Direction: "outbound", Transport: "Bitcoin P2P v1", Role: "bitcoin_transport", State: "connecting", Protocols: []string{"Bitcoin P2P"}, Updated: now}
	n.nextDial = now.Add(time.Second)
	n.mu.Unlock()
	go n.dialBitcoinPeer(x)
}

func (n *bitcoinNetwork) dialBitcoinPeer(x bitcoinAddress) {
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
	p, err := handshakeOutboundState(c, x.Address, nil, flags, height)
	close(done)
	if err != nil {
		c.Close()
		n.failed(x.Address, err)
		return
	}
	if n.ctx.Err() != nil || !n.enabled() || p.services&(nodeNetworkService|nodeNetworkLimitedService) == 0 {
		c.Close()
		n.failed(x.Address, fmt.Errorf("peer is unavailable for Bitcoin headers or blocks"))
		return
	}
	s := &bitcoinSession{n: n, peer: p, done: make(chan struct{}), slot: make(chan struct{}, 1), serveQueue: make(chan message, 4)}
	n.mu.Lock()
	if n.ctx.Err() != nil {
		n.mu.Unlock()
		c.Close()
		return
	}
	if !n.canKeepBitcoinPeerLocked(p.services) {
		n.mu.Unlock()
		c.Close()
		n.failed(x.Address, fmt.Errorf("reserving a Bitcoin connection for historical blocks"))
		return
	}
	v := n.dialing[x.Address]
	delete(n.dialing, x.Address)
	delete(n.dialCancels, x.Address)
	v.State, v.Services, v.ServiceHex, v.ProtocolVersion = "connected", p.services, fmt.Sprintf("0x%016x", p.services), p.version
	v.TargetHeight, v.Connected, v.Updated = p.startHeight, n.now(), n.now()
	s.view, s.addrAsked = v, true
	n.sessions[x.Address] = s
	if row := n.addresses[x.Address]; row != nil {
		row.LastSuccess, row.Services, row.Failures, row.LastError = n.now(), p.services, 0, ""
	}
	n.mu.Unlock()
	go s.readLoop()
	go s.serviceLoop()
	_ = s.send("getaddr", nil)
}

func (n *bitcoinNetwork) bitcoinSnapshot() map[string]any {
	enabled := n.enabled()
	n.mu.Lock()
	defer n.mu.Unlock()
	rows := []networkPeerView{}
	for _, s := range n.sessions {
		v := s.view
		v.Protocols = append([]string(nil), v.Protocols...)
		rows = append(rows, v)
	}
	for _, v := range n.dialing {
		rows = append(rows, v)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Address < rows[j].Address })
	return map[string]any{"connections": rows, "connected": len(n.sessions), "stable_connections": len(n.sessions), "search_connections": 0, "gateway_connected": 0, "gateway_locked": true, "gateway_lock_reason": "Gateway peerhood is unavailable in this testing build.", "address_count": len(n.addresses), "enabled": enabled, "bootstrap_running": n.bootstrapBusy, "bootstrap_error": n.bootstrapError, "persistence_error": n.persistenceError, "recent": append([]networkPeerView(nil), n.recent...), "events": append([]bitcoinNetworkEvent(nil), n.events...), "transport_note": "Bitcoin P2P supplies headers and requested blocks. Connections are not anonymous or encrypted."}
}

func (n *bitcoinNetwork) addressReplyRows() []advertisedPeer { return nil }
