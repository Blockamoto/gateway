package main

import (
	"context"
	_ "embed"
	"fmt"
	"strconv"
	"sync"
)

//go:embed recipes/builtins/txio-links.json
var builtinTXIOLinksJSON []byte

const recipeTXIOLinksEquivalentSemantic = "060c52f86e2983e2d5a534d34ded08f0e31f08603aef776c8e3efb82aae88052"

var builtinTXIOLinksOnce sync.Once
var builtinTXIOLinksProgram *recipeProgram
var builtinTXIOLinksSemantic string
var builtinTXIOLinksError error

func builtinTXIOLinksRecipe() (*recipeProgram, error) {
	builtinTXIOLinksOnce.Do(func() {
		d, err := parseRecipe(builtinTXIOLinksJSON)
		if err == nil {
			builtinTXIOLinksProgram, err = compileRecipe(d)
		}
		if err != nil {
			builtinTXIOLinksError = err
			return
		}
		builtinTXIOLinksSemantic = indexDigest(struct{ Source, Lookups, Physical, Program string }{recipeTXIOInputSourceProfile, recipeInputCreationLookupProfile, "gateway-positional-txio-v3-proof-translation-v1", builtinTXIOLinksProgram.semanticHash()})
	})
	return builtinTXIOLinksProgram, builtinTXIOLinksError
}

func recipeTXIOLinksCheckEquivalence(semantic string) error {
	if semantic != recipeTXIOLinksEquivalentSemantic {
		return fmt.Errorf("TXIO link recipe/proof binding changed; explicit migration required (actual %s)", semantic)
	}
	return nil
}

