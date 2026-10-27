package delta

import (
	"path/filepath"
	"strings"
	"testing"

	engine "gateway-common-engine-experiment"
	whole "gateway-sqlite-backend-experiment"
)

const utxoRecipe = `{"version":1,"id":"coins","state":"utxo","operations":[{"op":"consume-inputs"},{"op":"create-outputs"},{"op":"emit","fields":["tx.id","tx.position"]}]}`

func producer(t *testing.T, s *Store) *UTXOProducer {
	t.Helper()
	p, err := NewUTXOProducer(s, "coins", makeRecipe(t, utxoRecipe))
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func reference(t *testing.T, s *whole.Backend) *engine.Engine {
	t.Helper()
	e, err := engine.NewWithBackend(makeRecipe(t, utxoRecipe), s, "coins")
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func compareProducer(t *testing.T, p *UTXOProducer, e *engine.Engine) {
	t.Helper()
	a, err := p.View()
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.View()
	if err != nil {
		t.Fatal(err)
	}
	if !sameView(a, b) {
		t.Fatalf("direct and whole-state views differ: %+v %+v", a, b)
	}
}
func bothAnchors(t *testing.T, s *Store, w *whole.Backend, h uint64, hash string) {
	t.Helper()
	if err := s.SetAnchor(h, hash); err != nil {
		t.Fatal(err)
	}
	if err := w.SetAnchor(h, hash); err != nil {
		t.Fatal(err)
	}
}
func applyBoth(t *testing.T, p *UTXOProducer, e *engine.Engine, b engine.Block, want WriteStats) {
	t.Helper()
	a, stats, err := p.Apply(b)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := e.Apply(b)
	if err != nil {
		t.Fatal(err)
	}
	if stats != want || !sameReceipt(a, reference) {
		t.Fatalf("different outcome: stats=%+v wanted=%+v direct=%+v reference=%+v", stats, want, a, reference)
	}
	compareProducer(t, p, e)
}

func TestUTXOProducerAgainstWholeStateAndRestart(t *testing.T) {
	dir := t.TempDir()
	dp := filepath.Join(dir, "delta.db")
	wp := filepath.Join(dir, "whole.db")
	s := open(t, dp)
	w, err := whole.Open(wp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			_ = s.Close()
		}
		if w != nil {
			_ = w.Close()
		}
	}()
	if err = s.SetConsent("coins", true); err != nil {
		t.Fatal(err)
	}
	if err = w.SetConsent("coins", true); err != nil {
		t.Fatal(err)
	}
	p := producer(t, s)
	e := reference(t, w)
	bothAnchors(t, s, w, 0, "g")
	genesis := engine.Block{Height: 0, Hash: "g", Transactions: []engine.Transaction{{ID: "reward", Coinbase: true, Outputs: []engine.Coin{{Value: 50, Script: "a"}, {Value: 30, Script: "b"}}}}}
	applyBoth(t, p, e, genesis, WriteStats{Upserts: 2, Receipts: 1})
	bothAnchors(t, s, w, 1, "h")
	block := engine.Block{Height: 1, Hash: "h", ParentHash: "g", Transactions: []engine.Transaction{
		{ID: "reward2", Coinbase: true, Outputs: []engine.Coin{{Value: 25}}},
		{ID: "first", Inputs: []engine.Outpoint{{TxID: "reward"}}, Outputs: []engine.Coin{{Value: 10}, {Value: 39}}},
		{ID: "second", Inputs: []engine.Outpoint{{TxID: "first"}}, Outputs: []engine.Coin{{Value: 9}}},
	}}
	// first:0 is created and spent within the block, so it causes no persisted
	// mutation; the unrelated reward:1 coin is not rewritten.
	applyBoth(t, p, e, block, WriteStats{Upserts: 3, Deletes: 1, Receipts: 1})
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = nil
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	w = nil
	s = open(t, dp)
	w, err = whole.Open(wp)
	if err != nil {
		t.Fatal(err)
	}
	p = producer(t, s)
	e = reference(t, w)
	compareProducer(t, p, e)
	bothAnchors(t, s, w, 2, "j")
	// Synthetic no-output coinbase isolates a zero-coin-mutation block. This is
	// not a claim that the fixture is a consensus-valid Bitcoin block.
	noop := engine.Block{Height: 2, Hash: "j", ParentHash: "h", Transactions: []engine.Transaction{{ID: "empty", Coinbase: true}}}
	applyBoth(t, p, e, noop, WriteStats{Receipts: 1})
	bothAnchors(t, s, w, 3, "k")
	// Synthetic same-ID recreation tests a changed key after its old value is
	// spent. It is not an implementation of Bitcoin's BIP30 historical rules.
	change := engine.Block{Height: 3, Hash: "k", ParentHash: "j", Transactions: []engine.Transaction{{ID: "reward", Inputs: []engine.Outpoint{{TxID: "reward", Index: 1}}, Outputs: []engine.Coin{{Value: 1}, {Value: 20}}}}}
	before, err := p.View()
	if err != nil {
		t.Fatal(err)
	}
	s.FailNextCommit()
	if _, _, err = p.Apply(change); err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("injected failure not surfaced: %v", err)
	}
	after, err := p.View()
	if err != nil || !sameView(before, after) {
		t.Fatalf("failure published state: %v", err)
	}
	applyBoth(t, p, e, change, WriteStats{Upserts: 2, Receipts: 1})
	if _, _, err = p.Apply(change); err == nil || !strings.Contains(err.Error(), "already committed") {
		t.Fatalf("committed retry accepted: %v", err)
	}
}

