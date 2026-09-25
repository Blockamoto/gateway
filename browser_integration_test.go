package main

import "testing"

func TestNormalizeBrowserResourceAddress(t *testing.T) {
	good := []string{
		"0.bitcoin",
		"123.840000.bitcoin",
		"2.123.840000.bitcoin",
		"i1.123.840000.bitcoin",
		"500.2.123.840000.bitcoin",
	}
	for _, in := range good {
		got := normalizeBrowserResourceAddress(in)
		if !got.Valid {
			t.Fatalf("%s should be valid: %s", in, got.Error)
		}
		if got.Address != in {
			t.Fatalf("%s normalized to %s", in, got.Address)
		}
		if got.WebURL != "http://"+in+"/" {
			t.Fatalf("unexpected web url %s", got.WebURL)
		}
		if got.GatewayURI != "god://"+in {
			t.Fatalf("unexpected gateway uri %s", got.GatewayURI)
		}
	}
	bad := []string{"bitcoin", "what is 0.bitcoin", "0.bitcoin/path", "https://0.bitcoin", "banana.bitcoin", "1.2.3.4.5.bitcoin"}
	for _, in := range bad {
		if got := normalizeBrowserResourceAddress(in); got.Valid {
			t.Fatalf("%s should not be a browser Gateway resource", in)
		}
	}
}
