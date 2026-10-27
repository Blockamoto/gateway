package delta

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	engine "gateway-common-engine-experiment"
)

// UTXOProducer interprets the common fixture UTXO recipe directly over
// selective reads and submits an explicit Delta. Its source blocks and chain
// anchors are caller assertions, not authenticated Bitcoin evidence.
type UTXOProducer struct {
	store                *Store
	instance, recipeHash string
	recipe               engine.Recipe
}

func NewUTXOProducer(store *Store, instance string, recipe engine.Recipe) (*UTXOProducer, error) {
	if store == nil || strings.TrimSpace(instance) == "" {
		return nil, errors.New("store and instance required")
	}
	if err := recipe.Validate(); err != nil {
		return nil, err
	}
	if recipe.State != "utxo" {
		return nil, errors.New("producer requires UTXO recipe")
	}
	ops := make([]engine.Operation, len(recipe.Operations))
	for i, op := range recipe.Operations {
		ops[i] = engine.Operation{Op: op.Op, Fields: append([]string(nil), op.Fields...)}
	}
	recipe.Operations = ops
	data, err := json.Marshal(recipe)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	return &UTXOProducer{store: store, instance: instance, recipeHash: fmt.Sprintf("%x", hash), recipe: recipe}, nil
}

func (p *UTXOProducer) View() (engine.View, error) { return p.store.View(p.instance, p.recipeHash) }

// Apply evaluates one complete fixture block in transaction order. It obtains
// the publication fence, resolves touched outpoints only, builds an overlay,
// and commits the final changed keys and one receipt. It does not use the
// full-View adapter. Missing inputs or stale evidence abort without publication.
// Callers must not mutate the block concurrently with Apply. Evaluation uses
// a detached input snapshot across both passes and the intervening lookup.
func (p *UTXOProducer) Apply(block engine.Block) (engine.Receipt, WriteStats, error) {
	block = snapshotUTXOBlock(block)
	var empty engine.Receipt
	var noWrites WriteStats
	if block.Hash == "" {
		return empty, noWrites, errors.New("missing block anchor")
	}
	if len(block.Transactions) == 0 {
		return empty, noWrites, errors.New("complete block requires at least one transaction")
	}
	prep, err := p.store.PrepareDelta(p.instance, p.recipeHash, block.Height, block.Hash)
	if err != nil {
		return empty, noWrites, err
	}
	if prep.Head == nil {
		if block.Height != 0 || block.ParentHash != "" {
			return empty, noWrites, errors.New("genesis required")
		}
	} else {
		if prep.Head.Height == block.Height && prep.Head.Hash == block.Hash {
			return empty, noWrites, errors.New("block already committed")
		}
		if prep.Head.Height == math.MaxUint64 || block.Height != prep.Head.Height+1 || block.ParentHash != prep.Head.Hash {
			return empty, noWrites, errors.New("block is not exact next anchored successor")
		}
	}
	// Selective lookup includes every referenced input and prospective output,
	// so a live outpoint collision cannot be mistaken for an unused key. The
	// overlay then handles outputs created and spent earlier in this block.
	keys := []engine.Outpoint{}
	keySeen := map[engine.Outpoint]bool{}
	add := func(key engine.Outpoint) {
		if !keySeen[key] {
			keySeen[key] = true
			keys = append(keys, key)
		}
	}
	for _, tx := range block.Transactions {
		if tx.ID == "" {
			return empty, noWrites, errors.New("empty transaction identity")
		}
		for _, in := range tx.Inputs {
			if in.TxID == "" {
				return empty, noWrites, errors.New("empty outpoint")
			}
			add(in)
		}
		for oi := range tx.Outputs {
			if uint64(oi) > uint64(math.MaxUint32) {
				return empty, noWrites, errors.New("too many outputs")
			}
			add(engine.Outpoint{TxID: tx.ID, Index: uint32(oi)})
		}
	}
	base, _, err := p.store.LookupCoins(p.instance, p.recipeHash, prep.Fence, keys)
	if err != nil {
		return empty, noWrites, err
	}
	overlay := make(map[engine.Outpoint]engine.Coin, len(base))
	for k, v := range base {
		overlay[k] = v
	}
	receipt := engine.Receipt{Height: block.Height, Hash: block.Hash, Rows: []engine.Row{}}
	seenTX := map[string]bool{}
	seenInput := map[engine.Outpoint]bool{}
	for ti, tx := range block.Transactions {
		if seenTX[tx.ID] {
			return empty, noWrites, errors.New("duplicate transaction identity")
		}
		seenTX[tx.ID] = true
		for _, op := range p.recipe.Operations {
			switch op.Op {
			case "consume-inputs":
				if tx.Coinbase && (ti != 0 || len(tx.Inputs) != 0) {
					return empty, noWrites, errors.New("invalid fixture coinbase")
				}
				if !tx.Coinbase && len(tx.Inputs) == 0 {
					return empty, noWrites, errors.New("non-coinbase requires inputs")
				}
				for _, in := range tx.Inputs {
					if seenInput[in] {
						return empty, noWrites, errors.New("duplicate input")
					}
					seenInput[in] = true
					if _, ok := overlay[in]; !ok {
						return empty, noWrites, fmt.Errorf("unresolved previous output %s:%d", in.TxID, in.Index)
					}
					delete(overlay, in)
				}
			case "create-outputs":
				for oi, coin := range tx.Outputs {
					key := engine.Outpoint{TxID: tx.ID, Index: uint32(oi)}
					if _, exists := overlay[key]; exists {
						return empty, noWrites, errors.New("live output collision")
					}
					overlay[key] = coin
				}
			case "emit":
				row := engine.Row{Cells: make([]engine.Cell, 0, len(op.Fields))}
				for _, field := range op.Fields {
					value := ""
					switch field {
					case "tx.id":
						value = tx.ID
					case "tx.position":
						value = fmt.Sprintf("%d.%d", ti, block.Height)
					}
					row.Cells = append(row.Cells, engine.Cell{Field: field, Value: value})
				}
				receipt.Rows = append(receipt.Rows, row)
			default:
				return empty, noWrites, errors.New("unsupported producer operation")
			}
		}
	}
	d := Delta{Instance: p.instance, RecipeHash: p.recipeHash, Fence: prep.Fence, Receipt: receipt, Head: &engine.Head{Height: block.Height, Hash: block.Hash}, ParentHash: block.ParentHash, Upsert: map[engine.Outpoint]engine.Coin{}}
	for key, old := range base {
		now, ok := overlay[key]
		if !ok {
			d.Delete = append(d.Delete, key)
		} else if now != old {
			d.Upsert[key] = now
		}
	}
	for key, coin := range overlay {
		if _, ok := base[key]; !ok {
			d.Upsert[key] = coin
		}
	}
	stats, err := p.store.CommitDelta(d)
	if err != nil {
		return empty, noWrites, err
	}
	return receipt, stats, nil
}

// Snapshot exactly the fields understood by the fixed UTXO operation set.
// Inscription payloads are not used or copied by this producer.
func snapshotUTXOBlock(block engine.Block) engine.Block {
	copyBlock := engine.Block{Height: block.Height, Hash: block.Hash, ParentHash: block.ParentHash, Transactions: make([]engine.Transaction, len(block.Transactions))}
	for i, tx := range block.Transactions {
		copyBlock.Transactions[i] = engine.Transaction{
			ID: tx.ID, Coinbase: tx.Coinbase,
			Inputs:  append([]engine.Outpoint(nil), tx.Inputs...),
			Outputs: append([]engine.Coin(nil), tx.Outputs...),
		}
	}
	return copyBlock
}