func TestUTXOProducerFencesAndMissingEvidence(t *testing.T) {
	dir := t.TempDir()
	s := open(t, filepath.Join(dir, "delta.db"))
	defer s.Close()
	w, err := whole.Open(filepath.Join(dir, "whole.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err = s.SetConsent("coins", true); err != nil {
		t.Fatal(err)
	}
	if err = w.SetConsent("coins", true); err != nil {
		t.Fatal(err)
	}
	p := producer(t, s)
	e := reference(t, w)
	bothAnchors(t, s, w, 0, "g")
	applyBoth(t, p, e, engine.Block{Height: 0, Hash: "g", Transactions: []engine.Transaction{{ID: "reward", Coinbase: true, Outputs: []engine.Coin{{Value: 50}}}}}, WriteStats{Upserts: 1, Receipts: 1})
	bothAnchors(t, s, w, 1, "h")
	before, err := p.View()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		tx      engine.Transaction
		message string
	}{
		{"missing", engine.Transaction{ID: "bad", Inputs: []engine.Outpoint{{TxID: "missing"}}}, "unresolved"},
		{"duplicate", engine.Transaction{ID: "bad", Inputs: []engine.Outpoint{{TxID: "reward"}, {TxID: "reward"}}}, "duplicate input"},
		{"collision", engine.Transaction{ID: "reward", Coinbase: true, Outputs: []engine.Coin{{Value: 3}}}, "collision"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			block := engine.Block{Height: 1, Hash: "h", ParentHash: "g", Transactions: []engine.Transaction{tc.tx}}
			if _, _, err := p.Apply(block); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("invalid transition accepted: %v", err)
			}
			if _, err := e.Apply(block); err == nil {
				t.Fatal("reference accepted invalid transition")
			}
			v, err := p.View()
			if err != nil || !sameView(before, v) {
				t.Fatal("failed transition published data")
			}
			compareProducer(t, p, e)
		})
	}
	valid := engine.Block{Height: 1, Hash: "h", ParentHash: "g", Transactions: []engine.Transaction{{ID: "spend", Inputs: []engine.Outpoint{{TxID: "reward"}}, Outputs: []engine.Coin{{Value: 49}}}}}
	s.BeforeNextCommit(func() {
		if err := s.SetAnchor(1, "reorg"); err != nil {
			t.Fatal(err)
		}
	})
	if _, _, err = p.Apply(valid); err == nil || !strings.Contains(err.Error(), "chain") {
		t.Fatalf("stale chain publication accepted: %v", err)
	}
	v, err := p.View()
	if err != nil || !sameView(before, v) {
		t.Fatal("stale publication changed state")
	}
	if err = s.SetAnchor(1, "h"); err != nil {
		t.Fatal(err)
	}
	applyBoth(t, p, e, valid, WriteStats{Upserts: 1, Deletes: 1, Receipts: 1})
}

func TestUTXOProducerRejectsNonUTXORecipe(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "delta.db"))
	defer s.Close()
	recipe := makeRecipe(t, `{"version":1,"id":"sparse","state":"none","operations":[{"op":"emit","fields":["tx.id"]}]}`)
	if _, err := NewUTXOProducer(s, "coins", recipe); err == nil {
		t.Fatal("non-UTXO recipe accepted")
	}
}
