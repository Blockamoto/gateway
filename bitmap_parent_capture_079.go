package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

var errParcelReplayBudget079 = errors.New("Parcel replay transcript exceeds block or byte budget")

// The replay recorder retains only blocks needed by one positive relationship.
// It never walks the chain: transaction and spender lookups use existing
// bounded providers, while every captured block is checked against the selected
// chain and replayed independently before the receipt is committed.
type parcelCaptureBackend079 struct {
	satlineBackend
	app            *app
	ctx            context.Context
	hashAt         func(int64) (string, error)
	blockAt        func(context.Context, int64) (blockView, error)
	rawSpender     func(context.Context, satlinePoint, int64, parcelScanObserver079) (spenderLookupView, error)
	rawTransaction func(context.Context, string, int64) (satlineTxEvidence, error)
	childHeight    int64
	blocks         map[int64]blockView
	bytes          int
	negative       *parcelReplayNegative079
}

// Verify a replay receipt in its actual selected District and candidate context.
// Other generations keep their original read-only receipt interpretation.
func verifyBitmapCandidateEvidence079(ctx context.Context, a *app, index string, candidate bitmapDevelopmentCandidate, hashAt func(int64) (string, error)) error {
	root := a.dataDir
	if index != bitmapReplayParcelID {
		return verifyBitmapParentEvidence(ctx, root, candidate.Provenance)
	}
	p := candidate.Provenance
	if p == nil {
		if candidate.Eligibility == "eligible" {
			return fmt.Errorf("selected Parcel is missing replay evidence")
		}
		return nil
	}
	validKind := p.EvaluationKind == "selected-chain-satpoint-replay-v3" || p.EvaluationKind == "local-selected-raw-no-spend-attestation-v1" && p.RelationState == "rejected"
	if p.ReplayVersion != 3 || !p.Verified || !validKind || candidate.District == nil || candidate.Transaction == nil || !candidate.SelectedParentDeclared || p.ChildBlocator != candidate.Blocator || p.ChildBlockHash != candidate.RevealHash || (p.RelationState != "verified" && p.RelationState != "rejected") {
		return fmt.Errorf("Parcel replay candidate binding mismatch")
	}
	if candidate.Eligibility == "eligible" && p.RelationState != "verified" || candidate.Eligibility == "ineligible" && p.RelationState != "rejected" {
		return fmt.Errorf("Parcel replay result does not match candidate eligibility")
	}
	var receipt parcelReplayReceipt079
	if !validHash(p.EvidenceHash) || readBitmapJSON(ctx, parcelReplayReceiptPath079(root, p.EvidenceHash), &receipt) != nil || indexDigest(receipt) != p.EvidenceHash {
		return fmt.Errorf("Parcel replay receipt unavailable")
	}
	if p.EvaluationKind == "local-selected-raw-no-spend-attestation-v1" {
		if receipt.Negative == nil || p.ScanFrom == nil || p.ScanThrough == nil || *p.ScanFrom != receipt.Negative.Point.Height || *p.ScanThrough != receipt.Negative.Through {
			return fmt.Errorf("Parcel local scan interval binding mismatch")
		}
	} else if receipt.Negative != nil || p.ScanFrom != nil || p.ScanThrough != nil {
		return fmt.Errorf("Parcel replay evidence kind mismatch")
	}
	d, err := bitmapHeadAt(ctx, root, bitmapStandardDistrictID, candidate.RevealHeight)
	if err != nil {
		return err
	}
	if d == nil || d.ProfileHash != bitmapProfileHash(bitmapStandardProfile("district")) || d.BlockHash != candidate.RevealHash || !d.LastBlock.BlockEvaluated || !d.LastBlock.CompleteScope {
		return fmt.Errorf("selected District interpretation unavailable for Parcel")
	}
	var selected bitmapDevelopmentCandidate
	have, err := newOrdinalTree(filepath.Join(bitmapDevelopmentDir(root, bitmapStandardDistrictID), "pages"), d.WinnerRoot).withContext(ctx).get(bitmapTargetKey(*candidate.District, nil), &selected)
	if err != nil {
		return err
	}
	if !have || selected.Blocator != p.ParentBlocator || selected.Selection != "selected" || selected.Eligibility != "eligible" {
		return fmt.Errorf("Parcel parent is not the selected District")
	}
	// Ordinary views must not initiate an implicit historical Raw scan. A
	// receipt needing a long retained-source replay remains unavailable here.
	return loadParcelReplay079(ctx, root, p.EvidenceHash, p.ParentID, p.ChildID, p.ParentBlocator, p.ChildBlocator, p.RelationState, hashAt)
}
func (b *parcelCaptureBackend079) SpenderPoint(point satlinePoint) (spenderLookupView, error) {
	if b.app != nil {
		if spend, ok := b.localSpender(point); ok {
			return spend, nil
		}
	}
	if b.rawSpender != nil {
		journal, err := newParcelReplayJournal079(b.ctx, b.app.dataDir, point, b.childHeight, b.hashAt)
		if err != nil {
			return spenderLookupView{}, err
		}
		result, err := b.rawSpender(b.ctx, point, b.childHeight, journal)
		if err != nil {
			return result, err
		}
		if result.ConfirmedState == "unspent_at_snapshot" {
			complete, err := journal.HasPrefix(b.childHeight + 1)
			if err != nil || !complete {
				return spenderLookupView{}, fmt.Errorf("complete Parcel transcript unavailable: %v", err)
			}
			digest, err := journal.Finalize(b.app.dataDir)
			if err != nil {
				return spenderLookupView{}, err
			}
			b.negative = &parcelReplayNegative079{Point: point, Through: b.childHeight, Attestation: digest}
		}
		return result, nil
	}
	return b.satlineBackend.Spender(point.TxID, point.Vout)
}

