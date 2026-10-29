package main

import (
	"sort"
	"strings"
)

const maxAdvertisedRanges = 256

type chainSnapshot struct {
	Network string `json:"network"`
	Height  int64  `json:"height"`
	Hash    string `json:"hash,omitempty"`
	Source  string `json:"source,omitempty"` // core_consensus|bod_headers|unknown
}

type extensionCapability struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Name    string `json:"name,omitempty"`
}

type bodStatus struct {
	Wire               int                   `json:"wire"`
	ProtocolID         string                `json:"protocol_id"`
	GatewayFeatureID   string                `json:"gateway_feature_id"`
	GatewayWire        int                   `json:"gateway_wire"`
	Snapshot           chainSnapshot         `json:"snapshot"`
	Capabilities       []string              `json:"capabilities"`
	Profiles           []string              `json:"profiles"`
	BlockRanges        []heightInterval      `json:"block_ranges,omitempty"`
	BlockRangeCount    int                   `json:"block_range_count"`
	RangesTruncated    bool                  `json:"ranges_truncated"`
	TxLocationCoverage []heightInterval      `json:"tx_location_coverage,omitempty"`
	SpenderCoverage    []heightInterval      `json:"spender_coverage,omitempty"`
	SpenderLookup      bool                  `json:"spender_lookup"`
	SpenderSnapshot    *chainSnapshot        `json:"spender_snapshot,omitempty"`
	GraphThroughHeight int64                 `json:"graph_through_height,omitempty"`
	GraphThroughHash   string                `json:"graph_through_hash,omitempty"`
	CacheBlocks        int                   `json:"cache_blocks"`
	Extensions         []extensionCapability `json:"extensions,omitempty"`
}

func mergeIntervals(in []heightInterval) []heightInterval {
	out := append([]heightInterval(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].From == out[j].From {
			return out[i].To < out[j].To
		}
		return out[i].From < out[j].From
	})
	merged := make([]heightInterval, 0, len(out))
	for _, r := range out {
		if r.From < 0 || r.To < r.From {
			continue
		}
		if len(merged) == 0 || r.From > merged[len(merged)-1].To+1 {
			merged = append(merged, r)
			continue
		}
		if r.To > merged[len(merged)-1].To {
			merged[len(merged)-1].To = r.To
		}
	}
	return merged
}

func rangesCoverFromGenesis(r []heightInterval, through int64) bool {
	if through < 0 {
		return false
	}
	r = mergeIntervals(r)
	return len(r) > 0 && r[0].From == 0 && r[0].To >= through
}

func limitRanges(r []heightInterval) ([]heightInterval, bool) {
	r = mergeIntervals(r)
	if len(r) <= maxAdvertisedRanges {
		return r, false
	}
	return append([]heightInterval(nil), r[:maxAdvertisedRanges]...), true
}

func (a *app) bestClaimSnapshot() chainSnapshot {
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	if core := inspectCore(settings); core.Connected && core.Height >= 0 && validHash(core.BestBlockHash) {
		return chainSnapshot{Network: "mainnet", Height: core.Height, Hash: strings.ToLower(core.BestBlockHash), Source: "core_consensus"}
	}
	st := a.getStatus()
	if st.HeaderHeight >= 0 {
		return chainSnapshot{Network: "mainnet", Height: st.HeaderHeight, Hash: strings.ToLower(st.TipHash), Source: "bod_headers"}
	}
	return chainSnapshot{Network: "mainnet", Height: -1, Source: "unknown"}
}

