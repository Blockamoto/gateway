package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func runTestApp(t *testing.T) *app {
	t.Helper()
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = true
	raw, hash := installBlockOneHeader063(t, a)
	path := filepath.Join(a.dataDir, "indexes", "sources", hash+".block")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return a
}
func waitRun(t *testing.T, a *app) indexRunDocument {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		d := a.indexRunSnapshot()
		if d.Run != nil && (d.Run.State == "complete" || d.Run.State == "complete_with_errors" || d.Run.State == "paused" || d.Run.State == "failed" || d.Run.State == "cancelled") {
			a.indexMu.Lock()
			active := a.indexCancel != nil
			a.indexMu.Unlock()
			if !active {
				return d
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run did not settle", a.indexRunSnapshot())
	return indexRunDocument{}
}
func TestTimelineRunPreparationAndPrefixHaveNoImplicitWork(t *testing.T) {
	a := runTestApp(t)
	one := int64(1)
	req := indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{{Index: "utxo-state", Armed: true}, {Index: "sat-state", Armed: true}}}
	d, err := a.prepareIndexRun(req)
	if err != nil || !d.Configured || d.Run != nil {
		t.Fatal(d, err)
	}
	p, err := a.planIndexRun(context.Background(), req)
	if err != nil || p.Ready || len(p.Consumers) != 2 {
		t.Fatal(p, err)
	}
	for _, c := range p.Consumers {
		if !c.RequiresLeadIn || c.State != "needs_prerequisite" || len(c.Dependencies) == 0 {
			t.Fatal(c)
		}
	}
	a.liveIndexTick()
	a.indexMu.Lock()
	active := a.indexCancel != nil
	a.indexMu.Unlock()
	if active {
		t.Fatal("staged Live preference started work")
	}
	for _, index := range []string{"utxo-state", "sat-state", "txo-spender"} {
		if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", index, "head.json")); !os.IsNotExist(err) {
			t.Fatal("planning wrote an index", index, err)
		}
	}
}
func TestTimelineRunSharesBlocksWithIndependentStateAndNoRawTrack(t *testing.T) {
	a := runTestApp(t)
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: "utxo-state", Armed: true}, {Index: "sat-state", Armed: true}, {Index: "tx-locator", Armed: true}, {Index: "txo-spender", Armed: true}, {Index: "address-history", Armed: true}, {Index: "address-state", Armed: true}}}
	p, err := a.planIndexRun(context.Background(), req)
	if err != nil || !p.Ready {
		t.Fatal(p, err)
	}
	if p.RawRetention != "ephemeral" {
		t.Fatal(p)
	}
	if _, err = a.startIndexRun(context.Background(), req, p.PlanID); err != nil {
		t.Fatal(err)
	}
	d := waitRun(t, a)
	if d.Run.State != "complete" {
		t.Fatal(d.Run)
	}
	if d.Run.Counts.SourceReads != 2 || d.Run.Counts.DecodedBlocks != 2 || d.Run.Counts.InscriptionExtractions != 2 {
		t.Fatal("duplicate source work", d.Run.Counts)
	}
	for _, c := range d.Run.Consumers {
		if c.State != "complete" || c.Height != 1 || c.Commitment == "" {
			t.Fatal("consumer did not publish its own checkpoint", c)
		}
	}
	blocks, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || blocks.checkpoint != nil {
		t.Fatal("source reads built optional raw coverage", blocks, err)
	}
	u, err := utxoStoreHead(a.dataDir)
	if err != nil || u.Head() == nil || u.Head().Height != 1 || u.Head().LiveOutputs != 1 {
		t.Fatal("UTXO state missing", u, err)
	}
	s, err := indexStoreHead(a.dataDir, "sat-state")
	if err != nil || s.checkpoint == nil || s.checkpoint.Height != 1 {
		t.Fatal("Sat state missing", s, err)
	}
	before := s.checkpoint.Commitment
	if _, err = a.controlIndexRun(context.Background(), d.Run.ID, "resume"); err == nil {
		t.Fatal("completed bounded run resumed without a new review")
	}
	s, _ = indexStoreHead(a.dataDir, "sat-state")
	if s.checkpoint.Commitment != before {
		t.Fatal("completed state changed")
	}
}
func TestTimelineRunOffPreservesFrozenAddressFacts(t *testing.T) {
	u, _, b1 := address076Source(t, []uint64{5}, []string{address076P2PKH})
	cp, err := buildAddressStateContext(context.Background(), u.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{dataDir: u.root}
	_, err = a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "utxo-state", Armed: true}, {Index: "address-state", Armed: false}}})
	if err != nil {
		t.Fatal(err)
	}
	spend := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(88000, 50), utxoFixtureSpend(88001, []inputView{{PrevTxID: b1.Transactions[0].TxID, PrevVout: 0}}, 5))
	utxoApply(t, u, spend)
	frozen, err := readAddressStateContext(context.Background(), u.root)
	if err != nil || frozen.Commitment != cp.Commitment {
		t.Fatal("Off address projection advanced", frozen, err)
	}
	pins := map[string]bool{}
	if err = visitCurrentAddressFactPagesContext(context.Background(), u.root, func(p string) error { pins[p] = true; return nil }); err != nil || len(pins) == 0 {
		t.Fatal("frozen projection lost its ownership", pins, err)
	}
	for page := range pins {
		if _, err = readOutputFactPageContext(context.Background(), u.root, page); err != nil {
			t.Fatal(err)
		}
	}
}
func TestTimelineRunRestartAndMalformedMarksFailClosed(t *testing.T) {
	a := runTestApp(t)
	zero := int64(0)
	req := indexRunRequest{In: &zero, Out: &zero, InHash: "bad", Consumers: []indexRunSelection{{Index: "blocks", Armed: true}}}
	if _, err := a.planIndexRun(context.Background(), req); err == nil {
		t.Fatal("malformed marked identity accepted")
	}
	req.InHash = ""
	d, err := a.prepareIndexRun(req)
	if err != nil {
		t.Fatal(err)
	}
	a.indexRunMu.Lock()
	a.indexRunState.Run = &indexRunRecord{ID: "interrupted", State: "running", Request: d.Prepared}
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus()}
	got := restarted.indexRunSnapshot()
	if got.Run == nil || got.Run.State != "paused" {
		t.Fatal(got)
	}
	if restarted.indexCancel != nil {
		t.Fatal("status read restarted a run")
	}
}

