package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

//go:embed recipes/builtins/bitmap-parcels-standard-v1-retained-raw.json
var builtinParcelRawJSON []byte

//go:embed recipes/builtins/bitmap-parcels-standard-v1-tx-locators.json
var builtinParcelLocatorJSON []byte

//go:embed recipes/builtins/bitmap-parcels-standard-v1-txio-links.json
var builtinParcelTXIOJSON []byte

var builtinParcelOnce sync.Once
var builtinParcelPrograms map[string]*recipeProgram
var builtinParcelError error
var builtinParcelSemantic string

const recipeParcelEquivalentSemantic = "e2db73c5607f1cb89c37f6b8bce50a16cfb7ff920b6d6f2a310cf13ba1102a93"

func recipeParcelCheckEquivalence(semantic string) error {
	if semantic != recipeParcelEquivalentSemantic {
		return fmt.Errorf("Parcel JSON/source/profile changed; explicit migration required (actual %s)", semantic)
	}
	return nil
}

func recipeParcelCompositionSemantic() string {
	programs := []struct{ Provider, Program string }{}
	for _, provider := range []string{"retained-raw", "tx-locators", "txio-links"} {
		p := builtinParcelPrograms[provider]
		if p == nil {
			return ""
		}
		programs = append(programs, struct{ Provider, Program string }{provider, p.semanticHash()})
	}
	native, _ := bitmapCanonicalInstance(bitmapReplayParcelID)
	return indexDigest(struct {
		Admitted, Source, Physical, Profile string
		Programs                            []struct{ Provider, Program string }
	}{recipeBitmapAdmittedRevealProfile, recipeTablesSourceProfileFor("bitcoin.reveal"), "gateway-native-standard-parcel-composition-v1", native.ProfileHash, programs})
}

func builtinParcelRecipeBytes(provider string) []byte {
	switch provider {
	case "retained-raw":
		return builtinParcelRawJSON
	case "tx-locators":
		return builtinParcelLocatorJSON
	case "txio-links":
		return builtinParcelTXIOJSON
	}
	return nil
}
func builtinParcelRecipe(provider string) (*recipeProgram, error) {
	builtinParcelOnce.Do(func() {
		builtinParcelPrograms = map[string]*recipeProgram{}
		for _, route := range []string{"retained-raw", "tx-locators", "txio-links"} {
			d, err := parseRecipe(builtinParcelRecipeBytes(route))
			if err != nil {
				builtinParcelError = err
				return
			}
			p, err := compileRecipe(d)
			if err != nil {
				builtinParcelError = err
				return
			}
			builtinParcelPrograms[route] = p
		}
		builtinParcelSemantic = recipeParcelCompositionSemantic()
	})
	if builtinParcelError != nil {
		return nil, builtinParcelError
	}
	p := builtinParcelPrograms[provider]
	if p == nil {
		return nil, fmt.Errorf("explicit Parcel recipe provider required")
	}
	return p, nil
}

// Source availability is translated to the existing native pending view. Its
// candidate reason and presentation hint are supplied by the JSON program.
type recipeParcelUnavailable struct{ cause error }

func (e *recipeParcelUnavailable) Error() string { return e.cause.Error() }
func (e *recipeParcelUnavailable) Unwrap() error { return e.cause }
func (e *recipeParcelUnavailable) Is(target error) bool {
	return target == errRecipeDependencyUnavailable
}

type recipeParcelHint struct {
	Note                         string
	AppendError, ErrorDependency bool
}
type recipeParcelState struct {
	s                            *bitmapRunConsumer
	block                        blockView
	program                      *recipeProgram
	current                      string
	occurrences                  map[string]inscriptionOccurrence
	identities                   map[string]bitmapDevelopmentCandidate
	winners, candidates, targets *ordinalTree
	staged                       map[string]bitmapDevelopmentCandidate
	order, winnerRefs            []string
	hints                        map[string]recipeParcelHint
	closed                       bool
}

