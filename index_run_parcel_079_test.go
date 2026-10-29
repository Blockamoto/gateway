package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParcelDistrictRetryClassification079(t *testing.T) {
	if !parcelWaitsForDistrict079(fmt.Errorf("%w: Matching completed District evaluation is unavailable.", ErrBitmapEvidenceRequired)) {
		t.Fatal("missing District should wait for a producer")
	}
	if parcelWaitsForDistrict079(fmt.Errorf("%w: Checked parent provenance is unavailable: receipt budget", ErrBitmapEvidenceRequired)) {
		t.Fatal("proof gap must not be retried as a District wait")
	}
	if parcelWaitsForDistrict079(errors.New("District evaluation is required")) {
		t.Fatal("untyped error must not be treated as a dependency wait")
	}
}

func TestParcelScanConsentRoundTrip079(t *testing.T) {
	var req indexRunRequest
	input := `{"consumers":[{"index":"bitmap-parcels-standard-v1-replay","armed":true,"options":{"parcel_evidence_route":"retained-raw","parcel_raw_scan_budget":25}}]}`
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		t.Fatal(err)
	}
	normal, err := normalizeStoredIndexRunRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if c := normal.Consumers[0]; c.ParcelEvidenceRoute != "retained-raw" || c.ParcelRawScanBudget != 25 {
		t.Fatalf("lost explicit consent: %+v", c)
	}
	encoded, err := json.Marshal(normal)
	if err != nil {
		t.Fatal(err)
	}
	var saved indexRunRequest
	if err := json.Unmarshal(encoded, &saved); err != nil || saved.Consumers[0].ParcelRawScanBudget != 25 {
		t.Fatalf("consent not persisted: %s %v", encoded, err)
	}
	// A legacy flattened value survives newer route-only nested options.
	var mixed indexRunRequest
	if err := json.Unmarshal([]byte(`{"consumers":[{"index":"bitmap-parcels-standard-v1-replay","armed":true,"parcel_raw_scan_budget":10000001,"options":{"parcel_evidence_route":"tx-locators"}}]}`), &mixed); err != nil {
		t.Fatal(err)
	}
	mixed, err = normalizeStoredIndexRunRequest(mixed)
	if err != nil || mixed.Consumers[0].ParcelRawScanBudget != 10000001 || mixed.Consumers[0].ParcelEvidenceRoute != "tx-locators" {
		t.Fatalf("lost legacy flattened metadata: %+v %v", mixed, err)
	}
	encodedMixed, err := json.Marshal(mixed)
	if err != nil {
		t.Fatal(err)
	}
	var reopened indexRunRequest
	if err = json.Unmarshal(encodedMixed, &reopened); err != nil || reopened.Consumers[0].ParcelRawScanBudget != 10000001 {
		t.Fatalf("legacy metadata not round-trippable: %s %v", encodedMixed, err)
	}

	// Old grants remain round-trippable but are inert regardless of their value.
	for _, legacy := range []int64{0, 1, 10000001} {
		choice := indexRunSelection{Index: bitmapReplayParcelID, Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "retained-raw", ParcelRawScanBudget: legacy}}
		if _, err := normalizeStoredIndexRunRequest(indexRunRequest{Consumers: []indexRunSelection{choice}}); err != nil {
			t.Fatalf("legacy allowance %d prevented reading selection: %v", legacy, err)
		}
	}
	for _, bad := range []indexRunSelection{
		{Index: "tx-locator", Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "retained-raw", ParcelRawScanBudget: 1}},
		{Index: bitmapReplayParcelID, Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "unknown", ParcelRawScanBudget: 1}},
	} {
		if _, err := normalizeStoredIndexRunRequest(indexRunRequest{Consumers: []indexRunSelection{bad}}); err == nil {
			t.Fatalf("invalid scan consent accepted: %+v", bad)
		}
	}
}

func TestReplayParcelWriterDoesNotBlockDistrict079(t *testing.T) {
	parcel := &bitmapRunConsumer{id: bitmapReplayParcelID}
	district := &bitmapRunConsumer{id: bitmapStandardDistrictID}
	release := parcel.lockWriter()
	defer release()
	entered := make(chan struct{})
	go func() { unlock := district.lockWriter(); close(entered); unlock() }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("District waited for a Parcel proof scan")
	}
	if bitmapSnapshotMu.TryLock() {
		bitmapSnapshotMu.Unlock()
		t.Fatal("Parcel snapshot lease failed to fence deletion")
	}
}

