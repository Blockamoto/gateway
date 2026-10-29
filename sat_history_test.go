package main

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func Test077RetainedSatMissingIndexAvoidsAuthorityProbes(t *testing.T) {
	for _, state := range []string{"missing", "empty", "corrupt"} {
		t.Run(state, func(t *testing.T) {
			var calls atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, "no RPC should be needed for absent Sat evidence", http.StatusInternalServerError)
			}))
			defer srv.Close()
			a := &app{dataDir: t.TempDir(), settings: testRPCSettings050(t, srv.URL)}
			switch state {
			case "empty":
				if _, err := openIndexStore(a.dataDir, "sat-state"); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				dir := filepath.Join(a.dataDir, "indexes", "sat-state")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "head.json"), []byte("{damaged"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			if _, err := a.retainedSatStore(ctx); err == nil {
				t.Fatal("unavailable evidence supplied a Sat store")
			}
			if result, err := a.retainedSatSpender(ctx, testID(95001), 0); err == nil || result.Snapshot != nil {
				t.Fatal("unavailable membership supplied a snapshot", result, err)
			}
			if _, err := a.retainedSatTransaction(ctx, testID(95001), 1); err == nil {
				t.Fatal("unavailable transaction supplied evidence")
			}
			if _, err := a.retainedSatBlock(ctx, 1); err == nil {
				t.Fatal("unavailable block supplied evidence")
			}
			if _, err := a.retainedSatInputValues(ctx, transactionView{TxID: testID(95001)}, 1, testID(95002)); err == nil {
				t.Fatal("unavailable input amounts supplied evidence")
			}
			if calls.Load() != 0 {
				t.Fatalf("local %s evidence triggered %d authority RPCs", state, calls.Load())
			}
		})
	}
}

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
		if h == 0 {
			genesis, _ := hex.DecodeString(genesisHeaderHex)
			copy(header, genesis)
		}
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

func Test077RetainedSatSpenderMembershipUsesCapturedCommitment(t *testing.T) {
	ctx := context.Background()
	a, s, block := retainedSatFixture(t, 3)
	coin := testID(93001)
	for _, b := range []blockView{block(0, testCoinbase(testID(93000), subsidyAtHeight(0))), block(1, testCoinbase(coin, 100, subsidyAtHeight(1)-100))} {
		if err := s.appendBlock(b, 0, "pruned"); err != nil {
			t.Fatal(err)
		}
	}
	a.status.HeaderCount, a.status.HeaderHeight = 2, 1
	before, err := a.retainedSatSpender(ctx, coin, 0)
	if err != nil || before.ConfirmedState != "unspent_at_snapshot" || before.Snapshot == nil || before.Snapshot.Height != 1 || before.Snapshot.Hash != s.checkpoint.BlockHash {
		t.Fatal(before, err)
	}
	saved, err := indexStoreHead(a.dataDir, "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	savedBatch, err := saved.readBatch(saved.checkpoint.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	spend := testID(93003)
	next := block(2, testCoinbase(testID(93002), subsidyAtHeight(2)), testTx(spend, []inputView{{PrevTxID: coin}}, 100))
	if err = s.appendBlock(next, 0, "pruned"); err != nil {
		t.Fatal(err)
	}
	a.status.HeaderCount, a.status.HeaderHeight = 3, 2
	// The immutable membership adapter must still answer from the captured
	// commitment, even if the active head has advanced and consumed the coin.
	satStateMu.Lock()
	oldOutput, have, err := retainedSatSnapshotOutput077(ctx, saved, savedBatch, coin, 0)
	satStateMu.Unlock()
	if err != nil || !have || oldOutput.State != "unspent" || oldOutput.Point.Height != 1 {
		t.Fatal("captured membership was reopened at a different head", oldOutput, have, err)
	}
	miss, err := a.retainedSatSpender(ctx, coin, 0)
	if err == nil || miss.Snapshot != nil || miss.ConfirmedState != "unknown" {
		t.Fatal("new head miss became unspent or acquired an old snapshot", miss, err)
	}
	current, err := a.retainedSatSpender(ctx, spend, 0)
	if err != nil || current.Snapshot == nil || current.Snapshot.Height != 2 || current.Snapshot.Hash != next.Hash || current.ConfirmedState != "unspent_at_snapshot" {
		t.Fatal("membership snapshot does not identify its own commitment", current, err)
	}
	settings, _, stop := satline077RPC(t, map[int64]string{0: genesisHashDisplay})
	defer stop()
	a.settings = settings
	if a.indexChainState(s.checkpoint) != "selected_chain" {
		t.Fatal("fixture did not exercise the old provider fallback")
	}
	stale, err := a.retainedSatSpender(ctx, spend, 0)
	if err == nil || stale.Snapshot != nil || stale.ConfirmedState == "unspent_at_snapshot" {
		t.Fatal("shorter selected Core chain used the stale mirror", stale, err)
	}
	unchanged, err := indexStoreHead(a.dataDir, "sat-state")
	if err != nil || unchanged.checkpoint.Commitment != s.checkpoint.Commitment {
		t.Fatal("read-only stale query changed stored state", unchanged, err)
	}
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
	txio, e := openIndexStore(a.dataDir, "txo-spender")
	if e != nil {
		t.Fatal(e)
	}
	txio.retainOutputValues = true
	for _, b := range blocks {
		if e = txio.appendBlock(b, 0, "ephemeral"); e != nil {
			t.Fatal(e)
		}
	}
	a.status.HeaderCount, a.status.HeaderHeight = 3, 2
	// No Blocks index, raw store, Core, or peer exists: every hop must be
	// supplied by the compact receipts and current Sat output tree.
	backend := newSatlineEvidenceBackend077(a, context.Background())
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
			if result.Hops[i].Type != kind || result.Hops[i].SpenderProvider != satlineCompactSource {
				t.Fatal(result.Hops)
			}
		}
	}
	retained, e := backend.BlockByHeight(2)
	if e != nil || retained.SourceNetwork != satlineCompactSource || integrityVerified(retained) {
		t.Fatal("compact layout misrepresented as raw verification", retained, e)
	}
	tx, e := backend.Transaction(one, 2)
	if e != nil || tx.Source != satlineCompactSource || tx.Tx.TxID != one {
		t.Fatal(tx, e)
	}
	s.retainSatHistory = false
	a.status.HeaderCount, a.status.HeaderHeight = 5, 4
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
	if retained, e = newSatlineEvidenceBackend077(a, context.Background()).BlockByHeight(2); e != nil || retained.SourceNetwork != satlineCompactSource {
		t.Fatal("disabling range recording erased independent FIFO facts", e)
	}
}

