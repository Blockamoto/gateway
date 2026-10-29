package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// A progress poll must not reconstruct every disk-backed presence projection.
// Full status and all actual queries keep their existing validation paths.
const indexStatusDetailInterval = 30 * time.Second
const indexStatusPollBudget = 8 << 20

type indexStatusObservation struct {
	Mode                          string `json:"mode"`
	DetailsObservedAt             string `json:"details_observed_at"`
	DetailsAgeMilliseconds        int64  `json:"details_age_milliseconds"`
	DetailsRefreshing             bool   `json:"details_refreshing"`
	DetailsRefreshIntervalSeconds int    `json:"details_refresh_interval_seconds"`
}

type indexStatusPollCache struct {
	mu         sync.Mutex
	buildMu    sync.Mutex
	raw        []byte
	at         time.Time
	completed  time.Time // cooldown is measured separately from the observation age
	err        error
	refreshing bool
	done       chan struct{}
	build      func() indexStatusView // fixed before use in isolated tests only
}

func statusObservation(mode string, at time.Time, refreshing bool) *indexStatusObservation {
	age := time.Since(at).Milliseconds()
	if age < 0 {
		age = 0
	}
	return &indexStatusObservation{Mode: mode, DetailsObservedAt: at.UTC().Format(time.RFC3339Nano), DetailsAgeMilliseconds: age, DetailsRefreshing: refreshing, DetailsRefreshIntervalSeconds: int(indexStatusDetailInterval / time.Second)}
}

func (a *app) refreshIndexStatusProgress(out *indexStatusView) {
	out.Run = a.indexRunSnapshot()
	out.Job = a.indexJobSnapshot()
	if jobs, err := a.indexJobsSnapshot(); err == nil {
		out.Jobs = jobs
	}
	out.Browse = resolvedIngestionCapabilities(out.Run)
	a.settingsMu.RLock()
	networkEnabled := !a.settings.NetworkDisabled
	a.settingsMu.RUnlock()
	peersAvailable := networkEnabled && a.network != nil && len(a.network.listSessions()) > 0
	for n := range out.Providers {
		if out.Providers[n].ID == "blocks" && out.Providers[n].Provider == "gateway_cache" {
			out.Providers[n].OnDemand = peersAvailable
		}
	}
}

func (a *app) indexStatusSnapshot() indexStatusView {
	c := &a.indexStatusPoll
	c.buildMu.Lock()
	defer c.buildMu.Unlock()
	at := time.Now()
	var out indexStatusView
	if c.build != nil {
		out = c.build()
	} else {
		out = a.indexStatus()
	}
	a.refreshIndexStatusProgress(&out)
	out.Observation = statusObservation("details", at, false)
	raw, err := json.Marshal(out)
	if err == nil && len(raw) > indexStatusPollBudget {
		err = fmt.Errorf("detailed index status exceeds the bounded progress snapshot budget")
	}
	c.mu.Lock()
	// A slow build must not be immediately due again. Preserve its start time
	// as the honest age of the observed details, even when publication fails.
	c.completed = time.Now()
	if err == nil {
		c.raw = raw
		c.at = at
	}
	c.err = err
	c.mu.Unlock()
	return out
}

func (a *app) startIndexStatusRefreshLocked() {
	c := &a.indexStatusPoll
	c.refreshing = true
	c.done = make(chan struct{})
	go func() {
		a.indexStatusSnapshot()
		c.mu.Lock()
		c.refreshing = false
		close(c.done)
		c.mu.Unlock()
	}()
}

func (a *app) indexProgressStatus(ctx context.Context) (indexStatusView, error) {
	c := &a.indexStatusPoll
	for {
		if err := ctx.Err(); err != nil {
			return indexStatusView{}, err
		}
		c.mu.Lock()
		if len(c.raw) == 0 {
			if c.err != nil && time.Since(c.completed) < indexStatusDetailInterval {
				err := c.err
				c.mu.Unlock()
				return indexStatusView{}, err
			}
			if !c.refreshing {
				c.err = nil
				a.startIndexStatusRefreshLocked()
			}
			done := c.done
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return indexStatusView{}, ctx.Err()
			case <-done:
				continue
			}
		}
		if time.Since(c.completed) >= indexStatusDetailInterval && !c.refreshing {
			a.startIndexStatusRefreshLocked()
		}
		raw, at, refreshing := c.raw, c.at, c.refreshing
		c.mu.Unlock()
		var out indexStatusView
		if err := json.Unmarshal(raw, &out); err != nil {
			return out, err
		}
		a.refreshIndexStatusProgress(&out)
		out.Observation = statusObservation("progress", at, refreshing)
		return out, nil
	}
}
