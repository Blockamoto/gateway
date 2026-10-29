package main

// A transport run is scheduling intent, not a replacement index format. Every
// consumer keeps its own authenticated publication boundary.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type indexRunOptions struct {
	ParcelEvidenceRoute string            `json:"parcel_evidence_route,omitempty"`
	ParcelRawScanBudget int64             `json:"parcel_raw_scan_budget,omitempty"`
	Retention           string            `json:"retention,omitempty"`
	Mode                string            `json:"mode,omitempty"`
	RetainOutputValues  *bool             `json:"retain_output_values,omitempty"`
	RetainOutputScripts *bool             `json:"retain_output_scripts,omitempty"`
	RetainSatHistory    bool              `json:"retain_sat_history,omitempty"`
	ConventionalIDs     *bool             `json:"conventional_ids,omitempty"`
	Rebuild             bool              `json:"rebuild,omitempty"`
	RebuildSnapshot     *indexRunSnapshot `json:"rebuild_snapshot,omitempty"`
}
type indexRunSnapshot struct {
	Height     int64  `json:"height"`
	BlockHash  string `json:"block_hash"`
	Commitment string `json:"commitment"`
}
type indexRunSelection struct {
	Index string `json:"index"`
	Armed bool   `json:"armed"`
	Live  bool   `json:"live"`
	indexRunOptions
	Options *indexRunOptions `json:"options,omitempty"`
}
type indexRunRequest struct {
	In        *int64              `json:"in"`
	Out       *int64              `json:"out"`
	InHash    string              `json:"in_hash,omitempty"`
	OutHash   string              `json:"out_hash,omitempty"`
	Consumers []indexRunSelection `json:"consumers"`
}
type indexRunProgress struct {
	AdmissionState                string               `json:"admission_state,omitempty"`
	EffectiveFrom                 *int64               `json:"effective_from,omitempty"`
	RangeMode                     string               `json:"range_mode,omitempty"`
	OriginHeight                  *int64               `json:"origin_height,omitempty"`
	NextRequiredHeight            *int64               `json:"next_required_height,omitempty"`
	ProfileHash                   string               `json:"profile_hash,omitempty"`
	ConfigurationRevision         uint64               `json:"configuration_revision,omitempty"`
	DistrictInstance              string               `json:"district_instance,omitempty"`
	DistrictProfileHash           string               `json:"district_profile_hash,omitempty"`
	DistrictConfigurationRevision uint64               `json:"district_configuration_revision,omitempty"`
	Source                        *indexRunSnapshot    `json:"source,omitempty"`
	Dependencies                  []indexRunDependency `json:"dependencies"`
	Index                         string               `json:"index"`
	State                         string               `json:"state"`
	Reason                        string               `json:"reason,omitempty"`
	From                          int64                `json:"from"`
	To                            int64                `json:"to"`
	Height                        int64                `json:"height"`
	BlockHash                     string               `json:"block_hash,omitempty"`
	Commitment                    string               `json:"commitment,omitempty"`
	RequiresLeadIn                bool                 `json:"requires_lead_in,omitempty"`
	ReusedBlocks                  int64                `json:"reused_blocks"`
	AddedBlocks                   int64                `json:"added_blocks"`
	StagedHeight                  *int64               `json:"staged_height,omitempty"`
	StagedBlocks                  int                  `json:"staged_blocks,omitempty"`
	StagedBytes                   int64                `json:"staged_bytes,omitempty"`
	StagedRecords                 int                  `json:"staged_records,omitempty"`
	LogicalBatchFrom              *int64               `json:"logical_batch_from,omitempty"`
	LogicalBatchTo                *int64               `json:"logical_batch_to,omitempty"`
}
type indexRunDependency struct {
	Index  string `json:"index"`
	Kind   string `json:"kind"`
	State  string `json:"state"`
	Reason string `json:"reason"`
	Action string `json:"action,omitempty"`
}
type indexRunCounts struct {
	SourceAcquireMilliseconds      int64 `json:"source_acquire_milliseconds,omitempty"`
	SourceDecodeMilliseconds       int64 `json:"source_decode_milliseconds,omitempty"`
	SourceRawRetentionMilliseconds int64 `json:"source_raw_retention_milliseconds,omitempty"`
	SourceIncidentalMilliseconds   int64 `json:"source_incidental_milliseconds,omitempty"`
	StorageFlushMilliseconds       int64 `json:"storage_flush_milliseconds,omitempty"`
	LocatorBatchFlushes            int64 `json:"locator_batch_flushes,omitempty"`
	LocatorShardAppends            int64 `json:"locator_shard_appends,omitempty"`
	LocatorShardSyncs              int64 `json:"locator_shard_syncs,omitempty"`
	LocatorReceiptWrites           int64 `json:"locator_receipt_writes,omitempty"`
	LocatorActiveWrites            int64 `json:"locator_active_writes,omitempty"`
	LocatorHeadWrites              int64 `json:"locator_head_writes,omitempty"`
	LocatorCommitWrites            int64 `json:"locator_commit_writes,omitempty"`
	LocatorRecordsWritten          int64 `json:"locator_records_written,omitempty"`
	LocatorBytesWritten            int64 `json:"locator_bytes_written,omitempty"`
	SourceMilliseconds             int64 `json:"source_milliseconds,omitempty"`
	ExtractionMilliseconds         int64 `json:"extraction_milliseconds,omitempty"`
	ApplyMilliseconds              int64 `json:"apply_milliseconds,omitempty"`
	ProgressMilliseconds           int64 `json:"progress_milliseconds,omitempty"`
	ReconcileMilliseconds          int64 `json:"reconcile_milliseconds,omitempty"`
	OutcomeReads                   int64 `json:"outcome_reads,omitempty"`
	ChainSteps                     int64 `json:"chain_steps,omitempty"`
	SourceReads                    int64 `json:"source_reads"`
	DecodedBlocks                  int64 `json:"decoded_blocks"`
	InscriptionExtractions         int64 `json:"inscription_extractions"`
	CacheHits                      int64 `json:"cache_hits"`
}
type indexRunSourceFactError struct {
	Index     string `json:"index"`
	Height    int64  `json:"height"`
	BlockHash string `json:"block_hash"`
	Reason    string `json:"reason"`
}
type indexRunRecord struct {
	SourceFactErrors    []indexRunSourceFactError `json:"source_fact_errors,omitempty"`
	AdmissionRevision   uint64                    `json:"admission_revision,omitempty"`
	FrontierHeight      int64                     `json:"frontier_height,omitempty"`
	FrontierEpoch       uint64                    `json:"frontier_epoch,omitempty"`
	Phase               string                    `json:"phase,omitempty"`
	InitialTargetHeight int64                     `json:"initial_target_height"`
	ID                  string                    `json:"id"`
	State               string                    `json:"state"`
	Reason              string                    `json:"reason,omitempty"`
	Request             indexRunRequest           `json:"request"`
	TargetHeight        int64                     `json:"target_height"`
	TargetHash          string                    `json:"target_hash"`
	Consumers           []indexRunProgress        `json:"consumers"`
	Counts              indexRunCounts            `json:"counts"`
	Updated             string                    `json:"updated"`
}
type indexRunDocument struct {
	Schema     int             `json:"schema"`
	Revision   uint64          `json:"revision"`
	Configured bool            `json:"configured"`
	Prepared   indexRunRequest `json:"prepared"`
	Run        *indexRunRecord `json:"run,omitempty"`
	Error      string          `json:"error,omitempty"`
	Checksum   string          `json:"checksum,omitempty"`
}
type indexRunPlan struct {
	Reason       string             `json:"reason"`
	PlanID       string             `json:"plan_id"`
	Ready        bool               `json:"ready"`
	In           *int64             `json:"in"`
	Out          *int64             `json:"out"`
	TargetHeight int64              `json:"target_height"`
	TargetHash   string             `json:"target_hash"`
	RawRetention string             `json:"raw_retention"`
	Request      indexRunRequest    `json:"request"`
	Consumers    []indexRunProgress `json:"consumers"`
	Notes        []string           `json:"notes"`
}

