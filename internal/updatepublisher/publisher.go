// Package updatepublisher promotes explicitly selected private GitHub release
// archives into a signed, distribution-only feed. Its credentials and private
// signing key are never copied into that feed.
package updatepublisher

import (
	"../updateapply"
	"../updates"
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const maxOriginMetadata = 1 << 20
const currentFile = "current.json"
const DefaultRepository = "Blockamoto/gateway"

var snapshotPattern = regexp.MustCompile(`^[0-9]+-v[0-9]+\.[0-9]+\.[0-9]+-[a-f0-9]{16}$`)

type PrivateKeyFile struct {
	Schema     int    `json:"schema"`
	KeyID      string `json:"key_id"`
	PrivateKey string `json:"private_key"`
}

// GenerateKey writes a new private key and an independently shareable public
// trust document. Existing files are never overwritten.
func GenerateKey(privatePath, publicPath string) (updates.TrustedKey, error) {
	if err := checkPublisherPath(privatePath); err != nil {
		return updates.TrustedKey{}, err
	}
	if err := checkPublisherPath(publicPath); err != nil {
		return updates.TrustedKey{}, err
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return updates.TrustedKey{}, err
	}
	trusted := updates.NewTrustedKey(public)
	privateJSON, err := json.MarshalIndent(PrivateKeyFile{Schema: 1, KeyID: trusted.KeyID, PrivateKey: base64.StdEncoding.EncodeToString(private)}, "", "  ")
	if err != nil {
		return updates.TrustedKey{}, err
	}
	publicJSON, _ := json.MarshalIndent(trusted, "", "  ")
	if filepath.Clean(privatePath) == filepath.Clean(publicPath) {
		return updates.TrustedKey{}, errors.New("private and public key paths must differ")
	}
	if _, err := os.Lstat(publicPath); !os.IsNotExist(err) {
		return updates.TrustedKey{}, errors.New("public key output already exists or cannot be inspected")
	}
	if err := exclusiveWrite(privatePath, append(privateJSON, '\n'), 0600); err != nil {
		return updates.TrustedKey{}, err
	}
	if err := exclusiveWrite(publicPath, append(publicJSON, '\n'), 0644); err != nil {
		_ = os.Remove(privatePath)
		return updates.TrustedKey{}, err
	}
	return trusted, nil
}

func exclusiveWrite(name string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(name)
		return writeErr
	}
	return closeErr
}

func ReadPrivateKey(name string) (ed25519.PrivateKey, error) {
	if err := checkPublisherPath(name); err != nil {
		return nil, err
	}
	info, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return nil, errors.New("signing key must be a bounded regular file")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	var value PrivateKeyFile
	if err := decodeJSON(data, &value); err != nil {
		return nil, err
	}
	private, err := base64.StdEncoding.Strict().DecodeString(value.PrivateKey)
	if err != nil || value.Schema != 1 || len(private) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid publisher private key file")
	}
	// An Ed25519 private key includes both seed and public key. Reject malformed
	// combinations, rather than generating signatures clients cannot verify.
	rebuilt := ed25519.NewKeyFromSeed(private[:ed25519.SeedSize])
	if !strings.EqualFold(hex.EncodeToString(rebuilt), hex.EncodeToString(private)) || updates.KeyID(rebuilt.Public().(ed25519.PublicKey)) != value.KeyID {
		return nil, errors.New("publisher private key identity mismatch")
	}
	return ed25519.PrivateKey(private), nil
}

func ReadTrustedKey(name string) (updates.TrustedKey, error) {
	var key updates.TrustedKey
	if err := checkPublisherPath(name); err != nil {
		return key, err
	}
	info, err := os.Lstat(name)
	if err != nil {
		return key, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return key, errors.New("trust document must be a bounded regular file")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return key, err
	}
	if err := decodeJSON(data, &key); err != nil {
		return key, err
	}
	_, err = updates.ParseTrustedKey(key)
	return key, err
}

func decodeJSON(data []byte, value interface{}) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if decoder.Decode(new(interface{})) != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

