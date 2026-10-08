package main

import "testing"

func TestBitcoinFriendlyGrammar(t *testing.T) {
	cases := []struct{ in, kind, q, canonical string }{
		{"750000.bitcoin", "block", "750000", "god://750000.bitcoin"},
		{"123.750000.bitcoin", "transaction", "123.750000", "god://123.750000.bitcoin"},
		{"2.123.750000.bitcoin", "output", "2.123.750000", "god://2.123.750000.bitcoin"},
		{"i1.123.750000.bitcoin", "input", "i1.123.750000", "god://i1.123.750000.bitcoin"},
		{"500.2.123.750000.bitcoin", "satpoint", "500.2.123.750000", "god://500.2.123.750000.bitcoin"},
	}
	for _, tc := range cases {
		got, matched, err := parseBitcoinFriendly(tc.in)
		if err != nil || !matched {
			t.Fatalf("%s: matched=%v err=%v", tc.in, matched, err)
		}
		if got.Kind != tc.kind || got.SearchQuery != tc.q || got.CanonicalURI != tc.canonical {
			t.Fatalf("%s: got %#v", tc.in, got)
		}
	}
}
func TestGODMirrorsFriendlyGrammar(t *testing.T) {
	cases := []struct{ in, kind, q, friendly string }{
		{"god://750000.bitcoin", "block", "750000", "750000.bitcoin"},
		{"god://750000.bitcoin/", "block", "750000", "750000.bitcoin"},
		{"god://123.750000.bitcoin", "transaction", "123.750000", "123.750000.bitcoin"},
		{"god://2.123.750000.bitcoin", "output", "2.123.750000", "2.123.750000.bitcoin"},
		{"god://i1.123.750000.bitcoin", "input", "i1.123.750000", "i1.123.750000.bitcoin"},
		{"god://500.2.123.750000.bitcoin", "satpoint", "500.2.123.750000", "500.2.123.750000.bitcoin"},
	}
	for _, tc := range cases {
		got, matched, err := parseGOD(tc.in)
		if err != nil || !matched {
			t.Fatalf("%s: matched=%v err=%v", tc.in, matched, err)
		}
		if got.Kind != tc.kind || got.SearchQuery != tc.q || got.Friendly != tc.friendly {
			t.Fatalf("%s: got %#v", tc.in, got)
		}
	}
}
func TestLegacyONDPositionalMigration(t *testing.T) {
	got, matched, err := parseLegacyOND("ond://bitcoin/output-at/2/123/750000")
	if err != nil || !matched || got.CanonicalURI != "god://2.123.750000.bitcoin" {
		t.Fatalf("got=%#v matched=%v err=%v", got, matched, err)
	}
}
func TestInvalidResolverNames(t *testing.T) {
	bad := []string{"banana.750000.bitcoin", "2.foo.123.750000.bitcoin", "god://bitcoin/block/750000", "god://ord.bitcoin/path"}
	for _, in := range bad {
		if _, err := parseLocalResolverTarget(in); err == nil {
			t.Fatalf("expected %q to fail", in)
		}
	}
}
func TestSatpointCoordinateParser(t *testing.T) {
	c, ok := parseBODCoordinate("500.2.123.750000")
	if !ok || c.Kind != coordSatpoint || c.SatOffset != 500 || c.IOIndex != 2 || c.TxIndex != 123 || c.Height != 750000 {
		t.Fatalf("bad coordinate %#v", c)
	}
}
