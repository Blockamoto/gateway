package main

import (
	"net"
	"sort"
	"strings"
	"time"
)

const publicObservationTTL054 = 30 * time.Minute
const publicObservationRefresh054 = 20 * time.Minute

// Called before taking n.mu. Endpoint sources with their own authority do not
// need extra outbound observations, and pausing discovery also pauses probes.
func (a *app) needsPublicIPObservations054() bool {
	a.settingsMu.RLock()
	s := a.settings
	a.settingsMu.RUnlock()
	if s.NetworkDisabled || s.DiscoveryPaused || !(s.ServeData || s.SatlineServePublished) || strings.TrimSpace(s.AdvertiseAddr) != "" {
		return false
	}
	listen := a.gatewayListenerAddress()
	host, _, e := net.SplitHostPort(listen)
	if e != nil || publicBitcoinIP(net.ParseIP(host)) {
		return false
	}
	for _, host := range a.currentBitcoinServing().LocalAddresses {
		if publicBitcoinIP(net.ParseIP(host)) {
			return false
		}
	}
	return true
}

func (n *bitcoinNetwork) recordPublicIPObservationLocked(address string, observed net.IP, now time.Time) {
	if !publicBitcoinIP(observed) {
		return
	}
	group := peerNetworkGroup(address)
	n.observedIPs[group] = observed.String()
	n.observedAt[group] = now
}

func (n *bitcoinNetwork) freshPublicIPObservationLocked(group string, now time.Time) bool {
	at, known := n.observedAt[group]
	return known && !at.IsZero() && !at.After(now.Add(time.Minute)) && now.Sub(at) < publicObservationRefresh054 && publicBitcoinIP(net.ParseIP(n.observedIPs[group]))
}

func (n *bitcoinNetwork) publicIPRefreshDueLocked(now time.Time) bool {
	if !n.publicIPRefreshAt.IsZero() && now.Sub(n.publicIPRefreshAt) < time.Minute {
		return false
	}
	votes := map[string]int{}
	for group, host := range n.observedIPs {
		if n.freshPublicIPObservationLocked(group, now) {
			votes[host]++
			if votes[host] >= 2 {
				return false
			}
		}
	}
	return true
}

func (n *bitcoinNetwork) publicIPVotesLocked(now time.Time) map[string]int {
	votes := map[string]int{}
	for group, host := range n.observedIPs {
		at, known := n.observedAt[group]
		// An undated observation cannot establish fresh support. These records
		// are local connection evidence, never reconstructed from address gossip.
		if !known || at.IsZero() || now.Sub(at) > publicObservationTTL054 || at.After(now.Add(time.Minute)) {
			continue
		}
		if publicBitcoinIP(net.ParseIP(host)) {
			votes[host]++
		}
	}
	return votes
}
func (n *bitcoinNetwork) publicIPObservationsLocked(now time.Time) []map[string]any {
	votes := n.publicIPVotesLocked(now)
	hosts := []string{}
	for host := range votes {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	rows := []map[string]any{}
	for _, host := range hosts {
		rows = append(rows, map[string]any{"ip": host, "independent_network_groups": votes[host], "selected": host == n.selectedPublicIP, "evidence": "Observed outbound public IP only; incoming port not tested"})
	}
	return rows
}
func (n *bitcoinNetwork) selectPublicIPLocked(now time.Time) string {
	votes := n.publicIPVotesLocked(now)
	// Keep a still-supported choice even if another IP gains equal/more votes.
	if n.selectedPublicIP != "" && votes[n.selectedPublicIP] >= 2 {
		n.pendingPublicIP = ""
		return n.selectedPublicIP
	}
	candidates := []string{}
	for host, count := range votes {
		if count >= 2 {
			candidates = append(candidates, host)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if votes[candidates[i]] != votes[candidates[j]] {
			return votes[candidates[i]] > votes[candidates[j]]
		}
		return candidates[i] < candidates[j]
	})
	if len(candidates) == 0 {
		n.pendingPublicIP = ""
		return ""
	}
	choice := candidates[0]
	if n.selectedPublicIP != "" {
		if n.pendingPublicIP != choice {
			n.pendingPublicIP = choice
			n.pendingPublicAt = now
			return ""
		}
		if now.Sub(n.pendingPublicAt) < 2*time.Minute {
			return ""
		}
	}
	n.selectedPublicIP = choice
	n.pendingPublicIP = ""
	n.eventLocked("public_endpoint_selected", choice, "Stable public IP selection from independent Bitcoin peer observations; incoming port is not verified")
	return choice
}
