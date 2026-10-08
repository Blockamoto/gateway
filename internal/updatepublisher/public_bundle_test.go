package updatepublisher

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"../updates"
)

type publisherRoundTrip func(*http.Request) (*http.Response, error)

func (f publisherRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func publisherResponse(r *http.Request, status int, data []byte, location string) *http.Response {
	header := make(http.Header)
	if location != "" {
		header.Set("Location", location)
	}
	return &http.Response{StatusCode: status, Header: header, Request: r, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}
}

func publicBundleFixture(t *testing.T) ([]byte, ed25519.PrivateKey, updates.TrustedKey) {
	t.Helper()
	dir, private, key, _ := renewalFixture(t)
	path := filepath.Join(t.TempDir(), "feed.zip")
	if e := ExportDistribution(dir, path, key); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return raw, private, key
}

func Test070PublicBundleDownloadUsesExactReleaseWithoutREST(t *testing.T) {
	raw, _, key := publicBundleFixture(t)
	requests := 0
	client := githubClient()
	client.Transport = publisherRoundTrip(func(r *http.Request) (*http.Response, error) {
		requests++
		want := "https://github.com/Blockamoto/gateway/releases/download/v" + testVersion + "/gateway-update-feed-v" + testVersion + ".zip"
		if r.URL.String() != want || r.Method != http.MethodGet || r.Header.Get("Authorization") != "" {
			t.Fatalf("unexpected anonymous origin request: %s", r.URL.Redacted())
		}
		return publisherResponse(r, 200, raw, ""), nil
	})
	dest := filepath.Join(t.TempDir(), "feed")
	if e := restoreGitHubDistribution(context.Background(), DefaultRepository, testVersion, "", dest, key, client); e != nil {
		t.Fatal(e)
	}
	if requests != 1 {
		t.Fatal("public startup made additional REST requests", requests)
	}
	if _, e := CheckDistribution(dest, key); e != nil {
		t.Fatal(e)
	}
}

func Test070PublicBundleRejectsInvalidReleaseOrBytes(t *testing.T) {
	raw, private, key := publicBundleFixture(t)
	for _, kind := range []string{"tampered-package", "expired", "wrong-version", "wrong-key", "repository", "version-path", "private-or-missing"} {
		t.Run(kind, func(t *testing.T) {
			data, trust, repo, version, status := raw, key, DefaultRepository, testVersion, http.StatusOK
			switch kind {
			case "tampered-package":
				data = rewriteRenewalBundle(t, raw, func(name string, b []byte) []byte {
					if strings.HasPrefix(name, "artifacts/") {
						return append(b, []byte("tampered")...)
					}
					return b
				})
			case "expired":
				data = rewriteRenewalBundle(t, raw, func(name string, b []byte) []byte {
					if name == "manifest.json" || name == "headers-manifest.json" {
						return expireRenewalEnvelope(t, b, private, name == "headers-manifest.json")
					}
					return b
				})
			case "wrong-version":
				version = "0.7.0"
			case "wrong-key":
				public, _, _ := ed25519.GenerateKey(rand.Reader)
				trust = updates.NewTrustedKey(public)
			case "repository":
				repo = "Blockamoto/gateway-dev"
			case "version-path":
				version = "0.7.0/../latest"
			case "private-or-missing":
				status = http.StatusNotFound
			}
			calls := 0
			client := githubClient()
			client.Transport = publisherRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				return publisherResponse(r, status, data, ""), nil
			})
			dest := filepath.Join(t.TempDir(), "feed")
			e := restoreGitHubDistribution(context.Background(), repo, version, "", dest, trust, client)
			if e == nil {
				t.Fatal("invalid public bundle accepted")
			}
			if (kind == "repository" || kind == "version-path") && calls != 0 {
				t.Fatal("unapproved URL reached the network")
			}
			if kind == "private-or-missing" && (!strings.Contains(e.Error(), "publicly accessible") || !strings.Contains(e.Error(), "404")) {
				t.Fatal("missing public access was not explained", e)
			}
			if _, e := os.Stat(dest); !os.IsNotExist(e) {
				t.Fatal("failed restore exposed a serving directory")
			}
		})
	}
}

