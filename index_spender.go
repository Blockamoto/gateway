package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Positive spend records prove only their committed range. In particular a
// miss cannot assert unspentness or choose an incarnation of a BIP30 txid.
type indexSpendRecord struct {
	PrevTxID string `json:"prev_txid"`
	Vout     uint32 `json:"vout"`
	TxID     string `json:"txid"`
	TxIndex  int    `json:"tx_index"`
	Vin      int    `json:"vin"`
}
type indexSpenderBlock struct {
	Rows []indexSpendRecord `json:"rows"`
}

func indexSpendKey(txid string, vout uint32) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", txid, vout)))
	return hex.EncodeToString(sum[:])
}
func deriveIndexSpenders(block blockView) (*indexSpenderBlock, error) {
	out := &indexSpenderBlock{Rows: []indexSpendRecord{}}
	for ti, tx := range block.Transactions {
		if tx.Coinbase {
			continue
		}
		for vin, in := range tx.Inputs {
			if in.Coinbase || !validHash(in.PrevTxID) || ti == 0 {
				return nil, fmt.Errorf("invalid spender input evidence")
			}
			out.Rows = append(out.Rows, indexSpendRecord{in.PrevTxID, in.PrevVout, tx.TxID, ti, vin})
		}
	}
	return out, nil
}

type indexSpendResult struct {
	Rows       []indexSpendMatch `json:"rows"`
	Checkpoint *indexCheckpoint  `json:"checkpoint"`
	ChainState string            `json:"chain_state"`
	Absence    string            `json:"absence"`
}
type indexSpendMatch struct {
	indexSpendRecord
	Height    int64  `json:"height"`
	BlockHash string `json:"block_hash"`
}

func (a *app) lookupIndexSpender(ctx context.Context, key string) (indexSpendResult, error) {
	ctx, done := a.indexLookupContext(ctx)
	defer done()
	out := indexSpendResult{Rows: []indexSpendMatch{}, Absence: "No matching spend in queried committed records; not proof of unspent."}
	txid, voutString, ok := strings.Cut(key, ":")
	txid = strings.ToLower(txid)
	vout, err := strconv.ParseUint(voutString, 10, 32)
	if !ok || !validHash(txid) || err != nil || strconv.FormatUint(vout, 10) != voutString {
		return out, fmt.Errorf("spender key must be txid:vout")
	}
	s, err := indexStoreHead(a.dataDir, "txo-spender")
	if err != nil {
		return out, err
	}
	out.Checkpoint, out.ChainState = s.checkpoint, a.indexChainState(s.checkpoint)
	if s.checkpoint == nil {
		return out, nil
	}
	if out.ChainState != "selected_chain" {
		return out, fmt.Errorf("spender index needs a current chain anchor or reorg reconciliation")
	}
	rows, err := s.bitcoinLocatorRecords(ctx, indexSpendKey(txid, uint32(vout)))
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		b, current, e := a.validatedLocatorBatch(ctx, s, row)
		if e != nil {
			return out, e
		}
		if !current {
			continue
		}
		if b.Spenders == nil || row.TxIndex >= len(b.Spenders.Rows) {
			return out, fmt.Errorf("spender pointer does not match committed records")
		}
		r := b.Spenders.Rows[row.TxIndex]
		if r.PrevTxID != txid || r.Vout != uint32(vout) {
			return out, fmt.Errorf("spender pointer key mismatch")
		}
		out.Rows = append(out.Rows, indexSpendMatch{r, b.Checkpoint.Height, b.Checkpoint.BlockHash})
	}
	if len(out.Rows) > 0 {
		out.Absence = ""
	}
	return out, nil
}
