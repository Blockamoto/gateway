package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type runAdmissionReconcileProbe struct{ calls int }

func (p *runAdmissionReconcileProbe) NextHeight() int64 { return 0 }
func (p *runAdmissionReconcileProbe) Reconcile(context.Context, func(int64) (string, error)) error {
	p.calls++
	return nil
}
func (p *runAdmissionReconcileProbe) Apply(context.Context, blockView, *inscriptionBlockResult) error {
	return nil
}
func (p *runAdmissionReconcileProbe) Checkpoint() *indexCheckpoint { return nil }
func (p *runAdmissionReconcileProbe) Close()                       {}

func TestTimelineRunReconcileCacheIsAdapterAndAuthorityScoped(t *testing.T) {
	a := runTestApp(t)
	probe := &runAdmissionReconcileProbe{}
	c := &indexRunConsumer{external: probe}
	authority := a.currentChainAuthority()
	for i := 0; i < 4; i++ {
		if err := a.reconcileRunConsumer(context.Background(), c, authority); err != nil {
			t.Fatal(err)
		}
	}
	if probe.calls != 1 {
		t.Fatal("same selected authority repeatedly traversed store", probe.calls)
	}
	extension := authority
	extension.Height++
	extension.Hash = indexDigest("new tip")
	if err := a.reconcileRunConsumer(context.Background(), c, extension); err != nil {
		t.Fatal(err)
	}
	reorg := extension
	reorg.Hash = indexDigest("same-height replacement")
	if err := a.reconcileRunConsumer(context.Background(), c, reorg); err != nil {
		t.Fatal(err)
	}
	if probe.calls != 3 {
		t.Fatal("new tip or same-height replacement skipped reconciliation", probe.calls)
	}
	guarded := context.WithValue(context.Background(), indexRunPublicationKey{}, func() error { return errIndexRunConsumerStopped })
	if err := a.reconcileRunConsumer(guarded, c, reorg); !errors.Is(err, errIndexRunConsumerStopped) {
		t.Fatal("cached reconciliation ignored Off", err)
	}
	c.reconciled = false
	if err := a.reconcileRunConsumer(context.Background(), c, reorg); err != nil {
		t.Fatal(err)
	}
	if probe.calls != 4 {
		t.Fatal("reopened adapter reused reconciliation", probe.calls)
	}
}

