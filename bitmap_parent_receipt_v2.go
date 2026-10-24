package main

import (
	"context"
	"fmt"
	"path/filepath"
)

// This authenticates the result of local evaluation, not a standalone Sat
// proof. Existing Sat state owns its IDs; Bitmap keeps no copy of those pages.
type bitmapParentReceiptV2 struct {
	Schema int               `json:"schema"`
	Proof  bitmapParentProof `json:"proof"`
}

func bitmapParentReceiptPathV2(root, hash string) string {
	return filepath.Join(root, "indexes", "bitmap-parent-receipts-v2", hash[:2], hash+".json")
}
func storeBitmapParentReceiptV2(ctx context.Context, root string, proof *bitmapParentProof) error {
	proof.ParentID = ""
	copy := *proof
	copy.EvidenceHash = ""
	receipt := bitmapParentReceiptV2{Schema: 2, Proof: copy}
	hash := indexDigest(receipt)
	if err := writeBitmapChainJSON(ctx, bitmapParentReceiptPathV2(root, hash), receipt); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(bitmapParentReceiptPathV2(root, hash))); err != nil {
		return err
	}
	proof.EvidenceHash = hash
	return nil
}
func verifyBitmapParentReceiptV2(ctx context.Context, root string, proof *bitmapParentProof) error {
	if !proof.Verified || proof.ParentID != "" || !bitmapValidInscriptionBlocator(proof.ParentBlocator) || !bitmapValidInscriptionBlocator(proof.ChildBlocator) || !validHash(proof.ChildBlockHash) || !validHash(proof.EvidenceHash) || proof.InputIndex < 0 || proof.Offset >= proof.Value || proof.EvaluationKind != "locally_evaluated_creation_relationship_v2" || proof.Snapshot.Commitment != checkpointHash(proof.Snapshot) {
		return fmt.Errorf("Bitmap parent evaluation receipt is invalid")
	}
	var receipt bitmapParentReceiptV2
	if err := readBitmapJSON(ctx, bitmapParentReceiptPathV2(root, proof.EvidenceHash), &receipt); err != nil {
		return err
	}
	expected := *proof
	expected.EvidenceHash = ""
	if receipt.Schema != 2 || indexDigest(receipt) != proof.EvidenceHash || indexDigest(receipt.Proof) != indexDigest(expected) {
		return fmt.Errorf("Bitmap parent evaluation receipt authentication mismatch")
	}
	return nil
}
