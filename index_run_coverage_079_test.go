package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestIndexRunReviewsRawReorgBeforeAdmission079(t *testing.T) {
	ctx := context.Background()
	a := prepareLiveTestApp063(t)
	genesis := buildGenesisBlockIndex066(t, a)
	_, selectedHash := installBlockOneHeader063(t, a)
	store, err := openIndexStore(a.dataDir, "blocks")
	if err != nil {
		t.Fatal(err)
	}
	stale := inscriptionTestBlock()
	ids := make([]string, len(stale.Transactions))
	for i, tx := range stale.Transactions {
		ids[i] = tx.TxID
	}
	if err := store.commitPreparedIndexBatch(indexBatch{PreviousBlockHash: genesisHashDisplay, Bitcoin: &bitcoinIndexBlock{VerifierVersion: blockVerifierVersion, TxIDs: ids, Transactions: stale.Transactions}}, 1, strings.Repeat("f", 64), 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	staleCommit := store.checkpoint.Commitment
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: "blocks", Armed: true, indexRunOptions: indexRunOptions{Mode: "lean"}}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready || len(plan.Consumers) != 1 {
		t.Fatal(plan, err)
	}
	q := plan.Consumers[0]
	if q.SavedCoverageState != "changed" || q.ReusedBlocks != 0 || q.Commitment != staleCommit || q.NextRequiredHeight == nil || *q.NextRequiredHeight != 1 || len(q.MissingRanges) != 1 || q.MissingRanges[0] != (heightInterval{1, 1}) {
		t.Fatalf("review must expose reconciliation and never reuse the stale head: %+v", q)
	}
	before, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || before.checkpoint.Commitment != staleCommit {
		t.Fatal("planning changed Raw coverage", err)
	}
	if _, err := a.startIndexRun(ctx, req, strings.Repeat("0", 64)); err == nil {
		t.Fatal("unreviewed reconciliation plan was admitted")
	}
	after, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || after.checkpoint.Commitment != staleCommit || genesis.BlockHash != genesisHashDisplay || selectedHash == strings.Repeat("f", 64) {
		t.Fatal("rejected plan changed Raw coverage", err)
	}
	if _, err := a.reviewRawReorg079(ctx, after.checkpoint, func(int64) (string, error) { return "", fmt.Errorf("header unavailable") }); err == nil {
		t.Fatal("reconciliation review accepted unavailable ancestor evidence")
	}
	drifted := q
	two := int64(2)
	drifted.NextRequiredHeight = &two
	worker := &indexRunConsumer{selection: req.Consumers[0], progress: drifted, store: after}
	if err := a.reconcileRunConsumerUncached(ctx, worker, a.currentChainAuthority()); err == nil {
		t.Fatal("worker reconciled a different boundary from the reviewed plan")
	}
	after, err = indexStoreHead(a.dataDir, "blocks")
	if err != nil || after.checkpoint.Commitment != staleCommit {
		t.Fatal("boundary drift changed Raw coverage", err)
	}
	for _, commitment := range []string{"", strings.Repeat("e", 64)} {
		changed := q
		changed.Commitment = commitment
		worker = &indexRunConsumer{selection: req.Consumers[0], progress: changed, store: after}
		if err := a.reconcileRunConsumerUncached(ctx, worker, a.currentChainAuthority()); err == nil {
			t.Fatal("worker reconciled without the exact reviewed Raw commitment", commitment)
		}
		unchanged, e := indexStoreHead(a.dataDir, "blocks")
		if e != nil || unchanged.checkpoint.Commitment != staleCommit {
			t.Fatal("unreviewed commitment changed Raw coverage", e)
		}
	}
	// A head selected during review can become stale without its commitment
	// changing. Initial admission must not reconcile that unreviewed reorg.
	selectedAtReview := q
	selectedAtReview.SavedCoverageState = "selected_chain"
	worker = &indexRunConsumer{selection: req.Consumers[0], progress: selectedAtReview, store: after}
	if err := a.reconcileRunConsumerUncached(ctx, worker, a.currentChainAuthority()); err == nil {
		t.Fatal("worker silently reconciled a chain change after admission")
	}
	unchanged, e := indexStoreHead(a.dataDir, "blocks")
	if e != nil || unchanged.checkpoint.Commitment != staleCommit {
		t.Fatal("post-review chain change rewound Raw coverage", e)
	}
	// An established active run can reconcile a later reorg only inside its
	// admitted start, and must not mutate the head for a deeper rewind.
	selectedAtReview.From = 2
	worker = &indexRunConsumer{selection: req.Consumers[0], progress: selectedAtReview, store: after, reconciled: true}
	if err := a.reconcileRunConsumerUncached(ctx, worker, a.currentChainAuthority()); err == nil {
		t.Fatal("active run rewound before its admitted start")
	}
	unchanged, e = indexStoreHead(a.dataDir, "blocks")
	if e != nil || unchanged.checkpoint.Commitment != staleCommit {
		t.Fatal("out-of-range active rewind changed Raw coverage", e)
	}
	// A rewind earlier than the marked In may not become hidden work.
	later := indexRunProgress{Index: "blocks", State: "ready", From: 2}
	if err := a.refineRunCoverage079(ctx, &later, req.Consumers[0], after.checkpoint, 2); err != nil || later.State != "needs_prerequisite" || !later.RequiresLeadIn || later.ReusedBlocks != 0 || later.NextRequiredHeight != nil {
		t.Fatalf("earlier reconciliation escaped the reviewed boundary: %+v %v", later, err)
	}
	selectedAtReview.From = 0
	worker = &indexRunConsumer{selection: req.Consumers[0], progress: selectedAtReview, store: after, reconciled: true}
	if err := a.reconcileRunConsumerUncached(ctx, worker, a.currentChainAuthority()); err != nil {
		t.Fatalf("in-range active reconciliation failed: %v", err)
	}
	repaired, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || repaired.checkpoint == nil || repaired.checkpoint.Commitment != genesis.Commitment {
		t.Fatal("in-range active reconciliation did not retain its selected ancestor", err)
	}
}

