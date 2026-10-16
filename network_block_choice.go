package main

import (
	"sort"
	"time"
)

// Serving performance is local, transient transport evidence, not a claim of
// chain validity. Failed requests do not disable this peer's header service.
type bitcoinBlockWork struct {
	active     int
	successes  uint64
	failures   int
	latency    time.Duration
	retryAfter time.Time
}

// Reserve before releasing mu so concurrent prefetches prefer different idle
// sessions. The existing session slot still serializes replies with headers.
func (n *bitcoinNetwork) reserveBlockSession(tried map[string]bool) *bitcoinSession {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.now()
	rows := make([]*bitcoinSession, 0, len(n.sessions))
	for _, s := range n.sessions {
		if s.transportEligibleLocked(true) && !tried[s.peer.addr] && !s.blockWork.retryAfter.After(now) {
			rows = append(rows, s)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		costA, costB := blockSessionCost(a), blockSessionCost(b)
		if costA != costB {
			return costA < costB
		}
		if !a.view.Connected.Equal(b.view.Connected) {
			return a.view.Connected.Before(b.view.Connected)
		}
		return a.peer.addr < b.peer.addr
	})
	if len(rows) == 0 {
		return nil
	}
	rows[0].blockWork.active++
	return rows[0]
}

func blockSessionCost(s *bitcoinSession) time.Duration {
	w := s.blockWork
	base := 750 * time.Millisecond // allow an untried archival server a chance
	if w.successes > 0 {
		base = w.latency
	}
	base += time.Duration(w.active)*15*time.Second + time.Duration(w.failures)*5*time.Second
	// A short header exchange should not make a previously failing peer look
	// better than a proven fast server. Pending block reservations cost more.
	if w.active == 0 && len(s.slot) > 0 {
		base += 500 * time.Millisecond
	}
	return base
}

func (n *bitcoinNetwork) finishBlockSession(s *bitcoinSession, elapsed time.Duration, success, penalize bool) {
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
		if w.latency == 0 {
			w.latency = elapsed
		} else {
			w.latency = (3*w.latency + elapsed) / 4
		}
	} else if penalize {
		w.failures++
		// A timeout or notfound must not cost another full timeout on the next
		// adjacent block. This cooldown expires and never poisons header use.
		w.retryAfter = n.now().Add(time.Duration(min(w.failures, 6)) * 5 * time.Second)
	}
}