type indexRunPlanBlockedError struct{ Plan indexRunPlan }

func (e indexRunPlanBlockedError) Error() string { return e.Plan.Reason }

func indexRunPath(root string) string          { return filepath.Join(root, "indexes", "run.json") }
func indexRunDigest(d indexRunDocument) string { d.Checksum = ""; d.Error = ""; return indexDigest(d) }

// Optional writers inspect the durable policy without the app's indexRunMu.
// On Windows their read handle must close before atomic replacement of run.json.
var indexRunFileMu sync.RWMutex

func cloneIndexRunDocument(d indexRunDocument) indexRunDocument {
	b, _ := json.Marshal(d)
	var c indexRunDocument
	_ = json.Unmarshal(b, &c)
	return c
}
func readIndexRunDocument(root string) (indexRunDocument, error) {
	indexRunFileMu.RLock()
	defer indexRunFileMu.RUnlock()
	d := indexRunDocument{Schema: 1, Prepared: indexRunRequest{Consumers: []indexRunSelection{}}}
	err := utxoReadJSON(context.Background(), indexRunPath(root), 1<<20, &d)
	if os.IsNotExist(err) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	if d.Schema != 1 || d.Checksum != indexRunDigest(d) {
		return d, fmt.Errorf("coordinated indexing settings are damaged; saved indexes were not changed")
	}
	if _, err = normalizeStoredIndexRunRequest(d.Prepared); err != nil {
		return d, err
	}
	if d.Run != nil {
		if d.Run.ID == "" || len(d.Run.ID) > 128 {
			return d, fmt.Errorf("invalid coordinated run identity")
		}
		if _, err = normalizeStoredIndexRunRequest(d.Run.Request); err != nil {
			return d, err
		}
	}
	return d, nil
}
func (a *app) loadIndexRunLocked() error {
	if a.indexRunLoaded {
		return a.indexRunError
	}
	a.indexRunLoaded = true
	a.indexRunState, a.indexRunError = readIndexRunDocument(a.dataDir)
	if a.indexRunError == nil && a.indexRunState.Run != nil {
		r := a.indexRunState.Run
		// Staging belongs to the former writer's bounded memory, never to the
		// saved run request. A restart retains only the published index prefix.
		for i := range r.Consumers {
			r.Consumers[i].StagedHeight, r.Consumers[i].LogicalBatchFrom, r.Consumers[i].LogicalBatchTo = nil, nil, nil
			r.Consumers[i].StagedBlocks, r.Consumers[i].StagedBytes, r.Consumers[i].StagedRecords = 0, 0, 0
		}
		if r.State == "running" || r.State == "waiting" || r.State == "pausing" {
			r.State = "paused"
			r.Reason = "Interrupted by restart. Resume the admitted run from committed index checkpoints."
		}
	}
	return a.indexRunError
}
func (a *app) persistIndexRunLocked() error {
	indexRunFileMu.Lock()
	defer indexRunFileMu.Unlock()
	a.indexRunState.Revision++
	a.indexRunState.Checksum = indexRunDigest(a.indexRunState)
	if err := atomicWriteJSON(indexRunPath(a.dataDir), a.indexRunState); err != nil {
		return err
	}
	return syncDirectory(filepath.Join(a.dataDir, "indexes"))
}
func (a *app) indexRunSnapshot() indexRunDocument {
	a.indexRunMu.Lock()
	defer a.indexRunMu.Unlock()
	if err := a.loadIndexRunLocked(); err != nil {
		return indexRunDocument{Schema: 1, Error: err.Error(), Prepared: indexRunRequest{Consumers: []indexRunSelection{}}}
	}
	return cloneIndexRunDocument(a.indexRunState)
}
func normalizeIndexRunRequest(req indexRunRequest) (indexRunRequest, error) {
	return normalizeIndexRunRequestAvailability(req, true)
}

