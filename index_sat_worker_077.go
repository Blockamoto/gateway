package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func satTerminalError077(err error) bool {
	return errors.Is(err, ErrSatRebuildRequired) || errors.Is(err, ErrSatStateCorrupt) || errors.Is(err, ErrSatProfileIncompatible)
}

type satPublicationAnchorError077 struct{ error }

// The final anchor check runs after derivation, immediately before publication.
// A replaced or temporarily unavailable chain leaves the prior head intact.
func appendSatSelectedBlock077(ctx context.Context, s *indexStore, block blockView, from int64, retention string, prepared *inscriptionBlockResult, hashAt func(int64) (string, error)) error {
	prior := s.satCommitHook
	s.satCommitHook = func(stage string) error {
		if prior != nil {
			if err := prior(stage); err != nil {
				return err
			}
		}
		if stage != "before_head" {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		hash, err := hashAt(block.Height)
		if err == nil && hash != block.Hash {
			err = fmt.Errorf("selected Sat block changed during derivation")
		}
		if err != nil {
			return satPublicationAnchorError077{err}
		}
		return nil
	}
	defer func() { s.satCommitHook = prior }()
	return s.appendPreparedBlock(block, from, retention, prepared)
}

func (a *app) satChainState077(ctx context.Context, cp *indexCheckpoint, authority chainAuthorityView) string {
	if cp == nil {
		return "empty"
	}
	return a.utxoSelectedChainState(ctx, &utxoCheckpoint{Height: cp.Height, BlockHash: cp.BlockHash}, authority)
}
func (a *app) planSatIndexBuild077(req indexBuildRequest) (indexPlan, error) {
	if err := requireReleaseIndexRequest(req); err != nil {
		return indexPlan{}, err
	}
	if len(req.Outputs) > 0 || req.RetainOutputValues != nil || req.RetainOutputScripts != nil {
		return indexPlan{}, fmt.Errorf("Sat state retains mandatory shared facts; optional TXIO history is separate")
	}
	if req.From != nil && *req.From != 0 {
		return indexPlan{}, fmt.Errorf("Sat state resumes its genesis checkpoint; arbitrary starts are unsupported")
	}
	p, err := planIndex(req)
	if err != nil {
		return p, err
	}
	out := indexPlanOutput{Index: "sat-state", From: 0, To: p.To, Mode: "lean", Reuse: []heightInterval{}, Add: []heightInterval{}, Reason: "Process FIFO identities from genesis using local blocks before requesting missing bytes."}
	if req.Rebuild {
		out.Recovery = "explicit_genesis_rebuild"
		out.Reason = "Archive the prior Sat state and rebuild from genesis. Resume never performs this reset."
		p.Notes = append(p.Notes, "Reviewed rebuild preserves previous state and shared facts; existing UTXO, TXIO, Addresses and locator indexes remain separate.")
	} else {
		s, err := indexStoreHead(a.dataDir, "sat-state")
		if err != nil {
			return p, err
		}
		if s.checkpoint != nil {
			b, err := s.readBatch(s.checkpoint.Commitment)
			if err != nil {
				return p, err
			}
			if b.Sats == nil || b.Sats.Profile != satIndexProfile {
				return p, ErrSatProfileIncompatible
			}
			if err = s.validateSatSavepoints077(context.Background()); err != nil {
				return p, err
			}
			if !req.SatHistoryConfigured {
				p.RetainSatHistory = b.Sats.RetainHistory
			}
			switch a.satChainState077(context.Background(), s.checkpoint, a.currentChainAuthority()) {
			case "selected_chain":
				if p.To >= 0 && p.To < s.checkpoint.Height {
					return p, fmt.Errorf("Sat checkpoint is already at block %d; choose that tip or a later target", s.checkpoint.Height)
				}
				out.Reuse = []heightInterval{{0, s.checkpoint.Height}}
				out.Reason = "Resume the exact saved Sat state; only later blocks require FIFO processing."
			case "stale_reorg_resume_required":
				out.Recovery = "reconcile_reorg"
				out.Reason = "Restore a retained selected-chain savepoint at or before the common ancestor, then replay. Beyond saved recovery requires reviewed rebuild."
			default:
				out.Recovery = "verify_anchor"
				out.Reason = "Selected-chain evidence is unavailable. Saved state stays intact; no reset or replay is inferred."
			}
		}
	}
	if !req.SatHistoryConfigured {
		if policies, e := a.indexLivePolicies(); e != nil {
			return p, e
		} else if policy := policies[req.Index]; policy.SatHistoryConfigured {
			p.RetainSatHistory = policy.RetainSatHistory
		}
	}
	if p.To >= 0 {
		out.Add = indexCoverageGaps(out.Reuse, 0, p.To)
	}
	p.Outputs = []indexPlanOutput{out}
	p.Verification = "header_anchored_processing; consensus_validated=false"
	return p, nil
}
func (a *app) runSatIndexBuild077(ctx context.Context, j indexJob) {
	defer a.indexBuildFinished()
	finish := func(state string, err error) {
		if ctx.Err() != nil {
			state, err = "paused", nil
		}
		j.State, j.Stage, j.Error = state, "", ""
		if err != nil {
			j.Error = err.Error()
		}
		for i := range j.Progress {
			j.Progress[i].State = state
			j.Progress[i].Error = j.Error
		}
		if e := a.saveIndexJob(j); e != nil {
			a.indexMu.Lock()
			j.State, j.Error = "failed", e.Error()
			a.indexJob = cloneIndexJob(j)
			a.indexMu.Unlock()
		}
		if j.Live {
			if j.State == "failed" || j.State == "rebuild_required" {
				a.failLiveIndexRuntime(j.Index, fmt.Errorf("%s", j.Error))
			} else if j.State == "complete" || j.State == "yielded" {
				a.clearLiveIndexRuntime(j.Index)
			}
		}
	}
	if j.Rebuild {
		if err := resetSatStateForRebuildContext(ctx, a.dataDir, j.ID); err != nil {
			finish("failed", err)
			return
		}
	}
	s, err := openIndexStoreContext(ctx, a.dataDir, "sat-state")
	if err != nil {
		if satTerminalError077(err) {
			finish("rebuild_required", err)
		} else {
			finish("failed", err)
		}
		return
	}
	s.head.Mode = "lean"
	s.retainSatHistory = j.RetainSatHistory
	j.Progress = []indexOutputProgress{{Index: "sat-state", State: "running", From: 0, To: j.To, Height: -1}}
	if s.checkpoint != nil {
		j.Height = s.checkpoint.Height
		j.Progress[0].Height = j.Height
	}
	fixedTarget := j.To >= 0 && !j.Live
	processed := int64(0)
	wait := func(err error) bool {
		if ctx.Err() != nil || a.indexPauseRequested(j.ID) {
			finish("paused", nil)
			return false
		}
		if satTerminalError077(err) {
			finish("rebuild_required", err)
			return false
		}
		j.State, j.Error = "waiting", err.Error()
		j.WaitingReason = err.Error()
		for i := range j.Progress {
			j.Progress[i].State, j.Progress[i].Error = "waiting", err.Error()
		}
		if e := a.saveIndexJob(j); e != nil {
			finish("failed", e)
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
	maintain := func(shared bool) error {
		j.Stage = "maintenance"
		if err := a.saveIndexJob(j); err != nil {
			return err
		}
		defer func() { j.Stage = "" }()
		deadline := time.Now().Add(2 * time.Second)
		for {
			err := s.collectOrdinalState()
			if !errors.Is(err, errOrdinalMaintenancePending) {
				if err != nil {
					return err
				}
				break
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if time.Now().After(deadline) {
				break
			}
		}
		if shared {
			err := collectSharedOutputFactsContext(ctx, a.dataDir)
			if errors.Is(err, ErrSatArchivePinsUnavailable) {
				if len(j.Limitations) < 8 {
					j.Limitations = append(j.Limitations, err.Error())
				}
				return nil // preserve all dependencies; only cleanup is deferred
			}
			return err
		}
		return nil
	}
	for {
		if !indexRunOptionalWorkAllowed(a.dataDir, j.Index) {
			finish("paused", nil)
			return
		}
		if ctx.Err() != nil || a.indexPauseRequested(j.ID) {
			finish("paused", nil)
			return
		}
		authority := a.currentChainAuthority()
		if authority.Height < 0 || j.To < 0 && authority.Source == "bod_headers" && a.getStatus().Syncing {
			a.needHeaders(0)
			if !wait(fmt.Errorf("waiting for the selected Sat header tip")) {
				return
			}
			continue
		}
		hashAt := a.addressSelectedHashes(ctx, authority)
		if err = s.reconcileSatStateSelected(ctx, authority.Height, hashAt); err != nil {
			if !wait(err) {
				return
			}
			continue
		}
		if j.To < 0 {
			j.To = authority.Height
		}
		if !fixedTarget && j.To > authority.Height {
			j.To = authority.Height
		}
		j.Progress[0].To = j.To
		j.Height = -1
		if s.checkpoint != nil {
			j.Height = s.checkpoint.Height
		}
		j.Progress[0].Height = j.Height
		if j.Height >= j.To {
			if err = maintain(true); err != nil {
				if satTerminalError077(err) {
					finish("rebuild_required", err)
				} else {
					finish("failed", err)
				}
				return
			}
			finish("complete", nil)
			return
		}
		if j.Live && processed >= indexLiveBatchBlocks {
			if err = maintain(false); err != nil {
				finish("failed", err)
				return
			}
			finish("yielded", nil)
			return
		}
		next := j.Height + 1
		target, err := a.utxoSelectedTarget(ctx, authority, next)
		if err != nil {
			if !wait(err) {
				return
			}
			continue
		}
		if j.State == "waiting" {
			j.State, j.Error, j.WaitingReason = "running", "", ""
			for i := range j.Progress {
				j.Progress[i].State, j.Progress[i].Error = "running", ""
			}
			if err = a.saveIndexJob(j); err != nil {
				finish("failed", err)
				return
			}
		}
		block, err := a.indexSourceBlock(ctx, target, j.Retention)
		if err != nil {
			var unavailable blockUnavailableError
			if !errors.As(err, &unavailable) {
				finish("failed", err)
				return
			}
			if !wait(err) {
				return
			}
			continue
		}
		current, err := hashAt(next)
		if err != nil {
			if !wait(err) {
				return
			}
			continue
		}
		if current != block.Hash {
			if !wait(fmt.Errorf("selected Sat block changed before publication")) {
				return
			}
			continue
		}
		if err = appendSatSelectedBlock077(ctx, s, block, 0, j.Retention, nil, hashAt); err != nil {
			var changed satPublicationAnchorError077
			if errors.As(err, &changed) {
				if !wait(err) {
					return
				}
				continue
			}
			if satTerminalError077(err) {
				finish("rebuild_required", err)
			} else {
				finish("failed", err)
			}
			return
		}
		processed++
		j.State, j.Error = "running", ""
		j.WaitingReason = ""
		j.Height = next
		j.BlocksChecked++
		j.Progress[0].State = "running"
		j.Progress[0].Height = next
		j.Progress[0].AddedBlocks++
		if err = a.saveIndexJob(j); err != nil {
			finish("failed", err)
			return
		}
		if processed%128 == 0 {
			if err = maintain(false); err != nil {
				finish("failed", err)
				return
			}
		}
	}
}
