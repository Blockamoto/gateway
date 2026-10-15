package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func optionalAddressProjectionError(err error) bool {
	return !errors.Is(err, ErrUTXOStateCorrupt) && !errors.Is(err, ErrUTXORebuildRequired) && (errors.Is(err, ErrAddressStateStale) || errors.Is(err, ErrAddressStateCorrupt))
}

// The UTXO engine has one atomic state head, not an immutable historical batch
// for every processed block. This projection lets the existing scheduler share
// its single writer without interpreting a UTXO checkpoint as a block batch.
func utxoIndexStoreHead(root string, projection *indexStore) (*indexStore, error) {
	s, err := utxoStoreHead(root)
	if err != nil {
		return nil, err
	}
	projection.head.Mode, projection.head.Retention = "lean", "pruned"
	if cp := s.Head(); cp != nil {
		projection.checkpoint = utxoIndexCheckpoint(cp, projection.definition)
		projection.head.Commitment = cp.Commitment
	}
	return projection, nil
}

func utxoIndexCheckpoint(cp *utxoCheckpoint, def indexDefinition) *indexCheckpoint {
	if cp == nil {
		return nil
	}
	return &indexCheckpoint{Schema: def.CheckpointSchema, Definition: def.ID, Version: def.Version, RuleHash: def.RuleHash, Network: def.Network, From: 0, Height: cp.Height, BlockHash: cp.BlockHash, PreviousCommitment: cp.PreviousCommitment, Commitment: cp.Commitment}
}

// A reconciliation uses one captured selected-chain provider throughout. In
// particular, a shorter Core active chain must not fall back to an older local
// header mirror above its tip. Missing evidence is never a disconnected block.
func (a *app) utxoSelectedTarget(ctx context.Context, authority chainAuthorityView, height int64) (blockTarget, error) {
	if err := ctx.Err(); err != nil {
		return blockTarget{}, err
	}
	if authority.Height < 0 || !validHash(authority.Hash) || height < 0 || height > authority.Height {
		return blockTarget{}, fmt.Errorf("selected UTXO chain anchor unavailable at %d", height)
	}
	var target blockTarget
	var err error
	switch authority.Source {
	case "bitcoin_core":
		if height == authority.Height {
			target, err = a.coreTipTargetContext(ctx)
		} else {
			target, err = a.coreTargetByHeightContext(ctx, height)
		}
	case "bod_headers":
		if height == authority.Height {
			status := a.getStatus()
			if status.HeaderHeight != authority.Height || status.HeaderCount <= authority.Height {
				return blockTarget{}, fmt.Errorf("selected UTXO header tip changed")
			}
		}
		target, err = a.localBlockTarget(height)
	default:
		return blockTarget{}, fmt.Errorf("selected UTXO chain provider unavailable")
	}
	if err != nil {
		return blockTarget{}, err
	}
	if height == authority.Height && (target.Height != authority.Height || target.HashDisplay != authority.Hash) {
		return blockTarget{}, fmt.Errorf("selected UTXO chain tip changed")
	}
	return target, nil
}

func (a *app) utxoSelectedChainState(ctx context.Context, cp *utxoCheckpoint, authority chainAuthorityView) string {
	if cp == nil {
		return "empty"
	}
	if authority.Height < 0 || !validHash(authority.Hash) {
		return "anchor_unavailable"
	}
	// Authenticate the known selected tip before interpreting its height bound.
	if _, err := a.utxoSelectedTarget(ctx, authority, authority.Height); err != nil {
		return "anchor_unavailable"
	}
	if cp.Height > authority.Height {
		return "stale_reorg_resume_required"
	}
	target, err := a.utxoSelectedTarget(ctx, authority, cp.Height)
	if err != nil {
		return "anchor_unavailable"
	}
	if target.HashDisplay != cp.BlockHash {
		return "stale_reorg_resume_required"
	}
	return "selected_chain"
}

