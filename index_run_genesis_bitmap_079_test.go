package main

import (
	"context"
	"testing"
)

func TestIndexRunBitmapGenesisInReusesSavedOrigin079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &b0)
	b1 := bitmapDevelopmentFixtureBlock(b0.Height+1, b0.Hash)
	installRunProtocolHeader(t, a, &b1)
	district := bitmapTestConsumer(t, a.dataDir, bitmapStandardDistrictID)
	parcel := bitmapTestConsumer(t, a.dataDir, bitmapReplayParcelID)
	if err := district.Apply(ctx, b0, nil); err != nil {
		t.Fatal(err)
	}
	if err := parcel.Apply(ctx, b0, nil); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	req := indexRunRequest{In: &zero, Consumers: []indexRunSelection{
		{Index: bitmapStandardDistrictID, Armed: true},
		{Index: "blocks", Armed: true},
		{Index: bitmapReplayParcelID, Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "retained-raw", ParcelRawScanBudget: 100}},
	}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{bitmapStandardDistrictID, bitmapReplayParcelID} {
		var found bool
		for _, p := range plan.Consumers {
			if p.Index != id {
				continue
			}
			found = true
			if p.From != bitmapDevelopmentStart || p.Height != b0.Height || p.SavedCoverageState != "selected_chain" || p.State == "blocked" || p.State == "needs_prerequisite" || p.State == "rebuild_required" || p.NextRequiredHeight == nil || *p.NextRequiredHeight != b1.Height {
				t.Fatalf("%s failed to reuse its origin checkpoint with In=0: %+v", id, p)
			}
		}
		if !found {
			t.Fatalf("missing %s", id)
		}
	}
	if !plan.Ready {
		t.Fatalf("valid paired continuation should be ready: %+v", plan)
	}
}

// A deliberately short legacy Parcel segment is not made continuous just by
// selecting In=0; the entire reviewed plan must be blocked.
func TestIndexRunBlockedParcelPreventsOtherProducersStarting079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &b0)
	b1 := bitmapDevelopmentFixtureBlock(b0.Height+1, b0.Hash)
	installRunProtocolHeader(t, a, &b1)
	district := bitmapTestConsumer(t, a.dataDir, bitmapStandardDistrictID)
	if err := district.Apply(ctx, b0, nil); err != nil {
		t.Fatal(err)
	}
	if err := district.Apply(ctx, b1, nil); err != nil {
		t.Fatal(err)
	}
	parcel := bitmapTestConsumer(t, a.dataDir, bitmapReplayParcelID)
	parcel.instance.StartHeight = b1.Height
	if err := parcel.Apply(ctx, b1, nil); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	req := indexRunRequest{In: &zero, Consumers: []indexRunSelection{
		{Index: bitmapStandardDistrictID, Armed: true}, {Index: "blocks", Armed: true},
		{Index: bitmapReplayParcelID, Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "retained-raw", ParcelRawScanBudget: 100}},
	}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil {
		// A damaged immutable checkpoint is rejected even before a plan exists.
		return
	}
	var blocked bool
	for _, p := range plan.Consumers {
		if p.Index == bitmapReplayParcelID {
			blocked = p.State == "blocked" || p.State == "needs_prerequisite" || p.State == "rebuild_required"
		}
	}
	if !blocked || plan.Ready {
		t.Fatalf("short Parcel segment allowed other producers to start: %+v", plan)
	}
}

func TestCurrentAddressPlanDescribesUTXOCatchupAndSelectedChain079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	zero, one := int64(0), int64(1)
	build := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: "utxo-state", Armed: true}}}
	plan, err := a.planIndexRun(ctx, build)
	if err != nil || !plan.Ready {
		t.Fatalf("genesis UTXO plan: %+v %v", plan, err)
	}
	if _, err = a.startIndexRun(ctx, build, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a); done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: "utxo-state", Armed: true, Live: true}, {Index: "address-state", Armed: true}}}
	plan, err = a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var current indexRunProgress
	for _, p := range plan.Consumers {
		if p.Index == "address-state" {
			current = p
		}
	}
	if current.SourceChainState != "selected_chain" || current.SourceCaughtUp || current.State != "waiting_for_consumer" {
		t.Fatalf("behind source not reported as building: %+v", current)
	}
	build.Out = &one
	plan, err = a.planIndexRun(ctx, build)
	if err != nil || !plan.Ready {
		t.Fatalf("catch-up UTXO plan: %+v %v", plan, err)
	}
	if _, err = a.startIndexRun(ctx, build, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a); done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
	plan, err = a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plan.Consumers {
		if p.Index == "address-state" {
			current = p
		}
	}
	if current.SourceChainState != "selected_chain" || !current.SourceCaughtUp {
		t.Fatalf("caught-up source not recognized: %+v", current)
	}
	changed := bitmapDevelopmentFixtureBlock(1, "")
	installRunProtocolHeader(t, a, &changed)
	plan, err = a.planIndexRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plan.Consumers {
		if p.Index == "address-state" {
			current = p
		}
	}
	if current.SourceChainState == "selected_chain" || current.SourceCaughtUp || current.State != "needs_prerequisite" || plan.Ready {
		t.Fatalf("incompatible source was treated as ready: %+v plan=%+v", current, plan)
	}
}

func TestIndexRunHardParcelBlockStopsPlanButWaitingDoesNot079(t *testing.T) {
	for _, state := range []string{"blocked", "needs_prerequisite", "rebuild_required"} {
		q := indexRunProgress{Index: bitmapReplayParcelID, State: state}
		if !indexRunConsumerBlocksPlan079(&q) || q.AdmissionState != "blocked" {
			t.Fatalf("%s did not stop admission: %+v", state, q)
		}
	}
	for _, state := range []string{"ready", "waiting_for_consumer", "complete"} {
		q := indexRunProgress{Index: bitmapReplayParcelID, State: state}
		if indexRunConsumerBlocksPlan079(&q) {
			t.Fatalf("valid configured producer was blocked: %+v", q)
		}
	}
}
