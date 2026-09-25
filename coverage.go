package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type heightInterval struct {
	From int64 `json:"from"`
	To   int64 `json:"to"` // inclusive
}

type coverageState struct {
	Schema        int              `json:"schema"`
	IndexedBlocks []heightInterval `json:"indexed_blocks"`
	TxLocation    []heightInterval `json:"tx_location"`
	Spender       []heightInterval `json:"spender"`
	UpdatedAt     string           `json:"updated_at,omitempty"`
}

func newCoverageState() coverageState { return coverageState{Schema: 1} }
func (a *app) coveragePath() string   { return filepath.Join(a.dataDir, "coverage.json") }

func mergeHeight(intervals []heightInterval, h int64) []heightInterval {
	if h < 0 {
		return intervals
	}
	intervals = append(intervals, heightInterval{From: h, To: h})
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].From < intervals[j].From })
	out := make([]heightInterval, 0, len(intervals))
	for _, cur := range intervals {
		if len(out) == 0 || cur.From > out[len(out)-1].To+1 {
			out = append(out, cur)
		} else if cur.To > out[len(out)-1].To {
			out[len(out)-1].To = cur.To
		}
	}
	return out
}

func (a *app) loadCoverage() {
	c := newCoverageState()
	if b, err := os.ReadFile(a.coveragePath()); err == nil {
		var x coverageState
		if json.Unmarshal(b, &x) == nil && x.Schema == 1 {
			c = x
		}
	}
	a.coverageMu.Lock()
	a.coverage = c
	a.coverageMu.Unlock()
}

func (a *app) saveCoverage() error {
	a.coverageMu.RLock()
	c := a.coverage
	a.coverageMu.RUnlock()
	c.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.coveragePath(), b, 0644)
}

func (a *app) markIndexedHeight(h int64) {
	a.coverageMu.Lock()
	a.coverage.IndexedBlocks = mergeHeight(a.coverage.IndexedBlocks, h)
	a.coverage.TxLocation = mergeHeight(a.coverage.TxLocation, h)
	// Native spender coverage is advanced only after graphIndexBlock commits
	// the block into the dedicated graph store. Range materialization itself
	// must not claim negative-proof graph coverage.
	a.coverage.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	a.coverageMu.Unlock()
	_ = a.saveCoverage()
}

func (a *app) coverageView() map[string]any {
	a.coverageMu.RLock()
	c := a.coverage
	a.coverageMu.RUnlock()
	st := a.getStatus()
	cacheBytes, cacheBlocks := a.cacheStats()
	return map[string]any{
		"schema": c.Schema,
		"headers": func() []heightInterval {
			if st.HeaderHeight >= 0 {
				return []heightInterval{{From: 0, To: st.HeaderHeight}}
			}
			return nil
		}(),
		"indexed_blocks":    c.IndexedBlocks,
		"tx_location":       c.TxLocation,
		"spender":           c.Spender,
		"raw_cached_blocks": cacheBlocks,
		"raw_cached_bytes":  cacheBytes,
		"updated_at":        c.UpdatedAt,
	}
}
