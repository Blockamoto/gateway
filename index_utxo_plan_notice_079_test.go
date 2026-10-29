package main

import (
	"context"
	"testing"
)

func TestUTXOPlanReportsPrefixAndSavedNextHeight079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	one, two := int64(1), int64(2)
	request := func(in *int64) indexRunRequest {
		return indexRunRequest{In: in, Consumers: []indexRunSelection{{Index: "utxo-state", Armed: true}}}
	}
	empty, err := a.planIndexRun(ctx, request(&one))
	if err != nil || len(empty.Consumers) != 1 {
		t.Fatal(empty, err)
	}
	consumer := empty.Consumers[0]
	if consumer.State != "needs_prerequisite" || !consumer.RequiresLeadIn || consumer.NextRequiredHeight == nil || *consumer.NextRequiredHeight != 0 {
		t.Fatalf("empty state did not require genesis: %+v", consumer)
	}
	genesis, err := a.fetchAndDecodeContext(ctx, "0")
	if err != nil {
		t.Fatal(err)
	}
	store, err := openUTXOStoreContext(ctx, a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	utxoApply(t, store, genesis)
	if state := a.utxoSelectedChainState(ctx, store.Head(), a.currentChainAuthority()); state != "selected_chain" {
		t.Fatalf("fixture checkpoint not selected: %s", state)
	}
	continued, err := a.planIndexRun(ctx, request(&one))
	if err != nil || len(continued.Consumers) != 1 {
		t.Fatal(continued, err)
	}
	consumer = continued.Consumers[0]
	if consumer.RequiresLeadIn || consumer.State != "ready" || consumer.NextRequiredHeight == nil || *consumer.NextRequiredHeight != 1 || consumer.Height != 0 {
		t.Fatalf("selected checkpoint did not expose its next block: %+v", consumer)
	}
	gap, err := a.planIndexRun(ctx, request(&two))
	if err != nil || len(gap.Consumers) != 1 {
		t.Fatal(gap, err)
	}
	consumer = gap.Consumers[0]
	if consumer.State != "needs_prerequisite" || !consumer.RequiresLeadIn || consumer.NextRequiredHeight == nil || *consumer.NextRequiredHeight != 1 {
		t.Fatalf("gap after saved checkpoint was not reported: %+v", consumer)
	}
}