func TestReplayParcelLaneAllowsIndependentWork079(t *testing.T) {
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan parcelLaneResult, 1)
	c := &indexRunConsumer{selection: indexRunSelection{Index: bitmapReplayParcelID}}
	launchParcelLane(finished, c, 42, func() error { close(entered); <-release; return nil })
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("Parcel lane did not start")
	}
	select {
	case <-finished:
		t.Fatal("Parcel completed before its evidence was ready")
	default:
	}
	// Other consumers can do work while the Parcel operation is still blocked.
	district := &bitmapRunConsumer{id: bitmapStandardDistrictID}
	unlock := district.lockWriter()
	unlock()
	close(release)
	select {
	case result := <-finished:
		if result.err != nil || result.height != 42 || result.consumer != c {
			t.Fatalf("bad result: %+v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Parcel lane did not join")
	}
}

func TestReplayParcelLaneJoinsOnCancellation079(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	done := make(chan parcelLaneResult, 1)
	launchParcelLane(done, &indexRunConsumer{}, 4, func() error { close(entered); <-ctx.Done(); return ctx.Err() })
	<-entered
	cancel()
	select {
	case result := <-done:
		if result.err != context.Canceled {
			t.Fatalf("unexpected cancellation result: %v", result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Parcel worker did not join")
	}
}

func TestTXIOPrerequisiteAuditRunsInIndependentParcelLane079(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	c := &indexRunConsumer{parcelPrepare: func(context.Context) error { close(entered); <-release; return nil }}
	done := make(chan parcelLaneResult, 1)
	launchParcelLane(done, c, 42, func() error { return prepareParcelLane079(context.Background(), c) })
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("audit did not start")
	}
	district := &bitmapRunConsumer{id: bitmapStandardDistrictID}
	unlock := district.lockWriter()
	unlock()
	select {
	case <-done:
		t.Fatal("audit unexpectedly finished before release")
	default:
	}
	close(release)
	select {
	case result := <-done:
		if result.err != nil || !c.parcelPrepared {
			t.Fatalf("audit did not finish: %+v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("audit lane did not join")
	}
}

func TestReplayParcelBrowseDoesNotStart079(t *testing.T) {
	if !bitmapBuiltinExecutionAvailable(bitmapReplayParcelID) {
		t.Skip("replay execution remains gated until proof implementation is ready")
	}
	a := runTestApp(t)
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: bitmapReplayParcelID, Armed: true}}}); err != nil {
		t.Fatal(err)
	}
	block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	installRunProtocolHeader(t, a, &block)
	_ = a.ingestArmedResolvedBlock(context.Background(), block)
	if _, err := os.Stat(bitmapDevelopmentDir(a.dataDir, bitmapReplayParcelID)); !os.IsNotExist(err) {
		t.Fatalf("browse unexpectedly created Parcel state: %v", err)
	}
}

func TestReplayParcelLiveRequiresReviewedCoordinatedDependencies079(t *testing.T) {
	if !bitmapBuiltinExecutionAvailable(bitmapReplayParcelID) {
		t.Skip("replay execution remains gated")
	}
	a := runTestApp(t)
	for _, action := range []string{"enable", "start", "resume"} {
		if _, err := a.setIndexLivePolicy(indexLiveRequest{Index: bitmapReplayParcelID, Action: action}); err == nil {
			t.Fatalf("uncoordinated Live action %s unexpectedly accepted", action)
		}
	}
	if _, err := normalizeIndexRunRequest(indexRunRequest{Consumers: []indexRunSelection{{Index: bitmapReplayParcelID, Armed: true, Live: true}}}); err == nil {
		t.Fatal("Live coordinated run without required dependencies unexpectedly accepted")
	}
}

