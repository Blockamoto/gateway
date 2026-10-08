package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Bounded filesystem metadata only: detection is not an archival scan and does
// not open or modify Core's LevelDB files or require its daemon to be running.
func mountedPreparationInfo(s appSettings) map[string]any {
	selected := s
	selected.CoreMountDisabled = false
	dir := resolveCoreBlocksDir(selected)
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
	writeJSON(w, map[string]any{"headers": a.getStatus(), "core": inspectCore(settings), "mounted": store, "mounted_preparation": mountedPreparationInfo(settings), "mounted_locator_bytes": sidecarBytes, "cache": map[string]any{"blocks": cacheCount, "private_blocks": privateCount, "ranges": cached, "truncated": truncated, "sharing_eligible_ranges": shared, "sharing_ranges_truncated": sharedTruncated}, "transaction_locations": map[string]any{"ranges": tx, "truncated": txTruncated}, "spender_knowledge": map[string]any{"ranges": spend, "truncated": spendTruncated, "note": "Positive observed spends. Partial coverage does not prove an output unspent."}, "serving": map[string]any{"enabled": false, "locked": true, "reason": releaseLockReason("gateway-peerhood")}, "settings": settings.public(), "data_directory": filepath.Clean(a.dataDir), "notes": []string{"Local cache ranges describe retained metadata, not a fresh audit of every block file.", "Header coverage is not block-body possession or full consensus validation.", "Mounted locator counts are separate from downloaded cache coverage. The native archive and mounted height ranges remain detailed in Storage.", "Satline stores resumable lineage jobs; it is not a global sat-location index. Derived indexes are locked in this testing build."}})
}