// Saved intent must remain inspectable and cancellable after an availability
// change. Execution validates current availability separately before any work.
func normalizeStoredIndexRunRequest(req indexRunRequest) (indexRunRequest, error) {
	return normalizeIndexRunRequestAvailability(req, false)
}

func normalizeIndexRunRequestAvailability(req indexRunRequest, execution bool) (indexRunRequest, error) {
	req.Consumers = append([]indexRunSelection(nil), req.Consumers...)
	if len(req.Consumers) > 256 {
		return req, fmt.Errorf("too many run consumers")
	}
	if req.In != nil && *req.In < 0 || req.Out != nil && *req.Out < 0 || req.In != nil && req.Out != nil && *req.Out < *req.In {
		return req, fmt.Errorf("In and Out must be ordered non-negative block heights")
	}
	if req.InHash != "" && (req.In == nil || !validHash(req.InHash)) || req.OutHash != "" && (req.Out == nil || !validHash(req.OutHash)) {
		return req, fmt.Errorf("marked block identity is invalid")
	}
	seen := map[string]bool{}
	for i := range req.Consumers {
		c := &req.Consumers[i]
		c.Index = strings.ToLower(strings.TrimSpace(c.Index))
		if seen[c.Index] || c.Index == "" {
			return req, fmt.Errorf("duplicate or missing consumer identity")
		}
		seen[c.Index] = true
		if c.Options != nil {
			c.indexRunOptions = *c.Options
			c.Options = nil
		}
		if c.Mode == "" {
			c.Mode = "lean"
		}
		if c.Mode != "lean" && c.Mode != "full" {
			return req, fmt.Errorf("unsupported consumer mode")
		}
		if c.Retention != "" && (!bitmapConsumerID(c.Index) || bitmapConsumerKind(c.Index) != "chain" || c.Retention != "full" && c.Retention != "pruned") {
			return req, fmt.Errorf("retention must be full or pruned and belongs to an attached Bitmap Chain")
		}
		d, err := findIndexDefinition(c.Index)
		if err != nil || execution && (!d.Buildable || d.Locked) {
			if indexRunSelectionEnabled(*c) {
				if err == nil && d.LockReason != "" {
					return req, fmt.Errorf("%s", d.LockReason)
				}
				return req, fmt.Errorf("%s is not an available indexing consumer", c.Index)
			}
			continue
		}
		if c.RetainOutputValues != nil || c.RetainOutputScripts != nil {
			if c.Index != "txo-spender" {
				return req, fmt.Errorf("value and script history options belong to TXIO")
			}
		}
		if c.RetainSatHistory && c.Index != "sat-state" {
			return req, fmt.Errorf("Sat history belongs to Sats")
		}
		if c.ConventionalIDs != nil && c.Index != "inscriptions" {
			return req, fmt.Errorf("related inscription locators belong to Inscriptions")
		}
		if c.Index != bitmapReplayParcelID && (c.ParcelEvidenceRoute != "" || c.ParcelRawScanBudget != 0) {
			return req, fmt.Errorf("Parcel evidence route and scan budget belong to replay Parcels")
		}
		if c.Index == bitmapReplayParcelID {
			if c.Live {
				return req, fmt.Errorf("Replay Parcels require a finite reviewed run; Live extension is not yet available")
			}
			if c.ParcelEvidenceRoute != "" && c.ParcelEvidenceRoute != "retained-raw" {
				return req, fmt.Errorf("unsupported Parcel evidence route")
			}
			if c.ParcelRawScanBudget < 0 || c.ParcelRawScanBudget > 10000000 {
				return req, fmt.Errorf("Parcel raw scan budget must be at most 10000000 block reads")
			}
			if c.ParcelEvidenceRoute == "" && c.ParcelRawScanBudget != 0 {
				return req, fmt.Errorf("Parcel scan budget requires an explicit evidence route")
			}
		}
		if c.RebuildSnapshot != nil && c.Index != "address-state" {
			return req, fmt.Errorf("a reviewed address checkpoint belongs to Current Addresses")
		}
		if c.Rebuild && c.Index != "utxo-state" && c.Index != "sat-state" && c.Index != "address-state" {
			return req, fmt.Errorf("%s does not support a state rebuild", c.Index)
		}
	}
	sort.Slice(req.Consumers, func(i, j int) bool { return req.Consumers[i].Index < req.Consumers[j].Index })
	return req, nil
}
func runSelection(req indexRunRequest, id string) (indexRunSelection, bool) {
	for _, c := range req.Consumers {
		if c.Index == id {
			return c, true
		}
	}
	return indexRunSelection{}, false
}
func runRawRetention(req indexRunRequest) string {
	if c, ok := runSelection(req, "blocks"); ok && c.Armed {
		return "retain"
	}
	return "ephemeral"
}

// A missing new policy preserves legacy explicit consent. Once the transport is
// configured, Off also governs optional browse/cache/projection writers.
func indexRunOptionalWorkAllowed(root, id string) bool {
	if bitmapParcelExecutionDeferred(id) {
		return false
	}
	d, err := readIndexRunDocument(root)
	if err != nil {
		return false
	}
	if !d.Configured {
		live, e := readIndexLiveState(root)
		return e == nil && !live.Policies[id].Stopped
	}
	c, ok := runSelection(d.Prepared, id)
	return ok && c.Armed
}
func indexRunProjectionAllowed(root string, height int64) bool {
	d, err := readIndexRunDocument(root)
	if err != nil {
		return false
	}
	if !d.Configured {
		p, e := readIndexLiveState(root)
		return e == nil && !p.Policies["address-state"].Stopped
	}
	c, ok := runSelection(d.Prepared, "address-state")
	if !ok || !c.Armed || d.Run == nil {
		return false
	}
	if d.Run.State != "running" && d.Run.State != "waiting" {
		return false
	}
	c, ok = runSelection(d.Run.Request, "address-state")
	if p, exists := indexRunProgressFor(d.Run, "address-state"); exists && (p.AdmissionState == "blocked" || p.EffectiveFrom != nil && height < *p.EffectiveFrom) {
		return false
	}
	return ok && c.Armed && (c.Live && d.Run.Request.Out == nil || height <= d.Run.InitialTargetHeight)
}

