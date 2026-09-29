package main

import (
	"net"
	"strings"
	"time"
)

// Gateway introductions are still claims, but do not require Bitcoin-only discovery.
func normalGatewayCandidate053(x *bitcoinAddress) bool {
	return x != nil && (x.GatewayVerified || x.Source == "manual_gateway" || x.Source == "lan_udp" || x.GatewayIntroduction != "")
}

// Ordinary, specific-hint, and weak-hint queues, each split into first tries
// and revisits. The exact ledger is authoritative; these queues are disposable.
func workBucket053(x *bitcoinAddress) int {
	b := 0
	if x.EverHint || x.GatewayVerified || x.Source == "lan_udp" {
		b = 2
		quality := hintQuality054(x)
		if quality == "weak_all_bits" || quality == "legacy_aggregate_unknown" {
			b = 4
		}
	}
	if x.Attempts > 0 {
		b++
	}
	return b
}
func (n *bitcoinNetwork) workBucketAtLocked(x *bitcoinAddress, now time.Time) int {
	b := workBucket053(x)
	// A recently checked non-Gateway remains a possible Bitcoin transport, not
	// a new prioritized Gateway lead. Gossip and EOF do not undo this result.
	if b >= 2 && now.Before(n.gatewayRecheckAfterLocked(x)) {
		return b % 2
	}
	return b
}
func (n *bitcoinNetwork) refillWorkLocked(now time.Time) {
	n.addresses = make(map[string]*bitcoinAddress)
	for i := range n.workQueues {
		n.workQueues[i] = nil
	}
	if len(n.ledgerOrder) == 0 {
		return
	}
	for b := 0; b < len(n.workQueues); b++ {
		groupCounts, sourceCounts := map[string]int{}, map[string]int{}
		start := int(n.workCursor[b] % uint64(len(n.ledgerOrder)))
		for j := 0; j < len(n.ledgerOrder) && len(n.workQueues[b]) < maxBitcoinAddresses/len(n.workQueues); j++ {
			address := n.ledgerOrder[(start+j)%len(n.ledgerOrder)]
			x := n.ledger[address]
			if x == nil || x.Exclusion != "" || n.workBucketAtLocked(x, now) != b || now.Before(x.RetryAfter) || n.sessions[address] != nil {
				continue
			}
			if _, ok := n.dialing[address]; ok {
				continue
			}
			explicit := strings.HasPrefix(x.Source, "manual") || x.Source == "local_core_bitcoin"
			group := peerNetworkGroup(address)
			if !explicit && !n.allowPrivate && (groupCounts[group] >= 32 || sourceCounts[x.Source] >= 512) {
				continue
			}
			groupCounts[group]++
			sourceCounts[x.Source]++
			n.workQueues[b] = append(n.workQueues[b], address)
			n.addresses[address] = x
		}
	}
}

// Roles get a turn within three eligible opportunities when capacity exists.
// Within the candidate role, weak claims get one in four turns even under a
// continuous specific-hint flood. Each evidence class gives due retries one
// in four of its own turns. Empty classes lend their turns; caps do not grow.
func (n *bitcoinNetwork) planDiscoveryLocked(now time.Time, seek bool) []bitcoinAddress {
	return n.planDiscoveryWithRecoveryLocked(now, seek, seek)
}

// New-address intake can stop at its storage limit without stranding a known
// member. Recovery still shares every dial, cooldown and diversity budget.
func (n *bitcoinNetwork) planDiscoveryWithRecoveryLocked(now time.Time, seek, recoverKnown bool) []bitcoinAddress {
	return n.planDiscoveryWithRefreshLocked(now, seek, recoverKnown, false)
}

