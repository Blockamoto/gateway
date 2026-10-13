package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

type addressViewSnapshot struct {
	Height             int64  `json:"height"`
	BlockHash          string `json:"block_hash"`
	Commitment         string `json:"commitment"`
	Validation         string `json:"validation"`
	ConsensusValidated bool   `json:"consensus_validated"`
}
type addressViewOutput struct {
	Output      string `json:"output"`
	ValueSats   uint64 `json:"value_sats"`
	ScriptType  string `json:"script_type"`
	SpentStatus string `json:"spent_status"`
	Spender     string `json:"spender,omitempty"`
}
type addressViewResult struct {
	Address             string               `json:"address"`
	Mode                string               `json:"mode"`
	State               string               `json:"state"`
	Source              string               `json:"source"`
	Snapshot            *addressViewSnapshot `json:"snapshot"`
	CurrentBalanceSats  *uint64              `json:"current_balance_sats"`
	CurrentOutputCount  *uint64              `json:"current_output_count"`
	Outputs             []addressViewOutput  `json:"outputs"`
	NextCursor          string               `json:"next_cursor"`
	HasMore             bool                 `json:"has_more"`
	Coverage            []heightInterval     `json:"coverage"`
	Gaps                []heightInterval     `json:"gaps"`
	ReceivedSats        *uint64              `json:"received_sats"`
	ReceivedOutputCount *uint64              `json:"received_output_count"`
	Complete            bool                 `json:"complete"`
	Note                string               `json:"note"`
}

