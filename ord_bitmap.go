package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// ordBitmapAddress is a projection of an authenticated, selected saved result.
// It is never derived solely from the inscription's text.
type ordBitmapAddress struct {
	Address  string `json:"address"`
	Kind     string `json:"kind"`
	Instance string `json:"instance"`
	Through  int64  `json:"through"`
}

func (a *app) enrichOrdBitmap(ctx context.Context, rec *ordRecord) {
	rec.BitmapAddresses = nil
	if rec.TxIndex == nil || *rec.TxIndex < 0 || uint64(*rec.TxIndex) > uint64(^uint32(0)) || rec.Index < 0 || uint64(rec.Index) > uint64(^uint32(0)) || rec.Height < 0 || !validHash(rec.BlockHash) {
		return
	}
	position := inscriptionCoordinate(uint32(*rec.TxIndex), uint32(rec.Index), rec.Height)
	// Legacy Parcel generations never participate in badge recognition.
	for _, id := range []string{bitmapStandardDistrictID, bitmapReplayParcelID} {
		bitmapSnapshotMu.RLock()
		h, c := a.savedOrdBitmapCandidate(ctx, rec, position, id)
		bitmapSnapshotMu.RUnlock()
		if h == nil {
			continue
		}
		authority := a.currentChainAuthority()
		if a.utxoSelectedChainState(ctx, &utxoCheckpoint{Height: h.Height, BlockHash: h.BlockHash}, authority) != "selected_chain" {
			continue
		}
		target, err := a.utxoSelectedTarget(ctx, authority, rec.Height)
		if err != nil || target.HashDisplay != rec.BlockHash {
			continue
		}
		block, err := a.fetchBlockAtLocationContext(ctx, rec.Height, rec.BlockHash, "Bitmap inscription identity")
		if err != nil || !ordBitmapWitnessMatches(rec, block) {
			continue
		}
		if id == bitmapReplayParcelID {
			// The receipt binds the selected District and child to replayed
			// selected-chain evidence. Hold the page lease while reading it.
			bitmapSnapshotMu.RLock()
			err = verifyBitmapCandidateEvidence079(ctx, a, id, c, a.addressSelectedHashes(ctx, authority))
			bitmapSnapshotMu.RUnlock()
			if err != nil {
				continue
			}
		}
		authority = a.currentChainAuthority()
		if a.utxoSelectedChainState(ctx, &utxoCheckpoint{Height: h.Height, BlockHash: h.BlockHash}, authority) != "selected_chain" {
			continue
		}
		target, err = a.utxoSelectedTarget(ctx, authority, rec.Height)
		if err != nil || target.HashDisplay != rec.BlockHash {
			continue
		}
		bitmapSnapshotMu.RLock()
		latest, err := readBitmapDevelopmentHead(ctx, a.dataDir, id)
		bitmapSnapshotMu.RUnlock()
		if err != nil || latest == nil || latest.Commitment != h.Commitment {
			continue
		}
		address := fmt.Sprintf("%d.bitmap", *c.District)
		if c.Transaction != nil {
			address = fmt.Sprintf("%d.%d.bitmap", *c.Transaction, *c.District)
		}
		rec.BitmapAddresses = append(rec.BitmapAddresses, ordBitmapAddress{Address: address, Kind: c.Kind, Instance: id, Through: h.Height})
	}
}

// Caller holds bitmapSnapshotMu.RLock while accessing immutable pages.
func (a *app) savedOrdBitmapCandidate(ctx context.Context, rec *ordRecord, position, id string) (*bitmapDevelopmentHead, bitmapDevelopmentCandidate) {
	if id != bitmapStandardDistrictID && id != bitmapReplayParcelID {
		return nil, bitmapDevelopmentCandidate{}
	}
	var empty bitmapDevelopmentCandidate
	h, err := readBitmapDevelopmentHead(ctx, a.dataDir, id)
	if err != nil || h == nil || rec.Height < h.SegmentStart || rec.Height > h.Height {
		return nil, empty
	}
	tree := func(root string) *ordinalTree {
		return newOrdinalTree(filepath.Join(bitmapDevelopmentDir(a.dataDir, id), "pages"), root).withContext(ctx)
	}
	var c bitmapDevelopmentCandidate
	have, err := tree(h.CandidateRoot).get(bitmapCandidateKey(bitmapDevelopmentCandidate{RevealHeight: rec.Height, TxIndex: *rec.TxIndex, InscriptionIndex: uint32(rec.Index)}), &c)
	if err != nil || !have || c.District == nil || c.Blocator != position || c.RevealHash != rec.BlockHash || c.Selection != "selected" || c.Eligibility != "eligible" {
		return nil, empty
	}
	var winner bitmapDevelopmentCandidate
	have, err = tree(h.WinnerRoot).get(bitmapTargetKey(*c.District, c.Transaction), &winner)
	if err != nil || !have || winner.Blocator != position || winner.RevealHash != rec.BlockHash || winner.Selection != "selected" || winner.Eligibility != "eligible" || indexDigest(winner) != indexDigest(c) {
		return nil, empty
	}
	if id == bitmapStandardDistrictID {
		if c.Kind != "district" || c.Transaction != nil || c.Provenance != nil {
			return nil, empty
		}
	} else {
		if c.Kind != "parcel" || c.Transaction == nil || c.Provenance == nil || c.Provenance.RelationState != "verified" || c.Provenance.ReplayVersion != 3 || !strings.EqualFold(c.Provenance.ChildID, rec.ID) || c.Provenance.ChildBlocator != position || c.Provenance.ChildBlockHash != rec.BlockHash {
			return nil, empty
		}
	}
	var outcome bitmapDevelopmentBlock
	have, err = tree(h.BlockRoot).get(fmt.Sprintf("%016x", rec.Height), &outcome)
	if err != nil || !have || outcome.Height != rec.Height || outcome.BlockHash != rec.BlockHash || !outcome.BlockEvaluated || !outcome.CompleteScope {
		return nil, empty
	}
	for _, b := range outcome.WinnerBlocators {
		if b == position {
			return h, c
		}
	}
	return nil, empty
}

// A reveal position is useful only after the block's witness commitment and
// parser bind it to this exact conventional inscription ID.
func ordBitmapWitnessMatches(rec *ordRecord, block blockView) bool {
	if rec.TxIndex == nil || !integrityVerified(block) || !block.Verification.WitnessCommitment || block.Height != rec.Height || !strings.EqualFold(block.Hash, rec.BlockHash) {
		return false
	}
	parsed, err := extractInscriptionOccurrences(block)
	if err != nil {
		return false
	}
	for _, occurrence := range parsed.Occurrences {
		if occurrence.TxIndex == *rec.TxIndex && int(occurrence.Index) == rec.Index && strings.EqualFold(occurrence.ID, rec.ID) {
			return true
		}
	}
	return false
}
