package updatepublisher

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"../updates"
)

// HeaderSnapshot contains only public Bitcoin evidence from an explicitly
// selected release. The client still applies the full ordinary header checks.
type HeaderSnapshot struct {
	Raw           []byte
	SourceRelease string
}

func ReadHeaderSnapshotArchive(raw []byte, source string) (HeaderSnapshot, error) {
	z, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil {
		return HeaderSnapshot{}, e
	}
	if len(z.File) != 2 {
		return HeaderSnapshot{}, fmt.Errorf("header snapshot must contain exactly two files")
	}
	values := map[string][]byte{}
	for _, f := range z.File {
		limit := int64(4096)
		if f.Name == "headers-mainnet.bin" {
			limit = updates.MaxHeaderCount * 80
		} else if f.Name != "headers-mainnet.json" {
			return HeaderSnapshot{}, fmt.Errorf("unexpected header snapshot file")
		}
		if values[f.Name] != nil || !f.Mode().IsRegular() || f.UncompressedSize64 > uint64(limit) {
			return HeaderSnapshot{}, fmt.Errorf("invalid header snapshot entry")
		}
		r, e := f.Open()
		if e != nil {
			return HeaderSnapshot{}, e
		}
		b, e := io.ReadAll(io.LimitReader(r, limit+1))
		r.Close()
		if e != nil || int64(len(b)) > limit {
			return HeaderSnapshot{}, fmt.Errorf("invalid header snapshot size")
		}
		values[f.Name] = b
	}
	var m struct {
		Schema    int    `json:"schema"`
		Network   string `json:"network"`
		Count     int64  `json:"count"`
		SHA256    string `json:"sha256"`
		TipHash   string `json:"tip_hash"`
		CreatedAt string `json:"created_at"`
	}
	if e = decodeJSON(values["headers-mainnet.json"], &m); e != nil {
		return HeaderSnapshot{}, e
	}
	data := values["headers-mainnet.bin"]
	digest := sha256.Sum256(data)
	if m.Schema != 1 || m.Network != "mainnet" || m.Count < 1 || m.Count > updates.MaxHeaderCount || int64(len(data)) != m.Count*80 || hex.EncodeToString(digest[:]) != m.SHA256 || updates.HeaderHash(data[len(data)-80:]) != m.TipHash {
		return HeaderSnapshot{}, fmt.Errorf("header snapshot metadata mismatch")
	}
	if _, e = time.Parse(time.RFC3339, m.CreatedAt); e != nil {
		return HeaderSnapshot{}, e
	}
	return HeaderSnapshot{data, source}, nil
}

func FetchHeaderSnapshotGitHub(ctx context.Context, repository, version, token string) (HeaderSnapshot, error) {
	return fetchHeaderSnapshot(ctx, repository, version, token, "https://api.github.com", githubClient())
}
func fetchHeaderSnapshot(ctx context.Context, repository, version, token, api string, client *http.Client) (HeaderSnapshot, error) {
	var empty HeaderSnapshot
	if !validRepository(repository) || strings.TrimSpace(token) == "" {
		return empty, fmt.Errorf("approved repository and server-only GitHub credential required")
	}
	if _, e := updates.CompareVersions(version, "0.0.0"); e != nil {
		return empty, e
	}
	base := api + "/repos/" + repository
	raw, e := originGET(ctx, client, base+"/releases/tags/v"+version, token, "application/vnd.github+json", maxOriginMetadata)
	if e != nil {
		return empty, e
	}
	var release githubRelease
	if e = json.Unmarshal(raw, &release); e != nil {
		return empty, e
	}
	source := "https://github.com/" + repository + "/releases/tag/v" + version
	if release.Draft || release.TagName != "v"+version || release.HTMLURL != source {
		return empty, fmt.Errorf("header origin must be the approved published release")
	}
	assets := map[string]githubAsset{}
	for _, a := range release.Assets {
		if _, ok := assets[a.Name]; ok {
			return empty, fmt.Errorf("duplicate release asset")
		}
		assets[a.Name] = a
	}
	meta, ok := assets["artifact-manifest.json"]
	if !ok || meta.ID < 1 || meta.Size < 1 || meta.Size > maxOriginMetadata {
		return empty, fmt.Errorf("missing header origin manifest")
	}
	raw, e = originGET(ctx, client, fmt.Sprintf("%s/releases/assets/%d", base, meta.ID), token, "application/octet-stream", maxOriginMetadata)
	if e != nil {
		return empty, e
	}
	if int64(len(raw)) != meta.Size {
		return empty, fmt.Errorf("origin manifest size mismatch")
	}
	if e = verifyGitHubDigest(raw, meta.Digest); e != nil {
		return empty, e
	}
	var m OriginManifest
	if e = decodeJSON(raw, &m); e != nil {
		return empty, e
	}
	if m.Version != version || len(m.Artifacts) > 16 {
		return empty, fmt.Errorf("origin manifest identity mismatch")
	}
	if e = verifyOriginTagRepository(ctx, client, api, repository, version, token, m.SourceRevision); e != nil {
		return empty, e
	}
	name := "gateway-headers-v" + version + ".zip"
	a, ok := assets[name]
	if !ok || a.ID < 1 {
		return empty, fmt.Errorf("missing release header snapshot")
	}
	var expected OriginArtifact
	seen := map[string]bool{}
	for _, item := range m.Artifacts {
		if seen[item.File] {
			return empty, fmt.Errorf("duplicate origin digest")
		}
		seen[item.File] = true
		if item.File == name {
			expected = item
		}
	}
	if expected.Bytes < 1 || expected.Bytes > updates.MaxArtifactBytes || expected.Bytes != a.Size {
		return empty, fmt.Errorf("invalid header origin size")
	}
	raw, e = originGET(ctx, client, fmt.Sprintf("%s/releases/assets/%d", base, a.ID), token, "application/octet-stream", expected.Bytes)
	if e != nil {
		return empty, e
	}
	digest := sha256.Sum256(raw)
	if int64(len(raw)) != expected.Bytes || hex.EncodeToString(digest[:]) != expected.SHA256 {
		return empty, fmt.Errorf("header origin digest mismatch")
	}
	if e = verifyGitHubDigest(raw, a.Digest); e != nil {
		return empty, e
	}
	return ReadHeaderSnapshotArchive(raw, source)
}

