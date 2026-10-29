package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestParcelTXIOAuditResumesAndRejectsUnresolved079(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = true
	genesis, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "genesis fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	s, err := openIndexStore(a.dataDir, "txo-spender")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.appendBlock(genesis, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	b1 := txioTestBlock(t, a, 1, genesisHashDisplay, 1)
	if err = s.appendBlock(b1, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	selected := func(h int64) (string, error) {
		switch h {
		case 0:
			return genesisHashDisplay, nil
		case 1:
			return b1.Hash, nil
		default:
			return "", errors.New("unknown selected height")
		}
	}
	blocks := map[int64]blockView{0: genesis, 1: b1}
	reader := func(budget *parcelRawScanBudget079) func(context.Context, int64) (blockView, error) {
		return func(_ context.Context, h int64) (blockView, error) {
			if e := budget.take(); e != nil {
				return blockView{}, e
			}
			return blocks[h], nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	budget1 := &parcelRawScanBudget079{used: 10000000}
	interrupted := func(work context.Context, h int64) (blockView, error) {
		if h == 0 {
			cancel()
			return blockView{}, context.Canceled
		}
		return reader(budget1)(work, h)
	}
	first, err := auditParcelTXIO079(ctx, s, 1, selected, budget1, interrupted)
	if !errors.Is(err, context.Canceled) || first.NextHeight != 0 || first.Complete {
		t.Fatalf("cancelled audit: %+v %v", first, err)
	}
	budget2 := &parcelRawScanBudget079{used: 10000000}
	second, err := auditParcelTXIO079(context.Background(), s, 1, selected, budget2, reader(budget2))
	if err != nil || !second.Complete || budget2.count() <= 10000000 {
		t.Fatalf("audit restart: %+v %v", second, err)
	}
	reorg := func(h int64) (string, error) {
		if h == 1 {
			return testID(999999), nil
		}
		return selected(h)
	}
	budget3 := &parcelRawScanBudget079{}
	if _, err = auditParcelTXIO079(context.Background(), s, 1, reorg, budget3, reader(budget3)); err == nil {
		t.Fatal("reorg accepted completed audit")
	}
	missing := strings.Repeat("c", 64)
	b2 := txioTestBlock(t, a, 2, b1.Hash, 2, transactionView{TxID: strings.Repeat("b", 64), Inputs: []inputView{{PrevTxID: missing, PrevVout: 0}}})
	blocks[2] = b2
	if err = s.appendBlock(b2, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	select2 := func(h int64) (string, error) {
		if h == 2 {
			return b2.Hash, nil
		}
		return selected(h)
	}
	budget4 := &parcelRawScanBudget079{}
	result, err := auditParcelTXIO079(context.Background(), s, 2, select2, budget4, reader(budget4))
	if err == nil || !strings.Contains(err.Error(), "unresolved") || result.Complete {
		t.Fatalf("unresolved input was admitted: %+v %v", result, err)
	}
}

func TestParcelTXIORouteUsesBridgeAndCheckedBytes079(t *testing.T) {
	ctx := context.Background()
	a, raw0 := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = true
	raw1, hash1 := installBlockOneHeader063(t, a)
	genesis, err := parseBlockDetailed(0, genesisHashDisplay, raw0[:80], raw0, "real genesis", false)
	if err != nil {
		t.Fatal(err)
	}
	one, err := parseBlockDetailed(1, hash1, raw1[:80], raw1, "real block one", false)
	if err != nil {
		t.Fatal(err)
	}
	s, err := openIndexStore(a.dataDir, "txo-spender")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.appendBlock(genesis, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	if err = s.appendBlock(one, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	reads := 0
	a.blockBaseAcquire = func(_ context.Context, target blockTarget, _ appSettings) (blockView, error) {
		reads++
		switch target.Height {
		case 0:
			return genesis, nil
		case 1:
			return one, nil
		default:
			return blockView{}, fmt.Errorf("unexpected height")
		}
	}
	timeline := indexTimelineView{TipKnown: true, TipHeight: 1, TipHash: hash1, Tracks: []indexTimelineTrack{{ID: "txo-spender", Sources: []indexTimelineSource{{ID: "gateway_committed_index", Kind: "committed_records", Complete: true, Ranges: []heightInterval{{0, 1}}}}}}}
	budget := &parcelRawScanBudget079{}
	prepare, _, transaction, _, err := a.newParcelTXIORoute079(ctx, timeline, 1, budget)
	if err != nil {
		t.Fatal(err)
	}
	if err = prepare(ctx); err != nil {
		t.Fatal(err)
	}
	evidence, err := transaction(ctx, genesis.Transactions[0].TxID, 1)
	if err != nil || evidence.Tx.TxID != genesis.Transactions[0].TxID || evidence.Source != "approved_txio_translation_and_checked_block" {
		t.Fatalf("TXIO bridge lookup: %+v %v", evidence, err)
	}
	if reads != 2 || budget.count() < 2 {
		t.Fatalf("unexpected fetched-block reuse or count: reads=%d checks=%d", reads, budget.count())
	}
	prepareWithoutCounter, _, transactionWithoutCounter, _, err := a.newParcelTXIORoute079(ctx, timeline, 1, nil)
	if err != nil {
		t.Fatalf("nil telemetry blocked route: %v", err)
	}
	if err = prepareWithoutCounter(ctx); err != nil {
		t.Fatalf("nil telemetry blocked audit: %v", err)
	}
	if _, err = transactionWithoutCounter(ctx, genesis.Transactions[0].TxID, 1); err != nil {
		t.Fatalf("nil telemetry blocked checked lookup: %v", err)
	}
}

// Synthetic block/receipts exercise the TXIO reverse-pointer fast path and
// source acquisition; separate real-witness fixtures test Bitcoin
// authentication, not this fabricated transaction chain.
func TestParcelTXIOPositiveLinkUsesCheckedFetchedBlock079(t *testing.T) {
	ctx := context.Background()
	a, raw := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = true
	genesis, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "genesis", false)
	if err != nil {
		t.Fatal(err)
	}
	s, err := openIndexStore(a.dataDir, "txo-spender")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.appendBlock(genesis, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	created, spent := testID(9001), testID(9002)
	b := txioTestBlock(t, a, 1, genesisHashDisplay, 1,
		transactionView{TxID: created, Inputs: []inputView{{PrevTxID: genesis.Transactions[0].TxID, PrevVout: 0}}},
		transactionView{TxID: spent, Inputs: []inputView{{PrevTxID: created, PrevVout: 0}}})
	b.verifiedRaw = make([]byte, 81) // synthetic checked-block stand-in, not a Bitcoin byte fixture
	if err = s.appendBlock(b, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	budget := &parcelRawScanBudget079{}
	visits := 0
	fallback := false
	selected := func(h int64) (string, error) {
		if h == 1 {
			return b.Hash, nil
		}
		if h == 0 {
			return genesisHashDisplay, nil
		}
		return "", fmt.Errorf("missing selected height")
	}
	backend := &parcelCaptureBackend079{app: a, ctx: ctx, hashAt: selected, childHeight: 1, blocks: map[int64]blockView{},
		blockAt: func(_ context.Context, h int64) (blockView, error) {
			if err := budget.take(); err != nil {
				return blockView{}, err
			}
			visits++
			if h != 1 {
				return blockView{}, fmt.Errorf("unexpected source")
			}
			return b, nil
		},
		rawSpender: func(context.Context, satlinePoint, int64, parcelScanObserver079) (spenderLookupView, error) {
			fallback = true
			return spenderLookupView{}, fmt.Errorf("unexpected forward scan")
		},
	}
	point := satlinePoint{TxID: created, Vout: 0, Height: 1, TxIndex: 1, BlockHash: b.Hash}
	result, err := backend.SpenderPoint(point)
	if err != nil || result.ConfirmedState != "confirmed_spent" || result.SpendingTxID != spent || result.SpendingVin != 0 || visits != 1 || budget.count() != 1 || fallback {
		t.Fatalf("TXIO link did not use one checked fetched block: %+v %v visits=%d fallback=%v", result, err, visits, fallback)
	}
}

func TestParcelTXIORouteRequiresCommittedCoverage079(t *testing.T) {
	tline := indexTimelineView{TipKnown: true, TipHeight: 5, Tracks: []indexTimelineTrack{{ID: "txo-spender", RecordedCoverage: []heightInterval{{0, 5}}}}}
	if _, err := parcelTXIORouteGaps079(tline, 5); err == nil {
		t.Fatal("sparse inventory was admitted")
	}
	tline.Tracks[0].Sources = []indexTimelineSource{{ID: "gateway_committed_index", Kind: "committed_records", Complete: true, Ranges: []heightInterval{{0, 2}, {4, 5}}}}
	gaps, err := parcelTXIORouteGaps079(tline, 5)
	if err != nil || len(gaps) != 1 || gaps[0] != (heightInterval{3, 3}) {
		t.Fatalf("coverage gaps %v %v", gaps, err)
	}
}

func TestParcelEvidenceFetchDoesNotFanOut079(t *testing.T) {
	a := runTestApp(t)
	block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	ctx := context.WithValue(context.Background(), parcelEvidenceReadKey079{}, true)
	prepareResolvedBlockExtraction(ctx, a.dataDir, &block)
	if block.inscriptionExtraction != nil {
		t.Fatal("evidence-only read unexpectedly extracted inscriptions")
	}
	if got := a.ingestResolvedVerifiedBlock(ctx, block); len(got) != 0 {
		t.Fatalf("evidence-only read unexpectedly fanned out: %+v", got)
	}
}
