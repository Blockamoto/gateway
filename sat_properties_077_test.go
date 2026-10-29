package main

import "testing"

func Test077SatIssuanceProperties(t *testing.T) {
	for _, tc := range []struct {
		number                                    uint64
		rarity, name, decimal, degree, percentile string
	}{
		{0, "mythic", "nvtdijuwxlp", "0.0", "0°0′0″0‴", "0%"},
		{2099999997689999, "uncommon", "a", "6929999.0", "5°209999′1007″0‴", "100%"},
		{2099994106992659, "common", "satoshi", "3891094.16797", "3°111094′214″16797‴", ""},
	} {
		origin, ok := issuanceForSat(tc.number)
		if !ok {
			t.Fatalf("issuance missing for %d", tc.number)
		}
		got := propertiesForSat077(tc.number, origin)
		if got.Name != tc.name || got.Decimal != tc.decimal || got.Degree != tc.degree || (tc.percentile != "" && got.Percentile != tc.percentile) {
			t.Errorf("%d: %+v", tc.number, got)
		}
		// The last issued sat is the first sat of its one-sat subsidy.
		if got.Rarity != tc.rarity {
			t.Errorf("%d rarity %s", tc.number, got.Rarity)
		}
	}
	for _, tc := range []struct {
		height int64
		rarity string
	}{{1, "uncommon"}, {2016, "rare"}, {210000, "epic"}, {1260000, "legendary"}} {
		origin, ok := issuanceForSat(firstSatAtHeight(tc.height))
		if !ok {
			t.Fatal(tc.height)
		}
		if got := propertiesForSat077(origin.FirstSat, origin); got.Rarity != tc.rarity {
			t.Errorf("height %d: %s", tc.height, got.Rarity)
		}
	}
}
