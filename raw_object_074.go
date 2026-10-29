package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Retention names are independent, but newly written Gateway-owned raw bodies
// share one allocation when the profile filesystem supports hard links. The
// source is verified and flushed before linking; every replacement is atomic,
// so a cache repair cannot alter the durable inode through an existing link.
// Existing valid names are never removed or replaced merely to deduplicate.
func writeSharedRawObject(root, path string, target blockTarget, raw []byte) error {
	return writeSharedRawObjectWithLink(root, path, target, raw, os.Link)
}

func writeSharedRawObjectWithLink(root, path string, target blockTarget, raw []byte, link func(string, string) error) error {
	if len(raw) < 81 || len(raw) > maxMessageSize {
		return fmt.Errorf("invalid shared raw block length")
	}
	if existing, _, err := readRetainedIndexBlock(path, target); err == nil && bytes.Equal(existing, raw) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	durable, err := indexedRawBodyPath(root, target.HashDisplay)
	if err != nil {
		return err
	}
	candidates := []string{
		durable,
		filepath.Join(root, "indexes", "sources", target.HashDisplay+".block"),
		filepath.Join(root, "blocks", "raw", fmt.Sprintf("%d-%s.block", target.Height, target.HashDisplay)),
		filepath.Join(root, "blocks", "raw", "hash-"+target.HashDisplay+".block"),
	}
	for _, source := range candidates {
		if filepath.Clean(source) == filepath.Clean(path) || !ownedRegularRawObject(root, source) {
			continue
		}
		verified, _, err := readRetainedIndexBlock(source, target)
		if err != nil || !bytes.Equal(verified, raw) {
			continue
		}
		if linked, err := linkVerifiedRawObject(source, path, target, raw, link); err != nil {
			return err
		} else if linked {
			return nil
		}
	}
	// FAT/exFAT, some remote filesystems, read-only sources, or a concurrent
	// cache unlink may prevent a hard link. Durability takes priority: retain
	// an independent raw copy rather than depend on a pruneable provider.
	return atomicWriteBytes(path, raw)
}

// Only reuse real files owned by this profile. In particular, an externally
// mounted Core file or symlink must never become a mutable shared raw object.
func ownedRegularRawObject(root, path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func linkVerifiedRawObject(source, path string, target blockTarget, raw []byte, link func(string, string) error) (bool, error) {
	// Flush previously cached files too; linking alone does not make their
	// bytes durable. Failure falls back to an independently flushed copy.
	f, err := os.OpenFile(source, os.O_RDWR, 0)
	if err != nil {
		return false, nil
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return false, nil
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".gateway-raw-link-*.tmp")
	if err != nil {
		return false, err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err = temp.Close(); err != nil {
		return false, err
	}
	if err = os.Remove(name); err != nil {
		return false, err
	}
	if err = link(source, name); err != nil {
		return false, nil
	}
	verified, _, err := readRetainedIndexBlock(name, target)
	if err != nil || !bytes.Equal(verified, raw) {
		return false, nil
	}
	if err = os.Rename(name, path); err != nil {
		return false, err
	}
	return true, syncDirectory(filepath.Dir(path))
}
