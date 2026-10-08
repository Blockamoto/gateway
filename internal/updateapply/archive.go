package updateapply

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"../updates"
)

func RuntimeName(platform string) string {
	if platform == "windows-amd64" {
		return "GatewayClient.exe"
	}
	if platform == "linux-amd64" {
		return "gateway-client"
	}
	return ""
}
func HelperName(platform string) string {
	if platform == "windows-amd64" {
		return "GatewayUpdateHelper.exe"
	}
	if platform == "linux-amd64" {
		return "gateway-update-helper"
	}
	return ""
}

// ZIP layout intentionally mirrors package-release.py. Installation markers,
// the optional installer and reports are never installed by this engine.
// In particular, portable.marker must never turn an installed profile portable.
func archiveEntryAllowed(name, version, platform string) (allowed, managed bool) {
	if name == RuntimeName(platform) || name == HelperName(platform) {
		return true, true
	}
	if platform == "windows-amd64" {
		switch name {
		case "GatewayOnDemand.exe", "GatewayNativeHost.exe":
			return true, true
		case "portable.marker", "TestStandalone.cmd", "TestFreshStandalone.cmd":
			return true, false
		}
		if name == "gateway-client-v"+version+"-installer.exe" {
			return true, false
		}
		if strings.HasPrefix(name, "browser-companion/") && strings.Count(name, "/") == 1 {
			switch strings.TrimPrefix(name, "browser-companion/") {
			case "content.js", "IDENTITY.txt", "manifest.json", "popup.html", "popup.js", "README.txt", "search.js", "service-worker.js", "test.js":
				return true, true
			}
		}
	}
	switch name {
	case "README.txt", "SOURCE-REVISION.txt", "BINARY-AUDIT.json", "SHA256SUMS.txt", "LICENSE", "THIRD-PARTY-NOTICES.txt", "GO-LICENSE.txt", "GATEWAY-ROADMAP.md", "INDEXING.md", "INDEX-PEER.md", "GATEWAY-BOOTSTRAP.md", "STATUS.md", "BITMAP-RESEARCH.md", "RELEASE-ACCEPTANCE.json", "ZERO-STATE-INDEX-ACCEPTANCE.json", "COMPATIBILITY.json", "UPDATES.md":
		return true, true
	}
	if name == "RELEASE-NOTES-v"+version+".md" || name == "TESTING-"+version+".md" {
		return true, true
	}
	if name == "UPDATE-REVIEW-"+version+".md" || name == "INTEGRATION-"+version+"-INDEX-EXPLORER.md" {
		return true, false
	}
	return false, false
}

type archiveFile struct {
	name    string
	source  *zip.File
	managed bool
}

func managedHashes(packagePath, version, platform string) (map[string]string, error) {
	reader, e := zip.OpenReader(packagePath)
	if e != nil {
		return nil, e
	}
	defer reader.Close()
	files, e := inspectArchive(&reader.Reader, version, platform)
	if e != nil {
		return nil, e
	}
	result := map[string]string{}
	for _, file := range files {
		if !file.managed {
			continue
		}
		src, e := file.source.Open()
		if e != nil {
			return nil, e
		}
		hash := sha256.New()
		n, e := io.Copy(hash, io.LimitReader(src, int64(file.source.UncompressedSize64)+1))
		ce := src.Close()
		if e != nil {
			return nil, e
		}
		if ce != nil {
			return nil, ce
		}
		if n != int64(file.source.UncompressedSize64) {
			return nil, fmt.Errorf("ZIP member length mismatch")
		}
		result[file.name] = hex.EncodeToString(hash.Sum(nil))
	}
	return result, nil
}

