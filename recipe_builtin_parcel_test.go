package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestRecipeParcelCompositionPins(t *testing.T) {
	revision, semantic, err := recipeNativeBitmapDistrictRevision()
	if err != nil {
		t.Fatal(err)
	}
	parent := recipeDependency{Name: "districts", Scope: "builtin", Instance: bitmapStandardDistrictID, Revision: revision, SemanticHash: semantic, Export: "winners", Coverage: "snapshot", Type: "typed"}
	for _, provider := range []string{"retained-raw", "tx-locators", "txio-links"} {
		p, err := builtinParcelRecipe(provider)
		if err != nil {
			t.Fatal(provider, err)
		}
		if len(p.definition.Dependencies) != 1 || p.definition.Dependencies[0] != parent || len(p.definition.Lookups) != 3 || p.definition.Lookups[2].Provider != provider {
			t.Fatal("declared Parcel composition pins changed", provider)
		}
		if _, err = recipeValidateTablePointLookups(p.definition, map[string]bool{"winners": true, "candidates": true, "evaluation": true}, map[string]recipeDependency{"districts": parent}); err != nil {
			t.Fatal(err)
		}
		t.Log("Parcel composition", provider, p.semanticHash())
	}
	t.Log("Parcel composition equivalence bundle", builtinParcelSemantic)
	if err = recipeParcelCheckEquivalence(builtinParcelSemantic); err != nil {
		t.Fatal(err)
	}
	if err = recipeParcelCheckEquivalence(testID(889999)); err == nil {
		t.Fatal("unreviewed Parcel semantics admitted to native codec")
	}
}

// Controlled already-checked point facts exercise JSON policy independently
// of source authentication. Mainnet replay and native physical parity have
// separate tests; these synthetic facts are not admitted Bitcoin evidence.
type recipeParcelPolicyState struct {
	values map[recipeStateKey]recipeOrderedValue
	read   func(recipeStateKey) (recipeOrderedValue, error)
}

func (s *recipeParcelPolicyState) ReadOrdered(_ context.Context, key recipeStateKey) (recipeOrderedValue, error) {
	if key.Table == "winners" || key.Table == "candidates" || key.Table == "evaluation" {
		v, have := s.values[key]
		if !have {
			return recipeOrderedValue{Kind: "missing"}, nil
		}
		return v, nil
	}
	return s.read(key)
}
func (s *recipeParcelPolicyState) PutOrdered(_ context.Context, key recipeStateKey, value recipeOrderedValue) error {
	s.values[key] = value
	return nil
}
func (s *recipeParcelPolicyState) DeleteOrdered(_ context.Context, key recipeStateKey) error {
	delete(s.values, key)
	return nil
}
func (s *recipeParcelPolicyState) AbortOrdered() {}

