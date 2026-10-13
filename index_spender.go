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
	PrevTxID            string  `json:"prev_txid,omitempty"`
	Vout                uint32  `json:"vout"`
	TxID                string  `json:"txid,omitempty"`
	TxIndex             int     `json:"tx_index"`
	Vin                 int     `json:"vin"`
	Output              string  `json:"output,omitempty"`
	PreviousTransaction *uint32 `json:"previous_transaction,omitempty"`
}
type indexSpenderBlock struct {
	Version              int                      `json:"version,omitempty"`
	Rows                 []indexSpendRecord       `json:"rows"`
	Origins              []txioOrigin             `json:"origins,omitempty"`
	CreationAnchors      map[string]string        `json:"creation_anchors,omitempty"`
	PreviousTransactions []string                 `json:"previous_transactions,omitempty"`
	TransactionCount     uint32                   `json:"transaction_count,omitempty"`
	OutputCounts         []uint32                 `json:"output_counts,omitempty"`
	LocatorRef           string                   `json:"locator_ref,omitempty"`
	PendingTranslations  []txioPendingTranslation `json:"pending_translations,omitempty"`
	OutputFacts          map[string]outputFactRef `json:"output_facts,omitempty"`
	Values               bool                     `json:"values,omitempty"`
	Scripts              bool                     `json:"scripts,omitempty"`
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
			out.Rows = append(out.Rows, indexSpendRecord{PrevTxID: in.PrevTxID, Vout: in.PrevVout, TxID: tx.TxID, TxIndex: ti, Vin: vin})
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
	if err = s.ensureIndexLocators(ctx); err != nil {
		return out, err
	}
	// New positional rows reach this legacy API through the separate native-ID
	// locator bridge. TXIO does not keep a duplicate resolved outpoint index.
	positioned, err := a.txioSpendersViaLocator(ctx, s, txid, uint32(vout))
	if err != nil {
		return out, err
	}
	out.Rows = append(out.Rows, positioned...)
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
		if b.Spenders == nil {
			return out, fmt.Errorf("spender pointer does not match committed records")
		}
		slot := row.TxIndex
		if b.Spenders.Version == 3 {
			n := slot - 2*len(b.Spenders.Rows)
			if n < 0 || n >= len(b.Spenders.PendingTranslations) {
				return out, fmt.Errorf("pending spender pointer mismatch")
			}
			slot = int(b.Spenders.PendingTranslations[n].Row)
		}
		if slot < 0 || slot >= len(b.Spenders.Rows) {
			return out, fmt.Errorf("spender pointer does not match committed rows")
		}
		r, e := s.expandedTXIORow(ctx, b, slot)
		if e != nil {
			return out, e
		}
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
