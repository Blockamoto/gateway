package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Admission and District publication share this fence. It makes the saved
// catch-up checkpoint immutable for the lifetime of a reviewed run, including
// when a block was acquired by Explorer rather than the coordinated worker.
var parcelDistrictAdmissionMu079 sync.Mutex

// Unexported injection mirrors indexRunSourceBlock and is only assigned by
// isolated tests. Normal application construction always uses actual stores.
type parcelEvidenceHooks079 struct {
	Timeline         func() indexTimelineView
	Configure        func(context.Context, *indexRunConsumer, int64) error
	RetryDelay       time.Duration // tests only; production uses a bounded backoff
	BeforeParcelHead func(int64)   // tests only; simulates selected-chain changes at publication
}

func (a *app) parcelEvidenceTimeline079() indexTimelineView {
	if a.parcelEvidenceHooks079 != nil && a.parcelEvidenceHooks079.Timeline != nil {
		return a.parcelEvidenceHooks079.Timeline()
	}
	return a.indexStatus().Timeline
}

type parcelDistrictPair079 struct {
	DistrictSelected bool   `json:"district_selected"`
	CeilingHeight    int64  `json:"ceiling_height"`
	CeilingHash      string `json:"ceiling_hash,omitempty"`
	CeilingCommit    string `json:"ceiling_commitment,omitempty"`
	DistrictProfile  string `json:"district_profile"`
	EvidenceThrough  int64  `json:"evidence_through"`
}

func (a *app) parcelDistrictSnapshot079(ctx context.Context, req indexRunRequest, target int64) (*parcelDistrictPair079, error) {
	parcel, have := runSelection(req, bitmapReplayParcelID)
	if !have || !indexRunSelectionEnabled(parcel) {
		return nil, nil
	}
	instance, err := bitmapResolvedInstance(ctx, a.dataDir, bitmapReplayParcelID)
	if err != nil {
		return nil, err
	}
	if instance.DistrictInstance != bitmapStandardDistrictID || instance.DistrictProfileHash == "" {
		return nil, fmt.Errorf("Parcel District identity is incompatible; review the saved profiles")
	}
	district, selected := runSelection(req, instance.DistrictInstance)
	pair := &parcelDistrictPair079{DistrictSelected: selected && indexRunSelectionEnabled(district), CeilingHeight: instance.StartHeight - 1, DistrictProfile: instance.DistrictProfileHash, EvidenceThrough: min(target, instance.StartHeight)}
	parcelHead, err := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapReplayParcelID)
	if err != nil {
		return nil, err
	}
	if parcelHead != nil {
		pair.EvidenceThrough = min(target, parcelHead.Height+1)
	}

	head, err := readBitmapDevelopmentHead(ctx, a.dataDir, instance.DistrictInstance)
	if err != nil {
		return nil, err
	}
	if head == nil || target < instance.StartHeight {
		return pair, nil
	}
	height := min(head.Height, target)
	head, err = bitmapHeadAt(ctx, a.dataDir, instance.DistrictInstance, height)
	if err != nil || head == nil || head.ProfileHash != pair.DistrictProfile || !head.LastBlock.BlockEvaluated || !head.LastBlock.CompleteScope {
		return nil, fmt.Errorf("complete matching District checkpoint is unavailable for Parcel catch-up")
	}
	selectedBlock, err := a.utxoSelectedTarget(ctx, a.currentChainAuthority(), height)
	if err != nil || selectedBlock.HashDisplay != head.BlockHash {
		return nil, fmt.Errorf("saved District catch-up checkpoint is not on the selected chain")
	}
	pair.CeilingHeight, pair.CeilingHash, pair.CeilingCommit = height, head.BlockHash, head.Commitment
	if !pair.DistrictSelected && pair.EvidenceThrough > height {
		pair.EvidenceThrough = height
	}
	return pair, nil
}

// Verify the actual complete Parcel checkpoint and the exact District outcome
// it used; a height or scan count on its own is never alignment evidence.
func (a *app) parcelDistrictAlignedHeight079(ctx context.Context, pair *parcelDistrictPair079) (int64, error) {
	aligned, _, err := a.parcelDistrictAlignedCeiling079(ctx, pair, false)
	return aligned, err
}