func (a *app) prepareIndexRun(req indexRunRequest) (indexRunDocument, error) {
	req, err := normalizeIndexRunRequest(req)
	if err != nil {
		return indexRunDocument{}, err
	}
	a.indexRunMu.Lock()
	defer a.indexRunMu.Unlock()
	if err = a.loadIndexRunLocked(); err != nil {
		return indexRunDocument{}, err
	}
	prior := cloneIndexRunDocument(a.indexRunState)
	a.indexRunState.Configured = true
	a.indexRunState.Prepared = req
	if r := a.indexRunState.Run; r != nil && (r.State == "running" || r.State == "waiting" || r.State == "paused") {
		changed := false
		for i := range r.Request.Consumers {
			active := &r.Request.Consumers[i]
			next, ok := runSelection(req, active.Index)
			if !ok || !next.Armed {
				changed = changed || active.Armed
				active.Armed = false
			}
			if !ok || !next.Live {
				changed = changed || active.Live
				active.Live = false
			}
		}
		if changed {
			r.AdmissionRevision = effectiveIndexRunAdmissionRevision(r) + 1
		}
	}
	if err = a.persistIndexRunLocked(); err != nil {
		a.indexRunState = prior
		return indexRunDocument{}, err
	}
	return cloneIndexRunDocument(a.indexRunState), nil
}