func TestRecipeParcelCompositionCheckedFactsSelectOnlyFirstEligible(t *testing.T) {
	ctx := context.Background()
	p, err := builtinParcelRecipe("retained-raw")
	if err != nil {
		t.Fatal(err)
	}
	parentID := testID(889001) + "i0"
	parentRef := "0i0.792435"
	height := int64(792440)
	claims := []string{"0.0.bitmap", "0.0.bitmap", "1.0.bitmap", "2.0.bitmap", "3.0.bitmap", "0.1.bitmap", "0.2.bitmap", "2.0.bitmap", "01.0.bitmap", "0.0.BITMAP", "0.792441.bitmap", "unrelated"}
	events := []map[string]recipeOrderedValue{}
	for i, claim := range claims {
		parents := []recipeOrderedValue{rovText(parentID)}
		if i == 7 {
			parents = []recipeOrderedValue{rovText(testID(889002) + "i0")}
		}
		events = append(events, map[string]recipeOrderedValue{"kind": rovText("reveal"), "height": rovUint(uint64(height)), "block": rovText(testID(889003)), "tx_index": rovUint(uint64(i + 1)), "reveal_index": rovUint(0), "ref": rovText(inscriptionCoordinate(uint32(i+1), 0, height)), "content_hash": rovText(recipeStoreHash([]byte(claim))), "body": rovBytes([]byte(claim)), "content_type": rovBytes([]byte("text/plain")), "parent_count": rovUint(1), "parents": {Kind: "list", List: parents}})
	}
	state := &recipeParcelPolicyState{values: map[recipeStateKey]recipeOrderedValue{}}
	proofReads := 0
	state.read = func(key recipeStateKey) (recipeOrderedValue, error) {
		switch key.Table {
		case "districts":
			if key.Key == "1" {
				return recipeOrderedValue{Kind: "missing"}, nil
			}
			heightValue, tx := uint64(792435), uint64(0)
			if key.Key == "2" {
				heightValue, tx = uint64(height), 999
			}
			return recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"ref": rovText(parentRef), "height": rovUint(heightValue), "tx_index": rovUint(tx)}}, nil
		case "block_context":
			return recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"transaction_count": rovUint(3)}}, nil
		case "inscription_identity":
			return recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"id": rovText(parentID)}}, nil
		case "parent_replay":
			proofReads++
			child, _, ok := recipeInscriptionParentKey(key.Key, height)
			if !ok {
				return recipeOrderedValue{}, fmt.Errorf("unexpected proof key")
			}
			status := "verified"
			if child.TxIndex == 4 {
				status = "rejected"
			}
			return recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"state": rovText(status), "proof": rovBytes([]byte("controlled checked proof"))}}, nil
		}
		return recipeOrderedValue{}, fmt.Errorf("undeclared policy read %s", key.Table)
	}
	if err = recipeOrderedExecuteStateStream(ctx, *p.ordered, state, func(sink func(map[string]recipeOrderedValue) error) error {
		for _, event := range events {
			if err := sink(event); err != nil {
				return err
			}
		}
		return nil
	}, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"first_qualifying_canonical_reveal", "already_claimed", "first_qualifying_canonical_reveal", "parent_not_consumed_by_reveal", "transaction_out_of_range", "no_recognized_district_at_reveal", "district_parent_not_yet_recognized", "recognized_district_not_declared_parent", "noncanonical_transaction", "noncanonical_claim", "future_block"}
	for i, reason := range want {
		value, have := state.values[recipeStateKey{Table: "candidates", Key: events[i]["ref"].Text}]
		if !have || value.Object["reason"].Text != reason {
			t.Fatal(i, reason, value)
		}
	}
	winners, candidates, evaluations := 0, 0, 0
	for key, value := range state.values {
		switch key.Table {
		case "winners":
			winners++
			if value.Object["selection"].Text != "selected" || value.Object["eligibility"].Text != "eligible" {
				t.Fatal(value)
			}
		case "candidates":
			candidates++
		case "evaluation":
			evaluations++
		}
	}
	if winners != 2 || candidates != len(want) || evaluations != 0 || proofReads != 4 {
		t.Fatal(winners, candidates, evaluations, proofReads)
	}
	if _, have := state.values[recipeStateKey{Table: "winners", Key: "2.0"}]; have {
		t.Fatal("rejected relationship selected")
	}
}

func TestRecipeParcelCompositionNativeProviderVariantsPhysicalParity(t *testing.T) {
	ctx := context.Background()
	block := bitmapDevelopmentFixtureBlock(792435, testID(889101), "0.bitmap", "0.0.bitmap", "1.0.bitmap", "01.0.bitmap", "0.792436.bitmap", "not a claim")
	var expected *bitmapDevelopmentHead
	for _, provider := range []string{"retained-raw", "tx-locators", "txio-links"} {
		root := t.TempDir()
		district := bitmapTestConsumer(t, root, bitmapStandardDistrictID)
		if err := district.Apply(ctx, block, nil); err != nil {
			t.Fatal(err)
		}
		parcel := bitmapTestConsumer(t, root, bitmapReplayParcelID)
		parcel.parcelRecipeProvider = provider
		parcel.SetBlockSource(func(_ context.Context, h int64) (blockView, error) {
			return utxoFixtureBlock(h, "", utxoFixtureCoinbase(uint64(889102+h), subsidyAtHeight(h))), nil
		})
		if err := parcel.Apply(ctx, block, nil); err != nil {
			t.Fatal(provider, err)
		}
		if expected == nil {
			expected = parcel.head
		} else if !reflect.DeepEqual(expected, parcel.head) {
			t.Fatal("provider-only variant changed physical output", provider)
		}
		t.Log(provider, "candidates", strconv.FormatUint(parcel.head.CandidateCount, 10))
	}
}

