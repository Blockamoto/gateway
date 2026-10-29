package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func deleteTXPointFixture(t *testing.T) (*app, []blockView) {
	t.Helper()
	a := runTestApp(t)
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}}}); err != nil {
		t.Fatal(err)
	}
	var blocks []blockView
	for height, raw := range [][]byte{testGenesisBlockPayload(t), mainnetBlockOne(t)} {
		digest := hash256(raw[:80])
		block, err := parseBlockDetailed(int64(height), reverseHex(digest[:]), raw[:80], raw, "point deletion fixture", false)
		if err != nil {
			t.Fatal(err)
		}
		blocks = append(blocks, block)
	}
	return a, blocks
}

func deleteSaveTXPoint(t *testing.T, a *app, block blockView) indexBatch {
	t.Helper()
	batch, err := singletonBrowseBatch("tx-locator", block, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.persistBrowseBatch(context.Background(), "tx-locator", batch); err != nil {
		t.Fatal(err)
	}
	point, have, err := readBrowseTXPoint(context.Background(), a.dataDir, block.Height)
	if err != nil || !have || point.Commitment != batch.Checkpoint.Commitment {
		t.Fatal("fixture did not publish the new point receipt", point, have, err)
	}
	return batch
}

func TestIndexDeleteTXPointRangePreservesNeighborAndLegacyAbsence(t *testing.T) {
	a, blocks := deleteTXPointFixture(t)
	first := deleteSaveTXPoint(t, a, blocks[0])
	second := deleteSaveTXPoint(t, a, blocks[1])
	neighborPath := browseTXPointPath(a.dataDir, 1)
	before, err := os.ReadFile(neighborPath)
	if err != nil {
		t.Fatal(err)
	}
	q := indexDeleteRequest{Indices: []string{"tx-locator"}, Range: &indexDeleteRange{From: deleteHeight(0), To: deleteHeight(0)}}
	deleteConfirmBatch(t, a, q)
	if _, have, err := readBrowseTXPoint(context.Background(), a.dataDir, 0); err != nil || have {
		t.Fatal("selected point remains active", have, err)
	}
	after, err := os.ReadFile(neighborPath)
	if err != nil || string(after) != string(before) {
		t.Fatal("unselected point receipt changed", err)
	}
	store, head, err := a.browseStore("tx-locator")
	if err != nil || len(head.Blocks) != 1 || head.Blocks["1"] != second.Checkpoint.Commitment {
		t.Fatal("neighbor membership changed", head, err)
	}
	if _, err := os.Stat(filepath.Join(store.dir, "head.json")); !os.IsNotExist(err) {
		t.Fatal("point deletion materialized a legacy head", err)
	}
	if _, err := os.Stat(filepath.Join(store.dir, "commits", first.Checkpoint.Commitment+".json")); !os.IsNotExist(err) {
		t.Fatal("selected point commit remains", err)
	}
	if _, err := store.readBatch(second.Checkpoint.Commitment); err != nil {
		t.Fatal("neighbor commit was removed", err)
	}
	native, err := indexStoreHead(a.dataDir, "tx-locator")
	if err != nil || native.checkpoint != nil {
		t.Fatal("point deletion changed native prefix", err)
	}
}

func TestIndexDeleteTXPointReviewRejectsNewPointInsideRange(t *testing.T) {
	a, blocks := deleteTXPointFixture(t)
	deleteSaveTXPoint(t, a, blocks[0])
	q := indexDeleteRequest{Indices: []string{"tx-locator"}, Range: &indexDeleteRange{From: deleteHeight(0), To: deleteHeight(1)}}
	a.updateBackgroundWork.Wait()
	review, err := a.reviewIndexDeletionBatch(context.Background(), q)
	if err != nil || !review.Ready {
		t.Fatal(review, err)
	}
	deleteSaveTXPoint(t, a, blocks[1])
	q.ReviewID = review.ReviewID
	if _, err := a.deleteIndexDataBatch(context.Background(), q); err == nil || !strings.Contains(err.Error(), "review deletion again") {
		t.Fatal("new unreviewed point was admitted to deletion", err)
	}
	for _, height := range []int64{0, 1} {
		if _, have, err := readBrowseTXPoint(context.Background(), a.dataDir, height); err != nil || !have {
			t.Fatal("stale review removed data", height, have, err)
		}
	}
}

func TestIndexDeleteTXPointReviewPreservesNewPointOutsideRange(t *testing.T) {
	a, blocks := deleteTXPointFixture(t)
	deleteSaveTXPoint(t, a, blocks[0])
	q := indexDeleteRequest{Indices: []string{"tx-locator"}, Range: &indexDeleteRange{From: deleteHeight(0), To: deleteHeight(0)}}
	a.updateBackgroundWork.Wait()
	review, err := a.reviewIndexDeletionBatch(context.Background(), q)
	if err != nil || !review.Ready {
		t.Fatal(review, err)
	}
	deleteSaveTXPoint(t, a, blocks[1])
	q.ReviewID = review.ReviewID
	if _, err := a.deleteIndexDataBatch(context.Background(), q); err != nil {
		t.Fatal("unrelated point unnecessarily changed the reviewed selection", err)
	}
	if _, have, err := readBrowseTXPoint(context.Background(), a.dataDir, 1); err != nil || !have {
		t.Fatal("new unrelated point was removed", have, err)
	}
}

func TestIndexDeleteTXPointApplyRejectsChangedCapturedReceipt(t *testing.T) {
	a, blocks := deleteTXPointFixture(t)
	batch := deleteSaveTXPoint(t, a, blocks[0])
	act := indexDeleteAction{Item: indexDeleteItem{Index: "tx-locator"}, Review: "captured fixture"}
	if err := a.planIndexDeleteBrowse(context.Background(), &act, &indexDeleteRange{From: deleteHeight(0), To: deleteHeight(0)}); err != nil || act.Browse == nil || len(act.Browse.Points) != 1 {
		t.Fatal("point snapshot was not captured", act.Browse, err)
	}
	path := browseTXPointPath(a.dataDir, 0)
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	mutations := 0
	err := a.applyIndexDeleteBrowse(context.Background(), act.Browse, func(string) error { mutations++; return nil }, func(string) error { mutations++; return nil })
	if err == nil || mutations != 0 {
		t.Fatal("changed receipt reached a deletion mutation", mutations, err)
	}
	if _, err := act.Browse.Store.readBatch(batch.Checkpoint.Commitment); err != nil {
		t.Fatal("changed snapshot lost its saved commit", err)
	}
}

func TestIndexDeleteTXPointRangeDoesNotMigrateLegacyUnion(t *testing.T) {
	a, blocks := deleteTXPointFixture(t)
	legacy, err := singletonBrowseBatch("tx-locator", blocks[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Checkpoint.DependencyFingerprint = indexDigest("retained prior point source")
	legacy.Checkpoint.Commitment = checkpointHash(legacy.Checkpoint)
	dir := filepath.Join(a.dataDir, "indexes", "browse", "tx-locator")
	if err := atomicWriteJSON(filepath.Join(dir, "commits", legacy.Checkpoint.Commitment+".json"), legacy); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteJSON(filepath.Join(dir, "head.json"), indexBrowseHead{Schema: 1, Blocks: map[string]string{"0": legacy.Checkpoint.Commitment}}); err != nil {
		t.Fatal(err)
	}
	deleteSaveTXPoint(t, a, blocks[0])
	neighbor := deleteSaveTXPoint(t, a, blocks[1])
	deleteConfirmBatch(t, a, indexDeleteRequest{Indices: []string{"tx-locator"}, Range: &indexDeleteRange{From: deleteHeight(0), To: deleteHeight(0)}})
	_, old, err := a.rawBrowseStore("tx-locator")
	if err != nil || len(old.Blocks) != 0 {
		t.Fatal("deletion resurrected the prior point or migrated a new leaf", old, err)
	}
	store, union, err := a.browseStore("tx-locator")
	if err != nil || len(union.Blocks) != 1 || union.Blocks["1"] != neighbor.Checkpoint.Commitment {
		t.Fatal("independent new leaf was not retained", union, err)
	}
	if _, err := os.Stat(filepath.Join(store.dir, "commits", legacy.Checkpoint.Commitment+".json")); !os.IsNotExist(err) {
		t.Fatal("selected legacy generation remains", err)
	}
}

func TestIndexDeleteTXPointApplyRejectsReplacementReceipt(t *testing.T) {
	a, blocks := deleteTXPointFixture(t)
	deleteSaveTXPoint(t, a, blocks[1])
	act := indexDeleteAction{Item: indexDeleteItem{Index: "tx-locator"}, Review: "captured fixture"}
	if err := a.planIndexDeleteBrowse(context.Background(), &act, &indexDeleteRange{From: deleteHeight(1), To: deleteHeight(1)}); err != nil {
		t.Fatal(err)
	}
	// Synthetic selected-header replacement isolates receipt identity. The
	// genuine source/parser proof path is covered by the point-store tests.
	fork := bitmapDevelopmentFixtureBlock(1, blocks[0].Hash, "replacement branch fact")
	installRunProtocolHeader(t, a, &fork)
	replacement := deleteSaveTXPoint(t, a, fork)
	mutations := 0
	err := a.applyIndexDeleteBrowse(context.Background(), act.Browse, func(string) error { mutations++; return nil }, func(string) error { mutations++; return nil })
	if err == nil || !strings.Contains(err.Error(), "review deletion again") || mutations != 0 {
		t.Fatal("replacement receipt was removed under an older snapshot", mutations, err)
	}
	point, have, err := readBrowseTXPoint(context.Background(), a.dataDir, 1)
	if err != nil || !have || point.Commitment != replacement.Checkpoint.Commitment {
		t.Fatal("replacement point was changed", point, have, err)
	}
}

func TestIndexDeleteTXPointApplyPreservesNativeVolatileStage(t *testing.T) {
	a, blocks := deleteTXPointFixture(t)
	deleteSaveTXPoint(t, a, blocks[0])
	act := indexDeleteAction{Item: indexDeleteItem{Index: "tx-locator"}, Review: "captured fixture"}
	if err := a.planIndexDeleteBrowse(context.Background(), &act, &indexDeleteRange{From: deleteHeight(0), To: deleteHeight(0)}); err != nil {
		t.Fatal(err)
	}
	native, err := openIndexStoreContext(context.Background(), a.dataDir, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	native.head.Mode = "lean"
	if err := native.BeginLocatorBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(native.AbortLocatorBatch)
	if err := native.appendPreparedBlock(blocks[0], 0, "ephemeral", nil); err != nil {
		t.Fatal(err)
	}
	n, bytes, records := native.LocatorBatchStats()
	if n != 1 || native.DurableCheckpoint() != nil {
		t.Fatal("fixture did not stage a volatile native block")
	}
	if err := a.applyIndexDeleteBrowse(context.Background(), act.Browse, func(string) error { return nil }, os.Remove); err != nil {
		t.Fatal(err)
	}
	afterN, afterBytes, afterRecords := native.LocatorBatchStats()
	if afterN != n || afterBytes != bytes || afterRecords != records || native.DurableCheckpoint() != nil || native.checkpoint.Height != 0 {
		t.Fatal("browse point deletion changed native volatile staging", afterN, afterBytes, afterRecords)
	}
}

func TestIndexDeleteTXPointWholeReviewBindsLeafSnapshot(t *testing.T) {
	for _, legacyAPI := range []bool{false, true} {
		name := "batch"
		if legacyAPI {
			name = "legacy_single_index"
		}
		t.Run(name, func(t *testing.T) {
			a, blocks := deleteTXPointFixture(t)
			deleteSaveTXPoint(t, a, blocks[0])
			a.updateBackgroundWork.Wait()
			q := indexDeleteRequest{Indices: []string{"tx-locator"}}
			var review indexDeleteReview
			var err error
			if legacyAPI {
				review, err = a.reviewIndexDeletion(context.Background(), "tx-locator")
			} else {
				review, err = a.reviewIndexDeletionBatch(context.Background(), q)
			}
			if err != nil || !review.Ready {
				t.Fatal(review, err)
			}
			deleteSaveTXPoint(t, a, blocks[1])
			if legacyAPI {
				_, err = a.deleteIndexData(context.Background(), "tx-locator", review.ReviewID)
			} else {
				q.ReviewID = review.ReviewID
				_, err = a.deleteIndexDataBatch(context.Background(), q)
			}
			if err == nil || !strings.Contains(err.Error(), "review deletion again") {
				t.Fatal("whole-store deletion admitted an unreviewed point leaf", err)
			}
			for _, height := range []int64{0, 1} {
				if _, have, err := readBrowseTXPoint(context.Background(), a.dataDir, height); err != nil || !have {
					t.Fatal("stale whole review changed saved points", height, have, err)
				}
			}
		})
	}
}