func waitRunCondition(t *testing.T, a *app, condition func(indexRunDocument) bool) indexRunDocument {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		d := a.indexRunSnapshot()
		if condition(d) {
			return d
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("run condition was not reached", a.indexRunSnapshot())
	return indexRunDocument{}
}
func TestTimelineRunPauseResumeMissingBodyAndCancellationIntent(t *testing.T) {
	a := runTestApp(t)
	raw := mainnetBlockOne(t)
	h := hash256(raw[:80])
	path := filepath.Join(a.dataDir, "indexes", "sources", reverseHex(h[:])+".block")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: "utxo-state", Armed: true}, {Index: "sat-state", Armed: true}}}
	plan, err := a.planIndexRun(context.Background(), req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(context.Background(), req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	waiting := waitRunCondition(t, a, func(d indexRunDocument) bool {
		return d.Run != nil && d.Run.State == "waiting" && d.Run.Counts.SourceReads == 1
	})
	if _, err = a.controlIndexRun(context.Background(), waiting.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	paused := waitRun(t, a)
	if paused.Run.State != "paused" {
		t.Fatal(paused)
	}
	for _, c := range paused.Run.Consumers {
		if c.Height != 0 {
			t.Fatal("Pause lost committed prefix", c)
		}
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = a.controlIndexRun(context.Background(), paused.Run.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" || done.Run.Counts.SourceReads != 2 {
		t.Fatal("resume replayed committed blocks", done.Run)
	}
	// A cancelled durable request cannot be revived by status loading.
	if _, err = a.controlIndexRun(context.Background(), done.Run.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
}
func TestTimelineRunMixedLiveStopsNonLiveAtAdmittedTarget(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = true
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: "utxo-state", Armed: true}, {Index: "tx-locator", Armed: true, Live: true}}}
	p, err := a.planIndexRun(context.Background(), req)
	if err != nil || !p.Ready {
		t.Fatal(p, err)
	}
	if _, err = a.startIndexRun(context.Background(), req, p.PlanID); err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		return d.Run != nil && d.Run.State == "waiting" && d.Run.Counts.SourceReads == 1
	})
	raw, hash := installBlockOneHeader063(t, a)
	path := filepath.Join(a.dataDir, "indexes", "sources", hash+".block")
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	d := waitRunCondition(t, a, func(d indexRunDocument) bool {
		if d.Run == nil {
			return false
		}
		for _, c := range d.Run.Consumers {
			if c.Index == "tx-locator" && c.Height == 1 {
				return true
			}
		}
		return false
	})
	if _, err = a.controlIndexRun(context.Background(), d.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	paused := waitRun(t, a)
	for _, c := range paused.Run.Consumers {
		if c.Index == "utxo-state" && c.Height != 0 {
			t.Fatal("non-Live consumer followed another track", c)
		}
	}
}
func TestTimelineRunRawBlocksOwnRetentionAndOutCapsLive(t *testing.T) {
	a := runTestApp(t)
	zero := int64(0)
	req := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: "blocks", Armed: true, Live: true}, {Index: "utxo-state", Armed: true, Live: true}}}
	p, err := a.planIndexRun(context.Background(), req)
	if err != nil || !p.Ready || p.RawRetention != "retain" {
		t.Fatal(p, err)
	}
	if _, err = a.startIndexRun(context.Background(), req, p.PlanID); err != nil {
		t.Fatal(err)
	}
	d := waitRun(t, a)
	if d.Run.State != "complete" || d.Run.TargetHeight != 0 || d.Run.Counts.SourceReads != 1 {
		t.Fatal("fixed Out did not cap Live", d.Run)
	}
	blocks, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || blocks.checkpoint == nil || blocks.checkpoint.Height != 0 {
		t.Fatal(blocks, err)
	}
	req.Consumers[0].Armed = false
	if _, err = a.prepareIndexRun(req); err != nil {
		t.Fatal(err)
	}
	after, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || after.checkpoint.Commitment != blocks.checkpoint.Commitment {
		t.Fatal("Off removed existing raw coverage", err)
	}
}

