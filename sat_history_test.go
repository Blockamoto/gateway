package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Synthetic verifier receipts isolate derived indexing and lineage. Real block
// authentication has its own decoder fixtures; these are not mainnet scans.
func retainedSatFixture(t *testing.T, count int) (*app, *indexStore, func(int64, ...transactionView) blockView) {
	t.Helper()
	root := t.TempDir()
	headers := make([]byte, count*80)
	hashes := make([]string, count)
	for h := range hashes {
		header := headers[h*80 : (h+1)*80]
		header[0], header[1] = byte(h), byte(h>>8)
		sum := hash256(header)
		hashes[h] = reverseHex(sum[:])
	}
	path := filepath.Join(root, "headers.bin")
	if e := os.WriteFile(path, headers, 0600); e != nil {
		t.Fatal(e)
	}
	a := &app{dataDir: root, headersPath: path, settings: appSettings{CoreDisabled: true, CoreMountDisabled: true, NetworkDisabled: true}, status: appStatus{HeaderCount: int64(count), HeaderHeight: int64(count - 1)}, cacheIndex: newCacheIndex()}
	s, e := openIndexStore(root, "sat-state")
	if e != nil {
		t.Fatal(e)
	}
	block := func(height int64, txs ...transactionView) blockView {
		previous := ""
		if height > 0 {
			previous = hashes[height-1]
		}
		b := satIndexTestBlock(height, previous, txs...)
		b.Hash = hashes[height]
		return b
	}
	return a, s, block
}

// This backend exercises retained receipt algorithms directly, independently
// of the release-locked public transaction and Satline entry points.
type retainedHistoryTestBackend struct{ appSatlineBackend }

func (b retainedHistoryTestBackend) Transaction(id string, before int64) (satlineTxEvidence, error) {
	return b.a.retainedSatTransaction(context.Background(), id, before)
}