func (a *app) planUTXOIndexBuild(req indexBuildRequest) (indexPlan, error) {
	if err := requireReleaseIndexRequest(req); err != nil {
		return indexPlan{}, err
	}
	if len(req.Outputs) != 0 || req.RetainOutputValues != nil || req.RetainOutputScripts != nil || req.RetainSatHistory {
		return indexPlan{}, fmt.Errorf("UTXO retains mandatory live coin and recovery facts; optional historical layers are configured on TXIO")
	}
	p, err := planIndex(req)
	if err != nil {
		return p, err
	}
	output := indexPlanOutput{Index: req.Index, From: 0, To: p.To, Mode: "lean", Reuse: []heightInterval{}, Add: []heightInterval{}, Reason: "Process the selected chain from genesis, reusing locally available blocks and fetching only missing bodies."}
	if req.Rebuild {
		output.Recovery, output.Reason = "explicit_genesis_rebuild", "Archive the previous state, preserving retained data, and rebuild a new complete state from genesis. Normal Resume never performs this reset."
		if p.To >= 0 {
			output.Add = []heightInterval{{0, p.To}}
		}
		p.Outputs = []indexPlanOutput{output}
		p.Notes = append(p.Notes, "This reviewed rebuild starts from genesis and preserves prior state data. Recovery-window settings remain fixed.")
		return p, nil
	}
	s, err := utxoStoreHead(a.dataDir)
	if err != nil {
		return p, err
	}
	if cp := s.Head(); cp != nil {
		switch a.utxoSelectedChainState(context.Background(), cp, a.currentChainAuthority()) {
		case "selected_chain":
			end := cp.Height
			if output.To >= 0 && output.To < end {
				return p, fmt.Errorf("UTXO checkpoint is already at block %d; choose that tip or a later target", cp.Height)
			}
			output.Reuse = []heightInterval{{From: 0, To: end}}
			output.Reason = "Resume the atomic live state; only blocks after its checkpoint need processing."
		case "stale_reorg_resume_required":
			output.Recovery = "reconcile_reorg"
			output.Reason = "Rewind using locally pinned undo to the common ancestor, then process the replacement branch. Beyond the retained boundary, rebuild is required."
		default:
			output.Recovery = "verify_anchor"
			output.Reason = "The selected-chain anchor is unavailable. Saved state remains intact and processing waits for it."
		}
	}
	if output.To >= 0 {
		output.Add = indexCoverageGaps(output.Reuse, 0, output.To)
	}
	p.Outputs = []indexPlanOutput{output}
	return p, nil
}

type indexUTXOStatusView struct {
	State                  string          `json:"state"`
	Stage                  string          `json:"stage,omitempty"`
	ProcessedHeight        *int64          `json:"processed_height"`
	ProcessedHash          string          `json:"processed_hash,omitempty"`
	TargetHeight           *int64          `json:"target_height"`
	TargetHash             string          `json:"target_hash,omitempty"`
	LiveOutputCount        *uint64         `json:"live_output_count"`
	LiveValueSats          *uint64         `json:"live_value_sats"`
	StorageBytes           *int64          `json:"storage_bytes"`
	StorageMeasuredAt      string          `json:"storage_measured_at,omitempty"`
	StorageCheckpoint      string          `json:"storage_checkpoint,omitempty"`
	StorageRevision        string          `json:"storage_revision,omitempty"`
	RecoveryWindow         int             `json:"recovery_window"`
	RecoveryTransitions    int             `json:"recovery_transitions"`
	RecoveryBoundaryHeight *int64          `json:"recovery_boundary_height"`
	RecoveryBoundaryHash   string          `json:"recovery_boundary_hash,omitempty"`
	WaitingHeight          *int64          `json:"waiting_height"`
	Checkpoint             *utxoCheckpoint `json:"checkpoint,omitempty"`
	ConsensusValidated     bool            `json:"consensus_validated"`
	Validation             string          `json:"validation"`
	ChainState             string          `json:"chain_state"`
	Note                   string          `json:"note"`
	Error                  string          `json:"error,omitempty"`
}

