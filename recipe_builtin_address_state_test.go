package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
)

func TestRecipeAddressStateSemanticMapping(t *testing.T) {
	if _, err := builtinAddressStateRecipes(); err != nil {
		t.Fatal(err)
	}
	if err := recipeAddressStateCheckNative(); err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct {
		semantic string
		schema   int
	}{{recipeTestHash("a"), 1}, {builtinAddressStateSemantic, 2}} {
		if err := recipeAddressStateCheckEquivalence(pair.semantic, pair.schema); err == nil {
			t.Fatal("unknown native projection binding reused")
		}
	}
}

func TestRecipeAddressStatePhysicalDifferentialAndNetChanges(t *testing.T) {
	ctx := context.Background()
	u, _, _ := address076Source(t, []uint64{7, 11, 13, 17}, []string{address076P2PKH, "21" + address076PublicKey + "ac", "41" + address076UncompressedKey + "ac", "51"})
	var members []utxoMember
	if err := u.trees(ctx, u.head.Checkpoint)["live"].walk(func(kv ordinalKV) error {
		var member utxoMember
		if err := json.Unmarshal(kv.Value, &member); err != nil {
			return err
		}
		members = append(members, member)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for version := 0; version <= 1; version++ {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			initial := addressStateCheckpoint{DestinationVersion: version, PageGeneration: "frozen-comparison-" + strconv.Itoa(version)}
			actual, baseline := newAddressStateEditor(ctx, u.root, initial), newAddressStateEditor(ctx, u.root, initial)
			for _, member := range members {
				if err := actual.change(member, true); err != nil {
					t.Fatal(err)
				}
				if err := baseline.legacyChange(member, true); err != nil {
					t.Fatal(err)
				}
			}
			finish := func() {
				t.Helper()
				for _, editor := range []*addressStateEditor{actual, baseline} {
					if err := editor.flush(); err != nil {
						t.Fatal(err)
					}
					if err := editor.bind(u.head.Checkpoint); err != nil {
						t.Fatal(err)
					}
				}
				if !reflect.DeepEqual(actual.cp, baseline.cp) {
					t.Fatal("projected native checkpoint differs", actual.cp, baseline.cp)
				}
			}
			finish()
			before := actual.cp
			for _, member := range members {
				if err := actual.change(member, false); err != nil {
					t.Fatal(err)
				}
				if err := baseline.legacyChange(member, false); err != nil {
					t.Fatal(err)
				}
			}
			if actual.cp.OutputCount != 0 || actual.cp.ExcludedOutputCount != 0 || actual.cp.ValueSats != 0 || actual.cp.ExcludedValueSats != 0 {
				t.Fatal("net removal accounting", actual.cp)
			}
			for _, member := range members {
				if err := actual.change(member, true); err != nil {
					t.Fatal(err)
				}
				if err := baseline.legacyChange(member, true); err != nil {
					t.Fatal(err)
				}
			}
			finish()
			if !reflect.DeepEqual(actual.cp, before) {
				t.Fatal("net remove/recreate changed checkpoint")
			}
		})
	}
}
