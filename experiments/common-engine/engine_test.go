package engine

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func recipe(t *testing.T, name string) Recipe {
	t.Helper()
	f, err := os.Open("recipes/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, err := ReadRecipe(f)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func host(t *testing.T, name string) *Engine {
	t.Helper()
	e, err := New(recipe(t, name))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func mustView(t *testing.T, e *Engine) View {
	t.Helper()
	v, err := e.View()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSharedOperationInterpreterRunsBothBuiltins(t *testing.T) {
	block := Block{Height: 0, Hash: "fixture-g", Transactions: []Transaction{
		{ID: "reward", Coinbase: true, Outputs: []Coin{{Value: 50, Script: "a"}}},
		{ID: "spend", Inputs: []Outpoint{{TxID: "reward"}}, Outputs: []Coin{{Value: 49, Script: "b"}}},
	}}
	sparse := host(t, "transaction-locations")
	state := host(t, "utxo-fixture")
	a, err := sparse.Apply(block)
	if err != nil {
		t.Fatal(err)
	}
	b, err := state.Apply(block)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Rows, b.Rows) || len(a.Rows) != 2 || a.Rows[1].Cells[0].Value != "spend" || a.Rows[1].Cells[1].Value != "1.0" {
		t.Fatalf("shared emit differs: %+v %+v", a, b)
	}
	view := mustView(t, state)
	if view.Head.Hash != "fixture-g" || len(view.Coins) != 1 || view.Coins[Outpoint{TxID: "spend"}].Value != 49 || len(view.Receipts) != 1 {
		t.Fatalf("wrong state: %+v", view)
	}
	if len(mustView(t, sparse).Coins) != 0 || mustView(t, sparse).Head != nil {
		t.Fatal("sparse recipe unexpectedly changed state")
	}
}

func TestFailedStateTransitionPublishesNeitherStateNorCoverage(t *testing.T) {
	e := host(t, "utxo-fixture")
	if _, err := e.Apply(Block{Height: 0, Hash: "g", Transactions: []Transaction{{ID: "reward", Coinbase: true, Outputs: []Coin{{Value: 50}}}}}); err != nil {
		t.Fatal(err)
	}
	before := mustView(t, e)
	bad := Block{Height: 1, Hash: "one", ParentHash: "g", Transactions: []Transaction{
		{ID: "valid", Inputs: []Outpoint{{TxID: "reward"}}, Outputs: []Coin{{Value: 49}}},
		{ID: "unresolved", Inputs: []Outpoint{{TxID: "missing"}}},
	}}
	if _, err := e.Apply(bad); err == nil || !strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("missing input accepted: %v", err)
	}
	if !reflect.DeepEqual(before, mustView(t, e)) {
		t.Fatal("failed block published partial state or receipt")
	}
	bad.Transactions[1].Inputs = []Outpoint{{TxID: "valid"}}
	if _, err := e.Apply(bad); err != nil {
		t.Fatalf("intra-block spend: %v", err)
	}
	after := mustView(t, e)
	if after.Head.Hash != "one" || len(after.Receipts) != 2 {
		t.Fatalf("success not committed: %+v", after)
	}
}

func TestSparseCoverageAndStateAnchorValidation(t *testing.T) {
	s := host(t, "transaction-locations")
	for _, h := range []uint64{10, 12} {
		if _, err := s.Apply(Block{Height: h, Hash: "fixture", Transactions: []Transaction{{ID: "t"}}}); err != nil {
			t.Fatal(err)
		}
	}
	view := mustView(t, s)
	if len(view.Receipts) != 2 {
		t.Fatal("wrong receipts")
	}
	if _, ok := view.Receipts[11]; ok {
		t.Fatal("gap reported as covered")
	}
	if _, err := s.Apply(Block{Height: 10, Hash: "replacement"}); err == nil {
		t.Fatal("anchor conflict accepted")
	}
	st := host(t, "utxo-fixture")
	if _, err := st.Apply(Block{Height: 1, Hash: "x", ParentHash: "g"}); err == nil {
		t.Fatal("arbitrary state start accepted")
	}
	if len(mustView(t, st).Receipts) != 0 {
		t.Fatal("invalid block covered")
	}
}

func TestEmptyBlockCannotCreateCoverage(t *testing.T) {
	for _, name := range []string{"transaction-locations", "utxo-fixture"} {
		t.Run(name, func(t *testing.T) {
			e := host(t, name)
			before := mustView(t, e)
			if _, err := e.Apply(Block{Height: 0, Hash: "empty"}); err == nil {
				t.Fatal("empty block accepted as complete coverage")
			}
			if !reflect.DeepEqual(before, mustView(t, e)) {
				t.Fatal("empty block changed state or coverage")
			}
		})
	}
}

func TestDuplicateInputAndWrongParentDoNotCommit(t *testing.T) {
	e := host(t, "utxo-fixture")
	if _, err := e.Apply(Block{Height: 0, Hash: "g", Transactions: []Transaction{{ID: "reward", Coinbase: true, Outputs: []Coin{{Value: 50}}}}}); err != nil {
		t.Fatal(err)
	}
	before := mustView(t, e)
	bad := Block{Height: 1, Hash: "one", ParentHash: "g", Transactions: []Transaction{{ID: "spend", Inputs: []Outpoint{{TxID: "reward"}, {TxID: "reward"}}}}}
	if _, err := e.Apply(bad); err == nil || !strings.Contains(err.Error(), "duplicate input") {
		t.Fatalf("duplicate input accepted: %v", err)
	}
	if !reflect.DeepEqual(before, mustView(t, e)) {
		t.Fatal("duplicate input changed committed state")
	}
	bad.ParentHash = "wrong"
	if _, err := e.Apply(bad); err == nil || !strings.Contains(err.Error(), "successor") {
		t.Fatalf("wrong parent accepted: %v", err)
	}
	if !reflect.DeepEqual(before, mustView(t, e)) {
		t.Fatal("wrong parent changed committed state")
	}
}

func TestDefinitionAndReturnedViewsAreDetached(t *testing.T) {
	p := recipe(t, "transaction-locations")
	e, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Operations[0].Fields[0] = "changed"
	r, err := e.Apply(Block{Height: 7, Hash: "seven", Transactions: []Transaction{{ID: "t"}}})
	if err != nil {
		t.Fatal(err)
	}
	r.Rows[0].Cells[0].Value = "changed"
	v := mustView(t, e)
	v.Receipts[7].Rows[0].Cells[0].Value = "changed-again"
	if mustView(t, e).Receipts[7].Rows[0].Cells[0].Value != "t" {
		t.Fatal("caller mutated committed record")
	}
	for _, input := range []string{
		`{"version":1,"id":"x","state":"none","operations":[{"op":"emit","fields":["unknown"]}]}`,
		`{"version":1,"id":"x","state":"utxo","operations":[{"op":"create-outputs"},{"op":"consume-inputs"},{"op":"emit","fields":["tx.id"]}]}`,
		`{"version":1,"id":"x","state":"none","operations":[{"op":"emit","fields":["tx.id"]}],"unexpected":true}`,
		`{"version":1,"id":"x","state":"none","operations":[{"op":"emit","fields":["tx.id"]}]} {}`,
	} {
		if _, err := ReadRecipe(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted invalid recipe: %s", input)
		}
	}
}
