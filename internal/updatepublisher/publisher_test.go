package updatepublisher

import (
	"../updates"
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testVersion = "0.6.4"
const testRevision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func makeArchive(t *testing.T, directory, platform string, extra map[string]string) string {
	t.Helper()
	filename := filepath.Join(directory, updates.ArtifactName(testVersion, platform))
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	root := strings.TrimSuffix(filepath.Base(filename), ".zip") + "/"
	client, helper := "gateway-client", "gateway-update-helper"
	if platform == "windows-amd64" {
		client, helper = "GatewayClient.exe", "GatewayUpdateHelper.exe"
	}
	entries := map[string]string{client: "fixture executable bytes", helper: "fixture update helper bytes", "SOURCE-REVISION.txt": "Repository: https://github.com/Blockamoto/gateway\nCommit: " + testRevision + "\nVersion: " + testVersion + "\n", "COMPATIBILITY.json": "{}", "README.txt": "local updater fixture"}
	if platform == "windows-amd64" {
		entries["GatewayOnDemand.exe"] = "fixture launcher"
		entries["GatewayNativeHost.exe"] = "fixture nativehost"
		entries["browser-companion/manifest.json"] = "{}"
		entries["browser-companion/IDENTITY.txt"] = "fixture browser identity"
	}
	for name, data := range extra {
		entries[name] = data
	}
	for name, data := range entries {
		entry, err := writer.Create(root + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(entry, data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return filename
}

func originFixture(t *testing.T) OriginRelease {
	t.Helper()
	directory := t.TempDir()
	origin := OriginRelease{Version: testVersion, Revision: testRevision, Notes: "Local signed fixture", Packages: map[string]string{"windows-amd64": makeArchive(t, directory, "windows-amd64", nil), "linux-amd64": makeArchive(t, directory, "linux-amd64", nil)}, Expected: make(map[string]OriginArtifact)}
	for platform, filename := range origin.Packages {
		data, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(data)
		origin.Expected[platform] = OriginArtifact{File: filepath.Base(filename), Bytes: int64(len(data)), SHA256: hex.EncodeToString(hash[:])}
	}
	return origin
}

func TestPublisherKeyProvisioningAndIdentity(t *testing.T) {
	directory := t.TempDir()
	privatePath, publicPath := filepath.Join(directory, "private.json"), filepath.Join(directory, "public.json")
	trusted, err := GenerateKey(privatePath, publicPath)
	if err != nil {
		t.Fatal(err)
	}
	private, err := ReadPrivateKey(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadTrustedKey(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	if trusted != loaded || trusted.KeyID != updates.KeyID(private.Public().(ed25519.PublicKey)) {
		t.Fatal("provisioned key identity mismatch")
	}
	if _, err := GenerateKey(privatePath, publicPath); err == nil {
		t.Fatal("overwrote existing publisher keys")
	}
	publicData, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(publicData, []byte("private_key")) {
		t.Fatal("private key exposed in trust document")
	}
	keyData, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	keyData = bytes.Replace(keyData, []byte(trusted.KeyID), []byte(strings.Repeat("0", 32)), 1)
	if err := os.WriteFile(privatePath, keyData, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivateKey(privatePath); err == nil {
		t.Fatal("accepted mismatched private key identity")
	}
}

func TestPublisherPromotionDistributionIsolationAndRenewal(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	trusted := updates.NewTrustedKey(public)
	directory := t.TempDir()
	origin := originFixture(t)
	manifest, err := Publish(directory, origin, 1, time.Hour, private)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Artifacts) != 2 {
		t.Fatal("did not promote both intended packages")
	}
	if err := os.WriteFile(filepath.Join(directory, "github-token.txt"), []byte("do-not-expose-private-token"), 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := Handler(directory, trusted)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	response, err := http.Get(server.URL + "/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	verified, artifact, err := updates.VerifySelect(data, trusted, updates.Policy{CurrentVersion: "0.6.3", Platform: "windows-amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if verified.Sequence != 1 || bytes.Contains(data, []byte("token")) {
		t.Fatal("wrong or credential-bearing signed feed")
	}
	response, err = http.Get(server.URL + "/artifacts/" + artifact.File)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("artifact status %d", response.StatusCode)
	}
	if err := updates.VerifyDigest(response.Body, artifact); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	for _, target := range []string{"/", "/github-token.txt", "/config.json", "/api/settings", "/snapshots/", "/artifacts/../github-token.txt", "/manifest.json?token=test", "/artifacts/private.zip", "/manifest%2ejson"} {
		response, err := http.Get(server.URL + target)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 404 {
			t.Fatalf("unintended path %q returned %d", target, response.StatusCode)
		}
		if bytes.Contains(body, []byte("do-not-expose")) {
			t.Fatal("private distribution file exposed")
		}
	}
	response, err = http.Post(server.URL+"/manifest.json", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 405 {
		t.Fatal("publisher exposed mutation API")
	}
	request, _ := http.NewRequest(http.MethodHead, server.URL+"/artifacts/"+artifact.File, nil)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("artifact HEAD failed")
	}
	if _, err := Publish(directory, origin, 1, time.Hour, private); err == nil {
		t.Fatal("accepted nonincreasing feed sequence")
	}
	if _, err := Publish(directory, origin, 2, time.Hour, private); err != nil {
		t.Fatalf("same-package validity renewal failed: %v", err)
	}
	changed := origin
	changed.Revision = strings.Repeat("b", 40)
	if _, err := Publish(directory, changed, 3, time.Hour, private); err == nil {
		t.Fatal("substituted an existing version's revision")
	}
	response, err = http.Get(server.URL + "/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(response.Body)
	response.Body.Close()
	verified, err = updates.Verify(data, trusted, updates.Policy{})
	if err != nil || verified.Sequence != 2 {
		t.Fatalf("failed promotion damaged prior feed: %+v %v", verified, err)
	}
	_, snapshot, err := loadCurrent(directory, trusted, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "artifacts", artifact.File), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	response, err = http.Get(server.URL + "/artifacts/" + artifact.File)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 503 {
		t.Fatal("served tampered package")
	}
}

func TestPublisherRejectsOldUnsignedReleaseAndUnsafeZIP(t *testing.T) {
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	origin := originFixture(t)
	old := origin
	old.Version = "0.6.3"
	if _, err := Publish(t.TempDir(), old, 1, time.Hour, private); err == nil {
		t.Fatal("promoted current unsigned 0.6.3")
	}
	for _, entry := range []string{"../secret.txt", "profile/settings.json", "bitcoin/wallet.dat", "PRIVATE-KEY.json", "browser-companion/../../profile.json"} {
		t.Run(entry, func(t *testing.T) {
			directory := t.TempDir()
			filename := makeArchive(t, directory, "windows-amd64", map[string]string{entry: "private"})
			if err := ValidateArchive(filename, testVersion, "windows-amd64", testRevision); err == nil {
				t.Fatal("accepted unsafe origin ZIP")
			}
		})
	}
	directory := t.TempDir()
	filename := makeArchive(t, directory, "windows-amd64", nil)
	if err := ValidateArchive(filename, testVersion, "windows-amd64", strings.Repeat("b", 40)); err == nil {
		t.Fatal("accepted source revision mismatch")
	}
	if _, err := Publish(t.TempDir(), origin, 0, time.Hour, private); err == nil {
		t.Fatal("accepted zero sequence")
	}
	if _, err := Publish(t.TempDir(), origin, 1, 32*24*time.Hour, private); err == nil {
		t.Fatal("accepted excessive feed validity")
	}
	output := t.TempDir()
	if err := os.WriteFile(filepath.Join(output, ".publish-lock"), []byte("another publisher"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(output, origin, 1, time.Hour, private); err == nil {
		t.Fatal("ignored publisher lock")
	}
}

func TestPublisherRequiresIndependentServerKeyAndValidPointer(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	directory := t.TempDir()
	if _, err := Publish(directory, originFixture(t), 1, time.Hour, private); err != nil {
		t.Fatal(err)
	}
	if _, err := Handler(directory, updates.NewTrustedKey(other)); err == nil {
		t.Fatal("server accepted wrong trust identity")
	}
	if err := os.WriteFile(filepath.Join(directory, currentFile), []byte(`{"snapshot":"../../private"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Handler(directory, updates.NewTrustedKey(public)); err == nil {
		t.Fatal("server accepted traversal pointer")
	}
}

func TestPublisherListenerTLSRules(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8786", "localhost:8786", "[::1]:8786"} {
		if err := ValidateListener(address, false, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, address := range []string{"0.0.0.0:8786", "192.0.2.1:8786", "[::]:8786"} {
		if err := ValidateListener(address, false, false); err == nil {
			t.Fatal("accepted nonlocal cleartext distribution")
		}
		if err := ValidateListener(address, true, false); err != nil {
			t.Fatal(err)
		}
		if err := ValidateListener(address, false, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, address := range []string{"http://localhost:8786", "localhost", "localhost:8786/path"} {
		if err := ValidateListener(address, false, false); err == nil {
			t.Fatalf("accepted malformed listener %q", address)
		}
	}
}

func TestPublisherPrivateGitHubOriginDownloadAndIntegrity(t *testing.T) {
	origin := originFixture(t)
	files := make(map[string][]byte)
	assets := []githubAsset{}
	manifest := OriginManifest{Version: testVersion, SourceRevision: testRevision, Verification: "zip_crc_and_all_file_hashes_pass"}
	for i, platform := range []string{"windows-amd64", "linux-amd64"} {
		data, err := os.ReadFile(origin.Packages[platform])
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(data)
		name := updates.ArtifactName(testVersion, platform)
		manifest.Artifacts = append(manifest.Artifacts, OriginArtifact{File: name, Bytes: int64(len(data)), SHA256: hex.EncodeToString(hash[:])})
		assets = append(assets, githubAsset{ID: int64(i + 2), Name: name, Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(hash[:])})
		files["/repos/Blockamoto/gateway/releases/assets/"+string(rune('2'+i))] = data
	}
	data, _ := json.Marshal(manifest)
	hash := sha256.Sum256(data)
	assets = append(assets, githubAsset{ID: 1, Name: "artifact-manifest.json", Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(hash[:])}, githubAsset{ID: 4, Name: "private-wallet.zip", Size: 123})
	files["/repos/Blockamoto/gateway/releases/assets/1"] = data
	releaseData, _ := json.Marshal(githubRelease{TagName: "v" + testVersion, HTMLURL: "https://github.com/Blockamoto/gateway/releases/tag/v" + testVersion, Body: "fixture release notes", Assets: assets})
	files["/repos/Blockamoto/gateway/releases/tags/v"+testVersion] = releaseData
	files["/repos/Blockamoto/gateway/git/ref/tags/v"+testVersion] = []byte(`{"object":{"type":"commit","sha":"` + testRevision + `"}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-private-token" {
			t.Error("missing private-origin token")
			w.WriteHeader(401)
			return
		}
		data, found := files[r.URL.Path]
		if !found {
			t.Errorf("requested unrelated origin asset %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()
	downloaded, err := fetchGitHub(context.Background(), testVersion, "fixture-private-token", t.TempDir(), server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if len(downloaded.Packages) != 2 || downloaded.Revision != testRevision {
		t.Fatal("incorrect authenticated origin packages")
	}
	if _, err := fetchGitHub(context.Background(), "0.6.3", "fixture-private-token", t.TempDir(), server.URL, server.Client()); err == nil {
		t.Fatal("auto-promoted unsigned old release")
	}
	if _, err := fetchGitHub(context.Background(), testVersion, "", t.TempDir(), server.URL, server.Client()); err == nil {
		t.Fatal("accepted absent private-origin credential")
	}
	files["/repos/Blockamoto/gateway/releases/assets/2"] = []byte("corrupt package")
	if _, err := fetchGitHub(context.Background(), testVersion, "fixture-private-token", t.TempDir(), server.URL, server.Client()); err == nil || strings.Contains(err.Error(), "fixture-private-token") {
		t.Fatal("tampered private origin accepted or credential leaked in error")
	}
}

func TestPublisherRedirectStripsOriginCredentials(t *testing.T) {
	client := githubClient()
	request, _ := http.NewRequest("GET", "https://release-assets.githubusercontent.com/future.zip", nil)
	request.Header.Set("Authorization", "Bearer fixture-private-token")
	request.Header.Set("Cookie", "private-cookie")
	request.Header.Set("Proxy-Authorization", "private-proxy")
	if err := client.CheckRedirect(request, []*http.Request{{}}); err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{"Authorization", "Cookie", "Proxy-Authorization"} {
		if request.Header.Get(header) != "" {
			t.Fatal("private-origin credential forwarded to download redirect")
		}
	}
	insecure, _ := http.NewRequest("GET", "http://cdn.invalid/future.zip", nil)
	if err := client.CheckRedirect(insecure, []*http.Request{{}}); err == nil {
		t.Fatal("accepted insecure asset redirect")
	}
	unrelated, _ := http.NewRequest("GET", "https://unrelated.invalid/future.zip", nil)
	if err := client.CheckRedirect(unrelated, []*http.Request{{}}); err == nil {
		t.Fatal("accepted redirect outside GitHub release CDN")
	}
}

func TestPublisherLocalFixtureChecksDigest(t *testing.T) {
	directory := t.TempDir()
	filename := makeArchive(t, directory, "linux-amd64", nil)
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	manifest := OriginManifest{Version: testVersion, SourceRevision: testRevision, Artifacts: []OriginArtifact{{File: filepath.Base(filename), Bytes: int64(len(data)), SHA256: hex.EncodeToString(hash[:])}}}
	metadata, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(directory, "artifact-manifest.json"), metadata, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLocalFixture(directory, testVersion); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"0.6.3", "../0.6.5", "0.6.5/../../secret"} {
		if _, err := ReadLocalFixture(directory, bad); err == nil {
			t.Fatalf("accepted unsafe/old fixtureversion %q", bad)
		}
	}
	if err := os.WriteFile(filename, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLocalFixture(directory, testVersion); err == nil {
		t.Fatal("accepted tampered local fixture")
	}
}
