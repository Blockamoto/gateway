package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Inject only the selected evidence-prefix observer, avoiding a fabricated
// full 792k-block locator store. Actual coordinated District/Parcel writers,
// selected headers, and proof publication remain in use.
func TestParcelLiveFollowsEvidencePauseAndReorg079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &b0)
	var mu sync.Mutex
	blocks := map[int64]blockView{b0.Height: b0}
	through := b0.Height
	var proofReads atomic.Int64
	var missingBytes atomic.Bool
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		mu.Lock()
		b, ok := blocks[target.Height]
		mu.Unlock()
		if !ok || b.Hash != target.HashDisplay {
			return blockView{}, fmt.Errorf("selected fixture body unavailable")
		}
		return b, nil
	}
	a.parcelEvidenceHooks079 = &parcelEvidenceHooks079{
		RetryDelay: 20 * time.Millisecond,
		Timeline: func() indexTimelineView {
			mu.Lock()
			h := through
			mu.Unlock()
			authority := a.currentChainAuthority()
			return indexTimelineView{TipKnown: true, TipHeight: authority.Height, Tracks: []indexTimelineTrack{{ID: "tx-locator", Sources: []indexTimelineSource{{ID: "gateway_committed_index", Kind: "committed_records", Complete: true, Ranges: []heightInterval{{From: 0, To: h}}}}}}}
		},
		Configure: func(_ context.Context, c *indexRunConsumer, h int64) error {
			c.external.(*bitmapRunConsumer).SetBlockSource(func(work context.Context, height int64) (blockView, error) {
				if missingBytes.Load() && height == b0.Height+1 {
					noteParcelSourceMissing079(work)
					return blockView{}, fmt.Errorf("fixture source bytes unavailable")
				}
				mu.Lock()
				b, ok := blocks[height]
				mu.Unlock()
				if !ok {
					return blockView{}, fmt.Errorf("proof body unavailable")
				}
				proofReads.Add(1)
				return b, nil
			})
			c.parcelEvidenceThrough = h
			return nil
		},
	}
	zero := int64(0)
	req := indexRunRequest{In: &zero, Consumers: []indexRunSelection{
		{Index: bitmapStandardDistrictID, Armed: true, Live: true},
		{Index: "tx-locator", Armed: true, Live: true},
		{Index: bitmapReplayParcelID, Armed: true, Live: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "tx-locators"}},
	}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	var consumers []indexRunProgress
	for _, p := range plan.Consumers {
		if p.Index != "tx-locator" {
			consumers = append(consumers, p)
		}
	}
	record := &indexRunRecord{ID: "parcel-live-follow", State: "running", Request: plan.Request, TargetHeight: plan.TargetHeight, InitialTargetHeight: plan.TargetHeight, TargetHash: plan.TargetHash, Consumers: consumers, ParcelDistrict: plan.ParcelDistrict}
	record.FrontierHeight = initialIndexRunFrontier(record)
	a.indexRunMu.Lock()
	a.indexRunLoaded = true
	a.indexRunState.Schema = 1
	a.indexRunState.Configured = true
	a.indexRunState.Prepared = plan.Request
	a.indexRunState.Run = record
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	worker, cancel := context.WithCancel(ctx)
	defer cancel()
	a.indexMu.Lock()
	a.indexCancel = cancel
	a.indexRunActiveID = record.ID
	a.indexMu.Unlock()
	go a.runCoordinatedIndexes(worker, record.ID)
	waitHeight := func(height int64, hash string) indexRunDocument {
		return waitRunCondition(t, a, func(d indexRunDocument) bool {
			if d.Run == nil || d.Run.State != "waiting" {
				return false
			}
			p, ok := indexRunProgressFor(d.Run, bitmapReplayParcelID)
			return ok && p.ParcelValidatedHeight != nil && *p.ParcelValidatedHeight == height && p.ParcelValidatedHash == hash
		})
	}
	waitHeight(b0.Height, b0.Hash)
	first := proofReads.Load()
	time.Sleep(1200 * time.Millisecond)
	if proofReads.Load() != first {
		t.Fatal("unchanged evidence repeated proof work")
	}
	b1 := bitmapDevelopmentFixtureBlock(b0.Height+1, b0.Hash)
	installRunProtocolHeader(t, a, &b1)
	mu.Lock()
	blocks[b1.Height] = b1
	mu.Unlock()
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		if d.Run == nil {
			return false
		}
		p, ok := indexRunProgressFor(d.Run, bitmapReplayParcelID)
		h, e := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapStandardDistrictID)
		return ok && p.ParcelValidationState == "pending" && h != nil && e == nil && h.Height == b1.Height
	})
	if h, _ := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapReplayParcelID); h == nil || h.Height != b0.Height {
		t.Fatal("Parcel advanced without proof", h)
	}
	missingBytes.Store(true)
	mu.Lock()
	through = b1.Height
	mu.Unlock()
	notifyParcelIndexCommit079(a.dataDir, "tx-locator")
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		if d.Run == nil {
			return false
		}
		p, ok := indexRunProgressFor(d.Run, bitmapReplayParcelID)
		return ok && strings.Contains(p.ParcelValidationReason, "fixture source bytes unavailable")
	})
	if h, _ := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapReplayParcelID); h == nil || h.Height != b0.Height {
		t.Fatal("missing source advanced Parcel", h)
	}
	// Recovery has no additional prefix or District notification.
	missingBytes.Store(false)
	waitHeight(b1.Height, b1.Hash)
	if _, err = a.controlIndexRun(ctx, record.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool { return d.Run != nil && d.Run.State == "paused" })
	if _, err = a.controlIndexRun(ctx, record.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	waitHeight(b1.Height, b1.Hash)
	// Replace the selected tip at the same height. Only the new selected
	// District and checked Parcel outcome may become the published heads.
	replacement := bitmapDevelopmentFixtureBlock(b1.Height, b0.Hash)
	header := make([]byte, 80)
	binary.LittleEndian.PutUint32(header, 1)
	binary.LittleEndian.PutUint64(header[72:], uint64(b1.Height)+10000000)
	digest := hash256(header)
	replacement.Hash = reverseHex(digest[:])
	mu.Lock()
	blocks[b1.Height] = replacement
	mu.Unlock()
	f, e := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.WriteAt(header, b1.Height*80)
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		t.Fatal(e, closeErr)
	}
	a.setStatus(func(s *appStatus) { s.TipHash = replacement.Hash })
	notifyParcelIndexCommit079(a.dataDir, "tx-locator")
	waitHeight(replacement.Height, replacement.Hash)
	for _, id := range []string{bitmapStandardDistrictID, bitmapReplayParcelID} {
		h, e := readBitmapDevelopmentHead(ctx, a.dataDir, id)
		if e != nil || h == nil || h.BlockHash != replacement.Hash {
			t.Fatalf("%s did not reconcile selected tip: %+v %v", id, h, e)
		}
	}
	if h, e := readParcelDiscoveryHead079(ctx, a.dataDir); e != nil || h != nil {
		t.Fatalf("live created provisional data: %+v %v", h, e)
	}
	if _, err = a.controlIndexRun(ctx, record.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool { return d.Run != nil && d.Run.State == "paused" })
}

