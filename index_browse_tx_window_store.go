package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"./internal/updateapply"
)

const browseTXWindowBytes = 128 << 10

type indexBrowseTXPointWindow struct {
	Schema int                  `json:"schema"`
	Window int64                `json:"window"`
	Points []indexBrowseTXPoint `json:"points"`
	Digest string               `json:"digest"`
}
type indexBrowseTXWindow = indexBrowseTXPointWindow

func browseTXPointWindowDigest(w indexBrowseTXPointWindow) string {
	w.Digest = ""
	return indexDigest(w)
}
func browseTXWindowDigest(w indexBrowseTXPointWindow) string { return browseTXPointWindowDigest(w) }
func browseTXPointWindowPath(root string, height int64) string {
	base := height / indexLocatorBatchBlocks * indexLocatorBatchBlocks
	return filepath.Join(root, "indexes", "browse", "tx-locator", "points-v3", fmt.Sprintf("%02x", uint64(base/indexLocatorBatchBlocks)&255), strconv.FormatInt(base, 10)+".json")
}
func browseTXWindowPath(root string, height int64) string {
	return browseTXPointWindowPath(root, height)
}

func readBrowseTXSmallJSON(ctx context.Context, path string, limit int64, target any) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := updateapply.CheckPath(path); err != nil {
		return false, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > limit {
		return false, fmt.Errorf("invalid TX point metadata extent")
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return false, err
	}
	if int64(len(raw)) > limit {
		return false, fmt.Errorf("TX point metadata exceeds budget")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return false, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return false, fmt.Errorf("invalid trailing TX point metadata")
	}
	return true, ctx.Err()
}
func validateBrowseTXPointWindow(w indexBrowseTXPointWindow, base int64) error {
	if w.Schema != 3 || w.Window != base || base < 0 || base%indexLocatorBatchBlocks != 0 || len(w.Points) > int(indexLocatorBatchBlocks) || w.Digest != browseTXPointWindowDigest(w) {
		return fmt.Errorf("invalid TX point window identity/checksum")
	}
	prior := int64(-1)
	for _, p := range w.Points {
		if p.Schema != 2 || p.Height < base || p.Height >= base+indexLocatorBatchBlocks || p.Height <= prior || !validHash(p.BlockHash) || !validHash(p.Commitment) || p.Digest != browseTXPointDigest(p) {
			return fmt.Errorf("invalid TX point window member")
		}
		prior = p.Height
	}
	return nil
}
func readBrowseTXPointWindow(ctx context.Context, root string, height int64) (indexBrowseTXPointWindow, bool, error) {
	var w indexBrowseTXPointWindow
	if height < 0 {
		return w, false, fmt.Errorf("invalid TX point window height")
	}
	have, err := readBrowseTXSmallJSON(ctx, browseTXPointWindowPath(root, height), browseTXWindowBytes, &w)
	if err != nil || !have {
		return w, have, err
	}
	err = validateBrowseTXPointWindow(w, height/indexLocatorBatchBlocks*indexLocatorBatchBlocks)
	return w, err == nil, err
}
func readBrowseTXWindow(ctx context.Context, root string, height int64) (indexBrowseTXPointWindow, bool, error) {
	return readBrowseTXPointWindow(ctx, root, height)
}
func readBrowseTXPoint(ctx context.Context, root string, height int64) (indexBrowseTXPoint, bool, error) {
	w, have, err := readBrowseTXPointWindow(ctx, root, height)
	if err != nil {
		return indexBrowseTXPoint{}, false, err
	}
	if have {
		for _, p := range w.Points {
			if p.Height == height {
				return p, true, nil
			}
		}
	}
	return readBrowseTXPointLeaf(ctx, root, height)
}

