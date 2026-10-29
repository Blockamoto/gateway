package updatepublisher

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"../updates"
)

func renewalFixture(t *testing.T) (string, ed25519.PrivateKey, updates.TrustedKey, RenewalResult) {
	t.Helper()
	pub, private, _ := ed25519.GenerateKey(rand.Reader)
	key := updates.NewTrustedKey(pub)
	dir := t.TempDir()
	a, e := Publish(dir, originFixture(t), 3, time.Hour, private)
	if e != nil {
		t.Fatal(e)
	}
	h, e := PublishHeaders(dir, HeaderSnapshot{Raw: make([]byte, 80), SourceRelease: a.ReleaseURL}, 8, time.Hour, private)
	if e != nil {
		t.Fatal(e)
	}
	return dir, private, key, RenewalResult{a, h}
}

func Test070RenewBothFeedsPreservesApprovedRelease(t *testing.T) {
	dir, private, key, original := renewalFixture(t)
	renewed, e := RenewAll(dir, testVersion, 30*24*time.Hour, private)
	if e != nil {
		t.Fatal(e)
	}
	if renewed.Application.Sequence != 4 || renewed.Headers.Sequence != 9 || renewed.Application.SourceRevision != original.Application.SourceRevision || renewed.Application.ReleaseURL != original.Application.ReleaseURL || renewed.Headers.SourceRelease != original.Headers.SourceRelease || !reflect.DeepEqual(renewed.Application.Artifacts, original.Application.Artifacts) || !reflect.DeepEqual(renewed.Headers.Chunks, original.Headers.Chunks) {
		t.Fatal("renewal changed release identity or failed to advance each sequence", renewed)
	}
	for _, dates := range [][2]string{{renewed.Application.IssuedAt, renewed.Application.ExpiresAt}, {renewed.Headers.IssuedAt, renewed.Headers.ExpiresAt}} {
		issued, _ := time.Parse(time.RFC3339, dates[0])
		expires, _ := time.Parse(time.RFC3339, dates[1])
		if expires.Sub(issued) != 30*24*time.Hour {
			t.Fatal("wrong renewed validity")
		}
	}
	if _, e = CheckDistribution(dir, key); e != nil {
		t.Fatal(e)
	}
	if _, e = RenewAll(dir, testVersion, 30*24*time.Hour, private); e != nil {
		t.Fatal(e)
	}
	last, e := InspectForRenewal(dir, key)
	if e != nil || last.Application.Sequence != 5 || last.Headers.Sequence != 10 {
		t.Fatal("second renewal reused a sequence", e)
	}
}

func Test070RenewAllRejectsBeforeChangingAppSelection(t *testing.T) {
	for _, kind := range []string{"wrong-version", "missing-headers", "corrupt-chunk", "wrong-key", "invalid-validity", "wrong-header-release", "exhausted-header-sequence", "header-writer-active"} {
		t.Run(kind, func(t *testing.T) {
			dir, private, _, original := renewalFixture(t)
			before, _ := os.ReadFile(filepath.Join(dir, currentFile))
			version, validity := testVersion, time.Hour
			switch kind {
			case "wrong-version":
				version = "0.99.0"
			case "missing-headers":
				os.Remove(filepath.Join(dir, "headers-manifest.json"))
			case "corrupt-chunk":
				os.WriteFile(filepath.Join(dir, "header-chunks", original.Headers.Chunks[0].File), []byte("tampered"), 0600)
			case "wrong-key":
				_, private, _ = ed25519.GenerateKey(rand.Reader)
			case "invalid-validity":
				validity = 32 * 24 * time.Hour
			case "header-writer-active":
				os.WriteFile(filepath.Join(dir, ".headers-publish-lock"), []byte{}, 0600)
			default:
				if kind == "wrong-header-release" {
					original.Headers.SourceRelease = "https://github.com/" + DefaultRepository + "/releases/tag/v0.99.0"
				} else {
					original.Headers.Sequence = ^uint64(0)
				}
				raw, e := updates.SignHeaders(original.Headers, private)
				if e != nil {
					t.Fatal(e)
				}
				os.WriteFile(filepath.Join(dir, "headers-manifest.json"), raw, 0600)
			}
			if _, e := RenewAll(dir, version, validity, private); e == nil {
				t.Fatal("invalid renewal accepted")
			}
			after, _ := os.ReadFile(filepath.Join(dir, currentFile))
			if !bytes.Equal(before, after) {
				t.Fatal("failed preflight advanced the application feed")
			}
		})
	}
}

