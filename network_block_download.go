package main

// Block acquisition follows Bitcoin Core v31.1 (9be056a8a72b624dae9623b2f7bded92c2a21c91),
// src/net_processing.cpp: MAX_BLOCKS_IN_TRANSIT_PER_PEER, BLOCK_DOWNLOAD_WINDOW,
// FindNextBlocksToDownload, and SendMessages. This is a download policy, not a
// replacement for Gateway's selected-chain and consumer publication checks.
import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	coreBlocksPerPeer     = 16
	coreBlockWindow       = 1024
	coreBlockMessageBytes = 4_000_000
	// Unlike Core's block-file buffering, Gateway may discard every acquired
	// body. Reserve the worst-case serialized size before dispatch, including
	// completed out-of-order replies. Decoding happens only at consumption.
	gatewayBlockDownloadBudget = 64 << 20
)

type bitcoinBlockDownload struct {
	target      blockTarget
	done        chan struct{}
	ctx         context.Context
	cancel      context.CancelFunc
	owners      map[uint64]bool
	waiters     int
	running     bool
	finished    bool
	reservation int64
	tried       map[string]bool
	session     *bitcoinSession
	dispatched  time.Time
	stalling    time.Time
	raw         []byte
	provider    string
	err         error
}

type bitcoinBlockDownloads struct {
	n            *bitcoinNetwork
	mu           sync.Mutex
	jobs         map[[32]byte]*bitcoinBlockDownload
	nextOwner    uint64
	reserved     int64
	budget       int64
	wake         chan struct{}
	stallTimeout time.Duration
	requests     uint64
	responses    uint64
	bytes        uint64
	transfer     time.Duration
	stalls       uint64
}

type bitcoinBlockWindow struct {
	d      *bitcoinBlockDownloads
	id     uint64
	ctx    context.Context
	stop   func() bool
	mu     sync.Mutex
	closed bool
}

func (n *bitcoinNetwork) blockDownloader() *bitcoinBlockDownloads {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.blockDownloads == nil {
		d := &bitcoinBlockDownloads{n: n, jobs: map[[32]byte]*bitcoinBlockDownload{}, budget: gatewayBlockDownloadBudget, wake: make(chan struct{}, 1), stallTimeout: 2 * time.Second}
		n.blockDownloads = d
		go d.run()
	}
	return n.blockDownloads
}

func (d *bitcoinBlockDownloads) signal() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func (d *bitcoinBlockDownloads) newJob(t blockTarget) *bitcoinBlockDownload {
	t.ExpectedHeader = append([]byte(nil), t.ExpectedHeader...)
	ctx, cancel := context.WithCancel(d.n.ctx)
	j := &bitcoinBlockDownload{target: t, done: make(chan struct{}), ctx: ctx, cancel: cancel, owners: map[uint64]bool{}, tried: map[string]bool{}}
	d.jobs[t.HashRaw] = j
	return j
}

func (d *bitcoinBlockDownloads) releaseLocked(j *bitcoinBlockDownload) {
	if d.jobs[j.target.HashRaw] != j {
		return
	}
	delete(d.jobs, j.target.HashRaw)
	j.cancel()
	if !j.running {
		d.reserved -= j.reservation
		j.reservation = 0
	}
}

func (d *bitcoinBlockDownloads) hasTargetEntry(t blockTarget) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	j := d.jobs[t.HashRaw]
	// Update retains existing entries, including failures, without scheduling
	// another request. Only an absent entry can restart speculative acquisition.
	return j != nil &&
		(len(j.target.ExpectedHeader) != 80 || len(t.ExpectedHeader) != 80 || bytes.Equal(j.target.ExpectedHeader, t.ExpectedHeader))
}

