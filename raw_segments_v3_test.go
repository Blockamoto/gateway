package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func segmentFixtureV3(t *testing.T) (*app, [][]byte, []blockView) {
	t.Helper()
	a, genesis := testAppWithGenesis(t)
	a.updateBackgroundWork.Wait()
	one := mainnetBlockOne(t)
	if e := os.WriteFile(a.headersPath, append(append([]byte(nil), genesis[:80]...), one[:80]...), 0600); e != nil {
		t.Fatal(e)
	}
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	a.status.HeaderCount, a.status.HeaderHeight = 2, 1
	raws := [][]byte{genesis, one}
	views := []blockView{}
	for h, raw := range raws {
		hash := hash256(raw[:80])
		v, e := parseBlockDetailed(int64(h), reverseHex(hash[:]), raw[:80], raw, "verified fixture", false)
		if e != nil {
			t.Fatal(e)
		}
		views = append(views, v)
	}
	return a, raws, views
}
func targetSegmentV3(v blockView, raw []byte) blockTarget {
	h, _ := displayHashRaw(v.Hash)
	return blockTarget{Height: v.Height, HashDisplay: v.Hash, HashRaw: h, ExpectedHeader: raw[:80]}
}

func TestRawSegmentsV3ExactRestartBlockOnlyAndExplicitLocator(t *testing.T) {
	a, raws, views := segmentFixtureV3(t)
	s, e := openIndexStore(a.dataDir, "blocks")
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range views {
		if e = s.appendPreparedBlock(v, 0, "ephemeral", nil); e != nil {
			t.Fatal(e)
		}
	}
	cold, e := openIndexStore(a.dataDir, "blocks")
	if e != nil {
		t.Fatal(e)
	}
	for h, v := range views {
		commit, ready, e := cold.activeLocatorCommit(int64(h))
		if e != nil || !ready {
			t.Fatal(e)
		}
		b, e := cold.readBatch(commit)
		if e != nil {
			t.Fatal(e)
		}
		encoded, _ := json.Marshal(b)
		if b.Bitcoin.BodySchema != 3 || b.Bitcoin.TransactionCount != uint64(len(v.Transactions)) || len(b.Bitcoin.TxIDs) != 0 || len(b.Bitcoin.Transactions) != 0 || bytes.Contains(encoded, []byte(`"txids"`)) || bytes.Contains(encoded, []byte(`"script_sig"`)) {
			t.Fatal("Raw persisted derived transaction records", string(encoded))
		}
		got, decoded, e := readIndexedRawBody(a.dataDir, targetSegmentV3(v, raws[h]), nil)
		if e != nil || !bytes.Equal(got, raws[h]) || decoded.Transactions[0].TxID != v.Transactions[0].TxID {
			t.Fatal("cold exact bytes", e)
		}
		if _, found, e := a.indexedTransaction(v.Transactions[0].TxID); e != nil || found {
			t.Fatal("Raw created or scanned tx hash lookup", found, e)
		}
	}
	shard, _ := graphShardPath(cold.dir, cold.locatorKind(), views[0].Transactions[0].TxID)
	if _, e = os.Stat(shard); !os.IsNotExist(e) {
		t.Fatal("Raw wrote reverse shard", e)
	}
	if _, e = os.Stat(filepath.Join(a.dataDir, "indexes", "blocks", "bodies-v2")); !os.IsNotExist(e) {
		t.Fatal("Raw wrote per-body files", e)
	}
	locator, e := openIndexStore(a.dataDir, "tx-locator")
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range views {
		if e = locator.appendBlock(v, 0, "ephemeral"); e != nil {
			t.Fatal(e)
		}
	}
	r, found, e := a.indexedTransaction(views[1].Transactions[0].TxID)
	if e != nil || !found || !r.TransactionVerified || r.Height != 1 {
		t.Fatal("explicit Locator lost retained body resolution", r, found, e)
	}
	rows, complete, e := rawSegmentInventory(context.Background(), a.dataDir)
	if e != nil || !complete || len(rows) != 2 {
		t.Fatal(rows, complete, e)
	}
}

