package main

// These process-local certificates describe immutable native inventory
// ancestry. GETs never create sidecars or perform an unbounded history walk.
import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const inscriptionInventoryPrefixCacheEntries = 256
const inscriptionInventoryPrefixRetryDelay = 5 * time.Second

type inscriptionInventoryPrefixView struct {
	complete  bool
	cached    bool
	batches   int
	bytes     int64
	retryable bool
}
type inscriptionInventoryPrefixCertificate struct {
	checkpoint indexCheckpoint
	complete   bool
	done       chan struct{}
	retryAt    time.Time
}

var inscriptionInventoryPrefixes = struct {
	sync.Mutex
	rows  map[string]*inscriptionInventoryPrefixCertificate
	order []string
}{rows: map[string]*inscriptionInventoryPrefixCertificate{}}

func inscriptionInventoryPrefixKey(s *indexStore, commitment string) string {
	return filepath.Clean(s.dir) + "\x00" + commitment
}
func inscriptionInventoryPrefixInsertLocked(key string, entry *inscriptionInventoryPrefixCertificate) {
	for len(inscriptionInventoryPrefixes.order) >= inscriptionInventoryPrefixCacheEntries {
		old := inscriptionInventoryPrefixes.order[0]
		inscriptionInventoryPrefixes.order = inscriptionInventoryPrefixes.order[1:]
		delete(inscriptionInventoryPrefixes.rows, old)
	}
	inscriptionInventoryPrefixes.rows[key] = entry
	inscriptionInventoryPrefixes.order = append(inscriptionInventoryPrefixes.order, key)
}
func inscriptionInventoryPrefixRemoveLocked(key string, entry *inscriptionInventoryPrefixCertificate) {
	if inscriptionInventoryPrefixes.rows[key] != entry {
		return
	}
	delete(inscriptionInventoryPrefixes.rows, key)
	for i, existing := range inscriptionInventoryPrefixes.order {
		if existing == key {
			inscriptionInventoryPrefixes.order = append(inscriptionInventoryPrefixes.order[:i], inscriptionInventoryPrefixes.order[i+1:]...)
			break
		}
	}
}
func inscriptionInventoryPrefixCached(s *indexStore, commit string) (indexCheckpoint, bool) {
	inscriptionInventoryPrefixes.Lock()
	defer inscriptionInventoryPrefixes.Unlock()
	entry := inscriptionInventoryPrefixes.rows[inscriptionInventoryPrefixKey(s, commit)]
	if entry == nil || entry.done != nil || !entry.complete {
		return indexCheckpoint{}, false
	}
	return entry.checkpoint, true
}

func inspectInscriptionInventoryPrefix(ctx context.Context, s *indexStore, maxBatches int, maxBytes int64) inscriptionInventoryPrefixView {
	view := inscriptionInventoryPrefixView{}
	if s == nil || s.definition.ID != "inscriptions" || s.checkpoint == nil {
		return view
	}
	next := s.checkpoint.Commitment
	var child *indexCheckpoint
	childPreviousHash := ""
	for next != "" {
		if ctx.Err() != nil {
			view.retryable = true
			return view
		}
		if view.batches >= maxBatches {
			return view
		}
		if child != nil {
			if parent, complete := inscriptionInventoryPrefixCached(s, next); complete {
				view.complete = child.Height == parent.Height+1 && child.From == parent.From && child.Network == parent.Network && child.PreviousCommitment == parent.Commitment && childPreviousHash == parent.BlockHash
				return view
			}
		}
		if !validHash(next) {
			return view
		}
		info, err := os.Stat(filepath.Join(s.dir, "commits", next+".json"))
		if err != nil || !info.Mode().IsRegular() || info.Size() < 1 {
			view.retryable = true
			return view
		}
		if info.Size() > maxBytes-view.bytes {
			return view
		}
		batch, err := s.readBatch(next)
		if err != nil {
			view.retryable = true
			return view
		}
		view.batches++
		view.bytes += info.Size()
		if !inscriptionBatchInventoryComplete(batch) {
			return view
		}
		c := batch.Checkpoint
		if child != nil && (child.Height != c.Height+1 || child.From != c.From || child.Network != c.Network || childPreviousHash != c.BlockHash) {
			return view
		}
		next = c.PreviousCommitment
		if next == "" {
			view.complete = c.Height == c.From
			return view
		}
		// A compact link certificate avoids retaining occurrence/body arrays.
		child = &c
		childPreviousHash = batch.PreviousBlockHash
	}
	return view
}