type OriginManifest struct {
	Version        string           `json:"version"`
	SourceRevision string           `json:"source_revision"`
	Verification   string           `json:"verification"`
	Artifacts      []OriginArtifact `json:"artifacts"`
}
type OriginArtifact struct {
	File   string `json:"file"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type OriginRelease struct {
	Repository string
	Version    string
	Revision   string
	Notes      string
	Packages   map[string]string         // known platform -> local authenticated package
	Expected   map[string]OriginArtifact // exact authenticated origin digest/size, retained through promotion
}

type githubAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	HTMLURL string        `json:"html_url"`
	Draft   bool          `json:"draft"`
	Body    string        `json:"body"`
	Assets  []githubAsset `json:"assets"`
}

// FetchGitHub reads only an explicitly selected future Gateway release from
// GitHub. The API token is used at the private origin, never in artifact URLs,
// logs, manifests or distribution responses. GitHub's authenticated metadata
// and hashes are origin integrity checks, not upstream package signatures.
func FetchGitHub(ctx context.Context, version, token, downloadDir string) (OriginRelease, error) {
	return FetchGitHubRepository(ctx, DefaultRepository, version, token, downloadDir)
}
func FetchGitHubRepository(ctx context.Context, repository, version, token, downloadDir string) (OriginRelease, error) {
	return fetchGitHubRepository(ctx, repository, version, token, downloadDir, "https://api.github.com", githubClient())
}
func validRepository(repository string) bool {
	return repository == DefaultRepository
}

// ReadLocalFixture is an explicit test-only alternative to the private GitHub
// origin. The CLI labels it as such, and its development key must never be used
// as production trust. Package names and hashes still receive the same checks.
func ReadLocalFixture(directory, version string) (OriginRelease, error) {
	var result OriginRelease
	comparison, err := updates.CompareVersions(version, "0.6.3")
	if err != nil || comparison <= 0 {
		return result, errors.New("local fixture must use a valid version newer than unsigned 0.6.3")
	}
	data, err := boundedRegularRead(filepath.Join(directory, "artifact-manifest.json"), maxOriginMetadata)
	if err != nil {
		return result, err
	}
	var manifest OriginManifest
	if err := decodeJSON(data, &manifest); err != nil {
		return result, err
	}
	if manifest.Version != version || len(manifest.Artifacts) > 16 {
		return result, errors.New("local fixture version or asset count mismatch")
	}
	result = OriginRelease{Version: version, Revision: manifest.SourceRevision, Notes: "Local updater validation fixture; not a published release.", Packages: make(map[string]string), Expected: make(map[string]OriginArtifact)}
	seen := make(map[string]bool)
	for _, artifact := range manifest.Artifacts {
		if seen[artifact.File] {
			return OriginRelease{}, errors.New("duplicate local fixture artifact")
		}
		seen[artifact.File] = true
		for _, platform := range []string{"windows-amd64", "linux-amd64"} {
			if artifact.File != updates.ArtifactName(version, platform) {
				continue
			}
			filename := filepath.Join(directory, artifact.File)
			info, err := os.Lstat(filename)
			if err != nil || !info.Mode().IsRegular() {
				return OriginRelease{}, errors.New("local fixture artifact is not a regular file")
			}
			file, err := os.Open(filename)
			if err != nil {
				return OriginRelease{}, err
			}
			err = updates.VerifyDigest(file, updates.Artifact{Bytes: artifact.Bytes, SHA256: artifact.SHA256})
			file.Close()
			if err != nil {
				return OriginRelease{}, err
			}
			if err := ValidateArchive(filename, version, platform, manifest.SourceRevision); err != nil {
				return OriginRelease{}, err
			}
			result.Packages[platform] = filename
			result.Expected[platform] = artifact
		}
	}
	if len(result.Packages) == 0 {
		return OriginRelease{}, errors.New("local fixture requires an intended platform archive")
	}
	return result, nil
}

func githubClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Minute, CheckRedirect: func(request *http.Request, previous []*http.Request) error {
		if len(previous) >= 5 {
			return errors.New("too many GitHub asset redirects")
		}
		if request.URL.Scheme != "https" || request.URL.User != nil {
			return errors.New("GitHub asset redirect must use HTTPS")
		}
		switch request.URL.Hostname() {
		case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		default:
			return errors.New("GitHub asset redirect is outside the permitted release origin/CDN")
		}
		// GitHub API asset requests normally redirect to its release CDN. Never
		// forward private-origin credentials to any redirect, including subdomains.
		request.Header.Del("Authorization")
		request.Header.Del("Cookie")
		request.Header.Del("Proxy-Authorization")
		return nil
	}}
}

func fetchGitHub(ctx context.Context, version, token, downloadDir, api string, client *http.Client) (OriginRelease, error) {
	// Historical origin fixture coverage remains usable; production entrypoint
	// explicitly selects the current development repository above.
	return fetchGitHubRepository(ctx, "Blockamoto/gateway", version, token, downloadDir, api, client)
}
func fetchGitHubRepository(ctx context.Context, repository, version, token, downloadDir, api string, client *http.Client) (OriginRelease, error) {
	var result OriginRelease
	if !validRepository(repository) {
		return result, errors.New("unsupported Gateway release repository")
	}
	comparison, err := updates.CompareVersions(version, "0.6.3")
	if err != nil || comparison <= 0 {
		return result, errors.New("publisher only promotes explicitly selected releases newer than unsigned 0.6.3")
	}
	if strings.TrimSpace(token) == "" {
		return result, errors.New("GATEWAY_PUBLISHER_GITHUB_TOKEN is required for the private release origin")
	}
	if err := checkPublisherPath(downloadDir); err != nil {
		return result, err
	}
	if err := os.MkdirAll(downloadDir, 0700); err != nil {
		return result, err
	}
	releaseData, err := originGET(ctx, client, api+"/repos/"+repository+"/releases/tags/v"+version, token, "application/vnd.github+json", maxOriginMetadata)
	if err != nil {
		return result, err
	}
	var release githubRelease
	if err := json.Unmarshal(releaseData, &release); err != nil {
		return result, errors.New("invalid GitHub release response")
	}
	if release.Draft || release.TagName != "v"+version || release.HTMLURL != "https://github.com/"+repository+"/releases/tag/v"+version {
		return result, errors.New("GitHub origin did not return the requested published Gateway release")
	}
	assets := make(map[string]githubAsset)
	for _, asset := range release.Assets {
		if _, exists := assets[asset.Name]; exists {
			return result, errors.New("ambiguous duplicate GitHub release asset")
		}
		assets[asset.Name] = asset
	}
	manifestAsset, found := assets["artifact-manifest.json"]
	if !found || manifestAsset.ID <= 0 || manifestAsset.Size < 1 || manifestAsset.Size > maxOriginMetadata {
		return result, errors.New("release requires a bounded artifact-manifest.json")
	}
	manifestData, err := originGET(ctx, client, api+fmt.Sprintf("/repos/%s/releases/assets/%d", repository, manifestAsset.ID), token, "application/octet-stream", maxOriginMetadata)
	if err != nil {
		return result, err
	}
	if int64(len(manifestData)) != manifestAsset.Size {
		return result, errors.New("origin artifact manifest size mismatch")
	}
	if err := verifyGitHubDigest(manifestData, manifestAsset.Digest); err != nil {
		return result, err
	}
	var origin OriginManifest
	if err := decodeJSON(manifestData, &origin); err != nil {
		return result, fmt.Errorf("invalid origin artifact manifest: %w", err)
	}
	if origin.Version != version || len(origin.Artifacts) > 16 {
		return result, errors.New("origin artifact manifest version or asset count mismatch")
	}
	if err := verifyOriginTagRepository(ctx, client, api, repository, version, token, origin.SourceRevision); err != nil {
		return result, err
	}
	originAssets := make(map[string]OriginArtifact)
	for _, asset := range origin.Artifacts {
		if _, duplicate := originAssets[asset.File]; duplicate {
			return result, errors.New("duplicate origin artifact digest")
		}
		originAssets[asset.File] = asset
	}
	result = OriginRelease{Repository: repository, Version: version, Revision: origin.SourceRevision, Notes: release.Body, Packages: make(map[string]string), Expected: make(map[string]OriginArtifact)}
	if len(result.Notes) > 16000 {
		result.Notes = result.Notes[:16000]
	}
	for _, platform := range []string{"windows-amd64", "linux-amd64"} {
		name := updates.ArtifactName(version, platform)
		asset, found := assets[name]
		expected, expectedFound := originAssets[name]
		if !found || !expectedFound || asset.ID <= 0 || asset.Size != expected.Bytes || expected.Bytes < 1 || expected.Bytes > updates.MaxArtifactBytes {
			return OriginRelease{}, fmt.Errorf("origin requires valid %s package and digest", platform)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, api+fmt.Sprintf("/repos/%s/releases/assets/%d", repository, asset.ID), nil)
		if err != nil {
			return OriginRelease{}, err
		}
		setOriginHeaders(request, token, "application/octet-stream")
		response, err := client.Do(request)
		if err != nil {
			return OriginRelease{}, errors.New("GitHub package download failed")
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return OriginRelease{}, fmt.Errorf("GitHub package request returned HTTP %d", response.StatusCode)
		}
		nameOnDisk := filepath.Join(downloadDir, name)
		file, err := os.OpenFile(nameOnDisk, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			response.Body.Close()
			return OriginRelease{}, err
		}
		hash := sha256.New()
		count, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, expected.Bytes+1))
		response.Body.Close()
		if copyErr == nil {
			copyErr = file.Sync()
		}
		closeErr := file.Close()
		actualDigest := hex.EncodeToString(hash.Sum(nil))
		if copyErr != nil || closeErr != nil || count != expected.Bytes || actualDigest != expected.SHA256 || (asset.Digest != "" && asset.Digest != "sha256:"+actualDigest) {
			_ = os.Remove(nameOnDisk)
			return OriginRelease{}, errors.New("origin package download failed size or digest verification")
		}
		if err := ValidateArchive(nameOnDisk, version, platform, origin.SourceRevision); err != nil {
			_ = os.Remove(nameOnDisk)
			return OriginRelease{}, err
		}
		result.Packages[platform] = nameOnDisk
		result.Expected[platform] = expected
	}
	return result, nil
}

func setOriginHeaders(request *http.Request, token, accept string) {
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "GatewayUpdatePublisher/1")
}

func originGET(ctx context.Context, client *http.Client, target, token, accept string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	setOriginHeaders(request, token, accept)
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("private GitHub origin request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub origin returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("GitHub metadata exceeds its download limit or failed")
	}
	return data, nil
}

func verifyGitHubDigest(data []byte, digest string) error {
	if digest == "" {
		return nil
	} // Older GitHub assets predate API-provided digests.
	hash := sha256.Sum256(data)
	if digest != "sha256:"+hex.EncodeToString(hash[:]) {
		return errors.New("GitHub origin manifest digest mismatch")
	}
	return nil
}

func verifyOriginTag(ctx context.Context, client *http.Client, api, version, token, revision string) error {
	return verifyOriginTagRepository(ctx, client, api, "Blockamoto/gateway", version, token, revision)
}
func verifyOriginTagRepository(ctx context.Context, client *http.Client, api, repository, version, token, revision string) error {
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(revision) {
		return errors.New("origin source revision must be a full Git commit ID")
	}
	data, err := originGET(ctx, client, api+"/repos/"+repository+"/git/ref/tags/v"+version, token, "application/vnd.github+json", maxOriginMetadata)
	if err != nil {
		return err
	}
	var ref struct {
		Object struct {
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"object"`
	}
	if json.Unmarshal(data, &ref) != nil {
		return errors.New("invalid GitHub tag reference")
	}
	for depth := 0; ref.Object.Type == "tag" && depth < 4; depth++ {
		if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(ref.Object.SHA) {
			return errors.New("invalid annotated GitHub tag identity")
		}
		data, err = originGET(ctx, client, api+"/repos/"+repository+"/git/tags/"+ref.Object.SHA, token, "application/vnd.github+json", maxOriginMetadata)
		if err != nil {
			return err
		}
		if json.Unmarshal(data, &ref) != nil {
			return errors.New("invalid annotated GitHub tag response")
		}
	}
	if ref.Object.Type != "commit" || ref.Object.SHA != revision {
		return errors.New("origin artifact source revision differs from the requested GitHub tag")
	}
	return nil
}