func TestReplayParcelGapDoesNotBlockOtherTrack079(t *testing.T) {
	if !bitmapBuiltinExecutionAvailable(bitmapReplayParcelID) {
		t.Skip("replay execution remains gated until proof implementation is ready")
	}
	a := runTestApp(t)
	block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &block)
	// The selected locator producer has not built any committed coverage.
	a.parcelEvidenceHooks079 = &parcelEvidenceHooks079{Timeline: func() indexTimelineView {
		return indexTimelineView{TipKnown: true, TipHeight: block.Height, Tracks: []indexTimelineTrack{{ID: "tx-locator", Sources: []indexTimelineSource{{ID: "gateway_committed_index", Kind: "committed_records", Complete: true}}}}}
	}}
	zero := int64(0)
	req := indexRunRequest{In: &zero, Consumers: []indexRunSelection{
		{Index: bitmapStandardDistrictID, Armed: true},
		{Index: "tx-locator", Armed: true},
		{Index: "inscriptions", Armed: true},
		{Index: bitmapReplayParcelID, Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "tx-locators"}},
	}}
	plan, err := a.planIndexRun(context.Background(), req)
	if err != nil || !plan.Ready {
		t.Fatalf("configured producers could not run while proof coverage is missing: %+v %v", plan, err)
	}
	var parcel, independent *indexRunProgress
	for i := range plan.Consumers {
		p := &plan.Consumers[i]
		if p.Index == bitmapReplayParcelID {
			parcel = p
		}
		if p.Index == "inscriptions" {
			independent = p
		}
	}
	if parcel == nil || parcel.State == "complete" || !strings.Contains(parcel.Reason, "prefix is unavailable") {
		t.Fatalf("missing proof coverage was not reported: %+v", parcel)
	}
	if independent == nil || independent.State != "ready" || independent.From != 767430 {
		t.Fatalf("independent producer was not ready: %+v", independent)
	}
	if head, err := readBitmapDevelopmentHead(context.Background(), a.dataDir, bitmapReplayParcelID); err != nil || head != nil {
		t.Fatalf("planning a proof gap published Parcel coverage: %+v %v", head, err)
	}
}

