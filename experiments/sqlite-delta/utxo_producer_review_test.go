package delta

import (
	"path/filepath"
	"strings"
	"testing"

	engine "gateway-common-engine-experiment"
	whole "gateway-sqlite-backend-experiment"
)

func TestUTXOSnapshotDetachesCallerSlices(t *testing.T) {
	block := engine.Block{Height: 1, Hash: "h", ParentHash: "g", Transactions: []engine.Transaction{{
		ID: "t", Inputs: []engine.Outpoint{{TxID: "before"}}, Outputs: []engine.Coin{{Value: 4, Script: "before"}},
	}}}
	snapshot := snapshotUTXOBlock(block)
	block.Transactions[0].ID = "changed"
	block.Transactions[0].Inputs[0].TxID = "changed"
	block.Transactions[0].Outputs[0].Script = "changed"
	if snapshot.Transactions[0].ID != "t" || snapshot.Transactions[0].Inputs[0].TxID != "before" || snapshot.Transactions[0].Outputs[0].Script != "before" {
		t.Fatal("captured transition aliases caller-owned transaction slices")
	}
	snapshot.Transactions[0].Outputs[0].Value = 99
	if block.Transactions[0].Outputs[0].Value != 4 {
		t.Fatal("snapshot mutation changed caller input")
	}
}

func TestUTXOProducerLateInvalidTransitionsMatchReferenceAtomically(t *testing.T) {
	dir := t.TempDir()
	s := open(t, filepath.Join(dir, "delta.db"))
	defer s.Close()
	w, err := whole.Open(filepath.Join(dir, "whole.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := s.SetConsent("coins", true); err != nil {
		t.Fatal(err)
	}
	if err := w.SetConsent("coins", true); err != nil {
		t.Fatal(err)
	}
	p, e := producer(t, s), reference(t, w)
	bothAnchors(t, s, w, 0, "g")
	genesis := engine.Block{Height: 0, Hash: "g", Transactions: []engine.Transaction{{ID: "reward", Coinbase: true, Outputs: []engine.Coin{{Value: 50}, {Value: 30}}}}}
	applyBoth(t, p, e, genesis, WriteStats{Upserts: 2, Receipts: 1})
	bothAnchors(t, s, w, 1, "h")
	before, err := p.View()
	if err != nil {
		t.Fatal(err)
	}
	first := engine.Transaction{ID: "first", Inputs: []engine.Outpoint{{TxID: "reward"}}, Outputs: []engine.Coin{{Value: 49}}}
	cases := []struct {
		name, reason string
		txs          []engine.Transaction
	}{
		{"duplicate input across transactions", "duplicate input", []engine.Transaction{first, {ID: "other", Inputs: []engine.Outpoint{{TxID: "reward"}}}}},
		{"duplicate transaction identity", "duplicate transaction", []engine.Transaction{first, {ID: "first", Inputs: []engine.Outpoint{{TxID: "reward", Index: 1}}}}},
		{"live collision after a valid spend", "collision", []engine.Transaction{first, {ID: "reward", Inputs: []engine.Outpoint{{TxID: "first"}}, Outputs: []engine.Coin{{Value: 1}, {Value: 2}}}}},
		{"forward reference", "unresolved", []engine.Transaction{{ID: "first", Inputs: []engine.Outpoint{{TxID: "future"}}}, {ID: "future", Inputs: []engine.Outpoint{{TxID: "reward"}}, Outputs: []engine.Coin{{Value: 1}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			block := engine.Block{Height: 1, Hash: "h", ParentHash: "g", Transactions: tc.txs}
			if _, stats, err := p.Apply(block); err == nil || !strings.Contains(err.Error(), tc.reason) || stats != (WriteStats{}) {
				t.Fatalf("direct producer accepted or wrote invalid transition: %+v %v", stats, err)
			}
			if _, err := e.Apply(block); err == nil {
				t.Fatal("reference accepted invalid transition")
			}
			view, err := p.View()
			if err != nil || !sameView(before, view) {
				t.Fatalf("invalid transition published state: %v", err)
			}
			compareProducer(t, p, e)
		})
	}
	// A later transaction may spend an output made earlier in this block.
	valid := engine.Block{Height: 1, Hash: "h", ParentHash: "g", Transactions: []engine.Transaction{first, {ID: "later", Inputs: []engine.Outpoint{{TxID: "first"}}, Outputs: []engine.Coin{{Value: 48}}}}}
	applyBoth(t, p, e, valid, WriteStats{Upserts: 1, Deletes: 1, Receipts: 1})
}

func TestUTXOProducerConsentAndRevisionFences(t *testing.T) {
	dir := t.TempDir()
	s := open(t, filepath.Join(dir, "delta.db"))
	defer s.Close()
	w, err := whole.Open(filepath.Join(dir, "whole.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := s.SetConsent("coins", true); err != nil {
		t.Fatal(err)
	}
	if err := w.SetConsent("coins", true); err != nil {
		t.Fatal(err)
	}
	p, e := producer(t, s), reference(t, w)
	bothAnchors(t, s, w, 0, "g")
	applyBoth(t, p, e, engine.Block{Height: 0, Hash: "g", Transactions: []engine.Transaction{{ID: "reward", Coinbase: true, Outputs: []engine.Coin{{Value: 50}}}}}, WriteStats{Upserts: 1, Receipts: 1})
	bothAnchors(t, s, w, 1, "h")
	block := engine.Block{Height: 1, Hash: "h", ParentHash: "g", Transactions: []engine.Transaction{{ID: "spend", Inputs: []engine.Outpoint{{TxID: "reward"}}, Outputs: []engine.Coin{{Value: 49}}}}}
	before, err := p.View()
	if err != nil {
		t.Fatal(err)
	}
	s.BeforeNextCommit(func() {
		if err := s.SetConsent("coins", false); err != nil {
			t.Fatal(err)
		}
	})
	if _, stats, err := p.Apply(block); err == nil || !strings.Contains(err.Error(), "consent") || stats != (WriteStats{}) {
		t.Fatalf("stale consent accepted: %+v %v", stats, err)
	}
	after, err := p.View()
	if err != nil || !sameView(before, after) {
		t.Fatalf("stale consent changed state: %v", err)
	}
	if err := s.SetConsent("coins", true); err != nil {
		t.Fatal(err)
	}
	other := producer(t, s)
	s.BeforeNextCommit(func() {
		if _, _, err := other.Apply(block); err != nil {
			t.Fatalf("competing commit: %v", err)
		}
	})
	if _, stats, err := p.Apply(block); err == nil || !strings.Contains(err.Error(), "revision") || stats != (WriteStats{}) {
		t.Fatalf("stale revision accepted: %+v %v", stats, err)
	}
	if _, err := e.Apply(block); err != nil {
		t.Fatal(err)
	}
	compareProducer(t, p, e)
}
