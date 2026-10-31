package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Frozen Standard Parcel prefix from the pre-composition Apply oracle. Proof
// and winner processing begin only after syntaxValid becomes true.
func recipeLegacyParcelClaims(block blockView, inventory *inscriptionBlockResult) map[string]recipeParcelClaim {
	rows := map[string]recipeParcelClaim{}
	for _, o := range inventory.Occurrences {
		text, reason := bitmapDecodeProfileBody(bitmapStandardProfile("parcel"), o.Body, o.Envelope.ContentType)
		if reason != "" && reason != "unresolved_charset" {
			text = string(o.Body)
		}
		marker := bitmapClaimMarker(text)
		if reason != "unresolved_charset" && marker < 0 {
			continue
		}
		stem := text
		if marker >= 0 {
			stem = text[:marker]
		}
		parts := strings.Split(stem, ".")
		if reason != "unresolved_charset" && len(parts) != 2 {
			continue
		}
		ref := inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height)
		c := bitmapDevelopmentCandidate{ID: o.ID, Blocator: ref, Kind: "parcel", RevealCoordinate: ref, RevealHeight: block.Height, RevealHash: block.Hash, TxIndex: o.TxIndex, InscriptionIndex: o.Index, ContentHash: o.ContentSHA256, Eligibility: "ineligible", Selection: "not-selected", Reason: reason, Parents: append([]string(nil), o.Envelope.Parents...), DeclaredParentCount: len(o.Envelope.Parents), Ownership: "unknown", DecodeState: "pass"}
		if len(text) <= 128 {
			c.Content = text
		}
		if reason != "" {
			c.DecodeState = "fail"
		}
		fact := recipeParcelClaim{candidate: c}
		if reason == "" {
			if strings.Join(parts, ".")+".bitmap" != text {
				fact.candidate.Reason = "noncanonical_claim"
			} else if district, ok := bitmapCanonicalDecimal(parts[1]); !ok {
				fact.candidate.Reason = "noncanonical_district"
			} else {
				fact.candidate.District = &district
				if transaction, ok := bitmapCanonicalDecimal(parts[0]); !ok {
					fact.candidate.Reason = "noncanonical_transaction"
				} else {
					fact.candidate.Transaction = &transaction
					if district > uint64(block.Height) {
						fact.candidate.Reason = "future_block"
					} else {
						fact.syntaxValid = true
					}
				}
			}
		}
		rows[ref] = fact
	}
	return rows
}

func TestRecipeParcelClaimGrammarCompletePrefixParity(t *testing.T) {
	ctx := context.Background()
	block := bitmapDevelopmentFixtureBlock(792435, testID(669701), "0.bitmap", "0.0.bitmap", "01.0.bitmap", "0.01.bitmap", "0.792436.bitmap", "18446744073709551615.0.bitmap", "0.0.BITMAP", "0.0.bitmap\n", "0..bitmap", "0.0.0.bitmap", "\xff.0.bitmap", "0.0.bitmap.bitmap", "0.0.bitmap", ".0.bitmap", "0.18446744073709551615.bitmap", "0.18446744073709551616.bitmap", "18446744073709551616.0.bitmap", "0.0.bitmap")
	block.Transactions[13].Inputs[0].Witness = ordWitness050(inscriptionTestScript([]byte{1}, []byte("image/png"), nil, []byte("0.0.bitmap")))
	block.Transactions[18].Inputs[0].Witness = ordWitness050(inscriptionTestScript([]byte{1}, []byte("TEXT/PLAIN; charset=utf-16; x=y"), nil, []byte("0.0.bitmap")))
	inventory, err := extractInscriptionOccurrences(block)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := deriveParcelClaimsRecipe(ctx, block, &inventory)
	if err != nil {
		t.Fatal(err)
	}
	want := recipeLegacyParcelClaims(block, &inventory)
	if len(actual) == 0 || !reflect.DeepEqual(actual, want) {
		a, _ := json.Marshal(actual)
		b, _ := json.Marshal(want)
		t.Fatalf("Parcel claim prefix changed\nactual %s\nwant %s", a, b)
	}
	p, err := builtinParcelClaimsRecipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = recipeParcelClaimsCheckEquivalence(); err != nil {
		t.Fatal(err)
	}
	t.Log("Parcel claim recipe", p.semanticHash(), "equivalence bundle", builtinParcelClaimsSemantic)
}

func TestRecipeParcelClaimGrammarRealReferencePrefixParity(t *testing.T) {
	block, inventory := recipeDependencyReferenceBlock(t)
	actual, err := deriveParcelClaimsRecipe(context.Background(), block, inventory)
	if err != nil {
		t.Fatal(err)
	}
	want := recipeLegacyParcelClaims(block, inventory)
	if !reflect.DeepEqual(actual, want) {
		t.Fatal("real Parcel claim prefix changed", len(actual), len(want))
	}
}