func (n *bitcoinNetwork) fetchTargetBlockContext(ctx context.Context, t blockTarget) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if !n.enabled() {
		return nil, "", fmt.Errorf("WAITING_FOR_PEERS: outbound networking is disabled")
	}
	d := n.blockDownloader()
	d.mu.Lock()
	j := d.jobs[t.HashRaw]
	if (j == nil || !j.finished || j.err != nil) && !n.hasUntriedBlockProvider(nil, t) {
		d.mu.Unlock()
		return nil, "", fmt.Errorf("WAITING_FOR_PEERS: no connected peer can supply this known block")
	}
	if j != nil && (j.finished && j.err != nil || j.ctx.Err() != nil) {
		owners := j.owners
		d.releaseLocked(j)
		j = d.newJob(t)
		j.owners = owners
	}
	if j == nil {
		if len(d.jobs) >= coreBlockWindow+coreBlocksPerPeer {
			d.mu.Unlock()
			return nil, "", fmt.Errorf("WAITING_FOR_PEERS: bounded block request queue is full")
		}
		j = d.newJob(t)
	}
	if len(j.target.ExpectedHeader) == 80 && len(t.ExpectedHeader) == 80 && !bytes.Equal(j.target.ExpectedHeader, t.ExpectedHeader) {
		d.mu.Unlock()
		return nil, "", fmt.Errorf("conflicting selected block header")
	}
	if !j.running && j.target.Height < 0 && t.Height >= 0 {
		j.target.Height = t.Height
	}
	j.waiters++
	d.mu.Unlock()
	d.signal()
	defer func() {
		d.mu.Lock()
		j.waiters--
		if j.waiters == 0 && len(j.owners) == 0 {
			d.releaseLocked(j)
		}
		d.mu.Unlock()
		d.signal()
	}()
	select {
	case <-ctx.Done():
		return nil, "", ctx.Err()
	case <-n.ctx.Done():
		return nil, "", fmt.Errorf("network stopped")
	case <-j.ctx.Done():
		if !n.enabled() {
			return nil, "", fmt.Errorf("WAITING_FOR_PEERS: outbound networking is disabled")
		}
		return nil, "", j.ctx.Err()
	case <-j.done:
		d.mu.Lock()
		raw, provider, err := j.raw, j.provider, j.err
		if err == nil && d.stallTimeout > 2*time.Second {
			d.stallTimeout = max(2*time.Second, time.Duration(float64(d.stallTimeout)*0.85))
		}
		d.mu.Unlock()
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return raw, provider, err
	}
}

func (n *bitcoinNetwork) newBlockWindow(ctx context.Context) *bitcoinBlockWindow {
	d := n.blockDownloader()
	d.mu.Lock()
	d.nextOwner++
	id := d.nextOwner
	d.mu.Unlock()
	w := &bitcoinBlockWindow{d: d, id: id, ctx: ctx}
	w.mu.Lock()
	w.stop = context.AfterFunc(ctx, w.Close)
	w.mu.Unlock()
	return w
}

// Update describes missing bodies in the bounded forward window. An explicit
// request joins these same jobs, with priority over speculative acquisition.
// Removing an owner cancels only work nobody else is awaiting.
func (w *bitcoinBlockWindow) Update(targets []blockTarget) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.ctx.Err() != nil {
		return
	}
	d := w.d
	d.mu.Lock()
	wanted := map[[32]byte]blockTarget{}
	for _, t := range targets {
		if len(wanted) >= coreBlockWindow {
			break
		}
		wanted[t.HashRaw] = t
	}
	for key, j := range d.jobs {
		if j.owners[w.id] {
			if _, ok := wanted[key]; !ok {
				delete(j.owners, w.id)
				if len(j.owners) == 0 && j.waiters == 0 {
					d.releaseLocked(j)
				}
			}
		}
	}
	for key, t := range wanted {
		j := d.jobs[key]
		if j == nil {
			if len(d.jobs) >= coreBlockWindow {
				continue
			}
			j = d.newJob(t)
		}
		j.owners[w.id] = true
	}
	d.mu.Unlock()
	d.signal()
}

