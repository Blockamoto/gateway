package main

// The ledger owns discovery memory; addresses is only a disposable working set.
// All methods ending Locked require n.mu. No encounter history is shared with peers.
import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const defaultLedgerRecords = 250000
const defaultLedgerBytes int64 = 512 * 1024 * 1024
const maxLedgerRecords = 500000

// Reserve framing and metadata bytes in addition to serialized endpoint rows.
func ledgerReserve053(records int) int64 { return 16384 + int64((records+255)/256)*256 }

type discoveryLedgerMeta struct {
	Version           int       `json:"version"`
	Session           string    `json:"session"`
	Started           time.Time `json:"started"`
	MaxRecords        int       `json:"max_records"`
	MaxBytes          int64     `json:"max_bytes"`
	Observations      uint64    `json:"observations"`
	Repeats           uint64    `json:"repeat_observations"`
	Invalid           uint64    `json:"invalid_observations"`
	Unretained        uint64    `json:"unretained_observations"`
	Imported          uint64    `json:"imported_records"`
	AddressMessages   uint64    `json:"address_messages"`
	MalformedMessages uint64    `json:"malformed_address_messages"`
	IntakeLimited     bool      `json:"intake_limited"`
	RetentionComplete bool      `json:"retention_complete"`
	LastCommit        time.Time `json:"last_commit"`
}

func (n *bitcoinNetwork) now() time.Time {
	if n.clock != nil {
		return n.clock().UTC()
	}
	return time.Now().UTC()
}

// IPs are unwrapped and normalized; names remain names, not fabricated resolved
// IP identities. Mainnet is currently the only supported Bitcoin network.
func canonicalDiscoveryEndpoint(row advertisedPeer) (address, key, excluded string, err error) {
	if row.Invalid != "" {
		return "", "", "", fmt.Errorf("%s", row.Invalid)
	}
	if row.Network != 0 && row.Network != 1 && row.Network != 2 {
		if len(row.RawAddress) == 0 || len(row.RawAddress) > 512 || row.Port == 0 {
			return "", "", "", fmt.Errorf("invalid unsupported endpoint")
		}
		address = fmt.Sprintf("addrv2-%d:%s:%d", row.Network, hex.EncodeToString(row.RawAddress), row.Port)
		return address, "mainnet|" + address, "unsupported_address_family", nil
	}
	if !validPeerEndpoint(row.Addr) {
		return "", "", "", fmt.Errorf("invalid endpoint")
	}
	host, port, _ := net.SplitHostPort(row.Addr)
	p, _ := strconv.Atoi(port)
	if ip := net.ParseIP(host); ip != nil {
		family := "ipv6"
		if v4 := ip.To4(); v4 != nil {
			ip = v4
			family = "ipv4"
		}
		address = net.JoinHostPort(ip.String(), strconv.Itoa(p))
		return address, "mainnet|tcp|" + family + "|" + address, "", nil
	}
	// Zones, malformed numeric IPs and non-ASCII hosts are not dial targets.
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" || strings.Contains(host, ":") {
		return "", "", "", fmt.Errorf("invalid host")
	}
	for _, r := range host {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.') {
			return "", "", "", fmt.Errorf("invalid host character")
		}
	}
	address = net.JoinHostPort(host, strconv.Itoa(p))
	return address, "mainnet|tcp|dns|" + address, "", nil
}

func (n *bitcoinNetwork) initLedger() {
	n.ledger = make(map[string]*bitcoinAddress)
	n.ledgerDirty = make(map[string]bool)
	n.ledgerMeta = discoveryLedgerMeta{Version: 1, Session: fmt.Sprintf("discovery-%x", n.now().UnixNano()), Started: n.now(), MaxRecords: defaultLedgerRecords, MaxBytes: defaultLedgerBytes, RetentionComplete: true}
	if !n.restoreLedger() && n.ledgerError == "" {
		n.importLegacyLedger()
	}
	// Current connections are never restored. An attempted dial was committed
	// before opening its socket, so a killed process leaves explicit evidence.
	for _, x := range n.ledger {
		if x.InFlight {
			x.InFlight = false
			x.LastOutcome = "interrupted"
			x.OutcomeAt = n.now()
			if x.RetryAfter.Before(n.now()) {
				x.RetryAfter = n.now().Add(time.Minute)
			}
			n.touchLocked(x)
		}
	}
	n.discovery.Started = n.ledgerMeta.Started
	n.discovery.Mode = "bootstrap"
	if n.discovery.EverUseful {
		n.discovery.Mode = "recover"
	}
	n.refillWorkLocked(n.now())
}

