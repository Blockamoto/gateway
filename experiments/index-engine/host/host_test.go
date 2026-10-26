package host

import (
	"errors"
	"strings"
	"testing"

	engine "gateway-index-experiment"
	state "gateway-state-experiment"
)

const sparseRecipe = `{"version":1,"kind":"sparse","recipe":{"version":1,"id":"spends","source":"inputs","columns":[{"name":"previous","type":"text","field":"input.previous_txid"},{"name":"position","type":"text","lookup":{"table":"tx-position","key":"input.previous_txid"}}]}}`
const stateRecipe = `{"version":1,"kind":"state","recipe":{"version":1,"id":"coins","scope":"transactions-in-block-order","ops":["consume_inputs","create_outputs"]}}`

type emptyResolver struct{}

func (emptyResolver) ResolveTransaction(string) (engine.LookupResult, bool, error) {
	return engine.LookupResult{}, false, nil
}

type errorResolver struct{}

func (errorResolver) ResolveTransaction(string) (engine.LookupResult, bool, error) {
	return engine.LookupResult{}, false, errors.New("dependency failure")
}

func TestSharedHostRunsSparseAndStateRecipes(t *testing.T) {
	sparse, err := Load(strings.NewReader(sparseRecipe), 2)
	if err != nil {
		t.Fatal(err)
	}
	sparseBlock := &engine.Block{Height: 4, Hash: "fixture-4", Transactions: []engine.Transaction{{ID: "spend", Inputs: []engine.Input{{PreviousTxID: "unknown", PreviousOutput: 2}}}}}
	out, err := sparse.Execute(Request{SparseBlock: sparseBlock, Resolver: emptyResolver{}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != "sparse" || out.State != nil || out.Sparse.Unresolved != 1 || out.Sparse.Rows[0].Cells[1].State != "unresolved" {
		t.Fatalf("unresolved lookup lost: %+v", out)
	}
	if _, err := sparse.Execute(Request{SparseBlock: sparseBlock, Resolver: errorResolver{}}); err == nil {
		t.Fatal("dependency error accepted")
	}
	if _, err := sparse.Execute(Request{SparseBlock: sparseBlock}); err == nil {
		t.Fatal("absent resolver accepted")
	}

	ordered, err := Load(strings.NewReader(stateRecipe), 2)
	if err != nil {
		t.Fatal(err)
	}
	genesis := &state.Block{Height: 0, Hash: "fixture-g", Transactions: []state.Transaction{{ID: "coinbase", Coinbase: true, Outputs: []state.Coin{{Value: 50}}}}}
	out, err = ordered.Execute(Request{StateBlock: genesis})
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != "state" || out.Sparse != nil || out.State.Head.Hash != "fixture-g" || out.State.Coins[state.Outpoint{TxID: "coinbase"}].Value != 50 {
		t.Fatalf("wrong state result: %+v", out)
	}
	bad := &state.Block{Height: 1, Hash: "fixture-bad", ParentHash: "fixture-g", Transactions: []state.Transaction{{ID: "spend", Inputs: []state.Outpoint{{TxID: "missing"}}}}}
	if _, err := ordered.Execute(Request{StateBlock: bad}); err == nil {
		t.Fatal("unresolved input accepted")
	}
	next := &state.Block{Height: 1, Hash: "fixture-1", ParentHash: "fixture-g", Transactions: []state.Transaction{{ID: "spend", Inputs: []state.Outpoint{{TxID: "coinbase"}}, Outputs: []state.Coin{{Value: 49}}}}}
	out, err = ordered.Execute(Request{StateBlock: next})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Head.Hash != "fixture-1" || out.State.Coins[state.Outpoint{TxID: "spend"}].Value != 49 {
		t.Fatalf("wrong transition: %+v", out.State)
	}
	if err := ordered.Rollback("fixture-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := ordered.Execute(Request{StateBlock: next}); err != nil {
		t.Fatalf("rollback failed to restore predecessor: %v", err)
	}
}

func TestHostRejectsInvalidDefinitionAndWrongInputKind(t *testing.T) {
	for _, definition := range []string{
		`{"version":1,"kind":"unknown","recipe":{}}`,
		`{"version":1,"kind":"sparse","recipe":{"version":1,"id":"x","source":"transactions","columns":[]}}`,
		`{"version":1,"kind":"state","recipe":{"version":1,"id":"x","scope":"transactions-in-block-order","ops":["create_outputs","consume_inputs"]}}`,
		sparseRecipe + ` {}`,
	} {
		if _, err := Load(strings.NewReader(definition), 1); err == nil {
			t.Fatalf("accepted invalid envelope: %s", definition)
		}
	}
	h, err := Load(strings.NewReader(sparseRecipe), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Execute(Request{StateBlock: &state.Block{}}); err == nil {
		t.Fatal("wrong block kind accepted")
	}
	if err := h.Rollback("x"); err == nil {
		t.Fatal("sparse rollback accepted")
	}
}