func (w *bitcoinBlockWindow) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	if w.stop != nil {
		w.stop()
	}
	d := w.d
	d.mu.Lock()
	for _, j := range d.jobs {
		delete(j.owners, w.id)
		if len(j.owners) == 0 && j.waiters == 0 {
			d.releaseLocked(j)
		}
	}
	d.mu.Unlock()
	d.signal()
}

func (d *bitcoinBlockDownloads) run() {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-d.n.ctx.Done():
			d.mu.Lock()
			for _, j := range d.jobs {
				j.cancel()
			}
			d.mu.Unlock()
			return
		case <-d.wake:
		case <-tick.C:
		}
		d.pump()
	}
}

func (d *bitcoinBlockDownloads) pump() {
	if !d.n.enabled() {
		d.mu.Lock()
		for _, j := range d.jobs {
			j.cancel()
		}
		d.mu.Unlock()
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.n.now()
	jobs := make([]*bitcoinBlockDownload, 0, len(d.jobs))
	ready := 0
	for _, j := range d.jobs {
		jobs = append(jobs, j)
		if j.finished && j.err == nil {
			ready++
		}
	}
	sort.Slice(jobs, func(i, j int) bool {
		a, b := jobs[i], jobs[j]
		if (a.waiters > 0) != (b.waiters > 0) {
			return a.waiters > 0
		}
		return a.target.Height < b.target.Height
	})
	for _, j := range jobs {
		if j.finished || j.running || j.ctx.Err() != nil {
			continue
		}
		if d.reserved+coreBlockMessageBytes > d.budget {
			if j.waiters > 0 {
				// Interactive acquisition must not deadlock behind a full buffer
				// of speculative future replies. Discard farthest unused results;
				// their owners may request them again if they become necessary.
				for k := len(jobs) - 1; k >= 0 && d.reserved+coreBlockMessageBytes > d.budget; k-- {
					other := jobs[k]
					if other != j && other.waiters == 0 && other.finished {
						d.releaseLocked(other)
					}
				}
			}
			if d.reserved+coreBlockMessageBytes > d.budget {
				break
			}
		}
		s := d.n.reserveBlockTarget(j.tried, j.target)
		if s == nil {
			if (len(j.tried) > 0 || len(d.n.listSessions()) > 0) && !d.n.hasUntriedBlockProvider(j.tried, j.target) {
				j.err = fmt.Errorf("WAITING_FOR_PEERS: available peers did not supply the requested block")
				j.finished = true
				close(j.done)
			}
			continue
		}
		j.running = true
		j.session = s
		j.tried[s.peer.addr] = true
		j.reservation = coreBlockMessageBytes
		d.reserved += j.reservation
		d.requests++
		go d.request(j, s, j.target)
	}
	// Core detects a peer preventing its logical window from advancing, not
	// every slow individual block. Gateway's serialized-byte bound may close
	// that window sooner. Only a currently awaited missing body can stall;
	// slow disk commits and speculative-only work never trigger eviction.
	pending := false
	for _, j := range d.jobs {
		if !j.finished && !j.running && j.ctx.Err() == nil {
			pending = true
			break
		}
	}
	for _, j := range jobs {
		if j.running && !j.dispatched.IsZero() {
			d.n.mu.Lock()
			others := 0
			for _, peer := range d.n.sessions {
				if peer != j.session && peer.blockWork.active > 0 {
					others++
				}
			}
			d.n.mu.Unlock()
			if now.Sub(j.dispatched) > coreBlockRequestTimeout(others) {
				go j.session.close("Timeout downloading block (Core v31.1 policy)")
				continue
			}
		}
		blocked := j.waiters > 0 && j.running && !j.dispatched.IsZero() && ready > 0 && (d.reserved+coreBlockMessageBytes > d.budget || !pending)
		if !blocked {
			j.stalling = time.Time{}
			continue
		}
		if j.stalling.IsZero() {
			j.stalling = now
			continue
		}
		if now.Sub(j.stalling) >= d.stallTimeout {
			s := j.session
			j.stalling = time.Time{}
			d.stalls++
			d.stallTimeout = min(64*time.Second, 2*d.stallTimeout)
			go s.close("Peer is stalling block download window (Core v31.1 policy)")
		}
	}
}

func (d *bitcoinBlockDownloads) request(j *bitcoinBlockDownload, s *bitcoinSession, target blockTarget) {
	ctx, cancel := context.WithCancel(j.ctx)
	defer cancel()
	var dispatched time.Time
	m, err := s.requestBlock(ctx, target, func(sent time.Time) { dispatched = sent; d.mu.Lock(); j.dispatched = d.n.now(); d.mu.Unlock() })
	elapsed := time.Duration(0)
	if !dispatched.IsZero() {
		elapsed = time.Since(dispatched)
	}
	if err == nil && m.command == "notfound" {
		d.n.mu.Lock()
		if len(s.blockWork.missing) >= coreBlockWindow {
			s.blockWork.missing = map[[32]byte]bool{}
		}
		if s.blockWork.missing == nil {
			s.blockWork.missing = map[[32]byte]bool{}
		}
		s.blockWork.missing[target.HashRaw] = true
		d.n.mu.Unlock()
		err = fmt.Errorf("peer does not have requested block")
	}
	if err == nil && (len(m.payload) < 81 || len(m.payload) > coreBlockMessageBytes || hash256(m.payload[:80]) != target.HashRaw || len(target.ExpectedHeader) == 80 && !bytes.Equal(m.payload[:80], target.ExpectedHeader)) {
		err = fmt.Errorf("peer supplied incorrect block")
		s.close(err.Error())
	}
	d.n.finishBlockSession(s, elapsed, err == nil, err != nil && j.ctx.Err() == nil && !dispatched.IsZero())
	if err == nil {
		d.n.mu.Lock()
		s.blockWork.bytes += uint64(len(m.payload))
		d.n.mu.Unlock()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reserved -= j.reservation
	j.reservation = 0
	j.running = false
	if d.jobs[target.HashRaw] != j {
		d.signal()
		return
	}
	j.dispatched = time.Time{}
	j.stalling = time.Time{}
	if err == nil {
		j.err = nil
		j.raw = m.payload
		j.provider = s.peer.addr
		j.finished = true
		j.reservation = int64(len(m.payload))
		d.reserved += j.reservation
		d.responses++
		d.bytes += uint64(len(m.payload))
		d.transfer += elapsed
		close(j.done)
	} else {
		j.err = err
	}
	d.signal()
}

func (s *bitcoinSession) requestBlock(ctx context.Context, t blockTarget, onDispatch func(time.Time)) (message, error) {
	s.replyMu.Lock()
	if s.blockSlots == nil {
		s.blockSlots = make(chan struct{}, coreBlocksPerPeer)
	}
	slots := s.blockSlots
	s.replyMu.Unlock()
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return message{}, ctx.Err()
	case <-s.done:
		return message{}, fmt.Errorf("peer disconnected")
	}
	defer func() { <-slots }()
	p := &sessionPendingReply{reply: make(chan message, 1)}
	s.replyMu.Lock()
	if s.pendingBlocks == nil {
		s.pendingBlocks = map[[32]byte]*sessionPendingReply{}
	}
	if s.pendingBlocks[t.HashRaw] != nil {
		s.replyMu.Unlock()
		return message{}, fmt.Errorf("block already requested on peer")
	}
	s.pendingBlocks[t.HashRaw] = p
	s.replyMu.Unlock()
	defer func() {
		s.replyMu.Lock()
		if s.pendingBlocks[t.HashRaw] == p {
			delete(s.pendingBlocks, t.HashRaw)
		}
		s.replyMu.Unlock()
	}()
	var req bytes.Buffer
	req.Write(encodeVarInt(1))
	inv := uint32(2)
	if s.peer.services&nodeWitnessService != 0 {
		inv = 0x40000002
	}
	_ = binary.Write(&req, binary.LittleEndian, inv)
	req.Write(t.HashRaw[:])
	var dispatched time.Time
	if err := s.sendRequestWithDispatch(ctx, "getdata", req.Bytes(), &dispatched); err != nil {
		return message{}, err
	}
	if onDispatch != nil {
		onDispatch(dispatched)
	}
	s.n.mu.Lock()
	s.view.LastWork = "getdata"
	s.view.Updated = s.n.now()
	s.n.mu.Unlock()
	select {
	case <-ctx.Done():
		return message{}, ctx.Err()
	case <-s.done:
		return message{}, fmt.Errorf("peer disconnected")
	case m := <-p.reply:
		return m, nil
	}
}

func coreBlockRequestTimeout(otherDownloadingPeers int) time.Duration {
	return 10*time.Minute + time.Duration(max(0, otherDownloadingPeers))*5*time.Minute
}

func (s *bitcoinSession) blockRequestPending(hash [32]byte) bool {
	s.replyMu.Lock()
	defer s.replyMu.Unlock()
	return s.pendingBlocks[hash] != nil
}

// The network envelope/header check precedes full block parsing. A subsequent
// Merkle/witness/transaction failure invalidates this fallible transport result,
// never the selected header. Do not let a retained failed reply poison retries.
func (n *bitcoinNetwork) rejectBlockPayload(target blockTarget, provider string, reason error) {
	n.mu.Lock()
	d := n.blockDownloads
	s := n.sessions[provider]
	n.mu.Unlock()
	if d != nil {
		d.mu.Lock()
		if j := d.jobs[target.HashRaw]; j != nil && j.finished && j.provider == provider {
			d.reserved -= j.reservation
			j.reservation = 0
			j.raw = nil
			j.err = fmt.Errorf("invalid block payload: %w", reason)
		}
		d.mu.Unlock()
		d.signal()
	}
	if s != nil {
		s.close("Invalid block payload: " + reason.Error())
	}
}

func (s *bitcoinSession) deliverBlockReply(m message) bool {
	keys := [][32]byte{}
	if m.command == "block" && len(m.payload) >= 80 {
		keys = append(keys, hash256(m.payload[:80]))
	} else if m.command == "notfound" {
		count, used, err := decodeCompactSizeMinimal(m.payload)
		if err != nil || count > 50000 || uint64(len(m.payload)-used) != count*36 {
			return false
		}
		for ; used < len(m.payload); used += 36 {
			var key [32]byte
			copy(key[:], m.payload[used+4:used+36])
			keys = append(keys, key)
		}
	} else {
		return false
	}
	s.replyMu.Lock()
	defer s.replyMu.Unlock()
	delivered := false
	for _, key := range keys {
		if p := s.pendingBlocks[key]; p != nil {
			delete(s.pendingBlocks, key)
			p.reply <- m
			delivered = true
		}
	}
	return delivered
}

func (d *bitcoinBlockDownloads) snapshot() map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	running, ready, needed := 0, 0, 0
	for _, j := range d.jobs {
		if j.running {
			running++
		}
		if j.finished && j.err == nil {
			ready++
		}
		if j.waiters > 0 {
			needed++
		}
	}
	return map[string]any{"policy": "bitcoin-core-31.1", "logical_window": coreBlockWindow, "per_peer_limit": coreBlocksPerPeer, "serialized_budget": d.budget, "reserved_bytes": d.reserved, "in_flight": running, "ready": ready, "needed": needed, "requests": d.requests, "responses": d.responses, "received_bytes": d.bytes, "transfer_milliseconds": d.transfer.Milliseconds(), "window_stalls": d.stalls, "stall_timeout_milliseconds": d.stallTimeout.Milliseconds()}
}