func TestTimelineRunCurrentBoundaryJoinNoHiddenPrefixAndRawPromotion(t *testing.T) {
	a := runTestApp(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, retention string) (blockView, error) {
		if retention != "ephemeral" {
			t.Errorf("source retained optional raw data: %s", retention)
		}
		if target.Height == 0 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return blockView{}, ctx.Err()
			}
		}
		return a.indexSourceBlock(ctx, target, retention)
	}
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: "txo-spender", Armed: true}}}
	started, err := a.startIndexRun(context.Background(), req, "")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	joined, err := a.amendIndexRun(context.Background(), indexRunAmendment{RunID: started.Run.ID, AdmissionRevision: 1, Consumers: []indexRunSelection{{Index: "blocks", Armed: true}, {Index: "tx-locator", Armed: true}, {Index: "sat-state", Armed: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if joined.Run.AdmissionRevision != 2 || joined.Run.InitialTargetHeight != 1 || *joined.Run.Request.In != 0 || *joined.Run.Request.Out != 1 {
		t.Fatal(joined.Run)
	}
	for _, id := range []string{"blocks", "tx-locator", "sat-state"} {
		p, ok := indexRunProgressFor(joined.Run, id)
		if !ok || p.EffectiveFrom == nil || *p.EffectiveFrom != 1 {
			t.Fatal("wrong admission boundary", p)
		}
		if id == "sat-state" && (p.AdmissionState != "blocked" || !p.RequiresLeadIn) {
			t.Fatal("missing prefix silently accepted", p)
		}
	}
	if _, err = a.amendIndexRun(context.Background(), indexRunAmendment{RunID: started.Run.ID, AdmissionRevision: 1, Consumers: []indexRunSelection{{Index: "inscriptions", Armed: true}}}); err == nil {
		t.Fatal("stale membership revision accepted")
	}
	releaseOnce.Do(func() { close(release) })
	d := waitRunCondition(t, a, func(d indexRunDocument) bool {
		if d.Run == nil {
			return false
		}
		blocks, _ := indexRunProgressFor(d.Run, "blocks")
		locators, _ := indexRunProgressFor(d.Run, "tx-locator")
		return blocks.Height == 1 && locators.Height == 1
	})
	if d.Run.Counts.SourceReads != 2 {
		t.Fatal("joining repeated source work", d.Run.Counts)
	}
	if _, err = a.controlIndexRun(context.Background(), d.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	d = waitRun(t, a)
	for _, id := range []string{"blocks", "tx-locator"} {
		s, err := indexStoreHead(a.dataDir, id)
		if err != nil || s.checkpoint == nil || s.checkpoint.From != 1 || s.checkpoint.Height != 1 {
			t.Fatal(id, s, err)
		}
	}
	if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", "sat-state", "head.json")); !os.IsNotExist(err) {
		t.Fatal("blocked Sat join wrote state", err)
	}
	if _, err = a.controlIndexRun(context.Background(), d.Run.ID, "stop"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.amendIndexRun(context.Background(), indexRunAmendment{RunID: d.Run.ID, AdmissionRevision: d.Run.AdmissionRevision, Consumers: []indexRunSelection{{Index: "inscriptions", Armed: true}}}); err == nil {
		t.Fatal("join revived stopped run")
	}
}

func TestSelectedAuthorityCapturedTipAllowsExtensionButNotReplacement(t *testing.T) {
	a := runTestApp(t)
	old := a.currentChainAuthority()
	hashAt := a.addressSelectedHashes(context.Background(), old)
	b2 := utxoFixtureBlock(2, old.Hash, utxoFixtureCoinbase(99402, 50))
	installRunProtocolHeader(t, a, &b2)
	if hash, err := hashAt(old.Height); err != nil || hash != old.Hash {
		t.Fatal("ordinary tip extension invalidated captured ancestor", hash, err)
	}
	replacement := utxoFixtureBlock(1, "", utxoFixtureCoinbase(99401, 50))
	installRunProtocolHeader(t, a, &replacement)
	_, err := hashAt(old.Height)
	if err == nil || !isSelectedAuthorityRetry(err) {
		t.Fatal("replacement did not cause typed retry", err)
	}
	if isSelectedAuthorityRetry(context.Canceled) || !errors.Is(selectedAuthorityRetry(context.Canceled), context.Canceled) {
		t.Fatal("cancellation converted to authority retry")
	}
}

func TestSelectedCoreCapturedHeightDoesNotQueryNewLatestTip(t *testing.T) {
	var byHeight, latest atomic.Int64
	newHash := indexDigest("later Core tip")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		var result any
		switch request.Method {
		case "getblockhash":
			byHeight.Add(1)
			var height int64
			_ = json.Unmarshal(request.Params[0], &height)
			if height == 0 {
				result = genesisHashDisplay
			} else {
				result = newHash
			}
		case "getblockchaininfo":
			latest.Add(1)
			result = map[string]any{"blocks": int64(1), "headers": int64(1), "bestblockhash": newHash}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "bod", "result": result, "error": nil})
	}))
	defer server.Close()
	port, _ := strconv.Atoi(server.URL[strings.LastIndex(server.URL, ":")+1:])
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".cookie"), []byte("user:pass"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &app{settings: appSettings{BitcoinDataDir: root, RPCAuthMode: "cookie", RPCPort: port}}
	target, err := a.utxoSelectedTarget(context.Background(), chainAuthorityView{Source: "bitcoin_core", Height: 0, Hash: genesisHashDisplay}, 0)
	if err != nil || target.HashDisplay != genesisHashDisplay || byHeight.Load() != 1 || latest.Load() != 0 {
		t.Fatal("captured height was compared to the newer latest tip", target, err, byHeight.Load(), latest.Load())
	}
}

