package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBitmapAddressAndRevealProjection(t *testing.T) {
	t.Skip("Public Bitmap resolution is intentionally locked in 0.6.6; Bitmap recipe/validation tests remain active.")
	for _, input := range []string{"0.bitmap", "god://12.bitmap", "http://123.bitmap/"} {
		target, err := parseLocalResolverTarget(input)
		if err != nil || target.Namespace != "bitmap" {
			t.Fatalf("%s: %+v %v", input, target, err)
		}
	}
	for _, input := range []string{"01.bitmap", "1.2.bitmap", "-1.bitmap"} {
		if _, err := parseLocalResolverTarget(input); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	a, s := indexPeerFixture(t, "bitmap", 1)
	value, err := a.indexLookup("bitmap", "0")
	if err != nil {
		t.Fatal(err)
	}
	found := value.(indexLookupResult)
	if !found.Found || found.RevealHeight != 792435 || found.RevealBlockHash != s.checkpoint.BlockHash {
		t.Fatalf("lost reveal: %+v", found)
	}
	r := *found.Record
	r.Lean = true
	encoded, err := json.Marshal(bitmapQueryRecord{r, bitmapCompatibility(r)})
	if err != nil || !strings.Contains(string(encoded), `"reveal_height":792435`) || !strings.Contains(string(encoded), `"schema":"bitmap-lean-v2"`) {
		t.Fatalf("projection %s: %v", encoded, err)
	}
	stored, err := json.Marshal(r)
	if err != nil || !strings.HasPrefix(string(stored), `"0:`) {
		t.Fatalf("changed committed encoding: %s %v", stored, err)
	}
}
