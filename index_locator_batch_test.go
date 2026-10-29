package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Synthetic already-verified structural fixtures isolate persistence. They do
// not claim these invented blocks passed Bitcoin consensus validation.
func locatorBatchTestBlock(height int64, previous string, count int) blockView {
	b := blockView{Height: height, Hash: indexDigest(fmt.Sprintf("locator-batch-block-%d", height)), PreviousBlockHash: previous,
		Verification: verificationView{VerifierVersion: blockVerifierVersion, HeaderHash: true, ProofOfWork: true, MerkleRoot: true, TransactionsParsed: true, HeaderChainMatch: true}}
	for i := 0; i < count; i++ {
		id := "ab00" + indexDigest(fmt.Sprintf("locator-batch-tx-%d-%d", height, i))[4:]
		b.Transactions = append(b.Transactions, transactionView{Index: i, TxID: id, Coinbase: i == 0})
	}
	b.TransactionCount = uint64(count)
	return b
}

func locatorBatchTestStore(t *testing.T) *indexStore {
	t.Helper()
	s, err := openIndexStore(t.TempDir(), "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	s.head.Mode = "full"
	return s
}

func TestIndexLocatorBatch336ExactCommitmentsAndPhysicalAmortization(t *testing.T) {
	ctx := context.Background()
	plain, batched := locatorBatchTestStore(t), locatorBatchTestStore(t)
	if err := batched.BeginLocatorBatch(ctx); err != nil {
		t.Fatal(err)
	}
	previous := ""
	for height := int64(0); height < indexLocatorBatchBlocks; height++ {
		b := locatorBatchTestBlock(height, previous, 1)
		if err := plain.appendBlock(b, 0, "ephemeral"); err != nil {
			t.Fatal(height, err)
		}
		if err := batched.appendBlock(b, 0, "ephemeral"); err != nil {
			t.Fatal(height, err)
		}
		if plain.checkpoint.Commitment != batched.checkpoint.Commitment {
			t.Fatal("physical batching changed a logical commitment", height)
		}
		if height < 335 {
			if batched.DurableCheckpoint() != nil {
				t.Fatal("staged progress advertised as durable", height)
			}
			if _, err := os.Stat(filepath.Join(batched.dir, "head.json")); !os.IsNotExist(err) {
				t.Fatal("partial window published a head", height, err)
			}
		} else {
			if cp := batched.DurableCheckpoint(); cp == nil || cp.Height != 335 {
				t.Fatal("aligned window was not durable", cp)
			}
		}
		previous = b.Hash
	}
	for commit := plain.head.Commitment; commit != ""; {
		p, err := os.ReadFile(filepath.Join(plain.dir, "commits", commit+".json"))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(batched.dir, "commits", commit+".json"))
		if err != nil || string(p) != string(b) {
			t.Fatal("immutable per-block bytes changed", commit, err)
		}
		row, _ := plain.readBatch(commit)
		commit = row.Checkpoint.PreviousCommitment
	}
	old, current := plain.LocatorWriteStats(), batched.LocatorWriteStats()
	if old.HeadWrites != 336 || current.HeadWrites != 1 || old.ShardAppends != 336 || current.ShardAppends != 1 || current.ActiveWrites != 1 || current.CommitWrites != 336 || current.BatchFlushes != 1 || current.RecordsWritten != 336 || current.ReceiptWrites >= old.ReceiptWrites {
		t.Fatal("physical writes were not amortized", old, current)
	}
	first := locatorBatchTestBlock(0, "", 1).Transactions[0].TxID
	rows, err := batched.bitcoinLocatorRecords(ctx, first)
	if err != nil || len(rows) != 1 || rows[0].Height != 0 || rows[0].TxIndex != 0 {
		t.Fatal("coinbase locator lost", rows, err)
	}
	if active, ready, err := batched.activeLocatorCommit(0); err != nil || !ready || active != rows[0].Commit {
		t.Fatal("packed active membership differs", active, ready, err)
	}
	if _, err := os.Stat(filepath.Join(batched.dir, batched.locatorKind(), "active-commits.bin")); !os.IsNotExist(err) {
		t.Fatal("packed rows advanced the old binary's legacy ready marker", err)
	}
	if err := batched.appendBlock(locatorBatchTestBlock(336, previous, 3), 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	batched.AbortLocatorBatch()
	if batched.checkpoint.Height != 335 {
		t.Fatal("abort retained a staged head", batched.checkpoint)
	}
	restarted, err := openIndexStore(filepath.Dir(filepath.Dir(batched.dir)), "tx-locator")
	if err != nil || restarted.checkpoint.Height != 335 {
		t.Fatal("restart lost complete window", restarted, err)
	}
}

func TestIndexLocatorPackedRetryLegacyMergeRollbackAndReadBudget(t *testing.T) {
	ctx := context.Background()
	s := locatorBatchTestStore(t)
	b0 := locatorBatchTestBlock(0, "", 2)
	if err := s.appendBlock(b0, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginLocatorBatch(ctx); err != nil {
		t.Fatal(err)
	}
	b1 := locatorBatchTestBlock(1, b0.Hash, 4)
	if err := s.appendBlock(b1, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("injected packed receipt directory failure")
	path, _ := s.packedLocatorPath(b1.Transactions[0].TxID)
	ackAttempts := 0
	s.locatorSyncDirectory = func(dir string) error {
		if dir == filepath.Dir(path) {
			if r, err := readPackedLocatorReceipt(path); err == nil && r.Bytes > 0 {
				ackAttempts++
				if ackAttempts == 1 {
					return failure
				}
				if head, err := indexStoreHead(s.profileRoot, "tx-locator"); err != nil || head.checkpoint.Height != 0 {
					t.Fatal("head advanced before retrying packed receipt directory acknowledgement", head, err)
				}
			}
		}
		return nil
	}
	if err := s.FlushLocatorBatch(ctx); !errors.Is(err, failure) {
		t.Fatal("flush fault swallowed", err)
	}
	durable, err := indexStoreHead(s.profileRoot, "tx-locator")
	if err != nil || durable.checkpoint.Height != 0 || s.DurableCheckpoint().Height != 0 {
		t.Fatal("failed flush advanced published coverage", durable, err)
	}
	r, _ := readPackedLocatorReceipt(path)
	if ackAttempts != 1 {
		t.Fatal("first receipt acknowledgement fault was not exercised", ackAttempts)
	}
	if err := s.FlushLocatorBatch(ctx); err != nil {
		t.Fatal("idempotent flush retry failed", err)
	}
	after, _ := readPackedLocatorReceipt(path)
	if after.Bytes != r.Bytes || s.DurableCheckpoint().Height != 1 || ackAttempts != 2 {
		t.Fatal("retry duplicated rows or skipped receipt directory acknowledgement", r.Bytes, after.Bytes, ackAttempts)
	}
	s.locatorSyncDirectory = nil
	for _, b := range []blockView{b0, b1} {
		for i, tx := range b.Transactions {
			rows, err := s.bitcoinLocatorRecords(ctx, tx.TxID)
			if err != nil || len(rows) != 1 || rows[0].Height != b.Height || rows[0].TxIndex != i {
				t.Fatal("full positional locator lost", b.Height, i, rows, err)
			}
		}
	}
	if _, err := s.bitcoinLocatorRecordsWithBudget(ctx, b1.Transactions[0].TxID, false, 128, 64); err == nil {
		t.Fatal("packed lookup ignored byte budget")
	}
	// Emulate an older binary, which ignores the additive head field and only
	// understands the legacy ready table/shards. It must rebuild before lookup.
	older := *s
	older.head.LocatorLayout, older.locatorBatch = "", nil
	if _, ready, err := older.activeLocatorCommit(1); err == nil && ready {
		t.Fatal("older binary falsely considered legacy shards current")
	}
	if err := older.ensureIndexLocators(ctx); err != nil {
		t.Fatal("old layout could not reconstruct unchanged immutable history", err)
	}
	rows, err := older.bitcoinLocatorRecordsLegacyWithBudget(ctx, b1.Transactions[3].TxID, false, 128, 0)
	if err != nil || len(rows) != 1 || rows[0].TxIndex != 3 {
		t.Fatal("legacy rollback read lost transaction", rows, err)
	}
	// Unacknowledged physical tail bytes are never parsed as committed frames.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("interrupted orphan tail"))
	_ = f.Close()
	b2 := locatorBatchTestBlock(2, b1.Hash, 2)
	if err := s.appendBlock(b2, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	if err := s.FlushLocatorBatch(ctx); err != nil {
		t.Fatal("orphan tail blocked a safe next flush", err)
	}
	if rows, err := s.bitcoinLocatorRecords(ctx, b2.Transactions[1].TxID); err != nil || len(rows) != 1 || rows[0].TxIndex != 1 {
		t.Fatal(rows, err)
	}
	// The oldest requested frame remains authenticated after newer appends.
	f, err = os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteAt([]byte{0xff}, packedLocatorFrameHeader+8)
	_ = f.Close()
	if _, err := s.bitcoinLocatorRecords(ctx, b1.Transactions[0].TxID); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatal("changed historical packed frame was accepted", err)
	}
}

func TestIndexLocatorBatchByteRecordCapsAndFinalFence(t *testing.T) {
	ctx := context.Background()
	for _, capKind := range []string{"json", "records"} {
		t.Run(capKind, func(t *testing.T) {
			s := locatorBatchTestStore(t)
			if err := s.BeginLocatorBatch(ctx); err != nil {
				t.Fatal(err)
			}
			b0 := locatorBatchTestBlock(0, "", 2)
			if err := s.appendBlock(b0, 0, "ephemeral"); err != nil {
				t.Fatal(err)
			}
			if capKind == "json" {
				s.locatorBatch.maxJSONBytes = s.locatorBatch.jsonBytes * 3 / 2
			} else {
				s.locatorBatch.maxRecords = 3
			}
			if err := s.appendBlock(locatorBatchTestBlock(1, b0.Hash, 2), 0, "ephemeral"); err != nil {
				t.Fatal(err)
			}
			if cp := s.DurableCheckpoint(); cp == nil || cp.Height != 0 {
				t.Fatal("cap did not flush the prior bounded physical batch", cp)
			}
			s.AbortLocatorBatch()
			if s.checkpoint.Height != 0 {
				t.Fatal("abort adopted unflushed logical stage")
			}
		})
	}
	s := locatorBatchTestStore(t)
	allowed := true
	guarded := context.WithValue(ctx, indexRunPublicationKey{}, func() error {
		if !allowed {
			return errIndexRunConsumerStopped
		}
		return nil
	})
	s.locatorContext = guarded
	if err := s.BeginLocatorBatch(guarded); err != nil {
		t.Fatal(err)
	}
	b := locatorBatchTestBlock(0, "", 1)
	if err := s.appendBlock(b, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	activeDir := filepath.Join(s.dir, s.locatorKind())
	s.locatorSyncDirectory = func(dir string) error {
		if dir == activeDir {
			if _, err := os.Stat(filepath.Join(dir, "active-packed-commits.bin.receipt.json")); err == nil {
				allowed = false
			}
		}
		return nil
	}
	if err := s.FlushLocatorBatch(guarded); !errors.Is(err, errIndexRunConsumerStopped) {
		t.Fatal("revoked final fence published", err)
	}
	if head, err := indexStoreHead(s.profileRoot, "tx-locator"); err != nil || head.checkpoint != nil {
		t.Fatal("pre-head packed bytes became visible", head, err)
	}
}

func TestIndexLocatorBatchLeanGuardsReleaseHeavyPreparedBlocks(t *testing.T) {
	base := context.Background()
	s := locatorBatchTestStore(t)
	if err := s.BeginLocatorBatch(base); err != nil {
		t.Fatal(err)
	}
	const blocks = 12
	released := make(chan struct{}, blocks)
	previous := ""
	for h := 0; h < blocks; h++ {
		func() {
			prepared := &indexRunPrepared{block: locatorBatchTestBlock(int64(h), previous, 1)}
			prepared.block.Transactions[0].Inputs = []inputView{{Witness: []string{strings.Repeat("x", 4<<20)}}}
			runtime.SetFinalizer(prepared, func(*indexRunPrepared) { released <- struct{}{} })
			heavy := context.WithValue(base, indexRunPreparedKey{}, prepared)
			heavy = context.WithValue(heavy, resolvedIngestionKey{}, &resolvedIngestionQueue{blocks: []resolvedQueuedBlock{{block: &prepared.block}}})
			lean := context.WithValue(base, indexRunPublicationKey{}, func() error { return nil })
			s.locatorContext = context.WithValue(heavy, indexLocatorBatchGuardKey{}, lean)
			if err := s.appendBlock(prepared.block, 0, "ephemeral"); err != nil {
				t.Fatal(err)
			}
			previous = prepared.block.Hash
			s.locatorContext = base
		}()
	}
	for _, staged := range s.locatorBatch.commits {
		if staged.guard.Value(indexRunPreparedKey{}) != nil || staged.guard.Value(resolvedIngestionKey{}) != nil {
			t.Fatal("staged guard retained the decoded block or dependency queue")
		}
	}
	count := 0
	deadline := time.Now().Add(3 * time.Second)
	for count < blocks && time.Now().Before(deadline) {
		runtime.GC()
		for {
			select {
			case <-released:
				count++
			default:
				goto drained
			}
		}
	drained:
		if count < blocks {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if count != blocks {
		t.Fatal("partial physical window retained heavy prepared block lifetimes", count, blocks)
	}
	if pending, bytes, records := s.LocatorBatchStats(); pending != blocks || bytes > 1<<20 || records != blocks || s.DurableCheckpoint() != nil {
		t.Fatal("heavy source payload leaked into the staged representation", pending, bytes, records)
	}
	runtime.KeepAlive(s)
}

func TestIndexLocatorPackedReorgAndPostHeadFlushRetry(t *testing.T) {
	ctx := context.Background()
	s := locatorBatchTestStore(t)
	if err := s.BeginLocatorBatch(ctx); err != nil {
		t.Fatal(err)
	}
	b0 := locatorBatchTestBlock(0, "", 2)
	b1 := locatorBatchTestBlock(1, b0.Hash, 2)
	for _, b := range []blockView{b0, b1} {
		if err := s.appendBlock(b, 0, "ephemeral"); err != nil {
			t.Fatal(err)
		}
	}
	fault := errors.New("injected final head directory failure")
	s.locatorSyncDirectory = func(path string) error {
		if path == s.dir {
			if _, err := os.Stat(filepath.Join(path, "head.json")); err == nil {
				return fault
			}
		}
		return nil
	}
	if err := s.FlushLocatorBatch(ctx); !errors.Is(err, fault) {
		t.Fatal("final directory fault was not surfaced", err)
	}
	if s.DurableCheckpoint() != nil {
		t.Fatal("failed final directory acknowledgement advertised durable progress")
	}
	written := s.LocatorWriteStats()
	s.locatorSyncDirectory = nil
	if err := s.FlushLocatorBatch(ctx); err != nil {
		t.Fatal("already written complete head could not finish its directory acknowledgement", err)
	}
	if after := s.LocatorWriteStats(); after.HeadWrites != written.HeadWrites || after.RecordsWritten != written.RecordsWritten || s.DurableCheckpoint().Height != 1 {
		t.Fatal("head acknowledgement retry duplicated data", written, after)
	}
	beforeReconcile := s.LocatorWriteStats()
	if err := s.reconcile(func(h int64) (string, error) {
		if h == 0 {
			return b0.Hash, nil
		}
		return strings.Repeat("f", 64), nil
	}); err != nil {
		t.Fatal(err)
	}
	if after := s.LocatorWriteStats(); after.HeadWrites != beforeReconcile.HeadWrites+1 {
		t.Fatal("reconciliation head write missing from physical counters", beforeReconcile, after)
	}
	if commit, ready, err := s.activeLocatorCommit(1); err != nil || !ready || commit != "" || s.checkpoint.Height != 0 {
		t.Fatal("packed membership did not rewind", commit, ready, err)
	}
	if err := s.BeginLocatorBatch(ctx); err != nil {
		t.Fatal(err)
	}
	b1.Hash = indexDigest("replacement locator block")
	if err := s.appendBlock(b1, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	if err := s.FlushLocatorBatch(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := s.bitcoinLocatorRecords(ctx, b1.Transactions[1].TxID)
	if err != nil || len(rows) != 2 {
		t.Fatal("immutable branch evidence was lost", rows, err)
	}
	active, ready, err := s.activeLocatorCommit(1)
	if err != nil || !ready || active != s.head.Commitment {
		t.Fatal("replacement branch membership missing", active, ready, err)
	}
}

func TestIndexLocatorUnpublishedPackedOrphanPreservesLegacyReads(t *testing.T) {
	s := locatorBatchTestStore(t)
	b := locatorBatchTestBlock(0, "", 2)
	if err := s.appendBlock(b, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	path, _ := s.packedLocatorPath(b.Transactions[0].TxID)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	// An interrupted first packed receipt is outside the published legacy
	// layout and cannot make its acknowledged older rows unavailable.
	rows, err := s.bitcoinLocatorRecords(context.Background(), b.Transactions[0].TxID)
	if err != nil || len(rows) != 1 || rows[0].Height != 0 {
		t.Fatal("unpublished packed orphan hid valid legacy evidence", rows, err)
	}
}

func TestIndexLocatorPackedDowngradeAppendThenUpgrade(t *testing.T) {
	for _, replaceBranch := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy_extends", true: "legacy_reorg_same_length"}[replaceBranch], func(t *testing.T) {
			ctx := context.Background()
			s := locatorBatchTestStore(t)
			if err := s.BeginLocatorBatch(ctx); err != nil {
				t.Fatal(err)
			}
			b0 := locatorBatchTestBlock(0, "", 2)
			b1 := locatorBatchTestBlock(1, b0.Hash, 2)
			for _, b := range []blockView{b0, b1} {
				if err := s.appendBlock(b, 0, "ephemeral"); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.FlushLocatorBatch(ctx); err != nil {
				t.Fatal(err)
			}
			stalePackedCommit := s.head.Commitment
			// Emulate the old binary ignoring the additive field, rebuilding its
			// own acceleration, then actually writing a new legacy-format head.
			older := *s
			older.head.LocatorLayout, older.locatorBatch = "", nil
			if err := older.ensureIndexLocators(ctx); err != nil {
				t.Fatal(err)
			}
			legacyBlock := locatorBatchTestBlock(2, b1.Hash, 2)
			if replaceBranch {
				if err := older.reconcile(func(h int64) (string, error) {
					if h == 0 {
						return b0.Hash, nil
					}
					return strings.Repeat("f", 64), nil
				}); err != nil {
					t.Fatal(err)
				}
				legacyBlock = locatorBatchTestBlock(1, b0.Hash, 2)
				legacyBlock.Hash = indexDigest("legacy replacement block")
				for i := range legacyBlock.Transactions {
					legacyBlock.Transactions[i].TxID = "ab00" + indexDigest(fmt.Sprintf("legacy-replacement-%d", i))[4:]
				}
			}
			if err := older.appendBlock(legacyBlock, 0, "ephemeral"); err != nil {
				t.Fatal("legacy append failed", err)
			}
			headRaw, err := os.ReadFile(filepath.Join(s.dir, "head.json"))
			if err != nil || strings.Contains(string(headRaw), "locator_layout") {
				t.Fatal("fixture did not produce the old binary's rewritten head", string(headRaw), err)
			}
			upgraded, err := openIndexStore(s.profileRoot, "tx-locator")
			if err != nil || upgraded.checkpoint.Commitment != older.checkpoint.Commitment || upgraded.head.LocatorLayout != "" {
				t.Fatal("upgrade did not load the authoritative newer legacy prefix", upgraded, err)
			}
			if err := upgraded.BeginLocatorBatch(ctx); err != nil {
				t.Fatal(err)
			}
			next := locatorBatchTestBlock(legacyBlock.Height+1, legacyBlock.Hash, 2)
			if err := upgraded.appendBlock(next, 0, "ephemeral"); err != nil {
				t.Fatal(err)
			}
			if err := upgraded.FlushLocatorBatch(ctx); err != nil {
				t.Fatal("stale packed membership blocked downgrade/append/upgrade", err)
			}
			if upgraded.head.LocatorLayout != indexLocatorPackedLayout {
				t.Fatal("packed layout not restored")
			}
			for _, b := range []blockView{b0, legacyBlock, next} {
				for i, tx := range b.Transactions {
					rows, err := upgraded.bitcoinLocatorRecords(ctx, tx.TxID)
					if err != nil || len(rows) != 1 || rows[0].Height != b.Height || rows[0].TxIndex != i {
						t.Fatal("current full transaction locator lost after upgrade", b.Height, i, rows, err)
					}
					active, ready, err := upgraded.activeLocatorCommit(b.Height)
					if err != nil || !ready || active != rows[0].Commit {
						t.Fatal("membership did not follow the authoritative legacy prefix", b.Height, active, ready, err)
					}
				}
			}
			if replaceBranch {
				active, _, _ := upgraded.activeLocatorCommit(1)
				if active == stalePackedCommit {
					t.Fatal("stale same-length packed branch remained active")
				}
			}
		})
	}
}
