package state

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func program(t *testing.T) Program {
	t.Helper()
	f, err := os.Open("utxo-program.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, err := ReadProgram(f)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func newMachine(t *testing.T, depth int) *Machine {
	t.Helper()
	m, err := New(program(t), depth)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func genesis() Block {
	return Block{Height: 0, Hash: "g", Transactions: []Transaction{{ID: "genesis", Coinbase: true, Outputs: []Coin{{Value: 50, Script: "script-a"}}}}}
}
func mustApply(t *testing.T, m *Machine, b Block) {
	t.Helper()
	if err := m.Apply(b); err != nil {
		t.Fatal(err)
	}
}
func assertUnchanged(t *testing.T, m *Machine, head *Head, coins map[Outpoint]Coin) {
	t.Helper()
	a, b := m.View()
	if !reflect.DeepEqual(head, a) || !reflect.DeepEqual(coins, b) {
		t.Fatalf("failed transition changed state: %+v %+v", a, b)
	}
}

func TestAtomicBlockWithIntraBlockSpends(t *testing.T) {
	m := newMachine(t, 4)
	mustApply(t, m, genesis())
	b := Block{Height: 1, Hash: "a", ParentHash: "g", Transactions: []Transaction{
		{ID: "reward", Coinbase: true, Outputs: []Coin{{Value: 25, Script: "miner"}}},
		{ID: "first", Inputs: []Outpoint{{TxID: "genesis"}}, Outputs: []Coin{{Value: 30, Script: "one"}, {Value: 19, Script: "two"}}},
		{ID: "second", Inputs: []Outpoint{{TxID: "first", Index: 1}}, Outputs: []Coin{{Value: 18, Script: "three"}}},
	}}
	mustApply(t, m, b)
	head, coins := m.View()
	want := map[Outpoint]Coin{{TxID: "reward"}: {Value: 25, Script: "miner"}, {TxID: "first"}: {Value: 30, Script: "one"}, {TxID: "second"}: {Value: 18, Script: "three"}}
	if *head != (Head{Height: 1, Hash: "a"}) || !reflect.DeepEqual(coins, want) {
		t.Fatalf("unexpected state: %+v %+v", head, coins)
	}
	delete(coins, Outpoint{TxID: "first"})
	head.Hash = "tampered"
	h2, c2 := m.View()
	if h2.Hash != "a" || len(c2) != 3 {
		t.Fatal("view exposed mutable state")
	}
}

func TestInvalidTransitionsAreAllOrNothing(t *testing.T) {
	m := newMachine(t, 4)
	mustApply(t, m, genesis())
	head, coins := m.View()
	validInput := Outpoint{TxID: "genesis"}
	cases := []struct {
		name   string
		b      Block
		reason string
	}{
		{"wrong height", Block{Height: 2, Hash: "bad", ParentHash: "g"}, "successor"},
		{"wrong parent", Block{Height: 1, Hash: "bad", ParentHash: "other"}, "successor"},
		{"unresolved after mutation", Block{Height: 1, Hash: "bad", ParentHash: "g", Transactions: []Transaction{{ID: "spend", Inputs: []Outpoint{validInput, {TxID: "missing"}}}}}, "unresolved"},
		{"duplicate input", Block{Height: 1, Hash: "bad", ParentHash: "g", Transactions: []Transaction{{ID: "spend", Inputs: []Outpoint{validInput, validInput}}}}, "duplicate input"},
		{"duplicate across transactions", Block{Height: 1, Hash: "bad", ParentHash: "g", Transactions: []Transaction{{ID: "one", Inputs: []Outpoint{validInput}}, {ID: "two", Inputs: []Outpoint{validInput}}}}, "duplicate input"},
		{"live collision", Block{Height: 1, Hash: "bad", ParentHash: "g", Transactions: []Transaction{{ID: "genesis", Coinbase: true, Outputs: []Coin{{Value: 1}}}}}, "collision"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := m.Apply(tc.b); err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("expected %s, got %v", tc.reason, err)
			}
			assertUnchanged(t, m, head, coins)
		})
	}
}

func TestRollbackReplacementAndBoundedRecovery(t *testing.T) {
	m := newMachine(t, 2)
	mustApply(t, m, genesis())
	mustApply(t, m, Block{Height: 1, Hash: "a", ParentHash: "g", Transactions: []Transaction{{ID: "a-tx", Inputs: []Outpoint{{TxID: "genesis"}}, Outputs: []Coin{{Value: 40}}}}})
	mustApply(t, m, Block{Height: 2, Hash: "b", ParentHash: "a", Transactions: []Transaction{{ID: "b-tx", Inputs: []Outpoint{{TxID: "a-tx"}}, Outputs: []Coin{{Value: 30}}}}})
	head, coins := m.View()
	if err := m.RollbackOne("wrong"); err == nil {
		t.Fatal("accepted stale rollback")
	}
	assertUnchanged(t, m, head, coins)
	if err := m.RollbackOne("b"); err != nil {
		t.Fatal(err)
	}
	mustApply(t, m, Block{Height: 2, Hash: "c", ParentHash: "a", Transactions: []Transaction{{ID: "c-tx", Inputs: []Outpoint{{TxID: "a-tx"}}, Outputs: []Coin{{Value: 35}}}}})
	h, c := m.View()
	if h.Hash != "c" || c[Outpoint{TxID: "c-tx"}].Value != 35 {
		t.Fatalf("replacement state wrong: %+v %+v", h, c)
	}
	if err := m.RollbackOne("c"); err != nil {
		t.Fatal(err)
	}
	if err := m.RollbackOne("a"); err != nil {
		t.Fatal(err)
	}
	head, coins = m.View()
	if head.Hash != "g" || coins[Outpoint{TxID: "genesis"}].Value != 50 {
		t.Fatal("incorrect restored genesis state")
	}
	if err := m.RollbackOne("g"); err == nil || !strings.Contains(err.Error(), "rebuild") {
		t.Fatalf("expected bounded undo failure: %v", err)
	}
	assertUnchanged(t, m, head, coins)
}

func TestProgramValidationAndIsolation(t *testing.T) {
	for _, s := range []string{
		`{"version":1,"id":"x","scope":"transactions-in-block-order","ops":["create_outputs","consume_inputs"]}`,
		`{"version":1,"id":"x","scope":"transactions-in-block-order","ops":["consume_inputs","create_outputs"],"unknown":true}`,
		`{"version":1,"id":"x","scope":"transactions-in-block-order","ops":["consume_inputs","create_outputs"]} {}`,
	} {
		if _, err := ReadProgram(strings.NewReader(s)); err == nil {
			t.Fatalf("invalid program accepted: %s", s)
		}
	}
	p := program(t)
	m, err := New(p, 1)
	if err != nil {
		t.Fatal(err)
	}
	p.Ops[1] = "unknown"
	mustApply(t, m, genesis())
	_, coins := m.View()
	if coins[Outpoint{TxID: "genesis"}].Value != 50 {
		t.Fatal("external program mutation changed execution")
	}
}