func (b *parcelCaptureBackend079) capture(block blockView) error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	if !integrityVerified(block) || len(block.verifiedRaw) < 81 {
		return fmt.Errorf("verified serialized block required for Parcel proof")
	}
	if err := ordRequireSelected(b.hashAt, block.Height, block.Hash); err != nil {
		return err
	}
	if old, ok := b.blocks[block.Height]; ok {
		if !strings.EqualFold(old.Hash, block.Hash) {
			return fmt.Errorf("conflicting proof blocks at one height")
		}
		return nil
	}
	if len(b.blocks) >= parcelReplayMaxBlocks079 || len(block.verifiedRaw) > parcelReplayMaxBytes079-b.bytes {
		return errParcelReplayBudget079
	}
	b.blocks[block.Height] = block
	b.bytes += len(block.verifiedRaw)
	return nil
}
func (b *parcelCaptureBackend079) BlockByHeight(height int64) (blockView, error) {
	if block, ok := b.blocks[height]; ok {
		return block, nil
	}
	var block blockView
	var err error
	if b.blockAt != nil {
		block, err = b.blockAt(b.ctx, height)
	} else {
		block, err = b.satlineBackend.BlockByHeight(height)
	}
	if err != nil {
		return blockView{}, err
	}
	if block.Height != height {
		return blockView{}, fmt.Errorf("Parcel proof block height mismatch")
	}
	if err := b.capture(block); err != nil {
		return blockView{}, err
	}
	return block, nil
}
func (b *parcelCaptureBackend079) Transaction(id string, before int64) (satlineTxEvidence, error) {
	var evidence satlineTxEvidence
	var err error
	if b.app != nil {
		if local, ok := b.localTransaction(id, before); ok {
			return local, nil
		}
	}
	if b.rawTransaction != nil {
		evidence, err = b.rawTransaction(b.ctx, id, before)
	} else {
		evidence, err = b.satlineBackend.Transaction(id, before)
	}
	if err != nil {
		return satlineTxEvidence{}, err
	}
	block, err := b.BlockByHeight(evidence.Height)
	if err != nil {
		return satlineTxEvidence{}, err
	}
	if !strings.EqualFold(evidence.BlockHash, block.Hash) {
		return satlineTxEvidence{}, fmt.Errorf("Parcel value evidence changed chain")
	}
	for _, tx := range block.Transactions {
		if strings.EqualFold(tx.TxID, id) {
			evidence.Tx = tx
			return evidence, nil
		}
	}
	return satlineTxEvidence{}, fmt.Errorf("Parcel value transaction absent from checked block")
}

func (s *bitmapRunConsumer) checkedReplayParent(ctx context.Context, childBlock blockView, occurrence inscriptionOccurrence, parent bitmapDevelopmentCandidate) (*bitmapParentProof, error) {
	return replayInscriptionParent(ctx, inscriptionParentReplaySource{app: s.a, root: s.root, hashAt: s.hashAt, blockAt: s.blockAt, spender: s.parcelRawSpender, transaction: s.parcelRawTransaction}, childBlock, occurrence, inscriptionParentReplayReference{ID: parent.ID, Ref: parent.Blocator, BlockHash: parent.RevealHash, Height: parent.RevealHeight, TxIndex: parent.TxIndex, Index: parent.InscriptionIndex})
}