func rewriteRenewalBundle(t *testing.T, raw []byte, change func(string, []byte) []byte) []byte {
	t.Helper()
	r, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, f := range r.File {
		reader, _ := f.Open()
		b, _ := io.ReadAll(reader)
		reader.Close()
		entry, _ := w.Create(f.Name)
		entry.Write(change(f.Name, b))
	}
	w.Close()
	return out.Bytes()
}

// Construct authentic historical metadata independently; production Sign APIs
// correctly refuse to issue an already expired manifest.
func expireRenewalEnvelope(t *testing.T, raw []byte, private ed25519.PrivateKey, header bool) []byte {
	t.Helper()
	var envelope updates.Envelope
	json.Unmarshal(raw, &envelope)
	payload, _ := base64.StdEncoding.DecodeString(envelope.Payload)
	var fields map[string]json.RawMessage
	json.Unmarshal(payload, &fields)
	fields["issued_at"], _ = json.Marshal(time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339))
	fields["expires_at"], _ = json.Marshal(time.Now().Add(-time.Hour).UTC().Format(time.RFC3339))
	payload, _ = json.Marshal(fields)
	domain := "Gateway desktop update manifest v1\x00"
	if header {
		domain = "Gateway Bitcoin header manifest v1\x00"
	}
	envelope.Payload = base64.StdEncoding.EncodeToString(payload)
	envelope.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, append([]byte(domain), payload...)))
	raw, _ = json.Marshal(envelope)
	return raw
}

func Test070ExpiredBundleRecoveryStillRequiresAuthenticityAndRenewal(t *testing.T) {
	dir, private, key, _ := renewalFixture(t)
	archive := filepath.Join(t.TempDir(), "feed.zip")
	if e := ExportDistribution(dir, archive, key); e != nil {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(archive)
	expired := rewriteRenewalBundle(t, raw, func(name string, data []byte) []byte {
		if name == "manifest.json" || name == "headers-manifest.json" {
			return expireRenewalEnvelope(t, data, private, name == "headers-manifest.json")
		}
		return data
	})
	if e := RestoreDistribution(expired, filepath.Join(t.TempDir(), "live"), testVersion, DefaultRepository, key); e == nil {
		t.Fatal("ordinary restore accepted expired metadata")
	}
	recovery := filepath.Join(t.TempDir(), "operator")
	if e := RestoreDistributionForRenewal(expired, recovery, testVersion, DefaultRepository, key); e != nil {
		t.Fatal(e)
	}
	if _, e := Handler(recovery, key); e == nil {
		t.Fatal("expired recovered metadata became servable")
	}
	if e := ExportDistribution(recovery, filepath.Join(t.TempDir(), "out.zip"), key); e == nil {
		t.Fatal("expired recovered metadata became exportable")
	}
	if _, e := RenewAll(recovery, testVersion, 30*24*time.Hour, private); e != nil {
		t.Fatal(e)
	}
	if _, e := CheckDistribution(recovery, key); e != nil {
		t.Fatal(e)
	}
	tampered := rewriteRenewalBundle(t, expired, func(name string, data []byte) []byte {
		if name == "manifest.json" {
			var env updates.Envelope
			json.Unmarshal(data, &env)
			p, _ := base64.StdEncoding.DecodeString(env.Payload)
			p = bytes.Replace(p, []byte(testRevision), []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), 1)
			env.Payload = base64.StdEncoding.EncodeToString(p)
			data, _ = json.Marshal(env)
		}
		return data
	})
	if e := RestoreDistributionForRenewal(tampered, filepath.Join(t.TempDir(), "bad"), testVersion, DefaultRepository, key); e == nil {
		t.Fatal("operator recovery bypassed signature verification")
	}
}

func Test070PublisherLegalNotices(t *testing.T) {
	for _, platform := range []string{"windows-amd64", "linux-amd64"} {
		extra := map[string]string{"LICENSE": "project terms", "THIRD-PARTY-NOTICES.txt": "dependency notices", "GO-LICENSE.txt": "Go license"}
		archive := makeArchive(t, t.TempDir(), platform, extra)
		if e := ValidateArchive(archive, testVersion, platform, testRevision); e != nil {
			t.Fatal(e)
		}
		if approvedEntry("unexpected/LICENSE", testVersion, platform) || approvedEntry("LICENSE.exe", testVersion, platform) {
			t.Fatal("legal notice allowlist widened beyond exact filenames")
		}
	}
}
