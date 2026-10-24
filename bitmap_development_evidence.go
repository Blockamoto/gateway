package main

// Checked parent evidence is detached from Sats' rotating retention. A Parcel
// receipt must not rely on another consumer keeping an old snapshot forever.
import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"./internal/updateapply"
)

type bitmapParentEvidence struct {
	Schema   int                       `json:"schema"`
	Proof    bitmapParentProof         `json:"proof"`
	SatBatch indexBatch                `json:"sat_batch"`
	Pages    map[string]ordinalPage    `json:"pages"`
	Facts    map[string]outputFactPage `json:"facts"`
}

func storeBitmapParentEvidence(ctx context.Context, root string, e *satEditor077, b indexBatch, out satIndexOutput, proof *bitmapParentProof) error {
	if proof.ParentBlocator != "" {
		return storeBitmapParentReceiptV2(ctx, root, proof)
	}
	bundle := bitmapParentEvidence{Schema: 1, Proof: *proof, SatBatch: b, Pages: map[string]ordinalPage{}, Facts: map[string]outputFactPage{}}
	for _, tree := range []*ordinalTree{e.ids, e.locations, e.outputs, e.native} {
		for hash, page := range tree.cache {
			bundle.Pages[hash] = page
		}
	}
	if out.RangesRoot != "" {
		tree := newOrdinalTree(e.s.ordinalPages(), out.RangesRoot).withContext(ctx)
		if err := tree.walk(func(ordinalKV) error { return nil }); err != nil {
			return err
		}
		for hash, page := range tree.cache {
			bundle.Pages[hash] = page
		}
	}
	for hash, page := range e.facts.pages {
		bundle.Facts[hash] = page.page
	}
	if out.FactRef == nil {
		return fmt.Errorf("parent fact reference required")
	}
	if _, ok := bundle.Facts[out.FactRef.Page]; !ok {
		page, err := readOutputFactPageContext(ctx, root, out.FactRef.Page)
		if err != nil {
			return err
		}
		bundle.Facts[out.FactRef.Page] = page
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	if len(raw) > 16<<20 {
		return fmt.Errorf("checked parent evidence exceeds development proof budget")
	}
	hash := indexDigest(bundle)
	path := filepath.Join(root, "indexes", "bitmap-development-v1", "parent-evidence", hash[:2], hash+".json")
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	if err := atomicWriteBytes(path, raw); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	proof.EvidenceHash = hash
	return nil
}
func verifyBitmapParentEvidence(ctx context.Context, root string, proof *bitmapParentProof) error {
	if proof == nil {
		return nil
	}
	if proof.ParentBlocator != "" {
		return verifyBitmapParentReceiptV2(ctx, root, proof)
	}
	if !proof.Verified || !validHash(proof.EvidenceHash) {
		return fmt.Errorf("checked Parcel parent evidence is unavailable")
	}
	var bundle bitmapParentEvidence
	path := filepath.Join(root, "indexes", "bitmap-development-v1", "parent-evidence", proof.EvidenceHash[:2], proof.EvidenceHash+".json")
	if err := readBitmapJSON(ctx, path, &bundle); err != nil {
		return err
	}
	expected := *proof
	expected.EvidenceHash = ""
	if bundle.Schema != 1 || indexDigest(bundle) != proof.EvidenceHash || indexDigest(bundle.Proof) != indexDigest(expected) || bundle.SatBatch.Sats == nil || bundle.SatBatch.Checkpoint.Commitment != checkpointHash(bundle.SatBatch.Checkpoint) || bundle.SatBatch.Checkpoint.RecordsHash != batchRecordsHash(bundle.SatBatch) {
		return fmt.Errorf("Parcel parent evidence authentication mismatch")
	}
	if indexDigest(bundle.SatBatch.Checkpoint) != indexDigest(proof.Snapshot) {
		return fmt.Errorf("Parcel parent snapshot mismatch")
	}
	for hash, page := range bundle.Pages {
		if indexDigest(page) != hash {
			return fmt.Errorf("Parcel parent page checksum mismatch")
		}
	}
	for hash, page := range bundle.Facts {
		if indexDigest(page) != hash {
			return fmt.Errorf("Parcel parent facts checksum mismatch")
		}
	}
	p := bundle.SatBatch.Sats
	tree := func(rootHash string) *ordinalTree {
		t := newOrdinalTree(filepath.Join(root, "indexes", "bitmap-development-v1", "unavailable-proof-page"), rootHash).withContext(ctx)
		t.cache = bundle.Pages
		return t
	}
	var identity inscriptionSatResult
	have, err := tree(p.InscriptionRoot).get(ordinalInscriptionKey(proof.ParentID), &identity)
	if err != nil {
		return err
	}
	if !have || !identity.Known || identity.SatNumber == nil || *identity.SatNumber != proof.SatNumber {
		return fmt.Errorf("parent identity proof mismatch")
	}
	var loc satIndexLocation
	have, err = tree(p.LocationRoot).predecessor(satRangeKey(proof.SatNumber), &loc)
	if err != nil {
		return err
	}
	if !have || loc.State != "unspent" || loc.Output != proof.Output || proof.SatNumber < loc.Start || proof.SatNumber >= loc.End || loc.Offset+proof.SatNumber-loc.Start != proof.Offset {
		return fmt.Errorf("parent location proof mismatch")
	}
	key, err := satOutputKey077(proof.Output)
	if err != nil {
		return err
	}
	var output satIndexOutput
	have, err = tree(p.UTXORoot).get(key, &output)
	if err != nil {
		return err
	}
	if !have || output.Output != proof.Output || output.State != "unspent" || output.FactRef == nil {
		return fmt.Errorf("parent output proof mismatch")
	}
	page, ok := bundle.Facts[output.FactRef.Page]
	if !ok || int(output.FactRef.Row) >= len(page.Rows) {
		return fmt.Errorf("parent fact proof missing")
	}
	fact := page.Rows[output.FactRef.Row]
	point, _ := satPosition077(proof.Output)
	if fact.Value == nil || *fact.Value != proof.Value || fact.TxIndex != point.TxIndex || int(fact.Vout) != point.IOIndex || page.Height != point.Height || proof.Offset >= proof.Value {
		return fmt.Errorf("parent value proof mismatch")
	}
	var native satNative077
	have, err = tree(p.TransactionRoot).get(utxoTransactionKey(point.Height, point.TxIndex), &native)
	if err != nil {
		return err
	}
	if !have || !validHash(native.TxID) || uint64(point.IOIndex) >= native.Count || !validHash(page.BlockHash) {
		return fmt.Errorf("parent native output proof mismatch")
	}
	return nil
}