func (n *bitcoinNetwork) touchLocked(x *bitcoinAddress) {
	n.ledgerDirty[x.Address] = true
	// Compact storage budget counts actual JSON bytes plus bounded frame overhead.
	b, _ := json.Marshal(x)
	size := int64(len(b) + 2)
	n.ledgerCompactBytes += size - x.serializedBytes
	x.serializedBytes = size
	if n.ledgerCompactBytes+ledgerReserve053(len(n.ledger)) > n.ledgerMeta.MaxBytes {
		n.ledgerLimited = true
	}
}
func (n *bitcoinNetwork) intakeBlockedLocked() bool {
	return n.ledgerError != "" || n.ledgerLimited || len(n.ledger) >= n.ledgerMeta.MaxRecords
}

func (n *bitcoinNetwork) recordAddressLocked(row advertisedPeer, source string) bool {
	n.ledgerMetaDirty = true
	n.ledgerMeta.Observations++
	if strings.HasPrefix(source, "bitcoin_addr:") {
		n.discovery.AddressesReceived++
	}
	address, key, excluded, err := canonicalDiscoveryEndpoint(row)
	if err != nil || len(source) > 320 {
		n.ledgerMeta.Invalid++
		n.discovery.AddressesRejected++
		return false
	}
	now := n.now()
	if x := n.ledger[address]; x != nil {
		n.ledgerMeta.Repeats++
		x.Observations++
		x.LastSeen = now
		x.GossipServices |= row.Services
		n.observeClaimLocked(x, row.Services, source, now)
		if x.DirectAt.IsZero() {
			x.Services |= row.Services
		}
		if row.Services&gatewayExperimentalServiceBit != 0 && !x.EverHint {
			x.EverHint = true
			n.discovery.CandidateHints++
			n.eventLocked("candidate_hint", address, "New unique experimental-bit hint; any prior direct evidence is retained")
		}
		if strings.HasPrefix(source, "bitcoin_addr:") && !containsString(x.Suppliers, source) && len(x.Suppliers) < 4 {
			x.Suppliers = append(x.Suppliers, source)
		}
		if x.Exclusion == "inbound_source_only" && strings.HasPrefix(source, "bitcoin_addr:") {
			host, _, _ := net.SplitHostPort(address)
			if n.allowPrivate || publicBitcoinIP(net.ParseIP(host)) {
				x.Exclusion = ""
			} else {
				x.Exclusion = "private_or_disallowed_gossip"
			}
		}
		// Never replace the first introduction. An explicit local/manual source
		// may enable dialing an otherwise excluded private endpoint.
		if strings.HasPrefix(source, "manual") || source == "local_core_bitcoin" {
			if x.Exclusion == "private_or_disallowed_gossip" || x.Exclusion == "inbound_source_only" {
				x.Exclusion = ""
			}
		}
		if row.Timestamp != 0 {
			x.AdvertisedAt = row.Timestamp
		}
		n.touchLocked(x)
		return false
	}
	if n.intakeBlockedLocked() {
		n.ledgerMeta.Unretained++
		n.ledgerMeta.RetentionComplete = false
		return false
	}
	explicit := strings.HasPrefix(source, "manual") || source == "local_core_bitcoin" || source == "lan_udp"
	if excluded == "" && !explicit && !n.allowPrivate {
		host, _, _ := net.SplitHostPort(address)
		ip := net.ParseIP(host)
		if ip == nil || !publicBitcoinIP(ip) {
			excluded = "private_or_disallowed_gossip"
		}
	}
	x := &bitcoinAddress{Address: address, Key: key, Source: source, Services: row.Services, GossipServices: row.Services, FirstSeen: now, LastSeen: now, Observations: 1, AdvertisedAt: row.Timestamp, Exclusion: excluded, EverHint: row.Services&gatewayExperimentalServiceBit != 0}
	n.observeClaimLocked(x, row.Services, source, now)
	b, _ := json.Marshal(x)
	if n.ledgerCompactBytes+int64(len(b))+ledgerReserve053(len(n.ledger)+1) > n.ledgerMeta.MaxBytes {
		n.ledgerLimited = true
		n.ledgerMeta.Unretained++
		n.ledgerMeta.RetentionComplete = false
		return false
	}
	n.ledgerNextOrdinal++
	x.Ordinal = n.ledgerNextOrdinal
	if strings.HasPrefix(source, "bitcoin_addr:") {
		x.Suppliers = []string{source}
	}
	n.ledger[address] = x
	n.ledgerOrder = append(n.ledgerOrder, address)
	n.touchLocked(x)
	n.discovery.AddressesAccepted++
	n.discovery.LastProgress = now
	if x.EverHint {
		n.discovery.CandidateHints++
		n.eventLocked("candidate_hint", address, "Unique Gateway hint learned from "+source+"; not yet a connection")
	}
	// Immediate bounded admission is merely convenient for startup/telemetry.
	// The full work queue is rebuilt fairly from the ledger by the scheduler.
	if excluded == "" && len(n.addresses) < maxBitcoinAddresses {
		n.addresses[address] = x
	}
	return true
}

