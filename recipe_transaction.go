package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// A transaction snapshot is private to a prepared block. Its entries are
// copied only after their ordered Merkle root is matched to authenticated raw
// header bytes; it carries no selected-chain or run authority.
type recipeTransactionSource struct {
	height int64
	hash   string
	ids    []string
}

func recipeTransactionSourceFromBlock(ctx context.Context, block blockView) (*recipeTransactionSource, error) {
	if ctx == nil {
		return nil, fmt.Errorf("missing recipe context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if block.Height < 0 || !recipeStoreHashValid(block.Hash) || len(block.Transactions) == 0 || block.TransactionCount != uint64(len(block.Transactions)) {
		return nil, fmt.Errorf("transaction source is incomplete or has invalid identity")
	}
	raw, _, err := exactVerifiedBlockPayload(block)
	if err != nil {
		return nil, fmt.Errorf("transaction source requires authenticated complete bytes: %w", err)
	}
	hashes := make([][32]byte, 0, len(block.Transactions))
	ids := make([]string, 0, len(block.Transactions))
	for i, tx := range block.Transactions {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if tx.Index != i || !validHash(tx.TxID) || tx.TxID != strings.ToLower(tx.TxID) {
			return nil, fmt.Errorf("invalid transaction identity or ordering at %d", i)
		}
		hash, e := displayHashRaw(tx.TxID)
		if e != nil {
			return nil, e
		}
		hashes = append(hashes, hash)
		ids = append(ids, tx.TxID)
	}
	root := merkleRoot(hashes)
	if !bytes.Equal(root[:], raw[36:68]) || merkleMutated(hashes) {
		return nil, fmt.Errorf("transaction identities differ from verified block Merkle root")
	}
	return &recipeTransactionSource{height: block.Height, hash: block.Hash, ids: ids}, nil
}

func (p *recipeProgram) evaluateTransactionBlock(ctx context.Context, block blockView) (recipeRows, error) {
	source, err := recipeTransactionSourceFromBlock(ctx, block)
	if err != nil {
		return nil, err
	}
	return p.evaluateTransactionSource(ctx, block, source)
}

func (p *recipeProgram) evaluateTransactionSource(ctx context.Context, block blockView, source *recipeTransactionSource) (recipeRows, error) {
	return p.evaluateTransactionSourceWithLookup(ctx, block, source, nil)
}

func (p *recipeProgram) evaluateTransactionSourceWithLookup(ctx context.Context, block blockView, source *recipeTransactionSource, lookup func(string, string) (any, error)) (recipeRows, error) {
	if ctx == nil {
		return nil, fmt.Errorf("missing recipe context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.definition.Source != "bitcoin.transaction" {
		return nil, fmt.Errorf("recipe source is not bitcoin.transaction")
	}
	if len(p.definition.Dependencies) != 0 && lookup == nil {
		return nil, errRecipeDependencyUnavailable
	}
	if block.Height < p.definition.Origin || block.Height < 0 {
		return nil, fmt.Errorf("block precedes recipe origin")
	}
	if source == nil || source.height != block.Height || source.hash != block.Hash || len(source.ids) == 0 || uint64(len(source.ids)) != block.TransactionCount {
		return nil, fmt.Errorf("transaction source differs from block identity")
	}
	all := recipeRows{}
	for _, table := range p.definition.Tables {
		all[table.Name] = map[string]map[string]any{}
	}
	outputBytes := 0
	for i, id := range source.ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		event := map[string]any{"ref": txCoordinate(i, source.height), "txid": id, "block_hash": source.hash, "height": json.Number(fmt.Sprint(source.height)), "tx_index": json.Number(fmt.Sprint(i))}
		rows, err := p.evaluateWithLookup(event, lookup)
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
