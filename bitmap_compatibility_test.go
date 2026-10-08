package main

import "testing"

func TestBitmapCompatibilityCannotMutateBaseOrInventHistoricalAgreement(t *testing.T) {
	for _, mime := range []string{"", "application/json", "text/plainXYZ"} {
		r := bitmapRecord{Accepted: true, Inscription: "synthetic-fixture", ContentType: mime, Compatibility: "not_evaluated"}
		before := indexDigest(r)
		got := bitmapCompatibility(r)
		if got.State != "excluded" || !got.Contested || indexDigest(r) != before {
			t.Fatalf("wrong explicit MIME divergence: %+v", got)
		}
	}
	for _, mime := range []string{"text/plain", "text/plain;charset=utf-8"} {
		got := bitmapCompatibility(bitmapRecord{Accepted: true, ContentType: mime})
		if got.State != "historical_context_missing" || got.Contested {
			t.Fatalf("invented OPI agreement: %+v", got)
		}
	}
}