func ValidateArchive(filename, version, platform, revision string) error {
	if err := checkPublisherPath(filename); err != nil {
		return err
	}
	if err := updateapply.ValidateArchive(filename, version, platform); err != nil {
		return fmt.Errorf("origin package layout: %w", err)
	}
	reader, err := zip.OpenReader(filename)
	if err != nil {
		return errors.New("origin update package is not a readable ZIP archive")
	}
	defer reader.Close()
	if len(reader.File) == 0 || len(reader.File) > updates.MaxArchiveEntries {
		return errors.New("origin update package entry count outside bounds")
	}
	root := strings.TrimSuffix(updates.ArtifactName(version, platform), ".zip") + "/"
	seen := make(map[string]bool)
	var expanded uint64
	client := "gateway-client"
	if platform == "windows-amd64" {
		client = "GatewayClient.exe"
	}
	hasClient, hasRevision := false, false
	for _, file := range reader.File {
		name := file.Name
		if strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") || !strings.HasPrefix(name, root) || path.Clean(strings.TrimSuffix(name, "/")) != strings.TrimSuffix(name, "/") || strings.Contains(name, "//") {
			return errors.New("origin package contains unsafe path")
		}
		folded := strings.ToLower(name)
		if seen[folded] {
			return errors.New("origin package contains duplicate paths")
		}
		seen[folded] = true
		if file.Mode()&os.ModeSymlink != 0 || (!file.Mode().IsRegular() && !file.FileInfo().IsDir()) {
			return errors.New("origin package contains a link or special file")
		}
		if file.FileInfo().IsDir() {
			continue
		}
		if file.UncompressedSize64 > updates.MaxExpandedBytes-expanded {
			return errors.New("origin package expanded size exceeds limit")
		}
		expanded += file.UncompressedSize64
		relative := strings.TrimPrefix(name, root)
		if strings.Contains(relative, "/") && !strings.HasPrefix(relative, "browser-companion/") {
			return errors.New("origin package contains unexpected nested files")
		}
		if strings.HasPrefix(relative, "browser-companion/") && platform != "windows-amd64" {
			return errors.New("Linux update contains Windows browser companion")
		}
		if !approvedEntry(relative, version, platform) {
			return fmt.Errorf("origin package contains unexpected file %q", relative)
		}
		if relative == client {
			hasClient = file.UncompressedSize64 > 0
		}
		if relative == "SOURCE-REVISION.txt" {
			if file.UncompressedSize64 > 4096 {
				return errors.New("origin revision record too large")
			}
			entry, err := file.Open()
			if err != nil {
				return err
			}
			data, err := io.ReadAll(io.LimitReader(entry, 4097))
			entry.Close()
			if err != nil || !sourceRevisionMatches(data, version, revision) {
				return errors.New("origin package source revision does not match artifact manifest")
			}
			hasRevision = true
		}
	}
	if !hasClient || !hasRevision {
		return errors.New("origin package is missing its client or source revision")
	}
	return nil
}

