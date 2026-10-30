package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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
	all := recipeRows{}
	for _, table := range p.definition.Tables {
		all[table.Name] = map[string]map[string]any{}
	}
	err := p.evaluateTransactionSourceInto(ctx, block, source, lookup, func(row recipeRowProposal) error {
		all[row.Table][row.Key] = row.Value
		return nil
	})
	if err != nil {
		return nil, err
	}
	return all, nil
}

// The sink receives staged proposals, never permission to publish. It must
// discard them if any later event, duplicate, byte limit or cancellation fails.
func (p *recipeProgram) evaluateTransactionSourceInto(ctx context.Context, block blockView, source *recipeTransactionSource, lookup func(string, string) (any, error), sink func(recipeRowProposal) error, staged ...recipeRowStreaming) error {
	if ctx == nil {
		return fmt.Errorf("missing recipe context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.definition.Source != "bitcoin.transaction" || sink == nil {
		return fmt.Errorf("recipe source is not bitcoin.transaction or sink is missing")
	}
	if len(p.definition.Dependencies) != 0 && lookup == nil {
		return errRecipeDependencyUnavailable
	}
	if block.Height < p.definition.Origin || block.Height < 0 {
		return fmt.Errorf("block precedes recipe origin")
	}
	if source == nil || source.height != block.Height || source.hash != block.Hash || len(source.ids) == 0 || uint64(len(source.ids)) != block.TransactionCount {
		return fmt.Errorf("transaction source differs from block identity")
	}
	stream := newRecipeRowStream(p.definition, sink)
	var spool recipeRowStreaming
	if len(staged) > 1 {
		return fmt.Errorf("multiple sparse evaluation spools")
	}
	if len(staged) == 1 {
		spool = staged[0]
	}
	emit := stream.add
	if spool != nil {
		emit = sink
	}
	height := json.Number(strconv.FormatInt(source.height, 10))
	for i, id := range source.ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		event := map[string]any{"ref": txCoordinate(i, source.height), "txid": id, "block_hash": source.hash, "height": height, "tx_index": json.Number(strconv.Itoa(i))}
		rows, _, err := p.evaluateRowProposals(event, lookup, spool)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err = emit(row); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
