package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// indexProviderView is a local catalog projection, not a wire capability or a
// proof that an arbitrary query can be answered. Rows sharing ID are separate
// providers of the same index definition. Coverage describes recorded work;
// each existing resolver still checks the evidence needed for its answer.
type indexProviderView struct {
	Readiness      string           `json:"readiness"`
	ID             string           `json:"id"`
	Provider       string           `json:"provider"`
	Coverage       []heightInterval `json:"coverage,omitempty"`
	PublicCoverage []heightInterval `json:"public_coverage,omitempty"`
	Completeness   string           `json:"completeness"`
	Verification   string           `json:"verification"`
	Queryable      bool             `json:"queryable"`
	Serveable      bool             `json:"serveable"`
	Snapshot       *chainSnapshot   `json:"snapshot,omitempty"`
	Note           string           `json:"note"`
}

// These inputs keep policy projection independently testable. They contain no
// credentials, filesystem paths, addresses queried, or remote peer identities.
type indexProviderState struct {
	Headers                       *chainSnapshot
	Blocks, PublicBlocks, Mounted []heightInterval
	TxCoverage, PublicTxCoverage  []heightInterval
	GraphCoverage                 []heightInterval
	TxKnown, PublicTxKnown        bool
	SpendKnown, PublicSpendKnown  bool
	SatlineReady                  bool
	PublishedSatline              bool
	CoreHeaderServing             bool
	BitcoinPeersAvailable         bool
	CommittedBlocks               []heightInterval
	CommittedTx                   []heightInterval
	CommittedSpends               []heightInterval
	CommittedSats                 []heightInterval
	SatSnapshot                   *chainSnapshot
	NumberingSnapshot             *chainSnapshot
}

