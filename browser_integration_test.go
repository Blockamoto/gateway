package main

import (
	"strings"
	"testing"
)

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

func Test077SatBrowserNamespaceUsesExactCanonicalTokens(t *testing.T) {
	for _, input := range []string{"0.sat", "5000000000.sat", "5000000000.SAT", "nvtdijuwxlp.sat", "nvtcsezkbth.sat"} {
		got := normalizeBrowserResourceAddress(input)
		want := strings.ToLower(input)
		if !got.Valid || got.Address != want || got.Namespace != ".sat" || got.Module != "Sats" || got.WebURL != "http://"+want+"/" || got.GatewayURI != "god://"+want {
			t.Fatalf("%q: %+v", input, got)
		}
	}
	for _, input := range []string{".sat", "sat", "bad-name.sat", "nvtdijuwxlq.sat", "01.sat", "-1.sat", "1.2.sat", "18446744073709551616.sat", "1.sat.example", "1.sat/path", "what is 0.sat"} {
		if got := normalizeBrowserResourceAddress(input); got.Valid {
			t.Fatalf("noncanonical .sat accepted: %q", input)
		}
	}
	if !strings.Contains(gatewayNamespacePowerShellArray(), "'.sat'") {
		t.Fatal("optional Windows routing omitted the registered Sat namespace")
	}
}