func TestTimelineRunExtensionDuringBitmapPublicationIsNotTerminal(t *testing.T) {
	a := runTestApp(t)
	b := bitmapDevelopmentFixtureBlock(0, "", "0.bitmap")
	installRunProtocolHeader(t, a, &b)
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, retention string) (blockView, error) {
		next := bitmapDevelopmentFixtureBlock(b.Height+1, b.Hash)
		installRunProtocolHeader(t, a, &next)
		return b, nil
	}
	first := b.Height
	req := indexRunRequest{In: &first, Out: &first, Consumers: []indexRunSelection{{Index: bitmapCanonicalDistrictID, Armed: true}, {Index: "bitmap-district-chain-dev-v1", Armed: true}}}
	if _, err := a.startIndexRun(context.Background(), req, ""); err != nil {
		t.Fatal(err)
	}
	d := waitRun(t, a)
	if d.Run.State != "complete" || d.Run.Counts.SourceReads != 1 {
		t.Fatal(d.Run)
	}
	for _, p := range d.Run.Consumers {
		if p.Height != b.Height || p.State != "complete" {
			t.Fatal(p)
		}
	}
}

func TestTimelineRunPausedJoinWaitsAndPreservesLiveAndFiniteChoices(t *testing.T) {
	a := runTestApp(t)
	t.Cleanup(func() {
		a.indexMu.Lock()
		if a.indexCancel != nil {
			a.indexCancel()
		}
		a.indexMu.Unlock()
	})
	zero := int64(0)
	req := indexRunRequest{In: &zero, Consumers: []indexRunSelection{{Index: "txo-spender", Armed: true, Live: true}}}
	started, err := a.startIndexRun(context.Background(), req, "")
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitRunCondition(t, a, func(d indexRunDocument) bool {
		return d.Run != nil && d.Run.State == "waiting" && d.Run.Counts.SourceReads == 2
	})
	if _, err = a.controlIndexRun(context.Background(), started.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	paused := waitRun(t, a)
	joined, err := a.amendIndexRun(context.Background(), indexRunAmendment{RunID: paused.Run.ID, AdmissionRevision: effectiveIndexRunAdmissionRevision(paused.Run), Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true, Live: true}}})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := indexRunProgressFor(joined.Run, "tx-locator")
	if joined.Run.State != "paused" || p.AdmissionState != "pending" || p.EffectiveFrom == nil || *p.EffectiveFrom != 2 || joined.Run.InitialTargetHeight != waiting.Run.InitialTargetHeight {
		t.Fatal(joined.Run, p)
	}
	if _, err = a.startIndexRun(context.Background(), req, ""); err == nil {
		t.Fatal("Start replaced a paused interval without Stop")
	}
	a.indexMu.Lock()
	active := a.indexCancel != nil
	a.indexMu.Unlock()
	if active {
		t.Fatal("paused amendment started a writer")
	}
	if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", "tx-locator", "head.json")); !os.IsNotExist(err) {
		t.Fatal("pending join wrote while paused", err)
	}
	if _, err = a.controlIndexRun(context.Background(), joined.Run.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool { return d.Run != nil && d.Run.State == "waiting" })
	if _, err = a.controlIndexRun(context.Background(), joined.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	final := waitRun(t, a)
	if final.Run.Counts.SourceReads != 2 {
		t.Fatal("paused join backfilled old blocks", final.Run.Counts)
	}
	for _, id := range []string{"txo-spender", "tx-locator"} {
		selection, _ := runSelection(final.Run.Request, id)
		if !selection.Live || !selection.Armed {
			t.Fatal("Resume changed admitted choices", selection)
		}
	}
	if final.Run.Request.Out != nil || *final.Run.Request.In != 0 || final.Run.InitialTargetHeight != 1 {
		t.Fatal("amendment changed accepted interval", final.Run)
	}
}

