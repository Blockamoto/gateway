package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
)

// Fast prevout/value path: trusted local Core evidence is labelled as such.
// Ord content always fetches a complete block and checks the witness commitment.
func coreTransactionEvidence(ctx context.Context, s appSettings, id string, before int64) (satlineTxEvidence, error) {
	c, e := newCoreRPC(s)
	if e != nil {
		return satlineTxEvidence{}, e
	}
	var x struct {
		Hex       string `json:"hex"`
		BlockHash string `json:"blockhash"`
	}
	if e = c.callContext(ctx, "getrawtransaction", []any{id, true}, &x); e != nil {
		return satlineTxEvidence{}, e
	}
	if !validHash(x.BlockHash) {
		return satlineTxEvidence{}, fmt.Errorf("transaction has no confirmed block")
	}
	var h struct {
		Height        int64 `json:"height"`
		Confirmations int64 `json:"confirmations"`
	}
	if e = c.callContext(ctx, "getblockheader", []any{x.BlockHash, true}, &h); e != nil {
		return satlineTxEvidence{}, e
	}
	if h.Confirmations <= 0 || (before >= 0 && h.Height > before) {
		return satlineTxEvidence{}, fmt.Errorf("transaction is not in the requested active chain context")
	}
	raw, e := hex.DecodeString(x.Hex)
	if e != nil {
		return satlineTxEvidence{}, e
	}
	p := &byteParser{b: raw}
	tx, e := parseTransaction(p, -1)
	if e != nil {
		return satlineTxEvidence{}, e
	}
	if p.off != len(raw) || !strings.EqualFold(tx.TxID, id) {
		return satlineTxEvidence{}, fmt.Errorf("Core transaction identity mismatch")
	}
	return satlineTxEvidence{Tx: tx, Height: h.Height, BlockHash: x.BlockHash, VerificationState: "consensus_validated", Source: "trusted_local_core_rpc"}, nil
}

type contextSatlineBackend struct {
	appSatlineBackend
	ctx context.Context
}

