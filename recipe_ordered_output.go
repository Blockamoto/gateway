package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const recipeOrderedQueryProfile = "gateway-ordered-utxo-query-v1"

// Select only checkpoints authenticated by the current head's retained chain.
// A file left on disk by an old fork or interrupted publication is insufficient.
func (s *utxoStore) orderedCheckpointAt(ctx context.Context, height int64) (utxoCheckpoint, bool, error) {
	if s.head == nil || height > s.head.Checkpoint.Height {
		return utxoCheckpoint{}, false, nil
	}
	if height < s.head.RecoveryBoundaryHeight {
		return utxoCheckpoint{}, false, fmt.Errorf("ordered snapshot is outside the retained recovery range")
	}
	c := s.head.Checkpoint
	for i := len(s.head.Recovery) - 1; c.Height > height; i-- {
		if i < 0 {
			return utxoCheckpoint{}, false, ErrUTXOStateCorrupt
		}
		point := s.head.Recovery[i]
		parent, err := s.readCheckpoint(ctx, point.Commitment)
		if err != nil {
			return utxoCheckpoint{}, false, err
		}
		if parent.Height != c.Height-1 || parent.BlockHash != c.PreviousBlockHash || parent.Commitment != c.PreviousCommitment || parent.Height != point.Height || parent.BlockHash != point.BlockHash {
			return utxoCheckpoint{}, false, ErrUTXOStateCorrupt
		}
		c = parent
	}
	return c, true, nil
}