func (a *app) indexUTXOStatus(jobs []indexJob) *indexUTXOStatusView {
	view := &indexUTXOStatusView{State: "ready", RecoveryWindow: utxoRecoveryTransitions, Validation: "header_anchored_processing", ChainState: "empty", Note: "Build from genesis. Full consensus validation is not performed in this release; raw source retention is independent of the recovery window."}
	authority := a.currentChainAuthority()
	if authority.Height >= 0 && validHash(authority.Hash) {
		h := authority.Height
		view.TargetHeight, view.TargetHash = &h, authority.Hash
	}
	s, err := utxoStoreHead(a.dataDir)
	if err != nil {
		view.State, view.Error = "rebuild_required", err.Error()
		return view
	}
	if cp := s.Head(); cp != nil {
		status := s.Status()
		h, count, value, boundary := cp.Height, uint64(status.LiveOutputCount), uint64(status.LiveValueSats), cp.RecoveryBoundaryHeight
		view.ProcessedHeight, view.ProcessedHash = &h, cp.BlockHash
		view.LiveOutputCount, view.LiveValueSats = &count, &value
		view.RecoveryTransitions = int(status.RecoveryTransitions)
		view.RecoveryBoundaryHeight, view.RecoveryBoundaryHash = &boundary, cp.RecoveryBoundaryHash
		view.Checkpoint = cp
		if measurement, ok := cachedUTXOStorage(a.dataDir, s.head.Checksum); ok {
			total := measurement.TotalBytes
			view.StorageBytes, view.StorageMeasuredAt, view.StorageCheckpoint = &total, measurement.MeasuredAt, measurement.Checkpoint
			view.StorageRevision = measurement.Revision
		}
		view.ChainState = a.utxoSelectedChainState(context.Background(), cp, authority)
		if view.ChainState == "stale_reorg_resume_required" {
			view.State = "reconciling"
		} else if view.ChainState == "anchor_unavailable" {
			view.State = "waiting"
		} else if view.TargetHeight != nil && h == *view.TargetHeight && cp.BlockHash == view.TargetHash {
			view.State = "current"
		} else {
			view.State = "paused"
		}
	}
	for i := len(jobs) - 1; i >= 0; i-- {
		job := jobs[i]
		if job.Index != "utxo-state" {
			continue
		}
		view.Stage = job.Stage
		if job.Stage == "maintenance" {
			view.Note = "Reclaiming obsolete state pages at a committed boundary. Pause preserves the current checkpoint and stops maintenance."
		}
		if job.To >= 0 && (job.State == "running" || job.State == "waiting" || job.State == "pausing" || job.State == "queued") {
			h := job.To
			view.TargetHeight, view.TargetHash = &h, ""
			if target, err := a.indexTarget(h); err == nil {
				view.TargetHash = target.HashDisplay
			}
		}
		switch job.State {
		case "running", "queued":
			view.State = "building"
		case "waiting":
			view.State, view.Error = "waiting", job.Error
			h := job.Height + 1
			view.WaitingHeight = &h
		case "paused", "pausing", "cancelled":
			view.State = "paused"
		case "rebuild_required":
			view.State, view.Error = "rebuild_required", job.Error
		case "failed":
			view.State, view.Error = "paused", job.Error
		}
		break
	}
	if runtime := a.liveIndexRuntime("utxo-state"); runtime.State == "waiting" && view.State != "building" && view.State != "rebuild_required" {
		view.State, view.Error = "waiting", runtime.Error
		h := int64(0)
		if view.ProcessedHeight != nil {
			h = *view.ProcessedHeight + 1
		}
		view.WaitingHeight = &h
	}
	return view
}