func TestRecipeParcelCompositionPublicSavedRealRowsAndCustomGrammar(t *testing.T) {
	ctx := context.Background()
	a, block, _, _ := recipeIdentityPointFixture(t)
	district := bitmapTestConsumer(t, a.dataDir, bitmapStandardDistrictID)
	if err := district.Apply(ctx, block, nil); err != nil {
		t.Fatal(err)
	}
	beforeDistrict := district.head.Commitment
	p, err := builtinParcelRecipe("retained-raw")
	if err != nil {
		t.Fatal(err)
	}
	d := p.definition
	d.ID, d.Name = "my-complete-parcels", "My complete Parcel interpretation"
	p, err = compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	saved := saveTablesRecipeForRun(t, a, p)
	height := block.Height
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != height {
			t.Errorf("hidden Parcel source range %d", target.Height)
		}
		return block, nil
	}
	req := indexRunRequest{In: &height, Out: &height, Consumers: []indexRunSelection{{Index: saved.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a); done.Run.State != "complete" || done.Run.Counts.SourceReads != 1 {
		t.Fatal(done.Run)
	}
	g, err := openRecipeTablesGeneration(a.dataDir, p)
	if err != nil {
		t.Fatal(err)
	}
	_, point, err := g.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	oldDistrict := bitmapTestConsumer(t, root, bitmapStandardDistrictID)
	if err = oldDistrict.applyBitmapOracle(ctx, block, nil); err != nil {
		t.Fatal(err)
	}
	old := bitmapTestConsumer(t, root, bitmapReplayParcelID)
	old.SetBlockSource(func(_ context.Context, h int64) (blockView, error) {
		if h == height {
			return block, nil
		}
		if h == 0 {
			raw := testGenesisBlockPayload(t)
			return parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "public Parcel reference", false)
		}
		return blockView{}, fmt.Errorf("reference source unavailable")
	})
	if err = old.applyBitmapOracle(ctx, block, nil); err != nil {
		t.Fatal(err)
	}
	edit := newRecipeTablesEdit(ctx, g.profileRoot, point.Root, d.Tables, point.BlobPins)
	defer edit.AbortOrdered()
	for _, table := range []struct{ Name, Root string }{{"winners", old.head.WinnerRoot}, {"candidates", old.head.CandidateRoot}} {
		err = newOrdinalTree(filepath.Join(old.dir, "pages"), table.Root).withContext(ctx).walk(func(kv ordinalKV) error {
			var c bitmapDevelopmentCandidate
			if e := json.Unmarshal(kv.Value, &c); e != nil {
				return e
			}
			key := c.Blocator
			if table.Name == "winners" {
				key = strconv.FormatUint(*c.Transaction, 10) + "." + strconv.FormatUint(*c.District, 10)
			}
			actual, e := edit.ReadOrdered(ctx, recipeStateKey{Table: table.Name, Key: key})
			if e != nil {
				return e
			}
			want := recipeParcelValue(c)
			actual.Object["syntax_valid"] = want.Object["syntax_valid"]
			left, _ := json.Marshal(actual)
			right, _ := json.Marshal(want)
			if string(left) != string(right) {
				t.Fatalf("public Parcel row differs %s %s\n%s\n%s", table.Name, key, left, right)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if point.Entries != old.head.CandidateCount+old.head.WinnerCount {
		t.Fatal("saved Parcel inventory differs", point.Entries, old.head.CandidateCount, old.head.WinnerCount)
	}
	if latest, err := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapStandardDistrictID); err != nil || latest.Commitment != beforeDistrict {
		t.Fatal("saved Parcel changed its producer", err)
	}
	if _, err = os.Stat(filepath.Join(bitmapDevelopmentDir(a.dataDir, bitmapReplayParcelID), "head.json")); !os.IsNotExist(err) {
		t.Fatal("saved recipe started native Parcel", err)
	}
	if err = g.maintain(ctx); err != nil {
		t.Fatal(err)
	}
	custom := d
	custom.ID, custom.Name = "my-parcel-suffix", "My custom Parcel suffix"
	custom.Ordered = []byte(strings.ReplaceAll(string(d.Ordered), "LmJpdG1hcA==", "LmN1c3RvbQ=="))
	changed, err := compileRecipe(custom)
	if err != nil || changed.semanticHash() == p.semanticHash() {
		t.Fatal("custom grammar fingerprint unchanged", err)
	}
	customSaved := saveTablesRecipeForRun(t, a, changed)
	req.Consumers[0].Index = customSaved.Recipe.ID
	plan, err = a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a); done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
	out, _, err := a.readRecipeOutput(ctx, customSaved.Recipe.ID, customSaved.Revision, customSaved.SemanticHash, height, 0, 10, "")
	if err != nil || out["state"] != "covered" || out["state_entry_count"] != "0" {
		t.Fatal(out, err)
	}
	t.Log("authenticated real Parcel candidates", old.head.CandidateCount, "winners", old.head.WinnerCount)
}
