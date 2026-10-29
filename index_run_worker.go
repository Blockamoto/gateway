package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Development protocols implement this adapter without borrowing another
// index's coverage or introducing another source-fetching loop.
type indexRunExternalConsumer interface {
	NextHeight() int64
	Reconcile(context.Context, func(int64) (string, error)) error
	Apply(context.Context, blockView, *inscriptionBlockResult) error
	Checkpoint() *indexCheckpoint
	Close()
}
type indexRunBlockSource interface {
	SetBlockSource(func(context.Context, int64) (blockView, error))
}
type indexRunOutcomeConsumer interface {
	ApplyRetainedOutcome(context.Context, int64) error
}
type indexRunRetentionConsumer interface{ ConfigureRetention(string) }
type indexRunSourceFunc func(context.Context, blockTarget, string) (blockView, error)
type indexRunConsumer struct {
	selection                indexRunSelection
	progress                 indexRunProgress
	store                    *indexStore
	utxo                     *utxoStore
	history                  *addressHistoryStore
	external                 indexRunExternalConsumer
	enrich                   bool
	retryAfter               time.Time
	reconciled               bool
	authority                chainAuthorityView
	locatorStats             indexLocatorWriteStats
	locatorGuardReleases     []func()
	pointAckFlushes          int64
	pointAckCommit           string
	parcelBusy               bool // coordinator-owned; external state is not read until the worker joins
	parcelBudget             *parcelRawScanBudget079
	parcelWorkBase           uint64
	parcelCancel             context.CancelFunc
	parcelPrepare            func(context.Context) error
	parcelPrepared           bool
	parcelEvidenceThrough    int64
	parcelEvidenceAnchorHash string
	parcelDiscovery          *parcelDiscoveryHead079
	parcelValidationKey      string
	parcelObservationKey     string
	parcelAnchorHeight       int64
	parcelAnchorTip          string
	parcelAnchorHash         string
	parcelSourceMissing      atomic.Bool
	parcelSourceRetryAt      time.Time
}

// A retry is scheduled only when a proof reader failed to obtain exact source
// bytes. The flag survives errors converted to unresolved proof descriptions.
type parcelSourceMissingKey079 struct{}

func noteParcelSourceMissing079(ctx context.Context) {
	if marker, ok := ctx.Value(parcelSourceMissingKey079{}).(*atomic.Bool); ok && marker != nil {
		marker.Store(true)
	}
}

type indexRunPreparedKey struct{}
type indexRunPublicationKey struct{}
type indexRunAddressProjectionKey struct{}
type indexRunSourceOwnershipKey struct{}
type indexRunCoordinatorSourceKey struct{}
type indexRunCohortOwnersKey struct{}
type indexRunBlockConsumersKey struct{}
type indexRunBlockConsumer func(context.Context) error
type parcelLaneResult struct {
	consumer *indexRunConsumer
	height   int64
	err      error
	elapsed  time.Duration
}

func launchParcelLane(done chan<- parcelLaneResult, c *indexRunConsumer, height int64, work func() error) {
	go func() {
		started := time.Now()
		err := work()
		done <- parcelLaneResult{consumer: c, height: height, err: err, elapsed: time.Since(started)}
	}()
}

func prepareParcelLane079(ctx context.Context, c *indexRunConsumer) error {
	if c.parcelPrepare == nil || c.parcelPrepared {
		return nil
	}
	if err := c.parcelPrepare(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("%w: IO Links prerequisite audit: %v", ErrBitmapEvidenceRequired, err)
	}
	c.parcelPrepared = true
	return nil
}

// The last head write checks current selected-chain and exact District state,
// even if either changed while immutable proof files were being written.
func (a *app) parcelGuardedPublication079(work context.Context, height int64, expectedHash, districtCommit string) context.Context {
	return context.WithValue(work, indexRunPublicationKey{}, func() error {
		if err := checkIndexRunPublication(work); err != nil {
			return err
		}
		selected, err := a.utxoSelectedTarget(work, a.currentChainAuthority(), height)
		if err != nil || selected.HashDisplay != expectedHash {
			return satPublicationAnchorError077{fmt.Errorf("selected Parcel block changed before publication")}
		}
		district, err := bitmapHeadAt(work, a.dataDir, bitmapStandardDistrictID, height)
		if err != nil || district == nil || district.BlockHash != expectedHash || district.Commitment != districtCommit || !district.LastBlock.BlockEvaluated || !district.LastBlock.CompleteScope {
			return fmt.Errorf("%w: selected District changed before Parcel publication", ErrBitmapEvidenceRequired)
		}
		return nil
	})
}

func parcelWaitsForDistrict079(err error) bool {
	if err == nil || !errors.Is(err, ErrBitmapEvidenceRequired) {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "District evaluation is required") || strings.Contains(message, "completed District evaluation is unavailable") || strings.Contains(message, "completed selected District evaluation is unavailable")
}

type indexRunSourceOwnership struct {
	RunID  string
	Height int64
	Hash   string
}

var errIndexRunConsumerStopped = errors.New("consumer is Off in the prepared timeline")
var errIndexRunAdmissionChanged = errors.New("the track's active admission changed before publication")

func checkIndexRunPublication(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if queue, ok := ctx.Value(resolvedIngestionKey{}).(*resolvedIngestionQueue); ok {
		if err := queue.err(); err != nil {
			return err
		}
	}
	if guard, ok := ctx.Value(indexRunPublicationKey{}).(func() error); ok {
		return guard()
	}
	return ctx.Err()
}

func checkIndexConsumerPublication(ctx context.Context, root, index string) error {
	if err := checkIndexRunPublication(ctx); err != nil {
		return err
	}
	if !indexRunOptionalWorkAllowed(root, index) {
		return errIndexRunConsumerStopped
	}
	return nil
}

