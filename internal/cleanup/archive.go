package cleanup

import (
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Archive cleanup recognizes only content-addressed block/receipt pairs with
// Gateway's receipt schema. A configured folder is not blanket ownership.
func archivePair(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	if len(parts) != 3 || parts[0] != "blocks" || len(parts[1]) != 2 {
		return false
	}
	ext := filepath.Ext(parts[2])
	if ext != ".json" && ext != ".blk" {
		return false
	}
	hash := strings.TrimSuffix(parts[2], ext)
	if len(hash) != 64 || parts[1] != hash[:2] {
		return false
	}
	if _, err = hex.DecodeString(hash); err != nil {
		return false
	}
	meta := filepath.Join(root, "blocks", parts[1], hash+".json")
	b, err := readMetadata(meta)
	if err != nil {
		return false
	}
	var r struct {
		Schema   int    `json:"schema"`
		Verifier int    `json:"verifier_version"`
		Hash     string `json:"hash"`
		Bytes    int64  `json:"bytes"`
		SHA      string `json:"sha256"`
		Header   string `json:"header"`
	}
	if json.Unmarshal(b, &r) != nil || r.Schema != 1 || r.Verifier < 1 || r.Hash != hash || r.Bytes < 81 || len(r.SHA) != 64 || len(r.Header) != 160 {
		return false
	}
	if _, err = hex.DecodeString(r.SHA); err != nil {
		return false
	}
	if _, err = hex.DecodeString(r.Header); err != nil {
		return false
	}
	raw := filepath.Join(root, "blocks", parts[1], hash+".blk")
	if checkAncestors(raw) != nil {
		return false
	}
	st, err := os.Lstat(raw)
	return err == nil && st.Mode().IsRegular() && st.Size() == r.Bytes
}
func legacyArchiveOwned(root string) bool {
	found, visited := false, 0
	_ = filepath.WalkDir(filepath.Join(root, "blocks"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fs.SkipAll
		}
		visited++
		if visited > 10000 {
			return fs.SkipAll
		}
		if d.Type()&os.ModeSymlink != 0 || isReparse(path) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(path, ".json") && archivePair(root, path) {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}
