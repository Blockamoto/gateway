package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (a *app) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	if a.network == nil {
		jsonError(w, 503, fmt.Errorf("Discovery is not initialized"))
		return
	}
	if r.Method == "GET" {
		if r.URL.Query().Get("view") == "ledger" || r.URL.Query().Get("export") != "" || r.URL.Query().Get("endpoint") != "" {
			a.handleDiscoveryLedger(w, r)
			return
		}
		a.handleNetworkStatus(w, r)
		return
	}
	var q struct {
		Action     string `json:"action"`
		Address    string `json:"address"`
		MaxRecords int    `json:"max_records"`
		MaxMiB     int64  `json:"max_mib"`
	}
	if !satlineBody(w, r, &q) {
		return
	}
	if q.Action == "budget" {
		a.setDiscoveryBudget(w, q.MaxRecords, q.MaxMiB)
		return
	}
	if q.Action == "retry" {
		a.network.retry()
		writeJSON(w, map[string]any{"ok": true, "note": "Retry eligible peers; history, cooldowns and rate limits retained"})
		return
	}
	if q.Action != "pause" && q.Action != "resume" && q.Action != "advertise" {
		jsonError(w, 400, fmt.Errorf("choose pause, resume, retry or advertise"))
		return
	}
	q.Address = strings.TrimSpace(q.Address)
	if q.Action == "advertise" && q.Address != "" && !validPeerEndpoint(q.Address) {
		jsonError(w, 400, fmt.Errorf("Use an IP or host and explicit Gateway port, such as your-public-address:48333; leave blank for automatic detection"))
		return
	}
	a.settingsMu.Lock()
	next := a.settings
	switch q.Action {
	case "pause":
		next.DiscoveryPaused = true
	case "resume":
		next.DiscoveryPaused = false
	case "advertise":
		next.AdvertiseAddr = q.Address
	}
	if e := a.saveSettings(next); e != nil {
		a.settingsMu.Unlock()
		jsonError(w, 500, e)
		return
	}
	a.settings = next
	a.settingsMu.Unlock()
	a.network.mu.Lock()
	a.network.eventLocked("user_action", "", q.Action)
	if q.Action == "advertise" {
		a.network.lastAnnounceKey = ""
	}
	if q.Action == "pause" {
		for addr, cancel := range a.network.dialCancels {
			if a.network.dialing[addr].Role != "bitcoin_transport" {
				cancel()
			}
		}
	}
	a.network.mu.Unlock()
	a.network.maintain()
	writeJSON(w, map[string]any{"ok": true})
}

// Bounded filesystem metadata only: detection is not an archival scan and does
// not open or modify Core's LevelDB files or require its daemon to be running.
func mountedPreparationInfo(s appSettings) map[string]any {
	probe := s
	probe.CoreMountDisabled = false
	dir := resolveCoreBlocksDir(probe)
	found, readable := false, false
	if dir != "" {
		if f, e := os.Open(dir); e == nil {
			readable = true
			for batch := 0; batch < 16; batch++ {
				names, e := f.Readdirnames(128)
				for _, name := range names {
					if strings.HasPrefix(name, "blk") && strings.HasSuffix(name, ".dat") {
						found = true
						break
					}
				}
				if found || e != nil {
					break
				}
			}
			f.Close()
		}
	}
	enabled := !s.CoreMountDisabled && readable && found
	note := "Select readable Bitcoin block files to prepare local block locators. Core RPC is not required."
	if enabled {
		note = "Build Gateway-owned block locators without copying block bodies. Core can remain stopped. Transaction, spender and Ord indexes are separate."
	}
	return map[string]any{"directory": dir, "readable": readable, "block_files_found": found, "selected": !s.CoreMountDisabled, "available": enabled, "record_bytes": coreIndexRecordSize, "note": note}
}
func (a *app) handleDataCoverage(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "GET required", 405)
		return
	}
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	a.cacheMu.RLock()
	cacheRanges := []heightInterval{}
	publicRanges := []heightInterval{}
	privateCount := 0
	cacheCount := len(a.cacheIndex.Blocks)
	for _, e := range a.cacheIndex.Blocks {
		if e.Height >= 0 {
			cacheRanges = append(cacheRanges, heightInterval{From: e.Height, To: e.Height})
			if !e.Private && settings.ShareCache {
				publicRanges = append(publicRanges, heightInterval{From: e.Height, To: e.Height})
			}
		}
		if e.Private {
			privateCount++
		}
	}
	a.cacheMu.RUnlock()
	// This is possession metadata. Rendering it does not freshly re-read or
	// revalidate every cached block. Serving retains its normal integrity gates.
	a.coverageMu.RLock()
	tx := append([]heightInterval(nil), a.coverage.TxLocation...)
	spend := append([]heightInterval(nil), a.coverage.Spender...)
	a.coverageMu.RUnlock()
	cached, truncated := limitRanges(cacheRanges)
	shared, sharedTruncated := limitRanges(publicRanges)
	tx, txTruncated := limitRanges(tx)
	spend, spendTruncated := limitRanges(spend)
	sidecarBytes := int64(0)
	// Count only Gateway's own locator sidecar, never the Core block bodies.
	store := a.coreStoreStatusView()
	if st, e := os.Stat(a.coreStoreIndexPath()); e == nil {
		sidecarBytes = st.Size()
	}
	writeJSON(w, map[string]any{"headers": a.getStatus(), "core": inspectCore(settings), "mounted": store, "mounted_preparation": mountedPreparationInfo(settings), "mounted_locator_bytes": sidecarBytes, "cache": map[string]any{"blocks": cacheCount, "private_blocks": privateCount, "ranges": cached, "truncated": truncated, "sharing_eligible_ranges": shared, "sharing_ranges_truncated": sharedTruncated}, "transaction_locations": map[string]any{"ranges": tx, "truncated": txTruncated}, "spender_knowledge": map[string]any{"ranges": spend, "truncated": spendTruncated, "note": "Positive observed spends. Partial coverage does not prove an output unspent."}, "serving": a.bitcoinAdvertisementStatus(), "settings": settings.public(), "data_directory": filepath.Clean(a.dataDir), "notes": []string{"Local cache ranges describe retained metadata, not a fresh audit of every block file.", "Header coverage is not block-body possession or full consensus validation.", "Mounted locator counts are separate from downloaded cache coverage. The native archive and mounted height ranges remain detailed in Storage.", "Satline stores resumable lineage jobs; it is not a global sat-location index. Ord remains on demand in this release."}})
}
