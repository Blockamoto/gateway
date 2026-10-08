package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func baselineFixture065(t *testing.T) ([]byte, []byte) {
	t.Helper()
	genesis, _ := hex.DecodeString(genesisHeaderHex)
	raw := append(genesis, mainnetBlockOne(t)[:80]...)
	return raw, baselineMetadata065(raw)
}
func baselineMetadata065(raw []byte) []byte {
	digest, tip := sha256.Sum256(raw), hash256(raw[len(raw)-80:])
	b, _ := json.Marshal(headerBaseline{Schema: 1, Network: "mainnet", Count: int64(len(raw) / 80), SHA256: hex.EncodeToString(digest[:]), TipHash: reverseHex(tip[:]), CreatedAt: time.Now().UTC().Format(time.RFC3339)})
	return b
}
func Test065HeaderBaselineValidationResumeAndPreservation(t *testing.T) {
	raw, metadata := baselineFixture065(t)
	a := &app{headersPath: filepath.Join(t.TempDir(), "headers.bin")}
	if e := a.importHeaderBaseline(context.Background(), raw, metadata); e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(a.headersPath)
	if !bytes.Equal(got, raw) || a.getStatus().HeaderBootstrapState != "ready" {
		t.Fatal("valid baseline not installed")
	}
	if e := a.importHeaderBaseline(context.Background(), raw, metadata); e != nil {
		t.Fatal(e)
	}
	if a.getStatus().HeaderBootstrapState != "skipped" {
		t.Fatal("existing coverage was reimported")
	}
	// An interruption leaves its last validated boundary usable on retry.
	if e := os.WriteFile(a.headersPath, raw[:80], 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := a.importHeaderBaseline(ctx, raw, metadata); e == nil {
		t.Fatal("cancelled import succeeded")
	}
	got, _ = os.ReadFile(a.headersPath)
	if !bytes.Equal(got, raw[:80]) {
		t.Fatal("cancelled import changed saved headers")
	}
	if e := a.importHeaderBaseline(context.Background(), raw, metadata); e != nil {
		t.Fatal("retry failed", e)
	}
}
func Test065HeaderBaselineRejectsUntrustedEvidence(t *testing.T) {
	raw, metadata := baselineFixture065(t)
	for _, kind := range []string{"digest", "proof", "network", "length"} {
		t.Run(kind, func(t *testing.T) {
			input := append([]byte(nil), raw...)
			meta := metadata
			switch kind {
			case "digest":
				input[84] ^= 1
			case "proof":
				input[len(input)-1] ^= 1
				meta = baselineMetadata065(input)
			case "network":
				input[0] ^= 1
				meta = baselineMetadata065(input)
			case "length":
				input = input[:len(input)-1]
			}
			a := &app{headersPath: filepath.Join(t.TempDir(), "headers.bin")}
			if e := a.importHeaderBaseline(context.Background(), input, meta); e == nil {
				t.Fatal("invalid evidence accepted")
			}
			got, _ := os.ReadFile(a.headersPath)
			if len(got) > 80 {
				t.Fatal("invalid header committed")
			}
		})
	}
}
func Test065HeaderBaselineDoesNotReplaceDifferentHistory(t *testing.T) {
	raw, _ := baselineFixture065(t)
	longer := append(append([]byte(nil), raw...), raw[80:]...)
	a := &app{headersPath: filepath.Join(t.TempDir(), "headers.bin")}
	existing := append([]byte(nil), raw...)
	existing[159] ^= 1
	if e := os.WriteFile(a.headersPath, existing, 0600); e != nil {
		t.Fatal(e)
	}
	if e := a.importHeaderBaseline(context.Background(), longer, baselineMetadata065(longer)); e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(a.headersPath)
	if !bytes.Equal(got, existing) || a.getStatus().HeaderBootstrapState != "skipped" {
		t.Fatal("baseline replaced differing history")
	}
}