func (b contextSatlineBackend) Transaction(id string, before int64) (satlineTxEvidence, error) {
	if e := b.ctx.Err(); e != nil {
		return satlineTxEvidence{}, e
	}
	if r, e := b.a.retainedSatTransaction(b.ctx, strings.ToLower(strings.TrimSpace(id)), before); e == nil && transactionFitsHeightContext(r.Height, r.Tx.Coinbase, before) {
		return r, nil
	} else if b.ctx.Err() != nil {
		return satlineTxEvidence{}, b.ctx.Err()
	}
	b.a.settingsMu.RLock()
	s := b.a.settings
	b.a.settingsMu.RUnlock()
	if x, e := coreTransactionEvidence(b.ctx, s, id, before); e == nil && transactionFitsHeightContext(x.Height, x.Tx.Coinbase, before) {
		return x, nil
	} else if b.ctx.Err() != nil {
		return satlineTxEvidence{}, b.ctx.Err()
	}
	return b.appSatlineBackend.transactionContext(b.ctx, id, before)
}
func (b contextSatlineBackend) BlockByHeight(height int64) (blockView, error) {
	if e := b.ctx.Err(); e != nil {
		return blockView{}, e
	}
	if block, e := b.a.retainedSatBlock(b.ctx, height); e == nil {
		return block, nil
	} else if b.ctx.Err() != nil {
		return blockView{}, b.ctx.Err()
	}
	// Preserve the shared mounted-store/cache retrieval pipeline. All local Core
	// calls are individually bounded; explicit cancellation is checked before IO.
	type result struct {
		view blockView
		err  error
	}
	ch := make(chan result, 1)
	go func() { v, e := b.appSatlineBackend.BlockByHeight(height); ch <- result{v, e} }()
	select {
	case <-b.ctx.Done():
		return blockView{}, b.ctx.Err()
	case x := <-ch:
		return x.view, x.err
	}
}
func (b contextSatlineBackend) Spender(id string, vout uint32) (spenderLookupView, error) {
	if e := b.ctx.Err(); e != nil {
		return spenderLookupView{}, e
	}
	if spend, e := b.a.retainedSatSpender(b.ctx, id, vout); e == nil {
		return spend, nil
	} else if b.ctx.Err() != nil {
		return spenderLookupView{}, b.ctx.Err()
	}
	b.a.settingsMu.RLock()
	s := b.a.settings
	b.a.settingsMu.RUnlock()
	if v, e := b.a.coreSpenderFast(b.ctx, s, id, vout); e == nil {
		return v, nil
	} else if b.ctx.Err() != nil {
		return spenderLookupView{}, b.ctx.Err()
	}
	return b.appSatlineBackend.Spender(id, vout)
}
func (a *app) coreSpenderFast(ctx context.Context, s appSettings, id string, vout uint32) (spenderLookupView, error) {
	c, e := newCoreRPC(s)
	if e != nil {
		return spenderLookupView{}, e
	}
	var rows []struct {
		SpendingTxID string `json:"spendingtxid"`
		SpendingTx   string `json:"spendingtx"`
		BlockHash    string `json:"blockhash"`
	}
	if e = c.callContext(ctx, "gettxspendingprevout", []any{[]any{map[string]any{"txid": id, "vout": vout}}, map[string]any{"mempool_only": false, "return_spending_tx": true}}, &rows); e != nil {
		return spenderLookupView{}, e
	}
	if len(rows) != 1 {
		return spenderLookupView{}, fmt.Errorf("Core returned an invalid spender response")
	}
	row := rows[0]
	v := spenderLookupView{Outpoint: fmt.Sprintf("%s:%d", id, vout), Provider: "bitcoin_core_txospenderindex", MempoolState: "not_seen", VerificationState: "consensus_validated"}
	if validHash(row.BlockHash) && validHash(row.SpendingTxID) {
		var h struct {
			Height        int64 `json:"height"`
			Confirmations int64 `json:"confirmations"`
		}
		if e = c.callContext(ctx, "getblockheader", []any{row.BlockHash, true}, &h); e != nil {
			return v, e
		}
		if h.Confirmations <= 0 {
			return v, fmt.Errorf("spender block is not active")
		}
		raw, e := hex.DecodeString(row.SpendingTx)
		if e != nil {
			return v, e
		}
		p := &byteParser{b: raw}
		tx, e := parseTransaction(p, -1)
		if e != nil {
			return v, e
		}
		if p.off != len(raw) || !strings.EqualFold(tx.TxID, row.SpendingTxID) {
			return v, fmt.Errorf("spender txid mismatch")
		}
		vin := -1
		for i, in := range tx.Inputs {
			if strings.EqualFold(in.PrevTxID, id) && in.PrevVout == vout {
				vin = i
				break
			}
		}
		if vin < 0 {
			return v, fmt.Errorf("Core spender does not consume requested output")
		}
		v.ConfirmedState = "confirmed_spent"
		v.Found = true
		v.SpendingTxID = tx.TxID
		v.SpendingVin = vin
		v.Height = h.Height
		v.BlockHash = row.BlockHash
		v.Transaction = &tx
		v.SpendingInput = &tx.Inputs[vin]
		if native, ok := a.graphFindSpend(id, vout); ok && native.SpendingTxID != v.SpendingTxID {
			v.ConfirmedState = "provider_disagreement"
			v.Found = false
			v.Note = "Core and the canonical native graph disagree; no lineage advance is permitted."
			return v, nil
		}
		v.Note = "Local Core RPC supplied the spending transaction. Satline checks block membership and sat movement separately."
		return v, nil
	}
	// A mempool match has no blockhash. Ask the confirmed UTXO set separately.
	var u *struct {
		BestBlock string `json:"bestblock"`
	}
	if e = c.callContext(ctx, "gettxout", []any{id, vout, false}, &u); e != nil {
		return v, e
	}
	if u == nil || !validHash(u.BestBlock) {
		return v, fmt.Errorf("no confirmed unspent evidence")
	}
	var h struct {
		Height        int64 `json:"height"`
		Confirmations int64 `json:"confirmations"`
	}
	if e = c.callContext(ctx, "getblockheader", []any{u.BestBlock, true}, &h); e != nil {
		return v, e
	}
	if h.Confirmations <= 0 {
		return v, fmt.Errorf("UTXO snapshot no longer active")
	}
	v.ConfirmedState = "unspent_at_snapshot"
	v.Snapshot = &chainSnapshot{Network: "mainnet", Height: h.Height, Hash: u.BestBlock, Source: "bitcoin_core_utxo"}
	if validHash(row.SpendingTxID) {
		v.MempoolState = "spent"
		v.MempoolSpendingTxID = row.SpendingTxID
	}
	return v, nil
}
