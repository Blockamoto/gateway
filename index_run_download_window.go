package main

import "context"

type indexRunDownloadRange struct{ from, to int64 }
type indexRunWindowTarget struct {
	target blockTarget
	local  bool
}
type indexRunDownloadWindow struct {
	a           *app
	ctx         context.Context
	window      *bitcoinBlockWindow
	authority   chainAuthorityView
	known       map[int64]indexRunWindowTarget
	localSource func(blockTarget) bool // optional deterministic storage-read test seam
}

func (w *indexRunDownloadWindow) Reset() {
	if w.window != nil {
		w.window.Close()
		w.window = nil
	}
	w.known = nil
}

// Only verified peer-source runs offer lookahead hints. Core/local sources keep
// their ordinary source precedence, and Chain-only work supplies no ranges.
// The logical target queue contains headers, never decoded block views.
func (w *indexRunDownloadWindow) Update(authority chainAuthorityView, ranges []indexRunDownloadRange) {
	if w.a.network == nil || w.a.indexRunSourceBlock != nil || authority.Source != "bod_headers" || len(ranges) == 0 {
		w.Reset()
		return
	}
	if w.authority.Source != authority.Source || w.authority.Hash != authority.Hash || w.authority.Height != authority.Height {
		w.Reset()
		w.authority = authority
	}
	if w.known == nil {
		w.known = map[int64]indexRunWindowTarget{}
	}
	wanted := map[int64]bool{}
	quota := max(1, coreBlockWindow/len(ranges))
	for _, r := range ranges {
		for h := r.from; h <= r.to && h < r.from+int64(quota) && len(wanted) < coreBlockWindow; h++ {
			wanted[h] = true
		}
	}
	for h := range w.known {
		if !wanted[h] {
			delete(w.known, h)
		}
	}
	targets := make([]blockTarget, 0, len(wanted))
	localAvailable := w.a.hasLocalBlockSource
	if w.localSource != nil {
		localAvailable = w.localSource
	}
	for h := range wanted {
		if w.ctx.Err() != nil {
			w.Reset()
			return
		}
		entry, ok := w.known[h]
		if !ok {
			t, err := w.a.utxoSelectedTarget(w.ctx, authority, h)
			if err != nil {
				w.Reset()
				return
			}
			entry = indexRunWindowTarget{target: t, local: localAvailable(t)}
			w.known[h] = entry
		} else if !entry.local && (w.window == nil || !w.window.d.hasTargetEntry(entry.target)) {
			// A completed speculative response can be released under byte
			// pressure after another acquisition retained its verified Raw body.
			// Refresh only missing/retired jobs, outside the downloader lock;
			// active jobs keep their memoized hint without per-block disk reads.
			entry.local = localAvailable(entry.target)
			w.known[h] = entry
		}
		if !entry.local {
			targets = append(targets, entry.target)
		}
	}
	if w.window == nil {
		w.window = w.a.network.newBlockWindow(w.ctx)
	}
	w.window.Update(targets)
}
