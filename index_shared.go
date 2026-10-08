package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

type indexPlanOutput struct {
	Index    string           `json:"index"`
	From     int64            `json:"from"`
	To       int64            `json:"to"`
	Reuse    []heightInterval `json:"reuse"`
	Add      []heightInterval `json:"add"`
	Recovery string           `json:"recovery,omitempty"`
	Reason   string           `json:"reason"`
	Mode     string           `json:"mode"`
}
type indexOutputProgress struct {
	Limitations  []string `json:"limitations,omitempty"`
	Index        string   `json:"index"`
	State        string   `json:"state"`
	From         int64    `json:"from"`
	To           int64    `json:"to"`
	Height       int64    `json:"height"`
	Error        string   `json:"error,omitempty"`
	ReusedBlocks int64    `json:"reused_blocks"`
	AddedBlocks  int64    `json:"added_blocks"`
}

func (a *app) sharedLiveNeedsWork(outputs []string, tip int64) bool {
	for _, id := range outputs {
		s, err := indexStoreHead(a.dataDir, id)
		if err != nil || s.checkpoint == nil || s.checkpoint.Height < tip || a.indexChainState(s.checkpoint) != "selected_chain" {
			return true
		}
	}
	return false
}

func normalizedIndexOutputs(req indexBuildRequest) []string {
	seen := map[string]bool{req.Index: true}
	var ids []string
	for _, id := range req.Outputs {
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	sort.Strings(ids)
	return ids
}

// Planning examines only local manifests and current chain authority. It never
// downloads a block, enables an index, or repairs incompatible stores.
func (a *app) planIndexBuild(req indexBuildRequest) (indexPlan, error) {
	if err := requireReleaseIndexRequest(req); err != nil {
		return indexPlan{}, err
	}
	if len(req.Outputs) > 0 && req.Index != "blocks" {
		return indexPlan{}, fmt.Errorf("combined outputs belong to the Bitcoin blocks plan")
	}
	s, err := indexStoreHead(a.dataDir, req.Index)
	if err != nil {
		return indexPlan{}, err
	}
	if s.checkpoint != nil {
		if req.From == nil || (*req.From >= s.checkpoint.From && *req.From <= s.checkpoint.Height+1) {
			from := s.checkpoint.From
			req.From = &from
		}
		if req.From != nil && *req.From != s.checkpoint.From {
			return indexPlan{}, fmt.Errorf("%s has contiguous coverage from %d; resume that range instead of overwriting existing data", req.Index, s.checkpoint.From)
		}
		mode := s.head.Mode
		if mode == "" {
			mode = "full"
		}
		if req.Mode != "" && req.Mode != mode {
			return indexPlan{}, fmt.Errorf("%s uses %s mode; compatible work can resume in that mode", req.Index, mode)
		}
		req.Mode = mode
	}
	p, err := planIndex(req)
	if err != nil {
		return p, err
	}
	if !p.Definition.Buildable {
		return p, fmt.Errorf("%s has no derivation executor", req.Index)
	}
	ids := append([]string{req.Index}, normalizedIndexOutputs(req)...)
	for _, id := range ids {
		if id != req.Index && id != "sat-state" && id != "tx-locator" && id != "inscriptions" && id != "bitmap" && id != "txo-spender" {
			return p, fmt.Errorf("unsupported combined output %q", id)
		}
		store, e := indexStoreHead(a.dataDir, id)
		if e != nil {
			return p, fmt.Errorf("%s cannot reuse its saved rules/state: %w", id, e)
		}
		if id == "sat-state" && !req.SatHistoryConfigured && store.checkpoint != nil {
			batch, e := store.readBatch(store.checkpoint.Commitment)
			if e != nil {
				return p, e
			}
			if batch.Sats != nil {
				p.RetainSatHistory = batch.Sats.RetainHistory
			}
		}
		if id == "sat-state" && !req.SatHistoryConfigured {
			policies, e := a.indexLivePolicies()
			if e != nil {
				return p, e
			}
			if policy := policies[req.Index]; policy.SatHistoryConfigured {
				p.RetainSatHistory = policy.RetainSatHistory
			}
		}
		from := p.From
		if id == "bitmap" && from < 792435 {
			from = 792435
		}
		if p.To >= 0 && from > p.To {
			return p, fmt.Errorf("%s starts at %d, beyond this plan's end height %d", id, from, p.To)
		}
		if id == "sat-state" && from != 0 && (store.checkpoint == nil || store.checkpoint.From != 0 || store.checkpoint.Height < from-1) {
			return p, fmt.Errorf("Sat index requires continuous history from genesis; choose block 0 or first extend its existing Sat coverage to this range")
		}
		if id == "bitmap" && from != 792435 && (store.checkpoint == nil || store.checkpoint.From != 792435 || store.checkpoint.Height < from-1) {
			return p, fmt.Errorf("Bitmap requires its history from 792435; choose that start or reuse a continuous prior Bitmap checkpoint")
		}
		out := indexPlanOutput{Index: id, From: from, To: p.To, Mode: p.Mode, Reuse: []heightInterval{}, Add: []heightInterval{}, Reason: "Missing derived output; reuse local verified evidence before fetching missing blocks."}
		if store.checkpoint != nil {
			if from < store.checkpoint.From || from > store.checkpoint.Height+1 {
				return p, fmt.Errorf("%s retains a contiguous range from %d through %d; this plan would create a gap or prepend history", id, store.checkpoint.From, store.checkpoint.Height)
			}
			out.From = store.checkpoint.From
			out.Mode = store.head.Mode
			if out.Mode == "" {
				out.Mode = "full"
			}
			switch a.indexChainState(store.checkpoint) {
			case "selected_chain":
				end := store.checkpoint.Height
				if out.To >= 0 && out.To < end {
					end = out.To
				}
				if end >= out.From {
					out.Reuse = append(out.Reuse, heightInterval{out.From, end})
				}
				out.Reason = "Compatible committed derivation on the selected chain; only missing outputs will be computed. History is verified on resume."
			case "stale_reorg_resume_required":
				out.Recovery = "reconcile_reorg"
				out.Reason = "Selected chain changed; find the last matching committed checkpoint and rebuild its descendants."
			default:
				out.Recovery = "verify_anchor"
				out.Reason = "Saved coverage is not reusable until its chain anchor is available. No data is deleted."
			}
		}
		if out.To >= out.From {
			out.Add = indexCoverageGaps(out.Reuse, out.From, out.To)
		}
		if id == "inscriptions" {
			if repair, e := store.numberingNeedsRepair(); e != nil {
				return p, e
			} else if repair {
				out.Recovery = "derive_missing_numbering"
				out.Reason += " Canonical numbering will replay missing historical state using retained occurrences and local evidence first; existing occurrence records remain intact."
			}
		}
		p.Outputs = append(p.Outputs, out)
	}
	if len(ids) > 1 {
		p.Notes = append(p.Notes, "Selected outputs share each missing block's fetching, validation, decoding and inscription extraction. Each index commits its own range; failures never advance another index's coverage.")
	}
	return p, nil
}

// Committed Bitcoin records are local derivation evidence, not a claim that raw
// bytes remain cached. Their content-addressed batches were integrity-checked at
// ingestion and are anchored again to the current selected header here.
func (a *app) indexSourceBlock(ctx context.Context, target blockTarget, retention string) (blockView, error) {
	if retention == "ephemeral" {
		s, err := indexStoreHead(a.dataDir, "blocks")
		if err != nil {
			return blockView{}, err
		}
		if s.checkpoint != nil && target.Height >= s.checkpoint.From && target.Height <= s.checkpoint.Height {
			commit, ready, e := s.activeLocatorCommitContext(ctx, target.Height)
			if e != nil {
				return blockView{}, e
			}
			if ready && commit != "" {
				batch, e := s.readBatch(commit)
				if e != nil {
					return blockView{}, e
				}
				if batch.Checkpoint.BlockHash == target.HashDisplay && batch.Bitcoin != nil && reusableBitcoinEvidence(batch.Bitcoin) && len(batch.Bitcoin.Transactions) > 0 {
					return blockView{Height: target.Height, Hash: target.HashDisplay, PreviousBlockHash: batch.PreviousBlockHash, Transactions: batch.Bitcoin.Transactions, TransactionCount: uint64(len(batch.Bitcoin.Transactions)), SourceNetwork: "local_index", VerificationState: "header_anchored", Verification: verificationView{VerifierVersion: blockVerifierVersion, HeaderHash: true, ProofOfWork: true, MerkleRoot: true, TransactionsParsed: true, WitnessCommitment: true, HeaderChainMatch: true}, Note: "Reused committed decoded evidence; raw block bytes were not fetched or retained."}, nil
				}
			}
		}
	}
	return a.fetchBlockWithPolicy(target, retention)
}

func (a *app) runSharedIndexBuild(ctx context.Context, j indexJob) {
	defer a.indexBuildFinished()
	finish := func(state string, err error) {
		j.State = state
		j.Error = ""
		if err != nil {
			j.Error = err.Error()
		}
		for i := range j.Progress {
			if j.Progress[i].State == "running" || j.Progress[i].State == "queued" || j.Progress[i].State == "waiting" {
				j.Progress[i].State = state
				if state == "yielded" {
					j.Progress[i].State = "catching_up"
				}
			}
		}
		if e := a.saveIndexJob(j); e != nil {
			a.indexMu.Lock()
			j.State, j.Error = "failed", "Cannot persist job status: "+e.Error()
			a.indexJob = cloneIndexJob(j)
			a.indexMu.Unlock()
		}
		if j.Live {
			if state == "failed" {
				a.failLiveIndexRuntime(j.Index, err)
			} else if state == "complete" || state == "yielded" {
				a.clearLiveIndexRuntime(j.Index)
			}
		}
	}
	// Re-plan at execution time because an earlier queued job may have added
	// reusable coverage after this request was reviewed.
	plan, err := a.planIndexBuild(indexBuildRequest{Index: j.Index, From: &j.From, To: &j.To, Mode: j.Mode, Retention: j.Retention, Outputs: j.Outputs, RetainSatHistory: j.RetainSatHistory, SatHistoryConfigured: true})
	if err != nil {
		finish("failed", err)
		return
	}
	stores := map[string]*indexStore{}
	j.Progress = nil
	for _, output := range plan.Outputs {
		p := indexOutputProgress{Index: output.Index, State: "running", From: output.From, To: j.To, Height: output.From - 1}
		s, e := openIndexStoreContext(ctx, a.dataDir, output.Index)
		if e != nil {
			p.State, p.Error = "failed", e.Error()
		} else {
			s.head.Mode = output.Mode
			s.retainSatHistory = j.RetainSatHistory
			stores[output.Index] = s
			if s.checkpoint != nil {
				p.Height = s.checkpoint.Height
				p.ReusedBlocks = p.Height - p.From + 1
			}
		}
		j.Progress = append(j.Progress, p)
	}
	wait := func(e error) bool {
		if j.Live {
			a.indexLiveControlMu.Lock()
			defer a.indexLiveControlMu.Unlock()
		}
		if ctx.Err() != nil || a.indexPauseRequested(j.ID) {
			finish("paused", nil)
			return false
		}
		j.State, j.Error = "waiting", e.Error()
		if err := a.saveIndexJob(j); err != nil {
			finish("failed", err)
			return false
		}
		if j.Live {
			a.deferLiveIndexRetry(j.Index, e)
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
	committed := int64(0)
	initialReusable := map[string]int64{}
	repairAttempted := map[string]bool{}
	for {
		if ctx.Err() != nil || a.indexPauseRequested(j.ID) {
			finish("paused", nil)
			return
		}
		if j.To < 0 {
			authority := a.currentChainAuthority()
			if authority.Height < j.From || (authority.Source == "bod_headers" && a.getStatus().Syncing) {
				a.needHeaders(j.From)
				if !wait(fmt.Errorf("waiting for a verified tip at or beyond %d", j.From)) {
					return
				}
				continue
			}
			j.To = authority.Height
			for i := range j.Progress {
				j.Progress[i].To = j.To
			}
		}
		next := int64(-1)
		pending := []int{}
		for i := range j.Progress {
			p := &j.Progress[i]
			if p.State == "failed" {
				continue
			}
			s := stores[p.Index]
			if err := s.reconcile(func(h int64) (string, error) { t, e := a.indexTarget(h); return t.HashDisplay, e }); err != nil {
				p.State, p.Error = "waiting", err.Error()
				continue
			}
			if p.Index == "inscriptions" && !repairAttempted[p.Index] {
				repairAttempted[p.Index] = true
				if e := a.repairIndexNumbering(ctx, s, j.To); e != nil {
					if ctx.Err() != nil {
						finish("paused", nil)
						return
					}
					p.Limitations = append(p.Limitations, "Canonical numbering is pending: "+e.Error()+". Occurrence coverage remains useful; resume the reviewed plan to retry enrichment.")
					j.Limitations = append(j.Limitations, p.Limitations[len(p.Limitations)-1])
				}
			}
			p.Height = p.From - 1
			if s.checkpoint != nil {
				p.Height = s.checkpoint.Height
			}
			if _, known := initialReusable[p.Index]; !known {
				initialReusable[p.Index] = p.Height
			}
			if p.Height < initialReusable[p.Index] {
				initialReusable[p.Index] = p.Height
			}
			reusedTo := initialReusable[p.Index]
			if j.To >= 0 && reusedTo > j.To {
				reusedTo = j.To
			}
			p.ReusedBlocks = 0
			if reusedTo >= p.From {
				p.ReusedBlocks = reusedTo - p.From + 1
			}
			if p.Height >= j.To || p.From > j.To {
				p.State = "complete"
				continue
			}
			p.State, p.Error = "running", ""
			h := p.Height + 1
			if next < 0 || h < next {
				next = h
				pending = []int{i}
			} else if h == next {
				pending = append(pending, i)
			}
		}
		if next < 0 {
			var failure, errorWait error
			for _, p := range j.Progress {
				if p.State == "failed" {
					failure = fmt.Errorf("%s: %s", p.Index, p.Error)
				}
				if p.State == "waiting" {
					errorWait = fmt.Errorf("%s: %s", p.Index, p.Error)
				}
			}
			if errorWait != nil {
				if !wait(errorWait) {
					return
				}
				continue
			}
			if failure != nil {
				finish("failed", failure)
			} else {
				finish("complete", nil)
			}
			return
		}
		if j.Live && committed >= indexLiveBatchBlocks {
			j.WaitingReason = "Live yielded the shared writer; unfinished output ranges continue on the next scheduler turn."
			finish("yielded", nil)
			return
		}
		target, e := a.indexTarget(next)
		if e != nil {
			if !wait(e) {
				return
			}
			continue
		}
		block, e := a.indexSourceBlock(ctx, target, j.Retention)
		if e != nil {
			var unavailable blockUnavailableError
			if !errors.As(e, &unavailable) {
				finish("failed", e)
				return
			}
			if !wait(e) {
				return
			}
			continue
		}
		if ctx.Err() != nil {
			finish("paused", nil)
			return
		}
		current, e := a.indexTarget(next)
		if e != nil {
			if !wait(e) {
				return
			}
			continue
		}
		if current.HashDisplay != block.Hash {
			continue
		}
		var extracted *inscriptionBlockResult
		for _, i := range pending {
			if j.Progress[i].Index == "inscriptions" || j.Progress[i].Index == "bitmap" {
				x, e := extractInscriptionOccurrences(block)
				if e != nil {
					for _, k := range pending {
						if j.Progress[k].Index == "inscriptions" || j.Progress[k].Index == "bitmap" {
							j.Progress[k].State, j.Progress[k].Error = "failed", e.Error()
						}
					}
				} else {
					extracted = &x
				}
				break
			}
		}
		for _, i := range pending {
			p := &j.Progress[i]
			if p.State == "failed" {
				continue
			}
			if ctx.Err() != nil {
				finish("paused", nil)
				return
			}
			if p.Index == "inscriptions" {
				stores[p.Index].numberingValues = a.indexNumberingBlockValues(ctx, block)
			}
			if e := stores[p.Index].appendPreparedBlock(block, p.From, j.Retention, extracted); e != nil {
				p.State, p.Error = "failed", e.Error()
				continue
			}
			p.Height = next
			p.AddedBlocks++
			if p.Index == j.Index {
				j.Height = next
			}
		}
		committed++
		j.BlocksChecked = int(committed)
		j.State, j.Error = "running", ""
		if e := a.saveIndexJob(j); e != nil {
			finish("failed", e)
			return
		}
	}
}