func recipeParcelValue(c bitmapDevelopmentCandidate) recipeOrderedValue {
	v := bitmapRecipeValue(c)
	v.Object["has_transaction"] = rovBool(c.Transaction != nil)
	n := uint64(0)
	if c.Transaction != nil {
		n = *c.Transaction
	}
	v.Object["transaction"] = rovUint(n)
	v.Object["syntax_valid"] = rovBool(c.District != nil && c.Transaction != nil && c.DecodeState == "pass")
	v.Object["selected_parent_declared"] = rovBool(c.SelectedParentDeclared)
	ref := ""
	if len(c.ParentBlocators) > 0 {
		ref = c.ParentBlocators[0]
	}
	v.Object["parent_ref"] = rovText(ref)
	raw := []byte{}
	if c.Provenance != nil {
		raw, _ = json.Marshal(c.Provenance)
	}
	v.Object["provenance"] = rovBytes(raw)
	deps := []recipeOrderedValue{}
	for _, dep := range c.UnresolvedDependencies {
		deps = append(deps, rovText(dep))
	}
	v.Object["unresolved_dependencies"] = recipeOrderedValue{Kind: "list", List: deps}
	return v
}
func recipeParcelWinnerKey(key string) (uint64, uint64, error) {
	parts := strings.Split(key, ".")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid Parcel winner key")
	}
	tx, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || strconv.FormatUint(tx, 10) != parts[0] {
		return 0, 0, fmt.Errorf("noncanonical Parcel transaction key")
	}
	district, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || strconv.FormatUint(district, 10) != parts[1] {
		return 0, 0, fmt.Errorf("noncanonical Parcel District key")
	}
	return district, tx, nil
}
func (r *recipeParcelState) ReadOrdered(ctx context.Context, key recipeStateKey) (recipeOrderedValue, error) {
	if err := ctx.Err(); err != nil {
		return recipeOrderedValue{}, err
	}
	if r.closed {
		return recipeOrderedValue{}, fmt.Errorf("Parcel recipe state closed")
	}
	missing := recipeOrderedValue{Kind: "missing"}
	unavailable := func(err error) (recipeOrderedValue, error) {
		if ctx.Err() != nil {
			return missing, ctx.Err()
		}
		return missing, &recipeParcelUnavailable{cause: err}
	}
	switch key.Table {
	case "winners":
		district, tx, err := recipeParcelWinnerKey(key.Key)
		if err != nil {
			return missing, err
		}
		var c bitmapDevelopmentCandidate
		have, err := r.winners.get(bitmapTargetKey(district, &tx), &c)
		if err != nil || !have {
			return missing, err
		}
		return recipeParcelValue(c), nil
	case "candidates":
		c, have := r.staged[key.Key]
		if !have {
			return missing, nil
		}
		return recipeParcelValue(c), nil
	case "districts":
		district, err := strconv.ParseUint(key.Key, 10, 64)
		if err != nil || strconv.FormatUint(district, 10) != key.Key {
			return missing, fmt.Errorf("noncanonical District source key")
		}
		h, err := bitmapHeadAt(ctx, r.s.root, r.s.instance.DistrictInstance, r.block.Height)
		if err != nil {
			return missing, err
		}
		if h == nil || h.ProfileHash != r.s.instance.DistrictProfileHash || h.BlockHash != r.block.Hash || !h.LastBlock.BlockEvaluated || !h.LastBlock.CompleteScope {
			return unavailable(fmt.Errorf("matching completed selected District evaluation unavailable"))
		}
		var c bitmapDevelopmentCandidate
		have, err := newOrdinalTree(filepath.Join(bitmapDevelopmentDir(r.s.root, r.s.instance.DistrictInstance), "pages"), h.WinnerRoot).withContext(ctx).get(bitmapTargetKey(district, nil), &c)
		if err != nil || !have {
			return missing, err
		}
		r.identities[c.Blocator] = c
		return bitmapRecipeValue(c), nil
	case "block_context":
		height, err := strconv.ParseInt(key.Key, 10, 64)
		if err != nil || height < 0 || height > r.block.Height || strconv.FormatInt(height, 10) != key.Key {
			return missing, fmt.Errorf("noncanonical target height")
		}
		block := r.block
		if height != block.Height {
			if r.s.blockAt == nil {
				return unavailable(fmt.Errorf("referenced block transaction count is unavailable"))
			}
			block, err = r.s.blockAt(ctx, height)
			if err != nil {
				return unavailable(err)
			}
		}
		if block.Height != height || !integrityVerified(block) || uint64(len(block.Transactions)) != block.TransactionCount {
			return missing, fmt.Errorf("Parcel transaction-count source mismatch")
		}
		if r.s.hashAt != nil {
			hash, e := r.s.hashAt(height)
			if e != nil {
				return missing, e
			}
			if hash != block.Hash {
				return missing, fmt.Errorf("Parcel context is on a replaced source branch")
			}
		}
		return recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"height": rovUint(uint64(height)), "block": rovText(block.Hash), "transaction_count": rovUint(block.TransactionCount)}}, nil
	case "inscription_identity":
		parent, have := r.identities[key.Key]
		if !have {
			return missing, fmt.Errorf("native identity source position was not read")
		}
		parent, err := r.s.selectedParentIdentity(ctx, r.block, parent)
		if err != nil {
			return unavailable(err)
		}
		if !isInscriptionID(parent.ID) {
			return missing, fmt.Errorf("selected parent identity is invalid")
		}
		r.identities[key.Key] = parent
		return recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"ref": rovText(key.Key), "id": rovText(parent.ID), "txid": rovText(parent.ID[:64]), "block": rovText(parent.RevealHash), "height": rovUint(uint64(parent.RevealHeight)), "tx_index": rovUint(uint64(parent.TxIndex)), "reveal_index": rovUint(uint64(parent.InscriptionIndex))}}, nil
	case "parent_replay":
		child, parent, ok := recipeInscriptionParentKey(key.Key, r.block.Height)
		if !ok {
			return missing, fmt.Errorf("invalid replay point pair")
		}
		parts := strings.Split(key.Key, ";")
		identity, have := r.identities[parts[1]]
		if !have || identity.ID == "" {
			return missing, fmt.Errorf("authenticated parent identity required")
		}
		o, have := r.occurrences[parts[0]]
		if !have || o.TxIndex != child.TxIndex || o.Index != uint32(child.InscriptionIndex) || identity.RevealHeight != parent.Height {
			return missing, fmt.Errorf("replay point positions changed")
		}
		proof, err := r.s.checkedReplayParent(ctx, r.block, o, identity)
		if err != nil {
			return unavailable(err)
		}
		if proof == nil {
			return missing, nil
		}
		raw, err := json.Marshal(proof)
		if err != nil {
			return missing, err
		}
		return recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"child_ref": rovText(parts[0]), "parent_ref": rovText(parts[1]), "state": rovText(proof.RelationState), "proof": rovBytes(raw)}}, nil
	}
	return missing, fmt.Errorf("undeclared Parcel input/table")
}

