package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"./internal/updateapply"
)

const indexLocatorBatchBlocks int64 = 336
const indexLocatorBatchJSONBytes int64 = 32 << 20
const indexLocatorBatchRecords = 400000

// Coordinators attach a guard derived from their run context, without the
// current decoded/prepared-block context values. Retaining a full source
// context here would otherwise retain every staged decoded block indirectly.
type indexLocatorBatchGuardKey struct{}

// These are physical operations, separate from logical block/transaction
// progress. Like the store head, snapshots are taken on its writer goroutine.
type indexLocatorWriteStats struct {
	BatchFlushes     int64 `json:"batch_flushes"`
	ShardAppends     int64 `json:"shard_appends"`
	ShardSyncs       int64 `json:"shard_syncs"`
	ReceiptWrites    int64 `json:"receipt_writes"`
	ActiveWrites     int64 `json:"active_writes"`
	HeadWrites       int64 `json:"head_writes"`
	CommitWrites     int64 `json:"commit_writes"`
	RecordsWritten   int64 `json:"records_written"`
	BytesWritten     int64 `json:"bytes_written"`
	FlushNanoseconds int64 `json:"flush_nanoseconds"`
}

type stagedLocatorCommit struct {
	checkpoint indexCheckpoint
	json       []byte
	guard      context.Context
}

type indexLocatorBatchSession struct {
	durableHead       indexHead
	durableCheckpoint *indexCheckpoint
	commits           []stagedLocatorCommit
	groups            map[string][]byte // four hex digits, flat encoded records
	jsonBytes         int64
	records           int
	retry             bool
	headWritten       bool
	maxJSONBytes      int64
	maxRecords        int
}

func cloneLocatorCheckpoint(c *indexCheckpoint) *indexCheckpoint {
	if c == nil {
		return nil
	}
	copy := *c
	return &copy
}

func (s *indexStore) locatorWriteCounters() *indexLocatorWriteStats {
	if s.locatorWrites == nil {
		s.locatorWrites = &indexLocatorWriteStats{}
	}
	return s.locatorWrites
}

func (s *indexStore) LocatorWriteStats() indexLocatorWriteStats {
	if s.locatorWrites == nil {
		return indexLocatorWriteStats{}
	}
	return *s.locatorWrites
}

func (s *indexStore) LocatorBatchStats() (int, int64, int) {
	if s.locatorBatch == nil {
		return 0, 0, 0
	}
	b := s.locatorBatch
	return len(b.commits), b.jsonBytes, b.records
}

// Only the explicit full Transaction Locator executor opts into staging.
// Existing APIs, sparse ingestion and every other index keep their normal
// durable-per-block behavior. No decoded Bitcoin block is retained here.
func (s *indexStore) BeginLocatorBatch(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.definition.ID != "tx-locator" {
		return fmt.Errorf("physical locator batching belongs to the TX ID Locators executor")
	}
	if s.locatorBatch != nil {
		return nil
	}
	s.locatorWriteCounters()
	s.locatorBatch = &indexLocatorBatchSession{durableHead: cloneTXIOLayerCoverage(s.head), durableCheckpoint: cloneLocatorCheckpoint(s.checkpoint), groups: map[string][]byte{}, maxJSONBytes: indexLocatorBatchJSONBytes, maxRecords: indexLocatorBatchRecords}
	return nil
}

// The logical checkpoint may include staged blocks. Only this separate value
// may be described as durable progress or used to advertise saved coverage.
func (s *indexStore) DurableCheckpoint() *indexCheckpoint {
	if s.locatorBatch != nil {
		return cloneLocatorCheckpoint(s.locatorBatch.durableCheckpoint)
	}
	return cloneLocatorCheckpoint(s.checkpoint)
}

func (s *indexStore) AbortLocatorBatch() {
	if b := s.locatorBatch; b != nil {
		s.head = cloneTXIOLayerCoverage(b.durableHead)
		s.checkpoint = cloneLocatorCheckpoint(b.durableCheckpoint)
		// A failed enclosing-directory flush can follow a successful atomic
		// head replacement. Never silently rewind that already visible, fully
		// prepared prefix merely by discarding the session's memory.
		if b.headWritten {
			if current, err := indexStoreHead(s.profileRoot, s.definition.ID); err == nil {
				s.head, s.checkpoint = current.head, current.checkpoint
			}
		}
		s.locatorBatch = nil
	}
}

