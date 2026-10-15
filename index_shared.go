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

func (a *app) liveOutputHistoryNeedsWork(id string, policy indexLivePolicy, tip int64) bool {
	values := policy.RetainOutputValues != nil && *policy.RetainOutputValues
	scripts := policy.RetainOutputScripts != nil && *policy.RetainOutputScripts
	if !values && !scripts {
		return false
	}
	if id != "txo-spender" {
		found := false
		for _, output := range policy.Outputs {
			found = found || output == "txo-spender"
		}
		if !found {
			return false
		}
	}
	s, err := indexStoreHead(a.dataDir, "txo-spender")
	if err != nil || s.checkpoint == nil {
		return true
	}
	from := s.checkpoint.From
	if policy.InitialFrom != nil && *policy.InitialFrom > from {
		from = *policy.InitialFrom
	}
	return len(s.txioEnrichmentRanges(from, min(tip, s.checkpoint.Height), values, scripts)) != 0
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
	historyFrom := int64(-1)
	if req.From != nil {
		historyFrom = *req.From
	}
	if req.OutputHistoryFrom != nil {
		historyFrom = *req.OutputHistoryFrom
	}
	if req.Index == "utxo-state" {
		return a.planUTXOIndexBuild(req)
	}
	if req.Index == "sat-state" {
		return a.planSatIndexBuild077(req)
	}
	if req.Index == "address-state" || req.Index == "address-history" {
		return a.planAddressIndexBuild(req)
	}
	if (req.RetainOutputValues != nil || req.RetainOutputScripts != nil) && req.Index != "txo-spender" && req.Index != "blocks" {
		return indexPlan{}, fmt.Errorf("output value and script history options belong to TXIO")
	}
	if req.Index == "txo-spender" || req.Index == "blocks" {
		policies, err := a.indexLivePolicies()
		if err != nil {
			return indexPlan{}, err
		}
		policy := policies[req.Index]
		if req.RetainOutputValues == nil {
			req.RetainOutputValues = policy.RetainOutputValues
		}
		if req.RetainOutputScripts == nil {
			req.RetainOutputScripts = policy.RetainOutputScripts
		}
	}
	for _, id := range req.Outputs {
		if id == inscriptionLocatorIndex {
			return indexPlan{}, fmt.Errorf("conventional inscription locators belong to the inscription build option")
		}
	}
	if req.Index == inscriptionLocatorIndex {
		return indexPlan{}, fmt.Errorf("conventional inscription locators are internal inscription outputs")
	}
	if err := requireReleaseIndexRequest(req); err != nil {
		return indexPlan{}, err
	}
	if len(req.Outputs) > 0 && req.Index != "blocks" {
		return indexPlan{}, fmt.Errorf("combined outputs belong to the Bitcoin blocks plan")
	}
	var s *indexStore
	var err error
	if req.Index == "sat-state" && req.Rebuild {
		d, e := findIndexDefinition(req.Index)
		if e != nil {
			return indexPlan{}, e
		}
		s = &indexStore{profileRoot: a.dataDir, definition: d, head: indexHead{Mode: "lean", Retention: "pruned"}}
		if req.From != nil && *req.From != 0 {
			return indexPlan{}, fmt.Errorf("Sat rebuild must start at genesis")
		}
	} else {
		s, err = indexStoreHead(a.dataDir, req.Index)
	}
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
	p.OutputHistoryFrom = p.From
	if historyFrom >= 0 && historyFrom >= p.From {
		p.OutputHistoryFrom = historyFrom
	}
	if p.To >= 0 && p.OutputHistoryFrom > p.To {
		return p, fmt.Errorf("output history start exceeds the selected end")
	}
	if !p.Definition.Buildable {
		return p, fmt.Errorf("%s has no derivation executor", req.Index)
	}
	ids := append([]string{req.Index}, normalizedIndexOutputs(req)...)
	if req.Index == "inscriptions" && p.ConventionalIDs {
		ids = append(ids, inscriptionLocatorIndex)
	}
	for _, id := range ids {
		if id != req.Index && id != inscriptionLocatorIndex && id != "sat-state" && id != "tx-locator" && id != "inscriptions" && id != "bitmap" && id != "txo-spender" {
			return p, fmt.Errorf("unsupported combined output %q", id)
		}
		var store *indexStore
		var e error
		if id == "sat-state" && req.Rebuild {
			store = s
		} else {
			store, e = indexStoreHead(a.dataDir, id)
		}
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
			chainState := a.indexChainState(store.checkpoint)
			if id == "sat-state" {
				chainState = a.satChainState077(context.Background(), store.checkpoint, a.currentChainAuthority())
			}
			switch chainState {
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
	if retention == "pruned" {
		retention = "ephemeral"
	}
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
				if batch.Checkpoint.BlockHash == target.HashDisplay && batch.Bitcoin != nil && reusableBitcoinEvidence(batch.Bitcoin) {
					if decoded, decodeErr := a.decodedIndexBlockContext(ctx, s, batch); decodeErr == nil {
						return decoded, nil
					}
				}
			}
		}
	}
	return a.fetchBlockWithPolicyContext(ctx, target, retention)
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
			if state == "failed" || state == "rebuild_required" {
				a.failLiveIndexRuntime(j.Index, err)
			} else if state == "complete" || state == "yielded" {
				a.clearLiveIndexRuntime(j.Index)
			}
		}
	}
	// Re-plan at execution time because an earlier queued job may have added
	// reusable coverage after this request was reviewed.
	plan, err := a.planIndexBuild(indexBuildRequest{Rebuild: j.Rebuild, Index: j.Index, From: &j.From, To: &j.To, Mode: j.Mode, Retention: j.Retention, Outputs: j.Outputs, RetainSatHistory: j.RetainSatHistory, SatHistoryConfigured: true, ConventionalIDs: j.ConventionalIDs, RetainOutputValues: outputHistoryOption(j.Index, j.RetainOutputValues), RetainOutputScripts: outputHistoryOption(j.Index, j.RetainOutputScripts), OutputHistoryFrom: &j.OutputHistoryFrom})
	if err != nil {
		if satTerminalError077(err) {
			finish("rebuild_required", err)
		} else {
			finish("failed", err)
		}
		return
	}
	stores := map[string]*indexStore{}
	j.Progress = nil
	for _, output := range plan.Outputs {
		p := indexOutputProgress{Index: output.Index, State: "running", From: output.From, To: j.To, Height: output.From - 1}
		s, e := openIndexStoreContext(ctx, a.dataDir, output.Index)
		if e != nil {
			if output.Index == "sat-state" && satTerminalError077(e) {
				finish("rebuild_required", e)
				return
			}
			p.State, p.Error = "failed", e.Error()
		} else {
			s.head.Mode = output.Mode
			a.configureTXIOStore(s)
			s.retainOutputValues, s.retainOutputScripts = j.RetainOutputValues, j.RetainOutputScripts
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
	enriched := int64(0)
	if s := stores["txo-spender"]; s != nil && (j.RetainOutputValues || j.RetainOutputScripts) {
		if err := s.reconcile(func(h int64) (string, error) { t, e := a.indexTarget(h); return t.HashDisplay, e }); err != nil {
			finish("failed", err)
			return
		}
		end := int64(-1)
		if s.checkpoint != nil {
			end = s.checkpoint.Height
		}
		if j.To >= 0 && end > j.To {
			end = j.To
		}
		ranges := s.txioEnrichmentRanges(max(plan.OutputHistoryFrom, j.From), end, j.RetainOutputValues, j.RetainOutputScripts)
		reviewed := !j.Live || j.QueueManaged
		if reviewed && end >= max(plan.OutputHistoryFrom, j.From) {
			ranges = []heightInterval{{max(plan.OutputHistoryFrom, j.From), end}}
		}
		for _, interval := range ranges {
			for h := interval.From; h <= interval.To; {
				if ctx.Err() != nil || a.indexPauseRequested(j.ID) {
					finish("paused", nil)
					return
				}
				target, e := a.indexTarget(h)
				if e != nil {
					if !wait(e) {
						return
					}
					continue
				}
				needed, e := s.txioHistoryNeedsEnrichment(ctx, h, target.HashDisplay, j.RetainOutputValues, j.RetainOutputScripts, reviewed)
				if e != nil {
					finish("failed", e)
					return
				}
				if !needed {
					h++
					continue
				}
				block, e := a.indexSourceBlock(ctx, target, j.Retention)
				if e != nil {
					if ctx.Err() != nil {
						finish("paused", nil)
						return
					}
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
				current, e := a.indexTarget(h)
				if e != nil {
					if !wait(e) {
						return
					}
					continue
				}
				if current.HashDisplay != block.Hash {
					finish("failed", fmt.Errorf("selected chain changed during TXIO history enrichment; resume to reconcile"))
					return
				}
				if e = s.enrichTXIOBlockContext(ctx, block, j.RetainOutputValues, j.RetainOutputScripts); e != nil {
					finish("failed", e)
					return
				}
				if e = s.recordTXIOEnrichmentCoverage(h, block.Hash, j.RetainOutputValues, j.RetainOutputScripts); e != nil {
					finish("failed", e)
					return
				}
				j.State, j.Error = "running", ""
				if e = a.saveIndexJob(j); e != nil {
					finish("failed", e)
					return
				}
				enriched++
				if j.Live && !j.QueueManaged && enriched >= indexLiveBatchBlocks {
					finish("yielded", nil)
					return
				}
				h++
			}
		}
	}
	initialReusable := map[string]int64{}
	hasSat := stores["sat-state"] != nil
	fixedSatTarget := j.To >= 0 && !j.Live
	for {
		if ctx.Err() != nil || a.indexPauseRequested(j.ID) {
			finish("paused", nil)
			return
		}
		authority := a.currentChainAuthority()
		selectedTarget := func(h int64) (blockTarget, error) {
			if hasSat {
				return a.utxoSelectedTarget(ctx, authority, h)
			}
			return a.indexTarget(h)
		}
		if hasSat && (authority.Height < 0 || j.To < 0 && authority.Source == "bod_headers" && a.getStatus().Syncing) {
			if !wait(fmt.Errorf("waiting for selected Sat chain evidence")) {
				return
			}
			continue
		}
		if j.To < 0 {
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
		if hasSat && !fixedSatTarget && j.To > authority.Height {
			j.To = authority.Height
			for i := range j.Progress {
				j.Progress[i].To = j.To
			}
		}
		next := int64(-1)
		pending := []int{}
		for i := range j.Progress {
			p := &j.Progress[i]
			if !indexRunOptionalWorkAllowed(a.dataDir, p.Index) {
				p.State = "paused"
				p.Error = "Off in the timeline; committed coverage is retained."
				continue
			}
			if p.State == "failed" {
				continue
			}
			s := stores[p.Index]
			var reconcileErr error
			if p.Index == "sat-state" {
				reconcileErr = s.reconcileSatStateSelected(ctx, authority.Height, a.addressSelectedHashes(ctx, authority))
			} else {
				reconcileErr = s.reconcile(func(h int64) (string, error) { t, e := selectedTarget(h); return t.HashDisplay, e })
			}
			if err := reconcileErr; err != nil {
				if p.Index == "sat-state" && satTerminalError077(err) {
					finish("rebuild_required", err)
					return
				}
				p.State, p.Error = "waiting", err.Error()
				continue
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
				if hasSat {
					if err := stores["sat-state"].collectOrdinalState(); err != nil && !errors.Is(err, errOrdinalMaintenancePending) {
						finish("failed", err)
						return
					}
				}
				finish("complete", nil)
			}
			return
		}
		if j.Live && committed >= indexLiveBatchBlocks {
			j.WaitingReason = "Live yielded the shared writer; unfinished output ranges continue on the next scheduler turn."
			finish("yielded", nil)
			return
		}
		target, e := selectedTarget(next)
		if e != nil {
			if !wait(e) {
				return
			}
			continue
		}
		// A lagging related-ID output can reuse committed occurrence coverage.
		// Full records and evaluated-empty Lean blocks need no source fetch.
		if len(pending) == 1 && j.Progress[pending[0]].Index == inscriptionLocatorIndex {
			source, available, err := a.committedInscriptionBatch(ctx, next, target.HashDisplay)
			if err != nil {
				finish("failed", err)
				return
			}
			if available && (len(inscriptionBatchCoordinates(source)) == 0 || len(source.Inscriptions) == len(inscriptionBatchCoordinates(source))) {
				p := &j.Progress[pending[0]]
				if err = stores[p.Index].appendInscriptionLocators(source, nil, p.From, j.Retention); err != nil {
					p.State, p.Error = "failed", err.Error()
				} else {
					p.Height, p.AddedBlocks = next, p.AddedBlocks+1
				}
				committed++
				j.BlocksChecked = int(committed)
				if err = a.saveIndexJob(j); err != nil {
					finish("failed", err)
					return
				}
				continue
			}
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
		current, e := selectedTarget(next)
		if e != nil {
			if !wait(e) {
				return
			}
			continue
		}
		if current.HashDisplay != block.Hash {
			continue
		}
		if hasSat {
			if _, err := a.utxoSelectedTarget(ctx, authority, authority.Height); err != nil {
				if !wait(err) {
					return
				}
				continue
			}
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
			var appendErr error
			if p.Index == inscriptionLocatorIndex {
				source, available, err := a.committedInscriptionBatch(ctx, next, block.Hash)
				if err != nil {
					appendErr = err
				} else if !available {
					appendErr = fmt.Errorf("inscription output at height %d has not committed; resume its related locator output", next)
				} else {
					appendErr = stores[p.Index].appendInscriptionLocators(source, &block, p.From, j.Retention)
				}
			} else if p.Index == "sat-state" {
				appendErr = appendSatSelectedBlock077(ctx, stores[p.Index], block, p.From, j.Retention, extracted, a.addressSelectedHashes(ctx, authority))
			} else {
				appendErr = stores[p.Index].appendPreparedBlock(block, p.From, j.Retention, extracted)
			}
			if appendErr != nil {
				var changed satPublicationAnchorError077
				if p.Index == "sat-state" && errors.As(appendErr, &changed) {
					p.State, p.Error = "waiting", appendErr.Error()
					continue
				}
				if p.Index == "sat-state" && satTerminalError077(appendErr) {
					finish("rebuild_required", appendErr)
					return
				}
				p.State, p.Error = "failed", appendErr.Error()
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
		if hasSat && committed%128 == 0 {
			if err := stores["sat-state"].collectOrdinalState(); err != nil && !errors.Is(err, errOrdinalMaintenancePending) {
				finish("failed", err)
				return
			}
		}
		if e := a.saveIndexJob(j); e != nil {
			finish("failed", e)
			return
		}
	}
}
