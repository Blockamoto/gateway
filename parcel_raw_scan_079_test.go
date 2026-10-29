package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type testParcelObserver079 struct{ observe func(blockView) error }

func (o testParcelObserver079) Observe(b blockView) error     { return o.observe(b) }
func (o testParcelObserver079) HasPrefix(int64) (bool, error) { return false, nil }
func (o testParcelObserver079) Flush() error                  { return nil }
func (o testParcelObserver079) Scanned(blockView) error       { return nil }

func TestParcelRawScanBoundedAndGapAware079(t *testing.T) {
	parent, child := testID(400), testID(401)
	b0 := testBlock(100, testCoinbase(testID(402), 50), testTx(parent, nil, 10))
	b1 := testBlock(101, testCoinbase(testID(403), 50), testTx(child, []inputView{{PrevTxID: parent, PrevVout: 0}}, 10))
	// Synthetic block views exercise traversal policy; real byte validation is
	// provided by the retained reader and separately tested by replay fixtures.
	for _, b := range []*blockView{&b0, &b1} {
		b.Verification = verificationView{VerifierVersion: blockVerifierVersion, HeaderHash: true, ProofOfWork: true, MerkleRoot: true, TransactionsParsed: true}
	}
	blocks := map[int64]*blockView{100: &b0, 101: &b1}
	selected := func(h int64) (string, error) {
		if b := blocks[h]; b != nil {
			return b.Hash, nil
		}
		return "", fmt.Errorf("unknown")
	}
	read := func(_ context.Context, h int64, hash string) (*blockView, error) { return blocks[h], nil }
	point := satlinePoint{Height: 100, TxIndex: 1, TxID: parent, Vout: 0, BlockHash: b0.Hash}
	result, err := parcelRawSpender079(context.Background(), point, 101, 100, 101, &parcelRawScanBudget079{remaining: 2}, selected, read)
	if err != nil || !result.Found || result.SpendingTxID != child || result.Height != 101 {
		t.Fatalf("positive: %+v %v", result, err)
	}
	_, err = parcelRawSpender079(context.Background(), point, 101, 100, 101, &parcelRawScanBudget079{remaining: 1}, selected, read)
	if err == nil || !strings.Contains(err.Error(), "budget exhausted") {
		t.Fatalf("budget: %v", err)
	}
	_, err = parcelRawSpender079(context.Background(), point, 101, 101, 101, &parcelRawScanBudget079{remaining: 2}, selected, read)
	if err == nil {
		t.Fatal("unapproved range was scanned")
	}
	missing := func(_ context.Context, h int64, _ string) (*blockView, error) {
		if h == 101 {
			return nil, nil
		}
		return blocks[h], nil
	}
	_, err = parcelRawSpender079(context.Background(), point, 101, 100, 101, &parcelRawScanBudget079{remaining: 2}, selected, missing)
	if err == nil {
		t.Fatal("missing retained block accepted")
	}
	b1.Transactions[1].Inputs[0].PrevTxID = testID(404)
	result, err = parcelRawSpender079(context.Background(), point, 101, 100, 101, &parcelRawScanBudget079{remaining: 2}, selected, read)
	if err != nil || result.ConfirmedState != "unknown" || result.Found {
		t.Fatalf("absence: %+v %v", result, err)
	}
	var observed []int64
	result, err = parcelRawSpenderObserved079(context.Background(), point, 101, 100, 101, &parcelRawScanBudget079{remaining: 2}, selected, read, testParcelObserver079{func(b blockView) error { observed = append(observed, b.Height); return nil }})
	if err != nil || result.ConfirmedState != "unspent_at_snapshot" || result.Snapshot == nil || result.Snapshot.Height != 101 || len(observed) != 2 {
		t.Fatalf("complete negative: %+v %v", result, err)
	}
	result, err = parcelRawSpenderObserved079(context.Background(), point, 101, 100, 101, &parcelRawScanBudget079{remaining: 2}, selected, read, testParcelObserver079{func(blockView) error { return errParcelReplayBudget079 }})
	if err != nil || result.ConfirmedState != "unknown" {
		t.Fatalf("oversized negative: %+v %v", result, err)
	}
	valueTx, err := parcelRawTransaction079(context.Background(), parent, 101, 100, 101, &parcelRawScanBudget079{remaining: 2}, selected, read)
	if err != nil || valueTx.Height != 100 || valueTx.Tx.TxID != parent {
		t.Fatalf("transaction: %+v %v", valueTx, err)
	}
	_, err = parcelRawTransaction079(context.Background(), parent, 101, 100, 101, &parcelRawScanBudget079{remaining: 1}, selected, read)
	if err == nil || !strings.Contains(err.Error(), "budget exhausted") {
		t.Fatalf("transaction budget: %v", err)
	}
}

