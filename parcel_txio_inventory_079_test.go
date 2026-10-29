package main

import (
	"context"
	"errors"
	"testing"
)

func TestParcelTXIOInputInventory079(t *testing.T) {
	ctx := context.Background()
	creator := testID(700)
	b0 := satIndexTestBlock(0, "", testCoinbase(creator, 50))
	b1 := satIndexTestBlock(1, b0.Hash, testCoinbase(testID(701), 50), testTx(testID(702), []inputView{{PrevTxID: creator, PrevVout: 0}}, 40))
	b0Batch := indexBatch{Checkpoint: indexCheckpoint{Height: 0, BlockHash: b0.Hash}, Spenders: &indexSpenderBlock{Version: 3, TransactionCount: 1, OutputCounts: []uint32{1}}}
	b0Catalog := txioTranslationBlock{Height: 0, BlockHash: b0.Hash, Transactions: []txioOrigin{{TxID: creator, TxIndex: 0, Outputs: 1, Coinbase: true}}}
	b1Batch := indexBatch{Checkpoint: indexCheckpoint{Height: 1, BlockHash: b1.Hash}, Spenders: &indexSpenderBlock{Version: 3, TransactionCount: 2, OutputCounts: []uint32{1, 1}, Rows: []indexSpendRecord{{TxIndex: 1, Vin: 0, Vout: 0, Output: "0.0.0"}}, CreationAnchors: map[string]string{"0": b0.Hash}}}
	b1Catalog := txioTranslationBlock{Height: 1, BlockHash: b1.Hash, Transactions: []txioOrigin{{TxID: testID(701), TxIndex: 0, Outputs: 1, Coinbase: true}, {TxID: testID(702), TxIndex: 1, Outputs: 1}}, Parents: []txioTranslationParent{{TxID: creator, Output: "0.0.0", BlockHash: b0.Hash}}}
	creation := func(_ context.Context, h int64) (indexBatch, txioTranslationBlock, error) {
		if h != 0 {
			return indexBatch{}, txioTranslationBlock{}, errors.New("missing")
		}
		return b0Batch, b0Catalog, nil
	}
	if err := verifyParcelTXIOInputInventory079(ctx, b1Batch, b1, b1Catalog, creation); err != nil {
		t.Fatal(err)
	}
	previousIndex := uint32(0)
	v2b0 := b0Batch
	v2b0.Spenders = &indexSpenderBlock{Version: 2, Origins: append([]txioOrigin(nil), b0Catalog.Transactions...)}
	v2b1 := b1Batch
	v2b1.Spenders = &indexSpenderBlock{Version: 2, Origins: append([]txioOrigin(nil), b1Catalog.Transactions...), PreviousTransactions: []string{creator}, Rows: []indexSpendRecord{{TxIndex: 1, Vin: 0, Vout: 0, Output: "0.0.0", PreviousTransaction: &previousIndex}}, CreationAnchors: map[string]string{"0": b0.Hash}}
	v2creation := func(context.Context, int64) (indexBatch, txioTranslationBlock, error) { return v2b0, b0Catalog, nil }
	if err := verifyParcelTXIOInputInventory079(ctx, v2b1, b1, b1Catalog, v2creation); err != nil {
		t.Fatalf("v2 full inventory: %v", err)
	}
	missing := b1
	missing.Transactions = append([]transactionView(nil), b1.Transactions...)
	missing.Transactions[1].Inputs = append(append([]inputView(nil), b1.Transactions[1].Inputs...), inputView{PrevTxID: testID(703), PrevVout: 0})
	if err := verifyParcelTXIOInputInventory079(ctx, b1Batch, missing, b1Catalog, creation); err == nil {
		t.Fatal("missing trailing input row accepted")
	}
	wrong := b1Catalog
	wrong.Parents = append([]txioTranslationParent(nil), b1Catalog.Parents...)
	wrong.Parents[0].TxID = testID(999)
	if err := verifyParcelTXIOInputInventory079(ctx, b1Batch, b1, wrong, creation); err == nil {
		t.Fatal("wrong previous transaction alias accepted")
	}
	replaced := func(context.Context, int64) (indexBatch, txioTranslationBlock, error) {
		changed := b0Batch
		changed.Checkpoint.BlockHash = testID(998)
		return changed, b0Catalog, nil
	}
	if err := verifyParcelTXIOInputInventory079(ctx, b1Batch, b1, b1Catalog, replaced); err == nil {
		t.Fatal("replaced creation block accepted")
	}
	badOriginal := b0Catalog
	badOriginal.Transactions = append([]txioOrigin(nil), b0Catalog.Transactions...)
	badOriginal.Transactions[0].TxID = testID(997)
	wrongCreation := func(context.Context, int64) (indexBatch, txioTranslationBlock, error) {
		return b0Batch, badOriginal, nil
	}
	if err := verifyParcelTXIOInputInventory079(ctx, b1Batch, b1, b1Catalog, wrongCreation); err == nil {
		t.Fatal("wrong original transaction accepted")
	}
	badBytes := b1
	badBytes.Verification.MerkleRoot = false
	if err := verifyParcelTXIOInputInventory079(ctx, b1Batch, badBytes, b1Catalog, creation); err == nil {
		t.Fatal("unverified spending block accepted")
	}
}

func TestParcelTXIOInputInventorySameBlock079(t *testing.T) {
	ctx := context.Background()
	cb, first, second := testID(720), testID(721), testID(722)
	block := satIndexTestBlock(1, testID(719), testCoinbase(cb, 50), testTx(first, []inputView{{PrevTxID: cb, PrevVout: 0}}, 40), testTx(second, []inputView{{PrevTxID: first, PrevVout: 0}}, 30))
	batch := indexBatch{Checkpoint: indexCheckpoint{Height: 1, BlockHash: block.Hash}, Spenders: &indexSpenderBlock{Version: 3, TransactionCount: 3, OutputCounts: []uint32{1, 1, 1}, Rows: []indexSpendRecord{{TxIndex: 1, Vin: 0, Vout: 0, Output: "0.0.1"}, {TxIndex: 2, Vin: 0, Vout: 0, Output: "0.1.1"}}, CreationAnchors: map[string]string{"1": block.Hash}}}
	catalog := txioTranslationBlock{Height: 1, BlockHash: block.Hash, Transactions: []txioOrigin{{TxID: cb, TxIndex: 0, Outputs: 1, Coinbase: true}, {TxID: first, TxIndex: 1, Outputs: 1}, {TxID: second, TxIndex: 2, Outputs: 1}}, Parents: []txioTranslationParent{{TxID: cb, Output: "0.0.1", BlockHash: block.Hash}, {TxID: first, Output: "0.1.1", BlockHash: block.Hash}}}
	if err := verifyParcelTXIOInputInventory079(ctx, batch, block, catalog, nil); err != nil {
		t.Fatalf("same-block earlier origins: %v", err)
	}
	bad := *batch.Spenders
	bad.Rows = append([]indexSpendRecord(nil), batch.Spenders.Rows...)
	bad.Rows[1].Output = "0.2.1"
	batch.Spenders = &bad
	if err := verifyParcelTXIOInputInventory079(ctx, batch, block, catalog, nil); err == nil {
		t.Fatal("same-block origin at consuming transaction accepted")
	}
}
