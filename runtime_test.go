package main

import (
	"net/url"
	"testing"
)

func TestBrowserResolveURLNormalizesGatewayEnvelope(t *testing.T) {
	got := browserResolveURL("http://127.0.0.1:12345", "god://0.bitcoin/")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if q := u.Query().Get("resolve"); q != "0.bitcoin" {
		t.Fatalf("resolve=%q, want 0.bitcoin (url=%q)", q, got)
	}
}
