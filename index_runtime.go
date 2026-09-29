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
	Mode           string `json:"mode,omitempty"`
	ID             string `json:"id"`
	Index          string `json:"index"`
	State          string `json:"state"`
	From           int64  `json:"from"`
	To             int64  `json:"to"`
	Height         int64  `json:"height"`
	Error          string `json:"error,omitempty"`
	Retention      string `json:"retention"`
	BlocksChecked  int    `json:"blocks_checked"`
	DistrictsFound int    `json:"districts_found"`
}
type indexStatusView struct {
	Definitions []indexDefinition   `json:"definitions"`
	Providers   []indexProviderView `json:"providers"`
	Instances   []indexInstance     `json:"instances"`
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
	if a.indexJob.State == "running" || a.indexJob.State == "waiting" {
		a.indexJob.State = "paused"
		a.indexJob.Error = "Interrupted job; start the same index to resume its committed checkpoint."
	}
}
func (a *app) indexJobSnapshot() indexJob {
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	a.loadIndexJobLocked()
	return a.indexJob
}
func (a *app) saveIndexJob(j indexJob) error {
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	if e := atomicWriteJSON(filepath.Join(a.dataDir, "indexes", "job.json"), j); e != nil {
		return e
	}
	a.indexJob = j
	return nil
}
func (a *app) indexStatus() indexStatusView {
	out := indexStatusView{Definitions: indexDefinitions(), Providers: a.indexProviderViews(), Instances: []indexInstance{}, Job: a.indexJobSnapshot()}
	for _, id := range []string{"inscriptions", "bitmap"} {
		s, e := indexStoreHead(a.dataDir, id)
		if e != nil {
			out.Errors = append(out.Errors, id+": "+e.Error())
			continue
		}
		if s.checkpoint == nil {
			continue
		}
		out.Instances = append(out.Instances, indexInstance{Definition: id, Version: s.definition.Version, Network: s.definition.Network, Coverage: []heightInterval{{s.checkpoint.From, s.checkpoint.Height}}, Gaps: []heightInterval{}, Completeness: "recorded_contiguous_range; history_rechecked_on_resume", Verification: "locally_derived; " + a.indexChainState(s.checkpoint), Provenance: "Bitcoin block and witness evidence", Retention: s.head.Retention, Queryable: true, Serveable: a.indexPublicationMatches(id, s.checkpoint), Checkpoint: s.checkpoint, Note: "Private derived state. Canonical numbering and ownership are not inferred."})
	}
	return out
}
func (a *app) startIndexBuild(req indexBuildRequest) (indexJob, error) {
	p, e := planIndex(req)
	if e != nil {
		return indexJob{}, e
	}
	if !p.Definition.Buildable {
		return indexJob{}, fmt.Errorf("%s has no derivation executor; inspect its provider capabilities", req.Index)
	}
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	a.loadIndexJobLocked()
	if a.indexCancel != nil {
		return a.indexJob, fmt.Errorf("an index build is already active")
	}
	s, e := indexStoreHead(a.dataDir, req.Index)
	if e != nil {
		return indexJob{}, e
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
	j := indexJob{Mode: p.Mode, ID: fmt.Sprintf("%d", time.Now().UnixNano()), Index: req.Index, State: "running", From: p.From, To: p.To, Height: p.From - 1, Retention: p.Retention}
	if s.checkpoint != nil {
		j.Height = s.checkpoint.Height
	}
	if e = atomicWriteJSON(filepath.Join(a.dataDir, "indexes", "job.json"), j); e != nil {
		return j, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.indexCancel = cancel
	a.indexJob = j
	go a.runIndexBuild(ctx, j, s)
	return j, nil
}
func (a *app) pauseIndexBuild() (indexJob, error) {
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	a.loadIndexJobLocked()
	if a.indexCancel != nil {
		a.indexCancel()
	}
	if a.indexCancel == nil {
		return requestIndexPause(a.dataDir)
	}
	j := a.indexJob
	j.State = "pausing"
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
	defer func() { a.indexMu.Lock(); a.indexCancel = nil; a.indexMu.Unlock() }()
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
			a.indexJob = j
			a.indexMu.Unlock()
		}
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
	retryDelay := 2 * time.Second
	wait := func(e error) bool {
		j.State = "waiting"
		j.Error = e.Error()
		if err := a.saveIndexJob(j); err != nil {
			finish("failed", err)
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
		block, e := a.fetchBlockWithPolicy(t, j.Retention)
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
		j.Height = next
	}
}
func (a *app) indexQuery(id string, limit int) (any, error) {
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
		if id == "bitmap" {
			for _, r := range b.Bitmap {
				total++
				if len(rows) < limit {
					r.Height = b.Checkpoint.Height
					rows = append(rows, bitmapQueryRecord{r, bitmapCompatibility(r)})
				}
			}
		} else {
			for _, r := range b.Inscriptions {
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