func PublishHeaders(directory string, snapshot HeaderSnapshot, sequence uint64, validity time.Duration, private ed25519.PrivateKey) (updates.HeaderManifest, error) {
	var empty updates.HeaderManifest
	if len(private) != ed25519.PrivateKeySize || len(snapshot.Raw) < 80 || len(snapshot.Raw)%80 != 0 || int64(len(snapshot.Raw)) > updates.MaxHeaderCount*80 {
		return empty, fmt.Errorf("invalid header snapshot or signing key")
	}
	if e := checkPublisherPath(directory); e != nil {
		return empty, e
	}
	artifacts := filepath.Join(directory, "header-chunks")
	if e := checkPublisherPath(artifacts); e != nil {
		return empty, e
	}
	if e := os.MkdirAll(artifacts, 0700); e != nil {
		return empty, e
	}
	lockPath := filepath.Join(directory, ".headers-publish-lock")
	lock, e := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return empty, fmt.Errorf("header publisher lock exists")
	}
	lock.Close()
	defer os.Remove(lockPath)
	now := time.Now().UTC().Truncate(time.Second)
	m := updates.HeaderManifest{Schema: 1, Network: "mainnet", Sequence: sequence, IssuedAt: now.Format(time.RFC3339), ExpiresAt: now.Add(validity).Format(time.RFC3339), SourceRelease: snapshot.SourceRelease, Count: int64(len(snapshot.Raw) / 80), TipHash: updates.HeaderHash(snapshot.Raw[len(snapshot.Raw)-80:])}
	for from := int64(0); from < m.Count; from += updates.HeaderChunkCount {
		end := from + updates.HeaderChunkCount
		if end > m.Count {
			end = m.Count
		}
		raw := snapshot.Raw[from*80 : end*80]
		var buffer bytes.Buffer
		writer := gzip.NewWriter(&buffer)
		if _, e = writer.Write(raw); e != nil {
			return empty, e
		}
		if e = writer.Close(); e != nil {
			return empty, e
		}
		compressed := buffer.Bytes()
		digest := sha256.Sum256(compressed)
		previous := fmt.Sprintf("%064d", 0)
		if from > 0 {
			previous = updates.HeaderHash(snapshot.Raw[(from-1)*80 : from*80])
		}
		c := updates.HeaderChunk{From: from, To: end - 1, PreviousHash: previous, TipHash: updates.HeaderHash(raw[len(raw)-80:]), Bytes: int64(len(compressed)), SHA256: hex.EncodeToString(digest[:])}
		c.File = fmt.Sprintf("headers-%d-%d-%s.gz", c.From, c.To, c.SHA256)
		if _, e = updates.DecodeHeaderChunk(compressed, c); e != nil {
			return empty, e
		}
		path := filepath.Join(artifacts, c.File)
		if existing, readErr := boundedRegularRead(path, updates.MaxHeaderChunkBytes); readErr == nil {
			if !bytes.Equal(existing, compressed) {
				return empty, fmt.Errorf("immutable header chunk changed")
			}
		} else if os.IsNotExist(readErr) {
			if e = exclusiveWrite(path, compressed, 0644); e != nil {
				return empty, e
			}
		} else {
			return empty, readErr
		}
		m.Chunks = append(m.Chunks, c)
	}
	if e = updates.ValidateHeaderManifest(m, now); e != nil {
		return empty, e
	}
	manifestPath := filepath.Join(directory, "headers-manifest.json")
	if old, e := boundedRegularRead(manifestPath, updates.MaxHeaderManifestBytes); e == nil {
		// Re-authenticate expired metadata at its own issued time before renewal.
		// The verification time is extracted only to check the old signature and
		// monotonic sequence, never to allow expired metadata to reach a client.
		oldManifest, e := verifyExpiredHeaderManifest(old, updates.NewTrustedKey(private.Public().(ed25519.PublicKey)))
		if e != nil {
			return empty, e
		}
		if sequence <= oldManifest.Sequence {
			return empty, fmt.Errorf("header sequence must strictly increase")
		}
		if oldManifest.SourceRelease == m.SourceRelease {
			oldBytes, _ := json.Marshal(oldManifest.Chunks)
			newBytes, _ := json.Marshal(m.Chunks)
			if oldManifest.Count != m.Count || oldManifest.TipHash != m.TipHash || !bytes.Equal(oldBytes, newBytes) {
				return empty, fmt.Errorf("header renewal cannot change a released snapshot")
			}
		}
	} else if !os.IsNotExist(e) {
		return empty, e
	}
	envelope, e := updates.SignHeaders(m, private)
	if e != nil {
		return empty, e
	}
	temp, e := os.CreateTemp(directory, ".headers-manifest-")
	if e != nil {
		return empty, e
	}
	path := temp.Name()
	defer os.Remove(path)
	_, e = temp.Write(envelope)
	if e == nil {
		e = temp.Sync()
	}
	closeErr := temp.Close()
	if e != nil {
		return empty, e
	}
	if closeErr != nil {
		return empty, closeErr
	}
	if e = os.Rename(path, manifestPath); e != nil {
		return empty, e
	}
	return m, nil
}

