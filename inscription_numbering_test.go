package main

import (
	"fmt"
	"path/filepath"
	"testing"
)

func numberingFixture(t *testing.T, height int64, txs ...transactionView) (blockView, []inscriptionOccurrence) {
	t.Helper()
	b := satIndexTestBlock(height, "", append([]transactionView{testCoinbase(testID(uint64(height)+100), subsidyAtHeight(height)+1000)}, txs...)...)
	x, e := extractInscriptionOccurrences(b)
	if e != nil {
		t.Fatal(e)
	}
	return b, x.Occurrences
}
func numberingTx(id uint64, inputs int, output uint64, scripts ...[]byte) transactionView {
	tx := testTx(testID(id), make([]inputView, inputs), output)
	for i := range tx.Inputs {
		tx.Inputs[i].PrevTxID = testID(id + 1000 + uint64(i))
		if i < len(scripts) {
			tx.Inputs[i].Witness = ordWitness050(scripts[i])
		}
	}
	return tx
}
func numberingStore(t *testing.T) *indexStore {
	t.Helper()
	s, e := openIndexStore(t.TempDir(), "inscriptions")
	if e != nil {
		t.Fatal(e)
	}
	s.numberingValues = func(tx transactionView, height int64) ([]uint64, error) {
		v := make([]uint64, len(tx.Inputs))
		for i := range v {
			v[i] = 100
		}
		return v, nil
	}
	return s
}
func TestInscriptionNumberingHistoricalCurseJubileeAndPartial(t *testing.T) {
	plain := inscriptionTestScript(nil, []byte("x"))
	pointer := inscriptionTestScript([]byte{2}, []byte{1}, nil, []byte("p"))
	unbound := inscriptionTestScript([]byte{4}, []byte{1}, nil, []byte("u"))
	for _, height := range []int64{firstMainnetInscriptionHeight, mainnetJubileeHeight} {
		t.Run(fmt.Sprint(height), func(t *testing.T) {
			s := numberingStore(t)
			tx := numberingTx(100, 2, 200, append(append(append([]byte{}, plain...), pointer...), unbound...), plain)
			b, rows := numberingFixture(t, height, tx)
			b.Transactions[0].Outputs[0].ValueSats = subsidyAtHeight(height)
			state, e := s.deriveInscriptionNumbering(b, firstMainnetInscriptionHeight, rows)
			if e != nil || state.State != "complete" {
				t.Fatal(state, e)
			}
			// Pointer 1 sorts after both offset-zero envelopes. Negative and
			// positive counters are independent, and Jubilee vindicates curses.
			want := []int64{0, -2, -1, -3}
			if height == mainnetJubileeHeight {
				want = []int64{0, 2, 1, 3}
			}
			for i, row := range rows {
				if !row.CanonicalNumberKnown || row.CanonicalNumber == nil || *row.CanonicalNumber != want[i] {
					t.Fatal(i, row)
				}
			}
			var stored numberedInscription
			tree := newOrdinalTree(s.ordinalPages(), state.RecordsRoot)
			ok, e := tree.get(ordinalInscriptionKey(rows[2].ID), &stored)
			if e != nil || !ok || !stored.Unbound {
				t.Fatal(stored, e)
			}
		})
	}
	s := numberingStore(t)
	b, rows := numberingFixture(t, 800000, numberingTx(200, 1, 100, plain))
	b.Transactions[0].Outputs[0].ValueSats = subsidyAtHeight(b.Height)
	state, e := s.deriveInscriptionNumbering(b, 800000, rows)
	if e != nil || state.State != "unavailable" || state.Reason != "partial_history" || rows[0].CanonicalNumberKnown {
		t.Fatal(state, rows, e)
	}
}
func TestInscriptionNumberingFeeOrderingAndMissingValues(t *testing.T) {
	plain := inscriptionTestScript(nil, []byte("x"))
	a := numberingTx(300, 1, 0, plain)
	btx := numberingTx(301, 1, 100, plain)
	b, rows := numberingFixture(t, firstMainnetInscriptionHeight, a, btx)
	b.Transactions[0].Outputs[0].ValueSats = subsidyAtHeight(b.Height) + 100
	s := numberingStore(t)
	state, e := s.deriveInscriptionNumbering(b, firstMainnetInscriptionHeight, rows)
	if e != nil || state.State != "complete" {
		t.Fatal(state, e)
	}
	if *rows[0].CanonicalNumber != 1 || *rows[1].CanonicalNumber != 0 {
		t.Fatal("fee reveal was numbered before later normal output", rows)
	}
	s = numberingStore(t)
	s.numberingValues = func(transactionView, int64) ([]uint64, error) { return nil, fmt.Errorf("unavailable fixture input") }
	_, rows = numberingFixture(t, firstMainnetInscriptionHeight, a, btx)
	state, e = s.deriveInscriptionNumbering(b, firstMainnetInscriptionHeight, rows)
	if e != nil || state.State != "unavailable" {
		t.Fatal(state, e)
	}
	for _, row := range rows {
		if row.CanonicalNumberKnown || row.CanonicalNumber != nil {
			t.Fatal("dependency gap numbered", row)
		}
	}
}
func commitNumberingFixture(t *testing.T, s *indexStore, b blockView, state *inscriptionNumberingSnapshot) {
	t.Helper()
	// These fixtures exercise the historical numbering format, which predates
	// the positional Lean/Full recipe and its authenticated block denominator.
	d := legacyIndexDefinition(s.definition)
	batch := indexBatch{Numbering: state, Checkpoint: indexCheckpoint{Schema: d.CheckpointSchema, Definition: d.ID, Version: d.Version, RuleHash: d.RuleHash, Network: d.Network, From: firstMainnetInscriptionHeight, Height: b.Height, BlockHash: b.Hash}}
	batch.Checkpoint.RecordsHash = batchRecordsHash(batch)
	batch.Checkpoint.Commitment = checkpointHash(batch.Checkpoint)
	if e := atomicWriteJSON(filepath.Join(s.dir, "commits", batch.Checkpoint.Commitment+".json"), batch); e != nil {
		t.Fatal(e)
	}
	s.checkpoint = &batch.Checkpoint
}
func TestInscriptionNumberingReinscriptionTransfersAndRestart(t *testing.T) {
	plain := inscriptionTestScript(nil, []byte("x"))
	cursed := append(append([]byte{}, plain...), plain...)
	for _, initialCursed := range []bool{false, true} {
		s := numberingStore(t)
		script := plain
		if initialCursed {
			script = cursed
		}
		tx := numberingTx(400, 1, 100, script)
		b, rows := numberingFixture(t, firstMainnetInscriptionHeight, tx)
		b.Transactions[0].Outputs[0].ValueSats = subsidyAtHeight(b.Height)
		state, e := s.deriveInscriptionNumbering(b, firstMainnetInscriptionHeight, rows)
		if e != nil || state.State != "complete" {
			t.Fatal(state, e)
		}
		commitNumberingFixture(t, s, b, state)
		next := numberingTx(401, 1, 100, plain)
		next.Inputs[0].PrevTxID = tx.TxID
		b2, rows2 := numberingFixture(t, b.Height+1, next)
		b2.Transactions[0].Outputs[0].ValueSats = subsidyAtHeight(b2.Height)
		state2, e := s.deriveInscriptionNumbering(b2, firstMainnetInscriptionHeight, rows2)
		if e != nil || state2.State != "complete" || rows2[0].CanonicalNumber == nil || *rows2[0].CanonicalNumber >= 0 {
			t.Fatal("reinscription on blessed or multiple occupancy should be cursed", rows2, state2, e)
		}
		var output inscriptionNumberingOutput
		tree := newOrdinalTree(s.ordinalPages(), state2.StateRoot)
		ok, e := tree.get(ordinalOutpointKey(next.TxID, 0), &output)
		if e != nil || !ok || len(output.Inscriptions) != len(rows)+1 {
			t.Fatal(output, e)
		}
	}
}