func (a *app) blockPossessionRanges() []heightInterval {
	var ranges []heightInterval
	// BOD sparse cache: only advertise entries that this peer can currently
	// anchor to its own header chain. Cached-but-pending objects remain private
	// knowledge until validation catches up.
	a.settingsMu.RLock()
	shareCache := a.settings.ShareCache
	a.settingsMu.RUnlock()
	if shareCache {
		a.cacheMu.RLock()
		entries := make([]cachedBlockEntry, 0, len(a.cacheIndex.Blocks))
		for _, e := range a.cacheIndex.Blocks {
			entries = append(entries, e)
		}
		a.cacheMu.RUnlock()
		for _, e := range entries {
			if !e.Private && e.Height >= 0 && a.headerMatches(e.Height, e.Hash) {
				ranges = append(ranges, heightInterval{From: e.Height, To: e.Height})
			}
		}
	}

	// The read-only Core mount remains useful while the Core process is stopped.
	// Only blocks whose hashes are anchored to BOD's local header chain are
	// advertised as active-chain height coverage.
	ranges = append(ranges, a.mountedCoreRanges()...)

	// Core is an optional local validator/backing store. An archival Core has
	// canonical blocks from genesis through its validated tip. A pruned Core is
	// treated as retaining its reported prune-height through tip.
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	if core := inspectCore(settings); core.Connected && core.Height >= 0 {
		from := int64(0)
		if core.Pruned {
			from = core.PruneHeight
			if from < 0 {
				from = 0
			}
		}
		ranges = append(ranges, heightInterval{From: from, To: core.Height})
	}
	return mergeIntervals(ranges)
}

func (a *app) buildBODStatus() bodStatus {
	caps, _, _, _ := a.peerCapabilitiesLegacy()
	cacheBlocks := a.publicCacheBlockCount()
	snapshot := a.bestClaimSnapshot()
	blockRangesAll := a.blockPossessionRanges()
	blockRanges, truncated := limitRanges(blockRangesAll)

	a.coverageMu.RLock()
	coverage := a.coverage
	a.coverageMu.RUnlock()
	// Local materialization coverage may include private queries. Publish only
	// the subset still backed by explicitly public, chain-anchored cache blocks.
	txCov, txTrunc := limitRanges(a.publicTxLocationCoverage(coverage.TxLocation))
	spenderCov, spTrunc := limitRanges(a.graphCoverage())
	truncated = truncated || txTrunc || spTrunc

	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	core := inspectCore(settings)
	spenderLookup := core.Connected && core.SpenderIndexEnabled && core.SpenderIndexHeight >= 0
	var spenderSnapshot *chainSnapshot
	if core.Connected && core.SpenderIndex && core.SpenderIndexHeight >= 0 {
		h := core.SpenderIndexHeight
		hash := ""
		if h == core.Height {
			hash = core.BestBlockHash
		} else if loc, err := coreBlockLocationByHeight(settings, h); err == nil {
			hash = loc.BlockHash
		}
		x := chainSnapshot{Network: "mainnet", Height: h, Hash: strings.ToLower(hash), Source: "core_consensus"}
		spenderSnapshot = &x
	} else if len(spenderCov) > 0 {
		spenderLookup = true
		// BOD v1 serves native positive locator hints only. Native negatives
		// require per-outpoint shard integrity checks and are local results;
		// range coverage alone must not advertise an authoritative negative.
	}

	profiles := []string{}
	history := rangesCoverFromGenesis(blockRangesAll, snapshot.Height)
	if history {
		profiles = append(profiles, "history")
	} else if len(blockRangesAll) > 0 {
		profiles = append(profiles, "partial")
	}
	graph := false
	if history && spenderLookup && spenderSnapshot != nil && spenderSnapshot.Height >= snapshot.Height {
		graph = true
		profiles = append(profiles, "graph")
	}
	if graph {
		caps = append(caps, "authoritative_spender_negative")
	}

	graphThroughHeight := int64(-1)
	graphThroughHash := ""
	if graph && spenderSnapshot != nil {
		graphThroughHeight = spenderSnapshot.Height
		graphThroughHash = spenderSnapshot.Hash
	}
	return bodStatus{
		Wire: bodWireVersion, ProtocolID: bodProtocolID, GatewayFeatureID: gatewayFeatureID, GatewayWire: gatewayWireVersion,
		Snapshot: snapshot, Capabilities: uniqueStrings(caps), Profiles: profiles,
		BlockRanges: blockRanges, BlockRangeCount: len(blockRangesAll), RangesTruncated: truncated,
		TxLocationCoverage: txCov, SpenderCoverage: spenderCov,
		SpenderLookup: spenderLookup, SpenderSnapshot: spenderSnapshot, GraphThroughHeight: graphThroughHeight, GraphThroughHash: graphThroughHash,
		CacheBlocks: cacheBlocks, Extensions: a.extensionCapabilities(),
	}
}
