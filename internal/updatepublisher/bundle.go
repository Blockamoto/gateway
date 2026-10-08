package updatepublisher

import (
	"../updates"
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// ExportDistribution produces a public, already signed immutable serving input.
// The free-tier server only restores/verifies this; it never receives a signer.
func ExportDistribution(directory, output string, key updates.TrustedKey) error {
	m, snapshot, e := loadCurrent(directory, key, true)
	if e != nil {
		return e
	}
	files := map[string]string{"manifest.json": filepath.Join(snapshot, "manifest.json")}
	for _, a := range m.Artifacts {
		path := filepath.Join(snapshot, "artifacts", a.File)
		raw, e := boundedRegularRead(path, updates.MaxArtifactBytes)
		if e != nil {
			return e
		}
		if e = updates.VerifyDigest(bytes.NewReader(raw), a); e != nil {
			return e
		}
		files["artifacts/"+a.File] = path
	}
	headerPath := filepath.Join(directory, "headers-manifest.json")
	if raw, e := boundedRegularRead(headerPath, updates.MaxHeaderManifestBytes); e == nil {
		h, e := updates.VerifyHeaders(raw, key, 0, time.Now())
		if e != nil {
			return e
		}
		files["headers-manifest.json"] = headerPath
		for _, c := range h.Chunks {
			path := filepath.Join(directory, "header-chunks", c.File)
			b, e := boundedRegularRead(path, updates.MaxHeaderChunkBytes)
			if e != nil {
				return e
			}
			if _, e = updates.DecodeHeaderChunk(b, c); e != nil {
				return e
			}
			files["header-chunks/"+c.File] = path
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if e = checkPublisherPath(output); e != nil {
		return e
	}
	f, e := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if e != nil {
		return e
	}
	z := zip.NewWriter(f)
	success := false
	defer func() {
		z.Close()
		f.Close()
		if !success {
			os.Remove(output)
		}
	}()
	for name, path := range files {
		entry, e := z.Create(name)
		if e != nil {
			return e
		}
		source, e := os.Open(path)
		if e != nil {
			return e
		}
		_, e = io.Copy(entry, source)
		source.Close()
		if e != nil {
			return e
		}
	}
	if e = z.Close(); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if info, e := f.Stat(); e != nil {
		return e
	} else if info.Size() > updates.MaxArtifactBytes {
		return fmt.Errorf("distribution bundle exceeds supported download bound")
	}
	success = true
	return nil
}

func RestoreDistribution(raw []byte, directory, version, repository string, key updates.TrustedKey) error {
	return restoreDistribution(raw, directory, version, repository, key, true)
}

// RestoreDistributionForRenewal is an operator-only recovery path. It still
// authenticates every signature and package, but permits expired metadata so an
// approved release can be renewed after downtime. Serving, checking and export
// keep their ordinary expiry checks; this operation alone never makes it live.
func RestoreDistributionForRenewal(raw []byte, directory, version, repository string, key updates.TrustedKey) error {
	return restoreDistribution(raw, directory, version, repository, key, false)
}

func restoreDistribution(raw []byte, directory, version, repository string, key updates.TrustedKey, requireUnexpired bool) error {
	if len(raw) == 0 || len(raw) > int(updates.MaxArtifactBytes) || !validRepository(repository) {
		return fmt.Errorf("invalid distribution bundle bounds or repository")
	}
	z, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil {
		return e
	}
	if len(z.File) > updates.MaxArchiveEntries {
		return fmt.Errorf("too many bundle entries")
	}
	entries := map[string]*zip.File{}
	var expanded uint64
	for _, f := range z.File {
		if entries[f.Name] != nil || !f.Mode().IsRegular() {
			return fmt.Errorf("duplicate or non-regular bundle entry")
		}
		if f.UncompressedSize64 > updates.MaxExpandedBytes-expanded {
			return fmt.Errorf("bundle expansion too large")
		}
		expanded += f.UncompressedSize64
		entries[f.Name] = f
	}
	read := func(name string, limit int64) ([]byte, error) {
		f := entries[name]
		if f == nil || f.UncompressedSize64 > uint64(limit) {
			return nil, fmt.Errorf("missing or oversized bundle entry %s", name)
		}
		r, e := f.Open()
		if e != nil {
			return nil, e
		}
		defer r.Close()
		b, e := io.ReadAll(io.LimitReader(r, limit+1))
		if e != nil || int64(len(b)) > limit {
			return nil, fmt.Errorf("invalid bundle entry")
		}
		return b, nil
	}
	envelope, e := read("manifest.json", updates.MaxManifestBytes)
	if e != nil {
		return e
	}
	m, e := verifyApplicationManifest(envelope, key, requireUnexpired)
	if e != nil {
		return e
	}
	if m.Version != version || m.ReleaseURL != "https://github.com/"+repository+"/releases/tag/v"+version {
		return fmt.Errorf("bundle differs from explicitly selected release")
	}
	allowed := map[string]bool{"manifest.json": true}
	for _, a := range m.Artifacts {
		allowed["artifacts/"+a.File] = true
	}
	var header updates.HeaderManifest
	if entries["headers-manifest.json"] != nil {
		b, e := read("headers-manifest.json", updates.MaxHeaderManifestBytes)
		if e != nil {
			return e
		}
		header, e = verifyHeaderManifest(b, key, requireUnexpired)
		if e != nil {
			return e
		}
		if header.SourceRelease != m.ReleaseURL {
			return fmt.Errorf("header and application feeds must name the same approved release")
		}
		allowed["headers-manifest.json"] = true
		for _, c := range header.Chunks {
			allowed["header-chunks/"+c.File] = true
		}
	}
	for name := range entries {
		if !allowed[name] {
			return fmt.Errorf("unapproved distribution bundle entry")
		}
	}
	// Prepare in a sibling directory and reveal only after every signature, byte
	// count and digest passes. A live destination is never overwritten.
	if e = checkPublisherPath(directory); e != nil {
		return e
	}
	if _, e = os.Lstat(directory); !os.IsNotExist(e) {
		return fmt.Errorf("distribution restore requires a new directory")
	}
	parent := filepath.Dir(directory)
	if e = os.MkdirAll(parent, 0700); e != nil {
		return e
	}
	staging, e := os.MkdirTemp(parent, ".feed-restore-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(staging)
	digest := sha256.Sum256(envelope)
	snapshot := fmt.Sprintf("%d-v%s-%s", m.Sequence, m.Version, hex.EncodeToString(digest[:8]))
	root := filepath.Join(staging, "snapshots", snapshot)
	if e = os.MkdirAll(filepath.Join(root, "artifacts"), 0700); e != nil {
		return e
	}
	if e = exclusiveWrite(filepath.Join(root, "manifest.json"), envelope, 0644); e != nil {
		return e
	}
	for _, a := range m.Artifacts {
		b, e := read("artifacts/"+a.File, a.Bytes)
		if e != nil {
			return e
		}
		if e = updates.VerifyDigest(bytes.NewReader(b), a); e != nil {
			return e
		}
		path := filepath.Join(root, "artifacts", a.File)
		if e = exclusiveWrite(path, b, 0644); e != nil {
			return e
		}
		if e = ValidateArchive(path, m.Version, a.Platform, m.SourceRevision); e != nil {
			return e
		}
	}
	if header.Schema != 0 {
		if e = os.Mkdir(filepath.Join(staging, "header-chunks"), 0700); e != nil {
			return e
		}
		for _, c := range header.Chunks {
			b, e := read("header-chunks/"+c.File, c.Bytes)
			if e != nil {
				return e
			}
			if _, e = updates.DecodeHeaderChunk(b, c); e != nil {
				return e
			}
			if e = exclusiveWrite(filepath.Join(staging, "header-chunks", c.File), b, 0644); e != nil {
				return e
			}
		}
		b, e := read("headers-manifest.json", updates.MaxHeaderManifestBytes)
		if e != nil {
			return e
		}
		if e = exclusiveWrite(filepath.Join(staging, "headers-manifest.json"), b, 0644); e != nil {
			return e
		}
	}
	pointerData, _ := json.Marshal(pointer{Snapshot: snapshot})
	if e = exclusiveWrite(filepath.Join(staging, currentFile), pointerData, 0644); e != nil {
		return e
	}
	return os.Rename(staging, directory)
}

func RestoreGitHubDistribution(ctx context.Context, repository, version, token, directory string, key updates.TrustedKey) error {
	if !validRepository(repository) {
		return fmt.Errorf("unsupported repository")
	}
	if _, e := updates.CompareVersions(version, "0.0.0"); e != nil {
		return e
	}
	base := "https://api.github.com/repos/" + repository
	client := githubClient()
	raw, e := originGET(ctx, client, base+"/releases/tags/v"+version, token, "application/vnd.github+json", maxOriginMetadata)
	if e != nil {
		return e
	}
	var release githubRelease
	if e = json.Unmarshal(raw, &release); e != nil {
		return e
	}
	if release.Draft || release.TagName != "v"+version || release.HTMLURL != "https://github.com/"+repository+"/releases/tag/v"+version {
		return fmt.Errorf("selected distribution release mismatch")
	}
	name := "gateway-update-feed-v" + version + ".zip"
	var asset *githubAsset
	for i := range release.Assets {
		if release.Assets[i].Name == name {
			if asset != nil {
				return fmt.Errorf("duplicate distribution bundle")
			}
			asset = &release.Assets[i]
		}
	}
	if asset == nil || asset.ID < 1 || asset.Size < 1 || asset.Size > updates.MaxArtifactBytes {
		return fmt.Errorf("approved signed distribution bundle missing")
	}
	raw, e = originGET(ctx, client, fmt.Sprintf("%s/releases/assets/%d", base, asset.ID), token, "application/octet-stream", asset.Size)
	if e != nil {
		return e
	}
	if int64(len(raw)) != asset.Size {
		return fmt.Errorf("bundle size mismatch")
	}
	if e = verifyGitHubDigest(raw, asset.Digest); e != nil {
		return e
	}
	return RestoreDistribution(raw, directory, version, repository, key)
}