// During Live a reorg can invalidate the original fixed catch-up checkpoint.
// Rebind the hold only after the current District head is fully reconciled and
// selected; then Parcel must catch that new complete head before it can grow.
func (a *app) parcelDistrictAlignedCeiling079(ctx context.Context, pair *parcelDistrictPair079, live bool) (int64, int64, error) {
	if pair == nil {
		return -1, -1, nil
	}
	ceiling := pair.CeilingHeight
	if pair.CeilingCommit != "" {
		selected, selectedErr := a.utxoSelectedTarget(ctx, a.currentChainAuthority(), pair.CeilingHeight)
		if selectedErr != nil {
			return -1, -1, fmt.Errorf("District catch-up selected-chain anchor unavailable")
		}
		if selected.HashDisplay == pair.CeilingHash {
			d, err := bitmapHeadAt(ctx, a.dataDir, bitmapStandardDistrictID, pair.CeilingHeight)
			if err != nil || d == nil || d.Commitment != pair.CeilingCommit || d.BlockHash != pair.CeilingHash || d.ProfileHash != pair.DistrictProfile {
				return -1, -1, fmt.Errorf("District catch-up checkpoint changed or is unavailable")
			}
		} else {
			// Only a positively observed change of selected block proves a
			// reorg; unavailable or altered checkpoint data must fail closed.
			if !live {
				return -1, -1, fmt.Errorf("District catch-up checkpoint left the selected chain")
			}
			current, e := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapStandardDistrictID)
			if e != nil {
				return -1, -1, e
			}
			ceiling = bitmapDevelopmentStart - 1
			if current != nil {
				source, e := a.utxoSelectedTarget(ctx, a.currentChainAuthority(), current.Height)
				if e != nil || source.HashDisplay != current.BlockHash || current.ProfileHash != pair.DistrictProfile || !current.LastBlock.BlockEvaluated || !current.LastBlock.CompleteScope {
					return -1, -1, fmt.Errorf("District checkpoint is awaiting selected-chain reconciliation")
				}
				ceiling = current.Height
			}
		}
	}
	validated, err := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapReplayParcelID)
	if err != nil {
		return -1, -1, err
	}
	if validated == nil {
		return bitmapDevelopmentStart - 1, ceiling, nil
	}
	d, err := bitmapHeadAt(ctx, a.dataDir, bitmapStandardDistrictID, validated.Height)
	if err != nil || d == nil || d.BlockHash != validated.BlockHash || d.Commitment != validated.DependencyCommitment || d.ProfileHash != pair.DistrictProfile || !validated.LastBlock.BlockEvaluated || !validated.LastBlock.CompleteScope {
		return -1, -1, fmt.Errorf("validated Parcel coverage does not match the complete District outcome")
	}
	selected, err := a.utxoSelectedTarget(ctx, a.currentChainAuthority(), validated.Height)
	if err != nil || selected.HashDisplay != validated.BlockHash {
		return -1, -1, fmt.Errorf("validated Parcel checkpoint left the selected chain")
	}
	return validated.Height, ceiling, nil
}

// Called at the common District publication boundary with the admission fence
// held. It covers both the coordinated worker and incidental Explorer writes.
type parcelProofPrefixPending079 struct{ reason string }

func (e *parcelProofPrefixPending079) Error() string { return e.reason }
func (e *parcelProofPrefixPending079) Unwrap() error { return ErrBitmapEvidenceRequired }

func (a *app) allowPairedDistrictPublication079(ctx context.Context, height int64) error {
	doc, err := readIndexRunDocument(a.dataDir)
	if err != nil || doc.Run == nil {
		return err
	}
	r := doc.Run
	if r.State != "running" && r.State != "waiting" && r.State != "pausing" && r.State != "paused" {
		return nil
	}
	pair := r.ParcelDistrict
	if pair == nil {
		parcel, hasParcel := runSelection(r.Request, bitmapReplayParcelID)
		district, hasDistrict := runSelection(r.Request, bitmapStandardDistrictID)
		if hasParcel && hasDistrict && indexRunSelectionEnabled(parcel) && indexRunSelectionEnabled(district) {
			return fmt.Errorf("%w: this saved run has no authenticated District catch-up boundary; review a new run", ErrBitmapEvidenceRequired)
		}
		return nil
	}
	if !pair.DistrictSelected {
		return nil
	}
	parcel, ok := runSelection(r.Request, bitmapReplayParcelID)
	if !ok || !indexRunSelectionEnabled(parcel) {
		return nil
	}
	// Finite Parcel work always retains its original ceiling. Live following
	// may grow only to the current selected-chain target and never past Out.
	live := parcel.Live && r.Request.Out == nil
	if !live && height > r.InitialTargetHeight {
		return fmt.Errorf("%w: paired District is capped at the finite Parcel run boundary; review a new run to continue", ErrBitmapEvidenceRequired)
	}
	if live && height > a.currentChainAuthority().Height {
		return fmt.Errorf("%w: paired District waits for the selected chain", ErrBitmapEvidenceRequired)
	}
	progress, have := indexRunProgressFor(r, bitmapReplayParcelID)
	if !have || progress.AdmissionState == "blocked" {
		return fmt.Errorf("%w: District waits for an admitted Parcel track", ErrBitmapEvidenceRequired)
	}
	aligned, ceiling, err := a.parcelDistrictAlignedCeiling079(ctx, pair, live)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBitmapEvidenceRequired, err)
	}
	if aligned < ceiling || height > aligned+1 {
		return fmt.Errorf("%w: District waits for complete Parcel catch-up", ErrBitmapEvidenceRequired)
	}
	// A paired District block is useful only once Parcel can examine the same
	// block. In particular, do not speculate at the Bitmap origin while the
	// selected proof producer is still building its required genesis prefix.
	// This shared publication fence also covers incidental Explorer writes.
	gaps, evidenceErr := parcelEvidenceGaps079(parcel.ParcelEvidenceRoute, a.parcelEvidenceTimeline079(), height)
	if evidenceErr != nil {
		return &parcelProofPrefixPending079{reason: fmt.Sprintf("Bitmap Districts waits for the selected Parcel proof prefix through block %d: %v", height, evidenceErr)}
	}
	if len(gaps) != 0 {
		return &parcelProofPrefixPending079{reason: fmt.Sprintf("Bitmap Districts waits for the selected Parcel proof prefix through block %d; missing blocks begin at %d", height, gaps[0].From)}
	}
	return nil
}