func (a *app) planIndexRun(ctx context.Context, req indexRunRequest) (indexRunPlan, error) {
	req, err := normalizeIndexRunRequest(req)
	if err != nil {
		return indexRunPlan{}, err
	}
	authority := a.currentChainAuthority()
	p := indexRunPlan{Ready: true, In: req.In, Out: req.Out, Request: req, TargetHeight: authority.Height, TargetHash: authority.Hash, RawRetention: runRawRetention(req), Consumers: []indexRunProgress{}, Notes: []string{"Each consumer publishes its own checkpoint. Blocks On is the only raw retention choice for this run."}}
	if authority.Height < 0 || !validHash(authority.Hash) {
		p.Ready = false
		p.Notes = append(p.Notes, "Selected-chain tip evidence is unavailable.")
	}
	if req.Out != nil {
		p.TargetHeight = *req.Out
		if *req.Out > authority.Height {
			p.Ready = false
			p.Notes = append(p.Notes, "Out is beyond the selected chain tip.")
		} else {
			t, e := a.utxoSelectedTarget(ctx, authority, *req.Out)
			if e != nil {
				return p, e
			}
			p.TargetHash = t.HashDisplay
		}
	}
	for _, mark := range []struct {
		height *int64
		hash   string
	}{{req.In, req.InHash}, {req.Out, req.OutHash}} {
		if mark.hash != "" {
			t, e := a.utxoSelectedTarget(ctx, authority, *mark.height)
			if e != nil || !strings.EqualFold(t.HashDisplay, mark.hash) {
				return p, fmt.Errorf("the marked block has changed; review the global window again")
			}
		}
	}
	armed, runnable := 0, 0
	for _, c := range req.Consumers {
		if !indexRunSelectionEnabled(c) {
			continue
		}
		armed++
		q := indexRunProgress{Index: c.Index, State: "ready", Height: -1, To: p.TargetHeight}
		q.RangeMode = "contiguous_append"
		if c.Index == "address-history" {
			q.RangeMode = "sparse"
		}
		if c.Index == "address-state" {
			q.RangeMode = "snapshot_projection"
		}
		if c.Index == "utxo-state" || c.Index == "sat-state" {
			origin := int64(0)
			q.OriginHeight = &origin
			q.RangeMode = "complete_prefix"
		}
		d, _ := findIndexDefinition(c.Index)
		q.From = defaultIndexFrom(d)
		if req.In != nil {
			q.From = *req.In
		}
		if q.From < d.StartHeight {
			q.From = d.StartHeight
		}
		if indexRunFactsOnly(c) {
			q.RangeMode = "sparse_facts"
			q.Height = q.From - 1
			q.Reason = "Read selected blocks only for independently armed Values/ScriptPubKeys; TXIO links remain Off."
			q.Dependencies = []indexRunDependency{{Index: "blocks", Kind: "source", State: "obtainable_on_demand", Reason: "Verified temporary bodies; Raw need not be armed."}}
			next := q.From
			q.NextRequiredHeight = &next
			p.Consumers = append(p.Consumers, q)
			runnable++
			continue
		}
		var head *indexCheckpoint
		if bitmapConsumerID(c.Index) { // The instance adapter owns its distinct immutable rules/store.
			instance, e := bindIndexRunBitmapProgress(ctx, a.dataDir, &q)
			if e != nil {
				q.State = "blocked"
				q.Reason = e.Error()
				p.Ready = false
				p.Consumers = append(p.Consumers, q)
				continue
			}
			if q.From < instance.StartHeight {
				q.From = instance.StartHeight
			}
			origin := instance.StartHeight
			if bitmapCanonicalChainID(c.Index) {
				if established, e := bitmapChainOrigin(ctx, a.dataDir); e == nil {
					origin = established
					if q.From < origin {
						q.From = origin
					}
				} else if !errors.Is(e, ErrBitmapEvidenceRequired) {
					q.State, q.Reason = "blocked", e.Error()
				}
			}
			q.OriginHeight = &origin
			q.RangeMode = "complete_prefix"
			head, err = indexRunBitmapHead(ctx, a, c.Index)
		} else if c.Index == "utxo-state" {
			var u *utxoStore
			u, err = utxoStoreHead(a.dataDir)
			if err == nil && u.Head() != nil {
				head = utxoIndexCheckpoint(u.Head(), d)
			}
		} else if c.Index == "address-state" {
			var u *utxoStore
			u, err = utxoStoreHead(a.dataDir)
			if err == nil && u.Head() != nil {
				h := u.Head()
				q.Source = &indexRunSnapshot{Height: h.Height, BlockHash: h.BlockHash, Commitment: h.Commitment}
				if saved, e := readAddressStateContext(ctx, a.dataDir); e == nil {
					head = &indexCheckpoint{From: 0, Height: saved.Height, BlockHash: saved.BlockHash, Commitment: saved.Commitment}
				} else if !errors.Is(e, ErrAddressStateMissing) && !c.Rebuild {
					err = e
				}
				q.From = 0
				q.To = h.Height
				if a.utxoSelectedChainState(ctx, h, authority) != "selected_chain" {
					q.State = "needs_prerequisite"
					q.Reason = "Resume the UTXO source onto the selected chain before projecting Current Addresses."
				}
			}
			if (u == nil || u.Head() == nil) && err == nil {
				if source, ok := runSelection(req, "utxo-state"); ok && source.Armed {
					q.State = "waiting_for_consumer"
					q.Reason = "Build Current Addresses from the explicitly armed UTXO consumer after it commits."
					q.To = p.TargetHeight
				} else {
					q.State = "needs_prerequisite"
					q.Reason = "Arm UTXO explicitly or build a saved complete UTXO state first."
				}
			}
			if c.Rebuild && c.RebuildSnapshot != nil {
				saved, e := readAddressStateContext(ctx, a.dataDir)
				if e != nil || saved.Height != c.RebuildSnapshot.Height || saved.BlockHash != c.RebuildSnapshot.BlockHash || saved.Commitment != c.RebuildSnapshot.Commitment {
					q.State = "blocked"
					q.Reason = "The reviewed address checkpoint changed; review its repair again."
				}
			}
		} else if c.Index == "address-history" {
			var h *addressHistoryStore
			h, err = openAddressHistoryStoreContext(ctx, a.dataDir)
			if err == nil {
				head = addressHistoryIndexCheckpoint(h.head, d)
			}
		} else {
			var s *indexStore
			s, err = indexStoreHead(a.dataDir, c.Index)
			if err == nil {
				head = s.checkpoint
				if head != nil && !c.Rebuild && s.head.Mode != "" && s.head.Mode != c.Mode {
					q.State = "blocked"
					q.Reason = "The saved index uses a different mode. Preserve that mode when preparing this run."
				}
			}
		}
		if err != nil && !c.Rebuild {
			q.State = "rebuild_required"
			q.Reason = err.Error()
		}
		if head != nil && !c.Rebuild {
			if req.In != nil && *req.In < head.From && c.Index != "address-state" && c.Index != "address-history" {
				q.State = "blocked"
				q.Reason = "The saved instance starts after In. Preserve its start when preparing this global window; existing coverage cannot be silently prepended."
			}
			if req.In == nil && c.Index != "address-state" {
				q.From = head.From
			}
			q.Height, q.BlockHash, q.Commitment = head.Height, head.BlockHash, head.Commitment
			q.ReusedBlocks = max(int64(0), min(head.Height, q.To)-q.From+1)
		}
		if (c.Index == "utxo-state" || c.Index == "sat-state" || c.Index == "address-state" || c.Index == "address-history") && c.Mode != "lean" {
			q.State = "blocked"
			q.Reason = "This consumer supports its existing lean state format only."
		}
		if c.Index == "utxo-state" || c.Index == "sat-state" {
			if c.Rebuild {
				q.Height = -1
				q.Commitment = ""
			}
			if q.Height+1 < q.From {
				q.State = "needs_prerequisite"
				q.RequiresLeadIn = true
				q.Reason = "This state needs its complete preceding prefix. Move the global In back to its next block (or genesis) and review; no hidden lead-in scan will start."
			}
			q.From = 0
		}
		if bitmapConsumerID(c.Index) {
			instance, _ := bitmapResolvedInstance(ctx, a.dataDir, c.Index)
			next := instance.StartHeight
			if q.OriginHeight != nil {
				next = *q.OriginHeight
			}
			if head != nil {
				next = head.Height + 1
			}
			if next < q.From {
				q.State = "needs_prerequisite"
				q.RequiresLeadIn = true
				q.Reason = "This Bitmap instance needs its preceding declared segment. Move global In to its next height and review; no hidden lead-in or genesis scan will start."
			}
		}
		if c.Index != "address-state" && c.Index != "address-history" && head != nil && !c.Rebuild && req.In != nil && head.Height+1 < *req.In {
			q.State = "needs_prerequisite"
			q.RequiresLeadIn = true
			q.Reason = "Saved contiguous coverage ends before In. Review a global window including the missing prefix."
		}
		if c.Index != "address-state" && q.From > q.To && q.State == "ready" {
			q.State = "complete"
			q.Reason = "The consumer starts after this window; no work is required."
		}
		if c.Index != "address-history" && head != nil && !c.Rebuild && head.Height >= p.TargetHeight && q.State == "ready" {
			q.State = "complete"
			q.Reason = "The saved checkpoint already covers or is beyond the selected window; it will not be rewound."
		}
		if c.Index == "address-state" && q.Source != nil {
			if q.Source.Height > p.TargetHeight {
				q.State = "blocked"
				q.Reason = "The saved UTXO source is beyond Out; Current Addresses cannot recreate a historical UTXO snapshot."
			} else if source, ok := runSelection(req, "utxo-state"); ok && source.Armed && q.State != "blocked" && q.State != "rebuild_required" {
				q.To = p.TargetHeight
				q.State = "waiting_for_consumer"
				q.Reason = "Project the armed UTXO consumer's committed target; this is not a block-range scan."
			}
		}
		q.Dependencies = []indexRunDependency{{Index: "blocks", Kind: "source", State: "obtainable_on_demand", Reason: "Read verified source blocks locally first; Raw Blocks does not need to be armed.", Action: "inspect_sources"}}
		if c.Index == "address-state" {
			state := "ready"
			if q.Source == nil {
				state = "missing"
			}
			if source, ok := runSelection(req, "utxo-state"); ok && source.Armed {
				state = "selected"
			}
			q.Dependencies = []indexRunDependency{{Index: "utxo-state", Kind: "state", State: state, Reason: "Current Addresses projects an exact saved complete UTXO checkpoint.", Action: "include"}}
		}
		if c.Index == "txo-spender" {
			q.Dependencies = append(q.Dependencies, indexRunDependency{Index: "tx-locator", Kind: "lookup", State: "optional", Reason: "Private translation or verified creation evidence may resolve input positions; unavailable sparse translations remain unresolved.", Action: "inspect"})
		}
		if c.Index == "sat-state" {
			q.Dependencies = append(q.Dependencies, indexRunDependency{Index: "sat-state", Kind: "state", State: "internal", Reason: "Sats maintains its own genesis-first input identity lookup; TXIO and UTXO indexes are not required."})
		}
		if bitmapConsumerID(c.Index) && bitmapConsumerKind(c.Index) != "district" && q.State != "complete" {
			instance, _ := bitmapResolvedInstance(ctx, a.dataDir, c.Index)
			depState := "missing"
			if source, ok := runSelection(req, instance.DistrictInstance); ok && source.Armed {
				depState = "selected"
			} else if bitmapConsumerKind(c.Index) == "parcel" {
				next := max(q.From, q.Height+1)
				if ready, e := bitmapCompletedOutcomeInterval(ctx, a.dataDir, instance.DistrictInstance, next, next, instance.DistrictProfileHash); e == nil && ready {
					depState = "partial"
					if next == p.TargetHeight {
						depState = "ready"
					}
					if q.Reason == "" {
						q.Reason = "Process available completed outcomes, then wait for the exact producer when the next outcome is missing. Its Off state is preserved."
					}
				}
			} else if ready, e := bitmapCompletedOutcomeInterval(ctx, a.dataDir, instance.DistrictInstance, max(q.From, q.Height+1), p.TargetHeight, instance.DistrictProfileHash); e == nil && ready {
				depState = "ready"
			} else if c.Live && bitmapConsumerKind(c.Index) == "chain" {
				next := max(q.From, q.Height+1)
				if ready, e := bitmapCompletedOutcomeInterval(ctx, a.dataDir, instance.DistrictInstance, next, next, instance.DistrictProfileHash); e == nil && ready {
					depState = "partial"
					if q.Reason == "" {
						q.Reason = "Process available completed outcomes, then wait for the exact producer when the next outcome is missing. Its Off state is preserved."
					}
				}
			}
			if bitmapConsumerKind(c.Index) == "chain" {
				q.Dependencies = []indexRunDependency{}
			}
			q.Dependencies = append(q.Dependencies, indexRunDependency{Index: instance.DistrictInstance, Kind: "state", State: depState, Reason: "Requires retained completed outcomes from this exact index and profile; saved outcomes need no raw block reads.", Action: "include"})
			if depState == "missing" {
				q.State = "needs_prerequisite"
				q.Reason = "Arm the exact outcome producer or explicitly review its missing completed outcomes. Missing history is a gap; no source scan starts implicitly."
			}
		}
		if bitmapConsumerID(c.Index) && bitmapConsumerKind(c.Index) == "parcel" {
			if c.Index == bitmapReplayParcelID {
				q.Dependencies = append(q.Dependencies, indexRunDependency{Index: "parent-evidence", Kind: "lookup", State: "conditional", Reason: "Each candidate needs selected-chain witness, satpoint movement and value evidence; unavailable proof remains pending and no whole-chain scan begins implicitly.", Action: "inspect"})
				{
					reason := ""
					if c.ParcelEvidenceRoute != "retained-raw" || c.ParcelRawScanBudget <= 0 {
						reason = "Select retained Raw and approve a positive finite block-read budget."
					} else {
						gaps, gapErr := parcelRawRouteGaps079(a.indexStatus().Timeline, p.TargetHeight)
						if gapErr != nil {
							reason = gapErr.Error()
						} else if len(gaps) > 0 {
							reason = fmt.Sprintf("Retained Raw coverage gaps from genesis to selected end: %v", gaps)
						}
					}
					if reason != "" {
						q.State, q.Reason = "needs_prerequisite", reason
						q.Dependencies = append(q.Dependencies, indexRunDependency{Index: "blocks", Kind: "evidence", State: "missing", Reason: reason, Action: "inspect"})
					}
				}
			} else {
				q.Dependencies = append(q.Dependencies, indexRunDependency{Index: "sat-state", Kind: "lookup", State: "conditional", Reason: "Parcel candidates need checked parent provenance; missing saved evidence waits without starting Satline.", Action: "inspect"})
			}
		}
		if bitmapConsumerID(c.Index) && bitmapConsumerKind(c.Index) == "chain" && c.Retention != "" {
			view, e := bitmapChainRetentionStatus(ctx, a.dataDir, c.Index)
			if e != nil {
				q.State = "blocked"
				q.Reason = e.Error()
			} else if view.Mode != c.Retention {
				p.Notes = append(p.Notes, c.Index+": reviewed retention change to "+c.Retention+"; missing earlier history is not backfilled. Pruned keeps current and actual 12/144 restore points.")
			}
		}
		if q.Reason == "" {
			q.Reason = "Ready to reuse saved coverage and process missing blocks within the global window."
		}
		if c.Index != "address-state" {
			next := q.From
			if head != nil && !c.Rebuild {
				next = head.Height + 1
			}
			if q.RangeMode == "complete_prefix" && head == nil && q.OriginHeight != nil {
				next = *q.OriginHeight
			}
			q.NextRequiredHeight = &next
		}
		if q.State == "needs_prerequisite" || q.State == "blocked" || q.State == "rebuild_required" {
			if c.Index == bitmapReplayParcelID {
				q.AdmissionState = "blocked"
			} else {
				p.Ready = false
			}
		} else {
			runnable++
		}
		p.Consumers = append(p.Consumers, q)
		err = nil
	}
	if armed == 0 {
		p.Ready = false
		p.Notes = append(p.Notes, "Arm at least one available consumer.")
	}
	if armed > 0 && runnable == 0 {
		p.Ready = false
	}
	if req.Out != nil {
		p.Notes = append(p.Notes, "Out is a hard cap. Saved Live preferences cannot follow beyond it.")
	}
	p.Reason = "The prepared consumers are ready for review."
	if !p.Ready {
		p.Reason = "Resolve the marked window or consumer prerequisites before starting."
	}
	p.PlanID = indexDigest(p)
	return p, nil
}

