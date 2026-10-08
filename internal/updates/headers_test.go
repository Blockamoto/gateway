package updates

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

func headerProtocolFixture(t *testing.T) (HeaderManifest, []byte, ed25519.PrivateKey) {
	t.Helper()
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	raw := make([]byte, 80)
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	writer.Write(raw)
	writer.Close()
	data := buffer.Bytes()
	digest := sha256.Sum256(data)
	c := HeaderChunk{From: 0, To: 0, PreviousHash: fmt.Sprintf("%064d", 0), TipHash: HeaderHash(raw), Bytes: int64(len(data)), SHA256: hex.EncodeToString(digest[:])}
	c.File = fmt.Sprintf("headers-0-0-%s.gz", c.SHA256)
	now := time.Now().UTC()
	m := HeaderManifest{Schema: 1, Network: "mainnet", Sequence: 2, IssuedAt: now.Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), SourceRelease: "https://github.com/Blockamoto/gateway/releases/tag/v0.6.6", Count: 1, TipHash: c.TipHash, Chunks: []HeaderChunk{c}}
	return m, data, key
}
func TestHeaderManifestSignedScopeAndRollback(t *testing.T) {
	m, _, key := headerProtocolFixture(t)
	envelope, e := SignHeaders(m, key)
	if e != nil {
		t.Fatal(e)
	}
	trust := NewTrustedKey(key.Public().(ed25519.PublicKey))
	if _, e = VerifyHeaders(envelope, trust, 2, time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e = VerifyHeaders(envelope, trust, 3, time.Now()); e == nil {
		t.Fatal("rollback accepted")
	}
	if _, e = VerifyHeaders(envelope, trust, 0, time.Now().Add(2*time.Hour)); e == nil {
		t.Fatal("expired metadata accepted")
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	if _, e = VerifyHeaders(envelope, NewTrustedKey(other.Public().(ed25519.PublicKey)), 0, time.Now()); e == nil {
		t.Fatal("wrong key accepted")
	}
	if _, e = Verify(envelope, trust, Policy{}); e == nil {
		t.Fatal("header signature crossed into application domain")
	}
	duplicate := append([]byte(`{"schema":1,`), envelope[1:]...)
	if _, e = VerifyHeaders(duplicate, trust, 0, time.Now()); e == nil {
		t.Fatal("duplicate JSON accepted")
	}
	for _, mutate := range []func(*HeaderManifest){func(m *HeaderManifest) { m.Chunks[0].From = 1 }, func(m *HeaderManifest) { m.Chunks[0].File = "../secret" }, func(m *HeaderManifest) { m.Count = MaxHeaderCount + 1 }, func(m *HeaderManifest) { m.Network = "testnet" }, func(m *HeaderManifest) { m.Chunks[0].Bytes = MaxHeaderChunkBytes + 1 }} {
		candidate := m
		candidate.Chunks = append([]HeaderChunk{}, m.Chunks...)
		mutate(&candidate)
		if _, e = SignHeaders(candidate, key); e == nil {
			t.Fatal("invalid bounds accepted")
		}
	}
}
func TestHeaderChunkDigestAnchorsAndExpansion(t *testing.T) {
	m, data, _ := headerProtocolFixture(t)
	c := m.Chunks[0]
	if raw, e := DecodeHeaderChunk(data, c); e != nil || len(raw) != 80 {
		t.Fatal(e)
	}
	bad := append([]byte{}, data...)
	bad[len(bad)-1] ^= 1
	if _, e := DecodeHeaderChunk(bad, c); e == nil {
		t.Fatal("tampering accepted")
	}
	c.PreviousHash = fmt.Sprintf("%064d", 1)
	if _, e := DecodeHeaderChunk(data, c); e == nil {
		t.Fatal("wrong anchor accepted")
	}
	c = m.Chunks[0]
	c.To = 1
	if _, e := DecodeHeaderChunk(data, c); e == nil {
		t.Fatal("wrong expansion accepted")
	}
}
