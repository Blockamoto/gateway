package main

import (
	"math/rand"
	"strings"
	"testing"
)

func Test077SatNameDecoding(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    uint64
	}{
		{"nvtdijuwxlp", 0},
		{"a", 2099999997689999},
		{"satoshi", 2099994106992659},
	} {
		if n, ok := satNumberForName077(tc.name); !ok || n != tc.n {
			t.Fatalf("%q: got %d %t, want %d", tc.name, n, ok, tc.n)
		}
		if n, ok := parseSatAddress077(tc.name + ".sat"); !ok || n != tc.n {
			t.Fatalf("explicit %q: got %d %t", tc.name, n, ok)
		}
	}
	// Independently known boundary vectors above establish numbering; these
	// round trips exercise carry boundaries and broad portions of the supply.
	rng := rand.New(rand.NewSource(77))
	for i := 0; i < 1000; i++ {
		n := uint64(rng.Int63n(int64(theoreticalSatSupply())))
		origin, ok := issuanceForSat(n)
		if !ok {
			t.Fatal(n)
		}
		name := propertiesForSat077(n, origin).Name
		if decoded, ok := satNumberForName077(name); !ok || decoded != n {
			t.Fatalf("round trip %d through %q: %d %t", n, name, decoded, ok)
		}
	}
}

func Test077SatNameRejectsMalformedAndOutOfRange(t *testing.T) {
	for _, name := range []string{"", "NV T", "Satoshi", "sat0shi", " satoshi", "satoshi ", "sátoshi", "a.b", "a/b", "a\x00", "nvtdijuwxlq", strings.Repeat("z", 11), strings.Repeat("a", 12), strings.Repeat("z", 10000)} {
		if n, ok := satNumberForName077(name); ok {
			t.Errorf("accepted %q as %d", name, n)
		}
	}
	for _, address := range []string{"satoshi", ".sat", "01.sat", "+1.sat", "1.2.sat", "satoshi.sat.example", "satoshi/1.sat", "nvtdijuwxlq.sat", "18446744073709551616.sat"} {
		if n, ok := parseSatAddress077(address); ok {
			t.Errorf("accepted malformed address %q as %d", address, n)
		}
	}
	for _, address := range []string{"0.sat", " 0.sat "} {
		if n, ok := parseSatAddress077(address); !ok || n != 0 {
			t.Errorf("lost numeric address %q", address)
		}
	}
}
