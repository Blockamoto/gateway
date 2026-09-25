package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// GET uses bounded pages. A deliberate download streams a consistent full
// snapshot while holding the ledger lock, with a normal server write deadline.
func (a *app) handleDiscoveryLedger(w http.ResponseWriter, r *http.Request) {
	n := a.network
	query := r.URL.Query()
	if query.Get("export") == "diagnostics" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="gateway-discovery-diagnostics.json"`)
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"version": appVersion, "exported_at": time.Now().UTC(), "note": "Contains peer addresses and local readiness. Numeric service masks are exact in this server-produced file; use hex fields after JavaScript parsing. Candidate details are paginated separately.", "network": n.discoverySnapshot()})
		return
	}
	if query.Get("export") == "ledger" {
		control := http.NewResponseController(w)
		_ = control.SetWriteDeadline(time.Now().Add(30 * time.Second))
		defer control.SetWriteDeadline(time.Time{})
		n.persist()
		n.mu.Lock()
		defer n.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Content-Disposition", `attachment; filename="gateway-discovery-ledger.ndjson"`)
		w.Header().Set("Cache-Control", "no-store")
		enc := json.NewEncoder(w)
		if enc.Encode(map[string]any{"type": "summary", "version": appVersion, "privacy_warning": "Contains endpoints, introduction routes and local probe history. Review before sharing.", "summary": n.ledgerSummaryLocked(n.now())}) != nil {
			return
		}
		for _, address := range n.ledgerOrder {
			x := n.ledger[address]
			if x != nil {
				if enc.Encode(map[string]any{"type": "endpoint", "record": x, "state": n.workStateLocked(x, n.now()), "gossip_services_hex": fmt.Sprintf("0x%016x", x.GossipServices), "direct_services_hex": fmt.Sprintf("0x%016x", x.DirectServices)}) != nil {
					return
				}
			}
		}
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	offset, _ := strconv.Atoi(query.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	limit, _ := strconv.Atoi(query.Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 50
	}
	filter := query.Get("filter")
	endpoint := strings.TrimSpace(query.Get("endpoint"))
	if endpoint != "" {
		canonical, _, _, err := canonicalDiscoveryEndpoint(advertisedPeer{Addr: endpoint})
		if err == nil {
			endpoint = canonical
		}
		x := n.ledger[endpoint]
		if x == nil {
			writeJSON(w, map[string]any{"found": false, "endpoint": endpoint, "note": "This endpoint has not been recorded in this session."})
			return
		}
		views := n.candidateViewsLocked([]bitcoinAddress{*x}, n.now())
		writeJSON(w, map[string]any{"found": true, "endpoint": views[0]})
		return
	}
	rows := []bitcoinAddress{}
	scanned := offset
	for scanned < len(n.ledgerOrder) && len(rows) < limit {
		address := n.ledgerOrder[scanned]
		scanned++
		x := n.ledger[address]
		if x == nil {
			continue
		}
		if (filter == "hints" || strings.HasPrefix(filter, "hints_")) && !x.EverHint {
			continue
		}
		if filter == "hints_weak" && hintQuality054(x) != "weak_all_bits" && hintQuality054(x) != "legacy_aggregate_unknown" {
			continue
		}
		if filter == "hints_unchecked" && !x.DirectAt.IsZero() {
			continue
		}
		if filter == "hints_attempted" && x.GatewayAttempts == 0 && x.GatewayChecks == 0 && x.DirectAt.IsZero() {
			continue
		}
		if filter == "hints_negotiated" && !x.GatewayVerified {
			continue
		}
		if filter == "untried" && x.Attempts != 0 {
			continue
		}
		rows = append(rows, *x)
	}
	next := -1
	if scanned < len(n.ledgerOrder) {
		next = scanned
	}
	writeJSON(w, map[string]any{"session": n.ledgerMeta.Session, "rows": n.candidateViewsLocked(rows, n.now()), "next_offset": next, "total_records": len(n.ledger)})
}

func (a *app) setDiscoveryBudget(w http.ResponseWriter, records int, mb int64) {
	if records < 1000 || records > maxLedgerRecords || mb < 16 || mb > 1024 {
		jsonError(w, 400, fmt.Errorf("Use 1,000 to 500,000 endpoints and 16 to 1,024 MiB. No unlimited history mode."))
		return
	}
	n := a.network
	n.mu.Lock()
	if records < len(n.ledger) || mb*1024*1024 < n.ledgerCompactBytes+ledgerReserve053(len(n.ledger)) {
		n.mu.Unlock()
		jsonError(w, 400, fmt.Errorf("The budget cannot discard existing history. Choose limits above the retained record size."))
		return
	}
	n.ledgerMeta.MaxRecords = records
	n.ledgerMeta.MaxBytes = mb * 1024 * 1024
	n.ledgerLimited = false
	n.ledgerMetaDirty = true
	n.mu.Unlock()
	if !n.persistLedger() {
		jsonError(w, 500, fmt.Errorf("Discovery budget could not be committed; inspect persistence diagnostics"))
		return
	}
	writeJSON(w, map[string]any{"ok": true, "note": "Budget changed; existing records, outcomes and cooldowns are unchanged. Any earlier unretained observations remain reported."})
}
