package main

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"strconv"
	"sync"
)

//go:embed recipes/builtins/bitmap-districts-standard-v1.json
var builtinBitmapDistrictJSON []byte

const recipeBitmapDistrictEquivalentSemantic = "426dd8721c0b74b63a7f7e0083f338c2201e231bb16742c0ea84a73487cbf9af"
const recipeBitmapAdmittedRevealProfile = "gateway-admitted-complete-bitmap-reveals-v1"

var builtinBitmapDistrictOnce sync.Once
var builtinBitmapDistrictProgram *recipeProgram
var builtinBitmapDistrictSemantic string
var builtinBitmapDistrictError error

func builtinBitmapDistrictRecipe() (*recipeProgram, error) {
	builtinBitmapDistrictOnce.Do(func() {
		d, err := parseRecipe(builtinBitmapDistrictJSON)
		if err == nil {
			builtinBitmapDistrictProgram, err = compileRecipe(d)
		}
		if err != nil {
			builtinBitmapDistrictError = err
			return
		}
		builtinBitmapDistrictSemantic = indexDigest(struct{ AdmittedSource, TypedSource, Physical, Profile, Program string }{recipeBitmapAdmittedRevealProfile, recipeTablesSourceProfileFor("bitcoin.reveal"), "gateway-bitmap-blocator-cow-candidates-v2", bitmapProfileHash(bitmapStandardProfile("district")), builtinBitmapDistrictProgram.semanticHash()})
	})
	return builtinBitmapDistrictProgram, builtinBitmapDistrictError
}

func recipeBitmapDistrictCheckEquivalence(semantic string) error {
	if semantic != recipeBitmapDistrictEquivalentSemantic {
		return fmt.Errorf("Bitmap District recipe/source/profile changed; explicit migration required (actual %s)", semantic)
	}
	return nil
}

type bitmapRecipeState struct {
	block                        blockView
	current                      string
	occurrences                  map[string]inscriptionOccurrence
	winners, candidates, targets *ordinalTree
	winnerRefs                   []string
	writes                       uint64
	seen                         map[string]bool
	closed                       bool
}

func bitmapRecipeValue(c bitmapDevelopmentCandidate) recipeOrderedValue {
	ref := c.Blocator
	if ref == "" {
		ref = c.RevealCoordinate
	}
	district := uint64(0)
	if c.District != nil {
		district = *c.District
	}
	return recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{
		"kind": rovText(c.Kind), "ownership": rovText(c.Ownership), "ref": rovText(ref), "block": rovText(c.RevealHash), "height": rovUint(uint64(c.RevealHeight)), "tx_index": rovUint(uint64(c.TxIndex)), "reveal_index": rovUint(uint64(c.InscriptionIndex)), "content_hash": rovText(c.ContentHash), "parent_count": rovUint(uint64(c.DeclaredParentCount)), "content": rovBytes([]byte(c.Content)), "decode_state": rovText(c.DecodeState), "eligibility": rovText(c.Eligibility), "selection": rovText(c.Selection), "reason": rovText(c.Reason), "has_district": rovBool(c.District != nil), "district": rovUint(district),
	}}
}

func (r *bitmapRecipeState) ReadOrdered(ctx context.Context, key recipeStateKey) (recipeOrderedValue, error) {
	if err := ctx.Err(); err != nil {
		return recipeOrderedValue{}, err
	}
	if r.closed {
		return recipeOrderedValue{}, fmt.Errorf("Bitmap recipe state closed")
	}
	var value bitmapDevelopmentCandidate
	var have bool
	var err error
	switch key.Table {
	case "winners":
		n, e := strconv.ParseUint(key.Key, 10, 64)
		if e != nil || strconv.FormatUint(n, 10) != key.Key {
			return recipeOrderedValue{}, fmt.Errorf("noncanonical District key")
		}
		have, err = r.winners.get(bitmapTargetKey(n, nil), &value)
	case "candidates":
		c, ok := parseBODCoordinate(key.Key)
		if !ok || !bitmapValidInscriptionBlocator(key.Key) {
			return recipeOrderedValue{}, fmt.Errorf("noncanonical candidate key")
		}
		have, err = r.candidates.get(bitmapCandidateKey(bitmapDevelopmentCandidate{RevealHeight: c.Height, TxIndex: c.TxIndex, InscriptionIndex: uint32(c.InscriptionIndex)}), &value)
	default:
		return recipeOrderedValue{}, fmt.Errorf("undeclared Bitmap table")
	}
	if err != nil {
		return recipeOrderedValue{}, err
	}
	if !have {
		return recipeOrderedValue{Kind: "missing"}, nil
	}
	return bitmapRecipeValue(value), nil
}

