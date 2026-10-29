package main

import (
	"strings"
	"testing"
)

func TestGatewayBootstrapNormalizesDomainsAndPorts(t *testing.T) {
	for input, want := range map[string]string{
		" Example.COM. ": "example.com:48333", "example.com:49000": "example.com:49000",
		"127.0.0.1": "127.0.0.1:48333", "::1": "[::1]:48333", "[::1]": "[::1]:48333", "[::1]:49000": "[::1]:49000",
		"localhost": "localhost:48333",
	} {
		got, err := normalizeGatewayBootstrapPeer(input)
		if err != nil || got != want {
			t.Fatalf("%q became %q (%v), want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "https://example.com", "user@example.com", "bad host", "example.com:0", "example.com:65536", "example.com:no", "example..com", "-bad.example", "bad-.example", "999.999.999.999", "[fe80::1%3]:48333", "é.example", "name.onion"} {
		if _, err := normalizeGatewayBootstrapPeer(input); err == nil {
			t.Fatalf("accepted malformed/unsupported peer %q", input)
		}
	}
	got, err := normalizeGatewayBootstrapPeers([]string{"example.com", "EXAMPLE.COM.:48333", "127.0.0.1"})
	if err != nil || len(got) != 2 {
		t.Fatalf("canonical deduplication failed: %v %v", got, err)
	}
	tooMany := []string{}
	for i := 0; i <= maxGatewayBootstrapPeers; i++ {
		tooMany = append(tooMany, "peer"+strings.Repeat("x", i)+".example")
	}
	if _, err := normalizeGatewayBootstrapPeers(tooMany); err == nil {
		t.Fatal("bootstrap limit ignored")
	}
}
