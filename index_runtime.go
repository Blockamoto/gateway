package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type indexJob struct {
	ConventionalIDs  *bool                 `json:"conventional_ids,omitempty"`
	Limitations      []string              `json:"limitations,omitempty"`
	QueueManaged     bool                  `json:"queue_managed,omitempty"`
	Outputs          []string              `json:"outputs,omitempty"`
	Progress         []indexOutputProgress `json:"progress,omitempty"`
	RetainSatHistory bool                  `json:"retain_sat_history,omitempty"`
	QueuePosition    int                   `json:"queue_position,omitempty"`
	WaitingReason    string                `json:"waiting_reason,omitempty"`
	Live             bool                  `json:"live,omitempty"`
	Mode             string                `json:"mode,omitempty"`
	ID               string                `json:"id"`
	Index            string                `json:"index"`
	State            string                `json:"state"`
	From             int64                 `json:"from"`
	To               int64                 `json:"to"`
	Height           int64                 `json:"height"`
	Error            string                `json:"error,omitempty"`
	Retention        string                `json:"retention"`
	BlocksChecked    int                   `json:"blocks_checked"`
	DistrictsFound   int                   `json:"districts_found"`
}

// Workers, stored jobs and returned status views own their mutable slices.
// indexMu protects publication, but cannot protect JSON encoding after a
// snapshot is returned or the worker's next progress update outside that lock.
func cloneIndexJob(j indexJob) indexJob {
	if j.ConventionalIDs != nil {
		value := *j.ConventionalIDs
		j.ConventionalIDs = &value
	}
	j.Outputs = append([]string(nil), j.Outputs...)
	j.Limitations = append([]string(nil), j.Limitations...)
	j.Progress = append([]indexOutputProgress(nil), j.Progress...)
	for i := range j.Progress {
		j.Progress[i].Limitations = append([]string(nil), j.Progress[i].Limitations...)
	}
	return j
}

type indexStatusView struct {
	Timeline    indexTimelineView   `json:"timeline"`
	Jobs        []indexJob          `json:"jobs"`
	Definitions []indexDefinition   `json:"definitions"`
	Providers   []indexProviderView `json:"providers"`
	Instances   []indexInstance     `json:"instances"`
	Live        []indexLiveView     `json:"live"`
	Job         indexJob            `json:"job"`
	Errors      []string            `json:"errors,omitempty"`
}