func Test070PublicBundleRedirectBoundsAndHTTPS(t *testing.T) {
	raw, _, key := publicBundleFixture(t)
	for _, target := range []string{"https://release-assets.githubusercontent.com/fixture", "https://objects.githubusercontent.com/fixture", "http://release-assets.githubusercontent.com/fixture", "https://evil.example/fixture", "https://user:secret@github.com/fixture", "loop"} {
		t.Run(target, func(t *testing.T) {
			calls := 0
			client := githubClient()
			client.Transport = publisherRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
					t.Fatal("anonymous download sent credentials")
				}
				if target == "loop" {
					return publisherResponse(r, 302, nil, "https://github.com/loop"), nil
				}
				if calls == 1 {
					return publisherResponse(r, 302, nil, target), nil
				}
				return publisherResponse(r, 200, raw, ""), nil
			})
			e := restoreGitHubDistribution(context.Background(), DefaultRepository, testVersion, "", filepath.Join(t.TempDir(), "feed"), key, client)
			allowed := strings.HasPrefix(target, "https://release-assets.githubusercontent.com/") || strings.HasPrefix(target, "https://objects.githubusercontent.com/")
			if allowed && (e != nil || calls != 2) {
				t.Fatal("ordinary release CDN redirect failed", e, calls)
			}
			if !allowed && (e == nil || (target != "loop" && calls != 1) || calls > 5) {
				t.Fatal("redirect policy did not reject before unsafe request", e, calls)
			}
		})
	}
}

type unreadPublisherBody struct{ t *testing.T }

func (r unreadPublisherBody) Read([]byte) (int, error) {
	r.t.Fatal("oversized body was read")
	return 0, io.EOF
}
func (r unreadPublisherBody) Close() error { return nil }

func Test070OriginDownloadBounds(t *testing.T) {
	client := githubClient()
	client.Transport = publisherRoundTrip(func(r *http.Request) (*http.Response, error) {
		response := publisherResponse(r, 200, nil, "")
		response.ContentLength = updates.MaxArtifactBytes + 1
		response.Body = unreadPublisherBody{t}
		return response, nil
	})
	if _, e := originGET(context.Background(), client, "https://github.com/fixture", "", "application/octet-stream", updates.MaxArtifactBytes); e == nil {
		t.Fatal("known oversized response accepted")
	}
	client.Transport = publisherRoundTrip(func(r *http.Request) (*http.Response, error) {
		response := publisherResponse(r, 200, []byte("more than 4 bytes"), "")
		response.ContentLength = -1
		return response, nil
	})
	if _, e := originGET(context.Background(), client, "https://github.com/fixture", "", "application/octet-stream", 4); e == nil {
		t.Fatal("unknown-length response bypassed streaming size bound")
	}
}

func Test070AuthenticatedBundleKeepsRESTAndStripsRedirectCredentials(t *testing.T) {
	raw, _, key := publicBundleFixture(t)
	hash := sha256.Sum256(raw)
	asset := githubAsset{ID: 42, Name: "gateway-update-feed-v" + testVersion + ".zip", Size: int64(len(raw)), Digest: "sha256:" + hex.EncodeToString(hash[:])}
	metadata, _ := json.Marshal(githubRelease{TagName: "v" + testVersion, HTMLURL: "https://github.com/" + DefaultRepository + "/releases/tag/v" + testVersion, Assets: []githubAsset{asset}})
	apiCalls, cdnCalls := 0, 0
	client := githubClient()
	client.Transport = publisherRoundTrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Hostname() {
		case "api.github.com":
			apiCalls++
			if r.Header.Get("Authorization") != "Bearer fixture-private-token" {
				t.Fatal("explicit private credential was not used at API origin")
			}
			if strings.HasSuffix(r.URL.Path, "/releases/assets/42") {
				return publisherResponse(r, 302, nil, "https://release-assets.githubusercontent.com/fixture"), nil
			}
			return publisherResponse(r, 200, metadata, ""), nil
		case "release-assets.githubusercontent.com":
			cdnCalls++
			if r.Header.Get("Authorization") != "" {
				t.Fatal("private token reached CDN")
			}
			return publisherResponse(r, 200, raw, ""), nil
		default:
			t.Fatal("authenticated branch changed origin", r.URL.Redacted())
			return nil, nil
		}
	})
	if e := restoreGitHubDistribution(context.Background(), DefaultRepository, testVersion, "fixture-private-token", filepath.Join(t.TempDir(), "feed"), key, client); e != nil {
		t.Fatal(e)
	}
	if apiCalls != 2 || cdnCalls != 1 {
		t.Fatal("wrong private origin flow", apiCalls, cdnCalls)
	}
}
