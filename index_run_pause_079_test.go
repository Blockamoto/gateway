package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestIndexRunPauseDuringSatPublicationRetainsCheckpoint079(t *testing.T) {
	store, blocks := sat077Fixture(t, false)
	before := *store.checkpoint
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.locatorContext = ctx
	store.satCommitHook = func(stage string) error {
		if stage == "before_head" {
			cancel()
		}
		return nil
	}
	block := utxoFixtureBlock(3, blocks[2].Hash, utxoFixtureCoinbase(79900, subsidyAtHeight(3)))
	err := appendSatSelectedBlock077(ctx, store, block, 0, "pruned", nil, func(int64) (string, error) { return block.Hash, nil })
	progress := indexRunProgress{State: "failed", Reason: "context canceled", Height: before.Height, Commitment: before.Commitment}
	if !errors.Is(err, context.Canceled) || !indexRunMarkInterrupted(ctx, err, &progress) || progress.State != "paused" || progress.Reason == "context canceled" || progress.Commitment != before.Commitment || progress.Height != before.Height {
		t.Fatal("intentional cancellation was not classified as paused", err)
	}
	if indexRunMarkInterrupted(ctx, fmt.Errorf("independent storage failure"), &progress) {
		t.Fatal("pause masked an independent consumer failure")
	}
	if indexRunMarkInterrupted(context.Background(), context.Canceled, &progress) {
		t.Fatal("unrelated cancellation was classified as a run pause")
	}
	terminal := fmt.Errorf("%w: %w", ErrSatRebuildRequired, context.Canceled)
	if indexRunMarkInterrupted(ctx, terminal, &progress) {
		t.Fatal("pause masked a terminal rebuild requirement")
	}
	reopened, err := indexStoreHead(store.profileRoot, "sat-state")
	if err != nil || reopened.checkpoint == nil || reopened.checkpoint.Commitment != before.Commitment || reopened.checkpoint.BlockHash != before.BlockHash || reopened.checkpoint.Height != before.Height {
		t.Fatal("interrupted publication changed the committed Sat checkpoint", err)
	}
}

func TestIndexRunPauseWhileSourceBlockedKeepsCommittedSat079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: "sat-state", Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a); done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
	before, err := indexStoreHead(a.dataDir, "sat-state")
	if err != nil || before.checkpoint == nil || before.checkpoint.Height != 0 {
		t.Fatal("genesis Sat checkpoint missing", err)
	}
	commitment := before.checkpoint.Commitment
	entered := make(chan struct{}, 1)
	a.indexRunSourceBlock = func(work context.Context, _ blockTarget, _ string) (blockView, error) {
		entered <- struct{}{}
		<-work.Done()
		return blockView{}, work.Err()
	}
	req.Out = &one
	plan, err = a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	started, err := a.startIndexRun(ctx, req, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("source fetch did not start")
	}
	if _, err = a.controlIndexRun(ctx, started.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	paused := waitRun(t, a)
	if paused.Run.State != "paused" || len(paused.Run.Consumers) != 1 || paused.Run.Consumers[0].State != "paused" || paused.Run.Consumers[0].Commitment != commitment || paused.Run.Consumers[0].Height != 0 {
		t.Fatalf("pause lost progress or misclassified the consumer: %+v", paused.Run)
	}
	after, err := indexStoreHead(a.dataDir, "sat-state")
	if err != nil || after.checkpoint == nil || after.checkpoint.Commitment != commitment {
		t.Fatal("pause changed the durable Sat checkpoint", err)
	}
}
