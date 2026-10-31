package main

// Frozen pre-migration fee arithmetic; checked evidence and physical page codecs remain shared.
import (
	"context"
	"fmt"
)

func recipeLegacyStoreFees(ctx context.Context, root string, block blockView, orderedInputValues ...[][]uint64) error {
	if !integrityVerified(block) || (!block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated) || block.Height < 0 || !validHash(block.Hash) || len(block.Transactions) < 1 || len(block.Transactions) > satFeeMaxTransactions {
		return fmt.Errorf("fee cache requires checked selected block evidence")
	}
	var values [][]uint64
	if len(orderedInputValues) > 1 {
		return fmt.Errorf("ambiguous fee input values")
	}
	if len(orderedInputValues) == 1 {
		values = orderedInputValues[0]
	} else {
		values = make([][]uint64, len(block.Transactions))
		for ti, tx := range block.Transactions {
			if ti == 0 {
				continue
			}
			v, err := satlineTXIOInputValuesContext(ctx, root, tx, block.Height, block.Hash, nil)
			if err != nil {
				return err
			}
			for _, in := range tx.Inputs {
				n, ok := v[satlineOutpointKey(in.PrevTxID, in.PrevVout)]
				if !ok {
					return fmt.Errorf("fee input value unavailable")
				}
				values[ti] = append(values[ti], n)
			}
		}
	}
	if len(values) != len(block.Transactions) {
		return fmt.Errorf("fee input transaction count mismatch")
	}
	rows := make([]satFeeRow, len(block.Transactions))
	var total uint64
	for ti, tx := range block.Transactions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if tx.Index != ti || tx.Coinbase != (ti == 0) || !validHash(tx.TxID) {
			return fmt.Errorf("fee transaction order mismatch")
		}
		rows[ti].Before = total
		if ti == 0 {
			if len(values[ti]) != 0 {
				return fmt.Errorf("coinbase has no ordinary fee inputs")
			}
			continue
		}
		if len(values[ti]) != len(tx.Inputs) || len(tx.Inputs) == 0 {
			return fmt.Errorf("fee ordered input values incomplete")
		}
		var input uint64
		for i, value := range values[ti] {
			if tx.Inputs[i].Coinbase {
				return fmt.Errorf("ordinary fee transaction has coinbase input")
			}
			var err error
			input, err = addUint64(input, value)
			if err != nil {
				return err
			}
		}
		output, err := sumOutputs(tx)
		if err != nil {
			return err
		}
		if output > input {
			return fmt.Errorf("fee outputs exceed inputs")
		}
		rows[ti].Fee = input - output
		total, err = addUint64(total, rows[ti].Fee)
		if err != nil {
			return err
		}
	}
	return storeSatFeeRowsContext077(ctx, root, block.Height, block.Hash, rows, total)
}