func addressSnapshot(c *addressStateCheckpoint) *addressViewSnapshot {
	if c == nil {
		return nil
	}
	return &addressViewSnapshot{c.Height, c.BlockHash, c.Commitment, c.Validation, c.ConsensusValidated}
}
func (a *app) addressViewContext(ctx context.Context, input, mode, cursor string, limit int) (addressViewResult, error) {
	address, script, _, err := bitcoinAddressScript(input)
	if err != nil {
		return addressViewResult{}, err
	}
	if mode == "" {
		mode = "current"
	}
	if mode != "current" && mode != "historical" {
		return addressViewResult{}, fmt.Errorf("address mode must be current or historical")
	}
	if limit < 1 || limit > 100 {
		return addressViewResult{}, fmt.Errorf("address page limit must be between 1 and 100")
	}
	out := addressViewResult{Address: address, Mode: mode, Source: "local_address_index", State: "not_built", Outputs: []addressViewOutput{}, Coverage: []heightInterval{}, Gaps: []heightInterval{}}
	authority := a.currentChainAuthority()
	if mode == "current" {
		status, err := addressStateStatusContext(ctx, a.dataDir)
		if err != nil {
			out.State = status.State
			out.Note = err.Error()
			return out, nil
		}
		out.State = status.State
		out.Snapshot = addressSnapshot(status.Checkpoint)
		u, err := utxoStoreHead(a.dataDir)
		if err != nil {
			out.State = "stale"
			out.Note = err.Error()
			return out, nil
		}
		if u.Head() == nil {
			out.State = "dependency_missing"
			out.Note = "Build the UTXO set before current addresses."
			return out, nil
		}
		if status.State != "ready" {
			out.Note = "Build or resume Current UTXOs to create the address lookup for the saved UTXO state."
			return out, nil
		}
		if a.utxoSelectedChainState(ctx, &status.Checkpoint.Source, authority) != "selected_chain" {
			out.State = "stale"
			out.Note = "The saved address checkpoint cannot currently be anchored to the selected chain."
			return out, nil
		}
		result, err := lookupAddressStateContext(ctx, a.dataDir, script, limit, cursor)
		if err != nil {
			if errors.Is(err, ErrAddressStateStale) || errors.Is(err, ErrAddressStateCorrupt) || errors.Is(err, ErrAddressStateMissing) {
				out.State = "stale"
				if errors.Is(err, ErrAddressStateCorrupt) {
					out.State = "rebuild_required"
				}
				out.Note = err.Error()
				return out, nil
			}
			return out, err
		}
		if a.utxoSelectedChainState(ctx, &result.Checkpoint.Source, authority) != "selected_chain" {
			out.State = "stale"
			out.Note = "The selected chain changed during the address lookup."
			return out, nil
		}
		out.Snapshot = addressSnapshot(result.Checkpoint)
		out.CurrentBalanceSats = &result.BalanceSats
		out.CurrentOutputCount = &result.OutputCount
		for _, row := range result.Outputs {
			out.Outputs = append(out.Outputs, addressViewOutput{Output: row.Output, ValueSats: row.ValueSats, ScriptType: row.ScriptType, SpentStatus: "unspent"})
		}
		out.NextCursor, out.HasMore, out.Complete = result.NextCursor, result.HasMore, result.Complete
		out.Coverage = []heightInterval{{result.Checkpoint.Height, result.Checkpoint.Height}}
		out.Note = "Balance at the saved processed UTXO checkpoint. Mempool changes, wallet spendability, inscription holdings and complete consensus validation are not included."
		return out, nil
	}
	store, err := openAddressHistoryStoreContext(ctx, a.dataDir)
	if err != nil {
		return out, err
	}
	hashAt := a.addressSelectedHashes(ctx, authority)
	result, err := store.QueryContext(ctx, address, cursor, limit, hashAt)
	if err != nil {
		return out, err
	}
	out.State = result.State
	if out.State == "known" {
		out.State = "ready"
	}
	if out.State == "unknown" {
		out.State = "not_built"
	}
	out.Snapshot = &addressViewSnapshot{result.Snapshot.Height, result.Snapshot.BlockHash, result.Snapshot.Commitment, result.Snapshot.Validation, result.Snapshot.ConsensusValidated}
	out.Coverage, out.Gaps = result.Coverage, result.Gaps
	out.Complete = result.Complete
	out.Note = result.Note
	if out.State == "ready" {
		out.ReceivedSats = &result.ReceivedSats
		out.ReceivedOutputCount = &result.ReceivedOutputCount
	}
	for _, row := range result.Outputs {
		out.Outputs = append(out.Outputs, addressViewOutput{Output: row.Output, ValueSats: row.ValueSats, ScriptType: row.ScriptType, SpentStatus: row.SpentStatus})
	}
	out.NextCursor, out.HasMore = result.NextCursor, result.HasMore
	return out, nil
}
func (a *app) addressSelectedHashes(ctx context.Context, authority chainAuthorityView) func(int64) (string, error) {
	return func(height int64) (string, error) {
		if _, err := a.utxoSelectedTarget(ctx, authority, authority.Height); err != nil {
			return "", err
		}
		target, err := a.utxoSelectedTarget(ctx, authority, height)
		return target.HashDisplay, err
	}
}
func (a *app) handleAddressView(w http.ResponseWriter, r *http.Request) {
	if !indexMethod(w, r, http.MethodGet) {
		return
	}
	if !safeLocalRequest(r) {
		jsonError(w, 403, fmt.Errorf("address indexes are restricted to the local management interface"))
		return
	}
	if !releaseHTTPFeature(w, "address-state") {
		return
	}
	limit, err := indexQueryLimit(r.URL.Query().Get("limit"))
	if err != nil || limit > 100 {
		jsonError(w, 400, fmt.Errorf("address page limit must be between 1 and 100"))
		return
	}
	out, err := a.addressViewContext(r.Context(), r.URL.Query().Get("address"), r.URL.Query().Get("mode"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, out)
}

type indexAddressCurrentStatus struct {
	State               string               `json:"state"`
	Snapshot            *addressViewSnapshot `json:"snapshot"`
	UTXOSnapshot        *addressViewSnapshot `json:"utxo_snapshot"`
	OutputCount         uint64               `json:"output_count"`
	ExcludedOutputCount uint64               `json:"excluded_output_count"`
	Note                string               `json:"note"`
}
type indexAddressStatusView struct {
	Current    indexAddressCurrentStatus `json:"current"`
	Historical struct {
		State    string               `json:"state"`
		Coverage []heightInterval     `json:"coverage"`
		Snapshot *addressViewSnapshot `json:"snapshot"`
		Note     string               `json:"note"`
	} `json:"historical"`
}

func (a *app) indexAddressStatus(instances []indexInstance) (*indexAddressStatusView, []indexInstance) {
	ctx := context.Background()
	view := &indexAddressStatusView{}
	policies, _ := a.indexLivePolicies()
	retention := func(id string) string {
		if choice := policies[id].Retention; validIndexRetentionFor(id, choice) {
			return choice
		}
		return "ephemeral"
	}
	status, err := addressStateStatusContext(ctx, a.dataDir)
	view.Current.State = status.State
	view.Current.Note = status.Error
	if err != nil {
		view.Current.State = status.State
		view.Current.Note = err.Error()
	}
	authority := a.currentChainAuthority()
	if u, e := utxoStoreHead(a.dataDir); e == nil && u.Head() != nil {
		c := u.Head()
		view.Current.UTXOSnapshot = &addressViewSnapshot{c.Height, c.BlockHash, c.Commitment, c.Validation, c.ConsensusValidated}
	} else if e == nil {
		view.Current.State = "dependency_missing"
		view.Current.Note = "Build the UTXO set before current addresses."
	}
	if cp := status.Checkpoint; cp != nil {
		view.Current.Snapshot = addressSnapshot(cp)
		view.Current.OutputCount = cp.OutputCount
		view.Current.ExcludedOutputCount = cp.ExcludedOutputCount
		if view.Current.State == "ready" && a.utxoSelectedChainState(ctx, &cp.Source, authority) != "selected_chain" {
			view.Current.State = "stale"
			view.Current.Note = "Address snapshot requires selected-chain reconciliation."
		}
		def, _ := findIndexDefinition("address-state")
		c := addressStateIndexCheckpoint(cp, def)
		instances = append(instances, indexInstance{Mode: "lean", Definition: def.ID, Version: def.Version, Network: def.Network, Coverage: []heightInterval{{cp.Height, cp.Height}}, Gaps: []heightInterval{}, Completeness: "complete_live_address_set_at_snapshot", Verification: "header_anchored_processing; consensus_validated=false", Provenance: "Saved UTXO state and exact output scripts", Retention: retention(def.ID), Queryable: view.Current.State == "ready", Checkpoint: c, Note: view.Current.Note})
	}
	view.Historical.State = "not_built"
	view.Historical.Coverage = []heightInterval{}
	if s, e := openAddressHistoryStoreContext(ctx, a.dataDir); e != nil {
		view.Historical.State = "stale"
		view.Historical.Note = e.Error()
	} else if s.head.Commitment != "" && len(s.head.Coverage) > 0 && s.head.Height >= 0 {
		h := s.head
		view.Historical.State = "ready"
		view.Historical.Coverage = h.Coverage
		view.Historical.Snapshot = &addressViewSnapshot{h.Height, h.BlockHash, h.Commitment, "header_anchored_output_evidence", false}
		if hash, e := a.addressSelectedHashes(ctx, authority)(h.Height); e != nil || hash != h.BlockHash {
			view.Historical.State = "stale"
			view.Historical.Note = "Saved history needs selected-chain reconciliation."
		}
		def, _ := findIndexDefinition("address-history")
		cp := addressHistoryIndexCheckpoint(h, def)
		instances = append(instances, indexInstance{Mode: "lean", Definition: def.ID, Version: def.Version, Network: def.Network, Coverage: h.Coverage, Gaps: indexCoverageGaps(h.Coverage, 0, h.Height), Completeness: "explicit_scanned_ranges", Verification: "header_anchored_output_evidence; consensus_validated=false", Provenance: "Verified raw blocks or retained output facts", Retention: retention(def.ID), Queryable: view.Historical.State == "ready", Checkpoint: cp, Note: "Range output totals are not a current balance. Raw block retention is independent."})
	}
	return view, instances
}
func addressStateIndexCheckpoint(cp *addressStateCheckpoint, def indexDefinition) *indexCheckpoint {
	if cp == nil {
		return nil
	}
	return &indexCheckpoint{Schema: def.CheckpointSchema, Definition: def.ID, Version: def.Version, RuleHash: def.RuleHash, Network: def.Network, From: 0, Height: cp.Height, BlockHash: cp.BlockHash, Commitment: cp.Commitment}
}
func addressHistoryIndexCheckpoint(h addressHistoryHead, def indexDefinition) *indexCheckpoint {
	if h.Commitment == "" || h.Height < 0 || len(h.Coverage) == 0 {
		return nil
	}
	from := h.Height
	if len(h.Coverage) > 0 {
		from = h.Coverage[0].From
	}
	return &indexCheckpoint{Schema: def.CheckpointSchema, Definition: def.ID, Version: def.Version, RuleHash: def.RuleHash, Network: def.Network, From: from, Height: h.Height, BlockHash: h.BlockHash, Commitment: h.Commitment}
}
func addressIndexStoreHead(root string, s *indexStore) (*indexStore, error) {
	s.head.Mode, s.head.Retention = "lean", "ephemeral"
	if s.definition.ID == "address-state" {
		cp, err := readAddressStateContext(context.Background(), root)
		if errors.Is(err, ErrAddressStateMissing) {
			return s, nil
		}
		if err != nil {
			return nil, err
		}
		s.checkpoint = addressStateIndexCheckpoint(cp, s.definition)
	} else {
		h, err := openAddressHistoryStoreContext(context.Background(), root)
		if err != nil {
			return nil, err
		}
		s.checkpoint = addressHistoryIndexCheckpoint(h.head, s.definition)
	}
	if s.checkpoint != nil {
		s.head.Commitment = s.checkpoint.Commitment
	}
	return s, nil
}
func (a *app) planAddressIndexBuild(req indexBuildRequest) (indexPlan, error) {
	if err := requireReleaseIndexRequest(req); err != nil {
		return indexPlan{}, err
	}
	if len(req.Outputs) > 0 || req.RetainSatHistory || req.RetainOutputValues != nil || req.RetainOutputScripts != nil {
		return indexPlan{}, fmt.Errorf("address indexes use shared output facts; configure optional TXIO history separately")
	}
	if req.Index == "address-state" && (req.From != nil || req.To != nil) {
		return indexPlan{}, fmt.Errorf("Current UTXOs builds from the saved UTXO checkpoint; it has no historical range")
	}
	if req.Rebuild && req.Index != "address-state" {
		return indexPlan{}, fmt.Errorf("Historical outputs resumes explicit sparse ranges")
	}
	reqForPlan := req
	reqForPlan.Rebuild = false
	p, err := planIndex(reqForPlan)
	if err != nil {
		return p, err
	}
	if p.Mode != "lean" {
		return p, fmt.Errorf("address indexes use lean shared-fact records")
	}
	p.Rebuild = req.Rebuild
	output := indexPlanOutput{Index: req.Index, From: p.From, To: p.To, Mode: "lean", Reuse: []heightInterval{}, Add: []heightInterval{}}
	if req.Index == "address-state" {
		s, err := utxoStoreHead(a.dataDir)
		if err != nil {
			return p, err
		}
		if s.Head() == nil {
			return p, ErrAddressUTXORequired
		}
		cp := s.Head()
		if a.utxoSelectedChainState(context.Background(), cp, a.currentChainAuthority()) != "selected_chain" {
			return p, fmt.Errorf("resume UTXO processing before building addresses for its selected-chain checkpoint")
		}
		p.From, p.To = 0, cp.Height
		output.From, output.To = 0, cp.Height
		output.Reason = "Build the address reverse lookup from saved live UTXOs; no block scan and no implicit UTXO build."
		p.SourceBytes = "saved_UTXO_state"
		p.StorageEstimate = "Paged address membership pointing to existing output facts; current balances are saved per script."
	} else {
		s, e := openAddressHistoryStoreContext(context.Background(), a.dataDir)
		if e != nil {
			return p, e
		}
		output.Reuse = append([]heightInterval{}, s.head.Coverage...)
		if p.To >= 0 {
			output.Add = indexCoverageGaps(output.Reuse, p.From, p.To)
		}
		output.Reason = "Scan explicit sparse ranges, reusing retained output values/scripts and verified raw blocks. Address history pins facts after spend."
		p.SourceBytes = p.Retention
	}
	p.Verification = "header_anchored_processing; consensus_validated=false"
	p.Outputs = []indexPlanOutput{output}
	p.Notes = []string{output.Reason, "Current balance and sparse historical output totals are distinct. Inscription holdings wait for Sats."}
	if req.Index == "address-state" {
		p.Notes = append(p.Notes, "Queued work projects the latest complete saved UTXO checkpoint when it starts; it never starts a UTXO scan.")
	}
	if req.Index == "address-history" {
		p.Notes = append(p.Notes, "Source retention applies to raw blocks fetched for missing facts. Complete retained facts need no raw fetch; changing retention does not refill reused or previously scanned bodies.")
	}
	return p, nil
}
func (a *app) runAddressIndexBuild(ctx context.Context, j indexJob) {
	defer a.indexBuildFinished()
	finish := func(state string, err error) {
		if state == "failed" && (ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			state, err = "paused", nil
		}
		j.State, j.Stage, j.Error = state, "", ""
		if err != nil {
			j.Error = err.Error()
		}
		if e := a.saveIndexJob(j); e != nil {
			a.indexMu.Lock()
			j.State, j.Error = "failed", e.Error()
			a.indexJob = cloneIndexJob(j)
			a.indexMu.Unlock()
		}
		if j.Live {
			if state == "failed" {
				a.failLiveIndexRuntime(j.Index, fmt.Errorf("%s", j.Error))
			} else if state == "complete" {
				a.clearLiveIndexRuntime(j.Index)
			}
		}
	}
	if j.Index == "address-state" {
		authority := a.currentChainAuthority()
		u, err := utxoStoreHead(a.dataDir)
		if err != nil {
			finish("failed", err)
			return
		}
		if u.Head() == nil {
			finish("failed", ErrAddressUTXORequired)
			return
		}
		if a.utxoSelectedChainState(ctx, u.Head(), authority) != "selected_chain" {
			finish("failed", fmt.Errorf("UTXO source is not on the selected chain"))
			return
		}
		last := time.Time{}
		buildCtx, cancelBuild := context.WithCancel(ctx)
		defer cancelBuild()
		var progressErr error
		cp, err := buildAddressStateContext(buildCtx, a.dataDir, func(p addressStateBuildProgress) {
			if time.Since(last) < 150*time.Millisecond {
				return
			}
			last = time.Now()
			j.Stage = "building_address_lookup"
			j.BlocksChecked = int(p.OutputsChecked)
			j.Height = p.Height
			if progressErr = a.saveIndexJob(j); progressErr != nil {
				cancelBuild()
			}
		})
		if progressErr != nil {
			finish("failed", progressErr)
			return
		}
		if err != nil {
			if ctx.Err() != nil {
				finish("paused", nil)
			} else {
				finish("failed", err)
			}
			return
		}
		if a.utxoSelectedChainState(ctx, &cp.Source, authority) != "selected_chain" {
			finish("failed", fmt.Errorf("selected chain changed; resume UTXO state and rebuild addresses"))
			return
		}
		j.Height = cp.Height
		finish("complete", nil)
		return
	}
	store, err := openAddressHistoryStoreContext(ctx, a.dataDir)
	if err != nil {
		finish("failed", err)
		return
	}
	authority := a.currentChainAuthority()
	hashAt := a.addressSelectedHashes(ctx, authority)
	if err = store.ReconcileContext(ctx, hashAt); err != nil {
		finish("failed", err)
		return
	}
	if j.To < 0 {
		j.To = authority.Height
	}
	if j.To < j.From {
		finish("failed", fmt.Errorf("selected chain target is unavailable"))
		return
	}
	// Reconcile has authenticated the selected-chain coverage. Avoid a second
	// full history walk before an unchanged sparse build can resume.
	coverage := append([]heightInterval{}, store.head.Coverage...)
	missing := indexCoverageGaps(coverage, j.From, j.To)
	processed := int64(0)
	for _, gap := range missing {
		for height := gap.From; height <= gap.To; height++ {
			if ctx.Err() != nil || a.indexPauseRequested(j.ID) {
				finish("paused", nil)
				return
			}
			if j.Live && processed >= indexLiveBatchBlocks {
				finish("complete", nil)
				return
			}
			hash, err := hashAt(height)
			if err != nil {
				finish("failed", err)
				return
			}
			reused, err := store.CommitTXIOFactsContext(ctx, height, hash, hashAt)
			if err != nil {
				finish("failed", err)
				return
			}
			if !reused {
				target, err := a.utxoSelectedTarget(ctx, authority, height)
				if err != nil {
					finish("failed", err)
					return
				}
				block, err := a.indexSourceBlock(ctx, target, j.Retention)
				if err != nil {
					if ctx.Err() != nil {
						finish("paused", nil)
					} else {
						finish("failed", err)
					}
					return
				}
				if err = store.CommitBlockContext(ctx, block, hashAt); err != nil {
					if ctx.Err() != nil {
						finish("paused", nil)
					} else {
						finish("failed", err)
					}
					return
				}
			}
			processed++
			j.Height = height
			j.BlocksChecked++
			if err = a.saveIndexJob(j); err != nil {
				finish("failed", err)
				return
			}
		}
	}
	j.Height = j.To
	finish("complete", nil)
}