func (n *bitcoinNetwork) recordFailureLocked(x *bitcoinAddress, outcome, reason string) {
	x.InFlight = false
	x.Failures++
	x.LastError = reason
	if len(x.LastError) > 280 {
		x.LastError = x.LastError[:280]
	}
	x.LastOutcome = outcome
	x.OutcomeAt = n.now()
	shift := x.Failures - 1
	if shift > 7 {
		shift = 7
	}
	delay := 30 * time.Second * time.Duration(1<<uint(shift))
	if delay > time.Hour {
		delay = time.Hour
	}
	if x.GatewayVerified && !x.LastBODSuccess.IsZero() {
		delay = 10 * time.Second * time.Duration(1<<uint(shift))
		if delay > 10*time.Minute {
			delay = 10 * time.Minute
		}
	}
	// Stable per-attempt jitter avoids synchronized reconnect storms. No global
	// RNG or wall-clock dependence is needed by deterministic scheduling tests.
	jitter := time.Duration((x.Ordinal*17+x.Attempts*13)%21) * delay / 100
	x.RetryAfter = n.now().Add(delay + jitter)
	n.touchLocked(x)
}

func (n *bitcoinNetwork) workStateLocked(x *bitcoinAddress, now time.Time) string {
	if x.Exclusion != "" {
		return "excluded"
	}
	if _, ok := n.dialing[x.Address]; ok {
		return "checking"
	}
	if n.sessions[x.Address] != nil {
		return "connected"
	}
	if now.Before(x.RetryAfter) {
		return "cooldown"
	}
	if n.addresses[x.Address] != nil {
		return "queued"
	}
	return "deferred"
}

func (n *bitcoinNetwork) candidateViewsLocked(rows []bitcoinAddress, now time.Time) []map[string]any {
	result := make([]map[string]any, 0, len(rows))
	for i := range rows {
		x := &rows[i]
		result = append(result, map[string]any{"record": x, "state": n.workStateLocked(x, now), "hint_quality": hintQuality054(x), "check_outcome": hintOutcome054(x, n.workStateLocked(x, now)), "gateway_recheck_after": n.gatewayRecheckAfterLocked(x), "gossip_services_hex": fmt.Sprintf("0x%016x", x.GossipServices), "direct_services_hex": fmt.Sprintf("0x%016x", x.DirectServices), "queued": n.addresses[x.Address] != nil})
	}
	return result
}

