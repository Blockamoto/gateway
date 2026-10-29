package main

import (
	"context"
	"errors"
	"testing"
)

func TestOrdProvenancePlacementHistoricalFlagsAndValues079(t *testing.T) {
	tx := transactionView{Inputs: []inputView{{N: 0}, {N: 1}}, Outputs: []outputView{{N: 0, ValueSats: 200}}}
	values := func(i int) (uint64, error) { return []uint64{40, 60}[i], nil }
	for _, env := range []ordEnvelope{
		{Input: 1, Duplicate: true}, {Input: 1, Incomplete: true}, {Input: 1, Pushnum: true}, {Input: 1, Stutter: true}, {Input: 1, Offset: 1},
	} {
		got, err := deriveOrdProvenancePlacement(context.Background(), tx, env, values)
		if err != nil || got.State != "verified_reveal_output" || got.Offset != 40 {
			t.Fatalf("historical flags: %+v %v", got, err)
		}
		original, err := deriveInscriptionInitialPlacement(context.Background(), tx, firstMainnetInscriptionHeight, env, values)
		if err != nil || original.State != "historical_context_required" {
			t.Fatalf("viewer policy changed: %+v %v", original, err)
		}
	}
	pointer := uint64(9)
	got, err := deriveOrdProvenancePlacement(context.Background(), tx, ordEnvelope{Input: 1, Pointer: &pointer, Duplicate: true}, values)
	if err != nil || got.State != "verified_reveal_output" || got.Offset != 9 {
		t.Fatalf("pointer: %+v %v", got, err)
	}
	for _, test := range []struct {
		env ordEnvelope
		val func(int) (uint64, error)
	}{{ordEnvelope{Input: 1, Unbound: true}, values}, {ordEnvelope{Input: 1}, func(int) (uint64, error) { return 0, nil }}} {
		got, err = deriveOrdProvenancePlacement(context.Background(), tx, test.env, test.val)
		if err != nil || got.State != "unbound" {
			t.Fatalf("unbound incorrectly established: %+v %v", got, err)
		}
	}
	_, err = deriveOrdProvenancePlacement(context.Background(), tx, ordEnvelope{Input: 1, Duplicate: true}, func(int) (uint64, error) { return 0, errors.New("missing value") })
	if err == nil {
		t.Fatal("missing value was guessed")
	}
}
