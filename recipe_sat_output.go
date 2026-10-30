package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const recipeSatQueryProfile = "gateway-sat-output-range-fragments-v1"

func recipeSatCheckpointAt(ctx context.Context, s *indexStore, height int64) (indexBatch, bool, error) {
	for _, c := range recipeSatRetainedCommits(s) {
		b, err := s.readBatch(c)
		if err != nil {
			return b, false, err
		}
		if b.Checkpoint.Height == height {
			return b, true, nil
		}
	}
	if s.checkpoint != nil && height < s.checkpoint.Height {
		return indexBatch{}, false, fmt.Errorf("FIFO snapshot is not a retained recovery point")
	}
	return indexBatch{}, false, nil
}

// Range fragments keep even a highly fragmented output pageable. A cursor
// identifies a table, immutable physical output key and next interval index;
// sats offsets are derived from authenticated native segment prefixes.
func (a *app) readSatRecipeOutput(ctx context.Context, prepared resolvedSavedRecipe, height int64, limit int, token, cursor string) (map[string]any, int, error) {
	if ctx == nil || height < 0 || limit < 1 || limit > 1000 || token != "" && !validHash(token) || cursor != "" && token == "" {
		return nil, 400, fmt.Errorf("invalid FIFO output page")
	}
	table, after, next := "outputs", "", uint64(0)
	if cursor != "" {
		parts := strings.Split(cursor, ":")
		if len(parts) != 3 || (parts[0] != "outputs" && parts[0] != "history") || !validHash(parts[1]) {
			return nil, 400, fmt.Errorf("invalid FIFO cursor")
		}
		n, err := strconv.ParseUint(parts[2], 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != parts[2] {
			return nil, 400, fmt.Errorf("invalid FIFO cursor index")
		}
		table, after, next = parts[0], parts[1], n
	}
	p, err := compileRecipe(prepared.Definition)
	if err != nil {
		return nil, 409, err
	}
	g, err := openRecipeSatGeneration(a.dataDir, p)
	if err != nil {
		return nil, 409, err
	}
	if err = a.lockSelectedHeaders051(); err != nil {
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
	view, err := recipeRegistryRead(a.dataDir, prepared.ID, prepared.Revision)
	if err != nil || !view.Validated || view.Revision != prepared.Revision || view.SemanticHash != prepared.SemanticHash {
		return nil, 409, fmt.Errorf("saved FIFO revision unavailable")
	}
	resolved, err := recipeResolveView(view)
	if err != nil || indexDigest(resolved.Definition) != indexDigest(prepared.Definition) {
		return nil, 409, fmt.Errorf("saved FIFO identity changed")
	}
	recipeOrderedGenerationMu.Lock()
	defer recipeOrderedGenerationMu.Unlock()
	s, _, initialized, err := g.inspect(ctx)
	if err != nil {
		return nil, 409, err
	}
	result := map[string]any{"state": "uncovered", "authority": "selected_local_header_as_of_read", "height": height, "selected_block_hash": selected, "revision": prepared.Revision, "semantic_hash": prepared.SemanticHash}
	var b indexBatch
	found := false
	if initialized {
		b, found, err = recipeSatCheckpointAt(ctx, s, height)
		if err != nil {
			return nil, 409, err
		}
	}
	if !found {
		if token != "" || cursor != "" {
			return nil, 409, fmt.Errorf("FIFO output snapshot changed")
		}
		return result, 0, nil
	}
	if b.Checkpoint.BlockHash != selected {
		return nil, 409, fmt.Errorf("FIFO checkpoint is not on selected local headers")
	}
	if err = s.validateSatRoots077(ctx, *b.Sats); err != nil {
		return nil, 409, err
	}
	unit := indexDigest(struct{ Profile, Instance, Revision, Semantic, Checkpoint string }{recipeSatQueryProfile, prepared.ID, prepared.Revision, prepared.SemanticHash, b.Checkpoint.Commitment})
	if token != "" && token != unit {
		return nil, 409, fmt.Errorf("FIFO output snapshot changed")
	}
	result["state"], result["unit"], result["checkpoint"] = "covered", unit, b.Checkpoint.Commitment
	result["row_format"], result["ordering"] = recipeSatQueryProfile, "outputs_then_history_by_physical_key_and_interval"
	result["live_output_count"], result["lost_sats"], result["destroyed_sats"] = strconv.FormatUint(b.Sats.LiveOutputs, 10), strconv.FormatUint(b.Sats.LostSats, 10), strconv.FormatUint(b.Sats.DestroyedSats, 10)
	result["history_retained"] = b.Sats.RetainHistory
	rows, continuation, err := recipeSatMemberPage(ctx, s, *b.Sats, table, after, next, limit)
	if err != nil {
		return nil, 409, err
	}
	result["rows"] = rows
	if continuation != "" {
		result["next_cursor"] = continuation
	}
	// Repeat external-file fences after payload reads. An unrelated append may
	// preserve this retained point; a lost head, fork or expired point cannot.
	s2, _, err := g.snapshot(ctx)
	if err != nil {
		return nil, 409, err
	}
	final, retained, err := recipeSatCheckpointAt(ctx, s2, height)
	if err != nil || !retained || final.Checkpoint.Commitment != b.Checkpoint.Commitment {
		return nil, 409, fmt.Errorf("FIFO checkpoint changed during read")
	}
	header, err = readHeaderAt(a.headersPath, height)
	if err != nil {
		return nil, 409, err
	}
	digest = hash256(header)
	if reverseHex(digest[:]) != selected {
		return nil, 409, fmt.Errorf("selected FIFO header changed during read")
	}
	if err = ctx.Err(); err != nil {
		return nil, 409, err
	}
	return result, 0, nil
}

func recipeSatMemberPage(ctx context.Context, s *indexStore, p satIndexSnapshot, table, after string, next uint64, limit int) ([]recipeStoreRow, string, error) {
	e := newSatEditor077(ctx, s, p)
	trees := map[string]*ordinalTree{"outputs": e.outputs, "history": e.history}
	if table == "history" && !p.RetainHistory {
		return nil, "", fmt.Errorf("FIFO history is not retained")
	}
	rows := []recipeStoreRow{}
	last, more := "", false
	remaining := recipeOutputMaxResponseBytes - (16 << 10)
	stop := errors.New("FIFO page complete")
	appendOutput := func(name, key string, o satIndexOutput, index uint64) error {
		c, err := satPosition077(o.Output)
		if err != nil || key != utxoPositionKey(c.Height, c.TxIndex, c.IOIndex) || o.FactRef == nil {
			return fmt.Errorf("%w: FIFO output identity", ErrSatStateCorrupt)
		}
		if name == "history" && o.State != "historical" || name == "outputs" && o.State != "unspent" && o.State != "burned" && o.State != "genesis_unspendable" && o.State != "bip30_destroyed" {
			return fmt.Errorf("invalid FIFO output state")
		}
		f, err := e.facts.read(*o.FactRef)
		if err != nil {
			return err
		}
		if f.Height != c.Height || f.TxIndex != c.TxIndex || int(f.Vout) != c.IOIndex {
			return fmt.Errorf("FIFO output facts mismatch")
		}
		nativeOutpoint := ""
		if name == "outputs" {
			if err = e.hydrate(&o); err != nil {
				return err
			}
			nativeOutpoint, err = recipeBitcoinOutpoint(o.Point.TxID, o.Point.Vout)
			if err != nil {
				return err
			}
		}
		if index > 0 && index >= o.RangeCount {
			return fmt.Errorf("FIFO interval cursor is absent")
		}
		for {
			if len(rows) >= limit {
				more = true
				return stop
			}
			offset, err := satRangeOffset077(ctx, s.ordinalPages(), o, index)
			if err != nil {
				return err
			}
			ranges, err := satRangePage077(ctx, s.ordinalPages(), o, index, 256)
			if err != nil {
				return err
			}
			end := offset
			for _, r := range ranges {
				if r.Start >= r.End || r.End > theoreticalSatSupply() || end > f.ValueSats || r.End-r.Start > f.ValueSats-end {
					return fmt.Errorf("invalid FIFO range fragment")
				}
				end += r.End - r.Start
			}
			following := index + uint64(len(ranges))
			if following == o.RangeCount && end != f.ValueSats {
				return fmt.Errorf("FIFO complete range/value mismatch")
			}
			payload := map[string]any{"output": o.Output, "state": o.State, "block_hash": f.BlockHash, "value_sats": strconv.FormatUint(f.ValueSats, 10), "range_index": strconv.FormatUint(index, 10), "range_offset": strconv.FormatUint(offset, 10), "range_count": strconv.FormatUint(o.RangeCount, 10), "ranges": recipeOrderedRanges(ranges)}
			if nativeOutpoint != "" {
				payload["native_outpoint"] = nativeOutpoint
			}
			value, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			compound, _ := json.Marshal([]string{name, o.Output})
			row := recipeStoreRow{Key: string(compound), Value: value}
			wire, err := json.Marshal(row)
			if err != nil {
				return err
			}
			if len(wire)+1 > remaining {
				if len(rows) == 0 {
					return fmt.Errorf("FIFO fragment exceeds response budget")
				}
				more = true
				return stop
			}
			remaining -= len(wire) + 1
			rows = append(rows, row)
			index = following
			if index == o.RangeCount {
				last = name + ":" + key + ":0"
				return nil
			}
			last = name + ":" + key + ":" + strconv.FormatUint(index, 10)
		}
	}
	if after != "" {
		var o satIndexOutput
		have, err := trees[table].get(after, &o)
		if err != nil || !have {
			return nil, "", fmt.Errorf("FIFO output cursor is absent")
		}
		if next > 0 {
			err = appendOutput(table, after, o, next)
			if errors.Is(err, stop) {
				return rows, last, nil
			}
			if err != nil {
				return nil, "", err
			}
		}
	}
	for _, name := range []string{"outputs", "history"} {
		if table == "history" && name == "outputs" || name == "history" && !p.RetainHistory {
			continue
		}
		start := ""
		if name == table {
			start = after
		}
		err := trees[name].walkAfter(start, func(kv ordinalKV) error {
			var o satIndexOutput
			if json.Unmarshal(kv.Value, &o) != nil {
				return fmt.Errorf("invalid FIFO member")
			}
			return appendOutput(name, kv.Key, o, 0)
		})
		if errors.Is(err, stop) {
			break
		}
		if err != nil {
			return nil, "", err
		}
	}
	if !more {
		last = ""
	}
	return rows, last, ctx.Err()
}
