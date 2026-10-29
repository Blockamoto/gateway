package updates

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testManifest() Manifest {
	now := time.Now().UTC().Truncate(time.Second)
	return Manifest{Schema: Schema, Version: "0.6.4", Sequence: 7, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), SourceRevision: strings.Repeat("a", 40), ReleaseURL: "https://github.com/Blockamoto/gateway/releases/tag/v0.6.4", Notes: "Updater validation", Artifacts: []Artifact{{Platform: "windows-amd64", File: ArtifactName("0.6.4", "windows-amd64"), Bytes: 3, SHA256: strings.Repeat("a", 64), Format: "zip"}, {Platform: "linux-amd64", File: ArtifactName("0.6.4", "linux-amd64"), Bytes: 3, SHA256: strings.Repeat("b", 64), Format: "zip"}}}
}

func envelopeUnchecked(t *testing.T, manifest Manifest, private ed25519.PrivateKey) []byte {
	t.Helper()
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return signedRawPayload(t, payload, private)
}

func signedRawPayload(t *testing.T, payload []byte, private ed25519.PrivateKey) []byte {
	t.Helper()
	message := append(append([]byte(nil), signatureDomain...), payload...)
	data, err := json.Marshal(Envelope{Schema: Schema, KeyID: KeyID(private.Public().(ed25519.PublicKey)), Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, message))})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSignedManifestSelectAndTrust(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data, err := Sign(testManifest(), private)
	if err != nil {
		t.Fatal(err)
	}
	manifest, artifact, err := VerifySelect(data, NewTrustedKey(public), Policy{CurrentVersion: "0.6.3", Platform: "windows-amd64", MinimumSequence: 7})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "0.6.4" || artifact.Platform != "windows-amd64" {
		t.Fatalf("wrong selected update: %+v %+v", manifest, artifact)
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := Verify(data, NewTrustedKey(other), Policy{}); err == nil {
		t.Fatal("accepted publisher's untrusted key")
	}
	trusted := NewTrustedKey(public)
	trusted.KeyID = strings.Repeat("0", 32)
	if _, err := Verify(data, trusted, Policy{}); err == nil {
		t.Fatal("accepted key ID mismatch")
	}
	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	payload, _ := base64.StdEncoding.DecodeString(envelope.Payload)
	payload = bytes.Replace(payload, []byte("0.6.4"), []byte("0.6.9"), 1)
	envelope.Payload = base64.StdEncoding.EncodeToString(payload)
	tampered, _ := json.Marshal(envelope)
	if _, err := Verify(tampered, NewTrustedKey(public), Policy{}); err == nil {
		t.Fatal("accepted tampered signed manifest")
	}
}

func TestSignedManifestRejectsUnsafeMetadata(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	cases := []struct {
		name   string
		alter  func(*Manifest)
		policy Policy
	}{
		{"downgrade", func(m *Manifest) {}, Policy{CurrentVersion: "0.7.0"}},
		{"same-version", func(m *Manifest) {}, Policy{CurrentVersion: "0.6.4"}},
		{"old-sequence", func(m *Manifest) {}, Policy{MinimumSequence: 8}},
		{"expired", func(m *Manifest) { m.ExpiresAt = time.Now().Add(-time.Second).UTC().Format(time.RFC3339) }, Policy{}},
		{"future", func(m *Manifest) { m.IssuedAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339) }, Policy{}},
		{"long-validity", func(m *Manifest) { m.ExpiresAt = time.Now().Add(32 * 24 * time.Hour).UTC().Format(time.RFC3339) }, Policy{}},
		{"zero-sequence", func(m *Manifest) { m.Sequence = 0 }, Policy{}},
		{"unknown-schema", func(m *Manifest) { m.Schema = 2 }, Policy{}},
		{"origin-substitution", func(m *Manifest) { m.ReleaseURL = "https://evil.invalid/v0.6.4" }, Policy{}},
		{"truncated-revision", func(m *Manifest) { m.SourceRevision = "deadbeef" }, Policy{}},
		{"malformed-version", func(m *Manifest) { m.Version = "v0.6.4" }, Policy{}},
		{"large-component", func(m *Manifest) { m.Version = "1.999999999999999999999999.4" }, Policy{}},
		{"filename-traversal", func(m *Manifest) { m.Artifacts[0].File = "../../GatewayClient.exe" }, Policy{}},
		{"wrong-format", func(m *Manifest) { m.Artifacts[0].Format = "exe" }, Policy{}},
		{"zero-size", func(m *Manifest) { m.Artifacts[0].Bytes = 0 }, Policy{}},
		{"oversized-artifact", func(m *Manifest) { m.Artifacts[0].Bytes = MaxArtifactBytes + 1 }, Policy{}},
		{"invalid-digest", func(m *Manifest) { m.Artifacts[0].SHA256 = "not a hash" }, Policy{}},
		{"duplicate-platform", func(m *Manifest) { m.Artifacts[1] = m.Artifacts[0] }, Policy{}},
		{"unknown-platform", func(m *Manifest) { m.Artifacts[0].Platform = "darwin-arm64" }, Policy{}},
		{"empty-artifacts", func(m *Manifest) { m.Artifacts = nil }, Policy{}},
		{"long-notes", func(m *Manifest) { m.Notes = strings.Repeat("x", 16001) }, Policy{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testManifest()
			tc.alter(&m)
			if _, err := Verify(envelopeUnchecked(t, m, private), NewTrustedKey(public), tc.policy); err == nil {
				t.Fatal("accepted unsafe update metadata")
			}
		})
	}
	m := testManifest()
	m.Artifacts = m.Artifacts[:1]
	if _, _, err := VerifySelect(envelopeUnchecked(t, m, private), NewTrustedKey(public), Policy{Platform: "linux-amd64"}); err == nil {
		t.Fatal("accepted unavailable client platform")
	}
	if _, _, err := VerifySelect(envelopeUnchecked(t, m, private), NewTrustedKey(public), Policy{Platform: "windows-arm64"}); err == nil {
		t.Fatal("accepted unsupported client platform")
	}
}