func TestRecipeParcelClaimGrammarNativePhysicalAndPendingParity(t *testing.T) {
	ctx := context.Background()
	corpus := bitmapDevelopmentFixtureBlock(792435, testID(669702), "0.bitmap", "0.0.bitmap", "01.0.bitmap", "0.01.bitmap", "0.792436.bitmap", "0.0.BITMAP", "0.0.bitmap\n", "0..bitmap", "1.0.bitmap", "18446744073709551615.0.bitmap", "0.0.0.bitmap", "\xff.0.bitmap", "0.2.bitmap", "2.bitmap", "0.0.bitmap.bitmap")
	real, _ := recipeDependencyReferenceBlock(t)
	real.Verification.HeaderChainMatch = true
	for _, tc := range []struct {
		name  string
		block blockView
	}{{"malformed", corpus}, {"real", real}} {
		t.Run(tc.name, func(t *testing.T) {
			roots := [2]string{t.TempDir(), t.TempDir()}
			parcels := [2]*bitmapRunConsumer{}
			for index, root := range roots {
				district := bitmapTestConsumer(t, root, bitmapStandardDistrictID)
				if err := district.Apply(ctx, tc.block, nil); err != nil {
					t.Fatal(err)
				}
				parcel := bitmapTestConsumer(t, root, bitmapReplayParcelID)
				parcel.SetBlockSource(func(_ context.Context, height int64) (blockView, error) {
					if height == tc.block.Height {
						return tc.block, nil
					}
					return utxoFixtureBlock(height, "", utxoFixtureCoinbase(uint64(669800+height), subsidyAtHeight(height))), nil
				})
				parcels[index] = parcel
			}
			nextErr := parcels[0].Apply(ctx, tc.block, nil)
			oldErr := parcels[1].applyBitmapOracle(ctx, tc.block, nil)
			if (nextErr == nil) != (oldErr == nil) || errors.Is(nextErr, ErrBitmapEvidenceRequired) != errors.Is(oldErr, ErrBitmapEvidenceRequired) {
				t.Fatal(nextErr, oldErr)
			}
			if !reflect.DeepEqual(parcels[0].head, parcels[1].head) {
				t.Fatal("native Parcel head/pages/reference changed", parcels[0].head, parcels[1].head)
			}
			left, le := os.ReadFile(filepath.Join(parcels[0].dir, "pending.json"))
			right, re := os.ReadFile(filepath.Join(parcels[1].dir, "pending.json"))
			if !reflect.DeepEqual(left, right) || (le == nil) != (re == nil) {
				t.Fatalf("native pending payload changed\nnext %s\nold %s\nerrors %v %v", left, right, le, re)
			}
		})
	}
}

func TestRecipeParcelClaimGrammarPublicSavedComposition(t *testing.T) {
	ctx := context.Background()
	a, block, inventory, _ := recipeIdentityPointFixture(t)
	p, err := builtinParcelClaimsRecipe()
	if err != nil {
		t.Fatal(err)
	}
	d := p.definition
	d.ID, d.Name = "my-parcel-grammar", "My Parcel candidate grammar"
	p, err = compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	saved := saveTablesRecipeForRun(t, a, p)
	height := block.Height
	req := indexRunRequest{In: &height, Out: &height, Consumers: []indexRunSelection{{Index: saved.Recipe.ID, Armed: true}}}
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != height {
			t.Errorf("hidden candidate source %d", target.Height)
		}
		return block, nil
	}
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
	g, _ := openRecipeTablesGeneration(a.dataDir, p)
	_, point, err := g.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	oracle := recipeLegacyParcelClaims(block, &inventory)
	if point.Entries != uint64(len(oracle)) {
		t.Fatal("saved grammar row count", point.Entries, len(oracle))
	}
	edit := newRecipeTablesEdit(ctx, g.profileRoot, point.Root, d.Tables)
	occurrences := map[string]inscriptionOccurrence{}
	for _, o := range inventory.Occurrences {
		occurrences[inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height)] = o
	}
	for ref, want := range oracle {
		value, err := edit.ReadOrdered(ctx, recipeStateKey{Table: "candidates", Key: ref})
		if err != nil {
			t.Fatal(err)
		}
		base := map[string]recipeOrderedValue{}
		for name, item := range value.Object {
			if name != "syntax_valid" && name != "has_transaction" && name != "transaction" {
				base[name] = item
			}
		}
		actual, err := recipeDecodeBitmapCandidate(block, ref, occurrences, recipeOrderedValue{Kind: "object", Object: base}, "parcel")
		if err != nil {
			t.Fatal(err)
		}
		if value.Object["has_transaction"].Bool {
			n := value.Object["transaction"].Uint
			actual.Transaction = &n
		}
		if !reflect.DeepEqual(actual, want.candidate) || value.Object["syntax_valid"].Bool != want.syntaxValid {
			t.Fatal("saved grammar differs from native prefix", ref)
		}
	}
	for _, id := range []string{bitmapStandardDistrictID, bitmapReplayParcelID, "inscriptions", "utxo-state", "sat-state"} {
		if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", id, "head.json")); !os.IsNotExist(err) {
			t.Fatal("saved grammar started native parent", id, err)
		}
	}
	changed := d
	changed.ID = "my-custom-parcel-grammar"
	changed.Name = "Custom candidate grammar"
	changed.Ordered = []byte(strings.ReplaceAll(string(d.Ordered), "LmJpdG1hcA==", "LmN1c3RvbQ=="))
	custom, err := compileRecipe(changed)
	if err != nil || custom.semanticHash() == p.semanticHash() {
		t.Fatal("custom suffix did not change semantic identity", err)
	}
	customSaved := saveTablesRecipeForRun(t, a, custom)
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
}