// Enumerate fixed-size chunks. A caller may skip a retired window by its
// canonical path before reading its payload; physical data enumeration never
// supplies that predicate and therefore retains every saved member.
func walkBrowseTXPointWindowsFiltered(ctx context.Context, root string, skip func(int64) bool, visit func(indexBrowseTXPointWindow) error) error {
	dir := filepath.Join(root, "indexes", "browse", "tx-locator", "points-v3")
	if err := updateapply.CheckPath(dir); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer d.Close()
	remaining := indexBrowseBudget
	for {
		shards, readErr := d.ReadDir(128)
		for _, shard := range shards {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, err := strconv.ParseUint(shard.Name(), 16, 8)
			if err != nil || fmt.Sprintf("%02x", n) != shard.Name() || !shard.IsDir() || shard.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("invalid TX point window shard")
			}
			folder := filepath.Join(dir, shard.Name())
			if err := updateapply.CheckPath(folder); err != nil {
				return err
			}
			f, err := os.Open(folder)
			if err != nil {
				return err
			}
			err = func() error {
				defer f.Close()
				for {
					files, readErr := f.ReadDir(128)
					for _, entry := range files {
						if err := ctx.Err(); err != nil {
							return err
						}
						if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
							return fmt.Errorf("TX point window path is redirected")
						}
						if !strings.HasSuffix(entry.Name(), ".json") {
							continue
						}
						raw := strings.TrimSuffix(entry.Name(), ".json")
						base, err := strconv.ParseInt(raw, 10, 64)
						if err != nil || base < 0 || base%indexLocatorBatchBlocks != 0 || strconv.FormatInt(base, 10) != raw || filepath.Join(folder, entry.Name()) != browseTXPointWindowPath(root, base) {
							return fmt.Errorf("invalid TX point window path")
						}
						if skip != nil && skip(base) {
							continue
						}
						info, err := entry.Info()
						if err != nil {
							return err
						}
						if info.Size() > remaining {
							return fmt.Errorf("TX point window projection exceeds budget")
						}
						remaining -= info.Size()
						w, have, err := readBrowseTXPointWindow(ctx, root, base)
						if err != nil {
							return err
						}
						if !have {
							return fmt.Errorf("TX point window changed during enumeration")
						}
						if err := visit(w); err != nil {
							return err
						}
					}
					if readErr == io.EOF {
						return nil
					}
					if readErr != nil {
						return readErr
					}
				}
			}()
			if err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
func walkBrowseTXPointWindows(ctx context.Context, root string, visit func(indexBrowseTXPointWindow) error) error {
	return walkBrowseTXPointWindowsFiltered(ctx, root, nil, visit)
}
func walkBrowseTXWindows(ctx context.Context, root string, visit func(indexBrowseTXPointWindow) error) error {
	return walkBrowseTXPointWindows(ctx, root, visit)
}
func walkBrowseTXPoints(ctx context.Context, root string, visit func(indexBrowseTXPoint) error) error {
	seen := map[int64]bool{}
	if err := walkBrowseTXPointWindows(ctx, root, func(w indexBrowseTXPointWindow) error {
		for _, p := range w.Points {
			if err := visit(p); err != nil {
				return err
			}
			seen[p.Height] = true
		}
		return nil
	}); err != nil {
		return err
	}
	return walkBrowseTXPointLeaves(ctx, root, func(p indexBrowseTXPoint) error {
		if !seen[p.Height] {
			return visit(p)
		}
		return nil
	})
}
func writeBrowseTXPointWindow(ctx context.Context, s *indexStore, w indexBrowseTXPointWindow) error {
	sort.Slice(w.Points, func(i, j int) bool { return w.Points[i].Height < w.Points[j].Height })
	w.Schema = 3
	w.Digest = browseTXPointWindowDigest(w)
	if err := validateBrowseTXPointWindow(w, w.Window); err != nil {
		return err
	}
	raw, err := json.Marshal(w)
	if err != nil {
		return err
	}
	if len(raw) > browseTXWindowBytes {
		return fmt.Errorf("TX point window exceeds budget")
	}
	path := browseTXPointWindowPath(s.profileRoot, w.Window)
	if err := s.makeLocatorDirectories(filepath.Dir(path)); err != nil {
		return err
	}
	if err := checkTimelineTrackArmed(ctx, s.profileRoot, "tx-locator"); err != nil {
		return err
	}
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	if err := atomicWriteBytes(path, raw); err != nil {
		return err
	}
	return s.syncLocatorDirectory(filepath.Dir(path))
}
func mergeBrowseTXWindowPoint(w *indexBrowseTXPointWindow, p indexBrowseTXPoint) {
	for i, prior := range w.Points {
		if prior.Height == p.Height {
			if prior.Commitment == p.Commitment && prior.Packed {
				p.Packed = true
				p.Digest = browseTXPointDigest(p)
			}
			w.Points[i] = p
			return
		}
	}
	w.Points = append(w.Points, p)
}
