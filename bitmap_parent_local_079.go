package main

import (
	"fmt"
	"strings"
)

// Reading an optional hint must not turn into an unbounded locator-shard scan.
const parcelLocalHintBytes079 int64 = 16 << 20

// These are optional local hints only. No index repair, range scan, or network
// fetch is initiated; every hit is rechecked against a selected retained block.
func (b *parcelCaptureBackend079) localSpender(point satlinePoint) (spenderLookupView, bool) {
	if b.ctx.Err() != nil {
		return spenderLookupView{}, false
	}
	// Graph's generic lookup uses a background context; the optional hint must
	// not outlive the reviewed run or switch its selected-chain authority.
	store, err := indexStoreHead(b.app.dataDir, "txo-spender")
	if err != nil || store.checkpoint == nil {
		return spenderLookupView{}, false
	}
	position := outputCoordinate(int(point.Vout), point.TxIndex, point.Height)
	rows, err := store.bitcoinLocatorRecordsWithBudget(b.ctx, txioKey("output", position), false, 128, parcelLocalHintBytes079)
	if err != nil {
		return spenderLookupView{}, false
	}
	for _, pointer := range rows {
		active, ready, err := store.activeLocatorCommitContext(b.ctx, pointer.Height)
		if err != nil || !ready || active != pointer.Commit {
			continue
		}
		batch, err := store.readBatch(pointer.Commit)
		if err != nil || batch.Spenders == nil || batch.Checkpoint.Height != pointer.Height {
			continue
		}
		n := len(batch.Spenders.Rows)
		offset := 2 * n
		if batch.Spenders.Version == 3 {
			offset = n
		}
		if pointer.TxIndex < offset || pointer.TxIndex >= offset+n {
			continue
		}
		row, err := store.expandedTXIORow(b.ctx, batch, pointer.TxIndex-offset)
		if err != nil || row.Output != position || !strings.EqualFold(row.PrevTxID, point.TxID) || row.Vout != point.Vout {
			continue
		}
		if batch.Spenders.CreationAnchors[fmt.Sprint(point.Height)] != point.BlockHash {
			continue
		}
		if v, ok := b.checkedLocalSpender(point, pointer.Height, batch.Checkpoint.BlockHash, row.TxID, row.Vin); ok {
			return v, true
		}
	}
	return spenderLookupView{}, false
}

func (b *parcelCaptureBackend079) checkedLocalSpender(point satlinePoint, height int64, blockHash, txid string, vin int) (spenderLookupView, bool) {
	if height < point.Height || height > b.childHeight {
		return spenderLookupView{}, false
	}
	block, err := b.BlockByHeight(height)
	if err != nil || !strings.EqualFold(block.Hash, blockHash) {
		return spenderLookupView{}, false
	}
	for _, tx := range block.Transactions {
		if !strings.EqualFold(tx.TxID, txid) || height == point.Height && tx.Index <= point.TxIndex {
			continue
		}
		if vin < 0 || vin >= len(tx.Inputs) || tx.Inputs[vin].Coinbase || !strings.EqualFold(tx.Inputs[vin].PrevTxID, point.TxID) || tx.Inputs[vin].PrevVout != point.Vout {
			continue
		}
		copyTx := tx
		return spenderLookupView{Found: true, ConfirmedState: "confirmed_spent", SpendingTxID: tx.TxID, SpendingVin: vin, Height: height, BlockHash: block.Hash, Provider: "checked_local_link", VerificationState: block.VerificationState, Transaction: &copyTx}, true
	}
	return spenderLookupView{}, false
}

func (b *parcelCaptureBackend079) localTransaction(id string, before int64) (satlineTxEvidence, bool) {
	if b.ctx.Err() != nil {
		return satlineTxEvidence{}, false
	}
	// A saved locator can narrow an explicit Raw lookup. It is not itself proof
	// of the transaction bytes or of a negative search result.
	store, err := indexStoreHead(b.app.dataDir, "tx-locator")
	if err != nil || store.checkpoint == nil {
		return satlineTxEvidence{}, false
	}
	rows, err := store.bitcoinLocatorRecordsWithBudget(b.ctx, id, false, 128, parcelLocalHintBytes079)
	if err != nil {
		return satlineTxEvidence{}, false
	}
	var best *satlineTxEvidence
	for _, pointer := range rows {
		if pointer.Height > before || best != nil && pointer.Height <= best.Height {
			continue
		}
		active, ready, err := store.activeLocatorCommitContext(b.ctx, pointer.Height)
		if err != nil || !ready || active != pointer.Commit {
			continue
		}
		batch, err := store.readBatch(pointer.Commit)
		if err != nil || batch.Checkpoint.Height != pointer.Height {
			continue
		}
		entries := transactionBatchEntries(batch)
		if pointer.TxIndex < 0 || pointer.TxIndex >= len(entries) || !strings.EqualFold(entries[pointer.TxIndex].TxID, id) {
			continue
		}
		index := int(entries[pointer.TxIndex].TxIndex)
		block, err := b.BlockByHeight(pointer.Height)
		if err != nil || block.Hash != batch.Checkpoint.BlockHash || index < 0 || index >= len(block.Transactions) {
			continue
		}
		tx := block.Transactions[index]
		if !strings.EqualFold(tx.TxID, id) || !transactionFitsHeightContext(pointer.Height, tx.Coinbase, before) {
			continue
		}
		copy := satlineTxEvidence{Tx: tx, Height: pointer.Height, BlockHash: block.Hash, VerificationState: block.VerificationState, Source: "checked_local_locator"}
		best = &copy
	}
	if best == nil {
		return satlineTxEvidence{}, false
	}
	return *best, true
}
