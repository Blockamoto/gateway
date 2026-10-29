package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLeanBitmapRestartCollisionAndReorg(t *testing.T) {
	root := t.TempDir()
	s, e := openIndexStore(root, "bitmap")
	if e != nil {
		t.Fatal(e)
	}
	s.head.Mode = "lean"
	first := bitmapTestBlock(792435, strings.Repeat("1", 64), strings.Repeat("0", 64), "0.bitmap")
	if e = s.appendBlock(first, 792435, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	bytes, e := os.ReadFile(filepath.Join(s.dir, "commits", s.head.Commitment+".json"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(bytes), "first_valid_claim") || !strings.Contains(string(bytes), `"0:`) {
		t.Fatalf("not lean: %s", bytes)
	}
	s, e = openIndexStore(root, "bitmap")
	if e != nil {
		t.Fatal(e)
	}
	if len(s.winners) != 1 || s.head.Mode != "lean" {
		t.Fatal("restart lost winner/mode")
	}
	second := bitmapTestBlock(792436, strings.Repeat("2", 64), first.Hash, "0.bitmap")
	if e = s.appendBlock(second, 792435, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	b, e := s.readBatch(s.head.Commitment)
	if e != nil || len(b.Bitmap) != 0 {
		t.Fatal("duplicate persisted or checkpoint invalid", e)
	}
	s, e = openIndexStore(root, "bitmap")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.reconcile(func(h int64) (string, error) {
		if h == 792435 {
			return first.Hash, nil
		}
		return strings.Repeat("3", 64), nil
	}); e != nil {
		t.Fatal(e)
	}
	if s.checkpoint.Height != 792435 || len(s.winners) != 1 {
		t.Fatal("rewind lost base state")
	}
	var record bitmapRecord
	for _, bad := range []string{`"01:bad"`, `"0:bad"`, `"0"`} {
		if json.Unmarshal([]byte(bad), &record) == nil {
			t.Fatal("accepted malformed claim")
		}
	}
}

func TestInscriptionCoordinateAllEntryForms(t *testing.T) {
	for _, q := range []string{"12i3.900000", "12.i3.900000", "12.i3.900000.bitcoin", "god://12.i3.900000.bitcoin", "http://12.i3.900000.bitcoin/"} {
		target, e := parseLocalResolverTarget(q)
		if e != nil || target.Kind != "inscription_coordinate" || target.SearchQuery != "12i3.900000" || target.Friendly != "12i3.900000.bitcoin" {
			t.Fatalf("%s: %+v %v", q, target, e)
		}
	}
}
