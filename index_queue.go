package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

func (a *app) waitQueuedIndexJob(ctx context.Context, id string) (indexJob, error) {
	for {
		jobs, err := a.indexJobsSnapshot()
		if err != nil {
			return indexJob{}, err
		}
		found := false
		for _, j := range jobs {
			if j.ID != id {
				continue
			}
			found = true
			if j.State != "queued" && j.State != "running" && j.State != "waiting" && j.State != "pausing" {
				if j.State == "failed" {
					return j, fmt.Errorf("%s", j.Error)
				}
				return j, nil
			}
		}
		if !found {
			return indexJob{}, fmt.Errorf("queued job %q is no longer available in retained history; inspect its index coverage", id)
		}
		select {
		case <-ctx.Done():
			return indexJob{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// The queue records consent to a reviewed request. Reading it cannot start work.
// job.json remains compatible with older clients; this journal retains each
// accepted request and its own result when the shared writer moves on.
type indexQueueEntry struct {
	StopState string            `json:"stop_state,omitempty"`
	Request   indexBuildRequest `json:"request"`
	Job       indexJob          `json:"job"`
}

func cloneIndexBuildRequest(req indexBuildRequest) indexBuildRequest {
	if req.ConventionalIDs != nil {
		value := *req.ConventionalIDs
		req.ConventionalIDs = &value
	}
	req.Outputs = append([]string(nil), req.Outputs...)
	if req.From != nil {
		from := *req.From
		req.From = &from
	}
	if req.To != nil {
		to := *req.To
		req.To = &to
	}
	return req
}
func cloneIndexQueueEntry(entry indexQueueEntry) indexQueueEntry {
	entry.Request = cloneIndexBuildRequest(entry.Request)
	entry.Job = cloneIndexJob(entry.Job)
	return entry
}

type indexQueueState struct {
	Schema  int               `json:"schema"`
	Entries []indexQueueEntry `json:"entries"`
}
type indexQueueRequest struct {
	ID     string `json:"id"`
	Action string `json:"action"`
}

func (a *app) loadIndexQueueLocked() error {
	if a.indexQueueLoaded {
		return a.indexQueueError
	}
	a.indexQueueLoaded = true
	b, err := os.ReadFile(filepath.Join(a.dataDir, "indexes", "queue.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		a.indexQueueError = err
		return err
	}
	var state indexQueueState
	if err = json.Unmarshal(b, &state); err != nil || state.Schema != 1 || len(state.Entries) > 192 {
		a.indexQueueError = fmt.Errorf("invalid index queue; retained jobs were not started")
		return a.indexQueueError
	}
	seen := map[string]bool{}
	for i := range state.Entries {
		e := &state.Entries[i]
		if e.Job.ID == "" || seen[e.Job.ID] || e.Request.Index != e.Job.Index {
			a.indexQueueError = fmt.Errorf("invalid queued job identity")
			return a.indexQueueError
		}
		seen[e.Job.ID] = true
		if err := requireReleaseIndexRequest(e.Request); err != nil && e.Job.State != "complete" && e.Job.State != "cancelled" {
			e.Job.State, e.Job.WaitingReason = "paused", err.Error()
		}
		if e.StopState == "cancelled" && (e.Job.State == "running" || e.Job.State == "waiting" || e.Job.State == "pausing" || e.Job.State == "paused") {
			// The cancellation intent is committed before stopping the worker.
			// A crash before that worker saves its final state must not revive it.
			e.Job.State, e.Job.Error = "cancelled", ""
			e.Job.WaitingReason = "Cancelled; committed records are retained."
			for k := range e.Job.Progress {
				p := &e.Job.Progress[k]
				if p.State != "complete" && p.State != "failed" {
					p.State = "cancelled"
				}
			}
		} else if e.Job.State == "running" || e.Job.State == "waiting" || e.Job.State == "pausing" {
			e.Job.State = "paused"
			e.Job.WaitingReason = "Interrupted by restart. Resume to continue from committed per-index coverage."
		}
	}
	a.indexQueue = state.Entries
	return nil
}

func (a *app) persistIndexQueueLocked() error {
	// Retain bounded terminal history, but never discard outstanding work.
	terminal := 0
	for i := len(a.indexQueue) - 1; i >= 0; i-- {
		s := a.indexQueue[i].Job.State
		if s == "complete" || s == "cancelled" || s == "yielded" {
			terminal++
			if terminal > 64 {
				a.indexQueue = append(a.indexQueue[:i], a.indexQueue[i+1:]...)
			}
		}
	}
	if err := atomicWriteJSON(filepath.Join(a.dataDir, "indexes", "queue.json"), indexQueueState{1, a.indexQueue}); err != nil {
		return err
	}
	return syncDirectory(filepath.Join(a.dataDir, "indexes"))
}

func (a *app) updateQueuedJobLocked(j indexJob) error {
	if err := a.loadIndexQueueLocked(); err != nil {
		return err
	}
	for i := range a.indexQueue {
		if a.indexQueue[i].Job.ID == j.ID {
			a.indexQueue[i].Job = cloneIndexJob(j)
			return a.persistIndexQueueLocked()
		}
	}
	return nil // Existing Live/legacy job, not an admitted manual queue request.
}

func (a *app) indexJobsSnapshot() ([]indexJob, error) {
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	if err := a.loadIndexQueueLocked(); err != nil {
		return nil, err
	}
	a.loadIndexJobLocked()
	rows := []indexJob{}
	position := 0
	currentSeen := false
	for _, e := range a.indexQueue {
		j := cloneIndexJob(e.Job)
		j.QueueManaged = true
		if j.ID == a.indexJob.ID {
			currentSeen = true
		}
		if j.State == "queued" {
			position++
			j.QueuePosition = position
			j.WaitingReason = "Waiting for the shared block writer."
			if a.indexCancel == nil {
				j.WaitingReason = "Ready for the next scheduler turn."
			}
		}
		rows = append(rows, j)
	}
	if !currentSeen && a.indexJob.ID != "" {
		rows = append(rows, cloneIndexJob(a.indexJob))
	}
	return rows, nil
}

func sameIndexWork(a, b indexBuildRequest) bool {
	// Range extent is handled separately; retention and mode are user choices.
	return a.Index == b.Index && a.Mode == b.Mode && a.Retention == b.Retention && a.Live == b.Live && a.LiveConfigured == b.LiveConfigured && a.RetainSatHistory == b.RetainSatHistory && inscriptionIDsEnabled(a.ConventionalIDs) == inscriptionIDsEnabled(b.ConventionalIDs) && reflect.DeepEqual(a.Outputs, b.Outputs) && a.From != nil && b.From != nil
}

// Caller holds indexLiveControlMu across admission and start, preventing policy
// changes or a supervisor tick from overtaking an accepted manual request.
func (a *app) acceptIndexBuild(req indexBuildRequest) (indexJob, error) {
	plan, err := a.planIndexBuild(req)
	if err != nil {
		return indexJob{}, err
	}
	req.From, req.To, req.Mode, req.Retention = &plan.From, &plan.To, plan.Mode, plan.Retention
	req.RetainSatHistory, req.SatHistoryConfigured = plan.RetainSatHistory, true
	req.ConventionalIDs = boolPointer(plan.ConventionalIDs)
	req.Outputs = normalizedIndexOutputs(req)
	if req.Index != "blocks" {
		req.Outputs = nil
	}
	policies, err := a.indexLivePolicies()
	if err != nil {
		return indexJob{}, err
	}
	for _, output := range plan.Outputs {
		if policies[output.Index].Stopped {
			return indexJob{}, fmt.Errorf("%s indexing is off; enable it before queueing this plan", output.Index)
		}
	}
	a.indexMu.Lock()
	if err = a.loadIndexQueueLocked(); err != nil {
		a.indexMu.Unlock()
		return indexJob{}, err
	}
	for i := range a.indexQueue {
		e := &a.indexQueue[i]
		if (e.Job.State != "queued" && e.Job.State != "running" && e.Job.State != "waiting") || !sameIndexWork(e.Request, req) {
			continue
		}
		if plan.From >= e.Job.From && (e.Job.To < 0 || (plan.To >= 0 && e.Job.To >= plan.To)) {
			j := cloneIndexJob(e.Job)
			a.indexMu.Unlock()
			return j, nil
		}
		overlap := (e.Job.To < 0 || plan.From <= e.Job.To+1) && (plan.To < 0 || e.Job.From <= plan.To+1)
		if e.Job.State == "queued" && overlap {
			prior := cloneIndexQueueEntry(*e)
			from := min(e.Job.From, plan.From)
			to := max(e.Job.To, plan.To)
			if e.Job.To < 0 || plan.To < 0 {
				to = -1
			}
			e.Request.From, e.Job.From = &from, from
			e.Request.To, e.Job.To = &to, to
			e.Job.Progress = nil // Recomputed from the merged range when the writer starts.
			if err = a.persistIndexQueueLocked(); err != nil {
				*e = prior
			}
			j := cloneIndexJob(e.Job)
			a.indexMu.Unlock()
			return j, err
		}
	}
	if len(a.indexQueue) >= 192 {
		a.indexMu.Unlock()
		return indexJob{}, fmt.Errorf("index queue is full; finish or cancel outstanding work")
	}
	j := indexJob{ID: fmt.Sprintf("%d", time.Now().UnixNano()), Index: req.Index, State: "queued", From: plan.From, To: plan.To, Height: plan.From - 1, Mode: plan.Mode, Live: req.Live, Retention: plan.Retention, Outputs: req.Outputs, RetainSatHistory: req.RetainSatHistory, ConventionalIDs: req.ConventionalIDs}
	for _, output := range plan.Outputs {
		j.Progress = append(j.Progress, indexOutputProgress{Index: output.Index, State: "queued", From: output.From, To: output.To, Height: output.From - 1})
	}
	a.indexQueue = append(a.indexQueue, cloneIndexQueueEntry(indexQueueEntry{Request: req, Job: j}))
	if err = a.persistIndexQueueLocked(); err != nil {
		a.indexQueue = a.indexQueue[:len(a.indexQueue)-1]
		a.indexMu.Unlock()
		return indexJob{}, err
	}
	active := a.indexCancel != nil
	a.indexMu.Unlock()
	if active {
		return j, nil
	}
	// Always drain in queue order, including requests accepted before restart.
	a.drainIndexQueueLocked()
	rows, err := a.indexJobsSnapshot()
	for _, row := range rows {
		if row.ID == j.ID {
			return row, err
		}
	}
	return j, err
}

func indexJobOwns(j indexJob, id string) bool {
	if j.Index == id {
		return true
	}
	for _, output := range j.Outputs {
		if output == id {
			return true
		}
	}
	return false
}

func (a *app) clearIndexPause(id string) error {
	path := filepath.Join(a.dataDir, "indexes", "pause.json")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var marker struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(b, &marker); err != nil {
		return err
	}
	if marker.ID == id {
		return os.Remove(path)
	}
	return nil
}

// Caller holds indexLiveControlMu. Return true when a reviewed queue item owns
// the next turn, even if it fails validation, so Live cannot jump ahead.
func (a *app) drainIndexQueueLocked() bool {
	if a.updater != nil && a.updater.applying() {
		return false
	}
	if a.network != nil && a.network.ctx.Err() != nil {
		return false
	}
	attempted := false
	for attempts := 0; attempts < 192; attempts++ {
		a.indexMu.Lock()
		if a.indexCancel != nil {
			a.indexMu.Unlock()
			return true
		}
		if a.loadIndexQueueLocked() != nil {
			a.indexMu.Unlock()
			return true
		}
		var next indexQueueEntry
		for _, entry := range a.indexQueue {
			if entry.Job.State == "queued" {
				next = cloneIndexQueueEntry(entry)
				break
			}
		}
		a.indexMu.Unlock()
		if next.Job.ID == "" {
			return attempted
		}
		attempted = true
		policies, err := a.indexLivePolicies()
		if err == nil {
			for _, id := range append([]string{next.Request.Index}, next.Request.Outputs...) {
				if policies[id].Stopped {
					err = fmt.Errorf("%s indexing is off; enable it and resume this queued plan", id)
					break
				}
			}
		}
		if err == nil {
			_, err = a.startIndexBuildID(next.Request, next.Request.Live, next.Job.ID)
		}
		if err != nil {
			next.Job.State, next.Job.Error = "failed", err.Error()
			a.indexMu.Lock()
			persistErr := a.updateQueuedJobLocked(next.Job)
			a.indexMu.Unlock()
			if persistErr != nil {
				return true
			}
			continue
		}
		return true
	}
	return attempted
}

func (a *app) indexBuildFinished() {
	a.indexMu.Lock()
	a.indexCancel = nil
	// A reviewed Live request hands future retries to its durable policy after
	// yielding the writer. Its original queue entry must not look active forever.
	if a.indexJob.Live && a.indexJob.State == "waiting" {
		for _, entry := range a.indexQueue {
			if entry.Job.ID == a.indexJob.ID {
				j := cloneIndexJob(a.indexJob)
				j.State = "yielded"
				j.WaitingReason = "Live will retry through its saved policy: " + j.Error
				if err := atomicWriteJSON(filepath.Join(a.dataDir, "indexes", "job.json"), j); err == nil {
					a.indexJob = cloneIndexJob(j)
					_ = a.updateQueuedJobLocked(j)
				}
				break
			}
		}
	}
	a.indexMu.Unlock()
	a.indexLiveControlMu.Lock()
	defer a.indexLiveControlMu.Unlock()
	a.drainIndexQueueLocked()
}

func (a *app) controlIndexQueue(req indexQueueRequest) (indexJob, error) {
	a.indexLiveControlMu.Lock()
	defer a.indexLiveControlMu.Unlock()
	a.indexMu.Lock()
	if err := a.loadIndexQueueLocked(); err != nil {
		a.indexMu.Unlock()
		return indexJob{}, err
	}
	idx := -1
	for i, e := range a.indexQueue {
		if e.Job.ID == req.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.indexMu.Unlock()
		return indexJob{}, fmt.Errorf("unknown queued job")
	}
	e := &a.indexQueue[idx]
	if req.Action == "resume" {
		if err := requireReleaseIndexRequest(e.Request); err != nil {
			a.indexMu.Unlock()
			return indexJob{}, err
		}
	}
	active := a.indexCancel != nil && a.indexJob.ID == req.ID
	if active {
		if req.Action != "pause" && req.Action != "cancel" {
			a.indexMu.Unlock()
			return indexJob{}, fmt.Errorf("pause the active job before changing its queue state")
		}
		// Cancellation stops work without deleting committed outputs. A paused
		// checkpoint is retained; cancellation never destroys derived data.
		// Pause the durable Live policy first, so a crash at any later point
		// cannot restart a cancelled request through the maintenance supervisor.
		if err := a.pauseLivePolicyForJob(e.Job.Index); err != nil {
			j := cloneIndexJob(e.Job)
			a.indexMu.Unlock()
			return j, err
		}
		if req.Action == "cancel" {
			priorStopState := e.StopState
			e.StopState = "cancelled"
			if err := a.persistIndexQueueLocked(); err != nil {
				e.StopState = priorStopState
				a.indexMu.Unlock()
				return indexJob{}, err
			}
		}
		a.indexCancel()
		j := cloneIndexJob(a.indexJob)
		j.State = "pausing"
		a.indexMu.Unlock()
		return j, nil
	}
	prior := make([]indexQueueEntry, len(a.indexQueue))
	for i := range a.indexQueue {
		prior[i] = cloneIndexQueueEntry(a.indexQueue[i])
	}
	switch req.Action {
	case "pause":
		if e.Job.State != "queued" && e.Job.State != "paused" {
			j := cloneIndexJob(e.Job)
			a.indexMu.Unlock()
			return j, fmt.Errorf("only queued or active jobs can be paused")
		}
		e.Job.State, e.Job.WaitingReason = "paused", "Paused by you."
	case "cancel":
		e.Job.State, e.Job.WaitingReason = "cancelled", "Cancelled; committed records are retained."
	case "resume":
		if e.Job.State != "paused" && e.Job.State != "failed" {
			j := cloneIndexJob(e.Job)
			a.indexMu.Unlock()
			return j, fmt.Errorf("only paused or failed jobs can resume")
		}
		e.Job.State, e.Job.Error, e.Job.WaitingReason = "queued", "", ""
		e.StopState = ""
	case "up", "down":
		if e.Job.State != "queued" {
			j := cloneIndexJob(e.Job)
			a.indexMu.Unlock()
			return j, fmt.Errorf("only queued jobs can be reordered")
		}
		delta := 1
		if req.Action == "up" {
			delta = -1
		}
		for other := idx + delta; other >= 0 && other < len(a.indexQueue); other += delta {
			if a.indexQueue[other].Job.State == "queued" {
				a.indexQueue[idx], a.indexQueue[other] = a.indexQueue[other], a.indexQueue[idx]
				idx = other
				break
			}
		}
	default:
		a.indexMu.Unlock()
		return indexJob{}, fmt.Errorf("action must be pause, resume, cancel, up or down")
	}
	j := cloneIndexJob(a.indexQueue[idx].Job)
	err := a.persistIndexQueueLocked()
	if err != nil {
		a.indexQueue = prior
	}
	a.indexMu.Unlock()
	if err == nil {
		a.drainIndexQueueLocked()
	}
	return j, err
}