func (a *app) handleIndexRun(w http.ResponseWriter, r *http.Request) {
	if !safeLocalRequest(r) {
		jsonError(w, 403, fmt.Errorf("index runs are local management operations"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	action := strings.TrimPrefix(r.URL.Path, "/api/v1/index/run")
	if action == "" && r.Method == http.MethodGet {
		writeJSON(w, a.indexRunSnapshot())
		return
	}
	if !indexMethod(w, r, http.MethodPost) {
		return
	}
	var result any
	var err error
	switch action {
	case "/prepare", "/plan":
		var req indexRunRequest
		if !indexRequestBody(w, r, &req) {
			return
		}
		normalized, e := normalizeIndexRunRequest(req)
		if e != nil {
			jsonError(w, http.StatusBadRequest, e)
			return
		}
		req = normalized
		if action == "/prepare" {
			result, err = a.prepareIndexRun(req)
		} else {
			result, err = a.planIndexRun(r.Context(), req)
		}
	case "/start":
		var req struct {
			Request indexRunRequest `json:"request"`
			PlanID  string          `json:"plan_id"`
		}
		if !indexRequestBody(w, r, &req) {
			return
		}
		result, err = a.startIndexRun(r.Context(), req.Request, req.PlanID)
	case "/amend":
		var req indexRunAmendment
		if !indexRequestBody(w, r, &req) {
			return
		}
		result, err = a.amendIndexRun(r.Context(), req)
	case "/control":
		var req struct {
			Action string `json:"action"`
			ID     string `json:"id"`
		}
		if !indexRequestBody(w, r, &req) {
			return
		}
		result, err = a.controlIndexRun(r.Context(), req.ID, req.Action)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		var blocked indexRunPlanBlockedError
		if errors.As(err, &blocked) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			writeJSON(w, map[string]any{"error": err.Error(), "plan": blocked.Plan})
			return
		}
		jsonError(w, 409, err)
		return
	}
	writeJSON(w, result)
}

func (a *app) startIndexRun(ctx context.Context, req indexRunRequest, planID string) (indexRunDocument, error) {
	a.indexLiveControlMu.Lock()
	defer a.indexLiveControlMu.Unlock()
	p, err := a.planIndexRun(ctx, req)
	if err != nil {
		return indexRunDocument{}, err
	}
	if !p.Ready {
		return indexRunDocument{}, indexRunPlanBlockedError{p}
	}
	if planID != "" && planID != p.PlanID {
		return indexRunDocument{}, fmt.Errorf("the reviewed run changed; review its current plan again")
	}
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	if a.indexCancel != nil {
		return indexRunDocument{}, fmt.Errorf("another index writer is active; pause it before starting the prepared run")
	}
	a.indexRunMu.Lock()
	defer a.indexRunMu.Unlock()
	if err = a.loadIndexRunLocked(); err != nil {
		return indexRunDocument{}, err
	}
	if previous := a.indexRunState.Run; previous != nil && previous.State != "complete" && previous.State != "cancelled" {
		return cloneIndexRunDocument(a.indexRunState), fmt.Errorf("stop the existing run before starting a different interval; Resume preserves its admitted choices")
	}
	prior := cloneIndexRunDocument(a.indexRunState)
	record := &indexRunRecord{ID: fmt.Sprintf("run-%d", time.Now().UnixNano()), AdmissionRevision: 1, State: "running", Phase: "boundary", Request: p.Request, TargetHeight: p.TargetHeight, InitialTargetHeight: p.TargetHeight, TargetHash: p.TargetHash, Consumers: p.Consumers, Updated: time.Now().UTC().Format(time.RFC3339)}
	parcelProgress, parcelHasProgress := indexRunProgressFor(record, bitmapReplayParcelID)
	if parcel, ok := runSelection(record.Request, bitmapReplayParcelID); ok && indexRunSelectionEnabled(parcel) && parcelHasProgress && parcelProgress.AdmissionState != "blocked" && parcel.ParcelEvidenceRoute == "retained-raw" {
		if _, err := openParcelRawBudget079(a.dataDir, record.ID, parcel.ParcelRawScanBudget, true); err != nil {
			return indexRunDocument{}, err
		}
	}
	record.FrontierHeight = initialIndexRunFrontier(record)
	a.indexRunState.Configured = true
	a.indexRunState.Prepared = p.Request
	a.indexRunState.Run = record
	if err = a.persistIndexRunLocked(); err != nil {
		a.indexRunState = prior
		return indexRunDocument{}, err
	}
	worker, cancel := context.WithCancel(context.Background())
	a.indexCancel = cancel
	a.indexRunActiveID = record.ID
	go a.runCoordinatedIndexes(worker, record.ID)
	return cloneIndexRunDocument(a.indexRunState), nil
}
func (a *app) controlIndexRun(ctx context.Context, id, action string) (indexRunDocument, error) {
	if action == "stop" {
		action = "cancel"
	}
	if action == "resume" {
		d := a.indexRunSnapshot()
		if d.Run == nil || d.Run.ID != id {
			return d, fmt.Errorf("the selected run is unavailable")
		}
		if d.Run.State != "paused" && d.Run.State != "failed" && d.Run.State != "complete_with_errors" {
			return d, fmt.Errorf("only a paused or failed run can resume")
		}
		// Preserve the admitted global window and options, not newly staged edits.
		return a.resumeIndexRun(ctx, d.Run)
	}
	if action != "pause" && action != "cancel" {
		return indexRunDocument{}, fmt.Errorf("action must be pause, resume or cancel")
	}
	a.indexLiveControlMu.Lock()
	defer a.indexLiveControlMu.Unlock()
	a.indexRunMu.Lock()
	if err := a.loadIndexRunLocked(); err != nil {
		a.indexRunMu.Unlock()
		return indexRunDocument{}, err
	}
	r := a.indexRunState.Run
	if r == nil || r.ID != id {
		a.indexRunMu.Unlock()
		return indexRunDocument{}, fmt.Errorf("the selected run is unavailable")
	}
	if r.State == "complete" || r.State == "cancelled" {
		d := cloneIndexRunDocument(a.indexRunState)
		a.indexRunMu.Unlock()
		return d, nil
	}
	prior := cloneIndexRunDocument(a.indexRunState)
	r.State = "pausing"
	r.Reason = "Paused by you; committed records are retained."
	if action == "cancel" {
		r.State = "cancelled"
		r.Reason = "Cancelled; committed records are retained."
	}
	if err := a.persistIndexRunLocked(); err != nil {
		a.indexRunState = prior
		a.indexRunMu.Unlock()
		return indexRunDocument{}, err
	}
	d := cloneIndexRunDocument(a.indexRunState)
	a.indexRunMu.Unlock()
	a.indexMu.Lock()
	active := a.indexCancel != nil && a.indexRunActiveID == id
	if active {
		a.indexCancel()
	}
	a.indexMu.Unlock()
	if !active && action == "pause" {
		a.indexRunMu.Lock()
		if r := a.indexRunState.Run; r != nil && r.ID == id && r.State == "pausing" {
			r.State = "paused"
			_ = a.persistIndexRunLocked()
			d = cloneIndexRunDocument(a.indexRunState)
		}
		a.indexRunMu.Unlock()
	}
	return d, nil
}
func (a *app) resumeIndexRun(ctx context.Context, r *indexRunRecord) (indexRunDocument, error) {
	resumeRequest := r.Request
	resumeRequest.Consumers = append([]indexRunSelection(nil), r.Request.Consumers...)
	for i := range resumeRequest.Consumers {
		if p, exists := indexRunProgressFor(r, resumeRequest.Consumers[i].Index); exists && p.AdmissionState == "blocked" {
			resumeRequest.Consumers[i].Armed = false
		}
	}
	plan, err := a.planIndexRun(ctx, resumeRequest)
	if err != nil {
		return indexRunDocument{}, err
	}
	if !plan.Ready {
		return indexRunDocument{}, indexRunPlanBlockedError{plan}
	}
	a.indexLiveControlMu.Lock()
	defer a.indexLiveControlMu.Unlock()
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	if a.indexCancel != nil {
		return indexRunDocument{}, fmt.Errorf("another index writer is active")
	}
	a.indexRunMu.Lock()
	defer a.indexRunMu.Unlock()
	if err := a.loadIndexRunLocked(); err != nil {
		return indexRunDocument{}, err
	}
	if a.indexRunState.Run == nil || a.indexRunState.Run.ID != r.ID {
		return indexRunDocument{}, fmt.Errorf("run changed")
	}
	state := a.indexRunState.Run.State
	if state != "paused" && state != "failed" && state != "complete_with_errors" {
		return indexRunDocument{}, fmt.Errorf("the run is no longer resumable; its current stop intent is preserved")
	}
	for _, consumer := range a.indexRunState.Run.Consumers {
		if err := validateIndexRunBitmapProgress(ctx, a.dataDir, consumer); err != nil {
			return indexRunDocument{}, err
		}
	}
	for _, c := range r.Request.Consumers {
		if indexRunSelectionEnabled(c) {
			if p, ok := runSelection(a.indexRunState.Prepared, c.Index); !ok || !indexRunSelectionEnabled(p) {
				return indexRunDocument{}, fmt.Errorf("%s is Off; arm it before resuming the admitted run", c.Index)
			}
		}
	}
	parcelProgress, parcelHasProgress := indexRunProgressFor(r, bitmapReplayParcelID)
	if parcel, ok := runSelection(r.Request, bitmapReplayParcelID); ok && indexRunSelectionEnabled(parcel) && parcelHasProgress && parcelProgress.AdmissionState != "blocked" && parcel.ParcelEvidenceRoute == "retained-raw" {
		if _, err := openParcelRawBudget079(a.dataDir, r.ID, parcel.ParcelRawScanBudget, false); err != nil {
			return indexRunDocument{}, fmt.Errorf("Parcel scan budget unavailable on resume: %w", err)
		}
	}
	prior := cloneIndexRunDocument(a.indexRunState)
	a.indexRunState.Run.State = "running"
	a.indexRunState.Run.Reason = ""
	for i := range a.indexRunState.Run.Consumers {
		if a.indexRunState.Run.Consumers[i].AdmissionState == "blocked" {
			continue
		}
		a.indexRunState.Run.Consumers[i].State = "ready"
		a.indexRunState.Run.Consumers[i].Reason = ""
	}
	if err := a.persistIndexRunLocked(); err != nil {
		a.indexRunState = prior
		return indexRunDocument{}, err
	}
	worker, cancel := context.WithCancel(context.Background())
	a.indexCancel = cancel
	a.indexRunActiveID = r.ID
	go a.runCoordinatedIndexes(worker, r.ID)
	return cloneIndexRunDocument(a.indexRunState), nil
}
