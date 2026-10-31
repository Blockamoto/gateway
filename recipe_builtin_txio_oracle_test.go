package main

import "fmt"

// Frozen pre-migration source-to-raw-spend derivation.
func deriveIndexSpendersOracle(block blockView) (*indexSpenderBlock, error) {
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
