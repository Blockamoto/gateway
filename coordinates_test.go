package main

import "testing"

func TestInscriptionCoordinate(t *testing.T) {
	c, ok := parseBODCoordinate("0.i0.0")
	if !ok || c.Kind != coordInscription || c.TxIndex != 0 || c.InscriptionIndex != 0 || c.Height != 0 {
		t.Fatalf("unexpected inscription coordinate: %#v, ok=%v", c, ok)
	}
	if _, ok := parseBODCoordinate("0.i.bad"); ok {
		t.Fatal("accepted malformed inscription coordinate")
	}
}
