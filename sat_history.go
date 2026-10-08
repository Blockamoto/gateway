package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// These receipts retain the FIFO-relevant layout and checked input values, not
// transaction serialization or witness data. Only Satline consumes this adapter;
// it must never satisfy generic raw transaction/content verification requests.
const satHistorySource = "local_sat_movement_history"

func satHistorySpendKey(txid string, vout uint32) string {
	sum := sha256.Sum256([]byte("sat-movement-spend:" + satlineOutpointKey(txid, vout)))
	return hex.EncodeToString(sum[:])
}
func satHistoryLocatorKeys(snapshot *satIndexSnapshot) []string {
	keys := []string{}
	for _, movement := range snapshot.Movements {
		keys = append(keys, movement.TxID)
		for _, input := range movement.Inputs {
			keys = append(keys, satHistorySpendKey(input.TxID, input.Vout))
		}
	}
	return keys
}
func satHistoryLocatorMovement(snapshot *satIndexSnapshot, row int) (satIndexMovement, int, bool) {
	for _, movement := range snapshot.Movements {
		if row == 0 {
			return movement, -1, true
		}
		row--
		if row < len(movement.Inputs) {
			return movement, row, row >= 0
		}
		row -= len(movement.Inputs)
	}
	return satIndexMovement{}, -1, false
}
func satMovementTransaction(movement satIndexMovement) transactionView {
	tx := transactionView{TxID: movement.TxID, Index: movement.Index, Coinbase: movement.Coinbase}
	for i, input := range movement.Inputs {
		tx.Inputs = append(tx.Inputs, inputView{N: i, PrevTxID: input.TxID, PrevVout: input.Vout})
	}
	for i, value := range movement.Outputs {
		tx.Outputs = append(tx.Outputs, outputView{N: i, ValueSats: value})
	}
	return tx
}
func satHistoryBlock(batch indexBatch) (blockView, error) {
	if batch.Sats == nil || len(batch.Sats.Movements) == 0 {
		return blockView{}, fmt.Errorf("movement history was not retained at block %d", batch.Checkpoint.Height)
	}
	block := blockView{Height: batch.Checkpoint.Height, Hash: batch.Checkpoint.BlockHash, PreviousBlockHash: batch.PreviousBlockHash, TransactionCount: uint64(len(batch.Sats.Movements)), SourceNetwork: satHistorySource, VerificationState: "header_anchored", Note: "FIFO movement layout retained from checked Sat indexing; raw scripts and witness bytes are not retained by this receipt."}
	block.Transactions = make([]transactionView, len(batch.Sats.Movements))
	for _, movement := range batch.Sats.Movements {
		if movement.Index < 0 || movement.Index >= len(block.Transactions) || block.Transactions[movement.Index].TxID != "" || !validHash(movement.TxID) || movement.Coinbase != (movement.Index == 0) {
			return blockView{}, fmt.Errorf("invalid retained movement layout")
		}
		block.Transactions[movement.Index] = satMovementTransaction(movement)
	}
	return block, nil
}
func (a *app) retainedSatStore(ctx context.Context) (*indexStore, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	s, e := indexStoreHead(a.dataDir, "sat-state")
	if e != nil {
		return nil, e
	}
	if s.checkpoint == nil || a.indexChainState(s.checkpoint) != "selected_chain" {
		return nil, fmt.Errorf("retained Sat history has no selected-chain snapshot")
	}
	return s, nil
}
func (a *app) retainedSatBatch(ctx context.Context, s *indexStore, height int64) (indexBatch, error) {
	if height < s.checkpoint.From || height > s.checkpoint.Height {
		return indexBatch{}, fmt.Errorf("height outside retained Sat snapshot")
	}
	commit, ready, e := s.activeLocatorCommitContext(ctx, height)
	if e != nil {
		return indexBatch{}, e
	}
	if !ready || commit == "" {
		return indexBatch{}, fmt.Errorf("retained Sat history membership unavailable; resume required")
	}
	batch, current, e := a.validatedLocatorBatch(ctx, s, indexLocatorRecord{Commit: commit, Height: height})
	if e != nil {
		return batch, e
	}
	if !current {
		return batch, fmt.Errorf("retained Sat history is outside selected chain")
	}
	if batch.Sats == nil || len(batch.Sats.Movements) == 0 {
		return batch, fmt.Errorf("movement history was not retained at block %d", height)
	}
	return batch, nil
}
func (a *app) retainedSatBlock(ctx context.Context, height int64) (blockView, error) {
	s, e := a.retainedSatStore(ctx)
	if e != nil {
		return blockView{}, e
	}
	batch, e := a.retainedSatBatch(ctx, s, height)
	if e != nil {
		return blockView{}, e
	}
	return satHistoryBlock(batch)
}
func (a *app) retainedSatTransaction(ctx context.Context, id string, before int64) (satlineTxEvidence, error) {
	s, e := a.retainedSatStore(ctx)
	if e != nil {
		return satlineTxEvidence{}, e
	}
	rows, e := s.bitcoinLocatorRecords(ctx, id)
	if e != nil {
		return satlineTxEvidence{}, e
	}
	var best *satlineTxEvidence
	for _, row := range rows {
		if before >= 0 && row.Height > before {
			continue
		}
		batch, current, e := a.validatedLocatorBatch(ctx, s, row)
		if e != nil {
			return satlineTxEvidence{}, e
		}
		if !current {
			continue
		}
		if batch.Sats == nil {
			return satlineTxEvidence{}, fmt.Errorf("missing movement snapshot")
		}
		movement, vin, ok := satHistoryLocatorMovement(batch.Sats, row.TxIndex)
		if !ok || vin != -1 || movement.TxID != id {
			return satlineTxEvidence{}, fmt.Errorf("retained transaction pointer mismatch")
		}
		if best == nil || row.Height > best.Height {
			best = &satlineTxEvidence{Tx: satMovementTransaction(movement), Height: row.Height, BlockHash: batch.Checkpoint.BlockHash, VerificationState: "header_anchored", Source: satHistorySource}
		}
	}
	if best == nil {
		return satlineTxEvidence{}, fmt.Errorf("transaction movement history not retained")
	}
	// A partial history must not resurrect a coinbase displaced by BIP30.
	if !transactionFitsHeightContext(best.Height, best.Tx.Coinbase, before) {
		return satlineTxEvidence{}, fmt.Errorf("later BIP30 incarnation requires retained contextual evidence")
	}
	if _, oldCoinbase := bip30ReplacementHeight(best.Height); best.Tx.Coinbase && oldCoinbase && before < 0 {
		return satlineTxEvidence{}, fmt.Errorf("historical duplicate coinbase requires a containing-block bound")
	}
	return *best, nil
}
func (a *app) retainedSatSpender(ctx context.Context, id string, vout uint32) (spenderLookupView, error) {
	s, e := a.retainedSatStore(ctx)
	if e != nil {
		return spenderLookupView{}, e
	}
	out := spenderLookupView{Outpoint: satlineOutpointKey(id, vout), Provider: satHistorySource, VerificationState: "header_anchored", MempoolState: "unavailable", ConfirmedState: "unknown", Note: "A retained movement miss is not proof of unspentness."}
	batch, e := s.readBatch(s.checkpoint.Commitment)
	if e != nil {
		return out, e
	}
	if batch.Sats == nil {
		return out, fmt.Errorf("missing Sat state")
	}
	var output satIndexOutput
	known, e := newOrdinalTree(s.ordinalPages(), batch.Sats.UTXORoot).withContext(ctx).get(ordinalOutpointKey(id, vout), &output)
	if e != nil {
		return out, e
	}
	// Satline resolves the historical original in its finite BIP30 context
	// first. Generic outpoints refer to the current incarnation, so its UTXO
	// wins over a retained spend of a displaced original.
	if known && output.State == "unspent" {
		out.ConfirmedState = "unspent_at_snapshot"
		out.Snapshot = &chainSnapshot{Network: s.definition.Network, Height: s.checkpoint.Height, Hash: s.checkpoint.BlockHash, Source: satHistorySource}
		out.Note = "Unspent output present in the selected continuous Sat snapshot; later blocks and mempool are outside this claim."
		return out, nil
	}
	latestIncarnation := int64(-1)
	for _, incarnation := range batch.Sats.DuplicateCoinbases {
		if incarnation.TxID == id && incarnation.Height > latestIncarnation {
			latestIncarnation = incarnation.Height
		}
	}
	rows, e := s.bitcoinLocatorRecords(ctx, satHistorySpendKey(id, vout))
	if e != nil {
		return out, e
	}
	for _, row := range rows {
		if row.Height < latestIncarnation {
			continue
		}
		batch, current, e := a.validatedLocatorBatch(ctx, s, row)
		if e != nil {
			return out, e
		}
		if !current {
			continue
		}
		if batch.Sats == nil {
			return out, fmt.Errorf("missing movement snapshot")
		}
		movement, vin, ok := satHistoryLocatorMovement(batch.Sats, row.TxIndex)
		if !ok || vin < 0 || vin >= len(movement.Inputs) || movement.Inputs[vin].TxID != id || movement.Inputs[vin].Vout != vout {
			return out, fmt.Errorf("retained spender pointer mismatch")
		}
		if out.Found {
			return spenderLookupView{}, fmt.Errorf("ambiguous retained BIP30 spender; source-incarnation context required")
		}
		tx := satMovementTransaction(movement)
		out.Transaction = &tx
		out.SpendingInput = &tx.Inputs[vin]
		out.Found = true
		out.ConfirmedState = "confirmed_spent"
		out.SpendingTxID = tx.TxID
		out.SpendingVin = vin
		out.Height = row.Height
		out.BlockHash = batch.Checkpoint.BlockHash
		out.Note = "Confirmed FIFO movement retained from checked Sat indexing."
	}
	if out.Found {
		return out, nil
	}
	return out, fmt.Errorf("retained movement evidence does not establish this output's spender")
}
func (a *app) retainedSatInputValues(ctx context.Context, tx transactionView, height int64, hash string) (map[string]uint64, error) {
	s, e := a.retainedSatStore(ctx)
	if e != nil {
		return nil, e
	}
	batch, e := a.retainedSatBatch(ctx, s, height)
	if e != nil {
		return nil, e
	}
	if !strings.EqualFold(batch.Checkpoint.BlockHash, hash) {
		return nil, fmt.Errorf("retained input block mismatch")
	}
	for _, m := range batch.Sats.Movements {
		if m.TxID != tx.TxID {
			continue
		}
		if len(m.Inputs) != len(tx.Inputs) {
			return nil, fmt.Errorf("retained input count mismatch")
		}
		values := map[string]uint64{}
		for i, in := range m.Inputs {
			if in.TxID != tx.Inputs[i].PrevTxID || in.Vout != tx.Inputs[i].PrevVout {
				return nil, fmt.Errorf("retained input identity mismatch")
			}
			values[satlineOutpointKey(in.TxID, in.Vout)] = in.Value
		}
		return values, nil
	}
	return nil, fmt.Errorf("retained input values unavailable")
}
