package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestSatOutpointPagesAvoidSharedTransactionPrefix(t *testing.T) {
	tree := newOrdinalTree(t.TempDir(), "")
	for i := 0; i < 100; i++ {
		if e := tree.put(ordinalOutpointKey(testID(99), uint32(i)), i); e != nil {
			t.Fatal(e)
		}
	}
	root, e := tree.flush()
	if e != nil {
		t.Fatal(e)
	}
	var depth func(string, int) int
	depth = func(hash string, n int) int {
		if hash == "" {
			return n
		}
		page, e := tree.read(hash)
		if e != nil {
			t.Fatal(e)
		}
		max := n
		for _, child := range page.Children {
			if d := depth(child, n+1); d > max {
				max = d
			}
		}
		return max
	}
	if d := depth(root, 0); d > 4 {
		t.Fatalf("same-transaction outputs require %d radix levels", d)
	}
	for i := 0; i < 100; i++ {
		var got int
		ok, e := tree.get(ordinalOutpointKey(testID(99), uint32(i)), &got)
		if e != nil || !ok || got != i {
			t.Fatal(i, got, e)
		}
	}
}

func satIndexTestBlock(height int64, previous string, txs ...transactionView) blockView {
	b := testBlock(height, txs...)
	b.PreviousBlockHash = previous
	b.TransactionCount = uint64(len(txs))
	b.Verification = verificationView{VerifierVersion: blockVerifierVersion, HeaderHash: true, ProofOfWork: true, MerkleRoot: true, TransactionsParsed: true, HeaderChainMatch: true, WitnessPresent: true, WitnessCommitment: true}
	return b
}
func satTestLocation(t *testing.T, s *indexStore, n uint64) satIndexLocation {
	t.Helper()
	b, e := s.readBatch(s.checkpoint.Commitment)
	if e != nil {
		t.Fatal(e)
	}
	tree := newOrdinalTree(s.ordinalPages(), b.Sats.LocationRoot)
	var loc satIndexLocation
	ok, e := tree.predecessor(satRangeKey(n), &loc)
	if e != nil || !ok || n < loc.Start || n >= loc.End {
		t.Fatal(n, loc, e)
	}
	return loc
}
func TestSatIndexFIFOFeesLossRestartAndReorg(t *testing.T) {
	root := t.TempDir()
	s, e := openIndexStore(root, "sat-state")
	if e != nil {
		t.Fatal(e)
	}
	b0 := satIndexTestBlock(0, "", testCoinbase(testID(1), subsidyAtHeight(0)))
	if e = s.appendBlock(b0, 0, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	b1 := satIndexTestBlock(1, b0.Hash, testCoinbase(testID(2), 10, subsidyAtHeight(1)-10))
	if e = s.appendBlock(b1, 0, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	spend := testTx(testID(3), []inputView{{PrevTxID: testID(2), PrevVout: 0}}, 6)
	b2 := satIndexTestBlock(2, b1.Hash, testCoinbase(testID(4), subsidyAtHeight(2)+2), spend)
	if e = s.appendBlock(b2, 0, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	first := firstSatAtHeight(1)
	if loc := satTestLocation(t, s, first+3); loc.Point.TxID != spend.TxID || loc.Point.Offset+first+3-loc.Start != 3 {
		t.Fatal(loc)
	}
	if loc := satTestLocation(t, s, first+6); loc.Point.TxID != testID(4) || loc.Point.Offset != subsidyAtHeight(2) {
		t.Fatal("fee order", loc)
	}
	if loc := satTestLocation(t, s, first+8); loc.State != "lost" {
		t.Fatal("underclaim not retained", loc)
	}
	if loc := satTestLocation(t, s, 0); loc.State != "genesis_unspendable" {
		t.Fatal(loc)
	}
	s, e = openIndexStore(root, "sat-state")
	if e != nil {
		t.Fatal(e)
	}
	if loc := satTestLocation(t, s, first+3); loc.Point.TxID != spend.TxID {
		t.Fatal(loc)
	}
	if e = s.reconcile(func(h int64) (string, error) {
		if h == 2 {
			return testID(0xbad), nil
		}
		return []string{b0.Hash, b1.Hash}[h], nil
	}); e != nil {
		t.Fatal(e)
	}
	if s.checkpoint.Height != 1 {
		t.Fatal(s.checkpoint)
	}
	if loc := satTestLocation(t, s, first+3); loc.Point.TxID != testID(2) {
		t.Fatal("reorg did not restore root", loc)
	}
	bad := satIndexTestBlock(2, b1.Hash, testCoinbase(testID(4), subsidyAtHeight(2)), testTx(testID(5), []inputView{{PrevTxID: testID(999)}}, 1))
	head := s.head.Commitment
	if e = s.appendBlock(bad, 0, "ephemeral"); e == nil || s.head.Commitment != head {
		t.Fatal("missing input advanced coverage", e)
	}
	if s.head.Retention != "ephemeral" {
		t.Fatal("unexpected block retention")
	}
}
func TestSatIndexBIP30DestructionAndIsolatedRange(t *testing.T) {
	s, e := openIndexStore(t.TempDir(), "sat-state")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.deriveSatBlock(satIndexTestBlock(10, "", testCoinbase(testID(8), 50))); e == nil {
		t.Fatal("isolated sat state accepted")
	}
	// Small synthetic persisted precursor isolates the historical duplicate
	// rule; it is not a claim of having indexed mainnet's intervening blocks.
	utxo := newOrdinalTree(s.ordinalPages(), "")
	loc := newOrdinalTree(s.ordinalPages(), "")
	old := satIndexOutput{Point: satlinePoint{TxID: testID(7)}, Value: 10, Ranges: []satRange{{100, 110}}, State: "unspent"}
	_ = utxo.put(ordinalOutpointKey(testID(7), 0), old)
	_ = loc.put(satRangeKey(100), satIndexLocation{100, 110, old.Point, "unspent"})
	u, _ := utxo.flush()
	l, _ := loc.flush()
	b := indexBatch{Sats: &satIndexSnapshot{Profile: satIndexProfile, UTXORoot: u, LocationRoot: l, Outputs: 1}, Checkpoint: indexCheckpoint{Schema: s.definition.CheckpointSchema, Definition: s.definition.ID, Version: s.definition.Version, RuleHash: s.definition.RuleHash, Network: s.definition.Network, From: 0, Height: 91841, BlockHash: testID(991)}}
	b.Checkpoint.RecordsHash = batchRecordsHash(b)
	b.Checkpoint.Commitment = checkpointHash(b.Checkpoint)
	if e = atomicWriteJSON(filepath.Join(s.dir, "commits", b.Checkpoint.Commitment+".json"), b); e != nil {
		t.Fatal(e)
	}
	s.checkpoint = &b.Checkpoint
	next, e := s.deriveSatBlock(satIndexTestBlock(91842, b.Checkpoint.BlockHash, testCoinbase(testID(7), subsidyAtHeight(91842))))
	if e != nil {
		t.Fatal(e)
	}
	tree := newOrdinalTree(s.ordinalPages(), next.LocationRoot)
	var result satIndexLocation
	ok, e := tree.predecessor(satRangeKey(105), &result)
	if e != nil || !ok || result.State != "bip30_destroyed" || next.DestroyedSats != 10 {
		t.Fatal(result, e)
	}
}
func TestSatReverseDiscoveryInputAndCoinbaseFee(t *testing.T) {
	b1 := satIndexTestBlock(1, "", testCoinbase(testID(11), 10, subsidyAtHeight(1)-10))
	spend := testTx(testID(12), []inputView{{PrevTxID: testID(11)}}, 6)
	b2 := satIndexTestBlock(2, b1.Hash, testCoinbase(testID(13), subsidyAtHeight(2)+4), spend)
	backend := &memorySatlineBackend{blocks: map[int64]blockView{1: b1, 2: b2}, tip: 2}
	resolver := newSatlineResolver(backend)
	for _, tc := range []struct {
		point satlinePoint
		want  uint64
	}{{satlinePoint{TxID: testID(12), Offset: 3, Height: 2}, firstSatAtHeight(1) + 3}, {satlinePoint{TxID: testID(13), Offset: subsidyAtHeight(2) + 2, Height: 2}, firstSatAtHeight(1) + 8}, {satlinePoint{TxID: testID(13), Offset: 1, Height: 2}, firstSatAtHeight(2) + 1}} {
		got, _, e := resolver.reverseSatNumber(tc.point)
		if e != nil || got != tc.want {
			t.Fatal(tc, got, e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resolver.ctx = ctx
	if _, _, e := resolver.reverseSatNumber(satlinePoint{TxID: testID(12), Height: 2}); e == nil {
		t.Fatal("cancel ignored")
	}
}

func TestSatIndexFragmentedOutputUsesSegmentedRanges(t *testing.T) {
	s, e := openIndexStore(t.TempDir(), "sat-state")
	if e != nil {
		t.Fatal(e)
	}
	ranges := make([]satRange, 300000)
	for i := range ranges {
		start := uint64(100000000 + i*2)
		ranges[i] = satRange{start, start + 1}
	}
	output := satIndexOutput{Point: satlinePoint{TxID: testID(44)}, Value: uint64(len(ranges)), Ranges: ranges, State: "unspent"}
	oldInline, _ := json.Marshal(output)
	if len(oldInline) <= ordinalPageMaxBytes {
		t.Fatal("fixture must exceed old inline-page ceiling")
	}
	stored, e := s.storeSatOutput(output)
	if e != nil || stored.RangesRoot == "" || len(stored.Ranges) != 0 {
		t.Fatal("fragmented output was not segmented", e)
	}
	tree := newOrdinalTree(s.ordinalPages(), "")
	if e = tree.put(ordinalOutpointKey(testID(44), 0), stored); e != nil {
		t.Fatal(e)
	}
	root, e := tree.flush()
	if e != nil {
		t.Fatal(e)
	}
	if e = collectOrdinalPages(s.ordinalPages(), []string{root}); e != nil {
		t.Fatal(e)
	}
	loaded, e := loadSatOutputRanges(s.ordinalPages(), stored)
	if e != nil || len(loaded) != len(ranges) || loaded[12345] != ranges[12345] || loaded[len(loaded)-1] != ranges[len(ranges)-1] {
		t.Fatal("GC lost indirectly referenced interval segments", len(loaded), e)
	}
}