func (a *app) loadIndexJobLocked() {
	if a.indexJob.ID != "" {
		return
	}
	b, e := os.ReadFile(filepath.Join(a.dataDir, "indexes", "job.json"))
	if e != nil {
		return
	}
	if json.Unmarshal(b, &a.indexJob) != nil {
		a.indexJob = indexJob{}
		return
	}
	if a.indexJob.State == "running" || a.indexJob.State == "waiting" || a.indexJob.State == "pausing" {
		a.indexJob.State = "paused"
		a.indexJob.Error = "Interrupted job; start the same index to resume its committed checkpoint."
	}
	// The durable queue owns reviewed cancellation/restart state. Keep the
	// legacy single-job status consistent on the first read after restart.
	if a.loadIndexQueueLocked() == nil {
		for _, entry := range a.indexQueue {
			if entry.Job.ID == a.indexJob.ID {
				a.indexJob = cloneIndexJob(entry.Job)
				a.indexJob.QueueManaged = true
				break
			}
		}
	}
}
func (a *app) indexJobSnapshot() indexJob {
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	a.loadIndexJobLocked()
	return cloneIndexJob(a.indexJob)
}
func (a *app) saveIndexJob(j indexJob) error {
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	if err := a.loadIndexQueueLocked(); err != nil {
		return err
	}
	for _, entry := range a.indexQueue {
		if entry.Job.ID == j.ID && entry.StopState == "cancelled" && j.State == "paused" {
			j.State = "cancelled"
			j.WaitingReason = "Cancelled; committed records are retained."
		}
	}
	if e := atomicWriteJSON(filepath.Join(a.dataDir, "indexes", "job.json"), j); e != nil {
		return e
	}
	a.indexJob = cloneIndexJob(j)
	return a.updateQueuedJobLocked(j)
}
func (a *app) indexStatus() indexStatusView {
	out := indexStatusView{Definitions: indexDefinitions(), Providers: a.indexProviderViews(), Instances: []indexInstance{}, Live: []indexLiveView{}, Job: a.indexJobSnapshot()}
	var queueErr error
	out.Jobs, queueErr = a.indexJobsSnapshot()
	if queueErr != nil {
		out.Errors = append(out.Errors, "queue: "+queueErr.Error())
	}
	if live, err := a.indexLiveViews(); err != nil {
		out.Errors = append(out.Errors, "live: "+err.Error())
	} else {
		out.Live = live
	}
	for _, id := range []string{"blocks", "tx-locator", "inscriptions", "bitmap", "txo-spender", "sat-state"} {
		s, e := indexStoreHead(a.dataDir, id)
		if e != nil {
			out.Errors = append(out.Errors, id+": "+e.Error())
			continue
		}
		if s.checkpoint == nil {
			continue
		}
		var retainHistory bool
		if id == "sat-state" {
			if batch, err := s.readBatch(s.checkpoint.Commitment); err == nil && batch.Sats != nil {
				retainHistory = batch.Sats.RetainHistory
			}
		}
		out.Instances = append(out.Instances, indexInstance{Mode: s.head.Mode, Definition: id, Version: s.definition.Version, Network: s.definition.Network, Coverage: []heightInterval{{s.checkpoint.From, s.checkpoint.Height}}, Gaps: []heightInterval{}, Completeness: "recorded_contiguous_range; history_rechecked_on_resume", Verification: "locally_derived; " + a.indexChainState(s.checkpoint), Provenance: "Bitcoin block and witness evidence", Retention: s.head.Retention, Queryable: true, Serveable: a.indexPublicationMatches(id, s.checkpoint), Checkpoint: s.checkpoint, Note: a.indexInstanceNote(s)})
		out.Instances[len(out.Instances)-1].RetainSatHistory = retainHistory
		if !releaseFeatureAvailable(id) {
			instance := &out.Instances[len(out.Instances)-1]
			instance.Queryable, instance.Serveable = false, false
			instance.Note = releaseLockReason(id)
		}
	}
	out.Timeline = a.indexTimeline(out.Definitions, out.Providers, out.Instances, out.Errors...)
	return out
}
func (a *app) startIndexBuild(req indexBuildRequest) (indexJob, error) {
	if err := requireReleaseIndexRequest(req); err != nil {
		return indexJob{}, err
	}
	a.indexLiveControlMu.Lock()
	defer a.indexLiveControlMu.Unlock()
	policies, err := a.indexLivePolicies()
	if err != nil {
		return indexJob{}, err
	}
	if policies[req.Index].Stopped {
		return indexJob{}, fmt.Errorf("%s indexing is off; switch it on before starting a reviewed build", req.Index)
	}
	j, err := a.acceptIndexBuild(req)
	if err == nil {
		// An accepted reviewed build is an explicit retry after a Live failure.
		a.clearLiveIndexRuntime(req.Index)
	}
	return j, err
}