func TestTimelineRunPublicationGuardPreservesPriorHead(t *testing.T) {
	ctx := context.Background()
	u, b0, b1 := address076Source(t, []uint64{5}, []string{address076P2PKH})
	before := u.Head().Commitment
	guarded := context.WithValue(ctx, indexRunPublicationKey{}, func() error { return errIndexRunConsumerStopped })
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(88100, 50))
	if err := u.ApplyBlock(guarded, b2); !errors.Is(err, errIndexRunConsumerStopped) {
		t.Fatal("publication did not honor stopped consumer", err)
	}
	reopened, err := utxoStoreHead(u.root)
	if err != nil || reopened.Head().Commitment != before {
		t.Fatal("stopped publication advanced source", err)
	}
	s, err := openIndexStoreContext(ctx, t.TempDir(), "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	s.head.Mode = "lean"
	if err = s.appendPreparedBlock(b0, 0, "ephemeral", nil); err != nil {
		t.Fatal(err)
	}
	cp := s.checkpoint.Commitment
	s.locatorContext = guarded
	if err = s.appendPreparedBlock(b1, 0, "ephemeral", nil); !errors.Is(err, errIndexRunConsumerStopped) {
		t.Fatal("ordinary index ignored guard", err)
	}
	fresh, err := indexStoreHead(s.profileRoot, "tx-locator")
	if err != nil || fresh.checkpoint.Commitment != cp {
		t.Fatal("failed ordinary consumer advanced coverage", err)
	}
}
func TestTimelineRunFrozenAddressFactsSurviveUndoExpiryAndCollection(t *testing.T) {
	ctx := context.Background()
	u, _, b1 := address076Source(t, []uint64{5}, []string{address076P2PKH})
	if _, err := buildAddressStateContext(ctx, u.root, nil); err != nil {
		t.Fatal(err)
	}
	owned := map[string]bool{}
	if err := visitCurrentAddressFactPagesContext(ctx, u.root, func(p string) error { owned[p] = true; return nil }); err != nil {
		t.Fatal(err)
	}
	a := &app{dataDir: u.root}
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "utxo-state", Armed: true}, {Index: "address-state", Armed: false}}}); err != nil {
		t.Fatal(err)
	}
	b := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(88200, 50), utxoFixtureSpend(88201, []inputView{{PrevTxID: b1.Transactions[0].TxID, PrevVout: 0}}, 5))
	utxoApply(t, u, b)
	for height := int64(3); height <= 291; height++ {
		next := utxoFixtureBlock(height, b.Hash, utxoFixtureCoinbase(uint64(88200+height), 50))
		utxoApply(t, u, next)
		b = next
	}
	if u.Head().RecoveryBoundaryHeight <= 1 {
		t.Fatal("fixture did not expire original undo", u.Head())
	}
	for p := range owned {
		path, err := outputFactPath(u.root, p)
		if err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-2 * time.Hour)
		if err = os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := collectSharedOutputFactsContext(ctx, u.root); err != nil {
		t.Fatal(err)
	}
	for p := range owned {
		if _, err := readOutputFactPageContext(ctx, u.root, p); err != nil {
			t.Fatal("collector reclaimed frozen address facts", err)
		}
	}
}