func TestIndexRunResumeReconcilesRawAfterReorg079(t *testing.T) {
	for _, externallyRepaired := range []bool{false, true} {
		t.Run(fmt.Sprintf("externally_repaired_%t", externallyRepaired), func(t *testing.T) {
			testIndexRunResumeReconcilesRawAfterReorg079(t, externallyRepaired)
		})
	}
}

func testIndexRunResumeReconcilesRawAfterReorg079(t *testing.T, externallyRepaired bool) {
	ctx := context.Background()
	a := runTestApp(t)
	genesis := buildGenesisBlockIndex066(t, a)
	selected, err := a.fetchAndDecodeContext(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) {
		return blockView{}, blockUnavailableError{fmt.Errorf("fixture source paused")}
	}
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: "blocks", Armed: true, indexRunOptions: indexRunOptions{Mode: "lean"}}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	started, err := a.startIndexRun(ctx, req, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool { return d.Run != nil && d.Run.State == "waiting" })
	if _, err = a.controlIndexRun(ctx, started.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	if paused := waitRun(t, a); paused.Run.State != "paused" {
		t.Fatal(paused.Run)
	}
	store, err := openIndexStore(a.dataDir, "blocks")
	if err != nil {
		t.Fatal(err)
	}
	stale := inscriptionTestBlock()
	ids := make([]string, len(stale.Transactions))
	for i, tx := range stale.Transactions {
		ids[i] = tx.TxID
	}
	if err = store.commitPreparedIndexBatch(indexBatch{PreviousBlockHash: genesis.BlockHash, Bitcoin: &bitcoinIndexBlock{VerifierVersion: blockVerifierVersion, TxIDs: ids, Transactions: stale.Transactions}}, 1, strings.Repeat("f", 64), 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	if externallyRepaired {
		// Model a previously persisted changed-plan state after another
		// explicit repair brought its saved head back to the selected chain.
		a.indexRunMu.Lock()
		progress := &a.indexRunState.Run.Consumers[0]
		progress.SavedCoverageState = "changed"
		progress.Commitment = store.checkpoint.Commitment
		err = a.persistIndexRunLocked()
		a.indexRunMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if err = store.reconcile(a.addressSelectedHashes(ctx, a.currentChainAuthority())); err != nil {
			t.Fatal(err)
		}
	}
	acquisition := make(chan error, 10)
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		current, e := indexStoreHead(a.dataDir, "blocks")
		if e != nil || current.checkpoint == nil || current.checkpoint.Commitment != genesis.Commitment || target.HashDisplay != selected.Hash {
			acquisition <- fmt.Errorf("stale Raw coverage was visible when source work began: %v", e)
			return blockView{}, fmt.Errorf("unreconciled source")
		}
		acquisition <- nil
		return selected, nil
	}
	if _, err = a.controlIndexRun(ctx, started.Run.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
	if err = <-acquisition; err != nil {
		t.Fatal(err)
	}
	result, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || result.checkpoint == nil || result.checkpoint.BlockHash != selected.Hash {
		t.Fatal("resume did not extend the reconciled selected prefix", err)
	}
}

func TestIndexRunSavedAnchorMustBeSelected079(t *testing.T) {
	head := &indexCheckpoint{Height: 4, BlockHash: testID(1)}
	if got := indexRunSavedAnchorState079(nil, nil); got != "none" {
		t.Fatal(got)
	}
	if got := indexRunSavedAnchorState079(head, func(int64) (string, error) { return "", fmt.Errorf("missing header") }); got != "unavailable" {
		t.Fatal(got)
	}
	if got := indexRunSavedAnchorState079(head, func(int64) (string, error) { return testID(2), nil }); got != "changed" {
		t.Fatal(got)
	}
	if got := indexRunSavedAnchorState079(head, func(int64) (string, error) { return testID(1), nil }); got != "selected_chain" {
		t.Fatal(got)
	}
}

func TestIndexRunPlanReportsOriginSeparateFromSelectedIn079(t *testing.T) {
	a := runTestApp(t)
	in := int64(firstMainnetInscriptionHeight + 20)
	plan, err := a.planIndexRun(context.Background(), indexRunRequest{In: &in, Consumers: []indexRunSelection{{Index: "inscriptions", Armed: true}}})
	if err != nil || len(plan.Consumers) != 1 {
		t.Fatal(plan, err)
	}
	q := plan.Consumers[0]
	if q.OriginHeight == nil || *q.OriginHeight != firstMainnetInscriptionHeight || q.From != in || q.RangeMode != "contiguous_append" {
		t.Fatalf("track origin was conflated with selected range: %+v", q)
	}
}

func TestIndexRunHistoricalAddressesReviewActualHoles079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	block, err := a.fetchAndDecodeContext(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	history, err := openAddressHistoryStoreContext(ctx, a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	selected := a.addressSelectedHashes(ctx, a.currentChainAuthority())
	if err = history.CommitBlockContext(ctx, block, selected); err != nil {
		t.Fatal(err)
	}
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: "address-history", Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || len(plan.Consumers) != 1 {
		t.Fatal(plan, err)
	}
	q := plan.Consumers[0]
	if q.State == "complete" || q.SavedCoverageState != "selected_chain" || q.NextRequiredHeight == nil || *q.NextRequiredHeight != 0 || len(q.MissingRanges) != 1 || q.MissingRanges[0] != (heightInterval{0, 0}) || q.ReusedBlocks != 1 {
		t.Fatalf("sparse hole hidden by maximum height: %+v", q)
	}
	genesis, err := a.fetchAndDecodeContext(ctx, "0")
	if err != nil {
		t.Fatal(err)
	}
	if err = history.CommitBlockContext(ctx, genesis, selected); err != nil {
		t.Fatal(err)
	}
	plan, err = a.planIndexRun(ctx, req)
	if err != nil || len(plan.Consumers) != 1 {
		t.Fatal(plan, err)
	}
	q = plan.Consumers[0]
	if q.State != "complete" || len(q.MissingRanges) != 0 || q.ReusedBlocks != 2 {
		t.Fatalf("complete sparse coverage not recognized: %+v", q)
	}
	reads := 0
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) {
		reads++
		return blockView{}, fmt.Errorf("unexpected source acquisition")
	}
	if _, err = a.startIndexRun(ctx, req, ""); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" || reads != 0 {
		t.Fatalf("covered range should perform no missing-work fetch: reads=%d run=%+v", reads, done.Run)
	}
}

