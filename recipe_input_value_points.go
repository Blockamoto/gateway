package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
)

const recipeInputValueLookupProfile = "gateway-selected-input-output-value-point-v1"

func recipeInputValueLookupBinding() recipeSourceLookup {
	d := recipeSourceLookup{Name: "input_value", Source: "bitcoin.input_value", Provider: "selected", Key: "input_ref", Type: "input_value"}
	d.Revision = indexDigest(struct{ Profile, Source, Provider, Key, Type string }{recipeInputValueLookupProfile, d.Source, d.Provider, d.Key, d.Type})
	d.SemanticHash = indexDigest(struct {
		Revision, Authority string
		Verifier            int
	}{d.Revision, "owned-same-block-or-retained-positional-txio-output-fact-v1", blockVerifierVersion})
	return d
}

type recipeInputValuePoints struct {
	rows    map[recipeIdentityPosition]indexSpendRecord
	parents map[string]txioTranslationParent
	anchors map[string]string
	facts   *satlineFacts077
}

func (r *recipeTablePointLookups) readInputValue(ctx context.Context, key string) (recipeOrderedValue, error) {
	p, err := parseTXIOPosition("input", key)
	if err != nil || p.Height != r.block.Height || p.TxIndex < 1 || p.TxIndex >= len(r.block.Transactions) || p.IOIndex >= len(r.block.Transactions[p.TxIndex].Inputs) {
		return recipeOrderedValue{}, fmt.Errorf("input value requires an ordinary input in the owned current block")
	}
	in := r.block.Transactions[p.TxIndex].Inputs[p.IOIndex]
	if in.Coinbase {
		return recipeOrderedValue{}, fmt.Errorf("coinbase has no ordinary input value")
	}
	var value uint64
	var creation string
	var anchor string
	if r.inputPositions == nil {
		positions := map[string][]int{}
		for ti, tx := range r.block.Transactions {
			if err = ctx.Err(); err != nil {
				return recipeOrderedValue{}, err
			}
			positions[tx.TxID] = append(positions[tx.TxID], ti)
		}
		r.inputPositions = positions
	}
	positions := r.inputPositions[in.PrevTxID]
	index := sort.SearchInts(positions, p.TxIndex) - 1
	if index >= 0 {
		ti := positions[index]
		tx := r.block.Transactions[ti]
		if uint64(in.PrevVout) >= uint64(len(tx.Outputs)) {
			return recipeOrderedValue{}, fmt.Errorf("same-block previous output is absent")
		}
		value, creation, anchor = tx.Outputs[in.PrevVout].ValueSats, outputCoordinate(int(in.PrevVout), ti, r.block.Height), r.block.Hash
	}

	if creation == "" {
		if r.a == nil {
			return recipeOrderedValue{}, fmt.Errorf("%w: retained input values unavailable", errRecipeDependencyUnavailable)
		}
		if r.inputValues == nil {
			s, b, e := satlineTXIOBatchContext(ctx, r.a.dataDir, r.block.Height, r.block.Hash)
			if e != nil {
				return recipeOrderedValue{}, fmt.Errorf("%w: retained input layout unavailable (%v)", errRecipeDependencyUnavailable, e)
			}
			tr, e := s.readTXIOTranslation(ctx, b.Spenders.LocatorRef)
			if e != nil {
				return recipeOrderedValue{}, e
			}
			if tr.Height != r.block.Height || tr.BlockHash != r.block.Hash || len(tr.Transactions) != len(r.block.Transactions) {
				return recipeOrderedValue{}, fmt.Errorf("input value transaction inventory differs")
			}
			for ti, tx := range r.block.Transactions {
				if tr.Transactions[ti].TxID != tx.TxID {
					return recipeOrderedValue{}, fmt.Errorf("input value transaction identity differs")
				}
			}
			points := &recipeInputValuePoints{rows: map[recipeIdentityPosition]indexSpendRecord{}, parents: map[string]txioTranslationParent{}, anchors: b.Spenders.CreationAnchors, facts: newSatlineFacts077(ctx, r.a.dataDir)}
			for _, parent := range tr.Parents {
				points.parents[parent.Output] = parent
			}
			for _, row := range b.Spenders.Rows {
				if e = ctx.Err(); e != nil {
					return recipeOrderedValue{}, e
				}
				points.rows[recipeIdentityPosition{row.TxIndex, uint32(row.Vin)}] = row
			}
			r.inputValues = points
		}
		points := r.inputValues
		row, ok := points.rows[recipeIdentityPosition{p.TxIndex, uint32(p.IOIndex)}]
		parent, have := points.parents[row.Output]
		c, valid := parseBODCoordinate(row.Output)
		anchor = points.anchors[strconv.FormatInt(c.Height, 10)]
		if !ok || !have || !valid || c.Kind != coordOutput || c.Height > r.block.Height || c.Height == r.block.Height && c.TxIndex >= p.TxIndex || row.Vout != in.PrevVout || c.IOIndex != int(in.PrevVout) || parent.TxID != in.PrevTxID || parent.BlockHash != anchor || !validHash(anchor) {
			return recipeOrderedValue{}, fmt.Errorf("%w: positional input value predecessor unavailable", errRecipeDependencyUnavailable)
		}
		fact, e := satlineTXIOFactContext(ctx, r.a.dataDir, row.Output, anchor, points.facts)
		if e != nil {
			return recipeOrderedValue{}, fmt.Errorf("%w: retained output amount unavailable (%v)", errRecipeDependencyUnavailable, e)
		}
		value, creation = fact.ValueSats, row.Output
	}
	c, _ := parseBODCoordinate(creation)
	result := recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"input": rovText(key), "prev_txid": rovText(in.PrevTxID), "prev_vout": rovUint(uint64(in.PrevVout)), "value": rovUint(value), "creation": rovText(creation), "creation_height": rovUint(uint64(c.Height)), "creation_block": rovText(anchor)}}
	evidence := recipeImportedFact{Lookup: &recipeSourceLookup{Source: "bitcoin.input_value"}, Key: key, Value: result, Snapshot: recipeDependencyReceipt{Height: r.block.Height, BlockHash: r.block.Hash}, Evidence: []recipeTablePointEvidence{{Height: c.Height, BlockHash: anchor}}}
	if err = recipeCheckInputValueFact(evidence); err != nil {
		return recipeOrderedValue{}, err
	}
	if err = r.adoptEvidence(ctx, evidence); err != nil {
		return recipeOrderedValue{}, err
	}
	return result, ctx.Err()
}