func TestParcelLiveHonorsFiniteOutFence079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &b0)
	out := b0.Height
	req := indexRunRequest{Out: &out, Consumers: []indexRunSelection{
		{Index: bitmapStandardDistrictID, Armed: true, Live: true},
		{Index: "blocks", Armed: true, Live: true},
		{Index: bitmapReplayParcelID, Armed: true, Live: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "retained-raw"}},
	}}
	doc, err := a.prepareIndexRun(req)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := a.parcelDistrictSnapshot079(ctx, doc.Prepared, out)
	if err != nil {
		t.Fatal(err)
	}
	doc.Run = &indexRunRecord{ID: "live-out-fence", State: "running", Request: doc.Prepared, InitialTargetHeight: out, TargetHeight: out, ParcelDistrict: pair, Consumers: []indexRunProgress{{Index: bitmapReplayParcelID, AdmissionState: "admitted", From: out, To: out}}}
	a.indexRunMu.Lock()
	a.indexRunState = doc
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	b1 := bitmapDevelopmentFixtureBlock(b0.Height+1, b0.Hash)
	installRunProtocolHeader(t, a, &b1)
	if err = a.allowPairedDistrictPublication079(ctx, b1.Height); !errors.Is(err, ErrBitmapEvidenceRequired) {
		t.Fatalf("Live crossed finite Out: %v", err)
	}
	if err = a.allowPairedDistrictPublication079(ctx, b0.Height); err != nil {
		t.Fatalf("Out block was blocked: %v", err)
	}
}

