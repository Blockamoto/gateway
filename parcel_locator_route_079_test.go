package main

import (
	"context"
	"fmt"
	"os"
	"testing"
)

func TestParcelLocatorRouteRequiresCommittedCompletePrefix079(t *testing.T) {
	timeline := indexTimelineView{TipKnown: true, TipHeight: 10, Tracks: []indexTimelineTrack{{ID: "tx-locator", Sources: []indexTimelineSource{{ID: "gateway_browsed_index", Kind: "browse_records", Complete: true, Ranges: []heightInterval{{0, 10}}}}}}}
	if _, err := parcelLocatorRouteGaps079(timeline, 10); err == nil {
		t.Fatal("browsed facts cannot establish full locator prerequisite")
	}
	timeline.Tracks[0].Sources = append(timeline.Tracks[0].Sources, indexTimelineSource{ID: "gateway_committed_index", Kind: "committed_records", Complete: true, Ranges: []heightInterval{{0, 3}, {5, 10}}})
	gaps, err := parcelLocatorRouteGaps079(timeline, 10)
	if err != nil || len(gaps) != 1 || gaps[0].From != 4 || gaps[0].To != 4 {
		t.Fatalf("gap inventory: %+v %v", gaps, err)
	}
	timeline.Tracks[0].Sources[1].Ranges = []heightInterval{{0, 10}}
	if gaps, err = parcelLocatorRouteGaps079(timeline, 10); err != nil || len(gaps) != 0 {
		t.Fatalf("complete prefix: %+v %v", gaps, err)
	}
	timeline.TipKnown = false
	if _, err = parcelLocatorRouteGaps079(timeline, 10); err == nil {
		t.Fatal("unknown selected chain accepted")
	}
}

func TestParcelLocatorRouteChecksBytesBudgetRestartAndChain079(t *testing.T) {
	ctx := context.Background()
	a, raw := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	block, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "real genesis fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openIndexStore(a.dataDir, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	store.head.Mode = "full"
	if err := store.appendBlock(block, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	timeline := indexTimelineView{TipKnown: true, TipHeight: 0, TipHash: genesisHashDisplay, Tracks: []indexTimelineTrack{{ID: "tx-locator", Sources: []indexTimelineSource{{ID: "gateway_committed_index", Kind: "committed_records", Complete: true, Ranges: []heightInterval{{0, 0}}}}}}}
	reads := 0
	a.blockBaseAcquire = func(_ context.Context, target blockTarget, _ appSettings) (blockView, error) {
		reads++
		if target.HashDisplay != block.Hash {
			return blockView{}, fmt.Errorf("wrong selected block")
		}
		return block, nil
	}
	budget := &parcelRawScanBudget079{remaining: 3}
	spender, transaction, blockAt, err := a.newParcelLocatorRoute079(ctx, timeline, 0, budget)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := transaction(ctx, block.Transactions[0].TxID, 0)
	if err != nil || evidence.Tx.TxID != block.Transactions[0].TxID || evidence.BlockHash != genesisHashDisplay {
		t.Fatalf("located verified transaction: %+v %v", evidence, err)
	}
	if _, err = blockAt(ctx, 0); err != nil {
		t.Fatal(err)
	}
	point := satlinePoint{TxID: block.Transactions[0].TxID, Vout: 0, Height: 0, TxIndex: 0, BlockHash: block.Hash}
	result, err := spender(ctx, point, 0, nil)
	if err != nil || result.ConfirmedState != "unknown" {
		t.Fatalf("locator cannot infer unspent: %+v %v", result, err)
	}
	if _, err = blockAt(ctx, 0); err == nil {
		t.Fatal("approved read/fetch budget ignored")
	}
	if reads != 1 {
		t.Fatalf("shared checked block cache not reused: %d", reads)
	}
	// Restart against the same committed locator head, then remove bytes from
	// the injected source. A locator remains only a location and cannot supply values.
	a.blockBaseAcquire = func(context.Context, blockTarget, appSettings) (blockView, error) {
		return blockView{}, fmt.Errorf("source bytes unavailable")
	}
	_, transaction, _, err = a.newParcelLocatorRoute079(ctx, timeline, 0, &parcelRawScanBudget079{remaining: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = transaction(ctx, block.Transactions[0].TxID, 0); err == nil {
		t.Fatal("locator alone supplied missing bytes")
	}
	// A changed selected tip invalidates the captured route authority.
	_, _, blockAt, err = a.newParcelLocatorRoute079(ctx, timeline, 0, &parcelRawScanBudget079{remaining: 3})
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteAt(make([]byte, 80), 0); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = blockAt(ctx, 0); err == nil {
		t.Fatal("replaced selected chain accepted")
	}
}
