package main

import (
	"fmt"
	"sort"
	"time"
)

// Keep exact per-observation provenance rather than interpreting an OR-ed
// service mask as one provider's claim. These are hints, not authenticated IDs.
func (n *bitcoinNetwork) observeClaimLocked(x *bitcoinAddress, mask uint64, source string, now time.Time) {
	x.LastClaimMask = fmt.Sprintf("0x%016x", mask)
	x.LastClaimSource, x.LastClaimAt = source, now
	if mask&gatewayExperimentalServiceBit == 0 {
		return
	}
	if x.FirstHintAt.IsZero() {
		x.FirstHintAt, x.FirstHintSource, x.FirstHintMask = now, source, x.LastClaimMask
	}
	if mask == ^uint64(0) {
		x.AllBitsClaim = true
		return
	}
	if !x.SpecificHint {
		x.SpecificHintSource, x.SpecificHintMask = source, x.LastClaimMask
	}
	x.SpecificHint = true
}
func hintQuality054(x *bitcoinAddress) string {
	if x.GatewayVerified {
		return "previously_negotiated"
	}
	if !x.DirectAt.IsZero() && x.DirectServices&gatewayExperimentalServiceBit != 0 && x.DirectServices != ^uint64(0) {
		return "direct_specific_hint"
	}
	if x.SpecificHint {
		return "specific_advertisement"
	}
	if x.AllBitsClaim {
		return "weak_all_bits"
	}
	if x.EverHint && x.GossipServices == ^uint64(0) {
		return "legacy_aggregate_unknown"
	}
	if x.EverHint {
		return "legacy_specific_mask"
	}
	return "no_gateway_hint"
}
func hintOutcome054(x *bitcoinAddress, state string) string {
	switch {
	case state == "checking":
		return "checking"
	case !x.LastBODSuccess.IsZero():
		return "useful_service"
	case x.GatewayVerified:
		return "negotiated"
	case !x.DirectAt.IsZero():
		return "no_compatibility"
	case x.LastOutcome == "imported_attempt_count_lower_bound" || (x.Imported && x.GatewayAttempts == 0 && x.Attempts > 0):
		return "historical_unknown"
	case x.LastOutcome == "connection_failed" || x.LastOutcome == "bitcoin_handshake_failed":
		return "unreachable"
	case x.Attempts > 0:
		return "historical_unknown"
	default:
		return "unchecked"
	}
}
func (n *bitcoinNetwork) gatewayRecheckAfterLocked(x *bitcoinAddress) time.Time {
	deadline := x.GatewayRetryAfter
	// Existing 0.5.3 journals did not store this independent gate. Derive it from
	// the retained direct observation, without clearing or rewriting history.
	if x.LastNegotiation == "no_gateway" && !x.DirectAt.IsZero() {
		derived := x.DirectAt.Add(n.policy.RevisitAfter)
		if derived.After(deadline) {
			deadline = derived
		}
	}
	return deadline
}
func (n *bitcoinNetwork) gatewayCountLocked() int {
	count := 0
	for _, s := range n.sessions {
		if s.view.Gateway {
			count++
		}
	}
	return count
}

// Membership survives loss of a particular service. One additional provider
// may use a scout slot, leaving another scout available at the default limits.
func (n *bitcoinNetwork) retainGatewaySessionsLocked() {
	rows := []*bitcoinSession{}
	for _, s := range n.sessions {
		if s.view.Gateway && !s.retired && s.view.Direction != "inbound" {
			rows = append(rows, s)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		memberI := rows[i].view.Role == "gateway_member" || rows[i].view.Role == "gateway_service"
		memberJ := rows[j].view.Role == "gateway_member" || rows[j].view.Role == "gateway_service"
		if memberI != memberJ {
			return memberI
		}
		if rows[i].requestSuccess != rows[j].requestSuccess {
			return rows[i].requestSuccess
		}
		if rows[i].scout != rows[j].scout {
			return !rows[i].scout
		}
		if !rows[i].view.Connected.Equal(rows[j].view.Connected) {
			return rows[i].view.Connected.Before(rows[j].view.Connected)
		}
		return rows[i].peer.addr < rows[j].peer.addr
	})
	usefulMembers := 0
	for i, s := range rows {
		if i < n.policy.GatewaySlots && s.requestSuccess && s.view.BOD {
			usefulMembers++
		}
	}
	providerSpare := n.policy.ScoutSlots > 0 && usefulMembers < n.policy.GatewaySlots
	for i, s := range rows {
		if i < n.policy.GatewaySlots {
			s.scout = false
			s.view.Role = "gateway_member"
			if s.requestSuccess {
				s.view.Role = "gateway_service"
			}
		} else if providerSpare && s.requestSuccess && s.view.BOD {
			s.scout = false
			s.view.Role = "gateway_provider"
			providerSpare = false
		} else {
			s.scout = true
			s.view.Role = "gateway_candidate"
		}
	}
}