func sourceRevisionMatches(data []byte, version, revision string) bool {
	commitFound, versionFound := false, false
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "Commit: ") {
			if commitFound || line != "Commit: "+revision {
				return false
			}
			commitFound = true
		}
		if strings.HasPrefix(line, "Version: ") {
			if versionFound || line != "Version: "+version {
				return false
			}
			versionFound = true
		}
	}
	return commitFound && versionFound
}

func approvedEntry(relative, version, platform string) bool {
	if relative == "LICENSE" || relative == "THIRD-PARTY-NOTICES.txt" || relative == "GO-LICENSE.txt" {
		return true
	}
	if relative == "UPDATE-REVIEW-"+version+".md" || relative == "INTEGRATION-"+version+"-INDEX-EXPLORER.md" || relative == "TESTING-"+version+".md" {
		return true
	}
	if strings.HasPrefix(relative, "browser-companion/") {
		return true
	}
	for _, name := range []string{"gateway-client", "gateway-update-helper", "README.txt", "SOURCE-REVISION.txt", "BINARY-AUDIT.json", "SHA256SUMS.txt", "COMPATIBILITY.json", "GATEWAY-ROADMAP.md", "INDEXING.md", "INDEX-PEER.md", "GATEWAY-BOOTSTRAP.md", "STATUS.md", "BITMAP-RESEARCH.md", "RELEASE-ACCEPTANCE.json", "ZERO-STATE-INDEX-ACCEPTANCE.json", "UPDATES.md", "RELEASE-NOTES-v" + version + ".md"} {
		if relative == name {
			return true
		}
	}
	if platform == "windows-amd64" {
		for _, name := range []string{"GatewayClient.exe", "GatewayOnDemand.exe", "GatewayNativeHost.exe", "GatewayUpdateHelper.exe", "portable.marker", "TestStandalone.cmd", "TestFreshStandalone.cmd", "gateway-client-v" + version + "-installer.exe"} {
			if relative == name {
				return true
			}
		}
	}
	return false
}

