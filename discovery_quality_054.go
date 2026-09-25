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

// A negotiated member is not required to serve our blocks. Retain a bounded
// set; useful providers take precedence, while excess links remain probes.
func (n *bitcoinNetwork) retainGatewaySessionsLocked() {
	rows := []*bitcoinSession{}
	for _, s := range n.sessions {
		if s.view.Gateway && !s.retired {
			rows = append(rows, s)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
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
	for i, s := range rows {
		if i < n.policy.GatewaySlots {
			s.scout = false
			s.view.Role = "gateway_member"
			if s.requestSuccess {
				s.view.Role = "gateway_service"
			}
		} else {
			s.scout = true
			s.view.Role = "gateway_candidate"
		}
	}
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