func (s *indexStore) stageLocatorBatch(b indexBatch, retention string) error {
	ctx := s.ordinalContext()
	if err := checkIndexConsumerPublication(ctx, s.profileRoot, s.definition.ID); err != nil {
		return err
	}
	keys, err := s.indexBatchLocatorKeys(b)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if len(raw) > 64<<20 {
		return fmt.Errorf("index batch exceeds budget")
	}
	session := s.locatorBatch
	if len(session.commits) > 0 && (session.jsonBytes+int64(len(raw)) > session.maxJSONBytes || session.records+len(keys) > session.maxRecords) {
		if err := s.FlushLocatorBatch(ctx); err != nil {
			return err
		}
	}
	// Encode before changing the logical head; malformed identifiers cannot
	// leave a partially accepted logical block.
	encoded := make([]byte, 0, len(keys)*indexLocatorRecordBytes)
	for i, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		record, err := encodeIndexLocator(indexLocatorRecord{TxID: key, Commit: b.Checkpoint.Commitment, Height: b.Checkpoint.Height, TxIndex: i})
		if err != nil {
			return err
		}
		encoded = append(encoded, record...)
	}
	for i, key := range keys {
		prefix := key[:4]
		session.groups[prefix] = append(session.groups[prefix], encoded[i*indexLocatorRecordBytes:(i+1)*indexLocatorRecordBytes]...)
	}
	guard := ctx
	if lean, ok := ctx.Value(indexLocatorBatchGuardKey{}).(context.Context); ok {
		guard = lean
	}
	session.commits = append(session.commits, stagedLocatorCommit{b.Checkpoint, raw, guard})
	session.jsonBytes += int64(len(raw))
	session.records += len(keys)
	s.head.Commitment, s.head.Retention = b.Checkpoint.Commitment, retention
	s.head.LocatorLayout = indexLocatorPackedLayout
	s.checkpoint = cloneLocatorCheckpoint(&b.Checkpoint)
	if (b.Checkpoint.Height+1)%indexLocatorBatchBlocks == 0 || session.jsonBytes >= session.maxJSONBytes || session.records >= session.maxRecords {
		return s.FlushLocatorBatch(ctx)
	}
	return nil
}

// Flush publishes one durable prefix. Immutable per-block bytes/commitments
// are unchanged; only shard appends, receipts, active-table and head writes
// share a physical boundary. Failure retains the stage for explicit retry or
// Abort. Any pre-head files are filtered orphans under the durable old head.
func (s *indexStore) FlushLocatorBatch(ctx context.Context) (result error) {
	session := s.locatorBatch
	if session == nil || len(session.commits) == 0 {
		return ctx.Err()
	}
	started := time.Now()
	defer func() { s.locatorWriteCounters().FlushNanoseconds += time.Since(started).Nanoseconds() }()
	if err := ctx.Err(); err != nil {
		return err
	}
	finish := func() {
		s.locatorWriteCounters().BatchFlushes++
		session.durableHead, session.durableCheckpoint = cloneTXIOLayerCoverage(s.head), cloneLocatorCheckpoint(s.checkpoint)
		session.commits, session.groups = nil, map[string][]byte{}
		session.jsonBytes, session.records, session.retry, session.headWritten = 0, 0, false, false
		notifyParcelIndexCommit079(s.profileRoot, s.definition.ID)
	}
	if session.headWritten {
		current, err := indexStoreHead(s.profileRoot, s.definition.ID)
		if err != nil || current.head.Commitment != s.head.Commitment {
			return fmt.Errorf("published locator batch changed before directory flush retry: %w", err)
		}
		if err := s.syncLocatorDirectory(s.dir); err != nil {
			return err
		}
		finish()
		return nil
	}
	check := func() error {
		if err := checkIndexConsumerPublication(ctx, s.profileRoot, s.definition.ID); err != nil {
			return err
		}
		for _, staged := range session.commits {
			if err := checkIndexConsumerPublication(staged.guard, s.profileRoot, s.definition.ID); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	defer func() {
		if result != nil {
			session.retry = true
		}
	}()
	// Locator repair, when needed, may only walk the prior durable range.
	base := *s
	base.locatorBatch = nil
	base.head, base.checkpoint = cloneTXIOLayerCoverage(session.durableHead), cloneLocatorCheckpoint(session.durableCheckpoint)
	base.locatorContext = ctx
	commitDir := filepath.Join(s.dir, "commits")
	if err := base.makeLocatorDirectories(commitDir); err != nil {
		return err
	}
	replay := session.retry
	for _, staged := range session.commits {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(commitDir, staged.checkpoint.Commitment+".json")
		if _, err := os.Stat(path); err == nil {
			replay = true
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := writeLocatorCommitBytes(path, staged.json); err != nil {
			return err
		}
		s.locatorWriteCounters().CommitWrites++
	}
	if err := base.syncLocatorDirectory(commitDir); err != nil {
		return err
	}
	if err := lockIndexLocators(ctx); err != nil {
		return err
	}
	defer indexLocatorMu.Unlock()
	if err := base.requireVisibleLocatorHead(); err != nil {
		return err
	}
	if err := base.appendPackedLocatorGroups(ctx, session.groups, replay); err != nil {
		return err
	}
	if err := check(); err != nil {
		return err
	}
	if err := base.writeActiveLocatorBatch(session.commits); err != nil {
		return err
	}
	if err := check(); err != nil {
		return err
	}
	if err := atomicWriteJSON(filepath.Join(s.dir, "head.json"), s.head); err != nil {
		return err
	}
	s.locatorWriteCounters().HeadWrites++
	session.headWritten = true
	if err := base.syncLocatorDirectory(s.dir); err != nil {
		return err
	}
	finish()
	s.locatorDirectoriesSynced = base.locatorDirectoriesSynced
	return nil
}

// Same immutable JSON bytes and file-level durability as atomicWriteJSON;
// its enclosing commit directory is flushed once for the whole batch.
func writeLocatorCommitBytes(path string, raw []byte) error {
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".locator-commit-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
