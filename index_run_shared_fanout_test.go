package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSharedBlockTransactionPointPromotionUsesNoBody(t *testing.T) {
	a := runTestApp(t)
	zero, one := int64(0), int64(1)
	request := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}}}
	if _, err := a.prepareIndexRun(request); err != nil {
		t.Fatal(err)
	}
	blocks := []blockView{}
	for height, raw := range [][]byte{testGenesisBlockPayload(t), mainnetBlockOne(t)} {
		hash := hash256(raw[:80])
		block, err := parseBlockDetailed(int64(height), reverseHex(hash[:]), raw[:80], raw, "shared point fixture", false)
		if err != nil {
			t.Fatal(err)
		}
		blocks = append(blocks, block)
		for _, result := range a.ingestResolvedVerifiedBlock(context.Background(), block) {
			if result.State == "error" || result.State == "unavailable" {
				t.Fatal(result)
			}
		}
	}
	var sources atomic.Int64
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) {
		sources.Add(1)
		return blockView{}, fmt.Errorf("point promotion must not acquire a body")
	}
	if _, err := a.startIndexRun(context.Background(), request, ""); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" || sources.Load() != 0 || done.Run.Counts.SourceReads != 0 || done.Run.Counts.DecodedBlocks != 0 || done.Run.Counts.InscriptionExtractions != 0 {
		t.Fatal("promotion obtained or parsed a source", done.Run, sources.Load())
	}
	current, err := indexStoreHead(a.dataDir, "tx-locator")
	if err != nil || current.checkpoint == nil || current.checkpoint.From != 0 || current.checkpoint.Height != 1 {
		t.Fatal(current, err)
	}
	last, err := current.readBatch(current.checkpoint.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	if last.Checkpoint.PreviousCommitment == "" || last.Checkpoint.From != 0 || last.PreviousBlockHash != blocks[0].Hash {
		t.Fatal("singleton ancestry leaked into native prefix", last.Checkpoint)
	}
	control, err := openIndexStore(t.TempDir(), "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	control.head.Mode = "lean"
	for _, block := range blocks {
		if err = control.appendPreparedBlock(block, 0, "ephemeral", nil); err != nil {
			t.Fatal(err)
		}
	}
	if current.checkpoint.Commitment != control.checkpoint.Commitment {
		t.Fatal("promotion changed normal native commitments", current.checkpoint, control.checkpoint)
	}
}

// These structural fixtures bind the ordered transaction IDs into the selected
// header, so bodyless promotion exercises its actual Merkle proof. They do not
// claim Bitcoin consensus/PoW validation or external-chain throughput.
func sharedProtocolHeader(t *testing.T, a *app, b *blockView) {
	t.Helper()
	header := make([]byte, 80)
	binary.LittleEndian.PutUint32(header, 1)
	binary.LittleEndian.PutUint64(header[72:], uint64(b.Height))
	if b.PreviousBlockHash != "" {
		previous, err := displayHashRaw(b.PreviousBlockHash)
		if err != nil {
			t.Fatal(err)
		}
		copy(header[4:36], previous[:])
	}
	ids := make([][32]byte, len(b.Transactions))
	for i, tx := range b.Transactions {
		id, err := displayHashRaw(tx.TxID)
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	root := merkleRoot(ids)
	copy(header[36:68], root[:])
	hash := hash256(header)
	b.Hash = reverseHex(hash[:])
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(header, b.Height*80)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	a.setStatus(func(s *appStatus) {
		s.HeaderHeight = b.Height
		s.HeaderCount = b.Height + 1
		s.TipHash = b.Hash
		s.Ready = true
		s.Syncing = false
	})
}

func TestSharedBlockWorkerDifferentFrontiersSharesCompleteFactsAndPromotesFutureTX(t *testing.T) {
	a := runTestApp(t)
	var blocks []blockView
	previous := ""
	for i := 0; i < 3; i++ {
		b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart+int64(i), previous, fmt.Sprintf("%d.bitmap", i))
		sharedProtocolHeader(t, a, &b)
		previous = b.Hash
		blocks = append(blocks, b)
	}
	// Native Inscriptions and Bitmap are two blocks ahead of the new TX prefix.
	// Seed only through their normal consumers, with complete extractor receipts.
	district := bitmapTestConsumer(t, a.dataDir, bitmapStandardDistrictID)
	parcel := bitmapTestConsumer(t, a.dataDir, bitmapStandardParcelID)
	inscriptions, err := openIndexStore(a.dataDir, "inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	inscriptions.head.Mode = "lean"
	for _, b := range blocks[:2] {
		x, err := extractInscriptionOccurrences(b)
		if err != nil {
			t.Fatal(err)
		}
		if err = district.Apply(context.Background(), b, &x); err != nil {
			t.Fatal(err)
		}
		if err = parcel.Apply(context.Background(), b, &x); err != nil {
			t.Fatal(err)
		}
		if err = inscriptions.appendPreparedBlock(b, blocks[0].Height, "ephemeral", &x); err != nil {
			t.Fatal(err)
		}
	}
	district.Close()
	parcel.Close()
	parcelBefore, err := os.ReadFile(filepath.Join(parcel.dir, "head.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := map[int64]int{}
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, _ string) (blockView, error) {
		mu.Lock()
		calls[target.Height]++
		mu.Unlock()
		for _, b := range blocks {
			if b.Height == target.Height && b.Hash == target.HashDisplay {
				// Actual source hooks must defer speculative fanout until the owner
				// plan exists. This source has no complete inventory attached yet.
				if rows := a.ingestResolvedVerifiedBlock(ctx, b); len(rows) != 0 {
					return blockView{}, fmt.Errorf("scheduled source published before consumption: %v", rows)
				}
				return b, nil
			}
		}
		return blockView{}, fmt.Errorf("unexpected source height %d", target.Height)
	}
	first, last := blocks[0].Height, blocks[2].Height
	req := indexRunRequest{In: &first, Out: &last, Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}, {Index: "inscriptions", Armed: true, indexRunOptions: indexRunOptions{ConventionalIDs: boolPointer(false)}}, {Index: bitmapStandardDistrictID, Armed: true}}}
	if _, err = a.startIndexRun(context.Background(), req, ""); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
	if done.Run.Counts.SourceReads != 3 || done.Run.Counts.DecodedBlocks != 3 || done.Run.Counts.InscriptionExtractions != 3 {
		t.Fatal("different frontiers duplicated shared facts", done.Run.Counts)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, b := range blocks {
		if calls[b.Height] != 1 {
			t.Fatal("source reacquired", b.Height, calls)
		}
	}
	// The future body was offered to TX while Inscriptions/District
	// owned it. Its durable point survives native staged catch-up and remains
	// readable; the normal contiguous native prefix is independently complete.
	if _, have, err := a.browsedBatchContext(context.Background(), "tx-locator", last, blocks[2].Hash); err != nil || !have {
		t.Fatal("future point was suppressed/lost", have, err)
	}
	for _, b := range blocks[:2] {
		if _, have, err := readBrowseTXPoint(context.Background(), a.dataDir, b.Height); err != nil || have {
			t.Fatal("cohort/staged native owner also captured point", b.Height, have, err)
		}
	}
	for _, id := range []string{"tx-locator", "inscriptions", bitmapStandardDistrictID} {
		if bitmapConsumerID(id) {
			c := bitmapTestConsumer(t, a.dataDir, id)
			if c.head == nil || c.head.Height != last {
				t.Fatal(id, c.head)
			}
			c.Close()
		} else {
			s, err := indexStoreHead(a.dataDir, id)
			if err != nil || s.checkpoint == nil || s.checkpoint.Height != last || s.checkpoint.From != first {
				t.Fatal(id, s, err)
			}
		}
	}
	if after, err := os.ReadFile(filepath.Join(parcel.dir, "head.json")); err != nil || string(after) != string(parcelBefore) {
		t.Fatal("disabled saved Parcel advanced or changed", err)
	}
	point, have, err := a.browsedBatchContext(context.Background(), "inscriptions", last, blocks[2].Hash)
	if err != nil {
		t.Fatal(err)
	}
	if have && !inscriptionBatchInventoryComplete(point) {
		t.Fatal("candidate subset claimed complete inventory")
	}
}

func TestSharedBlockTransactionPointCannotSkipNativeGap(t *testing.T) {
	a := runTestApp(t)
	zero, one := int64(0), int64(1)
	request := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}}}
	if _, err := a.prepareIndexRun(request); err != nil {
		t.Fatal(err)
	}
	raw := mainnetBlockOne(t)
	hash := hash256(raw[:80])
	last, err := parseBlockDetailed(1, reverseHex(hash[:]), raw[:80], raw, "point gap fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	a.ingestResolvedVerifiedBlock(context.Background(), last)
	var zeroSources, lateSources atomic.Int64
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, retention string) (blockView, error) {
		if target.Height != 0 {
			lateSources.Add(1)
			return blockView{}, fmt.Errorf("known point source was reacquired")
		}
		zeroSources.Add(1)
		raw := testGenesisBlockPayload(t)
		return parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "required prefix fixture", false)
	}
	if _, err = a.startIndexRun(context.Background(), request, ""); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" || zeroSources.Load() != 1 || lateSources.Load() != 0 || done.Run.Counts.DecodedBlocks != 1 {
		t.Fatal("point skipped genesis or was reacquired", done.Run, zeroSources.Load(), lateSources.Load())
	}
}