// Rebind only to a checked committed coverage horizon. The same persistent run
// Work accounting is shared across phases; it does not limit proof validation.
func (a *app) configureParcelEvidence079(ctx context.Context, c *indexRunConsumer, through int64) error {
	if c == nil {
		return fmt.Errorf("Parcel evidence consumer is unavailable")
	}
	if consumer, ok := c.external.(*bitmapRunConsumer); ok {
		consumer.parcelRecipeProvider = c.selection.ParcelEvidenceRoute
	}
	if a.parcelEvidenceHooks079 != nil && a.parcelEvidenceHooks079.Configure != nil {
		return a.parcelEvidenceHooks079.Configure(ctx, c, through)
	}
	timeline := a.parcelEvidenceTimeline079()
	var spender func(context.Context, satlinePoint, int64, parcelScanObserver079) (spenderLookupView, error)
	var transaction func(context.Context, string, int64) (satlineTxEvidence, error)
	var blockAt func(context.Context, int64) (blockView, error)
	var prepare func(context.Context) error
	var err error
	switch c.selection.ParcelEvidenceRoute {
	case "retained-raw":
		spender, transaction, blockAt, err = a.newParcelRawRoute079(ctx, timeline, through, c.parcelBudget)
	case "tx-locators":
		spender, transaction, blockAt, err = a.newParcelLocatorRoute079(ctx, timeline, through, c.parcelBudget)
	case "txio-links":
		prepare, spender, transaction, blockAt, err = a.newParcelTXIORoute079(ctx, timeline, through, c.parcelBudget)
	default:
		return fmt.Errorf("explicit Parcel evidence route required")
	}
	if err != nil {
		return err
	}
	consumer, ok := c.external.(*bitmapRunConsumer)
	if !ok {
		return fmt.Errorf("Parcel replay consumer unavailable")
	}
	anchor, err := a.utxoSelectedTarget(ctx, a.currentChainAuthority(), through)
	if err != nil {
		return fmt.Errorf("Parcel evidence endpoint unavailable: %w", err)
	}
	consumer.parcelRawSpender, consumer.parcelRawTransaction = spender, transaction
	consumer.SetBlockSource(blockAt)
	c.parcelPrepare, c.parcelPrepared = prepare, false
	c.parcelEvidenceThrough, c.parcelEvidenceAnchorHash = through, anchor.HashDisplay
	return nil
}

func parcelEvidenceGaps079(route string, timeline indexTimelineView, through int64) ([]heightInterval, error) {
	switch route {
	case "retained-raw":
		return parcelRawRouteGaps079(timeline, through)
	case "tx-locators":
		return parcelLocatorRouteGaps079(timeline, through)
	case "txio-links":
		return parcelTXIORouteGaps079(timeline, through)
	default:
		return nil, fmt.Errorf("explicit Parcel evidence route required")
	}
}

func parcelEvidenceProducer079(route string) string {
	switch route {
	case "retained-raw":
		return "blocks"
	case "tx-locators":
		return "tx-locator"
	case "txio-links":
		return "txo-spender"
	}
	return ""
}

func parcelAvailableEvidenceThrough079(route string, timeline indexTimelineView, target int64) (int64, error) {
	gaps, err := parcelEvidenceGaps079(route, timeline, target)
	if err != nil {
		return -1, err
	}
	if len(gaps) > 0 {
		return gaps[0].From - 1, nil
	}
	return target, nil
}

func parcelProducerCanAdvance079(record *indexRunRecord, consumers []*indexRunConsumer, route string) bool {
	id := parcelEvidenceProducer079(route)
	selected, ok := runSelection(record.Request, id)
	if !ok || !indexRunSelectionEnabled(selected) {
		return false
	}
	for _, c := range consumers {
		if c.selection.Index == id && c.progress.State != "complete" && c.progress.State != "failed" && c.progress.State != "rebuild_required" && c.progress.State != "off" && c.progress.AdmissionState != "blocked" {
			return true
		}
	}
	return false
}
