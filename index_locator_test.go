package main

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func locatorShardFixture064(t *testing.T) (*indexStore, string, []byte) {
	t.Helper()
	s, err := indexStoreHead(t.TempDir(), "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("a", 64)
	path, err := graphShardPath(s.dir, s.locatorKind(), key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encodeIndexLocator(indexLocatorRecord{TxID: key, Commit: strings.Repeat("b", 64), Height: 0, TxIndex: 1})
	if err != nil {
		t.Fatal(err)
	}
	indexLocatorMu.Lock()
	err = s.appendBitcoinLocatorRecords(path, [][]byte{raw})
	indexLocatorMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return s, path, raw
}

func TestIndexLocatorInterruptedTailRecovery(t *testing.T) {
	s, path, raw := locatorShardFixture064(t)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(raw[:19]); err != nil {
		t.Fatal(err)
	}
	f.Close()
	indexLocatorMu.Lock()
	err = s.appendBitcoinLocatorRecords(path, [][]byte{raw})
	indexLocatorMu.Unlock()
	if err != nil {
		t.Fatal("valid committed prefix was not recovered", err)
	}
	records, err := s.bitcoinLocatorRecords(context.Background(), strings.Repeat("a", 64))
	if err != nil || len(records) != 1 {
		t.Fatal("orphan retry was not deduplicated", records, err)
	}
	info, _ := os.Stat(path)
	if info.Size() != 2*indexLocatorRecordBytes {
		t.Fatal("partial tail was retained", info.Size())
	}
}

func TestIndexLocatorWholeRecordTruncationAndChangedPrefix(t *testing.T) {
	for _, damage := range []string{"truncate", "same_size"} {
		t.Run(damage, func(t *testing.T) {
			s, path, raw := locatorShardFixture064(t)
			if damage == "truncate" {
				if err := os.Truncate(path, 0); err != nil {
					t.Fatal(err)
				}
			} else {
				changed := append([]byte(nil), raw...)
				changed[3] ^= 1
				if err := os.WriteFile(path, changed, 0600); err != nil {
					t.Fatal(err)
				}
			}
			// Simulate the required first validation after process restart.
			indexLocatorMu.Lock()
			delete(indexLocatorVerifiedPrefixes, path)
			err := s.appendBitcoinLocatorRecords(path, [][]byte{raw})
			indexLocatorMu.Unlock()
			if err == nil {
				t.Fatal("damaged committed shard advanced")
			}
			if _, err = s.bitcoinLocatorRecords(context.Background(), strings.Repeat("a", 64)); err == nil {
				t.Fatal("damaged shard produced locators")
			}
		})
	}
}

func TestIndexLocatorReceiptBeforeHeadRetryAndReorg(t *testing.T) {
	a, s, key := olderExplorerFixture064(t, "blocks", 2)
	oldHead := s.head
	parent, err := s.readBatch(s.checkpoint.PreviousCommitment)
	if err != nil {
		t.Fatal(err)
	}
	// Receipt/table are durable but main head still has the previous checkpoint.
	s.head.Commitment = parent.Checkpoint.Commitment
	s.checkpoint = &parent.Checkpoint
	if err = atomicWriteJSON(filepath.Join(s.dir, "head.json"), s.head); err != nil {
		t.Fatal(err)
	}
	if err = s.ensureIndexLocators(context.Background()); err != nil {
		t.Fatal("pre-head receipt could not recover", err)
	}
	next, err := s.readBatch(oldHead.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.writeIndexLocators(next); err != nil {
		t.Fatal("orphan replay failed", err)
	}
	s.head = oldHead
	s.checkpoint = &next.Checkpoint
	if err = atomicWriteJSON(filepath.Join(s.dir, "head.json"), s.head); err != nil {
		t.Fatal(err)
	}
	if _, found, err := a.indexedTransaction(key); err != nil || !found {
		t.Fatal("successful retry lost earlier transaction", found, err)
	}
	if err = s.reconcile(func(h int64) (string, error) {
		if h == 0 {
			return parent.Checkpoint.BlockHash, nil
		}
		return strings.Repeat("f", 64), nil
	}); err != nil {
		t.Fatal(err)
	}
	if active, ready, err := s.activeLocatorCommit(0); err != nil || !ready || active != parent.Checkpoint.Commitment {
		t.Fatal("rewind membership did not follow head", active, ready, err)
	}
}

func TestIndexLocatorRepairReplayDoesNotGrowShards(t *testing.T) {
	_, s, _ := olderExplorerFixture064(t, "inscriptions", 2)
	b, err := s.readBatch(s.checkpoint.PreviousCommitment)
	if err != nil {
		t.Fatal(err)
	}
	_ = b
	var shard string
	if err = filepath.Walk(filepath.Join(s.dir, s.locatorKind()), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".bin") && path != s.locatorActivePath() {
			shard = path
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(shard)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 3; n++ {
		if err = os.Remove(s.locatorActivePath() + ".receipt.json"); err != nil {
			t.Fatal(err)
		}
		if err = s.ensureIndexLocators(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := os.ReadFile(shard)
	if string(before) != string(after) {
		t.Fatal("repair replay duplicated pointers")
	}
}

func TestIndexLocatorLinkCannotMutateExternalFile(t *testing.T) {
	s, err := indexStoreHead(t.TempDir(), "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("a", 64)
	path, err := graphShardPath(s.dir, s.locatorKind(), key)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external-core-sentinel")
	sentinel := []byte("read-only external Bitcoin files")
	if err = os.WriteFile(external, sentinel, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(external, path); err != nil {
		t.Skip("OS does not permit symlink fixture:", err)
	}
	raw, _ := encodeIndexLocator(indexLocatorRecord{TxID: key, Commit: strings.Repeat("b", 64), Height: 0})
	indexLocatorMu.Lock()
	err = s.appendBitcoinLocatorRecords(path, [][]byte{raw})
	indexLocatorMu.Unlock()
	if err == nil {
		t.Fatal("linked shard accepted")
	}
	after, err := os.ReadFile(external)
	if err != nil || string(after) != string(sentinel) {
		t.Fatal("external file modified", err)
	}
}

func TestIndexLocatorActiveLengthRejectsOverflow(t *testing.T) {
	for _, c := range []indexCheckpoint{{Height: math.MaxInt64, From: 0}, {Height: 5, From: 6}, {Height: 0, From: -1}, {Height: indexLocatorShardBytes / indexLocatorActiveBytes, From: 0}} {
		if _, err := activeLocatorLength(&c); err == nil {
			t.Fatal("unbounded active range accepted", c)
		}
	}
}

func TestIndexLocatorWaitingReaderCancellation(t *testing.T) {
	s, _, _ := locatorShardFixture064(t)
	indexLocatorMu.Lock()
	defer indexLocatorMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := s.bitcoinLocatorRecords(ctx, strings.Repeat("a", 64)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("waiting reader ignored cancellation", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("reader blocked updater drain")
	}
}

func TestIndexLocatorSameHeightOrphanDoesNotBindActiveHead(t *testing.T) {
	a, s, key := olderExplorerFixture064(t, "blocks", 1)
	b, err := s.readBatch(s.head.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	active := b.Checkpoint.Commitment
	// Both payloads share the selected block hash, height, range and queried ID.
	// Only the original immutable commitment was made visible by the index head.
	b.Bitcoin.Transactions[0].OutputSats++
	b.Checkpoint.RecordsHash = batchRecordsHash(b)
	b.Checkpoint.Commitment = checkpointHash(b.Checkpoint)
	if err = atomicWriteJSON(filepath.Join(s.dir, "commits", b.Checkpoint.Commitment+".json"), b); err != nil {
		t.Fatal(err)
	}
	indexLocatorMu.Lock()
	err = s.writeIndexLocatorRows(b, false)
	indexLocatorMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	_, current, err := a.validatedLocatorBatch(context.Background(), s, indexLocatorRecord{TxID: key, Commit: b.Checkpoint.Commitment, Height: 0, TxIndex: 0})
	if err != nil || current {
		t.Fatal("same-height orphan borrowed active membership", current, err)
	}
	r, found, err := a.indexedTransaction(key)
	if err != nil || !found || r.Transaction.OutputSats != 5000000000 || s.head.Commitment != active {
		t.Fatal("inactive payload replaced active transaction", r, found, err)
	}
}