func TestSatHistoryOnlyLineageIncludesSameBlockHopsAndFees(t *testing.T) {
	a, s, block := retainedSatFixture(t, 5)
	s.retainSatHistory = true
	coinbase := testID(5101)
	one := testID(5102)
	two := testID(5103)
	blocks := []blockView{block(0, testCoinbase(testID(5100), subsidyAtHeight(0))), block(1, testCoinbase(coinbase, 10, subsidyAtHeight(1)-10)), block(2, testCoinbase(testID(5104), subsidyAtHeight(2)+4), testTx(one, []inputView{{PrevTxID: coinbase}}, 8), testTx(two, []inputView{{PrevTxID: one}}, 6))}
	for _, b := range blocks {
		if e := s.appendBlock(b, 0, "ephemeral"); e != nil {
			t.Fatal(e)
		}
	}
	// No Blocks index, raw store, Core, or peer exists: every hop must be
	// supplied by the compact receipts and current Sat output tree.
	backend := retainedHistoryTestBackend{appSatlineBackend{a}}
	for _, tc := range []struct {
		offset      uint64
		types       []string
		destination string
	}{{1, []string{"output_to_output", "output_to_output"}, two}, {7, []string{"output_to_output", "fee_to_coinbase"}, testID(5104)}, {8, []string{"fee_to_coinbase"}, testID(5104)}} {
		r := newSatlineResolver(backend)
		result := r.traverse(satlinePoint{TxID: coinbase, Vout: 0, Offset: tc.offset, Height: 1, BlockHash: blocks[1].Hash}, 10)
		if result.State != "CURRENTLY_UNSPENT" || result.CurrentSatpoint == nil || result.CurrentSatpoint.TxID != tc.destination || len(result.Hops) != len(tc.types) {
			t.Fatalf("offset%d: %+v", tc.offset, result)
		}
		for i, kind := range tc.types {
			if result.Hops[i].Type != kind || result.Hops[i].SpenderProvider != satHistorySource {
				t.Fatal(result.Hops)
			}
		}
	}
	retained, e := backend.BlockByHeight(2)
	if e != nil || retained.SourceNetwork != satHistorySource || integrityVerified(retained) {
		t.Fatal("compact layout misrepresented as raw verification", retained, e)
	}
	tx, e := backend.Transaction(one, 2)
	if e != nil || tx.Source != satHistorySource || tx.Tx.TxID != one {
		t.Fatal(tx, e)
	}
	s.retainSatHistory = false
	if e = s.appendBlock(block(3, testCoinbase(testID(5105), subsidyAtHeight(3))), 0, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	s.retainSatHistory = true
	if e = s.appendBlock(block(4, testCoinbase(testID(5106), subsidyAtHeight(4))), 0, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	answer, e := a.readIndexedSat(context.Background(), firstSatAtHeight(1)+1)
	if e != nil || answer.HistoryRetained || !answer.HistoryRecording || len(answer.HistoryCoverage) != 2 || answer.HistoryCoverage[0] != (heightInterval{From: 0, To: 2}) || answer.HistoryCoverage[1] != (heightInterval{From: 4, To: 4}) {
		t.Fatal("retention gap hidden", answer, e)
	}
	if _, e = a.retainedSatBlock(context.Background(), 3); e == nil {
		t.Fatal("unrecorded block claimed as retained")
	}
	if _, e = a.retainedSatBlock(context.Background(), 2); e != nil {
		t.Fatal("disabling recording erased old history", e)
	}
}
func TestSatInscriptionIdentitySurvivesPruningWithoutNumberingOrHistory(t *testing.T) {
	a, s, block := retainedSatFixture(t, 260)
	coinbase, one, two := testID(6101), testID(6102), testID(6103)
	plain := inscriptionTestScript(nil, []byte("identity"))
	unbound := inscriptionTestScript([]byte{4}, []byte{1}, nil, []byte("unbound"))
	first := testTx(one, []inputView{{PrevTxID: coinbase, Witness: ordWitness050(plain)}}, 10)
	second := testTx(two, []inputView{{PrevTxID: one, Witness: ordWitness050(append(append([]byte{}, plain...), unbound...))}}, 10)
	var reveal blockView
	for h := int64(0); h < 260; h++ {
		b := block(h, testCoinbase(testID(6200+uint64(h)), subsidyAtHeight(h)))
		if h == 1 {
			b = block(h, testCoinbase(coinbase, 10, subsidyAtHeight(h)-10))
		}
		if h == 2 {
			b = block(h, testCoinbase(testID(6104), subsidyAtHeight(h)), first, second)
			reveal = b
		}
		x, e := extractInscriptionOccurrences(b)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.appendPreparedBlock(b, 0, "ephemeral", &x); e != nil {
			t.Fatal(h, e)
		}
		if h == 2 {
			occ, e := openIndexStore(a.dataDir, "inscriptions")
			if e != nil {
				t.Fatal(e)
			}
			if e = occ.appendPreparedBlock(b, 2, "ephemeral", &x); e != nil {
				t.Fatal(e)
			}
			batch, e := occ.readBatch(occ.checkpoint.Commitment)
			if e != nil || batch.Inscriptions[0].CanonicalNumberKnown {
				t.Fatal("invented canonical number", e)
			}
		}
	}
	// The same-block input output was consumed before the final block root;
	// the durable identity map must preserve its sats past the recovery window.
	for _, id := range []string{one + "i0", two + "i0"} {
		identity := a.indexedSatIdentity(id, 2, reveal.Hash)
		if identity == nil || !identity.Known || identity.SatNumber == nil || *identity.SatNumber != firstSatAtHeight(1) {
			t.Fatal(id, identity)
		}
		if _, e := a.discoverInscriptionSat(context.Background(), id); e == nil || e.Error() != releaseLockReason("sat-state") {
			t.Fatal("stored identity bypassed the independent Sat Index release lock", e)
		}
	}
	identity := a.indexedSatIdentity(two+"i1", 2, reveal.Hash)
	if identity == nil || identity.State != "unbound" || identity.Known || identity.SatNumber != nil {
		t.Fatal(identity)
	}
	b, e := s.readBatch(s.checkpoint.Commitment)
	if e != nil || len(b.Sats.HistoryCoverage) != 0 || len(b.Sats.Movements) != 0 {
		t.Fatal("identity enabled optional history", e)
	}
	row := bitmapRecord{Inscription: one + "i0", Height: 2}
	a.enrichBitmapIdentity(&row, reveal.Hash)
	if row.SatNumber == nil || *row.SatNumber != firstSatAtHeight(1) {
		t.Fatal("Bitmap did not use persistent identity", row)
	}
}
func TestSatIndexCancellationCannotPublishOrLoadFragmentedState(t *testing.T) {
	_, s, block := retainedSatFixture(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.locatorContext = ctx
	if e := s.appendBlock(block(0, testCoinbase(testID(8100), subsidyAtHeight(0))), 0, "ephemeral"); !errors.Is(e, context.Canceled) || s.checkpoint != nil {
		t.Fatal(e, s.checkpoint)
	}
	if _, e := loadSatOutputRangesContext(ctx, s.ordinalPages(), satIndexOutput{RangesRoot: testID(1)}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := s.storeSatOutput(satIndexOutput{Ranges: make([]satRange, 300000)}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