// Model an authenticated inscription batch saved by an older version before
// the mainnet scan-origin guard existed. These Sat tests intentionally use
// height 2 to exercise a short genesis-first history, and must still be able
// to read a previously saved record without granting new pre-origin writes.
func savePreOriginInscriptionFixture(t *testing.T, root string, block blockView, extracted *inscriptionBlockResult, retention string) *indexStore {
	t.Helper()
	batch, err := singletonBrowseBatchMode(indexRunSelection{Index: "inscriptions", indexRunOptions: indexRunOptions{Mode: "full", ConventionalIDs: boolPointer(false)}}, block, extracted)
	if err != nil {
		t.Fatal(err)
	}
	// Install a previously saved, authenticated singleton checkpoint directly;
	// no production writer is invoked for a pre-origin block.
	dir := filepath.Join(root, "indexes", "inscriptions")
	if err = atomicWriteJSON(filepath.Join(dir, "commits", batch.Checkpoint.Commitment+".json"), batch); err != nil {
		t.Fatal(err)
	}
	if err = atomicWriteJSON(filepath.Join(dir, "head.json"), indexHead{Mode: "full", Commitment: batch.Checkpoint.Commitment, Retention: retention}); err != nil {
		t.Fatal(err)
	}
	store, err := indexStoreHead(root, "inscriptions")
	if err != nil || store.checkpoint == nil {
		t.Fatal(err)
	}
	return store
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
			occ := savePreOriginInscriptionFixture(t, a.dataDir, b, &x, "ephemeral")
			batch, e := occ.readBatch(occ.checkpoint.Commitment)
			if e != nil || batch.Inscriptions[0].CanonicalNumberKnown {
				t.Fatal("invented canonical number", e)
			}
		}
	}
	// Run actual bounded reclamation before discovery. The current identity
	// root must preserve same-block born/spent inscriptions independently of
	// old transaction receipts and optional historical output range retention.
	cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.locatorContext = cleanup
	for {
		err := s.collectOrdinalState()
		if errors.Is(err, errOrdinalMaintenancePending) {
			continue
		}
		if err != nil {
			t.Fatal("fixture reclamation failed", err)
		}
		break
	}
	s.locatorContext = context.Background()
	headBefore, err := os.ReadFile(filepath.Join(s.dir, "head.json"))
	if err != nil {
		t.Fatal(err)
	}
	jobBefore := indexDigest(a.indexJobSnapshot())
	policiesBefore, err := a.indexLivePolicies()
	if err != nil {
		t.Fatal(err)
	}
	// The same-block input output was consumed before the final block root;
	// the durable identity map must preserve its sats past the recovery window.
	for _, id := range []string{one + "i0", two + "i0"} {
		identity := a.indexedSatIdentity(id, 2, reveal.Hash)
		if identity == nil || !identity.Known || identity.SatNumber == nil || *identity.SatNumber != firstSatAtHeight(1) {
			t.Fatal(id, identity)
		}
		discovered, err := a.discoverInscriptionSat(context.Background(), id)
		if err != nil || indexDigest(discovered) != indexDigest(*identity) || discovered.RevealHeight != 2 || discovered.RevealHash != reveal.Hash || discovered.Profile != inscriptionSatProfile {
			t.Fatal("discovery did not reuse the exact committed inscription identity", discovered, identity, err)
		}
		if _, err := os.Stat(a.inscriptionSatPath(id)); !os.IsNotExist(err) {
			t.Fatal("stored identity was copied into a separate discovery cache", err)
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
	headAfter, err := os.ReadFile(filepath.Join(s.dir, "head.json"))
	if err != nil || string(headAfter) != string(headBefore) || indexDigest(a.indexJobSnapshot()) != jobBefore {
		t.Fatal("identity discovery changed Sat state or started a build", err)
	}
	policiesAfter, err := a.indexLivePolicies()
	if err != nil || indexDigest(policiesAfter) != indexDigest(policiesBefore) {
		t.Fatal("identity discovery changed Live policies", err)
	}
	for _, relative := range []string{"blocks/raw", "blocks/hex", "blocks/json", "indexes/sources"} {
		entries, err := os.ReadDir(filepath.Join(a.dataDir, relative))
		if !os.IsNotExist(err) && (err != nil || len(entries) != 0) {
			t.Fatal("identity discovery materialized raw or decoded block copies", relative, entries, err)
		}
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
