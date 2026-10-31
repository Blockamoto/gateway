package main

import (
	"context"
	_ "embed"
	"fmt"
	"sync"
)

//go:embed recipes/builtins/bitmap-parcel-claims-standard-v1.json
var builtinParcelClaimsJSON []byte

var builtinParcelClaimsOnce sync.Once
var builtinParcelClaimsProgram *recipeProgram
var builtinParcelClaimsSemantic string
var builtinParcelClaimsError error

const recipeParcelClaimsEquivalentSemantic = "a810cff65d014869c44b24211f740dfb6d353101a01d43830638a0e1374fe50c"

func recipeParcelClaimsCheckEquivalence() error {
	if builtinParcelClaimsSemantic != recipeParcelClaimsEquivalentSemantic {
		return fmt.Errorf("Parcel grammar/source/profile changed; explicit migration required (actual %s)", builtinParcelClaimsSemantic)
	}
	return nil
}

// This document is the candidate grammar stage. Relationship proof and final
// winner state remain separate until their complete composition is integrated.
func builtinParcelClaimsRecipe() (*recipeProgram, error) {
	builtinParcelClaimsOnce.Do(func() {
		d, err := parseRecipe(builtinParcelClaimsJSON)
		if err == nil {
			builtinParcelClaimsProgram, err = compileRecipe(d)
		}
		if err != nil {
			builtinParcelClaimsError = err
			return
		}
		builtinParcelClaimsSemantic = indexDigest(struct{ AdmittedSource, TypedSource, Stage, Profile, Program string }{recipeBitmapAdmittedRevealProfile, recipeTablesSourceProfileFor("bitcoin.reveal"), "gateway-standard-parcel-candidate-grammar-v1", bitmapProfileHash(bitmapStandardProfile("parcel")), builtinParcelClaimsProgram.semanticHash()})
	})
	return builtinParcelClaimsProgram, builtinParcelClaimsError
}

type recipeParcelClaim struct {
	candidate   bitmapDevelopmentCandidate
	syntaxValid bool
}
type recipeParcelClaimState struct {
	block       blockView
	current     string
	occurrences map[string]inscriptionOccurrence
	rows        map[string]recipeParcelClaim
	closed      bool
}

func (s *recipeParcelClaimState) ReadOrdered(context.Context, recipeStateKey) (recipeOrderedValue, error) {
	return recipeOrderedValue{}, fmt.Errorf("candidate grammar has no incoming state")
}
func (s *recipeParcelClaimState) DeleteOrdered(context.Context, recipeStateKey) error {
	return fmt.Errorf("candidate grammar cannot delete rows")
}
func (s *recipeParcelClaimState) AbortOrdered() { s.closed = true }
func (s *recipeParcelClaimState) PutOrdered(ctx context.Context, key recipeStateKey, value recipeOrderedValue) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closed || key.Table != "candidates" || key.Key != s.current || value.Kind != "object" || len(value.Object) != 19 {
		return fmt.Errorf("invalid Parcel grammar proposal")
	}
	if _, have := s.rows[key.Key]; have {
		return fmt.Errorf("duplicate Parcel grammar proposal")
	}
	v := value.Object
	if v["syntax_valid"].Kind != "bool" || v["has_transaction"].Kind != "bool" || v["transaction"].Kind != "uint" || !v["has_transaction"].Bool && v["transaction"].Uint != 0 {
		return fmt.Errorf("invalid Parcel transaction grammar fields")
	}
	base := map[string]recipeOrderedValue{}
	for name, item := range v {
		if name != "syntax_valid" && name != "has_transaction" && name != "transaction" {
			base[name] = item
		}
	}
	c, err := recipeDecodeBitmapCandidate(s.block, s.current, s.occurrences, recipeOrderedValue{Kind: "object", Object: base}, "parcel")
	if err != nil {
		return err
	}
	if c.Eligibility != "ineligible" || c.Selection != "not-selected" {
		return fmt.Errorf("candidate grammar cannot supply validated winner state")
	}
	if v["has_transaction"].Bool {
		n := v["transaction"].Uint
		c.Transaction = &n
	}
	if v["syntax_valid"].Bool && (c.District == nil || c.Transaction == nil || c.DecodeState != "pass" || c.Reason != "") {
		return fmt.Errorf("incomplete parsed Parcel proposal")
	}
	s.rows[key.Key] = recipeParcelClaim{candidate: c, syntaxValid: v["syntax_valid"].Bool}
	return nil
}

func deriveParcelClaimsRecipe(ctx context.Context, block blockView, inventory *inscriptionBlockResult) (map[string]recipeParcelClaim, error) {
	p, err := builtinParcelClaimsRecipe()
	if err != nil {
		return nil, err
	}
	state := &recipeParcelClaimState{block: block, occurrences: map[string]inscriptionOccurrence{}, rows: map[string]recipeParcelClaim{}}
	defer state.AbortOrdered()
	for _, o := range inventory.Occurrences {
		state.occurrences[inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height)] = o
	}
	err = recipeOrderedExecuteStateStream(ctx, *p.ordered, state, func(sink func(map[string]recipeOrderedValue) error) error {
		return recipeRevealInventoryWalk(ctx, block, inventory, func(event map[string]recipeOrderedValue) error { state.current = event["ref"].Text; return sink(event) })
	}, nil)
	return state.rows, err
}