// Only our own graceful standby cooldown may be shortened after a useful
// service is lost. Failed dials and direct-negative negotiation remain gated.
func (n *bitcoinNetwork) wakeStandbyProviderLocked(now time.Time) {
	if n.policy.ScoutSlots < 1 || n.usefulCountLocked() >= n.policy.GatewaySlots {
		return
	}
	for _, s := range n.sessions {
		if s.view.Direction != "inbound" && s.view.Role == "gateway_provider" && !s.retired {
			return
		}
	}
	for _, address := range n.ledgerOrder {
		x := n.ledger[address]
		if x == nil || x.LastOutcome != "gateway_standby" || !x.GatewayVerified || x.LastBODSuccess.IsZero() || x.Failures != 0 || x.Exclusion != "" || now.Before(n.gatewayRecheckAfterLocked(x)) || n.sessions[address] != nil {
			continue
		}
		if _, pending := n.dialing[address]; pending {
			continue
		}
		x.RetryAfter, x.OutcomeAt, x.LastOutcome = now, now, "gateway_standby_recovery"
		n.touchLocked(x)
		n.eventLocked("provider_recovery", address, "A useful service was lost; one gracefully retired provider is eligible within normal dial budgets")
		return
	}
}

// A timed-out uncorrelated headers request disqualifies only that socket's
// header service. Release an ordinary socket once a replacement is available;
// retain Gateway members and any socket still completing a correlated request.
func (n *bitcoinNetwork) retireHeaderTransportsLocked(now time.Time) []*bitcoinSession {
	ready := false
	outbound := len(n.dialing)
	for _, s := range n.sessions {
		if s.view.Direction != "inbound" {
			outbound++
		}
		if !s.retired && s.transportEligibleLocked(false) {
			ready = true
		}
	}
	// If every source is unavailable and the pool is full, make exactly one
	// idle ordinary slot available. Otherwise wait for the replacement first.
	if !ready && outbound < n.policy.BitcoinSlots+n.policy.GatewaySlots+n.policy.ScoutSlots {
		return nil
	}
	var retired []*bitcoinSession
	for _, s := range n.sessions {
		if s.retired || s.view.Direction == "inbound" || s.view.Gateway || s.view.Role != "bitcoin_transport" || !s.headerUnavailable || len(s.slot) != 0 {
			continue
		}
		s.retired = true
		if x := n.ledger[s.peer.addr]; x != nil {
			x.RetryAfter = now.Add(n.policy.RevisitAfter)
			x.LastOutcome, x.OutcomeAt, x.InFlight = "header_transport_replaced", now, false
			n.touchLocked(x)
		}
		retired = append(retired, s)
		if !ready {
			break
		}
	}
	return retired
}

// Look through every valid advertised range. A leading [0,0] is not evidence
// that the remainder of the provider's data is unavailable.
func serviceSample054(resp overlayResponse, height int64) (int64, bool) {
	if height > 1 {
		height = 1
	}
	if resp.Status == nil {
		return height, false
	}
	for _, r := range resp.Status.BlockRanges {
		candidate := r.From
		if candidate == 0 {
			candidate = 1
		}
		if r.From < 0 || r.To < r.From || candidate > r.To || candidate > resp.Status.Snapshot.Height {
			continue
		}
		return candidate, true
	}
	return height, false
}
func hexServices054(mask uint64) string { return fmt.Sprintf("0x%016x", mask) }