func sharedBitmapDispatchFixture(t *testing.T) (*app, blockView, *bitmapRunConsumer, *bitmapRunConsumer, *indexStore, *indexRunPrepared) {
	t.Helper()
	a := runTestApp(t)
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}, {Index: bitmapStandardDistrictID, Armed: true}}}); err != nil {
		t.Fatal(err)
	}
	block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	installRunProtocolHeader(t, a, &block)
	district := bitmapTestConsumer(t, a.dataDir, bitmapStandardDistrictID)
	parcel := bitmapTestConsumer(t, a.dataDir, bitmapStandardParcelID)
	tx, err := openIndexStore(a.dataDir, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	tx.head.Mode = "lean"
	if err = tx.BeginLocatorBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tx.AbortLocatorBatch)
	extraction, err := extractInscriptionOccurrences(block)
	if err != nil {
		t.Fatal(err)
	}
	prepared := &indexRunPrepared{block: block, extraction: &extraction}
	return a, block, district, parcel, tx, prepared
}

func TestSharedBlockTXLaneOverlapsDistrictAndSkipsDisabledParcel(t *testing.T) {
	a, block, district, parcel, tx, prepared := sharedBitmapDispatchFixture(t)
	txEntered, districtEntered, districtDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	operations := map[string]indexRunBlockConsumer{
		"tx-locator": func(ctx context.Context) error {
			close(txEntered)
			select {
			case <-districtEntered:
			case <-time.After(3 * time.Second):
				return fmt.Errorf("District did not overlap TX lane")
			}
			tx.locatorContext = ctx
			return tx.appendPreparedBlock(block, block.Height, "ephemeral", nil)
		},
		bitmapStandardDistrictID: func(ctx context.Context) error {
			close(districtEntered)
			select {
			case <-txEntered:
			case <-time.After(3 * time.Second):
				return fmt.Errorf("TX lane did not start")
			}
			err := district.Apply(ctx, block, prepared.extraction)
			close(districtDone)
			return err
		},
		bitmapStandardParcelID: func(ctx context.Context) error {
			select {
			case <-districtDone:
			default:
				return fmt.Errorf("Parcel ran before District joined")
			}
			return parcel.Apply(ctx, block, prepared.extraction)
		},
	}
	ctx := context.WithValue(context.Background(), indexRunPreparedKey{}, prepared)
	ctx = context.WithValue(ctx, indexRunBlockConsumersKey{}, operations)
	for _, result := range a.ingestArmedResolvedBlock(ctx, block) {
		if result.State == "unavailable" || result.State == "error" {
			t.Fatal(result)
		}
	}
	if district.head == nil || district.head.Height != block.Height || parcel.head != nil {
		t.Fatal("enabled District missing or disabled Parcel executed")
	}
	if n, _, _ := tx.LocatorBatchStats(); n != 1 || tx.DurableCheckpoint() != nil {
		t.Fatal("shared dispatch corrupted TX staging", n)
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", "browse", "tx-locator", "head.json")); !os.IsNotExist(err) {
		t.Fatal("actual cohort TX owner also published a point", err)
	}
	if _, have, err := readBrowseTXPoint(context.Background(), a.dataDir, block.Height); err != nil || have {
		t.Fatal("actual cohort TX owner also published packed point", have, err)
	}
	if prepared.extraction == nil || validateCompleteInscriptionInventory(block, prepared.extraction) != nil {
		t.Fatal("shared complete inventory was lost")
	}
}