func (a *app) indexRunConsumerContext(ctx context.Context, index string, admitted ...indexRunProgress) context.Context {
	return context.WithValue(ctx, indexRunPublicationKey{}, func() error {
		if bitmapParcelExecutionDeferred(index) {
			return errBitmapParcelsDeferred
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		d, err := readIndexRunDocument(a.dataDir)
		if err != nil {
			return err
		}
		if !d.Configured {
			live, err := readIndexLiveState(a.dataDir)
			if err != nil {
				return err
			}
			if live.Policies[index].Stopped || index == "address-state" && !legacyCurrentUTXOAllowed079(live) {
				return errIndexRunConsumerStopped
			}
			return nil
		}
		selection, ok := runSelection(indexRunEffectiveRequest(d), index)
		if !ok || !indexRunSelectionEnabled(selection) {
			return errIndexRunConsumerStopped
		}
		if index == bitmapReplayParcelID {
			if err := validateParcelSelection079(indexRunEffectiveRequest(d), selection); err != nil {
				return fmt.Errorf("%w: %v", errIndexRunConsumerStopped, err)
			}
		}
		if index == "address-state" {
			if err := validateCurrentSelection079(indexRunEffectiveRequest(d)); err != nil {
				return fmt.Errorf("%w: %v", errIndexRunConsumerStopped, err)
			}
		}
		if d.Run != nil {
			if d.Run.State != "running" && d.Run.State != "waiting" {
				return errIndexRunConsumerStopped
			}
			selection, ok = runSelection(d.Run.Request, index)
			if !ok || !indexRunSelectionEnabled(selection) {
				return errIndexRunConsumerStopped
			}
			if len(admitted) > 0 {
				current, exists := indexRunProgressFor(d.Run, index)
				if !exists || !sameIndexRunAdmission(current, admitted[0]) || current.AdmissionState == "blocked" {
					return errIndexRunAdmissionChanged
				}
			}
		}
		return nil
	})
}

type indexRunPrepared struct {
	factsMu             sync.Mutex
	prevoutsMu          sync.RWMutex
	extractionErr       error
	extractionAttempted bool
	cacheBytes          int
	rawProduced         bool
	block               blockView
	extraction          *inscriptionBlockResult
	extractionCounted   bool
	refs                map[string]map[string]outputFactRef
	prevouts            map[string]utxoCoin
	owner               indexRunSourceOwnership
}

func (a *app) ensurePreparedRawProducer(ctx context.Context, p *indexRunPrepared) error {
	ctx = preparedSourceContext(ctx, p)
	// A reused prepared body is another complete-source operation. New armed
	// tracks must see it even when Raw already owns its receipt or is now Off.
	if p.rawProduced {
		a.ingestResolvedVerifiedBlock(context.WithValue(ctx, indexRunPreparedKey{}, p), p.block)
		return ctx.Err()
	}
	produced, err := a.rawProducerReceiptContext(ctx, p.block)
	p.rawProduced = produced
	if err == nil {
		a.ingestResolvedVerifiedBlock(context.WithValue(ctx, indexRunPreparedKey{}, p), p.block)
	}
	return err
}
func preparedSourceContext(ctx context.Context, p *indexRunPrepared) context.Context {
	if parent := runPrepared(ctx); p.owner.RunID != "" && (parent == nil || parent.block.Hash == p.block.Hash) {
		return context.WithValue(ctx, indexRunSourceOwnershipKey{}, p.owner)
	}
	return ctx
}
func countPreparedExtraction(p *indexRunPrepared, counts *indexRunCounts) {
	if p.extraction != nil && !p.extractionCounted {
		counts.InscriptionExtractions++
		p.extractionCounted = true
	}
}
func (a *app) checkPreparedRawProducer(ctx context.Context, p *indexRunPrepared) error {
	return a.checkPreparedRawReceipt(ctx, p.rawProduced)
}
func (a *app) checkPreparedRawReceipt(ctx context.Context, produced bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !produced && timelineRawBlocksOn(a.dataDir) {
		return blockUnavailableError{fmt.Errorf("Raw was turned On during this cohort; waiting for its durable body receipt before derived publication")}
	}
	return nil
}

// Live changes future scheduling, not the build identity of blocks already
// admitted into a physical batch. Keep every publication fence except that
// scheduling flag: Armed, options, run, admission boundary, Out, Raw and anchor.
func indexRunLocatorBuildSelectionHash(selection indexRunSelection) string {
	selection.Live = false
	return indexDigest(selection)
}

func (a *app) indexRunLocatorBatchGuard(ctx context.Context, runID string, selection indexRunSelection, progress indexRunProgress, height int64, hash string, rawProduced bool) (context.Context, func()) {
	// This guard outlives Apply until a physical flush. Its context and closure
	// contain only run intent and anchor scalars, never prepared body/queue data.
	lean, cancel := context.WithCancel(context.Background())
	stop := context.AfterFunc(ctx, cancel)
	index, selectionHash := selection.Index, indexRunLocatorBuildSelectionHash(selection)
	hasBoundary, boundary := progress.EffectiveFrom != nil, int64(0)
	if hasBoundary {
		boundary = *progress.EffectiveFrom
	}
	guard := context.WithValue(lean, indexRunPublicationKey{}, func() error {
		if err := lean.Err(); err != nil {
			return err
		}
		d, err := readIndexRunDocument(a.dataDir)
		if err != nil {
			return err
		}
		if !d.Configured || d.Run == nil || d.Run.ID != runID || (d.Run.State != "running" && d.Run.State != "waiting") {
			return errIndexRunConsumerStopped
		}
		requested, ok := runSelection(d.Run.Request, index)
		if !ok || indexRunLocatorBuildSelectionHash(requested) != selectionHash {
			return errIndexRunAdmissionChanged
		}
		current, ok := indexRunProgressFor(d.Run, index)
		if !ok || current.AdmissionState == "blocked" || current.AdmissionState == "off" || (current.EffectiveFrom != nil) != hasBoundary || hasBoundary && *current.EffectiveFrom != boundary {
			return errIndexRunAdmissionChanged
		}
		if d.Run.Request.Out != nil && height > *d.Run.Request.Out {
			return errIndexRunAdmissionChanged
		}
		if raw, on := runSelection(d.Run.Request, "blocks"); !rawProduced && on && raw.Armed {
			return blockUnavailableError{fmt.Errorf("Raw was turned On during locator staging; durable body receipts are required before derived publication")}
		}
		target, err := a.utxoSelectedTarget(lean, a.currentChainAuthority(), height)
		if err != nil {
			return err
		}
		if target.HashDisplay != hash {
			return satPublicationAnchorError077{fmt.Errorf("selected block changed before locator batch publication")}
		}
		return nil
	})
	return guard, func() { stop(); cancel() }
}

func runPrepared(ctx context.Context) *indexRunPrepared {
	p, _ := ctx.Value(indexRunPreparedKey{}).(*indexRunPrepared)
	return p
}
func runRememberPrevout(ctx context.Context, tx, vin int, coin utxoCoin) {
	if p := runPrepared(ctx); p != nil {
		p.prevoutsMu.Lock()
		if p.prevouts == nil {
			p.prevouts = map[string]utxoCoin{}
		}
		p.prevouts[fmt.Sprintf("%d:%d", tx, vin)] = coin
		p.prevoutsMu.Unlock()
	}
}
func runPrevout(ctx context.Context, tx, vin int) (utxoCoin, bool) {
	if p := runPrepared(ctx); p != nil {
		p.prevoutsMu.RLock()
		c, ok := p.prevouts[fmt.Sprintf("%d:%d", tx, vin)]
		p.prevoutsMu.RUnlock()
		return c, ok
	}
	return utxoCoin{}, false
}

func runConsumerOrder(id string) int {
	if bitmapConsumerID(id) {
		switch bitmapConsumerKind(id) {
		case "district":
			return 5
		case "parcel":
			return 6
		case "chain":
			return 7
		}
	}
	switch id {
	case "blocks":
		return 0
	case "utxo-state":
		return 1
	case "tx-locator":
		return 2
	case "txo-spender":
		return 3
	case "inscriptions":
		return 4
	case "bitmap-districts":
		return 5
	case "bitmap-parcels":
		return 6
	case "bitmap-chain":
		return 7
	case "sat-state":
		return 8
	case "address-history":
		return 9
	case "address-state":
		return 10
	}
	return 20
}
func (a *app) openRunConsumer(ctx context.Context, r *indexRunRecord, p indexRunProgress) (*indexRunConsumer, error) {
	c, _ := runSelection(r.Request, p.Index)
	v := &indexRunConsumer{selection: c, progress: p}
	var err error
	if indexRunFactsOnly(c) {
		consumer := &sparseFactsRunConsumer{a: a, choice: c, from: p.From, next: p.From}
		if p.Height >= p.From && validHash(p.BlockHash) {
			consumer.next = p.Height + 1
			consumer.cp = &indexCheckpoint{Schema: 1, Definition: p.Index, Version: 1, From: p.From, Height: p.Height, BlockHash: p.BlockHash, Commitment: p.Commitment}
		}
		v.external = consumer
		return v, nil
	}
	if bitmapConsumerID(p.Index) {
		if err = checkIndexRunPublication(ctx); err != nil {
			return v, err
		}
	}
	if c.Rebuild {
		switch c.Index {
		case "utxo-state":
			err = resetUTXOForRebuild(ctx, a.dataDir, r.ID+":"+c.Index)
		case "sat-state":
			err = resetSatStateForRebuildContext(ctx, a.dataDir, r.ID+":"+c.Index)
		}
	}
	if err != nil {
		return nil, err
	}
	switch {
	case bitmapConsumerID(c.Index):
		v.external, err = openBitmapRunConsumer(ctx, a, c.Index)
		if err == nil && c.Index == bitmapReplayParcelID {
			if checkpoint := v.external.Checkpoint(); checkpoint != nil {
				height := checkpoint.Height
				v.progress.ParcelValidatedHeight, v.progress.ParcelValidatedHash = &height, checkpoint.BlockHash
			}
			if p.State == "complete" {
				break
			}
			if c.ParcelEvidenceRoute != "retained-raw" && c.ParcelEvidenceRoute != "tx-locators" && c.ParcelEvidenceRoute != "txio-links" {
				v.progress.ParcelValidationState = "pending"
				v.progress.ParcelValidationReason = "Select a proof route before Parcel processing."
				break
			}
			budget, budgetErr := openParcelRawBudget079(a.dataDir, r.ID, c.ParcelRawScanBudget, false)
			if budgetErr != nil {
				return v, fmt.Errorf("Parcel evidence work counter unavailable: %w", budgetErr)
			}
			v.parcelBudget = budget
			v.parcelWorkBase = r.Counts.ParcelEvidenceChecks
			through := p.To
			if r.ParcelDistrict != nil {
				through = r.ParcelDistrict.EvidenceThrough
			}
			if available, e := parcelAvailableEvidenceThrough079(c.ParcelEvidenceRoute, a.parcelEvidenceTimeline079(), p.To); e == nil && available > through {
				through = available
			}
			if err := a.configureParcelEvidence079(ctx, v, through); err != nil {
				v.progress.ParcelValidationState = "pending"
				v.progress.ParcelValidationReason = "Preparing explicitly selected evidence: " + err.Error()
			}
		}
		if configured, ok := v.external.(indexRunRetentionConsumer); ok {
			configured.ConfigureRetention(c.Retention)
		}
	case c.Index == "utxo-state":
		v.utxo, err = openUTXOStoreContext(ctx, a.dataDir)
	case c.Index == "address-history":
		v.history, err = openAddressHistoryStoreContext(ctx, a.dataDir)
	case c.Index == "address-state": // Its source is acquired only after the armed UTXO consumer commits.
	default:
		v.store, err = openIndexStoreContext(ctx, a.dataDir, c.Index)
		if err == nil {
			v.store.head.Mode = c.Mode
			v.store.retainSatHistory = c.RetainSatHistory
			v.store.retainOutputValues = c.RetainOutputValues != nil && *c.RetainOutputValues
			v.store.retainOutputScripts = c.RetainOutputScripts != nil && *c.RetainOutputScripts
			a.configureTXIOStore(v.store)
		}
	}
	return v, err
}
func (c *indexRunConsumer) checkpoint() *indexCheckpoint {
	if c.store != nil {
		return c.store.DurableCheckpoint()
	}
	return c.logicalCheckpoint()
}
func (c *indexRunConsumer) logicalCheckpoint() *indexCheckpoint {
	if c.external != nil {
		return c.external.Checkpoint()
	}
	if c.store != nil {
		return c.store.checkpoint
	}
	if c.utxo != nil && c.utxo.Head() != nil {
		d, _ := findIndexDefinition("utxo-state")
		return utxoIndexCheckpoint(c.utxo.Head(), d)
	}
	if c.history != nil {
		d, _ := findIndexDefinition("address-history")
		return addressHistoryIndexCheckpoint(c.history.head, d)
	}
	return nil
}
func (c *indexRunConsumer) next(target int64) int64 {
	c.enrich = false

	if c.external != nil {
		return c.external.NextHeight()
	}
	if c.history != nil {
		gaps := indexCoverageGaps(c.history.head.P2PKCoverage, c.progress.From, target)
		if len(gaps) > 0 {
			return gaps[0].From
		}
		return target + 1
	}
	if c.selection.Index == "address-state" {
		return -1
	}
	if c.store != nil && c.selection.Index == "txo-spender" && c.store.checkpoint != nil {
		gaps := c.store.txioEnrichmentRanges(c.progress.From, min(target, c.store.checkpoint.Height), c.store.retainOutputValues, c.store.retainOutputScripts)
		if len(gaps) > 0 {
			c.enrich = true
			return gaps[0].From
		}
	}
	if cp := c.logicalCheckpoint(); cp != nil {
		return cp.Height + 1
	}
	return c.progress.From
}

func sameIndexRunAdmission(a, b indexRunProgress) bool {
	if a.EffectiveFrom == nil || b.EffectiveFrom == nil {
		return a.EffectiveFrom == nil && b.EffectiveFrom == nil
	}
	return *a.EffectiveFrom == *b.EffectiveFrom
}
func (a *app) reconcileRunConsumer(ctx context.Context, c *indexRunConsumer, authority chainAuthorityView) error {
	if err := checkIndexRunPublication(ctx); err != nil {
		return err
	}
	if c.reconciled && c.authority.Source == authority.Source && c.authority.Height == authority.Height && c.authority.Hash == authority.Hash {
		return nil
	}
	if c.reconciled && c.selection.Index == bitmapReplayParcelID && c.parcelPrepare != nil && c.parcelPrepared {
		// Extending the chain leaves an already checked evidence prefix valid.
		// Invalidate only if its selected endpoint has actually changed.
		anchor, err := a.utxoSelectedTarget(ctx, authority, c.parcelEvidenceThrough)
		if err != nil || c.parcelEvidenceAnchorHash == "" || anchor.HashDisplay != c.parcelEvidenceAnchorHash {
			c.parcelPrepared = false
		}
	}
	if c.store != nil && c.selection.Index == "tx-locator" {
		if pending, _, _ := c.store.LocatorBatchStats(); pending > 0 {
			cp := c.logicalCheckpoint()
			selected, err := a.addressSelectedHashes(ctx, authority)(cp.Height)
			if err == nil && selected == cp.BlockHash {
				// An extending authority does not require walking unflushed files.
				// Every staged anchor is checked again by the flush publication fence.
				c.reconciled, c.authority = true, authority
				return nil
			}
			c.abortLocatorBatch()
		}
	}
	err := a.reconcileRunConsumerUncached(ctx, c, authority)
	if err == nil && c.store != nil && c.selection.Index == "tx-locator" {
		err = c.store.BeginLocatorBatch(ctx)
	}
	if err == nil {
		c.reconciled, c.authority = true, authority
	}
	return err
}
func (c *indexRunConsumer) abortLocatorBatch() {
	if c.store == nil {
		return
	}
	n, _, _ := c.store.LocatorBatchStats()
	priorHeight := c.progress.From - 1
	if cp := c.checkpoint(); cp != nil {
		priorHeight = cp.Height
	}
	c.progress.AddedBlocks -= c.stagedAddedBlocks()
	c.store.AbortLocatorBatch()
	c.releaseLocatorGuards()
	if cp := c.checkpoint(); cp != nil {
		c.progress.AddedBlocks += max(int64(0), cp.Height-priorHeight)
		c.progress.Height, c.progress.BlockHash, c.progress.Commitment = cp.Height, cp.BlockHash, cp.Commitment
	} else if n > 0 {
		c.progress.Height, c.progress.BlockHash, c.progress.Commitment = c.progress.From-1, "", ""
	}
}
func (c *indexRunConsumer) releaseLocatorGuards() {
	for _, release := range c.locatorGuardReleases {
		release()
	}
	c.locatorGuardReleases = nil
}
func (c *indexRunConsumer) stagedAddedBlocks() int64 {
	if c.store == nil {
		return 0
	}
	n, _, _ := c.store.LocatorBatchStats()
	durableHeight := c.progress.From - 1
	if cp := c.checkpoint(); cp != nil {
		durableHeight = cp.Height
	}
	// A failed automatic flush can leave the rejected last block staged.
	// Only previously successful logical Applies contributed to AddedBlocks.
	return min(int64(n), max(int64(0), c.progress.Height-durableHeight))
}
func indexRunLogicalBatchWindow(from, height int64) (int64, int64) {
	start := height / indexLocatorBatchBlocks * indexLocatorBatchBlocks
	return max(from, start), start + indexLocatorBatchBlocks - 1
}
func countLocatorWrites(c *indexRunConsumer, counts *indexRunCounts) {
	if c.store == nil {
		return
	}
	n := c.store.LocatorWriteStats()
	old := c.locatorStats
	counts.LocatorBatchFlushes += n.BatchFlushes - old.BatchFlushes
	counts.LocatorShardAppends += n.ShardAppends - old.ShardAppends
	counts.LocatorShardSyncs += n.ShardSyncs - old.ShardSyncs
	counts.LocatorReceiptWrites += n.ReceiptWrites - old.ReceiptWrites
	counts.LocatorActiveWrites += n.ActiveWrites - old.ActiveWrites
	counts.LocatorHeadWrites += n.HeadWrites - old.HeadWrites
	counts.LocatorCommitWrites += n.CommitWrites - old.CommitWrites
	counts.LocatorRecordsWritten += n.RecordsWritten - old.RecordsWritten
	counts.LocatorBytesWritten += n.BytesWritten - old.BytesWritten
	counts.StorageFlushMilliseconds += (n.FlushNanoseconds - old.FlushNanoseconds) / int64(time.Millisecond)
	c.locatorStats = n
	if pending, _, _ := c.store.LocatorBatchStats(); pending == 0 {
		c.releaseLocatorGuards()
	}
}
func (a *app) reconcileRunConsumerUncached(ctx context.Context, c *indexRunConsumer, authority chainAuthorityView) error {
	hashAt := a.addressSelectedHashes(ctx, authority)
	if c.external != nil {
		return c.external.Reconcile(ctx, hashAt)
	}
	if c.utxo != nil {
		err := c.utxo.ReconcileSelected(ctx, authority.Height, func(ctx context.Context, h int64) (string, error) { return hashAt(h) })
		if optionalAddressProjectionError(err) {
			return nil
		}
		return err
	}
	if c.history != nil {
		return c.history.ReconcileContext(ctx, hashAt)
	}
	if c.store != nil {
		c.store.locatorContext = ctx
		if c.selection.Index == "sat-state" {
			return c.store.reconcileSatStateSelected(ctx, authority.Height, hashAt)
		}
		return c.store.reconcile(hashAt)
	}
	return nil
}
func (a *app) applyRunConsumer(ctx context.Context, c *indexRunConsumer, prepared *indexRunPrepared, authority chainAuthorityView, retention string) error {
	ctx, drain := a.withDeferredBlockIngestion(ctx)
	defer drain()
	b := prepared.block
	hashAt := a.addressSelectedHashes(ctx, authority)
	if c.external != nil {
		if refresh, ok := c.external.(interface {
			RefreshForBlock(context.Context, blockView) (bool, error)
		}); ok {
			already, err := refresh.RefreshForBlock(ctx, b)
			if err != nil || already {
				return err
			}
		}
		return c.external.Apply(ctx, b, prepared.extraction)
	}
	if c.utxo != nil {
		latest, err := openUTXOStoreContext(ctx, a.dataDir)
		if err != nil {
			return err
		}
		c.utxo = latest
		if h := latest.Head(); h != nil && h.Height >= b.Height {
			selected, err := hashAt(h.Height)
			if err != nil {
				return err
			}
			if selected != h.BlockHash {
				return fmt.Errorf("UTXO source changed before cohort publication")
			}
			return nil
		}
		err = c.utxo.ApplyBlock(ctx, b)
		if optionalAddressProjectionError(err) {
			return nil
		}
		return err
	}
	if c.history != nil {
		latest, err := openAddressHistoryStoreContext(ctx, a.dataDir)
		if err != nil {
			return err
		}
		c.history = latest
		var saved addressHistoryBlock
		if have, err := latest.tree("blocks", latest.head.BlocksRoot, ctx).get(addressHistoryHeightKey(b.Height), &saved); err != nil {
			return err
		} else if have && saved.BlockHash == b.Hash && saved.DestinationVersion == 1 {
			return nil
		}
		return latest.CommitBlockContext(ctx, b, hashAt)
	}
	s := c.store
	s.locatorContext = ctx
	if c.selection.Index == "tx-locator" {
		// The staged flush guard is already independent of this active body.
		defer func() { s.locatorContext = nil }()
	}
	if c.enrich {
		if err := s.enrichTXIOBlockContext(ctx, b, s.retainOutputValues, s.retainOutputScripts); err != nil {
			return err
		}
		return s.recordTXIOEnrichmentCoverage(b.Height, b.Hash, s.retainOutputValues, s.retainOutputScripts)
	}
	if c.selection.Index == "sat-state" {
		latest, err := indexStoreHead(a.dataDir, "sat-state")
		if err != nil {
			return err
		}
		latest.locatorContext = ctx
		latest.head.Mode = "lean"
		latest.retainSatHistory = c.selection.RetainSatHistory
		c.store = latest
		s = latest
		if h := s.checkpoint; h != nil && h.Height >= b.Height {
			selected, err := hashAt(h.Height)
			if err != nil {
				return err
			}
			if selected != h.BlockHash {
				return fmt.Errorf("Sats source changed before cohort publication")
			}
			return nil
		}
		return appendSatSelectedBlock077(ctx, s, b, 0, retention, prepared.extraction, hashAt)
	}
	from := c.progress.From
	if c.selection.Index == "inscriptions" {
		if err := requireInscriptionIngestionRange(s.definition.Network, b.Height, from); err != nil {
			return err
		}
	}
	if s.checkpoint != nil {
		from = s.checkpoint.From
	}
	if err := s.appendPreparedBlock(b, from, retention, prepared.extraction); err != nil {
		return err
	}
	if c.selection.Index == "inscriptions" && inscriptionIDsEnabled(c.selection.ConventionalIDs) {
		related, err := openIndexStoreContext(ctx, a.dataDir, inscriptionLocatorIndex)
		if err != nil {
			return err
		}
		source, ok, err := a.committedInscriptionBatch(ctx, b.Height, b.Hash)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("the inscription result did not commit")
		}
		return related.appendInscriptionLocators(source, &b, from, retention)
	}
	return nil
}
func runTerminal(err error) bool {
	return satTerminalError077(err) || errors.Is(err, ErrUTXORebuildRequired) || errors.Is(err, ErrUTXOStateCorrupt) || errors.Is(err, ErrAddressStateCorrupt)
}
func (a *app) saveRunProgress(id, state, reason string, consumers []*indexRunConsumer, counts indexRunCounts, target int64, hash string) error {
	a.indexRunMu.Lock()
	defer a.indexRunMu.Unlock()
	r := a.indexRunState.Run
	if r == nil || r.ID != id {
		return fmt.Errorf("run was replaced")
	}
	before := *r
	before.Updated = ""
	beforeDigest := indexDigest(before)
	if r.State != "pausing" && r.State != "cancelled" {
		r.State, r.Reason = state, reason
	}
	for _, c := range consumers {
		if c.selection.Index == bitmapReplayParcelID && c.parcelBudget != nil {
			work := c.parcelBudget.count()
			if ^uint64(0)-c.parcelWorkBase < work {
				counts.ParcelEvidenceChecks = ^uint64(0)
			} else {
				counts.ParcelEvidenceChecks = c.parcelWorkBase + work
			}
		}
	}
	r.Counts = counts
	r.TargetHeight, r.TargetHash = target, hash
	// A click may have durably admitted a new member while this cohort was
	// applying. Progress must never erase that pending intent or an Off fence.
	saved := append([]indexRunProgress(nil), r.Consumers...)
	r.Consumers = nil
	seen := map[string]bool{}
	for _, c := range consumers {
		p := c.progress
		p.StagedHeight, p.LogicalBatchFrom, p.LogicalBatchTo = nil, nil, nil
		p.StagedBlocks, p.StagedBytes, p.StagedRecords = 0, 0, 0
		if c.store != nil && c.selection.Index == "tx-locator" {
			p.StagedBlocks, p.StagedBytes, p.StagedRecords = c.store.LocatorBatchStats()
			if p.StagedBlocks > 0 {
				h := c.logicalCheckpoint().Height
				from, to := indexRunLogicalBatchWindow(p.From, h)
				p.StagedHeight, p.LogicalBatchFrom, p.LogicalBatchTo = &h, &from, &to
				p.AddedBlocks -= c.stagedAddedBlocks()
				p.Height, p.BlockHash, p.Commitment = p.From-1, "", ""
			}
		}
		for _, pending := range saved {
			if pending.Index == p.Index && (!sameIndexRunAdmission(pending, p) || pending.AdmissionState == "blocked" && p.AdmissionState != "blocked") {
				p = pending
			}
		}
		if !c.parcelBusy {
			if cp := c.checkpoint(); cp != nil {
				p.Height, p.BlockHash, p.Commitment = cp.Height, cp.BlockHash, cp.Commitment
				if c.selection.Index == bitmapReplayParcelID {
					height := cp.Height
					p.ParcelValidatedHeight, p.ParcelValidatedHash = &height, cp.BlockHash
				}
			} else if c.selection.Index == bitmapReplayParcelID {
				p.ParcelValidatedHeight, p.ParcelValidatedHash = nil, ""
			}

		}
		if selection, ok := runSelection(r.Request, p.Index); !ok || !indexRunSelectionEnabled(selection) {
			p.State, p.AdmissionState, p.Reason = "off", "off", "Off; committed records are retained."
		}
		r.Consumers = append(r.Consumers, p)
		seen[p.Index] = true
	}
	for _, p := range saved {
		if !seen[p.Index] {
			r.Consumers = append(r.Consumers, p)
		}
	}
	after := *r
	after.Updated = ""
	if indexDigest(after) == beforeDigest {
		return nil
	}
	r.Updated = time.Now().UTC().Format(time.RFC3339)
	return a.persistIndexRunLocked()
}