func (r *recipeParcelState) decode(value recipeOrderedValue) (bitmapDevelopmentCandidate, error) {
	if value.Kind != "object" || len(value.Object) != 23 {
		return bitmapDevelopmentCandidate{}, fmt.Errorf("invalid Parcel proposal shape")
	}
	v := value.Object
	base := map[string]recipeOrderedValue{}
	for name, item := range v {
		switch name {
		case "has_transaction", "transaction", "syntax_valid", "selected_parent_declared", "parent_ref", "provenance", "unresolved_dependencies":
		default:
			base[name] = item
		}
	}
	// Unresolved rows are retained only in the in-memory abort projection; the
	// native replay writer publishes no provisional candidate journal.
	unresolved := base["eligibility"].Text == "unresolved"
	if unresolved {
		base["eligibility"] = rovText("ineligible")
	}
	c, err := recipeDecodeBitmapCandidate(r.block, r.current, r.occurrences, recipeOrderedValue{Kind: "object", Object: base}, "parcel")
	if err != nil {
		return c, err
	}
	if unresolved {
		c.Eligibility = "unresolved"
	}
	if v["has_transaction"].Kind != "bool" || v["transaction"].Kind != "uint" || v["syntax_valid"].Kind != "bool" || v["selected_parent_declared"].Kind != "bool" || v["parent_ref"].Kind != "text" || v["provenance"].Kind != "bytes" || v["unresolved_dependencies"].Kind != "list" || v["unresolved_dependencies"].List == nil {
		return c, fmt.Errorf("invalid typed Parcel fields")
	}
	if v["has_transaction"].Bool {
		n := v["transaction"].Uint
		c.Transaction = &n
	} else if v["transaction"].Uint != 0 {
		return c, fmt.Errorf("unexpected Parcel transaction")
	}
	c.SelectedParentDeclared = v["selected_parent_declared"].Bool
	if v["parent_ref"].Text != "" {
		if !bitmapValidInscriptionBlocator(v["parent_ref"].Text) {
			return c, fmt.Errorf("invalid parent reference proposal")
		}
		c.ParentBlocators = []string{v["parent_ref"].Text}
	}
	if len(v["provenance"].Bytes) > 0 {
		var proof bitmapParentProof
		if json.Unmarshal(v["provenance"].Bytes, &proof) != nil || proof.ChildBlocator != c.Blocator || proof.ChildBlockHash != c.RevealHash || proof.ParentBlocator != v["parent_ref"].Text {
			return c, fmt.Errorf("invalid Parcel proof projection")
		}
		c.Provenance = &proof
	}
	for _, dep := range v["unresolved_dependencies"].List {
		if dep.Kind != "text" {
			return c, fmt.Errorf("invalid dependency message")
		}
		c.UnresolvedDependencies = append(c.UnresolvedDependencies, dep.Text)
	}
	return c, nil
}
func (r *recipeParcelState) PutOrdered(ctx context.Context, key recipeStateKey, value recipeOrderedValue) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.closed {
		return fmt.Errorf("Parcel recipe state closed")
	}
	if key.Table == "evaluation" {
		v := value.Object
		if key.Key != r.current || value.Kind != "object" || len(v) != 3 || v["note"].Kind != "text" || v["append_error"].Kind != "bool" || v["error_dependency"].Kind != "bool" {
			return fmt.Errorf("invalid evaluation status projection")
		}
		r.hints[key.Key] = recipeParcelHint{Note: v["note"].Text, AppendError: v["append_error"].Bool, ErrorDependency: v["error_dependency"].Bool}
		return nil
	}
	c, err := r.decode(value)
	if err != nil {
		return err
	}
	switch key.Table {
	case "candidates":
		if key.Key != r.current {
			return fmt.Errorf("candidate proposal key differs")
		}
		if _, have := r.staged[key.Key]; !have {
			r.order = append(r.order, key.Key)
		}
		r.staged[key.Key] = c
		return nil
	case "winners":
		district, tx, err := recipeParcelWinnerKey(key.Key)
		if err != nil {
			return err
		}
		if c.District == nil || c.Transaction == nil || district != *c.District || tx != *c.Transaction || c.Selection != "selected" || c.Eligibility != "eligible" {
			return fmt.Errorf("winner proposal key/status differs")
		}
		c.RuleResults = bitmapRuleAudits(r.s.instance.Profile, c)
		if err = r.winners.put(bitmapTargetKey(district, &tx), c); err != nil {
			return err
		}
		r.winnerRefs = append(r.winnerRefs, c.Blocator)
		return nil
	}
	return fmt.Errorf("undeclared Parcel mutation")
}
func (r *recipeParcelState) DeleteOrdered(ctx context.Context, key recipeStateKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.closed || key.Table != "evaluation" || key.Key != r.current {
		return fmt.Errorf("native Parcel codec only clears evaluation status")
	}
	delete(r.hints, key.Key)
	return nil
}
func (r *recipeParcelState) AbortOrdered() { r.closed = true }