func recipeCheckInputValueFact(f recipeImportedFact) error {
	p, err := parseTXIOPosition("input", f.Key)
	v := f.Value
	if err != nil || p.Height != f.Snapshot.Height || p.TxIndex < 1 || v.Kind != "object" || len(v.Object) != 7 || len(f.Evidence) != 1 {
		return fmt.Errorf("owned input value shape changed")
	}
	for _, n := range []string{"input", "prev_txid", "creation", "creation_block"} {
		if v.Object[n].Kind != "text" {
			return fmt.Errorf("owned input value text changed")
		}
	}
	for _, n := range []string{"prev_vout", "value", "creation_height"} {
		if v.Object[n].Kind != "uint" {
			return fmt.Errorf("owned input value integer changed")
		}
	}
	c, e := parseTXIOPosition("output", v.Object["creation"].Text)
	a := f.Evidence[0]
	if e != nil || v.Object["input"].Text != f.Key || !validHash(v.Object["prev_txid"].Text) || v.Object["prev_vout"].Uint != uint64(c.IOIndex) || v.Object["creation_height"].Uint != uint64(c.Height) || c.Height > p.Height || c.Height == p.Height && c.TxIndex >= p.TxIndex || a.Height != c.Height || !validHash(a.BlockHash) || a.BlockHash != v.Object["creation_block"].Text || c.Height == p.Height && a.BlockHash != f.Snapshot.BlockHash {
		return fmt.Errorf("owned input value identity changed")
	}
	return nil
}