func (a *app) runCoordinatedIndexes(ctx context.Context, id string) {
	defer func() { a.indexMu.Lock(); a.indexRunActiveID = ""; a.indexMu.Unlock(); a.indexBuildFinished() }()
	d := a.indexRunSnapshot()
	if d.Run == nil || d.Run.ID != id {
		return
	}
	record := d.Run
	pointSession := newIndexRunPointSession(a, ctx, id, d)
	baseRunCtx := ctx
	ctx = pointSession.context(baseRunCtx)
	defer func() { pointSession.session.Abort() }()
	consumers := []*indexRunConsumer{}
	counts := record.Counts
	for _, p := range record.Consumers {
		if p.AdmissionState == "blocked" {
			selection, _ := runSelection(record.Request, p.Index)
			consumers = append(consumers, &indexRunConsumer{selection: selection, progress: p})
			continue
		}
		c, err := a.openRunConsumer(a.indexRunBoundConsumerContext(ctx, p), record, p)
		if c == nil {
			s, _ := runSelection(record.Request, p.Index)
			c = &indexRunConsumer{selection: s, progress: p}
		}
		if err != nil {
			c.progress.State = "failed"
			if runTerminal(err) {
				c.progress.State = "rebuild_required"
			}
			c.progress.Reason = err.Error()
		}
		consumers = append(consumers, c)
	}
	// Small stable consumer ordering establishes same-height dependency order.
	for i := 1; i < len(consumers); i++ {
		for j := i; j > 0 && runConsumerOrder(consumers[j].selection.Index) < runConsumerOrder(consumers[j-1].selection.Index); j-- {
			consumers[j], consumers[j-1] = consumers[j-1], consumers[j]
		}
	}
	defer func() {
		for _, c := range consumers {
			c.abortLocatorBatch()
			if c.external != nil {
				c.external.Close()
			}
		}
	}()
	parcelCtx, cancelParcel := context.WithCancel(baseRunCtx)
	parcelDone := make(chan parcelLaneResult, 1)
	parcelActive := false
	defer func() {
		cancelParcel()
		if parcelActive {
			<-parcelDone
		}
	}()
	consumeParcel := func(result parcelLaneResult) {
		parcelActive = false
		c := result.consumer
		c.parcelBusy = false
		c.parcelCancel = nil
		counts.ApplyMilliseconds += result.elapsed.Milliseconds()
		if result.err == nil {
			c.parcelSourceMissing.Store(false)
			c.parcelSourceRetryAt = time.Time{}
			c.progress.Height = result.height
			c.progress.AddedBlocks++
			height := result.height
			c.progress.ParcelValidatedHeight = &height
			if cp := c.logicalCheckpoint(); cp != nil {
				c.progress.ParcelValidatedHash = cp.BlockHash
			}
			c.progress.ParcelValidationState, c.progress.ParcelValidationReason = "ready", ""
			c.parcelValidationKey = ""
			return
		}
		c.progress.ParcelValidationState, c.progress.ParcelValidationReason = "pending", result.err.Error()
		if c.parcelSourceMissing.Swap(false) && !errors.Is(result.err, context.Canceled) {
			delay := 30 * time.Second
			if a.parcelEvidenceHooks079 != nil && a.parcelEvidenceHooks079.RetryDelay > 0 {
				delay = a.parcelEvidenceHooks079.RetryDelay
			}
			c.parcelSourceRetryAt = time.Now().Add(delay)
		}
		if errors.Is(result.err, context.Canceled) {
			c.progress.ParcelValidationReason = "Validation paused at its last committed checkpoint."
		}
	}
	target, targetHash := record.TargetHeight, record.TargetHash
	queueParcelValidation := func(c *indexRunConsumer, horizon int64, authority chainAuthorityView) bool {
		if c.selection.Index != bitmapReplayParcelID || c.parcelBusy || c.external == nil || !c.reconciled || c.progress.AdmissionState == "blocked" {
			return false
		}
		next := c.external.NextHeight()
		if next > horizon {
			c.progress.ParcelValidationState, c.progress.ParcelValidationReason = "complete", "Validated coverage reached the selected boundary."
			return false
		}
		if c.selection.ParcelEvidenceRoute == "" {
			c.progress.ParcelValidationState, c.progress.ParcelValidationReason = "blocked", "A selected proof route is required."
			return false
		}
		if c.parcelAnchorHeight != next || c.parcelAnchorTip != authority.Hash || c.parcelAnchorHash == "" {
			selected, err := a.utxoSelectedTarget(ctx, authority, next)
			if err != nil {
				c.progress.ParcelValidationState, c.progress.ParcelValidationReason = "pending", "Selected-chain evidence is unavailable for the next validation block."
				return false
			}
			c.parcelAnchorHeight, c.parcelAnchorTip, c.parcelAnchorHash = next, authority.Hash, selected.HashDisplay
		}
		if !c.parcelSourceRetryAt.IsZero() && !time.Now().Before(c.parcelSourceRetryAt) {
			c.parcelSourceRetryAt = time.Time{}
			c.parcelObservationKey, c.parcelValidationKey = "", ""
		}
		observation := indexDigest(struct {
			Height               int64
			Route, SelectedHash  string
			Epoch, DistrictEpoch uint64
		}{next, c.selection.ParcelEvidenceRoute, c.parcelAnchorHash, parcelEvidenceEpoch079(a.dataDir, c.selection.ParcelEvidenceRoute), parcelDistrictEvidenceEpoch079(a.dataDir, next)})
		if observation == c.parcelObservationKey {
			return false
		}
		c.parcelObservationKey = observation
		currentDistrict := ""
		if district, e := bitmapHeadAt(ctx, a.dataDir, bitmapStandardDistrictID, next); e == nil && district != nil && district.BlockHash == c.parcelAnchorHash && district.LastBlock.BlockEvaluated && district.LastBlock.CompleteScope {
			currentDistrict = district.Commitment
		}
		needed := next

		timeline := a.parcelEvidenceTimeline079()
		gaps, evidenceErr := parcelEvidenceGaps079(c.selection.ParcelEvidenceRoute, timeline, needed)
		fingerprint := indexDigest(struct {
			Height, Through                                    int64
			Hash, District, Route, SelectedHash, EvidenceError string
			Gaps                                               []heightInterval
		}{next, needed, c.parcelAnchorHash, currentDistrict, c.selection.ParcelEvidenceRoute, c.parcelAnchorHash, fmt.Sprint(evidenceErr), gaps})
		if fingerprint == c.parcelValidationKey {
			return false
		}
		c.parcelValidationKey = fingerprint
		if currentDistrict == "" {
			c.progress.ParcelValidationState, c.progress.ParcelValidationReason = "pending", "Waiting for the exact selected complete District outcome; no proof work was started."
			return false
		}

		if evidenceErr != nil || len(gaps) != 0 {
			c.progress.ParcelValidationState, c.progress.ParcelValidationReason = "pending", fmt.Sprintf("Preparing selected evidence through block %d; no proof scan started.", needed)
			if evidenceErr != nil {
				c.progress.ParcelValidationReason += " " + evidenceErr.Error()
			}
			return false
		}
		available, err := parcelAvailableEvidenceThrough079(c.selection.ParcelEvidenceRoute, timeline, horizon)
		if err != nil || available < needed {
			return false
		}
		if c.parcelEvidenceThrough < needed || c.external.(*bitmapRunConsumer).blockAt == nil {
			through := available

			if err = a.configureParcelEvidence079(ctx, c, through); err != nil {
				c.progress.ParcelValidationState, c.progress.ParcelValidationReason = "pending", err.Error()
				return false
			}
		}
		c.parcelBusy, parcelActive = true, true
		c.progress.ParcelValidationState, c.progress.ParcelValidationReason = "validating", "Checking the next Parcel block against the selected proof route."
		progress, expectedHash := c.progress, c.parcelAnchorHash
		retention := runRawRetention(record.Request)
		workCtx, workCancel := context.WithCancel(parcelCtx)
		c.parcelCancel = workCancel
		launchParcelLane(parcelDone, c, next, func() error {
			defer workCancel()
			work := a.indexRunBoundConsumerContext(workCtx, progress)
			work = context.WithValue(work, parcelSourceMissingKey079{}, &c.parcelSourceMissing)
			if err := prepareParcelLane079(work, c); err != nil {
				return err
			}
			consumer := c.external.(*bitmapRunConsumer)
			block, err := consumer.blockAt(work, next)
			if err != nil {
				return err
			}
			if block.Hash != expectedHash {
				return fmt.Errorf("selected block changed before Parcel validation")
			}
			district, err := bitmapHeadAt(work, a.dataDir, bitmapStandardDistrictID, next)
			if err != nil || district == nil || district.BlockHash != block.Hash || district.Commitment != currentDistrict || !district.LastBlock.BlockEvaluated || !district.LastBlock.CompleteScope {
				return fmt.Errorf("complete District outcome changed before Parcel validation")
			}
			guarded := a.parcelGuardedPublication079(work, next, expectedHash, currentDistrict)
			prepared := &indexRunPrepared{block: block}
			return a.applyRunConsumer(guarded, c, prepared, authority, retention)
		})
		return true
	}
	finish := func(state, reason string) {
		// A successful in-flight publication must be reflected in final status.
		// Cancel first so pause/cancel never waits for additional evidence work.
		if parcelActive {
			if state != "complete" {
				cancelParcel()
			}
			result := <-parcelDone
			consumeParcel(result)
			if state == "complete" && result.err != nil {
				reason = "Parcel validation remains incomplete: " + result.err.Error()
			}
		}
		stopIntent := func() bool {
			current := a.indexRunSnapshot().Run
			if current != nil && current.ID == id && (current.State == "pausing" || current.State == "paused" || current.State == "cancelled") {
				state, reason = "paused", current.Reason
				return true
			}
			if ctx.Err() != nil {
				state, reason = "paused", "Paused; committed records are retained."
				return true
			}
			return false
		}
		stopIntent()
		var pointFlushErr error
		if state == "complete" && ctx.Err() == nil {
			pointFlushErr = pointSession.session.Flush(ctx)
			if errors.Is(pointFlushErr, errBrowseTXPointInvalidated) || browseTXPointExpectedInvalidation(pointFlushErr) || errors.Is(pointFlushErr, context.Canceled) {
				pointSession.session.Abort()
				pointFlushErr = nil
			}
			stopIntent()
		} else {
			pointSession.session.Abort()
		}
		for _, c := range consumers {
			if c.store == nil || c.selection.Index != "tx-locator" {
				continue
			}
			if state == "complete" && ctx.Err() == nil {
				if err := c.store.FlushLocatorBatch(ctx); err != nil {
					if stopIntent() {
						c.progress.State, c.progress.Reason = "paused", reason
					} else {
						state, reason = "complete_with_errors", "Locator staging was discarded; durable prefix retained: "+err.Error()
						c.progress.State, c.progress.Reason = "failed", reason
					}
					c.abortLocatorBatch()
				} else {
					a.noteRunLocatorPublication(ctx, id, c, true)
				}
			} else {
				c.abortLocatorBatch()
			}
			countLocatorWrites(c, &counts)
		}
		if state == "paused" {
			memberState := "paused"
			if current := a.indexRunSnapshot().Run; current != nil && current.ID == id && current.State == "cancelled" {
				memberState = "cancelled"
			}
			for _, c := range consumers {
				if c.progress.State == "running" || c.progress.State == "waiting" || c.progress.State == "ready" || c.progress.State == "pending" || memberState == "cancelled" && c.progress.State == "paused" {
					c.progress.State, c.progress.Reason = memberState, reason
				}
			}
		}
		if state == "complete" && ctx.Err() == nil {
			for _, c := range consumers {
				var err error
				if c.utxo != nil {
					err = c.utxo.CollectPages(ctx)
				}
				if c.store != nil && c.selection.Index == "sat-state" {
					err = c.store.collectOrdinalState()
				}
				if err != nil && !errors.Is(err, errOrdinalMaintenancePending) {
					reason = "Committed state retained; reclamation is deferred: " + err.Error()
				}
			}
			if err := collectSharedOutputFactsContext(ctx, a.dataDir); err != nil {
				reason = "Committed state retained; shared reclamation is deferred: " + err.Error()
			}
		}
		if pointFlushErr != nil {
			if state == "complete_with_errors" && reason != "" {
				reason += "; future TX point batch publication failed: " + pointFlushErr.Error()
			} else {
				reason = "Independent native checkpoints are retained; future TX point batch publication failed: " + pointFlushErr.Error()
			}
			if state != "paused" {
				state = "complete_with_errors"
			}
		}
		stopIntent()
		if state == "complete" {
			if current := a.indexRunSnapshot().Run; current != nil && current.ID == id && len(current.SourceFactErrors) > 0 {
				state, reason = "complete_with_errors", "Selected checkpoints are retained; some armed source-fact owners could not publish. See source_fact_errors for bounded anchors and details."
			}
		}
		if err := a.saveRunProgress(id, state, reason, consumers, counts, target, targetHash); err != nil {
			a.indexRunMu.Lock()
			if r := a.indexRunState.Run; r != nil && r.ID == id {
				r.State = "failed"
				r.Reason = "Run status could not be saved: " + err.Error() + ". Published index checkpoints remain authoritative."
			}
			a.indexRunMu.Unlock()
			return
		}
		a.indexRunMu.Lock()
		if r := a.indexRunState.Run; r != nil && r.ID == id && r.State == "pausing" {
			r.State = "paused"
			_ = a.persistIndexRunLocked()
		}
		a.indexRunMu.Unlock()
	}
	cache := map[string]*indexRunPrepared{}
	order := []string{}
	cacheBytes := 0
	source := a.indexSourceBlock
	if a.indexRunSourceBlock != nil {
		source = a.indexRunSourceBlock
	}
	ownedSource := func(callCtx context.Context, target blockTarget, retention string) (blockView, error) {
		// Dependency reads carry their parent prepared body. Only the actual
		// scheduled source is owned by this coordinator's durable adapters.
		if p := runPrepared(callCtx); p == nil || p.block.Hash == target.HashDisplay {
			callCtx = context.WithValue(callCtx, indexRunSourceOwnershipKey{}, indexRunSourceOwnership{id, target.Height, target.HashDisplay})
			callCtx = context.WithValue(callCtx, indexRunCoordinatorSourceKey{}, true)
		}
		return source(callCtx, target, retention)
	}
	prefetch := newIndexRunPrefetch(ctx, func(callCtx context.Context, target blockTarget) (blockView, error) {
		return ownedSource(callCtx, target, "ephemeral")
	})
	defer prefetch.Close()
	peerWindow := &indexRunDownloadWindow{a: a, ctx: ctx}
	defer peerWindow.Reset()
	admissionRevision := effectiveIndexRunAdmissionRevision(record)
	var loadMu sync.Mutex
	var load func(context.Context, int64) (*indexRunPrepared, error)
	load = func(callCtx context.Context, h int64) (*indexRunPrepared, error) {
		loadMu.Lock()
		defer loadMu.Unlock()
		authority := a.currentChainAuthority()
		t, err := a.utxoSelectedTarget(callCtx, authority, h)
		if err != nil {
			return nil, err
		}
		if parent := runPrepared(callCtx); parent == nil || parent.block.Hash == t.HashDisplay {
			callCtx = context.WithValue(callCtx, indexRunSourceOwnershipKey{}, indexRunSourceOwnership{id, t.Height, t.HashDisplay})
			callCtx = context.WithValue(callCtx, indexRunCoordinatorSourceKey{}, true)
		}
		// A dependency requesting the active cohort's own body reads its
		// frozen facts directly. Do not mutate owner/raw/count state while
		// the TX lane's publication guard is reading that same prepared body.
		if parent := runPrepared(callCtx); parent != nil && parent.block.Height == h && parent.block.Hash == t.HashDisplay {
			counts.CacheHits++
			return parent, nil
		}
		if p := cache[t.HashDisplay]; p != nil {
			if parent := runPrepared(callCtx); parent == nil || parent.block.Hash == p.block.Hash {
				p.owner = indexRunSourceOwnership{id, p.block.Height, p.block.Hash}
			}
			if err := a.ensurePreparedRawProducer(callCtx, p); err != nil {
				return nil, err
			}
			countPreparedExtraction(p, &counts)
			counts.CacheHits++
			return p, nil
		}
		// Shared acquisition may retain sparse verified Raw body membership
		// while global Raw is On. Only the Raw consumer's Apply advances its
		// sequential checkpoint; this compatibility policy cannot override On.
		retention := "ephemeral"
		started := time.Now()
		b, elapsed, queued, err := prefetch.Take(callCtx, t)
		if !queued {
			// Another track can win this turn at a different frontier. Keep
			// the single already acquired upcoming body for its later owner;
			// discarding it here would reacquire/decode it on that next turn.
			b, err = ownedSource(callCtx, t, retention)
			elapsed = time.Since(started)
		}
		counts.SourceMilliseconds += elapsed.Milliseconds()
		counts.SourceAcquireMilliseconds += b.sourceTiming.acquireNanoseconds / int64(time.Millisecond)
		counts.SourceDecodeMilliseconds += b.sourceTiming.decodeNanoseconds / int64(time.Millisecond)
		counts.SourceRawRetentionMilliseconds += b.sourceTiming.rawNanoseconds / int64(time.Millisecond)
		counts.SourceIncidentalMilliseconds += b.sourceTiming.incidentalNanoseconds / int64(time.Millisecond)
		if err != nil && !integrityVerified(b) {
			return nil, err
		}
		counts.SourceReads++
		counts.DecodedBlocks++
		if b.Height != h || b.Hash != t.HashDisplay || !integrityVerified(b) {
			return nil, fmt.Errorf("source block does not match the selected target")
		}
		p := &indexRunPrepared{block: b, extraction: b.inscriptionExtraction, refs: map[string]map[string]outputFactRef{}, prevouts: map[string]utxoCoin{}}
		if parent := runPrepared(callCtx); parent == nil || parent.block.Hash == b.Hash {
			p.owner = indexRunSourceOwnership{id, b.Height, b.Hash}
		}
		countPreparedExtraction(p, &counts)
		// Bound the cache by decoded JSON as well as entry count. An oversized
		// block still serves its ready consumers without being retained here.
		encoded, _ := json.Marshal(b)
		size := len(encoded) + len(b.verifiedRaw)
		p.cacheBytes = size
		if size <= 32<<20 {
			for len(order) > 0 && (len(order) >= 8 || cacheBytes+size > 32<<20) {
				old := order[0]
				order = order[1:]
				cacheBytes -= cache[old].cacheBytes
				delete(cache, old)
			}
			cache[b.Hash] = p
			order = append(order, b.Hash)
			cacheBytes += size
		}
		if err != nil {
			return nil, err
		}
		if parent := runPrepared(callCtx); parent != nil && parent.block.Hash != b.Hash {
			// A dependency may consume the speculative main-source slot. Replay
			// its fanout under the dependency context, without coordinator ownership.
			if err := a.ensurePreparedRawProducer(callCtx, p); err != nil {
				return nil, err
			}
		}
		return p, nil
	}
	for _, c := range consumers {
		if source, ok := c.external.(indexRunBlockSource); ok && c.selection.Index != bitmapReplayParcelID {
			source.SetBlockSource(func(callCtx context.Context, h int64) (blockView, error) {
				p, err := load(callCtx, h)
				if err != nil {
					return blockView{}, err
				}
				return p.block, nil
			})
		}
	}
	processed := int64(0)
	for {
		select {
		case result := <-parcelDone:
			consumeParcel(result)
		default:
		}
		if ctx.Err() != nil {
			finish("paused", "Paused; resume continues from each committed checkpoint.")
			return
		}
		d = a.indexRunSnapshot()
		if d.Run == nil || d.Run.ID != id {
			return
		}
		record = d.Run
		if revision := effectiveIndexRunAdmissionRevision(record); revision != admissionRevision {
			prefetch.Reset()
			peerWindow.Reset()
			admissionRevision = revision
		}
		if record.State == "cancelled" || record.State == "pausing" {
			finish("paused", record.Reason)
			return
		}
		pointSession.refresh(d)
		ctx = pointSession.context(baseRunCtx)
		// Admit new members only between complete cohorts. A later Off/Stop is
		// still enforced by each consumer's final publication guard.
		for _, p := range record.Consumers {
			var existing *indexRunConsumer
			var position int
			for j, c := range consumers {
				if c.selection.Index == p.Index {
					existing, position = c, j
					break
				}
			}
			if existing != nil && existing.parcelBusy {
				continue
			}
			if existing != nil && sameIndexRunAdmission(existing.progress, p) && existing.progress.AdmissionState != "blocked" && p.AdmissionState != "pending" && p.AdmissionState != "blocked" {
				continue
			}
			if existing != nil && p.AdmissionState == "blocked" {
				existing.progress = p
				continue
			}
			if existing != nil && sameIndexRunAdmission(existing.progress, p) && existing.progress.AdmissionState == "admitted" && p.AdmissionState == "pending" {
				continue
			}
			selection, _ := runSelection(record.Request, p.Index)
			if existing != nil && existing.external != nil {
				existing.external.Close()
			}
			if existing != nil {
				existing.abortLocatorBatch()
				countLocatorWrites(existing, &counts)
			}
			c := &indexRunConsumer{selection: selection, progress: p}
			if p.AdmissionState != "blocked" && indexRunSelectionEnabled(selection) {
				opened, err := a.openRunConsumer(a.indexRunBoundConsumerContext(ctx, p), record, p)
				if opened != nil {
					c = opened
				}
				if err != nil {
					c.progress.State, c.progress.Reason = "failed", err.Error()
				} else {
					c.progress.AdmissionState = "admitted"
					c.progress.State = "ready"
				}
			}
			if source, ok := c.external.(indexRunBlockSource); ok && c.selection.Index != bitmapReplayParcelID {
				source.SetBlockSource(func(callCtx context.Context, h int64) (blockView, error) {
					p, err := load(callCtx, h)
					if err != nil {
						return blockView{}, err
					}
					return p.block, nil
				})
			}
			if existing != nil {
				consumers[position] = c
			} else {
				consumers = append(consumers, c)
			}
		}
		for i := 1; i < len(consumers); i++ {
			for j := i; j > 0 && runConsumerOrder(consumers[j].selection.Index) < runConsumerOrder(consumers[j-1].selection.Index); j-- {
				consumers[j], consumers[j-1] = consumers[j-1], consumers[j]
			}
		}
		authority := a.currentChainAuthority()
		hashAt := a.addressSelectedHashes(ctx, authority)
		anyLive := false
		for _, s := range record.Request.Consumers {
			pairedDistrict := s.Index == bitmapStandardDistrictID && record.ParcelDistrict != nil && record.ParcelDistrict.DistrictSelected
			anyLive = anyLive || indexRunSelectionEnabled(s) && s.Live && !pairedDistrict
		}
		if record.Request.Out == nil && anyLive && authority.Height >= 0 {
			target, targetHash = authority.Height, authority.Hash
		}
		nexts := map[int]int64{}
		eligible := []int{}
		failed, waiting := false, false
		for i, c := range consumers {
			if c.parcelBusy {
				selected, included := runSelection(record.Request, c.selection.Index)
				if (!included || !indexRunSelectionEnabled(selected)) && c.parcelCancel != nil {
					c.parcelCancel()
				}
				// The proof lane owns the validated writer until publication finishes.
				if c.selection.Index != bitmapReplayParcelID {
					waiting = true
					continue
				}
			}
			consumerTarget := record.InitialTargetHeight
			selection, _ := runSelection(record.Request, c.selection.Index)
			if !indexRunSelectionEnabled(selection) {
				c.abortLocatorBatch()
				countLocatorWrites(c, &counts)
				c.progress.State = "off"
				c.progress.Reason = "Off; committed records are retained."
				continue
			}
			c.selection.Live = selection.Live
			if selection.Live && record.Request.Out == nil {
				consumerTarget = target
			}
			if c.selection.Index == bitmapStandardDistrictID && record.ParcelDistrict != nil && record.ParcelDistrict.DistrictSelected {
				parcel, _ := runSelection(record.Request, bitmapReplayParcelID)
				if !parcel.Live || record.Request.Out != nil {
					consumerTarget = min(consumerTarget, record.InitialTargetHeight)
				}
			}
			if c.selection.Index == bitmapReplayParcelID && record.ParcelDistrict != nil && !record.ParcelDistrict.DistrictSelected {
				consumerTarget = min(consumerTarget, record.ParcelDistrict.CeilingHeight)
			}
			if c.progress.AdmissionState == "blocked" {
				c.progress.State = "blocked"
				if c.selection.Index == bitmapReplayParcelID {
					// An invalid admission cannot publish Parcel coverage.
					failed = true
				} else {
					waiting = true
				}
				continue
			}
			if c.progress.State == "failed" || c.progress.State == "rebuild_required" {
				failed = true
				continue
			}
			dependencyFailed := false
			for _, dependency := range c.progress.Dependencies {
				if dependency.Kind != "state" || dependency.Index == c.selection.Index {
					continue
				}
				for _, upstream := range consumers {
					if upstream.selection.Index == dependency.Index && !bitmapConsumerID(c.selection.Index) && (upstream.progress.State == "failed" || upstream.progress.State == "rebuild_required" || upstream.progress.State == "off") {
						dependencyFailed = true
					}
				}
			}
			if dependencyFailed {
				c.progress.State = "failed"
				c.progress.Reason = "The explicitly selected prerequisite is stopped or failed; its saved data was preserved."
				failed = true
				continue
			}
			if time.Now().Before(c.retryAfter) {
				waiting = true
				continue
			}
			consumerCtx := a.indexRunBoundConsumerContext(ctx, c.progress)
			if bitmapConsumerID(c.selection.Index) {
				if err := checkIndexRunPublication(consumerCtx); err != nil {
					c.progress.State = "failed"
					c.progress.Reason = err.Error()
					failed = true
					continue
				}
			}
			reconcileStarted := time.Now()
			reconcileNeeded := !c.reconciled || c.authority.Source != authority.Source || c.authority.Height != authority.Height || c.authority.Hash != authority.Hash
			var err error
			if !c.parcelBusy {
				err = a.reconcileRunConsumer(consumerCtx, c, authority)

			}
			if reconcileNeeded {
				counts.ReconcileMilliseconds += time.Since(reconcileStarted).Milliseconds()
			}
			if err != nil {
				c.progress.State = "waiting"
				if errors.Is(err, errIndexRunConsumerStopped) {
					c.progress.State = "off"
				}
				if errors.Is(err, errIndexRunAdmissionChanged) {
					c.progress.State = "waiting"
				}
				if runTerminal(err) {
					c.progress.State = "rebuild_required"
					failed = true
				}
				c.progress.Reason = err.Error()
				waiting = true
				continue
			}
			if c.selection.Index == "address-state" {
				u, err := utxoStoreHead(a.dataDir)
				if err != nil || u.Head() == nil {
					c.progress.State = "waiting"
					c.progress.Reason = "Waiting for an explicitly armed UTXO source."
					waiting = true
					continue
				}
				if source, ok := runSelection(record.Request, "utxo-state"); ok && source.Armed && u.Head().Height < consumerTarget {
					c.progress.State = "waiting"
					c.progress.Reason = "Waiting for the UTXO consumer to commit the selected target."
					waiting = true
					continue
				}
				if u.Head().Height > consumerTarget {
					if c.progress.State == "complete" {
						continue
					}
					c.progress.State = "failed"
					c.progress.Reason = "The UTXO source advanced beyond Out; no historical state was fabricated."
					failed = true
					continue
				}
				status, e := addressStateStatusContext(ctx, a.dataDir)
				if e == nil && status.State == "ready" {
					c.progress.State = "complete"
					c.progress.Height = u.Head().Height
					c.progress.Commitment = status.Checkpoint.Commitment
					c.progress.BlockHash = status.Checkpoint.BlockHash
					continue
				}
				projectionCtx := context.WithValue(consumerCtx, indexRunPublicationKey{}, func() error {
					if err := checkIndexRunPublication(consumerCtx); err != nil {
						return err
					}
					current, e := a.utxoSelectedTarget(ctx, a.currentChainAuthority(), u.Head().Height)
					if e != nil {
						return e
					}
					if current.HashDisplay != u.Head().BlockHash {
						return satPublicationAnchorError077{fmt.Errorf("selected UTXO source changed before address publication")}
					}
					return nil
				})
				cp, e := buildAddressStateContext(projectionCtx, a.dataDir, nil)
				if e != nil {
					c.progress.State = "failed"
					c.progress.Reason = e.Error()
					if errors.Is(e, errIndexRunConsumerStopped) {
						c.progress.State = "off"
					} else if isSelectedAuthorityRetry(e) {
						c.progress.State = "waiting"
						c.retryAfter = time.Now().Add(time.Second)
						waiting = true
					} else if ctx.Err() != nil {
						c.progress.State = "paused"
					} else {
						failed = true
					}
				} else {
					c.progress.State = "complete"
					c.progress.Height = cp.Height
					c.progress.Commitment = cp.Commitment
					c.progress.BlockHash = cp.BlockHash
				}
				continue
			}
			h := c.next(consumerTarget)
			c.progress.NextRequiredHeight = &h
			if c.progress.EffectiveFrom != nil && h < *c.progress.EffectiveFrom {
				c.progress.State, c.progress.AdmissionState = "blocked", "blocked"
				c.progress.RequiresLeadIn = true
				c.progress.Reason = "This saved state needs an earlier prefix than the active join boundary. Stop this run to explicitly build that prefix."
				waiting = true
				continue
			}
			c.progress.To = consumerTarget
			if h > consumerTarget {
				if c.selection.Index == bitmapReplayParcelID && c.parcelBusy {
					waiting = true
					continue
				}
				if c.store != nil && c.selection.Index == "tx-locator" {
					err := c.store.FlushLocatorBatch(consumerCtx)
					countLocatorWrites(c, &counts)
					if err != nil {
						c.progress.State, c.progress.Reason = "failed", err.Error()
						c.abortLocatorBatch()
						failed = true
						continue
					}
					a.noteRunLocatorPublication(consumerCtx, id, c, true)
				}
				c.progress.State = "complete"
				if parcel, _ := runSelection(record.Request, bitmapReplayParcelID); c.selection.Index == bitmapStandardDistrictID && selection.Live && record.ParcelDistrict != nil && record.ParcelDistrict.DistrictSelected && (!parcel.Live || record.Request.Out != nil) {
					c.progress.Reason = "District reached the finite paired Parcel boundary. Review a new run to continue District Live."
				} else {
					c.progress.Reason = "Committed coverage satisfies this run target."
				}
				continue
			}
			if c.selection.Index == bitmapStandardDistrictID && record.ParcelDistrict != nil && record.ParcelDistrict.DistrictSelected {
				parcelDistrictAdmissionMu079.Lock()
				pairErr := a.allowPairedDistrictPublication079(consumerCtx, h)
				parcelDistrictAdmissionMu079.Unlock()
				if pairErr != nil {
					c.progress.State, c.progress.Reason = "waiting", pairErr.Error()
					if parcel, ok := runSelection(record.Request, bitmapReplayParcelID); ok && parcelProducerCanAdvance079(record, consumers, parcel.ParcelEvidenceRoute) {
						waiting = true
					} else {
						for _, member := range consumers {
							if member.selection.Index == bitmapReplayParcelID && member.progress.AdmissionState != "blocked" && member.progress.State != "failed" && member.progress.State != "rebuild_required" && member.progress.State != "off" && member.progress.ParcelValidationState != "blocked" {
								waiting = true
							}
						}
						if !waiting {
							failed = true
						}
					}
					continue
				}
			}
			if record.Request.In != nil && h < *record.Request.In && !c.enrich {
				c.progress.State = "failed"
				c.progress.Reason = "Recovery requires a prefix outside In. Review the expanded global window; no hidden replay started."
				failed = true
				continue
			}
			if c.selection.Index == bitmapReplayParcelID {
				// The validation lane reads only after both checked prerequisites are ready.
				c.progress.State = "waiting"
				continue
			}
			c.progress.State = "running"
			c.progress.Reason = ""
			nexts[i] = h
			eligible = append(eligible, i)
		}
		for _, c := range consumers {
			if c.selection.Index != bitmapReplayParcelID {
				continue
			}
			validationTarget := record.InitialTargetHeight
			if c.selection.Live && record.Request.Out == nil {
				validationTarget = target
			}
			if record.ParcelDistrict != nil && !record.ParcelDistrict.DistrictSelected {
				validationTarget = min(validationTarget, record.ParcelDistrict.CeilingHeight)
			}
			if queueParcelValidation(c, validationTarget, authority) || c.parcelBusy || c.progress.State == "waiting" && c.progress.ParcelValidationState != "blocked" {
				waiting = true
			}
		}
		if len(eligible) == 0 {
			validationWaitOnly := !anyLive
			validationWaiting := false
			for _, c := range consumers {
				if c.selection.Index == bitmapReplayParcelID && c.progress.ParcelValidationState == "blocked" {
					failed = true
				} else if c.selection.Index == bitmapReplayParcelID && c.progress.ParcelValidationState != "complete" && c.external != nil && (c.parcelBusy || c.external.NextHeight() <= c.progress.To) {
					validationWaiting = true
					waiting = true
				} else if c.selection.Index != bitmapReplayParcelID && c.progress.State != "complete" && c.progress.State != "off" && c.progress.State != "failed" && c.progress.State != "rebuild_required" {
					validationWaitOnly = false
				}
			}
			if !waiting {
				_ = a.setIndexRunFrontier(id, target+1, false)
			}
			if failed && !waiting {
				finish("complete_with_errors", "Some consumers could not complete; successful checkpoints are retained.")
				return
			}
			if !waiting && !anyLive {
				reason := ""
				for _, c := range consumers {
					if c.selection.Index == bitmapReplayParcelID && c.progress.ParcelValidationState != "" && c.progress.ParcelValidationState != "complete" {
						reason = "Parcel validation remains " + c.progress.ParcelValidationState + ": " + c.progress.ParcelValidationReason
					}
				}
				finish("complete", reason)
				return
			}
			if record.Request.Out != nil && !waiting {
				finish("complete", "Out reached. Saved Live preferences remain capped by the global window.")
				return
			}
			if err := a.saveRunProgress(id, "waiting", "Waiting for selected-chain evidence or an armed prerequisite.", consumers, counts, target, targetHash); err != nil {
				finish("failed", err.Error())
				return
			}
			if validationWaiting && validationWaitOnly {
				select {
				case <-ctx.Done():
				case result := <-parcelDone:
					consumeParcel(result)
				case <-parcelEvidenceEventsFor079(a.dataDir).wake:
				case <-time.After(time.Second): // check deferred source retries; no proof work until due
				}
			} else {
				select {
				case <-ctx.Done():
				case result := <-parcelDone:
					consumeParcel(result)
				case <-parcelEvidenceEventsFor079(a.dataDir).wake:
				case <-time.After(time.Second):
				}
			}
			continue
		}
		// Sweep the lowest currently ready missing height. Matching consumers
		// share its verified body; stalled members are parked independently.
		chosen := lowestReadyIndexRunFrontier079(eligible, nexts)
		// A caught-up Live consumer must not wait for an entire older finite
		// backfill when the tip advances. Give it one turn per eight completed
		// cohorts, while continuing the lowest-frontier sweep in between.
		if record.Request.Out == nil && processed%8 == 7 {
			liveChosen := -1
			for _, i := range eligible {
				if consumers[i].selection.Live && nexts[i] > record.InitialTargetHeight && nexts[i] > nexts[chosen] && (liveChosen < 0 || nexts[i] < nexts[liveChosen]) {
					liveChosen = i
				}
			}
			if liveChosen >= 0 {
				chosen = liveChosen
			}
		}
		h := nexts[chosen]
		if err := a.setIndexRunFrontier(id, h, true); err != nil {
			finish("paused", "The current stop intent was preserved.")
			return
		}

		// Catch up a TX prefix from a complete committed point island without
		// acquiring/decoding/extracting its body again. Other same-height
		// consumers still require their own actual body source below.
		txOnly := -1
		sameHeight := 0
		for _, i := range eligible {
			if nexts[i] == h {
				sameHeight++
				if consumers[i].selection.Index == "tx-locator" {
					txOnly = i
				}
			}
		}
		if txOnly >= 0 && sameHeight == 1 {
			c := consumers[txOnly]
			selected, selectErr := hashAt(h)
			if selectErr == nil {
				lean, release := a.indexRunLocatorBatchGuard(ctx, id, c.selection, c.progress, h, selected, false)
				promoteCtx := context.WithValue(lean, indexLocatorBatchGuardKey{}, lean)
				promoteCtx = pointSession.context(promoteCtx)
				started := time.Now()
				promoted, promoteErr := a.promoteRunTransactionPoint(promoteCtx, c, h, selected, runRawRetention(record.Request))
				counts.ApplyMilliseconds += time.Since(started).Milliseconds()
				if promoted || promoteErr != nil {
					if promoteErr != nil {
						// Automatic flush can fail after staging the incoming
						// block. Abort before cancelling its guard; the saved
						// point remains available for exact zero-body replay.
						release()
					} else if promoted {
						c.locatorGuardReleases = append(c.locatorGuardReleases, release)
					} else {
						release()
					}
					countLocatorWrites(c, &counts)
					if promoteErr != nil {
						c.progress.State = "waiting"
						c.progress.Reason = promoteErr.Error()
						c.retryAfter = time.Now().Add(time.Second)
					} else {
						c.progress.AddedBlocks++
						c.progress.Height = h
						next := h + 1
						c.progress.NextRequiredHeight = &next
						c.progress.Reason = ""
						a.noteRunLocatorPublication(promoteCtx, id, c, false)
					}
					_ = a.setIndexRunFrontier(id, h+1, false)
					if err := a.saveRunProgress(id, "running", "", consumers, counts, target, targetHash); err != nil {
						finish("failed", err.Error())
						return
					}
					continue
				}
				release()
			}
		}
		// A Chain over retained completed outcomes has no Bitcoin-body input.
		// Do not route it through load(), even when the body is unavailable.
		rawRequired := false
		for _, i := range eligible {
			if nexts[i] == h && (!bitmapConsumerID(consumers[i].selection.Index) || bitmapConsumerKind(consumers[i].selection.Index) != "chain") {
				rawRequired = true
			}
		}
		if !rawRequired {
			peerWindow.Reset()
			for _, i := range eligible {
				if nexts[i] != h {
					continue
				}
				c := consumers[i]
				consumer, ok := c.external.(indexRunOutcomeConsumer)
				if !ok {
					c.progress.State = "failed"
					c.progress.Reason = "Outcome-only consumer adapter unavailable"
					continue
				}
				expected, e := hashAt(h)
				if e != nil {
					c.progress.State = "waiting"
					c.progress.Reason = e.Error()
					c.retryAfter = time.Now().Add(time.Second)
					continue
				}
				consumerCtx := a.indexRunBoundConsumerContext(ctx, c.progress)
				publishCtx := context.WithValue(consumerCtx, indexRunPublicationKey{}, func() error {
					if err := checkIndexRunPublication(consumerCtx); err != nil {
						return err
					}
					current, e := a.utxoSelectedTarget(ctx, a.currentChainAuthority(), h)
					if e != nil {
						return e
					}
					if current.HashDisplay != expected {
						return satPublicationAnchorError077{fmt.Errorf("selected outcome block changed before publication")}
					}
					return nil
				})
				counts.OutcomeReads++
				started := time.Now()
				err := consumer.ApplyRetainedOutcome(publishCtx, h)
				counts.ApplyMilliseconds += time.Since(started).Milliseconds()
				if err != nil {
					c.progress.State = "failed"
					if errors.Is(err, ErrBitmapEvidenceRequired) {
						c.progress.State = "waiting"
						c.retryAfter = time.Now().Add(time.Second)
					}
					if isSelectedAuthorityRetry(err) {
						c.progress.State = "waiting"
						c.retryAfter = time.Now().Add(time.Second)
					}
					if errors.Is(err, errIndexRunConsumerStopped) {
						c.progress.State = "off"
					}
					if errors.Is(err, errIndexRunAdmissionChanged) {
						c.progress.State = "waiting"
					}
					var changed satPublicationAnchorError077
					if errors.As(err, &changed) {
						c.progress.State = "waiting"
						c.retryAfter = time.Now().Add(time.Second)
					}
					c.progress.Reason = err.Error()
				} else {
					counts.ChainSteps++
					c.progress.AddedBlocks++
					c.progress.Height = h
					next := h + 1
					c.progress.NextRequiredHeight = &next
					c.progress.Reason = ""
				}
			}
			_ = a.setIndexRunFrontier(id, h+1, false)
			if err := a.saveRunProgress(id, "running", "", consumers, counts, target, targetHash); err != nil {
				finish("failed", err.Error())
				return
			}
			continue
		}
		// Prime the missing-body window before awaiting its first block. A
		// stalling first peer must not leave all other transports idle.
		initialRanges := []indexRunDownloadRange{}
		for _, i := range eligible {
			c := consumers[i]
			if bitmapConsumerID(c.selection.Index) && bitmapConsumerKind(c.selection.Index) == "chain" {
				continue
			}
			cap := c.progress.To
			if nexts[i] <= cap {
				initialRanges = append(initialRanges, indexRunDownloadRange{nexts[i], cap})
			}
		}
		peerWindow.Update(a.currentChainAuthority(), initialRanges)
		p, err := load(ctx, h)
		if err != nil {
			if ctx.Err() != nil {
				finish("paused", "Paused; committed records are retained.")
				return
			}
			var unavailable blockUnavailableError
			retry := errors.As(err, &unavailable) || isSelectedAuthorityRetry(err)
			for _, i := range eligible {
				if nexts[i] == h {
					consumers[i].progress.State = "waiting"
					if !retry {
						consumers[i].progress.State = "failed"
					} else {
						// A missing body at one frontier must not impose a universal
						// barrier on independently ready producers at other heights.
						consumers[i].retryAfter = time.Now().Add(time.Second)
					}
					consumers[i].progress.Reason = err.Error()
				}
			}
			if e := a.saveRunProgress(id, "waiting", err.Error(), consumers, counts, target, targetHash); e != nil {
				finish("failed", e.Error())
				return
			}
			// If another ready frontier exists, process it immediately. When
			// none exists, wait rather than repeatedly fetching the same body.
			otherReady := false
			for _, i := range eligible {
				if nexts[i] != h {
					otherReady = true
					break
				}
			}
			if !otherReady {
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
				}
			}
			continue
		}
		// Acquire at most one upcoming raw block while this cohort derives and
		// commits. The scheduling cap belongs to each admitted member; outcome-
		// only Chains never cause speculative Bitcoin reads.
		upcoming := int64(-1)
		peerRanges := []indexRunDownloadRange{}
		for _, i := range eligible {
			c := consumers[i]
			if bitmapConsumerID(c.selection.Index) && bitmapConsumerKind(c.selection.Index) == "chain" {
				continue
			}
			candidate := nexts[i]
			if candidate == h {
				candidate++
			}
			cap := c.progress.To
			if candidate <= cap && (upcoming < 0 || candidate < upcoming) {
				upcoming = candidate
			}
			if candidate <= cap {
				peerRanges = append(peerRanges, indexRunDownloadRange{candidate, cap})
			}
		}
		peerWindow.Update(a.currentChainAuthority(), peerRanges)
		if upcoming >= 0 {
			if candidate, e := a.utxoSelectedTarget(ctx, a.currentChainAuthority(), upcoming); e == nil && cache[candidate.HashDisplay] == nil {
				// A known TX point can satisfy this upcoming step without a body.
				// Do not let speculation defeat its zero-acquisition promotion.
				bodyNeeded := timelineRawBlocksOn(a.dataDir)
				for _, i := range eligible {
					next := nexts[i]
					if next == h {
						next++
					}
					if next == upcoming && consumers[i].selection.Index != "tx-locator" {
						bodyNeeded = true
					}
				}
				if !bodyNeeded {
					_, known, err := a.runTransactionPoint(ctx, upcoming, candidate.HashDisplay)
					bodyNeeded = err != nil || !known
				}
				if bodyNeeded {
					prefetch.Schedule(candidate)
				}
			}
		}

		extractionStarted := time.Now()
		wasAttempted := p.extractionAttempted || p.extraction != nil
		prepareSharedBlockInventory(a.dataDir, p, a.indexRunSnapshot())
		if !wasAttempted && p.extractionAttempted {
			counts.ExtractionMilliseconds += time.Since(extractionStarted).Milliseconds()
		}
		countPreparedExtraction(p, &counts)
		owners := map[string]bool{}
		for _, i := range eligible {
			if nexts[i] == h {
				owners[consumers[i].selection.Index] = true
			}
		}
		// Exact already-staged anchors have one native TX owner too. This is
		// deliberately not a reservation for the rest of its admitted range.
		for _, c := range consumers {
			if c.store != nil && c.selection.Index == "tx-locator" && c.store.locatorBatch != nil {
				for _, staged := range c.store.locatorBatch.commits {
					if staged.checkpoint.Height == h && staged.checkpoint.BlockHash == p.block.Hash {
						owners["tx-locator"] = true
						break
					}
				}
			}
		}
		blockCtx := context.WithValue(pointSession.context(ctx), indexRunPreparedKey{}, p)
		blockCtx = preparedSourceContext(blockCtx, p)
		blockCtx = context.WithValue(blockCtx, indexRunCoordinatorSourceKey{}, true)
		blockCtx = context.WithValue(blockCtx, indexRunCohortOwnersKey{}, owners)
		blockCtx, drain := a.withDeferredBlockIngestion(blockCtx)
		rawErr := a.ensurePreparedRawProducer(blockCtx, p) // frozen before either worker lane
		type consumerResult struct {
			attempted bool
			async     bool
			err       error
			elapsed   time.Duration
			outcome   bool
		}
		results := make([]consumerResult, len(consumers))
		operations := map[string]indexRunBlockConsumer{}
		for _, i := range eligible {
			if nexts[i] != h {
				continue
			}
			i, c := i, consumers[i]
			operations[c.selection.Index] = func(sharedCtx context.Context) error {
				result := &results[i]
				result.attempted = true
				if rawErr != nil {
					result.err = rawErr
					return result.err
				}
				if p.extractionErr != nil && resolvedNeedsInventory(c.selection.Index) {
					result.err = p.extractionErr
					return result.err
				}

				consumerCtx := a.indexRunBoundConsumerContext(sharedCtx, c.progress)
				publishCtx := context.WithValue(consumerCtx, indexRunPublicationKey{}, func() error {
					if err := checkIndexRunPublication(consumerCtx); err != nil {
						return err
					}
					if err := a.checkPreparedRawProducer(consumerCtx, p); err != nil {
						return err
					}
					current, err := a.utxoSelectedTarget(ctx, a.currentChainAuthority(), h)
					if err != nil {
						return err
					}
					if current.HashDisplay != p.block.Hash {
						return satPublicationAnchorError077{fmt.Errorf("selected block changed before consumer publication")}
					}
					return nil
				})
				if c.selection.Index == "tx-locator" {
					lean, release := a.indexRunLocatorBatchGuard(ctx, id, c.selection, c.progress, h, p.block.Hash, p.rawProduced)
					c.locatorGuardReleases = append(c.locatorGuardReleases, release)
					publishCtx = context.WithValue(publishCtx, indexLocatorBatchGuardKey{}, lean)
				}
				started := time.Now()
				if outcome, ok := c.external.(indexRunOutcomeConsumer); ok && bitmapConsumerKind(c.selection.Index) == "chain" {
					result.outcome = true
					result.err = outcome.ApplyRetainedOutcome(publishCtx, h)
				} else {
					result.err = a.applyRunConsumer(publishCtx, c, p, authority, runRawRetention(record.Request))
				}
				result.elapsed = time.Since(started)
				if result.err == nil {
					a.noteRunLocatorPublication(publishCtx, id, c, false)
				}
				return result.err
			}
		}
		blockCtx = context.WithValue(blockCtx, indexRunBlockConsumersKey{}, operations)
		var offered []indexBrowseResult
		if rawErr == nil {
			offered = a.ingestArmedResolvedBlock(blockCtx, p.block)
		}
		// Every native/point writer has joined and released its store gates.
		// Only now may dependency bodies enter their own all-armed plans.
		drain()
		failures := sourceFactFailures(h, p.block.Hash, offered)
		if queue, ok := blockCtx.Value(resolvedIngestionKey{}).(*resolvedIngestionQueue); ok {
			failures = append(failures, queue.fanoutErrors...)
		}
		fanoutReason := a.recordRunSourceFactFailures(id, failures)
		for _, i := range eligible {
			if nexts[i] != h {
				continue
			}
			c, result := consumers[i], results[i]
			if result.async {
				continue
			}
			if !result.attempted {
				result.err = rawErr
				if result.err == nil {
					result.err = blockUnavailableError{fmt.Errorf("shared selected source dispatch was unavailable")}
				}
			}
			counts.ApplyMilliseconds += result.elapsed.Milliseconds()
			if result.outcome {
				counts.OutcomeReads++
				if result.err == nil {
					counts.ChainSteps++
				}
			}
			countLocatorWrites(c, &counts)
			err := result.err
			if err != nil {
				c.progress.State = "failed"
				if errors.Is(err, errIndexRunConsumerStopped) {
					c.progress.State = "off"
				}
				if errors.Is(err, errIndexRunAdmissionChanged) {
					c.progress.State = "waiting"
				}
				var unavailable blockUnavailableError
				if errors.As(err, &unavailable) {
					c.progress.State = "waiting"
					c.retryAfter = time.Now().Add(time.Second)
				}
				var changed satPublicationAnchorError077
				if errors.As(err, &changed) {
					prefetch.Reset()
					c.progress.State = "waiting"
					c.retryAfter = time.Now().Add(time.Second)
					cache = map[string]*indexRunPrepared{}
					order = nil
					cacheBytes = 0
				}
				if errors.Is(err, ErrBitmapEvidenceRequired) {
					c.progress.State = "waiting"
					c.retryAfter = time.Now().Add(time.Second)
				}
				if isSelectedAuthorityRetry(err) {
					prefetch.Reset()
					c.progress.State = "waiting"
					c.retryAfter = time.Now().Add(time.Second)
					cache = map[string]*indexRunPrepared{}
					order = nil
					cacheBytes = 0
				}
				if runTerminal(err) {
					c.progress.State = "rebuild_required"
				}
				c.progress.Reason = err.Error()
			} else {
				c.progress.AddedBlocks++
				c.progress.Height = h
				next := h + 1
				c.progress.NextRequiredHeight = &next
				c.progress.Reason = ""
			}
		}
		if queue, ok := blockCtx.Value(resolvedIngestionKey{}).(*resolvedIngestionQueue); ok && queue.err() != nil {
			finish("failed", "Deferred verified source fanout failed; independently committed checkpoints are retained: "+queue.err().Error())
			return
		}
		_ = a.setIndexRunFrontier(id, h+1, false)
		started := time.Now()
		if e := a.saveRunProgress(id, "running", fanoutReason, consumers, counts, target, targetHash); e != nil {
			finish("failed", e.Error())
			return
		}
		counts.ProgressMilliseconds += time.Since(started).Milliseconds()
		processed++
		if processed%128 == 0 {
			sharedDue := processed%utxoSharedMaintenanceBlocks == 0
			for _, c := range consumers {
				if c.selection.Index == "sat-state" && c.store != nil {
					_ = c.store.collectOrdinalState()
				}
				if c.utxo != nil {
					_ = c.utxo.CollectPages(ctx)
					sharedDue = sharedDue || utxoSharedMaintenanceDue(c.utxo)
				}
			}
			// Live runs may never finish. Apply the existing shared-fact
			// maintenance cadence at a committed boundary rather than retaining
			// every expired creation page until a future manual pause/restart.
			if sharedDue && ctx.Err() == nil {
				if err := collectSharedOutputFactsContext(ctx, a.dataDir); err == nil {
					for _, c := range consumers {
						if c.utxo != nil {
							_ = recordUTXOSharedMaintenance(c.utxo)
						}
					}
				}
			}
		}
	}
}
