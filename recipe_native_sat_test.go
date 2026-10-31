package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRecipeNativeSatSemanticEquivalence(t *testing.T) {
	programs, err := builtinSatRecipes()
	if err != nil {
		t.Fatal(err)
	}
	if err = recipeSatCheckEquivalence(builtinSatSemantic, 1); err != nil {
		t.Fatal(err)
	}
	if programs[0].semanticHash() == programs[1].semanticHash() {
		t.Fatal("history layer has no semantic identity")
	}
	if recipeSatCheckEquivalence(builtinSatSemantic+"changed", 1) == nil || recipeSatCheckEquivalence(builtinSatSemantic, 2) == nil {
		t.Fatal("unknown native semantics adopted")
	}
	d, err := findIndexDefinition("sat-state")
	if err != nil || d.ExecutableRecipeHash != programs[0].semanticHash() || len(d.ExecutableRecipeVariants) != 4 {
		t.Fatal("native catalogue lacks actual documents", err)
	}
}

func TestRecipeSatDocumentContractAndRename(t *testing.T) {
	d, err := parseRecipe(builtinSatRangesJSON)
	if err != nil {
		t.Fatal(err)
	}
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	d.ID, d.Name = "my-sats", "My sats"
	renamed, err := compileRecipe(d)
	if err != nil || renamed.semanticHash() != p.semanticHash() {
		t.Fatal("presentation identity changed FIFO semantics", err)
	}
	for _, change := range []string{"origin", "source_field", "table", "history_read", "dependency", "storage"} {
		copy, err := parseRecipe(builtinSatRangesJSON)
		if err != nil {
			t.Fatal(err)
		}
		switch change {
		case "origin":
			copy.Origin = 1
		case "source_field":
			copy.Ordered = []byte(strings.Replace(string(copy.Ordered), `"kind"`, `"absent_source_field"`, 1))
		case "table":
			copy.Tables = append(copy.Tables, recipeTable{Name: "other", Key: "output"})
		case "history_read":
			copy.Ordered = []byte(strings.Replace(string(copy.Ordered), `"table": "outputs"`, `"table": "history"`, 1))
		case "dependency":
			copy.Dependencies = []recipeDependency{{Name: "unresolved"}}
		case "storage":
			copy.Storage = "dispatch-sat-index"
		}
		if _, err = compileRecipe(copy); err == nil {
			t.Fatal("accepted", change)
		}
	}
}

func appendSatPhysicalOracle(t *testing.T, s *indexStore, block blockView) {
	t.Helper()
	p, err := s.deriveSatPhysicalOracle(block, nil)
	if err != nil {
		t.Fatal(block.Height, err)
	}
	b := indexBatch{PreviousBlockHash: block.PreviousBlockHash, Sats: p}
	d := s.definition
	c := indexCheckpoint{Schema: d.CheckpointSchema, Definition: d.ID, Version: d.Version, RuleHash: d.RuleHash, Network: d.Network, From: 0, Height: block.Height, BlockHash: block.Hash, DependencyFingerprint: indexDigest(struct{ Block, Profile string }{block.Hash, satIndexProfile}), RecordsHash: batchRecordsHash(b)}
	if s.checkpoint != nil {
		c.PreviousCommitment = s.checkpoint.Commitment
	}
	c.Commitment = checkpointHash(c)
	b.Checkpoint = c
	if err = os.MkdirAll(filepath.Join(s.dir, "commits"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = atomicWriteJSON(filepath.Join(s.dir, "commits", c.Commitment+".json"), b); err != nil {
		t.Fatal(err)
	}
	s.head.Mode = "lean"
	s.head.Commitment, s.head.Retention = c.Commitment, "pruned"
	s.head.SatSavepoints = satRotateSavepoints077(s.head.SatSavepoints, c)
	s.head.SatSavepointsHash = satSavepointsDigest077(s.head)
	if err = atomicWriteJSON(filepath.Join(s.dir, "head.json"), s.head); err != nil {
		t.Fatal(err)
	}
	s.checkpoint = &c
}

func TestRecipeNativeSatExactPhysicalParity(t *testing.T) {
	for _, history := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "history"}[history], func(t *testing.T) {
			old, err := openIndexStore(t.TempDir(), "sat-state")
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := openIndexStore(t.TempDir(), "sat-state")
			if err != nil {
				t.Fatal(err)
			}
			old.retainSatHistory, fresh.retainSatHistory = history, history
			b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(861000, subsidyAtHeight(0)))
			cb := utxoFixtureCoinbase(861001, 10, subsidyAtHeight(1)-10)
			b1 := utxoFixtureBlock(1, b0.Hash, cb)
			pointer := inscriptionTestScript([]byte{2}, []byte{1}, nil, []byte("pointer"))
			unbound := inscriptionTestScript([]byte{4}, []byte{1}, nil, []byte("unbound"))
			one := utxoFixtureSpend(861002, []inputView{{N: 0, PrevTxID: cb.TxID, Witness: ordWitness050(append(pointer, unbound...))}}, 6, 2, 0)
			one.Outputs[1].ScriptPubKey = "6a"
			two := utxoFixtureSpend(861003, []inputView{{N: 0, PrevTxID: one.TxID}}, 5)
			b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(861004, subsidyAtHeight(2)+1), one, two)
			for _, block := range []blockView{b0, b1, b2} {
				appendSatPhysicalOracle(t, old, block)
				if err = fresh.appendBlock(block, 0, "pruned"); err != nil {
					t.Fatal(block.Height, err)
				}
				oldBatch, e1 := old.readBatch(old.head.Commitment)
				newBatch, e2 := fresh.readBatch(fresh.head.Commitment)
				if e1 != nil || e2 != nil || !reflect.DeepEqual(oldBatch, newBatch) || !reflect.DeepEqual(old.head, fresh.head) {
					t.Fatalf("physical bytes/checkpoint differ at %d: %v %v\nold=%+v\nnew=%+v", block.Height, e1, e2, oldBatch.Sats, newBatch.Sats)
				}
			}
			// The physical roots and complete checkpoint are identical, so native
			// restart, source facts and existing reverse queries read the same data.
			fresh, err = openIndexStore(fresh.profileRoot, "sat-state")
			if err != nil {
				t.Fatal(err)
			}
			for _, sat := range []uint64{0, firstSatAtHeight(1), firstSatAtHeight(1) + 6, firstSatAtHeight(1) + 9} {
				a, e1 := querySatNumberContext(context.Background(), old.profileRoot, sat)
				b, e2 := querySatNumberContext(context.Background(), fresh.profileRoot, sat)
				if e1 != nil || e2 != nil || !reflect.DeepEqual(a, b) {
					t.Fatal(sat, a, b, e1, e2)
				}
			}
		})
	}
}
