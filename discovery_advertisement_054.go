package main

import (
	"net"
	"sort"
	"time"
)

const publicObservationTTL054 = 30 * time.Minute

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
