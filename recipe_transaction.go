package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// evaluateTransactionBlock re-decodes the authenticated bytes so a mutated
// decoded view cannot turn a receipt for one transaction into another. The
// caller must separately fence selected-chain authority at publication.
// This adapter is intentionally not connected to scheduler or storage.
func (p *recipeProgram) evaluateTransactionBlock(ctx context.Context, block blockView) (recipeRows, error) {
	if ctx == nil {
		return nil, fmt.Errorf("missing recipe context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.definition.Source != "bitcoin.transaction" {
		return nil, fmt.Errorf("recipe source is not bitcoin.transaction")
	}
	if len(p.definition.Dependencies) != 0 {
		return nil, errRecipeDependencyUnavailable
	}
	if block.Height < p.definition.Origin || block.Height < 0 {
		return nil, fmt.Errorf("block precedes recipe origin")
	}
	raw, _, err := exactVerifiedBlockPayload(block)
	if err != nil {
		return nil, fmt.Errorf("transaction source requires authenticated complete bytes: %w", err)
	}
	verified, err := parseBlockDetailed(block.Height, block.Hash, nil, raw, "recipe transaction source", false)
	if err != nil || !integrityVerified(verified) {
		return nil, fmt.Errorf("transaction source verification failed: %v", err)
	}
	if len(verified.Transactions) == 0 || verified.TransactionCount != uint64(len(verified.Transactions)) {
		return nil, fmt.Errorf("transaction source is incomplete")
	}
	all := recipeRows{}
	for _, table := range p.definition.Tables {
		all[table.Name] = map[string]map[string]any{}
	}
	outputBytes := 0
	for i, tx := range verified.Transactions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if tx.Index != i || !validHash(tx.TxID) || tx.TxID != strings.ToLower(tx.TxID) {
			return nil, fmt.Errorf("invalid transaction identity or ordering at %d", i)
		}
		event := map[string]any{"ref": txCoordinate(i, verified.Height), "txid": tx.TxID, "block_hash": verified.Hash, "height": json.Number(fmt.Sprint(verified.Height)), "tx_index": json.Number(fmt.Sprint(i))}
		rows, err := p.evaluate(event)
		if err != nil {
			return nil, err
		}
		for table, values := range rows {
			for key, row := range values {
				if _, ok := all[table][key]; ok {
					return nil, fmt.Errorf("duplicate key %q in table %q", key, table)
				}
				encoded, err := json.Marshal(row)
				if err != nil {
					return nil, fmt.Errorf("encode recipe row: %w", err)
				}
				if len(encoded) > recipeMaxBlockOutputBytes-outputBytes {
					return nil, fmt.Errorf("recipe block output exceeds limit")
				}
				outputBytes += len(encoded)
				all[table][key] = row
			}
		}
	}
	return all, nil
}