// Synthetic protocol-height blocks exercise the coordinator and consumer
// contracts. The separate genesis/block-one fixtures exercise real source
// parsing; this injected source is never configurable through the public API.
func installRunProtocolHeader(t *testing.T, a *app, block *blockView) {
	t.Helper()
	header := make([]byte, 80)
	binary.LittleEndian.PutUint32(header, 1)
	binary.LittleEndian.PutUint64(header[72:], uint64(block.Height))
	digest := hash256(header)
	block.Hash = reverseHex(digest[:])
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(header, block.Height*80)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	a.setStatus(func(s *appStatus) {
		s.HeaderHeight = block.Height
		s.HeaderCount = block.Height + 1
		s.TipHash = block.Hash
		s.Ready = true
		s.Syncing = false
	})
}

func TestTimelineRunBitmapSharedExtractionAndIndependentWaiting(t *testing.T) {
	a := runTestApp(t)
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "792435.bitmap")
	installRunProtocolHeader(t, a, &b0)
	parent, err := hex.DecodeString(b0.Transactions[1].TxID)
	if err != nil {
		t.Fatal(err)
	}
	for i, j := 0, len(parent)-1; i < j; i, j = i+1, j-1 {
		parent[i], parent[j] = parent[j], parent[i]
	}
	b1 := bitmapDevelopmentFixtureBlock(b0.Height+1, b0.Hash, "0.792435.bitmap")
	b1.Transactions[1].Inputs[0].Witness = ordWitness050(inscriptionTestScript([]byte{1}, []byte("text/plain"), []byte{3}, parent, nil, []byte("0.792435.bitmap")))
	installRunProtocolHeader(t, a, &b1)
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, retention string) (blockView, error) {
		for _, b := range []blockView{b0, b1} {
			if b.Height == target.Height && b.Hash == target.HashDisplay {
				return b, nil
			}
		}
		return a.indexSourceBlock(ctx, target, retention)
	}
	first, last := b0.Height, b0.Height
	req := indexRunRequest{In: &first, Out: &last, Consumers: []indexRunSelection{{Index: bitmapCanonicalDistrictID, Armed: true}, {Index: bitmapCanonicalParcelID, Armed: true}}}
	p, err := a.planIndexRun(context.Background(), req)
	if err != nil || !p.Ready {
		t.Fatal(p, err)
	}
	if _, err = a.startIndexRun(context.Background(), req, p.PlanID); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" || done.Run.Counts.SourceReads != 1 || done.Run.Counts.InscriptionExtractions != 1 {
		t.Fatal("empty Parcel evaluation duplicated extraction or blocked the run", done.Run)
	}
	for _, c := range done.Run.Consumers {
		if c.Height != b0.Height || c.State != "complete" || c.Commitment == "" {
			t.Fatal(c)
		}
	}
	last = b1.Height
	p, err = a.planIndexRun(context.Background(), req)
	if err != nil || !p.Ready {
		t.Fatal(p, err)
	}
	if _, err = a.startIndexRun(context.Background(), req, p.PlanID); err != nil {
		t.Fatal(err)
	}
	waiting := waitRunCondition(t, a, func(d indexRunDocument) bool {
		if d.Run == nil {
			return false
		}
		states := map[string]indexRunProgress{}
		for _, c := range d.Run.Consumers {
			states[c.Index] = c
		}
		return states[bitmapCanonicalDistrictID].Height == b1.Height && states[bitmapCanonicalParcelID].State == "waiting" && states[bitmapCanonicalParcelID].Height == b0.Height
	})
	if waiting.Run.Counts.InscriptionExtractions != 1 || waiting.Run.Counts.SourceReads != 2 {
		t.Fatal("shared reveal or referenced block read was repeated", waiting.Run.Counts)
	}
	if _, err = a.controlIndexRun(context.Background(), waiting.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	waitRun(t, a)
	parcel := bitmapTestConsumer(t, a.dataDir, bitmapCanonicalParcelID)
	pending, err := readBitmapPending(context.Background(), a.dataDir, bitmapCanonicalParcelID, parcel.head)
	if err != nil || pending == nil || len(pending.Candidates) != 1 || pending.Candidates[0].Eligibility != "unresolved" {
		t.Fatal(pending, err)
	}
	if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", "sat-state", "head.json")); !os.IsNotExist(err) {
		t.Fatal("conditional Parcel proof started Sats", err)
	}
}