func TestRawSegmentsV3ProofTamperingAndColdIntegrity(t *testing.T) {
	a, raws, views := segmentFixtureV3(t)
	for _, mutation := range []string{"buffer", "header", "height", "count", "hash", "flags"} {
		t.Run(mutation, func(t *testing.T) {
			v := views[0]
			v.verifiedRaw = append([]byte(nil), v.verifiedRaw...)
			switch mutation {
			case "buffer":
				v.verifiedRaw[len(v.verifiedRaw)-1] ^= 1
			case "header":
				v.verifiedRaw[0] ^= 1
			case "height":
				v.Height++
			case "count":
				v.TransactionCount++
			case "hash":
				v.Hash = strings.Repeat("1", 64)
			case "flags":
				v.Verification.MerkleRoot = false
			}
			if _, e := appendRawSegment(context.Background(), t.TempDir(), v, rawSegmentBytes); e == nil {
				t.Fatal("tampered private proof accepted")
			}
		})
	}
	record, e := appendRawSegment(context.Background(), a.dataDir, views[0], rawSegmentBytes)
	if e != nil {
		t.Fatal(e)
	}
	p := rawSegmentPath(a.dataDir, record.RawBody.Segment.File)
	f, e := os.OpenFile(p, os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.WriteAt([]byte{raws[0][len(raws[0])-1] ^ 1}, record.RawBody.Segment.Offset+rawSegmentFrameBytes+int64(len(raws[0])-1)); e != nil {
		t.Fatal(e)
	}
	f.Close()
	if _, _, e = readIndexedRawBody(a.dataDir, targetSegmentV3(views[0], raws[0]), record.RawBody); e == nil {
		t.Fatal("cold raw corruption accepted")
	}
}

func TestRawSegmentsV3UnpublishedTailRotationAndReorg(t *testing.T) {
	a, raws, views := segmentFixtureV3(t)
	s, e := openIndexStore(a.dataDir, "blocks")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.appendBlock(views[0], 0, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	first, e := s.readBatch(s.head.Commitment)
	if e != nil {
		t.Fatal(e)
	}
	p := rawSegmentPath(a.dataDir, 0)
	f, e := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	tail := []byte("incomplete unpublished crash tail")
	f.Write(tail)
	f.Sync()
	f.Close()
	st, _ := os.Stat(p)
	before := st.Size()
	if e = s.appendBlock(views[1], 0, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	second, e := s.readBatch(s.head.Commitment)
	if e != nil {
		t.Fatal(e)
	}
	if second.Bitcoin.RawBody.Segment.Offset != before {
		t.Fatal("append overwrote unpublished bytes")
	}
	got, e := os.ReadFile(p)
	if e != nil || !bytes.Equal(got[first.Bitcoin.RawBody.Bytes+rawSegmentFrameBytes:before], tail) {
		t.Fatal("tail changed", e)
	}
	if e = s.reconcile(func(h int64) (string, error) {
		if h == 0 {
			return views[0].Hash, nil
		}
		return strings.Repeat("f", 64), nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, _, e = readIndexedRawBody(a.dataDir, targetSegmentV3(views[1], raws[1]), nil); e != nil {
		t.Fatal("run rewind removed independently retained source", e)
	}
	if e = s.appendBlock(views[1], 0, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	again, _ := s.readBatch(s.head.Commitment)
	if *again.Bitcoin.RawBody.Segment != *second.Bitcoin.RawBody.Segment {
		t.Fatal("replaying progress duplicated an existing immutable body")
	}
	root := t.TempDir()
	limit := int64(rawSegmentFrameBytes + len(raws[0]) + 5)
	r0, e := appendRawSegment(context.Background(), root, views[0], limit)
	if e != nil {
		t.Fatal(e)
	}
	// Same verified block forces a second file with a deterministic tiny budget.
	r1, e := appendRawSegment(context.Background(), root, views[0], limit)
	if e != nil || r0.RawBody.Segment.File == r1.RawBody.Segment.File {
		t.Fatal("segment cap did not rotate", e)
	}
	if _, _, e = readIndexedRawBody(root, targetSegmentV3(views[0], raws[0]), r0.RawBody); e != nil {
		t.Fatal("rotation damaged neighbor", e)
	}
}

func TestRawSegmentsV3RangePrunePreservesNeighborsAndFreshAppend(t *testing.T) {
	a, raws, views := segmentFixtureV3(t)
	s, e := openIndexStore(a.dataDir, "blocks")
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range views {
		if e = s.appendBlock(v, 0, "ephemeral"); e != nil {
			t.Fatal(e)
		}
	}
	path := rawSegmentPath(a.dataDir, 0)
	before, _ := os.ReadFile(path)
	zero := int64(0)
	q := indexDeleteRequest{Indices: []string{"blocks"}, Range: &indexDeleteRange{From: &zero, To: &zero}}
	review, e := a.reviewIndexDeletionBatch(context.Background(), q)
	if e != nil || !review.Ready || len(review.Items) != 1 {
		t.Fatal(review, e)
	}
	if review.Items[0].Raw.SegmentBodies != 1 || review.Items[0].Raw.SegmentPhysicalRetainedBytes != int64(len(before)) {
		t.Fatal("misleading segment accounting", review.Items)
	}
	q.ReviewID = review.ReviewID
	if _, e = a.deleteIndexDataBatch(context.Background(), q); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("range deletion rewrote shared segment")
	}
	if _, _, e = readIndexedRawBody(a.dataDir, targetSegmentV3(views[0], raws[0]), nil); e == nil {
		t.Fatal("pruned body remains available")
	}
	if _, _, e = readIndexedRawBody(a.dataDir, targetSegmentV3(views[1], raws[1]), nil); e != nil {
		t.Fatal("neighbor removed", e)
	}
	fresh, e := appendRawSegment(context.Background(), a.dataDir, views[0], rawSegmentBytes)
	if e != nil {
		t.Fatal(e)
	}
	if got, _, e := readIndexedRawBody(a.dataDir, targetSegmentV3(views[0], raws[0]), fresh.RawBody); e != nil || !bytes.Equal(got, raws[0]) {
		t.Fatal("old deletion hides fresh bytes", e)
	}
}

func TestRawSegmentsV3LegacyContinuationDoesNotMigrateOrBackfill(t *testing.T) {
	a, raws, views := segmentFixtureV3(t)
	legacy := legacyBlocks074(t, a, raws[0])
	old := legacy.head.Commitment
	// Genuine pre-existing reverse evidence remains a read-only compatibility path.
	installLegacyRawLocators(t, legacy)
	shard, _ := graphShardPath(legacy.dir, legacy.locatorKind(), views[0].Transactions[0].TxID)
	before, e := os.ReadFile(shard)
	if e != nil {
		t.Fatal(e)
	}
	s, e := openIndexStore(a.dataDir, "blocks")
	if e != nil {
		t.Fatal(e)
	}
	if s.head.Commitment != old {
		t.Fatal("opening automatically migrated legacy evidence")
	}
	if e = s.appendBlock(views[1], 0, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(shard)
	if !bytes.Equal(before, after) {
		t.Fatal("Raw continued by adding tx reverse rows")
	}
	r, found, e := a.indexedTransaction(views[0].Transactions[0].TxID)
	if e != nil || !found || !r.TransactionVerified {
		t.Fatal("legacy reverse compatibility lost", found, e)
	}
	if _, found, e = a.indexedTransaction(views[1].Transactions[0].TxID); e != nil || found {
		t.Fatal("legacy continuation backfilled modern Raw txs", found, e)
	}
	if _, e = os.Stat(filepath.Join(a.dataDir, "indexes", "blocks", "storage-v2.json")); !os.IsNotExist(e) {
		t.Fatal("implicit migration root created", e)
	}
}

func TestRawSegmentsV3SparseOnDemandCatalogAndGlobalOff(t *testing.T) {
	a, raws, views := segmentFixtureV3(t)
	// No Raw build exists. An on-demand view of block one can still save only
	// that body, independently of a scan from zero or any current run frontier.
	if _, e := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "blocks", Armed: true}}}); e != nil {
		t.Fatal(e)
	}
	v, e := a.fetchBlockWithPolicy(targetSegmentV3(views[0], raws[0]), "ephemeral")
	if e != nil {
		t.Fatal(e)
	}
	if v.Hash != views[0].Hash {
		t.Fatal("wrong view")
	}
	if _, e = os.Stat(filepath.Join(a.dataDir, "indexes", "blocks", "head.json")); !os.IsNotExist(e) {
		t.Fatal("on-demand retention started a run", e)
	}
	if _, e = retainVerifiedRawBlock(context.Background(), a.dataDir, views[1], func() error { return checkTimelineRawBlocksOn(context.Background(), a.dataDir) }); e != nil {
		t.Fatal(e)
	}
	rows, complete, e := rawSegmentInventory(context.Background(), a.dataDir)
	if e != nil || !complete || len(rows) != 2 {
		t.Fatal(rows, complete, e)
	}
	unknown := targetSegmentV3(views[1], raws[1])
	unknown.Height = -1
	unknown.ExpectedHeader = nil
	if got, _, e := readIndexedRawBody(a.dataDir, unknown, nil); e != nil || !bytes.Equal(got, raws[1]) {
		t.Fatal("block-hash access without run", e)
	}
	if _, e = a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "blocks", Armed: false}}}); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(rawSegmentPath(a.dataDir, 0))
	if _, e = a.fetchBlockWithPolicy(targetSegmentV3(views[0], raws[0]), "retain"); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(rawSegmentPath(a.dataDir, 0))
	if !bytes.Equal(before, after) {
		t.Fatal("Raw Off retained new source bytes")
	}
	if _, e = os.Stat(filepath.Join(a.dataDir, "indexes", "sources", views[0].Hash+".block")); !os.IsNotExist(e) {
		t.Fatal("legacy retain choice bypassed Raw Off", e)
	}
}