func TestIndexRunSparseCoverageDoesNotTrustOnlyHighestAnchor079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	history, err := openAddressHistoryStoreContext(ctx, a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	selected := a.addressSelectedHashes(ctx, a.currentChainAuthority())
	for _, height := range []string{"0", "1"} {
		block, e := a.fetchAndDecodeContext(ctx, height)
		if e != nil {
			t.Fatal(e)
		}
		if e = history.CommitBlockContext(ctx, block, selected); e != nil {
			t.Fatal(e)
		}
	}
	// Model an older mixed-branch sparse revision whose highest block still
	// matches the selected chain. The older record is not a valid reuse proof.
	blocks := history.tree("blocks", history.head.BlocksRoot, ctx)
	var old addressHistoryBlock
	if have, e := blocks.get(addressHistoryHeightKey(0), &old); e != nil || !have {
		t.Fatal(have, e)
	}
	old.BlockHash = testID(99999)
	if err = blocks.put(addressHistoryHeightKey(0), old); err != nil {
		t.Fatal(err)
	}
	h := history.head
	if h.BlocksRoot, err = blocks.flush(); err != nil {
		t.Fatal(err)
	}
	if err = history.publish(ctx, h); err != nil {
		t.Fatal(err)
	}
	zero, one := int64(0), int64(1)
	plan, err := a.planIndexRun(ctx, indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: "address-history", Armed: true}}})
	if err != nil || len(plan.Consumers) != 1 {
		t.Fatal(plan, err)
	}
	q := plan.Consumers[0]
	if plan.Ready || q.State != "needs_prerequisite" || q.SavedCoverageState != "changed" || q.ReusedBlocks != 0 || len(q.MissingRanges) != 0 || q.NextRequiredHeight != nil {
		t.Fatalf("mixed-chain sparse revision claimed compatible work: %+v", q)
	}
	copy := *history
	copy.head.P2PKCoverage = []heightInterval{{0, indexRunSparseReviewLimit079}}
	if state, err := indexRunReviewSparseAddressCoverage079(ctx, &copy, 0, indexRunSparseReviewLimit079, selected); err != nil || state != "review_limit" {
		t.Fatalf("unbounded sparse metadata review: %q %v", state, err)
	}
}

