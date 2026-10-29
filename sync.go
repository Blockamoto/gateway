package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type syncJobState struct {
	Mode        string `json:"mode"` // range|full|latest
	From        int64  `json:"from"`
	To          int64  `json:"to"` // snapshot, inclusive
	SnapshotTip int64  `json:"snapshot_tip"`
	Follow      bool   `json:"follow"`
	Current     int64  `json:"current"` // last completed height; From-1 before start
	Processed   int64  `json:"processed"`
	Running     bool   `json:"running"`
	Complete    bool   `json:"complete"`
	Error       string `json:"error,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

type syncRequest struct {
	Mode       string `json:"mode"`
	FromHeight int64  `json:"from_height"`
	ToHeight   int64  `json:"to_height"`
	Follow     bool   `json:"follow"`
}

func (a *app) syncStatePath() string { return filepath.Join(a.dataDir, "sync-state.json") }

func (a *app) loadSyncState() {
	var st syncJobState
	if b, err := os.ReadFile(a.syncStatePath()); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	a.syncMu.Lock()
	a.syncJob = st
	a.syncMu.Unlock()
}
func (a *app) saveSyncState() error {
	a.syncMu.RLock()
	st := a.syncJob
	a.syncMu.RUnlock()
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.syncStatePath(), b, 0644)
}
func (a *app) getSyncState() syncJobState {
	a.syncMu.RLock()
	defer a.syncMu.RUnlock()
	return a.syncJob
}

func (a *app) snapshotTip() (int64, error) {
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	best := a.getStatus().HeaderHeight
	if c := inspectCore(settings); c.Connected && c.Height > best {
		best = c.Height
	}
	for _, p := range a.getOverlayPeers() {
		if p.HeaderHeight > best {
			best = p.HeaderHeight
		}
	}
	if best >= 0 {
		return best, nil
	}
	return -1, fmt.Errorf("no validated tip is currently known")
}

func (a *app) startSync(req syncRequest) error {
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "range"
	}
	tip, err := a.snapshotTip()
	if err != nil {
		return err
	}
	from, to := req.FromHeight, req.ToHeight
	switch mode {
	case "full":
		// 0.4.0 treats mounted Core + BOD-owned blocks as one logical store.
		// A full snapshot therefore starts at the first height not already held
		// continuously from genesis instead of pointlessly replaying Core's store.
		from, to = a.unifiedContinuationHeight(), tip
	case "latest":
		if from < 0 {
			from = a.unifiedContinuationHeight()
		}
		to = tip
	case "range":
		if from < 0 || to < 0 {
			return fmt.Errorf("range heights cannot be negative")
		}
	default:
		return fmt.Errorf("sync mode must be range, latest, or full")
	}
	if to > tip {
		return fmt.Errorf("requested end height %d is beyond snapshot tip %d", to, tip)
	}
	if from > tip {
		a.syncMu.Lock()
		if a.syncJob.Running {
			a.syncMu.Unlock()
			return fmt.Errorf("a sync job is already running")
		}
		a.syncJob = syncJobState{Mode: mode, From: from, To: tip, SnapshotTip: tip, Follow: req.Follow, Current: tip, Running: false, Complete: true, StartedAt: time.Now().UTC().Format(time.RFC3339), UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
		a.syncMu.Unlock()
		_ = a.saveSyncState()
		return nil
	}
	if to < from {
		return fmt.Errorf("end height must be >= start height")
	}

	a.syncMu.Lock()
	if a.syncJob.Running {
		a.syncMu.Unlock()
		return fmt.Errorf("a sync job is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.syncCancel = cancel
	a.syncJob = syncJobState{Mode: mode, From: from, To: to, SnapshotTip: tip, Follow: req.Follow, Current: from - 1, Running: true, StartedAt: time.Now().UTC().Format(time.RFC3339), UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	a.syncMu.Unlock()
	_ = a.saveSyncState()
	a.startUpdateAwareWorker(func() { a.runSync(ctx) })
	return nil
}

func (a *app) resumeSyncIfNeeded() {
	st := a.getSyncState()
	if !st.Running || st.Complete {
		return
	}
	a.syncMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	a.syncCancel = cancel
	a.syncMu.Unlock()
	a.startUpdateAwareWorker(func() { time.Sleep(1500 * time.Millisecond); a.runSync(ctx) })
}

func (a *app) stopSync() {
	a.syncMu.Lock()
	if a.syncCancel != nil {
		a.syncCancel()
		a.syncCancel = nil
	}
	a.syncJob.Running = false
	a.syncJob.Error = "stopped by user"
	a.syncJob.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	a.syncMu.Unlock()
	_ = a.saveSyncState()
}

func (a *app) runSync(ctx context.Context) {
	for {
		a.syncMu.RLock()
		st := a.syncJob
		a.syncMu.RUnlock()
		start := st.Current + 1
		if start < st.From {
			start = st.From
		}
		for h := start; h <= st.To; h++ {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Range/full materialization advances only after an acceptable chain
			// authority can anchor the requested height. A live Bitcoin Core active
			// chain is authoritative immediately; otherwise the independent Bitcoin
			// on Demand header mirror supplies that anchor.
			if err := a.waitForHeaderCoverage(ctx, h); err != nil {
				if ctx.Err() != nil {
					return
				}
				a.failSync(err)
				return
			}

			view, err := a.fetchAndDecode(strconv.FormatInt(h, 10))
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				a.failSync(err)
				return
			}
			if !view.Verification.HeaderChainMatch && !view.Verification.ConsensusValidated {
				a.failSync(fmt.Errorf("block %d could not be anchored by Bitcoin Core or the independent Bitcoin on Demand header mirror; range coverage did not advance", h))
				return
			}
			a.markIndexedHeight(h)
			a.syncMu.Lock()
			a.syncJob.Current = h
			a.syncJob.Processed++
			a.syncJob.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			a.syncMu.Unlock()
			_ = a.saveSyncState()
		}
		if !st.Follow {
			a.syncMu.Lock()
			a.syncJob.Running = false
			a.syncJob.Complete = true
			a.syncJob.Error = ""
			a.syncJob.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			a.syncMu.Unlock()
			_ = a.saveSyncState()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
		tip, err := a.snapshotTip()
		if err != nil {
			continue
		}
		a.syncMu.Lock()
		if tip > a.syncJob.To {
			a.syncJob.To = tip
			a.syncJob.SnapshotTip = tip
			a.syncJob.Complete = false
		}
		a.syncMu.Unlock()
		_ = a.saveSyncState()
	}
}
func (a *app) waitForHeaderCoverage(ctx context.Context, h int64) error {
	for {
		if a.heightHasChainAuthority(h) {
			return nil
		}
		st := a.getStatus()
		// If live mirror sync has stopped below the requested height and there is
		// no Core authority, surface the failure instead of waiting forever.
		if !st.Syncing && st.Error != "" {
			return fmt.Errorf("chain authority stopped at %d before requested height %d: %s", st.HeaderHeight, h, st.Error)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (a *app) failSync(err error) {
	a.syncMu.Lock()
	a.syncJob.Running = false
	a.syncJob.Complete = false
	a.syncJob.Error = err.Error()
	a.syncJob.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	a.syncMu.Unlock()
	_ = a.saveSyncState()
}
