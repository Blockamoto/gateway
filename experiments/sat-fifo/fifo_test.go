package fifo

import (
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
)

func program(t *testing.T) Program {
	t.Helper()
	f, err := os.Open("program.json")
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
func input(id string, value uint64, ranges ...Range) Input {
	return Input{Outpoint: Outpoint{Transaction: id}, Evidence: &Evidence{Anchor: "fixture-anchor", Value: value, Ranges: ranges}}
}

func TestOrderedFragmentedInputsOutputSplitsAndFeeConserve(t *testing.T) {
	tx := Transaction{Inputs: []Input{input("a", 7, Range{Start: 100, End: 104}, Range{Start: 4, End: 7}), input("b", 6, Range{Start: 40, End: 46})}, OutputValues: []uint64{5, 0, 4}}
	before := append([]Range(nil), tx.Inputs[0].Evidence.Ranges...)
	out, err := Execute(program(t), tx)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]Range{{{Start: 100, End: 104}, {Start: 4, End: 5}}, {}, {{Start: 5, End: 7}, {Start: 40, End: 42}}}
	if !reflect.DeepEqual(out.Outputs, want) || !reflect.DeepEqual(out.Fee, []Range{{Start: 42, End: 46}}) || out.InputValue != 13 || out.FeeValue != 4 {
		t.Fatalf("unexpected FIFO result: %+v", out)
	}
	if !reflect.DeepEqual(tx.Inputs[0].Evidence.Ranges, before) {
		t.Fatal("input evidence mutated")
	}
	var produced uint64
	for _, ranges := range out.Outputs {
		n, err := rangeValue(ranges)
		if err != nil {
			t.Fatal(err)
		}
		produced += n
	}
	if produced+out.FeeValue != out.InputValue {
		t.Fatal("sats not conserved")
	}
}

func TestMissingOrInconsistentEvidenceFailsWithoutResult(t *testing.T) {
	good := input("a", 3, Range{Start: 10, End: 13})
	tests := []struct {
		name   string
		tx     Transaction
		reason string
	}{
		{"missing", Transaction{Inputs: []Input{{Outpoint: Outpoint{Transaction: "a"}}}}, "unresolved"},
		{"unanchored", Transaction{Inputs: []Input{{Outpoint: Outpoint{Transaction: "a"}, Evidence: &Evidence{Value: 1, Ranges: []Range{{Start: 1, End: 2}}}}}}, "unresolved"},
		{"value mismatch", Transaction{Inputs: []Input{input("a", 5, Range{Start: 10, End: 13})}}, "mismatch"},
		{"invalid range", Transaction{Inputs: []Input{input("a", 0, Range{Start: 2, End: 2})}}, "invalid"},
		{"duplicate input", Transaction{Inputs: []Input{good, good}}, "duplicate"},
		{"overlap", Transaction{Inputs: []Input{good, input("b", 2, Range{Start: 12, End: 14})}}, "overlapping"},
		{"overspend", Transaction{Inputs: []Input{good}, OutputValues: []uint64{2, 2}}, "exceed"},
		{"overflow", Transaction{Inputs: []Input{input("a", math.MaxUint64, Range{Start: 0, End: math.MaxUint64}), input("b", 1, Range{Start: 0, End: 1})}}, "overflow"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Execute(program(t), tc.tx)
			if err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("expected %q: %v", tc.reason, err)
			}
			if !reflect.DeepEqual(out, Result{}) {
				t.Fatalf("failure returned partial result: %+v", out)
			}
		})
	}
}

func TestZeroValueAndStrictProgram(t *testing.T) {
	out, err := Execute(program(t), Transaction{Inputs: []Input{input("empty", 0)}, OutputValues: []uint64{0}})
	if err != nil || out.InputValue != 0 || out.FeeValue != 0 || len(out.Outputs) != 1 || len(out.Outputs[0]) != 0 {
		t.Fatalf("zero-value allocation failed: %+v %v", out, err)
	}
	for _, s := range []string{
		`{"version":1,"id":"x","scope":"ordinary-transaction","ops":["allocate-outputs-fifo","concatenate-input-ranges","retain-fee-remainder"]}`,
		`{"version":1,"id":"x","scope":"ordinary-transaction","ops":["concatenate-input-ranges","allocate-outputs-fifo","retain-fee-remainder"],"unknown":true}`,
		`{"version":1,"id":"x","scope":"ordinary-transaction","ops":["concatenate-input-ranges","allocate-outputs-fifo","retain-fee-remainder"]} {}`,
	} {
		if _, err := ReadProgram(strings.NewReader(s)); err == nil {
			t.Fatalf("invalid program accepted: %s", s)
		}
	}
}