func verifyExpiredHeaderManifest(raw []byte, key updates.TrustedKey) (updates.HeaderManifest, error) {
	var envelope updates.Envelope
	if e := json.Unmarshal(raw, &envelope); e != nil {
		return updates.HeaderManifest{}, e
	}
	payload, e := base64.StdEncoding.Strict().DecodeString(envelope.Payload)
	if e != nil {
		return updates.HeaderManifest{}, e
	}
	var hint updates.HeaderManifest
	if e = json.Unmarshal(payload, &hint); e != nil {
		return hint, e
	}
	issued, e := time.Parse(time.RFC3339, hint.IssuedAt)
	if e != nil {
		return hint, e
	}
	return updates.VerifyHeaders(raw, key, 0, issued)
}

func headerDelivery(directory string, key updates.TrustedKey, w http.ResponseWriter, r *http.Request) {
	raw, e := boundedRegularRead(filepath.Join(directory, "headers-manifest.json"), updates.MaxHeaderManifestBytes)
	if e != nil {
		http.Error(w, "header feed unavailable", 503)
		return
	}
	m, e := updates.VerifyHeaders(raw, key, 0, time.Now())
	if e != nil {
		http.Error(w, "header feed unavailable", 503)
		return
	}
	if r.URL.Path == "/headers/manifest.json" {
		w.Header().Set("Content-Type", "application/json")
		http.ServeContent(w, r, "manifest.json", time.Time{}, bytes.NewReader(raw))
		return
	}
	for _, c := range m.Chunks {
		if r.URL.Path != "/headers/chunks/"+c.File {
			continue
		}
		data, e := boundedRegularRead(filepath.Join(directory, "header-chunks", c.File), updates.MaxHeaderChunkBytes)
		if e == nil {
			_, e = updates.DecodeHeaderChunk(data, c)
		}
		if e != nil {
			http.Error(w, "header chunk unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		http.ServeContent(w, r, c.File, time.Time{}, bytes.NewReader(data))
		return
	}
	http.NotFound(w, r)
}
