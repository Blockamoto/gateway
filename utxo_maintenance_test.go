package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func utxoMeasureFiles(t *testing.T, dir string) (int, int64) {
	t.Helper()
	count, bytes := 0, int64(0)
	if e := filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !entry.IsDir() {
			info, e := entry.Info()
			if e != nil {
				return e
			}
			count++
			bytes += info.Size()
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	return count, bytes
}

func TestUTXOFactEnumerationDoesNotHoldStatusMutex(t *testing.T) {
	ctx := context.Background()
	s, e := openUTXOStoreContext(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(1200, 50))
	utxoApply(t, s, b0)
	utxoApply(t, s, utxoFixtureBlock(1, b0.Hash, utxoFixtureCoinbase(1201, 7)))
	called := false
	if e = s.VisitFactPages(ctx, func(string) error {
		called = true
		// A status read inside the enumeration callback would deadlock if its
		// potentially whole-state traversal still owned the status mutex.
		status, e := utxoStoreHead(s.root)
		if e != nil {
			return e
		}
		if status.Status().LiveOutputCount != 1 {
			return fmt.Errorf("status lost committed state")
		}
		return nil
	}); e != nil || !called {
		t.Fatal(called, e)
	}
}

func TestUTXOLargeFixtureMaintenanceContinuousProgress(t *testing.T) {
	if testing.Short() {
		t.Skip("10,000-coin / 321-transition maintenance fixture")
	}
	ctx := context.Background()
	s, e := openUTXOStoreContext(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	started := time.Now()
	b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(2000, 50))
	utxoApply(t, s, b0)
	values := make([]uint64, 10000)
	for i := range values {
		values[i] = 1
	}
	cb := utxoFixtureCoinbase(2001, values...)
	b1 := utxoFixtureBlock(1, b0.Hash, cb)
	utxoApply(t, s, b1)
	expiredRoot := s.Head().LiveRoot
	previous := b1.Hash
	hashes := map[int64]string{0: b0.Hash, 1: b1.Hash}
	for h := int64(2); h <= 321; h++ {
		coinbase := utxoFixtureCoinbase(uint64(20000+h), 0)
		coinbase.Outputs[0].ScriptPubKey = "6a"
		spend := utxoFixtureSpend(uint64(30000+h), []inputView{{PrevTxID: cb.TxID, PrevVout: uint32(h - 2)}}, 1)
		block := utxoFixtureBlock(h, previous, coinbase, spend)
		utxoApply(t, s, block)
		previous, hashes[h] = block.Hash, block.Hash
	}
	buildDuration := time.Since(started)
	beforeFiles, beforeBytes := utxoMeasureFiles(t, s.pages())
	gcStarted := time.Now()
	passes := 0
	for {
		passes++
		if e = s.CollectPages(ctx); e != nil {
			t.Fatal(e)
		}
		if !ordinalCollectionPending(s.pages()) {
			break
		}
		if passes >= 2000 {
			t.Fatal("incremental collection did not finish")
		}
	}
	afterFiles, afterBytes := utxoMeasureFiles(t, s.pages())
	if afterFiles >= beforeFiles || afterBytes >= beforeBytes {
		t.Fatal("collection did not reclaim unreachable engine pages", beforeFiles, afterFiles, beforeBytes, afterBytes)
	}
	if _, e = os.Stat(newOrdinalTree(s.pages(), "").pagePath(expiredRoot)); !os.IsNotExist(e) {
		t.Fatal("expired creation state root survived maintenance", e)
	}
	commits, _ := os.ReadDir(filepath.Join(s.dir, "commits"))
	journals, _ := os.ReadDir(filepath.Join(s.dir, "journals"))
	if len(commits) != 289 || len(journals) != 288 || s.Status().LiveOutputCount != 10000 || s.Status().RecoveryBoundaryHeight != 33 {
		t.Fatal("large state/recovery metadata changed", len(commits), len(journals), s.Status())
	}
	utxoRequireCoin(t, s, "9999.0.1", 1)
	utxoRequireCoin(t, s, "0.1.321", 1)
	if e = s.Reconcile(ctx, func(_ context.Context, h int64) (string, error) {
		if h == 321 {
			return testID(40000321), nil
		}
		return hashes[h], nil
	}); e != nil {
		t.Fatal("retained reorg failed after maintenance", e)
	}
	utxoRequireCoin(t, s, "319.0.1", 1)
	t.Logf("synthetic fixture: 10,000 live Coins; 321 non-genesis transitions / 320 spend mutations; build=%s; maintenance plus postchecks/undo=%s in %d resumable mark passes; engine files %d/%d bytes -> %d/%d bytes (shared facts excluded)", buildDuration, time.Since(gcStarted), passes, beforeFiles, beforeBytes, afterFiles, afterBytes)
}