func TestSignedManifestBoundsAndAmbiguousJSON(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	trusted := NewTrustedKey(public)
	data, err := Sign(testManifest(), private)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range [][]byte{nil, []byte("{}"), append(data, []byte("{}")...), []byte(strings.Repeat("x", MaxManifestBytes+1)), bytes.Replace(data, []byte(`"schema": 1`), []byte(`"schema": 1, "unexpected": true`), 1), bytes.Replace(data, []byte(`"schema": 1`), []byte(`"schema": 1, "schema": 1`), 1)} {
		if _, err := Verify(value, trusted, Policy{}); err == nil {
			t.Fatalf("accepted invalid envelope %q", value[:min(len(value), 50)])
		}
	}
	payload, _ := json.Marshal(testManifest())
	payload = bytes.Replace(payload, []byte(`"version":"0.6.4"`), []byte(`"version":"0.6.3","version":"0.6.4"`), 1)
	if _, err := Verify(signedRawPayload(t, payload, private), trusted, Policy{}); err == nil {
		t.Fatal("accepted duplicated signed payload field")
	}
}

func TestUpdatePackageDigestBounded(t *testing.T) {
	data := []byte("valid update package")
	hash := sha256.Sum256(data)
	artifact := Artifact{Bytes: int64(len(data)), SHA256: hex.EncodeToString(hash[:])}
	if err := VerifyDigest(bytes.NewReader(data), artifact); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{append(append([]byte(nil), data...), 0), data[:len(data)-1], []byte(strings.Repeat("x", len(data)))} {
		if err := VerifyDigest(bytes.NewReader(bad), artifact); err == nil {
			t.Fatal("accepted changed package")
		}
	}
	artifact.Bytes = MaxArtifactBytes + 1
	if err := VerifyDigest(bytes.NewReader(nil), artifact); err == nil {
		t.Fatal("accepted unbounded package")
	}
}

func TestUpdateVersionComparison(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{{"0.6.4", "0.6.3", 1}, {"0.6.4", "0.6.4", 0}, {"0.6.4", "0.7.0", -1}, {"0.10.0", "0.9.99", 1}, {"1.0.0", "0.999.999", 1}} {
		result, err := CompareVersions(tc.a, tc.b)
		if err != nil || result != tc.want {
			t.Fatalf("%s / %s = %d, %v", tc.a, tc.b, result, err)
		}
	}
	for _, bad := range []string{"0.06.4", "0.6.4-beta", "0.6", "-1.0.0", "1.999999999999999999.0"} {
		if _, err := CompareVersions(bad, "0.0.0"); err == nil {
			t.Fatalf("accepted malformed version %q", bad)
		}
	}
}