func (n *bitcoinNetwork) ledgerSummaryLocked(now time.Time) map[string]any {
	uniqueAttempted, totalAttempts, hinted, checked, negotiated, useful := uint64(0), uint64(0), uint64(0), uint64(0), uint64(0), uint64(0)
	uniqueIntroducers, requestedReplies, unsupported := uint64(0), uint64(0), uint64(0)
	freshDeferred, eligibleFresh, eligibleRetry := uint64(0), uint64(0), uint64(0)
	states := map[string]int{"excluded": 0, "checking": 0, "connected": 0, "cooldown": 0, "queued": 0, "deferred": 0}
	outcomes := map[string]int{}
	hints := map[string]int{"unchecked": 0, "checking": 0, "no_compatibility": 0, "unreachable": 0, "historical_unknown": 0, "negotiated": 0, "useful_service": 0}
	hintAttempted, currentHintAttempted, weakHints, specificHints := uint64(0), uint64(0), uint64(0), uint64(0)
	var nextRetry time.Time
	firstWindow, uniqueWindow := 0, 0
	for _, x := range n.ledger {
		st := n.workStateLocked(x, now)
		states[st]++
		if x.Attempts > 0 {
			uniqueAttempted++
			totalAttempts += x.Attempts
		}
		if x.AddressMessages > 0 {
			uniqueIntroducers++
		}
		requestedReplies += x.RequestedReplies
		if x.Exclusion == "unsupported_address_family" {
			unsupported++
		}
		if x.LastOutcome != "" {
			outcomes[x.LastOutcome]++
		}
		if x.GatewayVerified {
			negotiated++
		}
		if !x.LastBODSuccess.IsZero() {
			useful++
		}
		if x.Exclusion == "" && !now.Before(x.RetryAfter) && st != "checking" && st != "connected" {
			if x.Attempts == 0 {
				eligibleFresh++
				if st == "deferred" {
					freshDeferred++
				}
			} else {
				eligibleRetry++
			}
		}
		if now.Before(x.RetryAfter) && x.Exclusion == "" && (nextRetry.IsZero() || x.RetryAfter.Before(nextRetry)) {
			nextRetry = x.RetryAfter
		}
		if !x.Imported && now.Sub(x.FirstSeen) <= time.Minute {
			uniqueWindow++
		}
		if !x.FirstAttempt.IsZero() && now.Sub(x.FirstAttempt) <= time.Minute {
			firstWindow++
		}
		if x.EverHint {
			hinted++
			if x.GatewayChecks > 0 || !x.DirectAt.IsZero() {
				checked++
			}
			if x.GatewayAttempts > 0 || x.GatewayChecks > 0 || !x.DirectAt.IsZero() {
				hintAttempted++
			}
			if x.GatewayAttempts > 0 {
				currentHintAttempted++
			}
			if hintQuality054(x) == "weak_all_bits" || hintQuality054(x) == "legacy_aggregate_unknown" {
				weakHints++
			} else {
				specificHints++
			}
			hints[hintOutcome054(x, st)]++
		}
	}
	status := "exploring"
	if n.ledgerError != "" {
		status = "history_error"
	} else if n.intakeBlockedLocked() {
		status = "history_limit"
	} else if states["checking"] > 0 {
		status = "checking"
	} else if eligibleFresh == 0 && eligibleRetry == 0 {
		status = "waiting"
	}
	return map[string]any{
		"session": n.ledgerMeta.Session, "started": n.ledgerMeta.Started, "records": len(n.ledger), "observations": n.ledgerMeta.Observations,
		"repeat_observations": n.ledgerMeta.Repeats, "invalid_observations": n.ledgerMeta.Invalid, "unretained_observations": n.ledgerMeta.Unretained,
		"imported_records": n.ledgerMeta.Imported, "retention_complete": n.ledgerMeta.RetentionComplete, "unsupported_records": unsupported,
		"unique_attempted": uniqueAttempted, "total_attempts": totalAttempts, "revisits": totalAttempts - uniqueAttempted,
		"unique_address_suppliers": uniqueIntroducers, "address_messages": n.ledgerMeta.AddressMessages, "requested_replies": requestedReplies,
		"malformed_messages": n.ledgerMeta.MalformedMessages, "unique_hints": hinted, "unique_candidates_checked": checked,
		"unique_candidates_attempted": hintAttempted, "unique_candidate_dials_recorded": currentHintAttempted, "weak_hints": weakHints, "specific_hints": specificHints, "unique_gateway_negotiated": negotiated, "unique_useful_service": useful, "states": states, "last_outcomes": outcomes, "hint_outcomes": hints,
		"eligible_first_attempts": eligibleFresh, "eligible_retries": eligibleRetry, "unattempted_deferred": freshDeferred, "next_retry": nextRetry,
		"new_endpoints_last_minute": uniqueWindow, "first_attempts_last_minute": firstWindow,
		"active_queue": len(n.addresses), "active_capacity": maxBitcoinAddresses, "max_records": n.ledgerMeta.MaxRecords, "max_bytes": n.ledgerMeta.MaxBytes,
		"disk_bytes": n.ledgerBytes, "compact_bytes": n.ledgerCompactBytes, "pending_records": len(n.ledgerDirty), "last_commit": n.ledgerMeta.LastCommit,
		"commit": n.ledgerCommitted, "persistence_error": n.ledgerError, "recovery_note": n.ledgerWarning, "intake_paused": n.intakeBlockedLocked(), "work_state": status,
		"history_note": "Exact recorded endpoints, not distinct people or machines. Imported attempts are a lower bound; pre-upgrade evictions cannot be reconstructed.",
	}
}