func TestRawSegmentsV3HashOnlyAnchoringAndPublicationFence(t *testing.T) {
	a, raws, views := segmentFixtureV3(t)
	v, e := parseBlockDetailed(-1, views[1].Hash, nil, raws[1], "hash-only fixture", false)
	if e != nil {
		t.Fatal(e)
	}
	first, e := retainVerifiedRawBlock(context.Background(), a.dataDir, v, nil)
	if e != nil {
		t.Fatal(e)
	}
	second, e := retainVerifiedRawBlock(context.Background(), a.dataDir, views[1], nil)
	if e != nil || *first.RawBody.Segment != *second.RawBody.Segment {
		t.Fatal("anchoring duplicated exact body", e)
	}
	rows, complete, e := rawSegmentInventory(context.Background(), a.dataDir)
	if e != nil || !complete || len(rows) != 1 || rows[0].Height != 1 || !rawSegmentFramePresent(a.dataDir, rows[0]) {
		t.Fatal("anchored sparse membership", rows, e)
	}
	root := t.TempDir()
	calls := 0
	_, e = retainVerifiedRawBlock(context.Background(), root, views[0], func() error {
		calls++
		if calls == 2 {
			return errIndexRunConsumerStopped
		}
		return nil
	})
	if e == nil {
		t.Fatal("late Off published body")
	}
	if r, e := rawBodyCatalogReceipt(context.Background(), root, targetSegmentV3(views[0], raws[0])); e != nil || r != nil {
		t.Fatal("unpublished tail acquired membership", r, e)
	}
	st, e := os.Stat(rawSegmentPath(root, 0))
	if e != nil || st.Size() != int64(rawSegmentFrameBytes+len(raws[0])) {
		t.Fatal("unpublished durable tail not retained safely", e)
	}
}