func (r *bitmapRecipeState) decodeProposal(value recipeOrderedValue) (bitmapDevelopmentCandidate, error) {
	fail := func() (bitmapDevelopmentCandidate, error) {
		return bitmapDevelopmentCandidate{}, fmt.Errorf("Bitmap candidate proposal differs from its admitted source/schema")
	}
	if value.Kind != "object" || len(value.Object) != 16 {
		return fail()
	}
	v := value.Object
	for _, name := range []string{"kind", "ownership", "ref", "block", "content_hash", "decode_state", "eligibility", "selection", "reason"} {
		if v[name].Kind != "text" {
			return fail()
		}
	}
	for _, name := range []string{"height", "tx_index", "reveal_index", "parent_count", "district"} {
		if v[name].Kind != "uint" {
			return fail()
		}
	}
	if v["has_district"].Kind != "bool" || v["content"].Kind != "bytes" || v["ref"].Text != r.current {
		return fail()
	}
	o, ok := r.occurrences[r.current]
	if !ok {
		return fail()
	}
	if v["kind"].Text != "district" || v["ownership"].Text != "unknown" || v["block"].Text != r.block.Hash || v["height"].Uint != uint64(r.block.Height) || v["tx_index"].Uint != uint64(o.TxIndex) || v["reveal_index"].Uint != uint64(o.Index) || v["content_hash"].Text != o.ContentSHA256 || v["parent_count"].Uint != uint64(len(o.Envelope.Parents)) {
		return fail()
	}
	if len(v["content"].Bytes) > 128 || len(v["content"].Bytes) > 0 && !bytes.Equal(v["content"].Bytes, o.Body) {
		return fail()
	}
	if !v["has_district"].Bool && v["district"].Uint != 0 {
		return fail()
	}
	if v["decode_state"].Text != "pass" && v["decode_state"].Text != "fail" || v["eligibility"].Text != "eligible" && v["eligibility"].Text != "ineligible" || v["selection"].Text != "selected" && v["selection"].Text != "not-selected" {
		return fail()
	}
	c := bitmapDevelopmentCandidate{ID: o.ID, Blocator: r.current, Kind: v["kind"].Text, RevealCoordinate: r.current, RevealHeight: r.block.Height, RevealHash: r.block.Hash, TxIndex: o.TxIndex, InscriptionIndex: o.Index, ContentHash: o.ContentSHA256, Content: string(v["content"].Bytes), Eligibility: v["eligibility"].Text, Selection: v["selection"].Text, Reason: v["reason"].Text, Parents: append([]string(nil), o.Envelope.Parents...), DeclaredParentCount: len(o.Envelope.Parents), Ownership: v["ownership"].Text, DecodeState: v["decode_state"].Text}
	if v["has_district"].Bool {
		n := v["district"].Uint
		c.District = &n
	}
	return c, nil
}

func (r *bitmapRecipeState) PutOrdered(ctx context.Context, key recipeStateKey, value recipeOrderedValue) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.closed {
		return fmt.Errorf("Bitmap recipe state closed")
	}
	c, err := r.decodeProposal(value)
	if err != nil {
		return err
	}
	switch key.Table {
	case "winners":
		if c.District == nil || key.Key != strconv.FormatUint(*c.District, 10) || c.Selection != "selected" || c.Eligibility != "eligible" {
			return fmt.Errorf("invalid winner proposal identity")
		}
		if err = r.winners.put(bitmapTargetKey(*c.District, nil), c); err != nil {
			return err
		}
		r.winnerRefs = append(r.winnerRefs, c.Blocator)
	case "candidates":
		if key.Key != c.Blocator || r.seen[key.Key] {
			return fmt.Errorf("duplicate or mismatched candidate proposal")
		}
		r.seen[key.Key] = true
		physical := bitmapCandidateKey(c)
		if err = r.candidates.put(physical, c); err != nil {
			return err
		}
		if c.District != nil {
			if err = r.targets.put(bitmapTargetKey(*c.District, nil)+physical, c); err != nil {
				return err
			}
		}
		r.writes++
	default:
		return fmt.Errorf("undeclared Bitmap table")
	}
	return nil
}
func (r *bitmapRecipeState) DeleteOrdered(context.Context, recipeStateKey) error {
	return fmt.Errorf("native Bitmap candidate codec does not delete records")
}
func (r *bitmapRecipeState) AbortOrdered() { r.closed = true }

func (s *bitmapRunConsumer) applyDistrictRecipe(ctx context.Context, block blockView, inventory *inscriptionBlockResult, h bitmapDevelopmentHead, row bitmapDevelopmentBlock, winners, candidates, targets, blocks *ordinalTree) error {
	p, err := builtinBitmapDistrictRecipe()
	if err != nil {
		return err
	}
	if err = recipeBitmapDistrictCheckEquivalence(builtinBitmapDistrictSemantic); err != nil {
		return err
	}
	if s.instance.ProfileHash != bitmapProfileHash(bitmapStandardProfile("district")) {
		return fmt.Errorf("native District codec requires its exact Standard profile")
	}
	state := &bitmapRecipeState{block: block, occurrences: map[string]inscriptionOccurrence{}, winners: winners, candidates: candidates, targets: targets, winnerRefs: []string{}, seen: map[string]bool{}}
	for _, o := range inventory.Occurrences {
		state.occurrences[inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height)] = o
	}
	defer state.AbortOrdered()
	err = recipeOrderedExecuteStateStream(ctx, *p.ordered, state, func(emit func(map[string]recipeOrderedValue) error) error {
		return recipeRevealInventoryWalk(ctx, block, inventory, func(event map[string]recipeOrderedValue) error { state.current = event["ref"].Text; return emit(event) })
	}, nil)
	if err != nil {
		return err
	}
	row.CandidateCount = int(state.writes)
	row.WinnerBlocators = append([]string{}, state.winnerRefs...)
	h.WinnerCount += uint64(len(state.winnerRefs))
	h.CandidateCount += state.writes
	h.LastBlock = row
	if err = blocks.put(fmt.Sprintf("%016x", block.Height), row); err != nil {
		return err
	}
	if err = s.lockProfileEvidence(ctx); err != nil {
		return err
	}
	roots, err := flushBitmapTrees([4]*ordinalTree{winners, candidates, targets, blocks})
	if err != nil {
		return err
	}
	h.WinnerRoot, h.CandidateRoot, h.TargetRoot, h.BlockRoot = roots[0], roots[1], roots[2], roots[3]
	return s.publish(ctx, h)
}