func TestSharedBlockFailedTXDoesNotCancelBitmapGroup(t *testing.T) {
	a, block, district, parcel, _, prepared := sharedBitmapDispatchFixture(t)
	failure := errors.New("independent TX failure")
	operations := map[string]indexRunBlockConsumer{"tx-locator": func(context.Context) error { return failure }, bitmapStandardDistrictID: func(ctx context.Context) error { return district.Apply(ctx, block, prepared.extraction) }, bitmapStandardParcelID: func(ctx context.Context) error { return parcel.Apply(ctx, block, prepared.extraction) }}
	ctx := context.WithValue(context.Background(), indexRunPreparedKey{}, prepared)
	ctx = context.WithValue(ctx, indexRunBlockConsumersKey{}, operations)
	var failed bool
	for _, result := range a.ingestArmedResolvedBlock(ctx, block) {
		if result.Index == "tx-locator" {
			failed = result.State == "unavailable" && result.Error == failure.Error()
		} else if result.State == "unavailable" || result.State == "error" {
			t.Fatal(result)
		}
	}
	if !failed || district.head == nil || parcel.head != nil {
		t.Fatal("independent error cancelled successful state", failed, district.head, parcel.head)
	}
}

func TestSharedBlockDependencyQueueDrainsOnlyAfterJoinedOwners(t *testing.T) {
	a := runTestApp(t)
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}, {Index: "inscriptions", Armed: true, indexRunOptions: indexRunOptions{ConventionalIDs: boolPointer(false)}}}}); err != nil {
		t.Fatal(err)
	}
	start := firstMainnetInscriptionHeight
	b := bitmapDevelopmentFixtureBlock(start, "", "parent")
	sharedProtocolHeader(t, a, &b)
	child := bitmapDevelopmentFixtureBlock(start+1, b.Hash, "child")
	sharedProtocolHeader(t, a, &child)
	x, err := extractInscriptionOccurrences(child)
	if err != nil {
		t.Fatal(err)
	}
	child.inscriptionExtraction = &x
	p := &indexRunPrepared{block: b, extraction: b.inscriptionExtraction}
	tx, err := openIndexStore(a.dataDir, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	tx.head.Mode = "lean"
	if err = tx.BeginLocatorBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tx.AbortLocatorBatch()
	ins, err := openIndexStore(a.dataDir, "inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	ins.head.Mode = "lean"
	ctx := context.WithValue(context.Background(), indexRunPreparedKey{}, p)
	ctx, drain := a.withDeferredBlockIngestion(ctx)
	var txCalls atomic.Int64
	queued, txDone := make(chan struct{}), make(chan struct{})
	ops := map[string]indexRunBlockConsumer{
		"tx-locator": func(ctx context.Context) error {
			txCalls.Add(1)
			<-queued
			tx.locatorContext = ctx
			err := tx.appendPreparedBlock(b, start, "ephemeral", nil)
			close(txDone)
			return err
		},
		"inscriptions": func(ctx context.Context) error {
			if rows := a.ingestResolvedVerifiedBlock(ctx, child); len(rows) != 0 {
				return fmt.Errorf("dependency did not queue: %v", rows)
			}
			if _, have, err := readBrowseTXPoint(context.Background(), a.dataDir, child.Height); err != nil || have {
				return fmt.Errorf("dependency published while parent owner active: %v/%v", have, err)
			}
			close(queued)
			return ins.appendPreparedBlock(b, start, "ephemeral", p.extraction)
		},
	}
	ctx = context.WithValue(ctx, indexRunBlockConsumersKey{}, ops)
	for _, row := range a.ingestArmedResolvedBlock(ctx, b) {
		if row.State == "error" || row.State == "unavailable" {
			t.Fatal(row)
		}
	}
	select {
	case <-txDone:
	default:
		t.Fatal("fanout returned before TX joined")
	}
	if _, have, err := readBrowseTXPoint(context.Background(), a.dataDir, child.Height); err != nil || have {
		t.Fatal("queue drained before explicit join boundary", have, err)
	}
	drain()
	if txCalls.Load() != 1 {
		t.Fatal("dependency inherited parent native callback", txCalls.Load())
	}
	if _, have, err := readBrowseTXPoint(context.Background(), a.dataDir, child.Height); err != nil || !have {
		t.Fatal("joined queue lost future point", have, err)
	}
	if point, have, err := a.browsedBatchContext(context.Background(), "inscriptions", child.Height, child.Hash); err != nil || !have || !inscriptionBatchInventoryComplete(point) {
		t.Fatal("dependency lost complete inventory", have, err)
	}
	q := ctx.Value(resolvedIngestionKey{}).(*resolvedIngestionQueue)
	if q.err() != nil || q.queuedBytes != 0 || !q.closed {
		t.Fatal("queue did not close cleanly", q.err(), q.queuedBytes)
	}
}