func TestParcelRawBudgetSurvivesRestart079(t *testing.T) {
	dir := t.TempDir()
	budget, err := openParcelRawBudget079(dir, "run-test", 70, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = budget.take(); err != nil {
		t.Fatal(err)
	}
	// A crash after the first reservation forfeits the other 63 reads.
	reopened, err := openParcelRawBudget079(dir, "run-test", 70, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if err = reopened.take(); err != nil {
			t.Fatal(err)
		}
	}
	if err = reopened.take(); err == nil {
		t.Fatal("restart granted extra work")
	}
	if _, err = openParcelRawBudget079(dir, "missing", 70, false); err == nil {
		t.Fatal("resume recreated missing allowance")
	}
	if _, err = openParcelRawBudget079(dir, "run-test", 71, false); err == nil {
		t.Fatal("changed allowance accepted")
	}
}

func TestParcelRawRouteRequiresCompleteInventory079(t *testing.T) {
	timeline := indexTimelineView{TipKnown: true, TipHeight: 10, Tracks: []indexTimelineTrack{{ID: "blocks", RetainedCoverage: &rawCoverageView{Complete: true, Ranges: []heightInterval{{0, 3}, {5, 10}}}}}}
	gaps, err := parcelRawRouteGaps079(timeline, 8)
	if err != nil || len(gaps) != 1 || gaps[0] != (heightInterval{4, 4}) {
		t.Fatalf("%+v %v", gaps, err)
	}
	timeline.Tracks[0].RetainedCoverage.Complete = false
	if _, err := parcelRawRouteGaps079(timeline, 8); err == nil {
		t.Fatal("partial inventory admitted")
	}
	timeline.Tracks[0].RetainedCoverage.Complete = true
	if _, err := parcelRawRouteGaps079(timeline, 11); err == nil {
		t.Fatal("target beyond selected tip admitted")
	}
}

func TestParcelRawScannerResumesAfterBudget079(t *testing.T) {
	dir := t.TempDir()
	id := testID(500)
	blocks := map[int64]*blockView{}
	for h := int64(0); h < 3; h++ {
		b := testBlock(h, testCoinbase(testID(uint64(510+h)), 50))
		b.Verification = verificationView{VerifierVersion: blockVerifierVersion, HeaderHash: true, ProofOfWork: true, MerkleRoot: true, TransactionsParsed: true}
		blocks[h] = &b
	}
	blocks[0].Transactions = append(blocks[0].Transactions, testTx(id, nil, 1))
	selected := func(h int64) (string, error) { return blocks[h].Hash, nil }
	read := func(_ context.Context, h int64, _ string) (*blockView, error) { return blocks[h], nil }
	query := parcelRawCursorQuery079{Kind: "transaction", TxID: id, From: 0, To: 2}
	first, err := openParcelRawCursor079(dir, query, selected)
	if err != nil {
		t.Fatal(err)
	}
	_, err = parcelRawTransaction079(context.Background(), id, 2, 0, 2, &parcelRawScanBudget079{remaining: 1}, selected, read, first)
	if !errors.Is(err, errParcelRawBudgetExhausted079) {
		t.Fatalf("expected budget stop: %v", err)
	}
	second, err := openParcelRawCursor079(dir, query, selected)
	if err != nil {
		t.Fatal(err)
	}
	if second.Next() != 1 {
		t.Fatalf("cursor did not persist: %d", second.Next())
	}
	found, err := parcelRawTransaction079(context.Background(), id, 2, 0, 2, &parcelRawScanBudget079{remaining: 2}, selected, read, second)
	if err != nil || found.Height != 0 || found.Tx.TxID != id {
		t.Fatalf("resume: %+v %v", found, err)
	}
}
