package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestRecipeAddressHistorySemanticMapping(t *testing.T) {
	if _, err := builtinAddressHistoryRecipes(); err != nil {
		t.Fatal(err)
	}
	if err := recipeAddressHistoryCheckNative(); err != nil {
		t.Fatal(err)
	}
	if err := recipeAddressHistoryCheckEquivalence(builtinAddressHistorySemantic, 2); err == nil {
		t.Fatal("unknown physical schema reused")
	}
	if err := recipeAddressHistoryCheckEquivalence(recipeTestHash("a"), 1); err == nil {
		t.Fatal("changed composition reused native data")
	}
}

func TestRecipeAddressHistoryRealReferenceAndPublicComposition(t *testing.T) {
	ctx := context.Background()
	block, _ := recipeDependencyReferenceBlock(t)
	root := t.TempDir()
	store, err := openAddressHistoryStoreContext(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := storeBlockOutputFactsContext(ctx, root, block)
	if err != nil {
		t.Fatal(err)
	}
	pages := map[string]bool{}
	for _, ref := range refs {
		pages[ref.Page] = true
	}
	programs, err := builtinAddressHistoryRecipes()
	if err != nil {
		t.Fatal(err)
	}
	for version := 0; version <= 1; version++ {
		metadata := addressHistoryBlock{Height: block.Height, BlockHash: block.Hash, DestinationVersion: version, FactPages: []string{}}
		for page := range pages {
			metadata.FactPages = append(metadata.FactPages, page)
		}
		sort.Strings(metadata.FactPages)
		for _, tx := range block.Transactions {
			for _, output := range tx.Outputs {
				raw, _ := hex.DecodeString(output.ScriptPubKey)
				_, address := describeScript(raw)
				if version == 1 {
					_, address = describeDestinationScript(raw)
				}
				if address != "" {
					metadata.AddressOutputs++
				}
			}
		}
		actual, want := map[string]string{}, map[string]string{}
		collect := func(target map[string]string) func(string, string, outputFactRef, outputFact) error {
			return func(address, kind string, ref outputFactRef, fact outputFact) error {
				position := outputCoordinate(int(fact.Vout), fact.TxIndex, fact.Height)
				raw, err := json.Marshal(map[string]any{"ref": position, "destination": address, "script_type": kind, "value_sats": json.Number(strconv.FormatUint(fact.ValueSats, 10)), "script": recipeBytes{Hex: fact.ScriptPubKey}, "block_hash": fact.BlockHash})
				if err != nil {
					return err
				}
				target[position] = string(raw)
				return nil
			}
		}
		if err = store.visitBlockFacts(ctx, metadata, collect(actual)); err != nil {
			t.Fatal(err)
		}
		if err = store.legacyVisitBlockFacts(ctx, metadata, collect(want)); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actual, want) || len(actual) == 0 {
			t.Fatal("real retained projection differs", version, len(actual), len(want))
		}
		public := map[string]string{}
		if err = programs[version].evaluateOrderedSparseInto(ctx, block, func(row recipeRowProposal) error {
			raw, err := json.Marshal(row.Value)
			if err != nil {
				return err
			}
			public[row.Key] = string(raw)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(public, actual) {
			t.Fatal("same JSON differs between owned raw and retained facts", version, len(public), len(actual))
		}
	}
}

func TestRecipeAddressHistoryNativeCommitDifferential(t *testing.T) {
	ctx := context.Background()
	blocks := recipeCoordinatorSource(t)
	script, _ := addressHistoryTestScript(17)
	fixtures := []blockView{blocks[0], blocks[1], addressHistoryTestBlock(10, recipeTestHash("b"), outputView{ValueSats: 7, ScriptPubKey: script}, outputView{ValueSats: 11, ScriptPubKey: "21" + address076PublicKey + "ac"}, outputView{ValueSats: 13, ScriptPubKey: "41" + address076UncompressedKey + "ac"}, outputView{ValueSats: 17, ScriptPubKey: "6a"}, outputView{ValueSats: 19, ScriptPubKey: "51"})}
	for _, block := range fixtures {
		t.Run(strings.Repeat("h", 1)+strconv.FormatInt(block.Height, 10), func(t *testing.T) {
			var heads []addressHistoryHead
			for _, legacy := range []bool{false, true} {
				root := t.TempDir()
				store, err := openAddressHistoryStoreContext(ctx, root)
				if err != nil {
					t.Fatal(err)
				}
				refs, err := storeBlockOutputFactsContext(ctx, root, block)
				if err != nil {
					t.Fatal(err)
				}
				hashAt := addressHistoryTestHashAt(map[int64]string{block.Height: block.Hash})
				if legacy {
					err = store.legacyCommitFacts(ctx, block.Height, block.Hash, refs, hashAt)
				} else {
					err = store.commitFacts(ctx, block.Height, block.Hash, refs, hashAt)
				}
				if err != nil {
					t.Fatal(err)
				}
				heads = append(heads, store.head)
				// Reapplying the same anchor must preserve the exact physical head.
				before := store.head
				if legacy {
					err = store.legacyCommitFacts(ctx, block.Height, block.Hash, refs, hashAt)
				} else {
					err = store.commitFacts(ctx, block.Height, block.Hash, refs, hashAt)
				}
				if err != nil {
					t.Fatal(err)
				}
				if before.AddressRoot != store.head.AddressRoot || before.BlocksRoot != store.head.BlocksRoot {
					t.Fatal("same anchor changed physical rows")
				}
			}
			if !reflect.DeepEqual(heads[0], heads[1]) {
				t.Fatal("native JSON commit differs from frozen baseline", heads)
			}
		})
	}
}

func TestRecipeAddressHistoryRetainedPaymentAndP2PKRules(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := openAddressHistoryStoreContext(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	script, _ := addressHistoryTestScript(21)
	block := addressHistoryTestBlock(10, recipeTestHash("b"), outputView{ValueSats: 5, ScriptPubKey: script}, outputView{ValueSats: 7, ScriptPubKey: "21" + address076PublicKey + "ac"}, outputView{ValueSats: 11, ScriptPubKey: "6a"})
	refs, err := storeBlockOutputFactsContext(ctx, root, block)
	if err != nil {
		t.Fatal(err)
	}
	pages := map[string]bool{}
	for _, ref := range refs {
		pages[ref.Page] = true
	}
	for _, version := range []int{0, 1} {
		metadata := addressHistoryBlock{Height: block.Height, BlockHash: block.Hash, DestinationVersion: version, FactPages: []string{}}
		for page := range pages {
			metadata.FactPages = append(metadata.FactPages, page)
		}
		sort.Strings(metadata.FactPages)
		for _, output := range block.Transactions[0].Outputs {
			raw, _ := hex.DecodeString(output.ScriptPubKey)
			_, address := describeScript(raw)
			if version == 1 {
				_, address = describeDestinationScript(raw)
			}
			if address != "" {
				metadata.AddressOutputs++
			}
		}
		type selected struct {
			Address, Kind string
			Ref           outputFactRef
			Fact          outputFact
		}
		var actual, want []selected
		if err = store.visitBlockFacts(ctx, metadata, func(address, kind string, ref outputFactRef, fact outputFact) error {
			actual = append(actual, selected{address, kind, ref, fact})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err = store.legacyVisitBlockFacts(ctx, metadata, func(address, kind string, ref outputFactRef, fact outputFact) error {
			want = append(want, selected{address, kind, ref, fact})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actual, want) {
			t.Fatal("retained destination policy changed", version, actual, want)
		}
	}
}
