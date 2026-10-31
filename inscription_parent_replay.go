package main

import (
	"context"
	"fmt"
	"strings"
)

// A checked Ord relationship source, independent of claim grammar, selected
// Districts or winner state. Legacy proof fields/messages remain byte compatible.
type inscriptionParentReplayReference struct {
	ID, Ref, BlockHash string
	Height             int64
	TxIndex            int
	Index              uint32
}
type inscriptionParentReplaySource struct {
	app         *app
	root        string
	hashAt      func(int64) (string, error)
	blockAt     func(context.Context, int64) (blockView, error)
	spender     func(context.Context, satlinePoint, int64, parcelScanObserver079) (spenderLookupView, error)
	transaction func(context.Context, string, int64) (satlineTxEvidence, error)
}

func replayInscriptionParent(ctx context.Context, source inscriptionParentReplaySource, childBlock blockView, occurrence inscriptionOccurrence, parent inscriptionParentReplayReference) (*bitmapParentProof, error) {

	if source.app == nil || source.hashAt == nil || source.blockAt == nil || !isInscriptionID(parent.ID) || !isInscriptionID(occurrence.ID) {
		return nil, fmt.Errorf("selected-chain source and parent identity required for replay")
	}
	canonical := source.hashAt
	base := ordSelectedBackend{satlineBackend: ordContextBackend{contextSatlineBackend{appSatlineBackend{source.app}, ctx}}, canonical: canonical}
	b := &parcelCaptureBackend079{satlineBackend: base, app: source.app, ctx: ctx, hashAt: canonical, blockAt: source.blockAt, rawSpender: source.spender, rawTransaction: source.transaction, childHeight: childBlock.Height, blocks: map[int64]blockView{}}
	if err := b.capture(childBlock); err != nil {
		return nil, err
	}
	if strings.EqualFold(parent.ID[:64], occurrence.ID[:64]) {
		if parent.Height != childBlock.Height || !strings.EqualFold(parent.BlockHash, childBlock.Hash) || !childBlock.Verification.WitnessCommitment {
			return nil, fmt.Errorf("same-transaction parent witness unavailable")
		}
		parentRecord, err := source.app.authenticatedOrdFromBlock(parent.ID, childBlock)
		if err != nil {
			return nil, err
		}
		childRecord, err := source.app.authenticatedOrdFromBlock(occurrence.ID, childBlock)
		if err != nil {
			return nil, err
		}
		if parentRecord.TxIndex == nil || *parentRecord.TxIndex != parent.TxIndex || parentRecord.Index != int(parent.Index) {
			return nil, fmt.Errorf("selected District parent position mismatch")
		}
		state, reason := ordSameTransactionRelation079(childRecord, childBlock.Transactions[occurrence.TxIndex], parent.ID)
		if state != "verified" && state != "rejected" {
			return nil, fmt.Errorf("parent replay %s: %s", state, reason)
		}
		childBlocator := inscriptionCoordinate(uint32(occurrence.TxIndex), occurrence.Index, childBlock.Height)
		hash, err := storeParcelReplay079(ctx, source.root, parent.ID, occurrence.ID, parent.Ref, childBlocator, state, []blockView{childBlock}, canonical)
		if err != nil {
			return nil, err
		}
		return &bitmapParentProof{ReplayVersion: 3, ParentID: parent.ID, ChildID: occurrence.ID, RelationState: state, ParentBlocator: parent.Ref, ChildBlocator: childBlocator, ChildBlockHash: childBlock.Hash, EvaluationKind: "selected-chain-satpoint-replay-v3", Verified: true, EvidenceHash: hash}, nil
	}
	parentBlock, err := b.BlockByHeight(parent.Height)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(parentBlock.Hash, parent.BlockHash) || !parentBlock.Verification.WitnessCommitment || !childBlock.Verification.WitnessCommitment {
		return nil, fmt.Errorf("selected District parent witness unavailable")
	}
	parentRecord, err := source.app.authenticatedOrdFromBlock(parent.ID, parentBlock)
	if err != nil {
		return nil, err
	}
	if parentRecord.TxIndex == nil || *parentRecord.TxIndex != parent.TxIndex || parentRecord.Index != int(parent.Index) {
		return nil, fmt.Errorf("selected District parent position mismatch")
	}
	parentTx := parentBlock.Transactions[*parentRecord.TxIndex]
	r := newSatlineResolver(b)
	r.ctx = ctx
	r.cacheBlock(parentBlock)
	placement, err := deriveOrdProvenancePlacement(ctx, parentTx, parentRecord.Envelope, func(i int) (uint64, error) {
		in := parentTx.Inputs[i]
		return r.prevoutValue(in.PrevTxID, in.PrevVout, parentBlock.Height)
	})
	if err != nil {
		return nil, err
	}
	if placement.State != "verified_reveal_output" && placement.State != "fee_to_coinbase" {
		return nil, fmt.Errorf("parent placement unresolved: %s", placement.Note)
	}
	position, dest := placement.Position, parentTx
	if placement.State == "fee_to_coinbase" {
		outputs, err := sumOutputs(parentTx)
		if err != nil {
			return nil, err
		}
		prior, err := r.priorBlockFees(parentBlock, parent.TxIndex)
		if err != nil {
			return nil, err
		}
		position, err = addUint64(position-outputs, prior)
		if err == nil {
			position, err = addUint64(position, subsidyAtHeight(parentBlock.Height))
		}
		if err != nil {
			return nil, err
		}
		dest = parentBlock.Transactions[0]
	}
	vout, offset, ok := mapStreamPosition(position, dest.Outputs)
	if !ok {
		return nil, fmt.Errorf("parent placement is outside claimed outputs")
	}
	point := satlinePoint{TxID: dest.TxID, Vout: vout, Offset: offset, Height: parentBlock.Height, BlockHash: parentBlock.Hash, TxIndex: dest.Index}
	state, reason, _, _ := ordParentAtReveal(r, point, childBlock.Transactions[occurrence.TxIndex], childBlock.Height, occurrence.TxIndex, canonical)
	if state != "verified" && state != "rejected" {
		return nil, fmt.Errorf("parent replay %s: %s", state, reason)
	}
	blocks := make([]blockView, 0, len(b.blocks))
	for _, block := range b.blocks {
		blocks = append(blocks, block)
	}
	childBlocator := inscriptionCoordinate(uint32(occurrence.TxIndex), occurrence.Index, childBlock.Height)
	read := func(ctx context.Context, height int64, hash string) (*blockView, error) {
		block, err := source.blockAt(ctx, height)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(block.Hash, hash) {
			return nil, fmt.Errorf("Parcel proof source changed")
		}
		return &block, nil
	}
	hash, err := storeParcelReplayWithSource079(ctx, source.root, parent.ID, occurrence.ID, parent.Ref, childBlocator, state, blocks, canonical, b.negative, read)
	if err != nil {
		return nil, err
	}
	kind := "selected-chain-satpoint-replay-v3"
	if b.negative != nil {
		kind = "local-selected-raw-no-spend-attestation-v1"
	}
	proof := &bitmapParentProof{ReplayVersion: 3, ParentID: parent.ID, ChildID: occurrence.ID, RelationState: state, ParentBlocator: parent.Ref,
		ChildBlocator: childBlocator, ChildBlockHash: childBlock.Hash,
		EvaluationKind: kind, Verified: true, EvidenceHash: hash}
	if b.negative != nil {
		from, through := b.negative.Point.Height, b.negative.Through
		proof.ScanFrom, proof.ScanThrough = &from, &through
		proof.AuditNote = "Locally evaluated no-spend scan. Independent replay needs verified Bitcoin block bytes for this interval; an audit action is not yet available. Raw Off may leave those bytes unretained."
	}
	return proof, nil

}