func TestTimelineRunRequiredOriginIsExplicitAndCannotBeSkipped(t *testing.T) {
	a := runTestApp(t)
	b := bitmapDevelopmentFixtureBlock(1, "", "0.bitmap")
	installRunProtocolHeader(t, a, &b)
	first, last := b.Height, b.Height
	request := indexRunRequest{In: &first, Out: &last, Consumers: []indexRunSelection{{Index: bitmapCanonicalDistrictID, Armed: true}, {Index: "utxo-state", Armed: true}, {Index: "sat-state", Armed: true}}}
	plan, err := a.planIndexRun(context.Background(), request)
	if err != nil || plan.Ready {
		t.Fatal(plan, err)
	}
	for _, p := range plan.Consumers {
		origin := int64(0)
		if p.RangeMode != "complete_prefix" || p.OriginHeight == nil || *p.OriginHeight != origin || p.NextRequiredHeight == nil || *p.NextRequiredHeight != origin || p.State != "needs_prerequisite" || !p.RequiresLeadIn {
			t.Fatal("required beginning was not explicit", p)
		}
	}
	if _, err = a.startIndexRun(context.Background(), request, ""); err == nil {
		t.Fatal("one-click Start skipped the required origin")
	}
	if a.indexRunSnapshot().Run != nil {
		t.Fatal("blocked start installed an admitted run")
	}
	w := indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/run/start", `{"request":{"in":1,"out":1,"consumers":[{"index":"bitmap-districts-canonical","armed":true}]}}`)
	var response struct {
		Error string       `json:"error"`
		Plan  indexRunPlan `json:"plan"`
	}
	if w.Code != 409 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Plan.Ready || len(response.Plan.Consumers) != 1 || response.Plan.Consumers[0].OriginHeight == nil || response.Plan.Consumers[0].Dependencies == nil {
		t.Fatal("blocked one-click Start omitted inline requirements", w.Code, w.Body.String())
	}
}

func TestTimelineRunPublicationFencesNewJoinAndDurableStopBeforeCancel(t *testing.T) {
	a := runTestApp(t)
	zero := int64(0)
	request := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}}}
	plan, err := a.planIndexRun(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	p := plan.Consumers[0]
	a.indexRunMu.Lock()
	if err = a.loadIndexRunLocked(); err != nil {
		t.Fatal(err)
	}
	a.indexRunState.Configured = true
	a.indexRunState.Prepared = plan.Request
	a.indexRunState.Run = &indexRunRecord{ID: "guard-test", State: "running", Request: plan.Request, Consumers: []indexRunProgress{p}}
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.amendIndexRun(context.Background(), indexRunAmendment{RunID: "guard-test", AdmissionRevision: 1, Consumers: []indexRunSelection{{Index: "blocks", Armed: true}}}); err == nil {
		t.Fatal("legacy run guessed an unrecorded processing boundary")
	}
	guarded := a.indexRunBoundConsumerContext(context.Background(), p)
	if err = checkIndexRunPublication(guarded); err != nil {
		t.Fatal(err)
	}
	a.indexRunMu.Lock()
	boundary := int64(1)
	a.indexRunState.Run.Consumers[0].EffectiveFrom = &boundary
	a.indexRunState.Run.Consumers[0].AdmissionState = "pending"
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = checkIndexRunPublication(guarded); !errors.Is(err, errIndexRunAdmissionChanged) {
		t.Fatal("old cohort published after new effective boundary", err)
	}
	a.indexRunMu.Lock()
	a.indexRunState.Run.State = "cancelled"
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = checkIndexRunPublication(guarded); !errors.Is(err, errIndexRunConsumerStopped) {
		t.Fatal("durable Stop allowed publication before context cancellation", err)
	}
}
