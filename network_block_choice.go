package main

import (
	"sort"
	"time"
)

// Download observations are diagnostics, never a fastest-peer trust/ranking rule.
// Capacity is refilled across eligible peers as in Core v31.1 SendMessages.
type bitcoinBlockWork struct {
	active         int
	successes      uint64
	failures       int
	latency        time.Duration
	retryAfter     time.Time // retained in diagnostic compatibility; no score cooldown
	missing        map[[32]byte]bool
	bytes          uint64
	failuresTotal  uint64
	transfer       time.Duration
	selectedHeight int64
	selectedHash   string
}

func (n *bitcoinNetwork) reserveBlockSession(tried map[string]bool) *bitcoinSession {
	return n.reserveBlockTarget(tried, blockTarget{Height: 0})
}
func blockTargetEligible(s *bitcoinSession, t blockTarget) bool {
	if !s.transportEligibleLocked(false) {
		return false
	}
	if (t.Height < 0 || t.Height >= 481824) && s.peer.services&nodeWitnessService == 0 {
		return false
	}
	if s.blockWork.selectedHash != "" && t.Height > s.blockWork.selectedHeight {
		return false
	}
	if s.peer.services&nodeNetworkService != 0 {
		return true
	}
	// NODE_NETWORK_LIMITED only promises the latest288 blocks; Core leaves a
	// two-block race buffer. Unknown heights cannot use a limited service.
	tip := s.view.TargetHeight
	return t.Height >= 0 && tip >= t.Height && tip-t.Height < 286 && s.peer.services&nodeNetworkLimitedService != 0
}
func (n *bitcoinNetwork) reserveBlockTarget(tried map[string]bool, t blockTarget) *bitcoinSession {
	n.mu.Lock()
	defer n.mu.Unlock()
	rows := []*bitcoinSession{}
	for _, s := range n.sessions {
		if blockTargetEligible(s, t) && !tried[s.peer.addr] && !s.blockWork.missing[t.HashRaw] && s.blockWork.active < coreBlocksPerPeer && !s.blockRequestPending(t.HashRaw) {
			rows = append(rows, s)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].blockWork.active != rows[j].blockWork.active {
			return rows[i].blockWork.active < rows[j].blockWork.active
		}
		return rows[i].peer.addr < rows[j].peer.addr
	})
	if len(rows) == 0 {
		return nil
	}
	// Round-robin ties rather than ranking ping, last-success age, or throughput.
	equal := 1
	for equal < len(rows) && rows[equal].blockWork.active == rows[0].blockWork.active {
		equal++
	}
	s := rows[n.blockCursor%uint64(equal)]
	n.blockCursor++
	s.blockWork.active++
	return s
}
func (n *bitcoinNetwork) hasUntriedBlockProvider(tried map[string]bool, t blockTarget) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, s := range n.sessions {
		if !tried[s.peer.addr] && !s.blockWork.missing[t.HashRaw] && blockTargetEligible(s, t) {
			return true
		}
	}
	return false
}
func (n *bitcoinNetwork) finishBlockSession(s *bitcoinSession, elapsed time.Duration, success, failed bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	w := &s.blockWork
	if w.active > 0 {
		w.active--
	}
	if success {
		w.successes++
		w.failures = 0
		w.retryAfter = time.Time{}
		w.latency = elapsed
		w.transfer += elapsed
	} else if failed {
		w.failures++
		w.failuresTotal++
	}
}

// Gateway keeps one selected verified header file, not Core's per-peer block
// index/ancestor graph. Record only a batch the header validator actually
// joined to that file. Unknown archival peers remain candidates for known-hash
// requests; a version-message height is never promoted to this proof.
func (n *bitcoinNetwork) noteSelectedPeerHeaders(s *bitcoinSession, height int64, hash string) {
	if height < 0 || !validHash(hash) {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if height > s.blockWork.selectedHeight || hash != s.blockWork.selectedHash {
		s.blockWork.missing = nil
	}
	s.blockWork.selectedHeight = height
	s.blockWork.selectedHash = hash
	if height > s.view.TargetHeight {
		s.view.TargetHeight = height
	}
}