func (s *bitmapRunConsumer) applyParcelRecipe(ctx context.Context, block blockView, inventory *inscriptionBlockResult, h bitmapDevelopmentHead, row bitmapDevelopmentBlock, winners, candidates, targets, blocks *ordinalTree) error {
	provider := s.parcelRecipeProvider
	if provider == "" {
		provider = "retained-raw"
	}
	p, err := builtinParcelRecipe(provider)
	if err != nil {
		return err
	}
	if err = recipeParcelCheckEquivalence(builtinParcelSemantic); err != nil {
		return err
	}
	native, _ := bitmapCanonicalInstance(bitmapReplayParcelID)
	if s.instance.ProfileHash != native.ProfileHash {
		return fmt.Errorf("native Parcel recipe requires its exact Standard profile")
	}
	r := &recipeParcelState{s: s, block: block, program: p, occurrences: map[string]inscriptionOccurrence{}, identities: map[string]bitmapDevelopmentCandidate{}, winners: winners, candidates: candidates, targets: targets, staged: map[string]bitmapDevelopmentCandidate{}, order: []string{}, winnerRefs: []string{}, hints: map[string]recipeParcelHint{}}
	for _, o := range inventory.Occurrences {
		r.occurrences[inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height)] = o
	}
	defer r.AbortOrdered()
	err = recipeOrderedExecuteStateStream(ctx, *p.ordered, r, func(sink func(map[string]recipeOrderedValue) error) error {
		return recipeRevealInventoryWalk(ctx, block, inventory, func(event map[string]recipeOrderedValue) error { r.current = event["ref"].Text; return sink(event) })
	}, nil)
	if err != nil {
		if hint, have := r.hints[r.current]; have && errors.Is(err, errRecipeDependencyUnavailable) {
			staged := []bitmapDevelopmentCandidate{}
			for _, ref := range r.order {
				c := r.staged[ref]
				if ref == r.current && hint.ErrorDependency {
					c.UnresolvedDependencies = []string{err.Error()}
				}
				staged = append(staged, c)
			}
			note := hint.Note
			if hint.AppendError {
				note += err.Error()
			}
			return s.pending(ctx, block, staged, note)
		}
		return err
	}
	for _, ref := range r.order {
		c := r.staged[ref]
		c.RuleResults = bitmapRuleAudits(s.instance.Profile, c)
		key := bitmapCandidateKey(c)
		if err = candidates.put(key, c); err != nil {
			return err
		}
		if c.District != nil {
			if err = targets.put(bitmapTargetKey(*c.District, c.Transaction)+key, c); err != nil {
				return err
			}
		}
	}
	row.CandidateCount = len(r.order)
	row.WinnerBlocators = append([]string{}, r.winnerRefs...)
	h.WinnerCount += uint64(len(r.winnerRefs))
	h.CandidateCount += uint64(len(r.order))
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