func TestParcelLocatorRouteReviewedRunAdmission079(t *testing.T) {
	if !parcelLocatorExecutionAvailable079 {
		t.Skip("locator route remains gated")
	}
	a, raw0 := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = true
	raw1, hash1 := installBlockOneHeader063(t, a)
	genesis, err := parseBlockDetailed(0, genesisHashDisplay, raw0[:80], raw0, "genesis fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	block1, err := parseBlockDetailed(1, hash1, raw1[:80], raw1, "block one fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	s, err := openIndexStore(a.dataDir, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.appendBlock(genesis, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	if err = s.appendBlock(block1, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	req := indexRunRequest{In: &zero, Consumers: []indexRunSelection{{Index: bitmapStandardDistrictID, Armed: true}, {Index: "tx-locator", Armed: true}, {Index: bitmapReplayParcelID, Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "tx-locators", ParcelRawScanBudget: 8}}}}
	plan, err := a.planIndexRun(context.Background(), req)
	if err != nil || !plan.Ready {
		t.Fatalf("locator prerequisite not admitted: %+v %v", plan, err)
	}
	if _, err = a.startIndexRun(context.Background(), req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run == nil || (done.Run.State != "complete" && done.Run.State != "complete_with_errors") {
		t.Fatalf("locator run did not reach a terminal state: %+v", done.Run)
	}
	if done.Run.State == "complete_with_errors" && len(done.Run.SourceFactErrors) == 0 {
		t.Fatalf("terminal error was not recorded: %+v", done.Run)
	}
	p, ok := indexRunProgressFor(done.Run, bitmapReplayParcelID)
	if !ok || p.State != "complete" || p.ParcelDiscoveryHeight != nil {
		t.Fatalf("Parcel admission did not finish without discovery: %+v", p)
	}
}

func TestTXIORouteWorkerBindsBudgetAndAudits079(t *testing.T) {
	if !parcelTXIOExecutionAvailable079 {
		t.Skip("IO Links route remains gated")
	}
	a, raw0 := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = true
	raw1, hash1 := installBlockOneHeader063(t, a)
	genesis, err := parseBlockDetailed(0, genesisHashDisplay, raw0[:80], raw0, "real genesis fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	one, err := parseBlockDetailed(1, hash1, raw1[:80], raw1, "real block-one fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openIndexStore(a.dataDir, "txo-spender")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.appendBlock(genesis, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	if err = store.appendBlock(one, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	a.blockBaseAcquire = func(_ context.Context, target blockTarget, _ appSettings) (blockView, error) {
		switch target.Height {
		case 0:
			return genesis, nil
		case 1:
			return one, nil
		default:
			return blockView{}, fmt.Errorf("unexpected source block")
		}
	}
	choice := indexRunSelection{Index: bitmapReplayParcelID, Armed: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "txio-links", ParcelRawScanBudget: 10}}
	record := &indexRunRecord{ID: "test-txio-admission", InitialTargetHeight: 1, Request: indexRunRequest{Consumers: []indexRunSelection{choice}}}
	if _, err = openParcelRawBudget079(a.dataDir, record.ID, choice.ParcelRawScanBudget, true); err != nil {
		t.Fatal(err)
	}
	consumer, err := a.openRunConsumer(context.Background(), record, indexRunProgress{Index: choice.Index})
	if err != nil || consumer.parcelPrepare == nil || consumer.parcelBudget == nil {
		t.Fatalf("worker route not bound: %+v %v", consumer, err)
	}
	if err = prepareParcelLane079(context.Background(), consumer); err != nil || !consumer.parcelPrepared {
		t.Fatalf("worker audit: %v", err)
	}
}

func TestReplayParcelCohortJoinsDistrictAndTX079(t *testing.T) {
	if !bitmapBuiltinExecutionAvailable(bitmapReplayParcelID) {
		t.Skip("replay execution remains gated until proof implementation is ready")
	}
	a := runTestApp(t)
	_, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}, {Index: bitmapStandardDistrictID, Armed: true}, {Index: bitmapReplayParcelID, Armed: true}}})
	if err != nil {
		t.Fatal(err)
	}
	block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	installRunProtocolHeader(t, a, &block)
	extraction, err := extractInscriptionOccurrences(block)
	if err != nil {
		t.Fatal(err)
	}
	prepared := &indexRunPrepared{block: block, extraction: &extraction}
	txEntered, districtEntered, districtDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	parcelEntered, parcelRelease := make(chan struct{}), make(chan struct{})
	parcelDone := make(chan parcelLaneResult, 1)
	parcelCalled := false
	ops := map[string]indexRunBlockConsumer{
		"tx-locator": func(context.Context) error {
			close(txEntered)
			select {
			case <-districtEntered:
				return nil
			case <-time.After(3 * time.Second):
				return fmt.Errorf("District did not overlap TX")
			}
		},
		bitmapStandardDistrictID: func(context.Context) error {
			close(districtEntered)
			select {
			case <-txEntered:
				close(districtDone)
				return nil
			case <-time.After(3 * time.Second):
				return fmt.Errorf("TX did not overlap District")
			}
		},
		bitmapReplayParcelID: func(context.Context) error {
			select {
			case <-districtDone:
				parcelCalled = true
				launchParcelLane(parcelDone, &indexRunConsumer{}, block.Height, func() error { close(parcelEntered); <-parcelRelease; return nil })
				return nil
			default:
				return fmt.Errorf("Parcel preceded District")
			}
		},
	}
	ctx := context.WithValue(context.Background(), indexRunPreparedKey{}, prepared)
	ctx = context.WithValue(ctx, indexRunBlockConsumersKey{}, ops)
	for _, v := range a.ingestArmedResolvedBlock(ctx, block) {
		if v.State == "unavailable" || v.State == "error" {
			t.Fatalf("dispatch: %+v", v)
		}
	}
	if !parcelCalled {
		t.Fatal("replay Parcel consumer was not dispatched")
	}
	select {
	case <-parcelEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("Parcel lane did not start")
	}
	select {
	case <-parcelDone:
		t.Fatal("Parcel unexpectedly finished before proof release")
	default:
	}
	close(parcelRelease)
	select {
	case result := <-parcelDone:
		if result.err != nil {
			t.Fatal(result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Parcel lane did not join")
	}
}
