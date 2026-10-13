package main

import (
	"encoding/hex"
	"strings"
	"testing"
)

func Test076AddressDecoderPublishedVectorsAndExactScriptIdentity(t *testing.T) {
	// Witness vector from BIP350; checksum implementations must not validate
	// themselves using only round trips through their own encoder.
	vectors := []struct{ address, script string }{
		{"1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", "76a91462e907b15cbf27d5425399ebf6f0fb50ebb88f1888ac"},
		{"3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy", "a914b472a266d0bd89c13706a4132ccfb16f7c3b9fcb87"},
		{"BC1QW508D6QEJXTDG4Y5R3ZARVARY0C5XW7KV8F3T4", "0014751e76e8199196d454941c45d1b3a323f1433bd6"},
		{"bc1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7vqzk5jj0", "512079be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"},
	}
	for _, v := range vectors {
		canonical, script, _, err := bitcoinAddressScript(v.address)
		if err != nil || script != v.script {
			t.Fatal(v, canonical, script, err)
		}
	}
	for _, script := range []string{"0020" + strings.Repeat("11", 32), "5120" + strings.Repeat("22", 32)} {
		raw, _ := hex.DecodeString(script)
		_, address := describeScript(raw)
		canonical, got, _, err := bitcoinAddressScript(strings.ToUpper(address))
		if err != nil || canonical != address || got != script {
			t.Fatal(address, got, err)
		}
	}
}
func Test076AddressDecoderRejectsNetworkChecksumCaseAndAmbiguity(t *testing.T) {
	invalid := []string{"", strings.Repeat("1", 91), "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNb", "111A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", "bc1Qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4", "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080", "tb1qrp33g0q5c5txsp9arysrx4k6zdkfs4nce4xj0gdcccefvpysxf3q0sl5k7", "mipcBbFg9gMiCh81Kj8tqqdgoZub1ZJRfn", "bc1q<script>alert(1)</script>", "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa?x=1"}
	for _, address := range invalid {
		if _, _, _, err := bitcoinAddressScript(address); err == nil {
			t.Fatal("invalid address accepted", address)
		}
	}
	for _, address := range []string{"bc1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7vqh2y7hd", "bc1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7vq07qwwzcrf"} {
		if _, _, _, err := bitcoinAddressScript(address); err == nil {
			t.Fatal("published invalid Taproot vector accepted", address)
		}
	}
	program, _ := convertBits(make([]byte, 32), 8, 5, true)
	for _, address := range []string{bech32Encode("bc", append([]byte{0}, program...), 0x2bc830a3), bech32Encode("bc", append([]byte{1}, program...), 1)} {
		if _, _, _, err := bitcoinAddressScript(address); err == nil {
			t.Fatal("wrong witness checksum encoding accepted", address)
		}
	}
}