func TestIndexRunSweepPicksLowestReadyIndependentOfArmOrder079(t *testing.T) {
	next := map[int]int64{0: 792435, 1: 0, 2: 767430, 3: 0}
	for _, order := range [][]int{{0, 1, 2, 3}, {2, 3, 0, 1}, {3, 2, 1, 0}} {
		if chosen := lowestReadyIndexRunFrontier079(order, next); next[chosen] != 0 {
			t.Fatalf("picked %d from %v", next[chosen], order)
		}
	}
	// A temporarily unavailable earlier source is omitted from ready members;
	// the remaining independently ready producer can make progress.
	if chosen := lowestReadyIndexRunFrontier079([]int{0, 2}, next); next[chosen] != 767430 {
		t.Fatal(chosen, next[chosen])
	}
}

func TestIndexRunMissingEarlierBodyDoesNotBlockIndependentProducer079(t *testing.T) {
	a := runTestApp(t)
	block := bitmapDevelopmentFixtureBlock(firstMainnetInscriptionHeight, "", "separate frontier")
	sharedProtocolHeader(t, a, &block)
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height == block.Height && target.HashDisplay == block.Hash {
			return block, nil
		}
		return blockView{}, blockUnavailableError{fmt.Errorf("fixture source unavailable at %d", target.Height)}
	}
	end := block.Height
	req := indexRunRequest{Out: &end, Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}, {Index: "inscriptions", Armed: true}}}
	started, err := a.startIndexRun(context.Background(), req, "")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		store, err := indexStoreHead(a.dataDir, "inscriptions")
		if err == nil && store.checkpoint != nil && store.checkpoint.Height == block.Height {
			if _, err = a.controlIndexRun(context.Background(), started.Run.ID, "cancel"); err != nil {
				t.Fatal(err)
			}
			waitRun(t, a)
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	_, _ = a.controlIndexRun(context.Background(), started.Run.ID, "cancel")
	waitRun(t, a)
	t.Fatal("independent inscription frontier did not progress while genesis source was unavailable")
}