func TestParcelLiveRebindsOnlyAfterProvenReorg079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &b0)
	b1 := bitmapDevelopmentFixtureBlock(b0.Height+1, b0.Hash)
	installRunProtocolHeader(t, a, &b1)
	district := bitmapTestConsumer(t, a.dataDir, bitmapStandardDistrictID)
	parcel := bitmapTestConsumer(t, a.dataDir, bitmapReplayParcelID)
	for _, b := range []blockView{b0, b1} {
		if err := district.Apply(ctx, b, nil); err != nil {
			t.Fatal(err)
		}
		if err := parcel.Apply(ctx, b, nil); err != nil {
			t.Fatal(err)
		}
	}
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: bitmapStandardDistrictID, Armed: true, Live: true}, {Index: "tx-locator", Armed: true, Live: true}, {Index: bitmapReplayParcelID, Armed: true, Live: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: "tx-locators"}}}}
	pair, err := a.parcelDistrictSnapshot079(ctx, req, b1.Height)
	if err != nil || pair.CeilingHeight != b1.Height {
		t.Fatalf("pair: %+v %v", pair, err)
	}
	corrupted := *pair
	corrupted.CeilingCommit = testID(987654)
	if _, _, err = a.parcelDistrictAlignedCeiling079(ctx, &corrupted, true); err == nil {
		t.Fatal("same-chain commitment mismatch was accepted")
	}
	header := make([]byte, 80)
	binary.LittleEndian.PutUint32(header, 1)
	binary.LittleEndian.PutUint64(header[72:], uint64(b1.Height)+20000000)
	digest := hash256(header)
	newHash := reverseHex(digest[:])
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(header, b1.Height*80)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	a.setStatus(func(s *appStatus) { s.TipHash = newHash })
	// The old District head is not selected until the consumer reconciles.
	if _, _, err = a.parcelDistrictAlignedCeiling079(ctx, pair, true); err == nil {
		t.Fatal("unreconciled District was accepted")
	}
	selected := a.addressSelectedHashes(ctx, a.currentChainAuthority())
	if err = district.Reconcile(ctx, selected); err != nil {
		t.Fatal(err)
	}
	if err = parcel.Reconcile(ctx, selected); err != nil {
		t.Fatal(err)
	}
	aligned, ceiling, err := a.parcelDistrictAlignedCeiling079(ctx, pair, true)
	if err != nil || aligned != b0.Height || ceiling != b0.Height {
		t.Fatalf("reorg rebound failed: aligned=%d ceiling=%d err=%v", aligned, ceiling, err)
	}
	if _, _, err = a.parcelDistrictAlignedCeiling079(ctx, pair, false); err == nil {
		t.Fatal("finite saved fence silently rebound")
	}
}

func TestParcelLiveReorgAtHeadPublicationIsRejected079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	installRunProtocolHeader(t, a, &b)
	district := bitmapTestConsumer(t, a.dataDir, bitmapStandardDistrictID)
	if err := district.Apply(ctx, b, nil); err != nil {
		t.Fatal(err)
	}
	value, err := openBitmapRunConsumer(ctx, a, bitmapReplayParcelID)
	if err != nil {
		t.Fatal(err)
	}
	consumer := value.(*bitmapRunConsumer)
	if err = consumer.Reconcile(ctx, a.addressSelectedHashes(ctx, a.currentChainAuthority())); err != nil {
		t.Fatal(err)
	}
	called := false
	a.parcelEvidenceHooks079 = &parcelEvidenceHooks079{BeforeParcelHead: func(height int64) {
		if height != b.Height || called {
			return
		}
		called = true
		header := make([]byte, 80)
		binary.LittleEndian.PutUint32(header, 1)
		binary.LittleEndian.PutUint64(header[72:], uint64(height)+30000000)
		digest := hash256(header)
		hash := reverseHex(digest[:])
		f, e := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
		if e != nil {
			t.Error(e)
			return
		}
		_, e = f.WriteAt(header, height*80)
		closeErr := f.Close()
		if e != nil || closeErr != nil {
			t.Error(e, closeErr)
			return
		}
		a.setStatus(func(s *appStatus) { s.TipHash = hash })
	}}
	guarded := a.parcelGuardedPublication079(ctx, b.Height, b.Hash, district.head.Commitment)
	err = consumer.Apply(guarded, b, nil)
	if !called || err == nil {
		t.Fatalf("changed-chain publication was not rejected: called=%v err=%v", called, err)
	}
	head, err := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapReplayParcelID)
	if err != nil || head != nil {
		t.Fatalf("stale Parcel head was published: %+v %v", head, err)
	}
}