func (n *bitcoinNetwork) ensureConfiguredAddress(address, source string, services uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	canonical, _, _, err := canonicalDiscoveryEndpoint(advertisedPeer{Addr: address})
	if err != nil {
		return
	}
	if n.ledger[canonical] != nil {
		return
	} // Settings polling is not a new observation.
	n.recordAddressLocked(advertisedPeer{Addr: canonical, Services: services}, source)
}

// Inbound source ports are not advertised listening endpoints. Account for the
// supplier but never queue a client's ephemeral TCP source as a dial target.
func (n *bitcoinNetwork) receiveInboundAddresses(rows []advertisedPeer, supplier string, parseErr error, exceeded bool) {
	n.mu.Lock()
	n.ledgerMetaDirty = true
	if parseErr != nil {
		n.ledgerMeta.MalformedMessages++
	} else if exceeded {
		n.ledgerMeta.Observations += uint64(len(rows))
		n.ledgerMeta.Unretained += uint64(len(rows))
		n.ledgerMeta.RetentionComplete = false
	} else {
		n.ledgerMeta.AddressMessages++
		address, _, _, e := canonicalDiscoveryEndpoint(advertisedPeer{Addr: supplier})
		if e == nil {
			if n.ledger[address] == nil {
				n.recordAddressLocked(advertisedPeer{Addr: address}, "inbound_source")
				if x := n.ledger[address]; x != nil {
					x.Exclusion = "inbound_source_only"
					delete(n.addresses, address)
					n.touchLocked(x)
				}
			}
			if x := n.ledger[address]; x != nil {
				x.AddressMessages++
				x.LastCrawled = n.now()
				x.LastAddrOutcome = "unsolicited_address_message"
				n.touchLocked(x)
			}
		}
		for _, row := range rows {
			n.recordAddressLocked(row, "bitcoin_addr:"+supplier)
		}
	}
	n.mu.Unlock()
	n.persist()
}
func (n *bitcoinNetwork) serviceOutcomeLocked(s *bitcoinSession, outcome, reason string) {
	if x := n.ledger[s.peer.addr]; x != nil {
		if len(reason) > 280 {
			reason = reason[:280]
		}
		x.LastServiceOutcome = outcome
		x.LastServiceError = reason
		x.LastServiceAt = n.now()
		x.LastOutcome = outcome
		x.OutcomeAt = n.now()
		n.touchLocked(x)
	}
}
func (n *bitcoinNetwork) serviceOutcome(s *bitcoinSession, outcome, reason string) {
	n.mu.Lock()
	n.serviceOutcomeLocked(s, outcome, reason)
	n.mu.Unlock()
}
