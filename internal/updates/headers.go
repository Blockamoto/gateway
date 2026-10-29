package updates

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

const MaxHeaderManifestBytes = 256 << 10
const HeaderChunkCount int64 = 10000
const MaxHeaderCount int64 = 2000000
const MaxHeaderChunkBytes int64 = 1 << 20

var headerSignatureDomain = []byte("Gateway Bitcoin header manifest v1\x00")

type HeaderChunk struct {
	From         int64  `json:"from"`
	To           int64  `json:"to"`
	PreviousHash string `json:"previous_hash"`
	TipHash      string `json:"tip_hash"`
	File         string `json:"file"`
	Bytes        int64  `json:"bytes"`
	SHA256       string `json:"sha256"`
}
type HeaderManifest struct {
	Schema        int           `json:"schema"`
	Network       string        `json:"network"`
	Sequence      uint64        `json:"sequence"`
	IssuedAt      string        `json:"issued_at"`
	ExpiresAt     string        `json:"expires_at"`
	SourceRelease string        `json:"source_release"`
	Count         int64         `json:"count"`
	TipHash       string        `json:"tip_hash"`
	Chunks        []HeaderChunk `json:"chunks"`
}

func HeaderHash(raw []byte) string {
	first := sha256.Sum256(raw)
	second := sha256.Sum256(first[:])
	for i, j := 0, len(second)-1; i < j; i, j = i+1, j-1 {
		second[i], second[j] = second[j], second[i]
	}
	return hex.EncodeToString(second[:])
}
func ValidateHeaderManifest(m HeaderManifest, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	if m.Schema != 1 || m.Network != "mainnet" || m.Sequence == 0 || m.Count < 1 || m.Count > MaxHeaderCount || !digestPattern.MatchString(m.TipHash) {
		return fmt.Errorf("invalid header manifest identity or bounds")
	}
	issued, e := time.Parse(time.RFC3339, m.IssuedAt)
	if e != nil {
		return e
	}
	expiry, e := time.Parse(time.RFC3339, m.ExpiresAt)
	if e != nil {
		return e
	}
	if issued.After(now.Add(5*time.Minute)) || !expiry.After(now) || !expiry.After(issued) || expiry.Sub(issued) > MaxValidity {
		return fmt.Errorf("invalid or expired header manifest validity")
	}
	if !validReleaseURL(m.SourceRelease) {
		return fmt.Errorf("invalid header release source")
	}
	if len(m.Chunks) != int((m.Count+HeaderChunkCount-1)/HeaderChunkCount) {
		return fmt.Errorf("invalid header chunk count")
	}
	previous := fmt.Sprintf("%064d", 0)
	for i, c := range m.Chunks {
		end := int64(i+1)*HeaderChunkCount - 1
		if end >= m.Count {
			end = m.Count - 1
		}
		if c.From != int64(i)*HeaderChunkCount || c.To != end || c.PreviousHash != previous || !digestPattern.MatchString(c.TipHash) || !digestPattern.MatchString(c.SHA256) || c.Bytes < 1 || c.Bytes > MaxHeaderChunkBytes || c.File != fmt.Sprintf("headers-%d-%d-%s.gz", c.From, c.To, c.SHA256) {
			return fmt.Errorf("invalid or discontinuous header chunk")
		}
		previous = c.TipHash
	}
	if previous != m.TipHash {
		return fmt.Errorf("header manifest tip mismatch")
	}
	return nil
}
func validReleaseURL(raw string) bool {
	const prefix = "https://github.com/Blockamoto/gateway/releases/tag/v"
	if len(raw) > len(prefix) && raw[:len(prefix)] == prefix {
		_, e := CompareVersions(raw[len(prefix):], "0.0.0")
		return e == nil
	}
	return false
}
func SignHeaders(m HeaderManifest, private ed25519.PrivateKey) ([]byte, error) {
	if len(private) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid signing key")
	}
	if e := ValidateHeaderManifest(m, time.Now()); e != nil {
		return nil, e
	}
	payload, e := json.Marshal(m)
	if e != nil {
		return nil, e
	}
	envelope := Envelope{Schema: 1, KeyID: KeyID(private.Public().(ed25519.PublicKey)), Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, append(append([]byte{}, headerSignatureDomain...), payload...)))}
	raw, e := json.Marshal(envelope)
	if len(raw) > MaxHeaderManifestBytes {
		return nil, fmt.Errorf("header manifest too large")
	}
	return raw, e
}
func VerifyHeaders(raw []byte, key TrustedKey, minimum uint64, now time.Time) (HeaderManifest, error) {
	var m HeaderManifest
	if len(raw) == 0 || len(raw) > MaxHeaderManifestBytes {
		return m, fmt.Errorf("header manifest length invalid")
	}
	public, e := ParseTrustedKey(key)
	if e != nil {
		return m, e
	}
	var envelope Envelope
	if e = strictJSON(raw, &envelope); e != nil {
		return m, e
	}
	if envelope.Schema != 1 || envelope.KeyID != key.KeyID {
		return m, fmt.Errorf("untrusted header signer")
	}
	payload, e := base64.StdEncoding.Strict().DecodeString(envelope.Payload)
	if e != nil {
		return m, e
	}
	sig, e := base64.StdEncoding.Strict().DecodeString(envelope.Signature)
	if e != nil || !ed25519.Verify(public, append(append([]byte{}, headerSignatureDomain...), payload...), sig) {
		return m, fmt.Errorf("header signature invalid")
	}
	if e = strictJSON(payload, &m); e != nil {
		return m, e
	}
	if e = ValidateHeaderManifest(m, now); e != nil {
		return m, e
	}
	if m.Sequence < minimum {
		return m, fmt.Errorf("header manifest sequence rollback")
	}
	return m, nil
}
func DecodeHeaderChunk(raw []byte, c HeaderChunk) ([]byte, error) {
	digest := sha256.Sum256(raw)
	if int64(len(raw)) != c.Bytes || c.Bytes > MaxHeaderChunkBytes || hex.EncodeToString(digest[:]) != c.SHA256 || c.From < 0 || c.To < c.From || c.To-c.From >= HeaderChunkCount {
		return nil, fmt.Errorf("header chunk size or digest mismatch")
	}
	r, e := gzip.NewReader(bytes.NewReader(raw))
	if e != nil {
		return nil, e
	}
	defer r.Close()
	size := (c.To - c.From + 1) * 80
	data, e := io.ReadAll(io.LimitReader(r, size+1))
	if e != nil || int64(len(data)) != size {
		return nil, fmt.Errorf("header chunk expansion invalid")
	}
	previous := append([]byte{}, data[4:36]...)
	for i, j := 0, len(previous)-1; i < j; i, j = i+1, j-1 {
		previous[i], previous[j] = previous[j], previous[i]
	}
	if hex.EncodeToString(previous) != c.PreviousHash || HeaderHash(data[len(data)-80:]) != c.TipHash {
		return nil, fmt.Errorf("header chunk anchor mismatch")
	}
	return data, nil
}
