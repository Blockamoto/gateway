package main

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

func exactBTCSats(raw json.RawMessage) (uint64, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("value unavailable")
	}
	r, ok := new(big.Rat).SetString(string(raw))
	if !ok || r.Sign() < 0 {
		return 0, fmt.Errorf("invalid BTC amount")
	}
	r.Mul(r, new(big.Rat).SetInt64(100000000))
	if !r.IsInt() || !r.Num().IsUint64() {
		return 0, fmt.Errorf("BTC amount is not an exact satoshi value")
	}
	n := r.Num().Uint64()
	if n > 2100000000000000 {
		return 0, fmt.Errorf("BTC amount out of range")
	}
	return n, nil
}

type coreValueBackend interface {
	InputValues(transactionView, int64, string) (map[string]uint64, error)
	FeePrefix(blockView) ([]uint64, error)
}

func (b contextSatlineBackend) InputValues(tx transactionView, height int64, hash string) (map[string]uint64, error) {
	if values, e := b.a.retainedSatInputValues(b.ctx, tx, height, hash); e == nil {
		return values, nil
	} else if b.ctx.Err() != nil {
		return nil, b.ctx.Err()
	}
	b.a.settingsMu.RLock()
	s := b.a.settings
	b.a.settingsMu.RUnlock()
	c, e := newCoreRPC(s)
	if e != nil {
		return nil, e
	}
	if !validHash(hash) {
		return nil, fmt.Errorf("input-value shortcut requires a checked block hash")
	}
	// Explicit block context also works without a global txindex when Core has
	// that block and its undo data. Values are a local Core attestation, not a
	// cryptographic prevout proof.
	var x struct {
		BlockHash string `json:"blockhash"`
		TxID      string `json:"txid"`
		Vin       []struct {
			TxID    string `json:"txid"`
			Vout    uint32 `json:"vout"`
			Prevout *struct {
				Value json.RawMessage `json:"value"`
			} `json:"prevout"`
		} `json:"vin"`
	}
	if e = c.callContext(b.ctx, "getrawtransaction", []any{tx.TxID, 2, hash}, &x); e != nil {
		return nil, e
	}
	if x.TxID != tx.TxID || x.BlockHash != hash || len(x.Vin) != len(tx.Inputs) {
		return nil, fmt.Errorf("Core input-value context mismatch")
	}
	var active string
	if e = c.callContext(b.ctx, "getblockhash", []any{height}, &active); e != nil || !strings.EqualFold(active, hash) {
		return nil, fmt.Errorf("Core chain context changed")
	}
	out := map[string]uint64{}
	for i, in := range x.Vin {
		if in.TxID != tx.Inputs[i].PrevTxID || in.Vout != tx.Inputs[i].PrevVout {
			return nil, fmt.Errorf("Core input identity mismatch")
		}
		if in.Prevout != nil {
			n, e := exactBTCSats(in.Prevout.Value)
			if e != nil {
				return nil, e
			}
			out[satlineOutpointKey(in.TxID, in.Vout)] = n
		}
	}
	return out, nil
}
func (b contextSatlineBackend) FeePrefix(block blockView) ([]uint64, error) {
	b.a.settingsMu.RLock()
	s := b.a.settings
	b.a.settingsMu.RUnlock()
	c, e := newCoreRPC(s)
	if e != nil {
		return nil, e
	}
	var x struct {
		Hash          string `json:"hash"`
		Confirmations int64  `json:"confirmations"`
		Tx            []struct {
			TxID string          `json:"txid"`
			Fee  json.RawMessage `json:"fee"`
		} `json:"tx"`
	}
	if e = c.callContext(b.ctx, "getblock", []any{block.Hash, 3}, &x); e != nil {
		return nil, e
	}
	if x.Hash != block.Hash || x.Confirmations <= 0 || len(x.Tx) != len(block.Transactions) {
		return nil, fmt.Errorf("Core fee layout is not the checked active block")
	}
	prefix := make([]uint64, len(x.Tx)+1)
	for i, tx := range x.Tx {
		if tx.TxID != block.Transactions[i].TxID {
			return nil, fmt.Errorf("Core fee transaction order mismatch")
		}
		prefix[i+1] = prefix[i]
		if i == 0 {
			continue
		}
		fee, e := exactBTCSats(tx.Fee)
		if e != nil {
			return nil, e
		}
		prefix[i+1], e = addUint64(prefix[i], fee)
		if e != nil {
			return nil, e
		}
	}
	return prefix, nil
}
func (r *satlineResolver) primeInputValues(tx transactionView, height int64) {
	b, ok := r.backend.(coreValueBackend)
	if !ok {
		return
	}
	block, exists := r.blocks[height]
	if !exists {
		return
	}
	if r.valuesTried == nil {
		r.valuesTried = map[string]bool{}
	}
	key := block.Hash + tx.TxID
	if r.valuesTried[key] {
		return
	}
	r.valuesTried[key] = true
	// A transaction whose prevouts are already known needs no extra RPC.
	missing := false
	for _, in := range tx.Inputs {
		if _, ok := r.values[satlineOutpointKey(in.PrevTxID, in.PrevVout)]; !ok {
			missing = true
			break
		}
	}
	if !missing {
		return
	}
	values, e := b.InputValues(tx, height, block.Hash)
	if e != nil {
		return
	}
	for k, v := range values {
		r.values[k] = v
	}
	if len(values) > 0 {
		r.observeVerification("consensus_validated")
	}
}
func (r *satlineResolver) coreFeePrefix(block blockView) ([]uint64, bool) {
	b, ok := r.backend.(coreValueBackend)
	if !ok {
		return nil, false
	}
	if r.feePrefixes == nil {
		r.feePrefixes = map[string][]uint64{}
	}
	if prefix, tried := r.feePrefixes[block.Hash]; tried {
		return prefix, prefix != nil
	}
	prefix, e := b.FeePrefix(block)
	if e != nil {
		r.feePrefixes[block.Hash] = nil
		return nil, false
	}
	r.feePrefixes[block.Hash] = prefix
	r.observeVerification("consensus_validated")
	return prefix, true
}
