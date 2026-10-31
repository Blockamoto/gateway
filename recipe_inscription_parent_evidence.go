package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// Validate every owned extent before maintenance can remove anything. The
// receipt binds exact blocks and any local no-spend attestation to the value.
func recipeCheckParentPointBlobs(ctx context.Context, root string, f recipeImportedFact, verified map[string]int64) error {
	var proof bitmapParentProof
	if json.Unmarshal(f.Value.Object["proof"].Bytes, &proof) != nil {
		return fmt.Errorf("invalid owned relationship proof")
	}
	var receipt parcelReplayReceipt079
	var attestation *parcelScanAttestation079
	blocks := map[string]bool{}
	receipts := 0
	seen := map[string]bool{}
	for _, blob := range f.Blobs {
		if seen[blob.Role+":"+blob.Hash] {
			return fmt.Errorf("duplicate owned relationship blob")
		}
		seen[blob.Role+":"+blob.Hash] = true
		if err := recipeCheckSourceBlobRef(blob); err != nil {
			return err
		}
		if prior, have := verified[blob.Hash]; have && prior != blob.Bytes {
			return fmt.Errorf("conflicting owned source extents")
		}
		if verified[blob.Hash] == 0 {
			if err := recipeVerifySourceBlob(ctx, root, blob); err != nil {
				return err
			}
			if verified != nil {
				verified[blob.Hash] = blob.Bytes
			}
		}
		switch blob.Role {
		case "block":
			blocks[blob.Hash] = true
		case "receipt":
			receipts++
			if err := utxoReadJSON(ctx, recipeSourceBlobPath(root, blob.Hash), 1<<20, &receipt); err != nil {
				return err
			}
		case "attestation":
			if attestation != nil {
				return fmt.Errorf("duplicate owned scan attestation")
			}
			attestation = &parcelScanAttestation079{}
			if err := utxoReadJSON(ctx, recipeSourceBlobPath(root, blob.Hash), 64<<10, attestation); err != nil {
				return err
			}
		}
	}
	if receipts != 1 || receipt.Schema != 3 || receipt.Ord != ordParentReference || indexDigest(receipt) != proof.EvidenceHash || receipt.ParentID != proof.ParentID || receipt.ChildID != proof.ChildID || receipt.ParentBlocator != proof.ParentBlocator || receipt.ChildBlocator != proof.ChildBlocator || receipt.State != proof.RelationState || len(receipt.Blocks) == 0 || len(receipt.Blocks) > parcelReplayMaxBlocks079 || len(blocks) != len(receipt.Blocks) {
		return fmt.Errorf("owned relationship receipt binding changed")
	}
	anchors := map[int64]string{}
	for _, e := range f.Evidence {
		anchors[e.Height] = e.BlockHash
	}
	last := int64(-1)
	for _, block := range receipt.Blocks {
		if !blocks[block.SHA256] || block.Height <= last || anchors[block.Height] != block.Hash {
			return fmt.Errorf("owned relationship block binding changed")
		}
		last = block.Height
	}
	if receipt.Negative == nil {
		if attestation != nil || proof.ScanFrom != nil || proof.ScanThrough != nil || proof.EvaluationKind != "selected-chain-satpoint-replay-v3" {
			return fmt.Errorf("unexpected owned no-spend evidence")
		}
	} else {
		n := receipt.Negative
		if attestation == nil || attestation.Schema != 1 || indexDigest(*attestation) != n.Attestation || attestation.Point != n.Point || attestation.Through != n.Through || attestation.BlockCount != n.Through-n.Point.Height+1 || attestation.Evaluation != "locally_evaluated_complete_no_spend_v1" || !validHash(attestation.HashListDigest) || !validHash(attestation.MAC) || anchors[n.Through] != attestation.EndHash || proof.ScanFrom == nil || *proof.ScanFrom != n.Point.Height || proof.ScanThrough == nil || *proof.ScanThrough != n.Through || proof.EvaluationKind != "local-selected-raw-no-spend-attestation-v1" {
			return fmt.Errorf("owned local scan attestation binding changed")
		}
	}
	return ctx.Err()
}