func (a *app) startIndexBuildInternal(req indexBuildRequest, live bool) (indexJob, error) {
	return a.startIndexBuildID(req, live, "")
}
func (a *app) startIndexBuildID(req indexBuildRequest, live bool, id string) (indexJob, error) {
	if err := requireReleaseIndexRequest(req); err != nil {
		return indexJob{}, err
	}
	req = cloneIndexBuildRequest(req)
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	a.loadIndexJobLocked()
	if a.indexCancel != nil {
		return cloneIndexJob(a.indexJob), fmt.Errorf("an index build is already active")
	}
	s, e := indexStoreHead(a.dataDir, req.Index)
	if e != nil {
		return indexJob{}, e
	}
	// Suggested starts apply only to fresh instances. An omitted resume range
	// retains its authenticated checkpoint even when the current UI suggestion
	// differs; an explicit conflicting start still fails below.
	if s.checkpoint != nil && req.From == nil {
		from := s.checkpoint.From
		req.From = &from
	}
	p, e := a.planIndexBuild(req)
	if e != nil {
		return indexJob{}, e
	}
	req.RetainSatHistory, req.SatHistoryConfigured = p.RetainSatHistory, true
	req.ConventionalIDs = boolPointer(p.ConventionalIDs)
	if !p.Definition.Buildable {
		return indexJob{}, fmt.Errorf("%s has no derivation executor; inspect its provider capabilities", req.Index)
	}
	if s.checkpoint != nil && s.checkpoint.From != p.From {
		return indexJob{}, fmt.Errorf("existing instance begins at %d; use that range to resume", s.checkpoint.From)
	}
	if s.checkpoint != nil {
		existingMode := s.head.Mode
		if existingMode == "" {
			existingMode = "full"
		}
		if req.Mode != "" && req.Mode != existingMode {
			return indexJob{}, fmt.Errorf("existing index uses %s mode; resume with that mode", existingMode)
		}
		p.Mode = existingMode
	}
	policies, e := a.indexLivePolicies()
	if e != nil {
		return indexJob{}, e
	}
	if !live || req.Live {
		policy := policies[req.Index]
		policy.Index, policy.Retention = req.Index, p.Retention
		policy.Outputs, policy.RetainSatHistory = req.Outputs, req.RetainSatHistory
		policy.ConventionalIDs = req.ConventionalIDs
		policy.SatHistoryConfigured = true
		if !live && req.LiveConfigured {
			// An explicitly reviewed fixed-range build turns following off.
			// Scheduler continuations pass live=true and preserve the policy.
			policy.Enabled = false
		}
		if req.Live {
			policy.Enabled, policy.Paused, policy.Stopped = true, false, false
			policy.InitialFrom = &p.From
			policy.Mode = p.Mode
		}
		policy.Updated = time.Now().UTC().Format(time.RFC3339)
		if e = a.writeIndexLivePolicy(policy); e != nil {
			return indexJob{}, e
		}
	}
	if id == "" {
		id = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if e := a.clearIndexPause(id); e != nil {
		return indexJob{}, e
	}
	j := indexJob{Live: live, Mode: p.Mode, ID: id, Index: req.Index, State: "running", From: p.From, To: p.To, Height: p.From - 1, Retention: p.Retention, Outputs: req.Outputs, RetainSatHistory: req.RetainSatHistory, ConventionalIDs: req.ConventionalIDs}
	if s.checkpoint != nil {
		j.Height = s.checkpoint.Height
	}
	if e = atomicWriteJSON(filepath.Join(a.dataDir, "indexes", "job.json"), j); e != nil {
		return j, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.indexCancel = cancel
	a.indexJob = cloneIndexJob(j)
	if err := a.updateQueuedJobLocked(j); err != nil {
		cancel()
		a.indexCancel = nil
		return j, err
	}
	if len(req.Outputs) > 0 || req.Index == "sat-state" || req.Index == "txo-spender" || req.Index == "inscriptions" && p.ConventionalIDs {
		go a.runSharedIndexBuild(ctx, cloneIndexJob(j))
	} else {
		go a.runIndexBuild(ctx, cloneIndexJob(j), s)
	}
	return j, nil
}
func (a *app) pauseIndexBuild() (indexJob, error) {
	return a.pauseIndexBuildFor("")
}
func (a *app) pauseIndexBuildFor(index string) (indexJob, error) {
	a.indexLiveControlMu.Lock()
	defer a.indexLiveControlMu.Unlock()
	a.indexMu.Lock()
	a.loadIndexJobLocked()
	j := cloneIndexJob(a.indexJob)
	if index != "" && !indexJobOwns(j, index) {
		a.indexMu.Unlock()
		return indexJob{}, fmt.Errorf("no active job belongs to %s", index)
	}
	cancel := a.indexCancel
	// Persist the parent policy before stopping a shared worker. A restart
	// between cancellation and policy persistence must not resume Live work.
	if j.Index != "" {
		if err := a.pauseLivePolicyForJob(j.Index); err != nil {
			a.indexMu.Unlock()
			return j, fmt.Errorf("Live policy could not be paused: %w", err)
		}
	}
	if cancel != nil {
		cancel()
		j.State = "pausing"
	}
	a.indexMu.Unlock()
	if cancel == nil {
		return requestIndexPause(a.dataDir)
	}
	return j, nil
}

func requestIndexPause(root string) (indexJob, error) {
	var j indexJob
	b, e := os.ReadFile(filepath.Join(root, "indexes", "job.json"))
	if e != nil {
		return j, e
	}
	if e = json.Unmarshal(b, &j); e != nil {
		return j, e
	}
	if j.ID == "" {
		return j, fmt.Errorf("no index job")
	}
	if j.State != "running" && j.State != "waiting" {
		return j, nil
	}
	if e = atomicWriteJSON(filepath.Join(root, "indexes", "pause.json"), struct {
		ID string `json:"id"`
	}{j.ID}); e != nil {
		return j, e
	}
	j.State = "pausing"
	return j, nil
}
func (a *app) indexPauseRequested(id string) bool {
	b, e := os.ReadFile(filepath.Join(a.dataDir, "indexes", "pause.json"))
	if e != nil {
		return false
	}
	var req struct {
		ID string `json:"id"`
	}
	return json.Unmarshal(b, &req) == nil && req.ID == id
}
func (a *app) waitIndexBuild(ctx context.Context) (indexJob, error) {
	for {
		j := a.indexJobSnapshot()
		if j.State != "running" && j.State != "waiting" {
			if j.State == "failed" {
				return j, fmt.Errorf("%s", j.Error)
			}
			return j, nil
		}
		select {
		case <-ctx.Done():
			return j, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
func (a *app) indexTarget(height int64) (blockTarget, error) {
	if t, e := a.coreTargetByHeight(height); e == nil {
		return t, nil
	}
	t, e := a.localBlockTarget(height)
	if e != nil {
		a.needHeaders(height)
	}
	return t, e
}
func (a *app) runIndexBuild(ctx context.Context, j indexJob, s *indexStore) {
	defer a.indexBuildFinished()
	finish := func(state string, e error) {
		j.State = state
		j.Error = ""
		if e != nil {
			j.Error = e.Error()
		}
		if err := a.saveIndexJob(j); err != nil {
			a.indexMu.Lock()
			j.State = "failed"
			j.Error = "Cannot persist job status: " + err.Error()
			a.indexJob = cloneIndexJob(j)
			a.indexMu.Unlock()
		}
		if j.Live {
			if j.State == "failed" {
				a.failLiveIndexRuntime(j.Index, fmt.Errorf("%s", j.Error))
			} else if j.State == "complete" {
				a.clearLiveIndexRuntime(j.Index)
			}
		}
	}
	if err := requireReleaseIndexRequest(indexBuildRequest{Index: j.Index, Outputs: j.Outputs, ConventionalIDs: j.ConventionalIDs}); err != nil {
		finish("paused", err)
		return
	}
	// Validate persisted dependency history off the request goroutine. A large
	// resume must not freeze the GUI's start request.
	checked, e := openIndexStoreContext(ctx, a.dataDir, j.Index)
	if e != nil {
		if ctx.Err() != nil {
			finish("paused", nil)
		} else {
			finish("failed", e)
		}
		return
	}
	s = checked
	s.head.Mode = j.Mode
	s.retainSatHistory = j.RetainSatHistory
	liveBlocksProcessed := int64(0)
	retryDelay := 2 * time.Second
	wait := func(e error) bool {
		if j.Live {
			// Policy changes cancel under this same lock. A failed request that
			// outlives Pause must not replace its paused status with a retry.
			a.indexLiveControlMu.Lock()
			defer a.indexLiveControlMu.Unlock()
		}
		if ctx.Err() != nil || a.indexPauseRequested(j.ID) {
			finish("paused", nil)
			return false
		}
		j.State = "waiting"
		j.Error = e.Error()
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
		case <-time.After(retryDelay):
			return true
		}
	}
	for {
		if ctx.Err() != nil || a.indexPauseRequested(j.ID) {
			finish("paused", nil)
			return
		}
		if j.Live && liveBlocksProcessed >= indexLiveBatchBlocks {
			// Reconciliation can rewind well below the height used to choose a
			// batch's end. Bound actual new commits too so a deep reorg cannot
			// give one Live recipe the shared writer for its entire history.
			j.To = j.Height
			finish("complete", nil)
			return
		}
		if j.To < 0 {
			authority := a.currentChainAuthority()
			st := a.getStatus()
			if authority.Height < j.From || (authority.Source == "bod_headers" && st.Syncing) {
				a.needHeaders(j.From)
				if !wait(fmt.Errorf("waiting for a verified tip at or beyond %d; an explicit end height can bound the build", j.From)) {
					return
				}
				continue
			}
			j.To = authority.Height
		}
		e := s.reconcile(func(h int64) (string, error) { t, e := a.indexTarget(h); return t.HashDisplay, e })
		if e != nil {
			if !wait(e) {
				return
			}
			continue
		}
		j.Height = j.From - 1
		next := j.From
		if s.checkpoint != nil {
			next = s.checkpoint.Height + 1
			j.Height = s.checkpoint.Height
		}
		j.BlocksChecked = int(next - j.From)
		j.DistrictsFound = len(s.winners)
		if next > j.To {
			finish("complete", nil)
			return
		}
		t, e := a.indexTarget(next)
		if e != nil {
			if !wait(fmt.Errorf("waiting for selected headers at %d: %w", next, e)) {
				return
			}
			continue
		}
		j.State = "running"
		j.Error = ""
		if e = a.saveIndexJob(j); e != nil {
			finish("failed", e)
			return
		}
		block, e := a.indexSourceBlock(ctx, t, j.Retention)
		if e != nil {
			var unavailable blockUnavailableError
			if !errors.As(e, &unavailable) {
				finish("failed", e)
				return
			}
			// Block bodies may be temporarily unavailable even while headers and
			// peer sessions are healthy. Keep the checkpointed job alive and retry
			// after the normal wait interval instead of forcing a manual restart.
			if !wait(fmt.Errorf("waiting for block %d from Bitcoin peers: %w", next, e)) {
				return
			}
			if retryDelay < 30*time.Second {
				retryDelay *= 2
				if retryDelay > 30*time.Second {
					retryDelay = 30 * time.Second
				}
			}
			continue
		}
		retryDelay = 2 * time.Second
		if ctx.Err() != nil {
			finish("paused", nil)
			return
		}
		// Fetch can outlive a chain update. Recheck the exact anchor before commit.
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
		if e = s.appendBlock(block, j.From, j.Retention); e != nil {
			finish("failed", e)
			return
		}
		if j.Live {
			a.clearLiveIndexRuntime(j.Index)
			liveBlocksProcessed++
		}
		j.Height = next
	}
}
func (a *app) indexQuery(id string, limit int) (any, error) {
	if err := requireReleaseFeature(id); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	s, e := indexStoreHead(a.dataDir, id)
	if e != nil {
		return nil, e
	}
	rows := []any{}
	total := 0
	next := s.head.Commitment
	blocks := 0
	var child *indexBatch
	for next != "" && blocks < 512 && len(rows) < limit {
		b, e := s.readBatch(next)
		if e != nil {
			return nil, e
		}
		if child != nil && (child.Checkpoint.Height != b.Checkpoint.Height+1 || child.PreviousBlockHash != b.Checkpoint.BlockHash) {
			return nil, fmt.Errorf("query checkpoint discontinuity")
		}
		if b.Sats != nil {
			rows = append(rows, struct {
				Height int64             `json:"block_height"`
				Hash   string            `json:"block_hash"`
				Data   *satIndexSnapshot `json:"data"`
			}{b.Checkpoint.Height, b.Checkpoint.BlockHash, b.Sats})
			total++
		} else if b.TransactionLocator != nil {
			rows = append(rows, b.TransactionLocator)
			total++
		} else if b.Bitcoin != nil {
			rows = append(rows, struct {
				Height int64              `json:"block_height"`
				Hash   string             `json:"block_hash"`
				Data   *bitcoinIndexBlock `json:"data"`
			}{b.Checkpoint.Height, b.Checkpoint.BlockHash, b.Bitcoin})
			total++
		} else if b.Spenders != nil {
			for i := len(b.Spenders.Rows) - 1; i >= 0; i-- {
				total++
				if len(rows) < limit {
					rows = append(rows, indexSpendMatch{b.Spenders.Rows[i], b.Checkpoint.Height, b.Checkpoint.BlockHash})
				}
			}
		} else if id == "bitmap" {
			for i := len(b.Bitmap) - 1; i >= 0; i-- {
				r := b.Bitmap[i]
				a.enrichBitmapIdentity(&r, b.Checkpoint.BlockHash)
				total++
				if len(rows) < limit {
					r.Height = b.Checkpoint.Height
					rows = append(rows, bitmapQueryRecord{r, bitmapCompatibility(r)})
				}
			}
		} else if id == "inscriptions" {
			coordinates := inscriptionBatchCoordinates(b)
			rows = append(rows, struct {
				Inscriptions     []string `json:"inscriptions"`
				Height           int64    `json:"block_height"`
				TransactionCount *uint32  `json:"transaction_count,omitempty"`
				Mode             string   `json:"mode"`
			}{coordinates, b.Checkpoint.Height, inscriptionBatchCount(b), inscriptionBatchMode(b)})
			total += len(coordinates)
		} else {
			for i := len(b.Inscriptions) - 1; i >= 0; i-- {
				r := b.Inscriptions[i]
				a.enrichInscriptionOccurrence(&r)
				total++
				if len(rows) < limit {
					rows = append(rows, r)
				}
			}
		}
		next = b.Checkpoint.PreviousCommitment
		child = &b
		blocks++
	}
	return struct {
		Definition      indexDefinition  `json:"definition"`
		Checkpoint      *indexCheckpoint `json:"checkpoint"`
		Rows            []any            `json:"rows"`
		Total           int              `json:"total"`
		TotalKnown      bool             `json:"total_known"`
		Truncated       bool             `json:"truncated"`
		ChainState      string           `json:"chain_state"`
		InspectedBlocks int              `json:"inspected_blocks"`
	}{s.definition, s.checkpoint, rows, total, next == "", next != "" || total > len(rows), a.indexChainState(s.checkpoint), blocks}, nil
}

type indexLookupResult struct {
	RevealHeight    int64                      `json:"reveal_height"`
	RevealBlockHash string                     `json:"reveal_block_hash,omitempty"`
	Found           bool                       `json:"found"`
	Record          *bitmapRecord              `json:"record,omitempty"`
	Checkpoint      *indexCheckpoint           `json:"checkpoint"`
	ChainState      string                     `json:"chain_state"`
	Absence         string                     `json:"absence"`
	Compatibility   *bitmapCompatibilityResult `json:"compatibility_result,omitempty"`
}

func (a *app) indexLookup(id, key string) (any, error) {
	if err := requireReleaseFeature(id); err != nil {
		return nil, err
	}
	if id == "txo-spender" {
		return a.lookupIndexSpender(context.Background(), key)
	}
	if id != "bitmap" {
		return nil, fmt.Errorf("key lookup is implemented for Bitmap districts")
	}
	n, e := strconv.ParseUint(key, 10, 64)
	if e != nil || strconv.FormatUint(n, 10) != key {
		return nil, fmt.Errorf("district must be canonical unsigned decimal")
	}
	s, e := indexStoreHead(a.dataDir, id)
	if e != nil {
		return nil, e
	}
	result := indexLookupResult{Checkpoint: s.checkpoint, ChainState: a.indexChainState(s.checkpoint), Absence: "not_found_within_committed_range; not a global absence proof"}
	// Traverse bounded-memory committed history. Unlike a preview query this is
	// an explicit exact-key request and may inspect the full chosen range.
	e = s.walk(func(b indexBatch) error {
		for _, r := range b.Bitmap {
			if r.Accepted && r.District != nil && *r.District == n {
				copy := r
				a.enrichBitmapIdentity(&copy, b.Checkpoint.BlockHash)
				copy.Height = b.Checkpoint.Height
				result.RevealHeight = b.Checkpoint.Height
				result.RevealBlockHash = b.Checkpoint.BlockHash
				result.Found = true
				result.Record = &copy
				result.Absence = ""
				c := bitmapCompatibility(r)
				result.Compatibility = &c
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return result, nil
}

func (a *app) indexChainState(c *indexCheckpoint) string {
	if c == nil {
		return "empty"
	}
	t, e := a.coreTargetByHeight(c.Height)
	if e != nil {
		t, e = a.localBlockTarget(c.Height)
	}
	if e != nil {
		return "anchor_unavailable"
	}
	if t.HashDisplay != c.BlockHash {
		return "stale_reorg_resume_required"
	}
	return "selected_chain"
}