// indexProviderViews performs no Bitcoin scans, materialization, peer discovery or
// resolution. Core inspection uses the existing short-lived status cache and
// bounded local status RPCs; a configured provider is not assumed connected.
func (a *app) indexProviderViews() []indexProviderView {
	a.settingsMu.RLock()
	s := a.settings
	a.settingsMu.RUnlock()
	core := coreStatus{Height: -1}
	if !s.CoreDisabled && strings.TrimSpace(s.BitcoinDataDir) != "" {
		core = inspectCore(s)
	}
	local := indexProviderState{}
	local.BitcoinPeersAvailable = !s.NetworkDisabled && a.network != nil && len(a.network.listSessions()) > 0
	for _, id := range []string{"blocks", "tx-locator", "txo-spender"} {
		store, err := indexStoreHead(a.dataDir, id)
		if err == nil && store.checkpoint != nil && a.indexChainState(store.checkpoint) == "selected_chain" {
			rangeDone := []heightInterval{{From: store.checkpoint.From, To: store.checkpoint.Height}}
			if id == "blocks" {
				local.CommittedBlocks = rangeDone
			} else if id == "txo-spender" {
				local.CommittedSpends = rangeDone
			} else {
				local.CommittedTx = rangeDone
			}
		}
	}
	local.CoreHeaderServing = a.currentBitcoinServing().Archival
	if store, err := indexStoreHead(a.dataDir, "sat-state"); err == nil && store.checkpoint != nil && a.indexChainState(store.checkpoint) == "selected_chain" {
		batch, err := store.readBatch(store.checkpoint.Commitment)
		if err == nil && batch.Sats != nil {
			tree := newOrdinalTree(store.ordinalPages(), batch.Sats.UTXORoot)
			if _, err = tree.read(batch.Sats.UTXORoot); err == nil {
				local.CommittedSats = []heightInterval{{From: 0, To: store.checkpoint.Height}}
				local.SatSnapshot = &chainSnapshot{Network: "mainnet", Height: store.checkpoint.Height, Hash: store.checkpoint.BlockHash, Source: "gateway_sat_index"}
			}
		}
	}
	if store, err := indexStoreHead(a.dataDir, "inscriptions"); err == nil && store.checkpoint != nil && a.indexChainState(store.checkpoint) == "selected_chain" {
		if state, checkpoint, err := store.numberingView(); err == nil && state != nil && checkpoint != nil {
			local.NumberingSnapshot = &chainSnapshot{Network: "mainnet", Height: checkpoint.Height, Hash: checkpoint.BlockHash, Source: "gateway_inscriptions"}
		}
	}
	st := a.getStatus()
	if st.HeaderHeight >= 0 && st.HeaderCount > st.HeaderHeight {
		if h, err := a.readSelectedHeader(st.HeaderHeight); err == nil {
			digest := hash256(h)
			local.Headers = &chainSnapshot{Network: "mainnet", Height: st.HeaderHeight, Hash: reverseHex(digest[:]), Source: "bod_headers"}
		}
	}
	a.coverageMu.RLock()
	local.TxCoverage = mergeIntervals(a.coverage.TxLocation)
	a.coverageMu.RUnlock()
	local.PublicTxCoverage = a.publicTxLocationCoverage(local.TxCoverage)
	local.GraphCoverage = mergeIntervals(a.graphCoverage())
	if !s.CoreMountDisabled {
		local.Mounted = a.mountedCoreRanges()
	}
	a.cacheMu.RLock()
	blocks := make([]cachedBlockEntry, 0, len(a.cacheIndex.Blocks))
	for _, b := range a.cacheIndex.Blocks {
		blocks = append(blocks, b)
	}
	local.TxKnown, local.SpendKnown = len(a.cacheIndex.Tx) > 0, len(a.cacheIndex.Spends) > 0
	var publicTx, publicSpend []txLocation
	for id, l := range a.cacheIndex.Tx {
		if !a.cacheIndex.PrivateTx[id] {
			publicTx = append(publicTx, l)
		}
	}
	for id, l := range a.cacheIndex.Spends {
		if l.Found && !a.cacheIndex.PrivateSpends[id] {
			publicSpend = append(publicSpend, txLocation{Height: l.Height, BlockHash: l.BlockHash})
		}
	}
	a.cacheMu.RUnlock()
	for _, b := range blocks {
		if !a.headerMatches(b.Height, b.Hash) {
			continue
		}
		info, err := os.Stat(filepath.Join(a.dataDir, "blocks", "raw", b.Prefix+".block"))
		if err != nil || !info.Mode().IsRegular() || info.Size() < 81 {
			continue
		}
		local.Blocks = append(local.Blocks, heightInterval{From: b.Height, To: b.Height})
		if !b.Private && s.ShareCache {
			local.PublicBlocks = append(local.PublicBlocks, heightInterval{From: b.Height, To: b.Height})
		}
	}
	for _, l := range publicTx {
		if a.headerMatches(l.Height, l.BlockHash) {
			local.PublicTxKnown = true
			break
		}
	}
	for _, l := range publicSpend {
		if a.headerMatches(l.Height, l.BlockHash) {
			local.PublicSpendKnown = true
			break
		}
	}
	a.satlineMu.Lock()
	local.SatlineReady = a.satlineInitErr == ""
	a.satlineMu.Unlock()
	if s.SatlineEnabled && s.SatlineServePublished {
		local.PublishedSatline = a.hasPublishedIndexSatline()
	}
	return buildIndexProviderViews(s, core, local)
}