func inspectArchive(reader *zip.Reader, version, platform string) ([]archiveFile, error) {
	root := "gateway-client-v" + version + "-" + platform
	if RuntimeName(platform) == "" {
		return nil, fmt.Errorf("unsupported update platform")
	}
	if len(reader.File) > updates.MaxArchiveEntries {
		return nil, fmt.Errorf("too many archive entries")
	}
	seen := map[string]bool{}
	files := []archiveFile{}
	var expanded uint64
	for _, file := range reader.File {
		name := file.Name
		if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
			return nil, fmt.Errorf("unsafe ZIP name: %q", name)
		}
		parts := strings.Split(strings.TrimSuffix(name, "/"), "/")
		if len(parts) < 1 || parts[0] != root {
			return nil, fmt.Errorf("unexpected ZIP root: %q", name)
		}
		for _, part := range parts {
			if !validSegment(part) {
				return nil, fmt.Errorf("unsafe ZIP segment: %q", name)
			}
		}
		folded := strings.ToLower(strings.TrimSuffix(name, "/"))
		if seen[folded] {
			return nil, fmt.Errorf("duplicate ZIP entry: %q", name)
		}
		seen[folded] = true
		mode := file.Mode()
		if mode&os.ModeSymlink != 0 || (!mode.IsRegular() && !mode.IsDir()) {
			return nil, fmt.Errorf("nonregular ZIP entry: %q", name)
		}
		rel := strings.Join(parts[1:], "/")
		if mode.IsDir() {
			if rel != "" && rel != "browser-companion" {
				return nil, fmt.Errorf("unexpected ZIP directory: %q", name)
			}
			continue
		}
		allowed, managed := archiveEntryAllowed(rel, version, platform)
		if !allowed {
			return nil, fmt.Errorf("unexpected update payload: %q", rel)
		}
		if file.UncompressedSize64 > updates.MaxExpandedBytes-expanded {
			return nil, fmt.Errorf("ZIP expanded-size limit exceeded")
		}
		expanded += file.UncompressedSize64
		files = append(files, archiveFile{rel, file, managed})
	}
	for _, required := range []string{RuntimeName(platform), HelperName(platform), "COMPATIBILITY.json"} {
		if !seen[strings.ToLower(root+"/"+required)] {
			return nil, fmt.Errorf("update ZIP missing %s", required)
		}
	}
	if platform == "windows-amd64" {
		for _, required := range []string{"GatewayOnDemand.exe", "GatewayNativeHost.exe", "browser-companion/manifest.json", "browser-companion/IDENTITY.txt"} {
			if !seen[strings.ToLower(root+"/"+required)] {
				return nil, fmt.Errorf("update ZIP missing %s", required)
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return files, nil
}

// ValidateArchive performs layout, entry type, collision, bound and CRC checks.
// Signature and full archive digest verification must precede calling it.
func ValidateArchive(packagePath, version, platform string) error {
	reader, e := zip.OpenReader(packagePath)
	if e != nil {
		return e
	}
	defer reader.Close()
	files, e := inspectArchive(&reader.Reader, version, platform)
	if e != nil {
		return e
	}
	for _, file := range files {
		src, e := file.source.Open()
		if e != nil {
			return e
		}
		n, e := io.Copy(io.Discard, io.LimitReader(src, int64(file.source.UncompressedSize64)+1))
		ce := src.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		if n != int64(file.source.UncompressedSize64) {
			return fmt.Errorf("incorrect ZIP length: %s", file.name)
		}
	}
	return nil
}

func extractArchive(packagePath, destination, version, platform string, artifact updates.Artifact) ([]string, map[string]string, error) {
	// Keep a single open archive descriptor for authentication and extraction.
	if e := regular(packagePath, updates.MaxArtifactBytes); e != nil {
		return nil, nil, e
	}
	archive, e := os.Open(packagePath)
	if e != nil {
		return nil, nil, e
	}
	defer archive.Close()
	h := sha256.New()
	length, e := io.Copy(h, io.LimitReader(archive, updates.MaxArtifactBytes+1))
	if e != nil {
		return nil, nil, e
	}
	if length != artifact.Bytes || hex.EncodeToString(h.Sum(nil)) != artifact.SHA256 {
		return nil, nil, fmt.Errorf("update archive changed before extraction")
	}
	reader, e := zip.NewReader(archive, length)
	if e != nil {
		return nil, nil, e
	}
	files, e := inspectArchive(reader, version, platform)
	if e != nil {
		return nil, nil, e
	}
	names := []string{}
	hashes := map[string]string{}
	for _, file := range files {
		// Check every member's CRC, including reports which are not installed.
		src, e := file.source.Open()
		if e != nil {
			return nil, nil, e
		}
		var writer io.Writer = io.Discard
		var dst *os.File
		if file.managed {
			path := filepath.Join(destination, filepath.FromSlash(file.name))
			if !within(destination, path) {
				src.Close()
				return nil, nil, fmt.Errorf("ZIP escape refused")
			}
			if e = CheckPath(path); e != nil {
				src.Close()
				return nil, nil, e
			}
			if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
				src.Close()
				return nil, nil, e
			}
			mode := os.FileMode(0644)
			if file.name == RuntimeName(platform) || file.name == HelperName(platform) || strings.HasSuffix(file.name, ".exe") {
				mode = 0755
			}
			dst, e = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if e != nil {
				src.Close()
				return nil, nil, e
			}
			writer = dst
		}
		hash := sha256.New()
		n, e := io.Copy(io.MultiWriter(writer, hash), io.LimitReader(src, int64(file.source.UncompressedSize64)+1))
		ce := src.Close()
		if dst != nil {
			if e == nil {
				e = dst.Sync()
			}
			de := dst.Close()
			if e == nil {
				e = de
			}
		}
		if e != nil {
			return nil, nil, e
		}
		if ce != nil {
			return nil, nil, ce
		}
		if n != int64(file.source.UncompressedSize64) {
			return nil, nil, fmt.Errorf("incorrect ZIP length: %s", file.name)
		}
		if file.managed {
			names = append(names, file.name)
			hashes[file.name] = hex.EncodeToString(hash.Sum(nil))
		}
	}
	// Catch in-place archive mutation while the descriptor was being read.
	if _, e = archive.Seek(0, io.SeekStart); e != nil {
		return nil, nil, e
	}
	h.Reset()
	length, e = io.Copy(h, io.LimitReader(archive, updates.MaxArtifactBytes+1))
	if e != nil {
		return nil, nil, e
	}
	if length != artifact.Bytes || hex.EncodeToString(h.Sum(nil)) != artifact.SHA256 {
		return nil, nil, fmt.Errorf("update archive changed during extraction")
	}
	return names, hashes, nil
}
