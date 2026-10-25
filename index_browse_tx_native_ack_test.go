package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBrowseTXNativeAckDurabilityRetryAndReadOnlyInvalidation(t *testing.T) {
	a, c, b := sharedSavedGenesisPoint(t)
	ctx := context.Background()
	if promoted, err := a.promoteRunTransactionPoint(ctx, c, 0, b.Hash, "ephemeral"); err != nil || !promoted {
		t.Fatal(promoted, err)
	}
	if err := a.acknowledgeBrowseTXPointsNative(ctx, c.store); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(browseTXNativeAckPath(a.dataDir)); !os.IsNotExist(err) {
		t.Fatal("volatile native stage gained acknowledgement", err)
	}
	fault := errors.New("injected final native directory acknowledgement")
	c.store.locatorSyncDirectory = func(dir string) error {
		if dir == c.store.dir {
			return fault
		}
		return nil
	}
	if err := c.store.FlushLocatorBatch(ctx); !errors.Is(err, fault) {
		t.Fatal(err)
	}
	if err := a.acknowledgeBrowseTXPointsNative(ctx, c.store); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(browseTXNativeAckPath(a.dataDir)); !os.IsNotExist(err) {
		t.Fatal("failed native flush gained acknowledgement", err)
	}
	c.store.locatorSyncDirectory = nil
	if err := c.store.FlushLocatorBatch(ctx); err != nil {
		t.Fatal(err)
	}
	ackDir := filepath.Dir(browseTXNativeAckPath(a.dataDir))
	attempts := 0
	c.store.locatorSyncDirectory = func(dir string) error {
		if dir == ackDir {
			if _, err := os.Stat(browseTXNativeAckPath(a.dataDir)); err == nil {
				attempts++
				if attempts == 1 {
					return fault
				}
			}
		}
		return nil
	}
	if err := a.acknowledgeBrowseTXPointsNative(ctx, c.store); !errors.Is(err, fault) {
		t.Fatal("acknowledgement directory fault not exercised", err)
	}
	before, err := os.ReadFile(browseTXNativeAckPath(a.dataDir))
	if err != nil {
		t.Fatal(err)
	}
	if err = a.acknowledgeBrowseTXPointsNative(ctx, c.store); err != nil {
		t.Fatal("visible descriptor directory retry failed", err)
	}
	if after, _ := os.ReadFile(browseTXNativeAckPath(a.dataDir)); string(before) != string(after) || attempts < 2 {
		t.Fatal("dedup skipped directory retry or changed descriptor", attempts)
	}
	c.store.locatorSyncDirectory = nil
	if ack, err := a.browseTXNativeAck(ctx); err != nil || ack == nil || ack.Height != 0 {
		t.Fatal(ack, err)
	}
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: false}}}); err != nil {
		t.Fatal(err)
	}
	files := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes"))
	if ack, err := a.browseTXNativeAck(ctx); err != nil || ack == nil {
		t.Fatal("Off cannot read saved acknowledgement", ack, err)
	}
	if after := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes")); !reflect.DeepEqual(files, after) {
		t.Fatal("acknowledgement read repaired Off data")
	}
	// Replacing the selected block invalidates the hint, while its original
	// descriptor remains intact and inspectable. Descriptor corruption is an error.
	replacement := bitmapDevelopmentFixtureBlock(0, "", "0.bitmap")
	installRunProtocolHeader(t, a, &replacement)
	if ack, err := a.browseTXNativeAck(ctx); err != nil || ack != nil {
		t.Fatal("reorg retained ancestor hint", ack, err)
	}
	if err := os.WriteFile(browseTXNativeAckPath(a.dataDir), []byte(`{"schema":3}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.browseTXNativeAck(ctx); err == nil {
		t.Fatal("corrupt acknowledgement treated as absence")
	}
}

func TestBrowseTXNativeAckRetiresMoreThan512FuturePointsAfterDurableCatchup(t *testing.T) {
	const total = 513
	a, _, hashes := timelineFixture(t, total)
	a.setStatus(func(s *appStatus) { s.TipHash = hashes[total-1] })
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}}}
	if _, err := a.prepareIndexRun(req); err != nil {
		t.Fatal(err)
	}
	a.indexRunMu.Lock()
	a.indexRunState.Run = &indexRunRecord{ID: "future-point-ack-fixture", State: "running", Request: req, InitialTargetHeight: total - 1, TargetHeight: total - 1, TargetHash: hashes[total-1], Consumers: []indexRunProgress{{Index: "tx-locator", From: 0, To: total - 1, Height: -1, AdmissionState: "admitted"}}}
	err := a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session := newBrowseTXPointSession(a, ctx, "future-point-ack-fixture")
	defer session.Abort()
	var blocks []blockView
	previous := ""
	for height := 0; height < total; height++ {
		b := locatorBatchTestBlock(int64(height), previous, 2)
		b.Hash = hashes[height]
		previous = b.Hash
		blocks = append(blocks, b)
		batch, err := singletonBrowseBatch("tx-locator", b, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = lockIndexBrowse(ctx); err != nil {
			t.Fatal(err)
		}
		_, err = session.Stage(ctx, batch, false)
		indexBrowseMu.Unlock()
		if err != nil && !errors.Is(err, errBrowseTXPointStaged) {
			t.Fatal(height, err)
		}
	}
	if err = session.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	target := blocks[total-1].Transactions[1].TxID
	if _, _, err = a.indexedTransactionContext(ctx, target); !errors.Is(err, errIndexLookupIncomplete) {
		t.Fatal("unacknowledged >512 future points became complete", err)
	}
	native, err := openIndexStore(a.dataDir, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	native.head.Mode = "lean"
	if err = native.BeginLocatorBatch(ctx); err != nil {
		t.Fatal(err)
	}
	defer native.AbortLocatorBatch()
	for _, b := range blocks {
		if err = native.appendPreparedBlock(b, 0, "ephemeral", nil); err != nil {
			t.Fatal(b.Height, err)
		}
	}
	if native.DurableCheckpoint() == nil || native.DurableCheckpoint().Height != 335 {
		t.Fatal("physical336 boundary changed", native.DurableCheckpoint())
	}
	if err = a.acknowledgeBrowseTXPointsNative(ctx, native); err != nil {
		t.Fatal(err)
	}
	if ack, err := a.browseTXNativeAck(ctx); err != nil || ack == nil || ack.Height != 335 {
		t.Fatal("staged suffix retired lookup debt", ack, err)
	}
	if err = native.FlushLocatorBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if err = a.acknowledgeBrowseTXPointsNative(ctx, native); err != nil {
		t.Fatal(err)
	}
	if _, err = a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: false}}}); err != nil {
		t.Fatal(err)
	}
	before := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes"))
	view, found, err := a.indexedTransactionContext(ctx, target)
	if err != nil || !found || view.Height != total-1 {
		t.Fatal("complete durable native prefix retained sticky point debt", view, found, err)
	}
	if after := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes")); !reflect.DeepEqual(before, after) {
		t.Fatal("Off reverse lookup performed a repair")
	}
	count := 0
	if err = walkBrowseTXPoints(ctx, a.dataDir, func(indexBrowseTXPoint) error { count++; return nil }); err != nil || count != total {
		t.Fatal("acknowledgement hid physical point evidence", count, err)
	}
}