func (n *bitcoinNetwork) planDiscoveryWithRefreshLocked(now time.Time, seek, recoverKnown, refreshPublicIP bool) []bitcoinAddress {
	n.app.settingsMu.RLock()
	probe := n.app.settings.BitcoinGatewayDiscovery
	n.app.settingsMu.RUnlock()
	n.refillWorkLocked(now)
	if n.ledgerError != "" {
		return nil
	}
	roles := []string{"bitcoin_transport", "gateway_candidate", "gateway_search"}
	counts := map[string]int{}
	outbound := 0
	memberCount := 0
	groups := map[string]map[string]bool{}
	for _, r := range append(roles, "gateway_service", "gateway_member", "gateway_provider") {
		groups[r] = map[string]bool{}
	}
	for _, s := range n.sessions {
		if s.view.Direction == "inbound" {
			continue
		}
		outbound++
		if s.view.Gateway && !s.retired {
			memberCount++
		}
		if !(s.view.Role == "bitcoin_transport" && s.headerUnavailable) {
			counts[s.view.Role]++
		}
		if groups[s.view.Role] == nil {
			groups[s.view.Role] = map[string]bool{}
		}
		groups[s.view.Role][peerNetworkGroup(s.peer.addr)] = true
	}
	for _, v := range n.dialing {
		counts[v.Role]++
		if groups[v.Role] == nil {
			groups[v.Role] = map[string]bool{}
		}
		groups[v.Role][peerNetworkGroup(v.Address)] = true
	}
	plans := []bitcoinAddress{}
	for n.tokens >= 1 && len(n.dialing) < n.policy.DialMax && outbound+len(n.dialing) < n.policy.BitcoinSlots+n.policy.GatewaySlots+n.policy.ScoutSlots {
		scheduled := false
		for tries := 0; tries < 3; tries++ {
			r := (n.roleCursor + tries) % 3
			role := roles[r]
			capacity := n.policy.BitcoinSlots
			if r == 1 {
				capacity = n.policy.GatewaySlots
			}
			if r == 2 {
				capacity = n.policy.ScoutSlots
			}
			knownTurn := r == 1 && recoverKnown && memberCount+counts[role] < n.policy.GatewaySlots
			refreshTurn := probe && r == 2 && !seek && refreshPublicIP && !n.intakeBlockedLocked() && n.publicIPRefreshDueLocked(now)
			if r == 2 && !probe {
				continue
			}
			if counts[role] >= capacity || r != 0 && !seek && !knownTurn && !refreshTurn {
				continue
			}
			buckets := []int{0, 1}
			if n.roleTurns[r]%4 == 3 {
				buckets = []int{1, 0}
			}
			if r == 1 {
				turn := n.roleTurns[r]
				specific := []int{2, 3}
				weak := []int{4, 5}
				// Count opportunities within each class, not the role's absolute phase.
				if (turn-turn/4)%4 == 3 {
					specific = []int{3, 2}
				}
				if (turn/4)%4 == 3 {
					weak = []int{5, 4}
				}
				buckets = append(specific, weak...)
				if turn%4 == 3 {
					buckets = append(weak, specific...)
				}
			}
			var chosen *bitcoinAddress
			chosenBucket := 0
			// Alternate known-member priority with the existing hint schedule.
			// Odd turns preserve both specific-hint and weak-hint opportunities.
			chooseKnown := func(standbyOnly bool) {
				for _, address := range n.ledgerOrder {
					x := n.ledger[address]
					if x == nil || !x.GatewayVerified || x.Exclusion != "" || now.Before(x.RetryAfter) || now.Before(n.gatewayRecheckAfterLocked(x)) || n.sessions[address] != nil {
						continue
					}
					if standbyOnly && x.LastOutcome != "gateway_standby_recovery" {
						continue
					}
					if _, ok := n.dialing[address]; ok {
						continue
					}
					explicit := strings.HasPrefix(x.Source, "manual") || x.Source == "local_core_bitcoin"
					group := peerNetworkGroup(address)
					if !n.allowPrivate && !explicit && (groups[role][group] || groups["gateway_service"][group] || groups["gateway_member"][group] || groups["gateway_provider"][group]) {
						continue
					}
					if chosen == nil || x.LastAttempt.Before(chosen.LastAttempt) {
						chosen, chosenBucket = x, n.workBucketAtLocked(x, now)
					}
				}
			}
			if r == 1 && seek && recoverKnown && n.policy.ScoutSlots > 0 && counts["gateway_provider"] == 0 && counts[role] == 0 {
				chooseKnown(true)
			}
			if chosen == nil && knownTurn && (!seek || n.roleTurns[r]%2 == 0) {
				chooseKnown(false)
			}
			// Configured entrances get alternating candidate opportunities after
			// remembered verified members. Other turns retain the Bitcoin hint
			// queues, so failed seeds cannot monopolize Gateway discovery.
			if chosen == nil && r == 1 && seek && (!probe || n.roleTurns[r]%2 == 0) {
				for _, address := range n.configuredGateways {
					x := n.ledger[address]
					if x == nil || x.Exclusion != "" || now.Before(x.RetryAfter) || now.Before(n.gatewayRecheckAfterLocked(x)) || n.sessions[address] != nil {
						continue
					}
					if _, pending := n.dialing[address]; pending {
						continue
					}
					if chosen == nil || x.LastAttempt.Before(chosen.LastAttempt) {
						chosen, chosenBucket = x, n.workBucketAtLocked(x, now)
					}
				}
			}
			for _, b := range buckets {
				if chosen != nil || r != 0 && !seek && !refreshTurn {
					break
				}
				for _, address := range n.workQueues[b] {
					x := n.ledger[address]
					if x == nil || x.Exclusion != "" || now.Before(x.RetryAfter) || n.sessions[address] != nil {
						continue
					}
					if _, ok := n.dialing[address]; ok {
						continue
					}
					if r == 1 && !probe && !normalGatewayCandidate053(x) {
						continue
					}
					if r == 1 && now.Before(n.gatewayRecheckAfterLocked(x)) {
						continue
					}
					// A completed address visit remains useful transport, but not a reason
					// for another scout to ask the same peer again after its socket closes.
					if r == 2 && ((!x.LastCrawled.IsZero() && now.Before(x.LastCrawled.Add(n.policy.RevisitAfter))) || now.Before(n.gatewayRecheckAfterLocked(x))) {
						continue
					}
					explicit := strings.HasPrefix(x.Source, "manual") || x.Source == "local_core_bitcoin"
					group := peerNetworkGroup(address)
					if refreshTurn {
						host, _, _ := net.SplitHostPort(address)
						if !publicBitcoinIP(net.ParseIP(host)) || n.freshPublicIPObservationLocked(group, now) {
							continue
						}
						pendingGroup := false
						for _, dial := range n.dialing {
							if peerNetworkGroup(dial.Address) == group {
								pendingGroup = true
								break
							}
						}
						if pendingGroup {
							continue
						}
					}
					if !n.allowPrivate && !explicit && (groups[role][group] || r == 1 && (groups["gateway_service"][group] || groups["gateway_member"][group] || groups["gateway_provider"][group])) {
						continue
					}
					chosen = x
					chosenBucket = b
					break
				}
				if chosen != nil {
					break
				}
			}
			if chosen == nil && knownTurn {
				chooseKnown(false)
			}
			if chosen == nil {
				continue
			}
			x := chosen
			if refreshTurn {
				n.publicIPRefreshAt = now
				n.eventLocked("public_ip_refresh", x.Address, "Opening a bounded address probe for a fresh outbound public-IP observation")
			}
			n.workCursor[chosenBucket] = x.Ordinal
			n.roleTurns[r]++
			n.roleCursor = (r + 1) % 3
			n.tokens--
			counts[role]++
			groups[role][peerNetworkGroup(x.Address)] = true
			attemptSource := x.Source
			if r == 1 {
				if containsString(n.configuredGateways, x.Address) {
					attemptSource = "manual_gateway"
				} else if x.GatewayVerified {
					attemptSource = "remembered_gateway:" + x.Source
				} else if x.GatewayIntroduction != "" {
					attemptSource = x.GatewayIntroduction
				}
			}
			n.dialing[x.Address] = networkPeerView{ID: "bitcoin:" + x.Address, Address: x.Address, Source: attemptSource, Role: role, State: "connecting", Owner: "Gateway", Direction: "outbound", Transport: "Bitcoin P2P v1", Updated: now, Services: x.Services, ServiceHex: hexServices054(x.Services), Protocols: []string{"Bitcoin P2P"}}
			if x.FirstAttempt.IsZero() {
				x.FirstAttempt = now
			}
			x.LastAttempt = now
			x.Attempts++
			x.InFlight = true
			x.LastRole = role
			if x.EverHint || r == 1 {
				x.GatewayAttempts++
			}
			x.LastOutcome = "attempt_started"
			x.OutcomeAt = now
			n.touchLocked(x)
			n.discovery.Attempts++
			n.eventLocked("dialing", x.Address, "Opening "+role+"; retained check history and evidence priority consulted")
			plans = append(plans, *x)
			scheduled = true
			break
		}
		if !scheduled {
			break
		}
	}
	return plans
}