type pointer struct {
	Snapshot string `json:"snapshot"`
}

// Publish signs and atomically selects a new immutable local distribution
// snapshot. Callers must explicitly authorize promotion of the selected origin.
func Publish(directory string, origin OriginRelease, sequence uint64, validity time.Duration, private ed25519.PrivateKey) (updates.Manifest, error) {
	var empty updates.Manifest
	if err := checkPublisherPath(directory); err != nil {
		return empty, err
	}
	if err := checkPublisherPath(filepath.Join(directory, "snapshots")); err != nil {
		return empty, err
	}
	comparison, err := updates.CompareVersions(origin.Version, "0.6.3")
	if err != nil || comparison <= 0 {
		return empty, errors.New("unsigned releases at or before 0.6.3 cannot be promoted as signed updates")
	}
	if len(private) != ed25519.PrivateKeySize {
		return empty, errors.New("invalid publisher signing key")
	}
	if validity <= 0 || validity > updates.MaxValidity {
		return empty, errors.New("publisher validity must be positive and at most 31 days")
	}
	if err := os.MkdirAll(filepath.Join(directory, "snapshots"), 0700); err != nil {
		return empty, err
	}
	lock, err := os.OpenFile(filepath.Join(directory, ".publish-lock"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return empty, errors.New("another publisher may be running; inspect .publish-lock before removing a stale lock")
	}
	lock.Close()
	defer os.Remove(filepath.Join(directory, ".publish-lock"))
	now := time.Now().UTC().Truncate(time.Second)
	repository := origin.Repository
	if repository == "" {
		repository = DefaultRepository
	}
	if !validRepository(repository) {
		return empty, errors.New("unsupported Gateway release repository")
	}
	manifest := updates.Manifest{Schema: updates.Schema, Version: origin.Version, Sequence: sequence, IssuedAt: now.Format(time.RFC3339), ExpiresAt: now.Add(validity).Format(time.RFC3339), SourceRevision: origin.Revision, ReleaseURL: "https://github.com/" + repository + "/releases/tag/v" + origin.Version, Notes: origin.Notes}
	platforms := make([]string, 0, len(origin.Packages))
	for platform := range origin.Packages {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	if len(platforms) < 1 || len(platforms) > 2 {
		return empty, errors.New("publisher requires one or two known platform archives")
	}
	staging, err := os.MkdirTemp(filepath.Join(directory, "snapshots"), ".staging-")
	if err != nil {
		return empty, err
	}
	defer os.RemoveAll(staging)
	if err := os.Mkdir(filepath.Join(staging, "artifacts"), 0700); err != nil {
		return empty, err
	}
	for _, platform := range platforms {
		if !updates.SupportedPlatform(platform) {
			return empty, errors.New("publisher does not support this package platform")
		}
		source := origin.Packages[platform]
		info, err := os.Lstat(source)
		if err != nil {
			return empty, err
		}
		if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > updates.MaxArtifactBytes {
			return empty, errors.New("publisher archive must be a bounded regular file")
		}
		if err := ValidateArchive(source, origin.Version, platform, origin.Revision); err != nil {
			return empty, err
		}
		input, err := os.Open(source)
		if err != nil {
			return empty, err
		}
		name := updates.ArtifactName(origin.Version, platform)
		expected, ok := origin.Expected[platform]
		if !ok || expected.File != name || expected.Bytes != info.Size() || expected.Bytes < 1 || expected.Bytes > updates.MaxArtifactBytes {
			input.Close()
			return empty, errors.New("publisher promotion requires retained authenticated origin size and digest for each package")
		}
		output, err := os.OpenFile(filepath.Join(staging, "artifacts", name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			input.Close()
			return empty, err
		}
		hash := sha256.New()
		count, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(input, updates.MaxArtifactBytes+1))
		input.Close()
		if copyErr == nil {
			copyErr = output.Sync()
		}
		closeErr := output.Close()
		if copyErr != nil {
			return empty, copyErr
		}
		if closeErr != nil {
			return empty, closeErr
		}
		if count != info.Size() || count > updates.MaxArtifactBytes {
			return empty, errors.New("publisher archive changed size during staging")
		}
		if count != expected.Bytes || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
			return empty, errors.New("origin package changed after verification; publisher signing refused")
		}
		// Validate the immutable copy too; never sign a package validated before
		// another process changed it during the copy.
		if err := ValidateArchive(filepath.Join(staging, "artifacts", name), origin.Version, platform, origin.Revision); err != nil {
			return empty, err
		}
		manifest.Artifacts = append(manifest.Artifacts, updates.Artifact{Platform: platform, File: name, Bytes: count, SHA256: hex.EncodeToString(hash.Sum(nil)), Format: "zip"})
	}
	if err := updates.ValidateManifest(manifest, now); err != nil {
		return empty, err
	}
	trusted := updates.NewTrustedKey(private.Public().(ed25519.PublicKey))
	if _, err := os.Lstat(filepath.Join(directory, currentFile)); err == nil {
		old, _, err := loadCurrent(directory, trusted, false)
		if err != nil {
			return empty, fmt.Errorf("existing publisher snapshot cannot be verified: %w", err)
		}
		if sequence <= old.Sequence {
			return empty, errors.New("publisher sequence must strictly increase")
		}
		comparison, _ := updates.CompareVersions(origin.Version, old.Version)
		if comparison < 0 {
			return empty, errors.New("publisher version cannot go backwards")
		}
		if comparison == 0 && !sameRelease(old, manifest) {
			return empty, errors.New("renewal of an existing version cannot change its revision or packages")
		}
	} else if !os.IsNotExist(err) {
		return empty, err
	}
	envelope, err := updates.Sign(manifest, private)
	if err != nil {
		return empty, err
	}
	if err := exclusiveWrite(filepath.Join(staging, "manifest.json"), envelope, 0644); err != nil {
		return empty, err
	}
	digest := sha256.Sum256(envelope)
	snapshot := fmt.Sprintf("%d-v%s-%s", sequence, manifest.Version, hex.EncodeToString(digest[:8]))
	target := filepath.Join(directory, "snapshots", snapshot)
	if err := os.Rename(staging, target); err != nil {
		return empty, err
	}
	pointerData, _ := json.Marshal(pointer{Snapshot: snapshot})
	temporary, err := os.CreateTemp(directory, ".current-")
	if err != nil {
		return empty, err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	_, err = temporary.Write(append(pointerData, '\n'))
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err != nil {
		return empty, err
	}
	if closeErr != nil {
		return empty, closeErr
	}
	if err := os.Rename(temporaryName, filepath.Join(directory, currentFile)); err != nil {
		return empty, err
	}
	return manifest, nil
}

func sameRelease(a, b updates.Manifest) bool {
	if a.SourceRevision != b.SourceRevision || len(a.Artifacts) != len(b.Artifacts) {
		return false
	}
	for i := range a.Artifacts {
		if a.Artifacts[i] != b.Artifacts[i] {
			return false
		}
	}
	return true
}

func boundedRegularRead(name string, limit int64) ([]byte, error) {
	if err := checkPublisherPath(name); err != nil {
		return nil, err
	}
	info, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > limit {
		return nil, errors.New("distribution metadata is not a bounded regular file")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("distribution metadata exceeds bounds or failed")
	}
	return data, nil
}

func checkPublisherPath(name string) error {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	return updateapply.CheckPath(absolute)
}

func loadCurrent(directory string, trusted updates.TrustedKey, requireUnexpired bool) (updates.Manifest, string, error) {
	var empty updates.Manifest
	if err := checkPublisherPath(directory); err != nil {
		return empty, "", err
	}
	data, err := boundedRegularRead(filepath.Join(directory, currentFile), 4096)
	if err != nil {
		return empty, "", err
	}
	var current pointer
	if err := decodeJSON(data, &current); err != nil {
		return empty, "", err
	}
	if !snapshotPattern.MatchString(current.Snapshot) {
		return empty, "", errors.New("invalid distribution snapshot pointer")
	}
	snapshot := filepath.Join(directory, "snapshots", current.Snapshot)
	info, err := os.Lstat(snapshot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return empty, "", errors.New("distribution snapshot is not a directory")
	}
	envelope, err := boundedRegularRead(filepath.Join(snapshot, "manifest.json"), updates.MaxManifestBytes)
	if err != nil {
		return empty, "", err
	}
	manifest, err := verifyApplicationManifest(envelope, trusted, requireUnexpired)
	return manifest, snapshot, err
}

// Handler exposes exactly the signed current manifest and its named archives.
// It has no management routes, directory browsing, profile files or credentials.
func Handler(directory string, trusted updates.TrustedKey) (http.Handler, error) {
	if _, err := updates.ParseTrustedKey(trusted); err != nil {
		return nil, err
	}
	if _, _, err := loadCurrent(directory, trusted, true); err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Cache-Control", "no-store")
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writer.Header().Set("Allow", "GET, HEAD")
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if request.URL.RawQuery != "" || request.URL.RawPath != "" || strings.Contains(request.URL.Path, "..") || strings.Contains(request.URL.Path, "\\") {
			http.NotFound(writer, request)
			return
		}
		if strings.HasPrefix(request.URL.Path, "/headers/") {
			headerDelivery(directory, trusted, writer, request)
			return
		}
		manifest, snapshot, err := loadCurrent(directory, trusted, true)
		if err != nil {
			http.Error(writer, "signed update feed unavailable", http.StatusServiceUnavailable)
			return
		}
		var filename string
		var artifact *updates.Artifact
		if request.URL.Path == "/manifest.json" {
			filename = filepath.Join(snapshot, "manifest.json")
			writer.Header().Set("Content-Type", "application/json")
			// Serve the exact bytes reauthenticated from the open read, avoiding
			// a second unverified open after loadCurrent has checked metadata.
			data, err := boundedRegularRead(filename, updates.MaxManifestBytes)
			if err != nil {
				http.Error(writer, "signed update feed unavailable", http.StatusServiceUnavailable)
				return
			}
			if _, err = updates.Verify(data, trusted, updates.Policy{MinimumSequence: manifest.Sequence}); err != nil {
				http.Error(writer, "signed update feed unavailable", http.StatusServiceUnavailable)
				return
			}
			http.ServeContent(writer, request, "manifest.json", time.Time{}, bytes.NewReader(data))
			return
		} else {
			for i := range manifest.Artifacts {
				if request.URL.Path == "/artifacts/"+manifest.Artifacts[i].File {
					artifact = &manifest.Artifacts[i]
					filename = filepath.Join(snapshot, "artifacts", artifact.File)
					break
				}
			}
			if artifact == nil {
				http.NotFound(writer, request)
				return
			}
			writer.Header().Set("Content-Type", "application/zip")
			writer.Header().Set("Content-Disposition", `attachment; filename="`+artifact.File+`"`)
		}
		info, err := os.Lstat(filename)
		if err != nil || !info.Mode().IsRegular() {
			http.Error(writer, "update file unavailable", http.StatusServiceUnavailable)
			return
		}
		file, err := os.Open(filename)
		if err != nil {
			http.Error(writer, "update file unavailable", http.StatusServiceUnavailable)
			return
		}
		defer file.Close()
		if artifact != nil {
			if err := updates.VerifyDigest(file, *artifact); err != nil {
				http.Error(writer, "update artifact failed integrity verification", http.StatusServiceUnavailable)
				return
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				http.Error(writer, "update file unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		http.ServeContent(writer, request, path.Base(filename), info.ModTime(), file)
	}), nil
}

// ValidateListener makes local previews deliberate and production binding safe.
// A non-loopback listener requires TLS at this server or an explicit reverse
// proxy mode; the ordinary client still requires its independently trusted key.
func ValidateListener(address string, tls, reverseProxy bool) error {
	parsed, err := url.Parse("http://" + address)
	if err != nil || parsed.Host != address || parsed.Port() == "" || parsed.Path != "" {
		return errors.New("publisher listen address must be host:port")
	}
	host := parsed.Hostname()
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return nil
	}
	if !tls && !reverseProxy {
		return errors.New("non-loopback publisher requires TLS or explicit trusted reverse proxy mode")
	}
	return nil
}