func inscriptionInventoryPrefixStatus(ctx context.Context, s *indexStore) inscriptionInventoryPrefixView {
	if s == nil || s.definition.ID != "inscriptions" || s.checkpoint == nil || ctx.Err() != nil {
		return inscriptionInventoryPrefixView{}
	}
	key := inscriptionInventoryPrefixKey(s, s.checkpoint.Commitment)
	for {
		inscriptionInventoryPrefixes.Lock()
		if entry := inscriptionInventoryPrefixes.rows[key]; entry != nil {
			if entry.done == nil && !entry.complete && !entry.retryAt.IsZero() && !time.Now().Before(entry.retryAt) {
				inscriptionInventoryPrefixRemoveLocked(key, entry)
			} else if entry.done == nil {
				view := inscriptionInventoryPrefixView{complete: entry.complete, cached: true, retryable: !entry.retryAt.IsZero()}
				inscriptionInventoryPrefixes.Unlock()
				return view
			} else {
				done := entry.done
				inscriptionInventoryPrefixes.Unlock()
				select {
				case <-ctx.Done():
					return inscriptionInventoryPrefixView{}
				case <-done:
					continue
				}
			}
		}
		entry := &inscriptionInventoryPrefixCertificate{checkpoint: *s.checkpoint, done: make(chan struct{})}
		inscriptionInventoryPrefixInsertLocked(key, entry)
		inscriptionInventoryPrefixes.Unlock()
		view := inspectInscriptionInventoryPrefix(ctx, s, indexExplorerMaxBatches, indexExplorerMaxBytes)
		inscriptionInventoryPrefixes.Lock()
		entry.complete = entry.complete || view.complete
		view.complete = entry.complete
		if view.retryable && !entry.complete {
			entry.retryAt = time.Now().Add(inscriptionInventoryPrefixRetryDelay)
		}
		if ctx.Err() != nil && !entry.complete && inscriptionInventoryPrefixes.rows[key] == entry {
			inscriptionInventoryPrefixRemoveLocked(key, entry)
		}
		if entry.done != nil {
			close(entry.done)
			entry.done = nil
		}
		inscriptionInventoryPrefixes.Unlock()
		return view
	}
}
func inscriptionInventoryPrefixComplete(ctx context.Context, s *indexStore) bool {
	return inscriptionInventoryPrefixStatus(ctx, s).complete
}

// Only the successful existing full writer/resume walk calls this function.
// It can upgrade an earlier bounded/unknown GET result without changing disk.
func rememberValidatedInscriptionInventoryPrefix(s *indexStore) {
	if s == nil || s.definition.ID != "inscriptions" || s.checkpoint == nil {
		return
	}
	key := inscriptionInventoryPrefixKey(s, s.checkpoint.Commitment)
	inscriptionInventoryPrefixes.Lock()
	defer inscriptionInventoryPrefixes.Unlock()
	entry := inscriptionInventoryPrefixes.rows[key]
	if entry == nil {
		entry = &inscriptionInventoryPrefixCertificate{}
		inscriptionInventoryPrefixInsertLocked(key, entry)
	}
	entry.checkpoint, entry.complete = *s.checkpoint, true
	entry.retryAt = time.Time{}
	if entry.done != nil {
		close(entry.done)
		entry.done = nil
	}
}

// Register only after successful native head publication. A complete child
// cannot certify an unknown parent or a different height/hash/origin edge.
func rememberInscriptionInventoryAppend(s *indexStore, batch indexBatch) {
	if s == nil || s.definition.ID != "inscriptions" || s.checkpoint == nil || batch.Checkpoint.Commitment != s.checkpoint.Commitment || !inscriptionBatchInventoryComplete(batch) {
		return
	}
	c := batch.Checkpoint
	if c.PreviousCommitment == "" {
		if c.Height == c.From {
			rememberValidatedInscriptionInventoryPrefix(s)
		}
		return
	}
	parent, complete := inscriptionInventoryPrefixCached(s, c.PreviousCommitment)
	if complete && c.Height == parent.Height+1 && c.From == parent.From && c.Network == parent.Network && batch.PreviousBlockHash == parent.BlockHash {
		rememberValidatedInscriptionInventoryPrefix(s)
	}
}