// Header -> registry -> custom generation is also the publication lock order.
// No native global UTXO mutex is acquired for these privately owned snapshots.
func (a *app) readOrderedRecipeOutput(ctx context.Context, prepared resolvedSavedRecipe, height int64, limit int, token, cursor string) (map[string]any, int, error) {
	if height < 0 || limit < 1 || token != "" && !recipeStoreHashValid(token) {
		return nil, 400, fmt.Errorf("invalid ordered output page")
	}
	table, after := "coins", ""
	if cursor != "" {
		parts := strings.Split(cursor, ":")
		if len(parts) != 2 || (parts[0] != "coins" && parts[0] != "counts") || !validHash(parts[1]) || token == "" {
			return nil, 400, fmt.Errorf("invalid ordered output cursor")
		}
		table, after = parts[0], parts[1]
	}
	program, err := compileRecipe(prepared.Definition)
	if err != nil {
		return nil, 409, err
	}
	g, err := openRecipeOrderedGeneration(a.dataDir, program)
	if err != nil {
		return nil, 409, err
	}
	if err := a.lockSelectedHeaders051(); err != nil {
		return nil, 409, err
	}
	defer a.headerChainMu.RUnlock()
	header, err := readHeaderAt(a.headersPath, height)
	if err != nil {
		return nil, 409, err
	}
	digest := hash256(header)
	selected := reverseHex(digest[:])
	recipeRegistryMu.Lock()
	defer recipeRegistryMu.Unlock()
	view, err := recipeRegistryRead(a.dataDir, prepared.ID, "")
	if err != nil {
		return nil, 409, err
	}
	if view.Revision != prepared.Revision {
		view, err = recipeRegistryRead(a.dataDir, prepared.ID, prepared.Revision)
	}
	if err != nil || !view.Validated || view.Revision != prepared.Revision || view.SemanticHash != prepared.SemanticHash {
		return nil, 409, fmt.Errorf("saved ordered recipe revision unavailable or incompatible")
	}
	resolved, err := recipeResolveView(view)
	if err != nil || indexDigest(resolved.Definition) != indexDigest(prepared.Definition) {
		return nil, 409, fmt.Errorf("saved recipe identity changed during output read")
	}
	recipeOrderedGenerationMu.Lock()
	defer recipeOrderedGenerationMu.Unlock()
	s, _, err := g.inspect(ctx)
	if err != nil {
		return nil, 409, err
	}
	var checkpoint utxoCheckpoint
	found := false
	if s != nil {
		checkpoint, found, err = s.orderedCheckpointAt(ctx, height)
		if err != nil {
			return nil, 409, err
		}
	}
	result := map[string]any{"state": "uncovered", "authority": "selected_local_header_as_of_read", "height": height, "selected_block_hash": selected, "revision": prepared.Revision, "semantic_hash": prepared.SemanticHash}
	if !found {
		if token != "" || cursor != "" {
			return nil, 409, fmt.Errorf("ordered output snapshot changed")
		}
		return result, 0, nil
	}
	if checkpoint.BlockHash != selected {
		return nil, 409, fmt.Errorf("ordered output anchor differs from selected local headers")
	}
	unit := indexDigest(struct{ Profile, Instance, Revision, Semantic, Checkpoint string }{recipeOrderedQueryProfile, prepared.ID, prepared.Revision, prepared.SemanticHash, checkpoint.Commitment})
	if token != "" && token != unit {
		return nil, 409, fmt.Errorf("ordered output snapshot changed")
	}
	trees := s.trees(ctx, checkpoint)
	physical := map[string]string{"coins": "live", "counts": "transactions"}
	if after != "" {
		var value json.RawMessage
		found, err := trees[physical[table]].get(after, &value)
		if err != nil || !found {
			return nil, 409, fmt.Errorf("ordered output cursor is absent from this snapshot: %v", err)
		}
	}
	result["state"], result["unit"] = "covered", unit
	result["checkpoint"] = checkpoint.Commitment
	result["ordering"] = "coins_then_counts_by_physical_key"
	result["live_coin_count"] = strconv.FormatUint(checkpoint.LiveOutputs, 10)
	result["rows"] = []recipeStoreRow{}
	// Reserve the longest possible continuation, including JSON escaping.
	result["next_cursor"] = "counts:" + strings.Repeat("f", 64)
	envelope, err := json.Marshal(result)
	if err != nil {
		return nil, 409, err
	}
	remaining := recipeOutputMaxResponseBytes - len(envelope) - 1
	rows := []recipeStoreRow{}
	last, more := "", false
	stop := errors.New("ordered output page complete")
	facts := newUTXOFactReader(ctx, s.root)
	for _, name := range []string{"coins", "counts"} {
		if table == "counts" && name == "coins" {
			continue
		}
		start := ""
		if table == name {
			start = after
		}
		err = trees[physical[name]].walkAfter(start, func(kv ordinalKV) error {
			if !validHash(kv.Key) {
				return ErrUTXOStateCorrupt
			}
			if len(rows) >= limit {
				more = true
				return stop
			}
			var key string
			var value recipeOrderedValue
			if name == "coins" {
				var member utxoMember
				if err := json.Unmarshal(kv.Value, &member); err != nil {
					return err
				}
				coin, err := s.coinUsingReader(ctx, trees, member, facts)
				if err != nil {
					return err
				}
				if kv.Key != utxoPositionKey(coin.Height, coin.TxIndex, int(coin.Vout)) {
					return ErrUTXOStateCorrupt
				}
				key, err = recipeBitcoinOutpoint(coin.TxID, coin.Vout)
				if err != nil {
					return err
				}
				value = recipeUTXOCoinValue(coin)
			} else {
				var tx utxoTransaction
				if err := json.Unmarshal(kv.Value, &tx); err != nil {
					return err
				}
				if !validHash(tx.TxID) || tx.Height < 0 || tx.Height > checkpoint.Height || tx.TxIndex < 0 || tx.LiveOutputs == 0 || kv.Key != utxoTransactionKey(tx.Height, tx.TxIndex) {
					return ErrUTXOStateCorrupt
				}
				var bridge utxoBridge
				ok, err := trees["bridge"].get(utxoBridgeKey(tx.TxID), &bridge)
				if err != nil || !ok || bridge.Height != tx.Height || bridge.TxIndex != tx.TxIndex || bridge.BlockHash != tx.BlockHash {
					return ErrUTXOStateCorrupt
				}
				key, value = tx.TxID, recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"live": rovUint(tx.LiveOutputs)}}
			}
			compound, err := json.Marshal([]string{name, key})
			if err != nil {
				return err
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				return err
			}
			row := recipeStoreRow{Key: string(compound), Value: encoded}
			wire, err := json.Marshal(row)
			if err != nil {
				return err
			}
			size := len(wire)
			if len(rows) > 0 {
				size++
			}
			if size > remaining {
				if len(rows) == 0 {
					return fmt.Errorf("ordered Coin row exceeds page byte limit")
				}
				more = true
				return stop
			}
			remaining -= size
			rows = append(rows, row)
			last = name + ":" + kv.Key
			return nil
		})
		if errors.Is(err, stop) {
			break
		}
		if err != nil {
			return nil, 409, err
		}
	}
	result["rows"] = rows
	if more {
		result["next_cursor"] = last
	} else {
		delete(result, "next_cursor")
	}
	return result, 0, nil
}