func deriveTXIOBlockFromRecipe(ctx context.Context, s *indexStore, block blockView) (*indexSpenderBlock, error) {
	if ctx == nil || s == nil || block.Height < 0 {
		return nil, fmt.Errorf("TXIO recipe requires its admitted source and codec")
	}
	p, err := builtinTXIOLinksRecipe()
	if err != nil {
		return nil, err
	}
	if err = recipeTXIOLinksCheckEquivalence(builtinTXIOLinksSemantic); err != nil {
		return nil, err
	}
	// The previous native boundary rejected malformed input inventory before
	// staging optional retained layers. Keep that failure-side-effect ordering.
	for ti, tx := range block.Transactions {
		if tx.Coinbase {
			continue
		}
		for _, in := range tx.Inputs {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			if in.Coinbase || !validHash(in.PrevTxID) || ti == 0 {
				return nil, fmt.Errorf("invalid spender input evidence")
			}
		}
	}
	lookups, err := newRecipeInputCreationLookups(ctx, nil, s, block, p.definition)
	if err != nil {
		return nil, err
	}
	defer lookups.AbortOrdered()
	out := &indexSpenderBlock{Version: 3, Rows: []indexSpendRecord{}, CreationAnchors: map[string]string{}, TransactionCount: uint32(len(block.Transactions)), Values: s.retainOutputValues, Scripts: s.retainOutputScripts}
	for _, tx := range block.Transactions {
		out.OutputCounts = append(out.OutputCounts, uint32(len(tx.Outputs)))
	}
	if out.Values || out.Scripts {
		out.OutputFacts, err = storeBlockOutputFactLayersContext(ctx, s.profileRoot, block, out.Values, out.Scripts)
		if err != nil {
			return nil, err
		}
	}
	parents := []txioTranslationParent{}
	parentSeen := map[string]bool{}
	current := indexSpendRecord{}
	emitted := false
	err = p.evaluateOrderedSparseTypedStateEventsInto(ctx, lookups, func(emit func(map[string]recipeOrderedValue) error) error {
		event := map[string]recipeOrderedValue{"height": rovUint(uint64(block.Height)), "block": rovText(block.Hash)}
		for ti, tx := range block.Transactions {
			event["kind"], event["txid"], event["tx_index"] = rovText("tx_begin"), rovText(tx.TxID), rovUint(uint64(ti))
			if err := emit(event); err != nil {
				return err
			}
			if tx.Coinbase {
				continue
			}
			for vin, in := range tx.Inputs {
				if err := ctx.Err(); err != nil {
					return err
				}
				if in.Coinbase || !validHash(in.PrevTxID) || ti == 0 {
					return fmt.Errorf("invalid spender input evidence")
				}
				current = indexSpendRecord{PrevTxID: in.PrevTxID, Vout: in.PrevVout, TxID: tx.TxID, TxIndex: ti, Vin: vin}
				event["kind"], event["prev_txid"], event["prev_vout"] = rovText("input"), rovText(in.PrevTxID), rovUint(uint64(in.PrevVout))
				emitted = false
				if err := emit(event); err != nil {
					return err
				}
				if !emitted {
					return fmt.Errorf("TXIO recipe omitted an admitted input")
				}
			}
		}
		return ctx.Err()
	}, func(table, key string, value recipeOrderedValue) error {
		row := value.Object
		ref := inputCoordinate(current.Vin, current.TxIndex, block.Height)
		if emitted || key != ref || table != "links" && table != "pending" {
			return fmt.Errorf("TXIO proposal differs from its admitted input")
		}
		fields := 8
		if table == "links" {
			fields = 9
		}
		if len(row) != fields {
			return fmt.Errorf("invalid typed TXIO proposal fields")
		}
		for name, want := range map[string]string{"ref": ref, "block_hash": block.Hash, "prev_txid": current.PrevTxID, "txid": current.TxID} {
			if row[name].Kind != "text" || row[name].Text != want {
				return fmt.Errorf("TXIO proposal differs at %s", name)
			}
		}
		for name, want := range map[string]uint64{"prev_vout": uint64(current.Vout), "tx_index": uint64(current.TxIndex), "input_index": uint64(current.Vin)} {
			if row[name].Kind != "uint" || row[name].Uint != want {
				return fmt.Errorf("TXIO proposal differs at %s", name)
			}
		}
		r := indexSpendRecord{Vout: current.Vout, TxIndex: current.TxIndex, Vin: current.Vin}
		if table == "links" {
			position, hash := row["output_ref"], row["creation_block_hash"]
			if position.Kind != "text" || hash.Kind != "text" || !lookups.offered(key, txioCreation{position.Text, hash.Text}) {
				return fmt.Errorf("TXIO proposal has no checked offered creation proof")
			}
			c, ok := parseBODCoordinate(position.Text)
			if !ok {
				return fmt.Errorf("invalid TXIO creation position")
			}
			h := strconv.FormatInt(c.Height, 10)
			if prior := out.CreationAnchors[h]; prior != "" && prior != hash.Text {
				return fmt.Errorf("TXIO creation providers disagree on chain identity")
			}
			out.CreationAnchors[h] = hash.Text
			r.Output = position.Text
			if !parentSeen[r.Output] {
				parents = append(parents, txioTranslationParent{current.PrevTxID, r.Output, hash.Text})
				parentSeen[r.Output] = true
			}
		} else {
			if row["evidence_state"].Kind != "text" || row["evidence_state"].Text != "unavailable" {
				return fmt.Errorf("pending TXIO proposal lost unavailable evidence state")
			}
			out.PendingTranslations = append(out.PendingTranslations, txioPendingTranslation{uint32(len(out.Rows)), current.PrevTxID})
		}
		emitted = true
		out.Rows = append(out.Rows, r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	out.LocatorRef, err = s.storeTXIOTranslation(ctx, block, parents...)
	if err != nil {
		return nil, err
	}
	if err = validateTXIOBatch(indexBatch{Checkpoint: indexCheckpoint{Height: block.Height, BlockHash: block.Hash}, Spenders: out}); err != nil {
		return nil, err
	}
	return out, nil
}