func buildIndexProviderViews(s appSettings, core coreStatus, local indexProviderState) []indexProviderView {
	rows := []indexProviderView{}
	add := func(id, provider, completeness, verification string, queryable, serveable bool, coverage, public []heightInterval, snapshot *chainSnapshot, note string) {
		rows = append(rows, indexProviderView{ID: id, Provider: provider, Coverage: mergeIntervals(coverage), PublicCoverage: mergeIntervals(public), Completeness: completeness, Verification: verification, Queryable: queryable, Serveable: serveable, Snapshot: snapshot, Note: note})
	}
	var headerRanges []heightInterval
	if local.Headers != nil {
		headerRanges = []heightInterval{{From: 0, To: local.Headers.Height}}
	}
	add("headers", "gateway_headers", "selected_chain_prefix", "header_anchored", local.Headers != nil, s.ServeData && local.Headers != nil, headerRanges, headerRanges, local.Headers,
		"Selected proof-of-work header chain. Headers locate blocks; they do not establish block possession or full script validation.")
	add("blocks", "gateway_cache", "sparse", "rechecked_on_read", len(local.Blocks) > 0, s.ServeData && len(local.PublicBlocks) > 0, local.Blocks, local.PublicBlocks, nil,
		"Present raw cache files anchored to local headers; block bytes are verified on read. Private blocks remain local and sharing requires ShareCache.")
	add("blocks", "core_mount", "recorded_ranges", "rechecked_on_read", len(local.Mounted) > 0, s.ServeData && len(local.Mounted) > 0, local.Mounted, local.Mounted, nil,
		"Read-only mounted Bitcoin files with recorded header anchors; file availability and block integrity are rechecked when read. This is block storage, not txindex or spender coverage.")
	add("blocks", "gateway_committed_index", "recorded_contiguous_range", "committed_locally_derived", len(local.CommittedBlocks) > 0, false, local.CommittedBlocks, nil, nil,
		"Private committed decoded transaction evidence survives source eviction. Durable private ID pointers cover the committed range; each shard and active-commit table is bounded to 64 MiB. Auxiliary legacy scans stop at 512 batches/64 MiB and cannot establish absence. Missing or damaged acceleration requires cancellable repair; raw/witness bytes may still require a source.")
	add("tx-locator", "gateway_committed_index", "recorded_contiguous_range", "locator_hint", len(local.CommittedTx) > 0, false, local.CommittedTx, nil, nil,
		"Private durable ID pointers retain containing-block positions throughout committed coverage without a global transaction map. A saved location remains available after source eviction; transaction details require independently verified bytes. Each shard and active-commit table is bounded to 64 MiB; damaged/missing acceleration requires repair and a miss remains unknown.")
	add("tx-locator", "gateway_cache", "sparse", "locator_hint", local.TxKnown, s.ServeData && local.PublicTxKnown, local.TxCoverage, local.PublicTxCoverage, nil,
		"Locally retained txid locations survive raw-block eviction. Resolve through verifyTxLocation to fetch the real block and check transaction inclusion; a miss is unknown.")
	add("tx-locator", "gateway_native_graph", "recorded_ranges", "rechecked_on_query", len(local.GraphCoverage) > 0, false, local.GraphCoverage, nil, nil,
		"Disk-sharded local graph retains transaction incarnations, including duplicate historical txids. Native graph tx lookup is local; Gateway Bitcoin-data wire v1 does not dispatch txloc to this store.")
	add("txo-spender", "gateway_committed_index", "recorded_contiguous_positive_spends", "locally_derived", len(local.CommittedSpends) > 0, false, local.CommittedSpends, nil, nil,
		"Private committed input-to-spender records with durable outpoint lookups. Positive records are anchored to the selected chain; a miss never proves unspentness and duplicate historical origins remain distinct.")
	add("txo-spender", "gateway_cache", "sparse_positive_only", "locator_hint", local.SpendKnown, s.ServeData && local.PublicSpendKnown, nil, nil, nil,
		"Retained positive spend sightings. A miss cannot prove an output unspent; positive answers require the actual spending transaction and exact input.")
	add("txo-spender", "gateway_native_graph", "recorded_ranges", "rechecked_on_query", len(local.GraphCoverage) > 0, s.ServeData && len(local.GraphCoverage) > 0, local.GraphCoverage, local.GraphCoverage, nil,
		"Local unspent answers require a unique existing origin output, continuous creation-to-snapshot coverage, matching tip anchor and intact queried shard. Remote Gateway Bitcoin-data v1 serves positive locator hints only.")
	connected := !s.CoreDisabled && core.Connected
	var coreSnapshot *chainSnapshot
	if connected && core.Height >= 0 && validHash(core.BestBlockHash) {
		coreSnapshot = &chainSnapshot{Network: "mainnet", Height: core.Height, Hash: strings.ToLower(core.BestBlockHash), Source: "bitcoin_core"}
	}
	var coreHeaders, coreBlocks []heightInterval
	if coreSnapshot != nil {
		coreHeaders = []heightInterval{{From: 0, To: core.Height}}
		from := int64(0)
		if core.Pruned {
			from = core.PruneHeight
			if from <= 0 { // An absent pruning boundary is not archival coverage.
				from = core.Height + 1
			}
		}
		coreBlocks = mergeIntervals([]heightInterval{{From: from, To: core.Height}})
	}
	add("headers", "bitcoin_core", "provider_reported_prefix", "core_consensus", coreSnapshot != nil, s.ServeData && coreSnapshot != nil && local.CoreHeaderServing, coreHeaders, coreHeaders, coreSnapshot,
		"Core active-chain height/hash provider; no duplicate Gateway header mirror is required for local lookup. Core-backed getheaders serving additionally requires current archival serving readiness.")
	add("blocks", "bitcoin_core", "provider_reported_ranges", "rechecked_on_read", connected, s.ServeData && connected, coreBlocks, coreBlocks, coreSnapshot,
		"Core RPC supplies retained blocks. Pruned data may be unavailable; Gateway verifies returned block bytes. Core IBD and tip freshness are not inferred from connectivity.")
	txEnabled := connected && core.TxIndexEnabled
	txUsable := connected && coreTxIndexUsable(core)
	txCov := indexCoreCoverage(core, core.TxIndexHeight, txEnabled)
	add("tx-locator", "bitcoin_core_txindex", indexCoreCompleteness(core, core.TxIndexHeight, txEnabled, core.TxIndex), "locator_hint", txUsable, s.ServeData && txUsable, txCov, txCov, coreSnapshot,
		"Core reports its own txindex progress. Enabled is distinct from synced. Returned locations still pass Bitcoin block/transaction verification; index coverage does not require retaining those blocks in Gateway.")
	spendEnabled := connected && core.SpenderIndexEnabled
	spendCov := indexCoreCoverage(core, core.SpenderIndexHeight, spendEnabled)
	add("txo-spender", "bitcoin_core_txospenderindex", indexCoreCompleteness(core, core.SpenderIndexHeight, spendEnabled, core.SpenderIndex), "rechecked_on_query", spendEnabled, s.ServeData && spendEnabled && core.SpenderIndex, spendCov, spendCov, coreSnapshot,
		"Core historical spender provider. Local positives verify the actual input; local negatives also require a synced index and an existing confirmed UTXO at the stated snapshot. Mempool observations remain separate.")
	add("address-state", "bitcoin_core_chainstate", "on_demand_snapshot", "provider_reported", connected, false, nil, nil, coreSnapshot,
		"Local explicit scantxoutset request for a current UTXO snapshot. Successful scan, returned height and best-block hash belong to the answer; no address index is prebuilt and remote-triggered scans are unsupported.")
	add("address-history", "none", "unavailable", "unknown", false, false, nil, nil, nil,
		"No historical address activity index or provider is implemented. Current UTXO scans do not supply spent outputs or historical activity; deeper indexing requires explicit opt-in implementation.")
	satline := s.SatlineEnabled && local.SatlineReady
	add("sat-state", "gateway_sat_index", "continuous_genesis_snapshot", "locally_derived_selected_chain", local.SatSnapshot != nil, false, local.CommittedSats, nil, local.SatSnapshot, "Global ordinal FIFO identity, issuance and placement at the explicit snapshot. Lost, burned, genesis-unspendable and BIP30-destroyed sats remain distinct. Header anchoring is not full script consensus validation. Full movement history and raw block retention are independent.")
	add("sat-state", "gateway_satline_resolver", "on_demand", "rechecked_on_query", satline, false, nil, nil, nil,
		"Current placement can also be derived on demand through Satline. Only CURRENTLY_UNSPENT with a usable snapshot establishes placement at that snapshot; an unresolved frontier is not current state. This provider is separate from the optional global Sat index.")
	add("satline", "gateway_satline", "per_query_sparse", "rechecked_on_query", satline, satline && s.SatlineServePublished && local.PublishedSatline, nil, nil, nil,
		"Per-query lineage and reusable checkpoints, with optional verified peer acceleration. No whole-chain coverage is inferred from saved hops. Only detached explicit publications are remotely served, as hints requiring recipient verification.")
	add("inscriptions", "gateway_known_id", "on_demand", "reveal_checked_on_query", s.OrdEnabled, false, nil, nil, nil, "Existing known-ID inscription resolver checks reveal/content evidence on demand. It does not supply a complete inventory or global numbering; bounded index instances are reported separately.")
	_, ordErr := validatedOrdURL(s.OrdURL)
	add("inscriptions", "ord_http", "configured_coverage_unknown", "provider_claim_until_reveal_checked", s.OrdEnabled && strings.TrimSpace(s.OrdURL) != "" && ordErr == nil, false, nil, nil, nil, "Optional read-only Ord HTTP provider for known-ID content and metadata. Configuration does not prove availability or historical coverage; external databases are not modified.")
	add("inscriptions", "gateway_canonical_numbering", "continuous_historical_rules", "locally_derived_selected_chain", local.NumberingSnapshot != nil, false, nil, nil, local.NumberingSnapshot, "Automatic Inscriptions enrichment under the pinned Ord historical rules. Continuous history from no later than block 767430, verified input values, curse/reinscription state and Jubilee rules are required. Partial scans retain stable IDs without invented canonical numbers.")

	for i := range rows {
		r := &rows[i]
		r.Readiness = "needs_coverage_or_provider"
		if r.Queryable {
			r.Readiness = "provider_available_check_coverage"
		}
		if r.Provider == "gateway_committed_index" && r.Queryable {
			r.Readiness = "retained_records_durable_id_lookup"
		}
		if r.ID == "blocks" && r.Provider == "gateway_cache" && !r.Queryable && local.BitcoinPeersAvailable {
			r.Readiness = "connected_peers_fetch_on_demand"
		}
		if (r.ID == "sat-state" || r.ID == "satline") && r.Queryable && r.Provider != "gateway_sat_index" {
			r.Readiness = "on_demand_derivation_requires_evidence"
		}
		if r.Provider == "ord_http" && r.Queryable {
			r.Readiness = "configured_provider_unchecked"
		}
		if r.Provider == "bitcoin_core_txindex" || r.Provider == "bitcoin_core_txospenderindex" {
			if r.Provider == "bitcoin_core_txindex" && txEnabled && !txUsable {
				r.Readiness = "enabled_index_not_ready"
			}
			if r.Queryable {
				r.Readiness = "provider_available_partial_coverage"
				if r.Completeness == "provider_reported_at_snapshot" {
					r.Readiness = "provider_reported_at_snapshot"
				}
			}
		}
		if r.ID == "address-history" || r.ID == "inscription-numbering" {
			r.Readiness = "not_implemented"
		}
		if !releaseFeatureAvailable(r.ID) {
			r.Readiness, r.Note = "locked", releaseLockReason(r.ID)
			r.Queryable, r.Serveable = false, false
			r.PublicCoverage = nil
		}
	}
	return rows
}