func (a *app) runUTXOIndexBuild(ctx context.Context, j indexJob) {
	defer a.indexBuildFinished()
	var maintain func(forceShared bool) error
	finish := func(state string, err error) {
		if state == "complete" && maintain != nil {
			if maintenanceErr := maintain(!j.Live); maintenanceErr != nil {
				var limitation string
				state, err, limitation = utxoMaintenanceCompletion(maintenanceErr, ctx.Err())
				if limitation != "" && len(j.Limitations) < 8 {
					j.Limitations = append(j.Limitations, limitation)
				}
			}
		}
		j.Stage = ""
		j.State, j.Error = state, ""
		if err != nil {
			j.Error = err.Error()
		}
		if err := a.saveIndexJob(j); err != nil {
			a.indexMu.Lock()
			j.State, j.Error = "failed", "Cannot persist UTXO job status: "+err.Error()
			a.indexJob = cloneIndexJob(j)
			a.indexMu.Unlock()
		}
		if j.Live {
			if state == "failed" || state == "rebuild_required" {
				a.failLiveIndexRuntime(j.Index, fmt.Errorf("%s", j.Error))
			} else if state == "complete" {
				a.clearLiveIndexRuntime(j.Index)
			}
		}
	}
	wait := func(err error) bool {
		if ctx.Err() != nil || a.indexPauseRequested(j.ID) {
			finish("paused", nil)
			return false
		}
		j.State, j.Error = "waiting", err.Error()
		if err := a.saveIndexJob(j); err != nil {
			finish("failed", err)
			return false
		}
		if j.Live {
			a.deferLiveIndexRetry(j.Index, err)
			return false
		}
		select {
		case <-ctx.Done():
			finish("paused", nil)
			return false
		case <-time.After(2 * time.Second):
			return true
		}
	}
	if j.Rebuild {
		if err := resetUTXOForRebuild(ctx, a.dataDir, j.ID); err != nil {
			finish("failed", err)
			return
		}
	}
	s, err := openUTXOStoreContext(ctx, a.dataDir)
	if err != nil {
		if ctx.Err() != nil {
			finish("paused", nil)
		} else {
			finish("failed", err)
		}
		return
	}
	maintain = func(forceShared bool) error {
		j.State, j.Stage, j.Error = "running", "maintenance", ""
		if err := a.saveIndexJob(j); err != nil {
			return err
		}
		defer func() { j.Stage = "" }()
		allowance := time.Now().Add(2 * time.Second)
		for ctx.Err() == nil {
			if err := s.CollectPages(ctx); err != nil {
				return err
			}
			if !ordinalCollectionPending(s.pages()) {
				break
			}
			if time.Now().After(allowance) {
				break
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if forceShared || utxoSharedMaintenanceDue(s) {
			// A canceled incomplete mark never sweeps. The complete pass uses
			// the job context so a large retained history can make progress,
			// while Pause remains immediately available to the user.
			if err := collectSharedOutputFactsContext(ctx, a.dataDir); err != nil {
				return err
			}
			return recordUTXOSharedMaintenance(s)
		}
		return nil
	}
	processed := int64(0)
	for {
		if !indexRunOptionalWorkAllowed(a.dataDir, j.Index) {
			finish("paused", nil)
			return
		}
		if ctx.Err() != nil || a.indexPauseRequested(j.ID) {
			finish("paused", nil)
			return
		}
		if j.To < 0 {
			authority := a.currentChainAuthority()
			if authority.Height < 0 || authority.Source == "bod_headers" && a.getStatus().Syncing {
				a.needHeaders(0)
				if !wait(fmt.Errorf("waiting for the selected header tip")) {
					return
				}
				continue
			}
			j.To = authority.Height
		}
		authority := a.currentChainAuthority()
		if err = s.ReconcileSelected(ctx, authority.Height, func(ctx context.Context, h int64) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			t, err := a.utxoSelectedTarget(ctx, authority, h)
			return t.HashDisplay, err
		}); err != nil {
			if ctx.Err() == nil && optionalAddressProjectionError(err) {
				// Reconcile completed the authoritative source rewind. A damaged
				// optional address projection cannot strand UTXO processing.
				if len(j.Limitations) < 8 {
					j.Limitations = append(j.Limitations, "Current addresses require repair: "+err.Error())
				}
			} else {
				if errors.Is(err, ErrUTXORebuildRequired) || errors.Is(err, ErrUTXOStateCorrupt) {
					finish("rebuild_required", err)
					return
				}
				if !wait(err) {
					return
				}
				continue
			}
		}
		// A reorganization can select a higher-work branch with fewer blocks.
		if err := resumeAddressStateContext(ctx, a.dataDir); err != nil {
			if ctx.Err() != nil {
				finish("paused", nil)
				return
			}
			if !optionalAddressProjectionError(err) {
				state := "failed"
				if errors.Is(err, ErrUTXOStateCorrupt) || errors.Is(err, ErrUTXORebuildRequired) {
					state = "rebuild_required"
				}
				finish(state, err)
				return
			}
			// The UTXO commit is authoritative; a failed reverse projection must
			// not claim that the source commit was rolled back.
			if len(j.Limitations) < 8 {
				j.Limitations = append(j.Limitations, "Current addresses require repair: "+err.Error())
			}
		}
		// Its authenticated tip replaces a target that no longer exists.
		if authority.Height >= 0 && j.To > authority.Height {
			j.To = authority.Height
		}
		next := int64(0)
		if cp := s.Head(); cp != nil {
			next = cp.Height + 1
		}
		j.Height, j.BlocksChecked = next-1, int(next)
		if next > j.To || j.Live && processed >= indexLiveBatchBlocks {
			finish("complete", nil)
			return
		}
		target, err := a.utxoSelectedTarget(ctx, authority, next)
		if err != nil {
			if !wait(fmt.Errorf("waiting for selected header at block %d: %w", next, err)) {
				return
			}
			continue
		}
		j.State, j.Error = "running", ""
		if err = a.saveIndexJob(j); err != nil {
			finish("failed", err)
			return
		}
		block, err := a.indexSourceBlock(ctx, target, j.Retention)
		if err != nil {
			if ctx.Err() != nil {
				finish("paused", nil)
				return
			}
			var unavailable blockUnavailableError
			if !errors.As(err, &unavailable) {
				finish("failed", err)
				return
			}
			if !wait(fmt.Errorf("waiting for block %d: %w", next, err)) {
				return
			}
			continue
		}
		current, err := a.utxoSelectedTarget(ctx, authority, next)
		if err != nil {
			if !wait(err) {
				return
			}
			continue
		}
		if current.HashDisplay != block.Hash {
			continue
		}
		if _, err := a.utxoSelectedTarget(ctx, authority, authority.Height); err != nil {
			if !wait(err) {
				return
			}
			continue
		}
		if err = s.ApplyBlock(ctx, block); err != nil {
			if optionalAddressProjectionError(err) {
				if len(j.Limitations) < 8 {
					j.Limitations = append(j.Limitations, "UTXO committed; current addresses require repair: "+err.Error())
				}
			} else {
				if ctx.Err() != nil {
					finish("paused", nil)
				} else {
					finish("failed", err)
				}
				return
			}
		}
		processed++
		j.Height, j.BlocksChecked = next, int(next+1)
		if err = a.saveIndexJob(j); err != nil {
			finish("failed", err)
			return
		}
		if !j.Live && processed%indexLiveBatchBlocks == 0 {
			// Long genesis builds also need bounded page reclamation. Pause and
			// cancellation retain the committed head even if maintenance stops.
			if err := maintain(false); err != nil {
				if ctx.Err() != nil {
					finish("paused", nil)
					return
				}
				if len(j.Limitations) < 8 {
					j.Limitations = append(j.Limitations, "Reclamation deferred: "+err.Error())
				}
			}
		}
	}
}

func utxoMaintenanceCompletion(maintenanceErr, contextErr error) (string, error, string) {
	// A reclamation problem can be deferred only while committed state remains
	// usable. Typed corruption must never leave a successful completed job.
	if errors.Is(maintenanceErr, ErrUTXOStateCorrupt) || errors.Is(maintenanceErr, ErrUTXORebuildRequired) {
		return "rebuild_required", maintenanceErr, ""
	}
	if contextErr != nil {
		return "paused", nil, ""
	}
	return "complete", nil, "Reclamation deferred: " + maintenanceErr.Error()
}

func (a *app) queryUTXOIndex(ctx context.Context, limit int) (any, error) {
	s, err := openUTXOStoreContext(ctx, a.dataDir)
	if err != nil {
		return nil, err
	}
	chainState := a.utxoSelectedChainState(ctx, s.Head(), a.currentChainAuthority())
	if s.Head() != nil && chainState != "selected_chain" {
		return nil, fmt.Errorf("UTXO snapshot cannot currently be anchored to the selected chain")
	}
	rows, err := s.ListCoins(ctx, limit)
	if err != nil {
		return nil, err
	}
	return struct {
		Checkpoint         *utxoCheckpoint `json:"checkpoint"`
		Rows               []utxoCoin      `json:"rows"`
		ConsensusValidated bool            `json:"consensus_validated"`
		State              utxoStatus      `json:"state"`
		ChainState         string          `json:"chain_state"`
	}{Checkpoint: s.Head(), Rows: rows, State: s.Status(), ChainState: chainState}, nil
}

func (a *app) lookupUTXOIndex(ctx context.Context, key string) (any, error) {
	s, err := openUTXOStoreContext(ctx, a.dataDir)
	if err != nil {
		return nil, err
	}
	var coin utxoCoin
	var found bool
	if parts := strings.Split(key, ":"); len(parts) == 2 && validHash(parts[0]) {
		vout, parseErr := strconv.ParseUint(parts[1], 10, 32)
		if parseErr != nil || strconv.FormatUint(vout, 10) != parts[1] {
			return nil, fmt.Errorf("invalid output number")
		}
		coin, found, err = s.LookupOutpoint(ctx, strings.ToLower(parts[0]), uint32(vout))
	} else {
		coin, found, err = s.LookupBPS(ctx, key)
	}
	if err != nil {
		return nil, err
	}
	chainState := a.utxoSelectedChainState(ctx, s.Head(), a.currentChainAuthority())
	if chainState != "selected_chain" {
		return nil, fmt.Errorf("UTXO snapshot cannot currently be anchored to the selected chain")
	}
	return struct {
		Found              bool            `json:"found"`
		Coin               *utxoCoin       `json:"coin,omitempty"`
		Checkpoint         *utxoCheckpoint `json:"checkpoint"`
		ChainState         string          `json:"chain_state"`
		ConsensusValidated bool            `json:"consensus_validated"`
	}{Found: found, Coin: func() *utxoCoin {
		if found {
			return &coin
		}
		return nil
	}(), Checkpoint: s.Head(), ChainState: chainState}, nil
}
