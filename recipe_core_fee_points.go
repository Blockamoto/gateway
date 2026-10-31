package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

const recipeCoreFeeLookupProfile = "gateway-core-active-block-transaction-fee-point-v1"

func recipeCoreFeeLookupBinding() recipeSourceLookup {
	d := recipeSourceLookup{Name: "transaction_fee", Source: "bitcoin.transaction_fee", Provider: "core", Key: "transaction_ref", Type: "transaction_fee"}
	d.Revision = indexDigest(struct{ Profile, Source, Provider, Key, Type string }{recipeCoreFeeLookupProfile, d.Source, d.Provider, d.Key, d.Type})
	d.SemanticHash = indexDigest(struct{ Revision, Authority string }{d.Revision, "core-active-block-exact-satoshis-transaction-order-v1"})
	return d
}

// This is an explicitly selected Core source, not an independent Bitcoin proof.
// It returns exact checked fee facts without computing a prefix or storing rows.
func recipeCoreFeeValues(ctx context.Context, a *app, block blockView) ([]uint64, error) {
	if ctx == nil || a == nil {
		return nil, fmt.Errorf("Core fee source unavailable")
	}
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	c, e := newCoreRPC(settings)
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
	if e = c.callContext(ctx, "getblock", []any{block.Hash, 3}, &x); e != nil {
		return nil, e
	}
	if x.Hash != block.Hash || x.Confirmations <= 0 || len(x.Tx) != len(block.Transactions) {
		return nil, fmt.Errorf("Core fee layout is not the checked active block")
	}
	values := make([]uint64, len(x.Tx))
	for i, tx := range x.Tx {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		if tx.TxID != block.Transactions[i].TxID {
			return nil, fmt.Errorf("Core fee transaction order mismatch")
		}
		if i > 0 {
			values[i], e = exactBTCSats(tx.Fee)
			if e != nil {
				return nil, e
			}
		}
	}
	return values, ctx.Err()
}
func (r *recipeTablePointLookups) readCoreFee(ctx context.Context, key string) (recipeOrderedValue, error) {
	p, e := parseBODCoordinate(key)
	if !e || p.Kind != coordTransaction || p.TxID != "" || p.Height != r.block.Height || p.TxIndex < 0 || p.TxIndex >= len(r.block.Transactions) || key != strconv.Itoa(p.TxIndex)+"."+strconv.FormatInt(p.Height, 10) {
		return recipeOrderedValue{}, fmt.Errorf("Core fee source requires a transaction in the owned current block")
	}
	if r.coreFees == nil {
		fees, err := recipeCoreFeeValues(ctx, r.a, r.block)
		if err != nil {
			if ctx.Err() != nil {
				return recipeOrderedValue{}, ctx.Err()
			}
			return recipeOrderedValue{}, fmt.Errorf("%w: Core fee source unavailable (%v)", errRecipeDependencyUnavailable, err)
		}
		r.coreFees = fees
	}
	r.anchors[r.block.Height] = r.block.Hash
	return recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"transaction": rovText(key), "txid": rovText(r.block.Transactions[p.TxIndex].TxID), "block": rovText(r.block.Hash), "height": rovUint(uint64(p.Height)), "fee_sats": rovUint(r.coreFees[p.TxIndex])}}, nil
}
func recipeCheckCoreFeeFact(f recipeImportedFact) error {
	p, ok := parseBODCoordinate(f.Key)
	v := f.Value
	if !ok || p.Kind != coordTransaction || p.TxID != "" || p.Height != f.Snapshot.Height || p.TxIndex < 0 || f.Key != strconv.Itoa(p.TxIndex)+"."+strconv.FormatInt(p.Height, 10) || v.Kind != "object" || len(v.Object) != 5 || len(f.Evidence) != 1 {
		return fmt.Errorf("owned Core fee shape changed")
	}
	for _, n := range []string{"transaction", "txid", "block"} {
		if v.Object[n].Kind != "text" {
			return fmt.Errorf("owned Core fee text changed")
		}
	}
	if v.Object["height"].Kind != "uint" || v.Object["fee_sats"].Kind != "uint" || v.Object["height"].Uint != uint64(p.Height) || v.Object["transaction"].Text != f.Key || !validHash(v.Object["txid"].Text) || v.Object["block"].Text != f.Snapshot.BlockHash || f.Evidence[0].Height != p.Height || f.Evidence[0].BlockHash != f.Snapshot.BlockHash || p.TxIndex == 0 && v.Object["fee_sats"].Uint != 0 {
		return fmt.Errorf("owned Core fee identity changed")
	}
	return nil
}
