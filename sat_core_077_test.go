package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func Test077SatCoreFragmentPaginationOffsetsAndForgedCursor(t *testing.T) {
	ctx := context.Background()
	s, err := openIndexStore(t.TempDir(), "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(81000, subsidyAtHeight(0)))
	if err = s.appendBlock(b0, 0, "pruned"); err != nil {
		t.Fatal(err)
	}
	values := make([]uint64, 351)
	for i := 0; i < 350; i++ {
		values[i] = 1
	}
	values[350] = subsidyAtHeight(1) - 350
	cb := utxoFixtureCoinbase(81001, values...)
	b1 := utxoFixtureBlock(1, b0.Hash, cb)
	if err = s.appendBlock(b1, 0, "pruned"); err != nil {
		t.Fatal(err)
	}
	inputs := []inputView{}
	for parity := 0; parity < 2; parity++ {
		for v := parity; v < 350; v += 2 {
			inputs = append(inputs, inputView{N: len(inputs), PrevTxID: cb.TxID, PrevVout: uint32(v)})
		}
	}
	tx := utxoFixtureSpend(81002, inputs, 350)
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(81003, subsidyAtHeight(2)), tx)
	if err = s.appendBlock(b2, 0, "pruned"); err != nil {
		t.Fatal(err)
	}
	first, err := querySatOutputRangesContext(ctx, s.profileRoot, "0.1.2", 97, "")
	if err != nil || !first.HasMore || first.RangeCount != 350 || first.RangeOffset != 0 {
		t.Fatal(first, err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	var forged satRangeCursor077
	if err = json.Unmarshal(raw, &forged); err != nil {
		t.Fatal(err)
	}
	forged.Offset++
	forged.Checksum = satRangeCursorDigest077(forged)
	raw, _ = json.Marshal(forged)
	if _, err = querySatOutputRangesContext(ctx, s.profileRoot, "0.1.2", 97, base64.RawURLEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("forged recomputed-checksum offset was trusted")
	}
	count := len(first.Ranges)
	cursor := first.NextCursor
	for cursor != "" {
		page, err := querySatOutputRangesContext(ctx, s.profileRoot, "0.1.2", 97, cursor)
		if err != nil || page.RangeOffset != uint64(count) {
			t.Fatal(count, page, err)
		}
		count += len(page.Ranges)
		cursor = page.NextCursor
	}
	if count != 350 {
		t.Fatal(count)
	}
	status, err := satStateStatusContext(ctx, s.profileRoot)
	if err != nil {
		t.Fatal(err)
	}
	b3 := utxoFixtureBlock(3, b2.Hash, utxoFixtureCoinbase(81004, subsidyAtHeight(3)))
	if err = s.appendBlock(b3, 0, "pruned"); err != nil {
		t.Fatal(err)
	}
	if _, err = querySatOutputRangesContext(ctx, s.profileRoot, "0.1.2", 97, first.NextCursor); err == nil {
		t.Fatal("stale range cursor accepted", status.Checkpoint)
	}
}

func Test077SatCoreSameBlockHistoryBurnedAndInscriptionPaging(t *testing.T) {
	ctx := context.Background()
	s, err := openIndexStore(t.TempDir(), "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	s.retainSatHistory = true
	b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(92000, subsidyAtHeight(0)))
	cb := utxoFixtureCoinbase(92001, 10, subsidyAtHeight(1)-10)
	b1 := utxoFixtureBlock(1, b0.Hash, cb)
	plain := inscriptionTestScript(nil, []byte("known"))
	unbound := inscriptionTestScript([]byte{4}, []byte{1}, nil, []byte("unbound"))
	one := utxoFixtureSpend(92002, []inputView{{N: 0, PrevTxID: cb.TxID, Witness: ordWitness050(plain)}}, 6, 2, 0)
	one.Outputs[1].ScriptPubKey = "6a"
	two := utxoFixtureSpend(92003, []inputView{{N: 0, PrevTxID: one.TxID, Witness: ordWitness050(append(append([]byte{}, plain...), unbound...))}}, 5)
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(92004, subsidyAtHeight(2)+3), one, two)
	for _, b := range []blockView{b0, b1, b2} {
		if err = s.appendBlock(b, 0, "pruned"); err != nil {
			t.Fatal(b.Height, err)
		}
	}
	first := firstSatAtHeight(1)
	for _, tc := range []struct {
		sat           uint64
		output, state string
		offset        uint64
	}{{first, "0.2.2", "unspent", 0}, {first + 6, "1.1.2", "burned", 0}, {first + 8, "0.0.2", "unspent", subsidyAtHeight(2)}, {first + 5, "0.0.2", "unspent", subsidyAtHeight(2) + 2}} {
		got, err := querySatNumberContext(ctx, s.profileRoot, tc.sat)
		if err != nil || got.Output != tc.output || got.State != tc.state || got.Offset != tc.offset {
			t.Fatal(tc, got, err)
		}
	}
	historical, err := querySatOutputRangesContext(ctx, s.profileRoot, "0.1.2", 5, "")
	if err != nil || historical.State != "historical" || !historical.HistoryRetained || historical.ValueSats != 6 {
		t.Fatal("same-block spent ranges were not retained", historical, err)
	}
	zero, err := querySatOutputRangesContext(ctx, s.profileRoot, "2.1.2", 5, "")
	if err != nil || zero.State != "unspent" || zero.ValueSats != 0 || len(zero.Ranges) != 0 || zero.HasMore {
		t.Fatal("zero-valued output treated as missing", zero, err)
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err := querySatRangeInscriptionsContext(ctx, s.profileRoot, first, first+1, 1, cursor)
		if err != nil || !page.IdentityComplete || len(page.Rows) != 1 || page.Rows[0].SatNumber != first || seen[page.Rows[0].ID] {
			t.Fatal(page, err)
		}
		seen[page.Rows[0].ID] = true
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 2 || !seen[one.TxID+"i0"] || !seen[two.TxID+"i0"] {
		t.Fatal(seen)
	}
	identity, have, err := satInscriptionIdentityContext(ctx, s.profileRoot, two.TxID+"i1")
	if err != nil || !have || identity.Known || identity.State != "unbound" || identity.SatNumber != nil {
		t.Fatal(identity, have, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = querySatRangeInscriptionsContext(ctx, s.profileRoot, first, first+1, 1, ""); !errors.Is(err, context.Canceled) {
		t.Fatal("identity traversal ignored cancellation", err)
	}
}

func Test077SatCoreArchivedSharedFactsSurviveCollection(t *testing.T) {
	ctx := context.Background()
	s, _ := sat077Fixture(t, false)
	old, err := querySatOutputRangesContext(ctx, s.profileRoot, "0.1.2", 5, "")
	if err != nil || old.FactRef == nil {
		t.Fatal(old, err)
	}
	if err = resetSatStateForRebuildContext(ctx, s.profileRoot, "archive-with-shared-facts"); err != nil {
		t.Fatal(err)
	}
	fresh, err := openIndexStore(s.profileRoot, "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	if err = fresh.appendBlock(utxoFixtureBlock(0, "", utxoFixtureCoinbase(93000, subsidyAtHeight(0))), 0, "pruned"); err != nil {
		t.Fatal(err)
	}
	aged := time.Now().Add(-2 * time.Hour)
	if err = filepath.Walk(outputFactDirectory(s.profileRoot), func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if !info.IsDir() {
			return os.Chtimes(path, aged, aged)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = collectSharedOutputFactsContext(ctx, s.profileRoot); err != nil {
		t.Fatal(err)
	}
	fact, err := newUTXOFactReader(ctx, s.profileRoot).read(*old.FactRef)
	if err != nil || fact.ValueSats != 6 || fact.ScriptPubKey != address076P2SH {
		t.Fatal("preserved archive lost its shared facts", fact, err)
	}
}

func Test077SatCorePinTraversalLeavesSnapshotReadsResponsive(t *testing.T) {
	ctx := context.Background()
	s, _ := sat077Fixture(t, false)
	visited := 0
	if err := visitSatFactPagesContext077(ctx, s.profileRoot, func(page string) error {
		// Reentrant status reads used to deadlock under the traversal's
		// state mutex. The writer lock pins this generation independently.
		status, err := satStateStatusContext(ctx, s.profileRoot)
		if err != nil || status.Checkpoint == nil || status.Checkpoint.Commitment != s.head.Commitment || !validHash(page) {
			t.Fatal(status, page, err)
		}
		visited++
		return nil
	}); err != nil || visited == 0 {
		t.Fatal(visited, err)
	}
}

func Test077SatCoreLegacyProfileRequiresReviewedRebuildAndPreservesArchive(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := openIndexStore(root, "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	b := indexBatch{Sats: &satIndexSnapshot{Profile: "gateway-sat-fifo-mainnet-v1"}, Checkpoint: indexCheckpoint{Schema: 1, Definition: "sat-state", Version: 1, RuleHash: testID(91001), Network: "bitcoin-mainnet", From: 0, Height: 0, BlockHash: genesisHashDisplay}}
	b.Checkpoint.RecordsHash = batchRecordsHash(b)
	b.Checkpoint.Commitment = checkpointHash(b.Checkpoint)
	if err = atomicWriteJSON(filepath.Join(s.dir, "commits", b.Checkpoint.Commitment+".json"), b); err != nil {
		t.Fatal(err)
	}
	head := indexHead{Commitment: b.Checkpoint.Commitment}
	if err = atomicWriteJSON(filepath.Join(s.dir, "head.json"), head); err != nil {
		t.Fatal(err)
	}
	prior, err := os.ReadFile(filepath.Join(s.dir, "head.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = satStateStatusContext(ctx, root); !errors.Is(err, ErrSatProfileIncompatible) {
		t.Fatal("legacy profile not classified for reviewed rebuild", err)
	}
	a := &app{dataDir: root, settings: appSettings{CoreDisabled: true}, status: appStatus{HeaderHeight: -1}}
	if got := a.indexSatStatus077(); got.State != "rebuild_required" {
		t.Fatal(got)
	}
	unchanged, _ := os.ReadFile(filepath.Join(s.dir, "head.json"))
	if string(unchanged) != string(prior) {
		t.Fatal("query changed legacy evidence")
	}
	if err = resetSatStateForRebuildContext(ctx, root, "reviewed077"); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "indexes", "sat-state-archives", indexDigest("reviewed077"))
	saved, _ := os.ReadFile(filepath.Join(archive, "head.json"))
	if string(saved) != string(prior) {
		t.Fatal("legacy evidence not preserved")
	}
	fresh, err := openIndexStore(root, "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	genesis := utxoFixtureBlock(0, "", utxoFixtureCoinbase(91002, subsidyAtHeight(0)))
	if err = fresh.appendBlock(genesis, 0, "pruned"); err != nil {
		t.Fatal(err)
	}
	if err = resetSatStateForRebuildContext(ctx, root, "reviewed077"); err != nil {
		t.Fatal(err)
	}
	retained, err := openIndexStore(root, "sat-state")
	if err != nil || retained.checkpoint == nil {
		t.Fatal("same-job retry reset committed state", err)
	}
	if err = collectSharedOutputFactsContext(ctx, root); err != nil {
		t.Fatal("authentic legacy archive blocked shared collection", err)
	}
	if err = os.WriteFile(filepath.Join(archive, "head.json"), []byte("damaged preserved archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = collectSharedOutputFactsContext(ctx, root); !errors.Is(err, ErrSatArchivePinsUnavailable) || errors.Is(err, ErrSatStateCorrupt) {
		t.Fatal("archive damage invalidated current state or permitted cleanup", err)
	}
	status, err := satStateStatusContext(ctx, root)
	if err != nil || status.State != "ready" {
		t.Fatal(status, err)
	}
}

func sat077Fixture(t *testing.T, history bool) (*indexStore, []blockView) {
	t.Helper()
	s, err := openIndexStore(t.TempDir(), "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	s.retainSatHistory = history
	b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(77000, subsidyAtHeight(0)))
	cb := utxoFixtureCoinbase(77001, 10, subsidyAtHeight(1)-10)
	cb.Outputs[0].ScriptPubKey = address076P2PKH
	b1 := utxoFixtureBlock(1, b0.Hash, cb)
	tx := utxoFixtureSpend(77002, []inputView{{N: 0, PrevTxID: cb.TxID}}, 6)
	tx.Outputs[0].ScriptPubKey = address076P2SH
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(77003, subsidyAtHeight(2)+2), tx)
	blocks := []blockView{b0, b1, b2}
	for _, b := range blocks {
		if err = s.appendBlock(b, 0, "pruned"); err != nil {
			t.Fatal(b.Height, err)
		}
	}
	return s, blocks
}
func Test077SatCoreFIFOSharedFactsLossAndSelectiveHistory(t *testing.T) {
	ctx := context.Background()
	s, blocks := sat077Fixture(t, false)
	first := firstSatAtHeight(1)
	for _, tc := range []struct {
		sat           uint64
		output, state string
		offset        uint64
	}{{first + 3, "0.1.2", "unspent", 3}, {first + 6, "0.0.2", "unspent", subsidyAtHeight(2)}, {first + 8, "", "lost", 0}, {0, "0.0.0", "genesis_unspendable", 0}} {
		got, err := querySatNumberContext(ctx, s.profileRoot, tc.sat)
		if err != nil || !got.Known || got.Output != tc.output || got.State != tc.state || got.Offset != tc.offset {
			t.Fatal(tc, got, err)
		}
	}
	got, err := querySatOutputRangesContext(ctx, s.profileRoot, "0.1.2", 1, "")
	if err != nil || got.State != "unspent" || got.RangeOffset != 0 || got.ValueSats != 6 || got.ScriptPubKey != address076P2SH || len(got.Ranges) != 1 || got.Ranges[0] != (satRange{first, first + 6}) {
		t.Fatal(got, err)
	}
	unknown, err := querySatOutputRangesContext(ctx, s.profileRoot, "0.0.1", 5, "")
	if err != nil || unknown.State != "unknown" || len(unknown.Ranges) != 0 {
		t.Fatal("spent output falsely retained", unknown, err)
	}
	u, err := openUTXOStoreContext(ctx, s.profileRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range blocks {
		utxoApply(t, u, b)
	}
	coin := utxoRequireCoin(t, u, "0.1.2", 6)
	if got.FactRef == nil || *got.FactRef != coin.FactRef {
		t.Fatal("Sat duplicated shared output facts", got, coin)
	}
	batch, err := s.readBatch(s.head.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	var row json.RawMessage
	key, _ := satOutputKey077("0.1.2")
	have, err := newOrdinalTree(s.ordinalPages(), batch.Sats.UTXORoot).get(key, &row)
	if err != nil || !have {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"txid", "point", "value", "script_pub"} {
		if strings.Contains(string(row), forbidden) {
			t.Fatal("range row duplicates coin/native data", string(row))
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	filepath.Walk(outputFactDirectory(s.profileRoot), func(path string, info os.FileInfo, e error) error {
		if e == nil && !info.IsDir() {
			return os.Chtimes(path, old, old)
		}
		return e
	})
	if err = collectSharedOutputFactsContext(ctx, s.profileRoot); err != nil {
		t.Fatal(err)
	}
	if _, err = querySatNumberContext(ctx, s.profileRoot, 0); err != nil {
		t.Fatal("Sat-only genesis facts were collected", err)
	}
	h, _ := sat077Fixture(t, true)
	historical, err := querySatOutputRangesContext(ctx, h.profileRoot, "0.0.1", 5, "")
	if err != nil || historical.State != "historical" || !historical.HistoryRetained || historical.ValueSats != 10 {
		t.Fatal("historical ranges missing or falsely unspent", historical, err)
	}
}
func Test077SatRecoveryCadenceRotationRestoreReplayAndDeepFailure(t *testing.T) {
	ctx := context.Background()
	s, err := openIndexStore(t.TempDir(), "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	blocks := []blockView{}
	prev := ""
	for h := int64(0); h <= 300; h++ {
		b := utxoFixtureBlock(h, prev, utxoFixtureCoinbase(uint64(78000+h), subsidyAtHeight(h)))
		if err = s.appendBlock(b, 0, "pruned"); err != nil {
			t.Fatal(h, err)
		}
		blocks = append(blocks, b)
		prev = b.Hash
	}
	status, err := satStateStatusContext(ctx, s.profileRoot)
	if err != nil || len(status.Savepoints) != 3 || status.OldestSavepointHeight != 144 {
		t.Fatal(status, err)
	}
	want := []int64{144, 288, 300}
	for i, p := range status.Savepoints {
		if p.Height != want[i] {
			t.Fatal(status.Savepoints)
		}
	}
	before := s.head.Commitment
	missing := errors.New("headers unavailable")
	if err = s.reconcileSatStateSelected(ctx, 300, func(h int64) (string, error) {
		if h == 300 {
			return testID(999999), nil
		}
		return "", missing
	}); !errors.Is(err, missing) || s.head.Commitment != before {
		t.Fatal("missing evidence rewound head", err)
	}
	if err = s.reconcileSatStateSelected(ctx, 299, func(h int64) (string, error) { return blocks[h].Hash, nil }); err != nil || s.checkpoint.Height != 288 {
		t.Fatal("recovery did not choose newest eligible savepoint", s.checkpoint, err)
	}
	for h := 289; h <= 300; h++ {
		if err = s.appendBlock(blocks[h], 0, "pruned"); err != nil {
			t.Fatal(h, err)
		}
	}
	if s.head.Commitment != before {
		t.Fatal("replay changed deterministic state", s.head.Commitment, before)
	}
	before = s.head.Commitment
	if err = s.reconcileSatStateSelected(ctx, 143, func(h int64) (string, error) { return blocks[h].Hash, nil }); !errors.Is(err, ErrSatRebuildRequired) || s.head.Commitment != before {
		t.Fatal("deeper recovery silently reset", err)
	}
	for pass := 0; pass < 40; pass++ {
		err = s.collectSat077State()
		if err == nil {
			break
		}
		if !errors.Is(err, errOrdinalMaintenancePending) {
			t.Fatal(err)
		}
	}
	if err != nil {
		t.Fatal("savepoint page collection did not finish", err)
	}
	if err = s.reconcileSatStateSelected(ctx, 150, func(h int64) (string, error) { return blocks[h].Hash, nil }); err != nil || s.checkpoint.Height != 144 {
		t.Fatal("oldest savepoint collected", s.checkpoint, err)
	}
	if _, err = querySatNumberContext(ctx, s.profileRoot, firstSatAtHeight(144)); err != nil {
		t.Fatal("restored location unavailable", err)
	}
}
func Test077SatRecoverySavepointIntervalsDeduplicateAndInterrupt(t *testing.T) {
	points := []satSavepoint077{}
	for h := int64(0); h <= 432; h++ {
		points = satRotateSavepoints077(points, indexCheckpoint{Height: h, BlockHash: testID(uint64(h + 1)), Commitment: testID(uint64(h + 500))})
		if len(points) > 4 {
			t.Fatal(h, points)
		}
	}
	if len(points) != 3 || points[0].Height != 288 || points[1].Height != 420 || points[2].Height != 432 || len(points[2].Intervals) != 2 {
		t.Fatal(points)
	}
	s, blocks := sat077Fixture(t, false)
	before := s.head.Commitment
	injected := errors.New("interrupt before head")
	s.satCommitHook = func(stage string) error {
		if stage == "before_head" {
			return injected
		}
		return nil
	}
	defer func() { s.satCommitHook = nil }()
	b3 := utxoFixtureBlock(3, blocks[2].Hash, utxoFixtureCoinbase(79900, subsidyAtHeight(3)))
	if err := s.appendBlock(b3, 0, "pruned"); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	reopened, err := openIndexStore(s.profileRoot, "sat-state")
	if err != nil || reopened.head.Commitment != before {
		t.Fatal("interrupted publication advanced head", err)
	}
	s.satCommitHook = nil
	if err = reopened.appendBlock(b3, 0, "pruned"); err != nil {
		t.Fatal(err)
	}
}