func indexCoreCoverage(core coreStatus, height int64, enabled bool) []heightInterval {
	if !enabled || height < 0 || core.Height < 0 {
		return nil
	}
	if height > core.Height {
		height = core.Height
	}
	return []heightInterval{{From: 0, To: height}}
}

func indexCoreCompleteness(core coreStatus, height int64, enabled, synced bool) string {
	if !enabled {
		return "unavailable"
	}
	if synced && !core.InitialBlockDownload && height >= core.Height && core.Height >= 0 && validHash(core.BestBlockHash) {
		return "provider_reported_at_snapshot"
	}
	return "provider_reported_partial"
}

// Stop at the first structurally usable publication. A file name, private
// checkpoint or enabled preference alone must not imply serveable lineage.
func (a *app) hasPublishedIndexSatline() bool {
	f, err := os.Open(a.satlinePublicRoot())
	if err != nil {
		return false
	}
	defer f.Close()
	for {
		entries, err := f.ReadDir(64)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			if a.indexSatlinePublicationUsable(e.Name()) {
				return true
			}
		}
		if err != nil {
			return false
		}
	}
}

func (a *app) indexSatlinePublicationUsable(name string) bool {
	f, err := os.Open(filepath.Join(a.satlinePublicRoot(), name))
	if err != nil {
		return false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxSatlinePublishedBytes+1))
	if err != nil || len(b) > maxSatlinePublishedBytes {
		return false
	}
	var p satlinePublication
	if json.Unmarshal(b, &p) != nil || p.Schema != 1 || p.VerifierVersion != blockVerifierVersion || !p.Query.valid() ||
		filepath.Base(a.satlinePublicationPath(p.Query)) != name || !validSatlinePoint(p.Start) || len(p.Hops) == 0 || len(p.Hops) > maxSatlinePublishedHops {
		return false
	}
	// Serving is a structural capability. Publication anchors may have become
	// stale; the receiving peer must still verify the Bitcoin evidence.
	return sameSatlinePoint(p.Start, p.Hops[0].Source)
}

// indexSatStateAtSnapshot applies only to results from the local resolver or
// verifier. It does not authenticate a peer JSON object. A saved frontier, an
// unchecked anchor or an answer without a snapshot is not current placement.
func indexSatStateAtSnapshot(r satlineResult) bool {
	return r.State == "CURRENTLY_UNSPENT" && r.CurrentSatpoint != nil && validSatlinePoint(*r.CurrentSatpoint) &&
		r.Snapshot != nil && r.Snapshot.Network == "mainnet" && r.Snapshot.Height >= r.CurrentSatpoint.Height && validHash(r.Snapshot.Hash) &&
		verificationRank(r.VerificationState) >= 4 && r.AnchorStatus != string(anchorUnavailable) && r.AnchorStatus != string(anchorConflict)
}