func TestTimelineRunOffAtAddressAndUTXOPublication(t *testing.T) {
	ctx := context.Background()
	u, _, b1 := address076Source(t, []uint64{5}, []string{address076P2PKH})
	a := &app{dataDir: u.root}
	active := indexRunRequest{Consumers: []indexRunSelection{{Index: "utxo-state", Armed: true}, {Index: "address-state", Armed: true}}}
	if _, err := a.prepareIndexRun(active); err != nil {
		t.Fatal(err)
	}
	_, err := buildAddressStateWithHookContext(ctx, u.root, nil, func(stage string) error {
		if stage == "before_address_head" {
			next := active
			next.Consumers = append([]indexRunSelection(nil), active.Consumers...)
			next.Consumers[1].Armed = false
			_, e := a.prepareIndexRun(next)
			return e
		}
		return nil
	})
	if !errors.Is(err, errIndexRunConsumerStopped) {
		t.Fatal("Off during projection published an address head", err)
	}
	if _, err = readAddressStateContext(ctx, u.root); !errors.Is(err, ErrAddressStateMissing) {
		t.Fatal("stopped address head became visible", err)
	}
	before := u.Head().Commitment
	u.commitHook = func(stage string) error {
		if stage == "before_head" {
			next := active
			next.Consumers = append([]indexRunSelection(nil), active.Consumers...)
			next.Consumers[0].Armed = false
			_, e := a.prepareIndexRun(next)
			return e
		}
		return nil
	}
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(88900, 50))
	if err = u.ApplyBlock(ctx, b2); !errors.Is(err, errIndexRunConsumerStopped) {
		t.Fatal("Off at UTXO visibility boundary was ignored", err)
	}
	fresh, err := utxoStoreHead(u.root)
	if err != nil || fresh.Head().Commitment != before {
		t.Fatal("stopped UTXO publication replaced prior state", err)
	}
}

func TestTimelineRunCancelledIntentCannotBeResumedFromStaleSnapshot(t *testing.T) {
	a := runTestApp(t)
	zero := int64(0)
	req := indexRunRequest{In: &zero, Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true, Live: true}}}
	p, err := a.planIndexRun(context.Background(), req)
	if err != nil || !p.Ready {
		t.Fatal(p, err)
	}
	if _, err = a.startIndexRun(context.Background(), req, p.PlanID); err != nil {
		t.Fatal(err)
	}
	waiting := waitRunCondition(t, a, func(d indexRunDocument) bool {
		return d.Run != nil && d.Run.State == "waiting" && d.Run.Counts.SourceReads == 2
	})
	if _, err = a.controlIndexRun(context.Background(), waiting.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	paused := waitRun(t, a)
	if _, err = a.controlIndexRun(context.Background(), paused.Run.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.resumeIndexRun(context.Background(), paused.Run); err == nil {
		t.Fatal("stale paused snapshot overrode durable Cancel")
	}
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus()}
	if d := restarted.indexRunSnapshot(); d.Run == nil || d.Run.State != "cancelled" {
		t.Fatal("restart forgot cancellation", d)
	}
	if restarted.indexCancel != nil {
		t.Fatal("reading cancelled state started work")
	}
}

func TestTimelineRunAPIStagesOnlyAndRejectsForeignOrPerConsumerBounds(t *testing.T) {
	a := runTestApp(t)
	handler := indexInterfaceHandler(a)
	foreign := httptest.NewRecorder()
	a.handleIndexRun(foreign, httptest.NewRequest("GET", "http://untrusted.invalid/api/v1/index/run", nil))
	if foreign.Code != 403 {
		t.Fatal("private run settings exposed", foreign.Code)
	}
	for _, body := range []string{`{"in":0,"out":1,"consumers":[{"index":"tx-locator","armed":true,"from":0}]}`, `{"consumers":[{"index":"tx-locator","armed":true,"retention":"retain"}]}`} {
		w := indexInterfaceCall(handler, "POST", "/api/v1/index/run/prepare", body)
		if w.Code != 400 {
			t.Fatal("accepted a competing range or raw policy", w.Code, w.Body.String())
		}
	}
	w := indexInterfaceCall(handler, "POST", "/api/v1/index/run/prepare", `{"in":0,"out":1,"consumers":[{"index":"tx-locator","armed":true,"live":true}]}`)
	var prepared indexRunDocument
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &prepared) != nil || !prepared.Configured || prepared.Run != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	a.indexMu.Lock()
	active := a.indexCancel != nil
	a.indexMu.Unlock()
	if active {
		t.Fatal("staging controls started an index worker")
	}
	w = indexInterfaceCall(handler, "POST", "/api/v1/index/run/start", `{"request":{"in":0,"out":1,"consumers":[{"index":"tx-locator","armed":true}]},"plan_id":"stale"}`)
	if w.Code != 409 {
		t.Fatal("unreviewed run started", w.Code, w.Body.String())
	}
}