func TestRawSegmentsV3WholeDeleteReclaimsOwnedFilesKeepsUnknown(t *testing.T) {
	a, _, views := segmentFixtureV3(t)
	if _, e := retainVerifiedRawBlock(context.Background(), a.dataDir, views[1], nil); e != nil {
		t.Fatal(e)
	}
	unknown := filepath.Join(rawSegmentDir(a.dataDir), "catalog-user-notes.dat")
	if e := os.WriteFile(unknown, []byte("unrelated"), 0600); e != nil {
		t.Fatal(e)
	}
	q := indexDeleteRequest{Indices: []string{"blocks"}}
	r, e := a.reviewIndexDeletionBatch(context.Background(), q)
	if e != nil || !r.Ready || !r.Exists || r.Items[0].Raw.SegmentPhysicalRemovedBytes == 0 {
		t.Fatal(r, e)
	}
	q.ReviewID = r.ReviewID
	if _, e = a.deleteIndexDataBatch(context.Background(), q); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(rawSegmentPath(a.dataDir, 0)); !os.IsNotExist(e) {
		t.Fatal("owned segment remained", e)
	}
	if got, e := os.ReadFile(unknown); e != nil || string(got) != "unrelated" {
		t.Fatal("unproven file removed", e)
	}
}

func TestRawSegmentsV3PrunedHeightDoesNotHideLaterHashOnlyReacquisition(t *testing.T) {
	a, raws, views := segmentFixtureV3(t)
	old, e := retainVerifiedRawBlock(context.Background(), a.dataDir, views[1], nil)
	if e != nil {
		t.Fatal(e)
	}
	one := int64(1)
	q := indexDeleteRequest{Indices: []string{"blocks"}, Range: &indexDeleteRange{From: &one, To: &one}}
	r, e := a.reviewIndexDeletionBatch(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	q.ReviewID = r.ReviewID
	if _, e = a.deleteIndexDataBatch(context.Background(), q); e != nil {
		t.Fatal(e)
	}
	v, e := parseBlockDetailed(-1, views[1].Hash, nil, raws[1], "verified hash-only reacquisition", false)
	if e != nil {
		t.Fatal(e)
	}
	fresh, e := retainVerifiedRawBlock(context.Background(), a.dataDir, v, nil)
	if e != nil || *fresh.RawBody.Segment == *old.RawBody.Segment {
		t.Fatal("pruned bytes reused", e)
	}
	if got, _, e := readIndexedRawBody(a.dataDir, targetSegmentV3(views[1], raws[1]), nil); e != nil || !bytes.Equal(got, raws[1]) {
		t.Fatal("old height tombstone hides newer hash membership", e)
	}
}

func TestRawLocalAvailabilityDoesNotTreatConfiguredArchiveAsComplete(t *testing.T) {
	a, raws, views := segmentFixtureV3(t)
	a.settings.ArchiveDir = t.TempDir()
	// Remove the one preexisting cache fixture so only the sparse archive can
	// provide local hints. Configuration alone does not suppress peer work.
	if e := os.Remove(filepath.Join(a.dataDir, "blocks", "raw", "0-"+views[0].Hash+".block")); e != nil {
		t.Fatal(e)
	}
	if a.hasLocalBlockSource(targetSegmentV3(views[0], raws[0])) || a.hasLocalBlockSource(targetSegmentV3(views[1], raws[1])) {
		t.Fatal("empty configured archive suppressed missing-body acquisition")
	}
	p := filepath.Join(a.settings.ArchiveDir, "blocks", views[0].Hash[:2], views[0].Hash+".blk")
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, raws[0], 0600); e != nil {
		t.Fatal(e)
	}
	if !a.hasLocalBlockSource(targetSegmentV3(views[0], raws[0])) || a.hasLocalBlockSource(targetSegmentV3(views[1], raws[1])) {
		t.Fatal("sparse archive was treated as complete, or actual body ignored")
	}
}
