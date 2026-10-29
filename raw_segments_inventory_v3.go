package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"./internal/updateapply"
)

type rawSegmentInventoryRow struct {
	Height   int64
	Hash     string
	SHA256   string
	Count    uint64
	Location rawSegmentLocation
	Bytes    int64
}

// Bounded read-only physical inventory. No transaction locators, body parsing,
// repair or source acquisition occurs while rendering the timeline.
func rawSegmentInventory(ctx context.Context, root string) ([]rawSegmentInventoryRow, bool, error) {
	return rawSegmentInventoryWithLock(ctx, root, false)
}
func rawSegmentInventoryWithLock(ctx context.Context, root string, locatorLocked bool) ([]rawSegmentInventoryRow, bool, error) {
	return rawSegmentInventoryFiltered(ctx, root, nil)
}
func rawSegmentInventoryFiltered(ctx context.Context, root string, filter *indexDeleteRange) ([]rawSegmentInventoryRow, bool, error) {
	entries, complete, e := timelineDirectory(rawSegmentDir(root), 4096)
	if e != nil {
		return nil, false, e
	}
	prunes, e := readRawSegmentPrunes(ctx, root)
	if e != nil {
		return nil, false, e
	}
	out := []rawSegmentInventoryRow{}
	remaining := int64(64 << 20)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "catalog") || !strings.HasSuffix(entry.Name(), ".dat") {
			continue
		}
		var shard uint32
		if _, er := fmt.Sscanf(entry.Name(), "catalog%08d.dat", &shard); er != nil || entry.Name() != fmt.Sprintf("catalog%08d.dat", shard) {
			continue
		}
		base := int64(shard) * rawSegmentMapRows
		if filter != nil && (filter.From != nil && base+rawSegmentMapRows-1 < *filter.From || filter.To != nil && base > *filter.To) {
			continue
		}
		p := filepath.Join(rawSegmentDir(root), entry.Name())
		if e = updateapply.CheckPath(p); e != nil {
			return nil, false, e
		}
		f, e := os.Open(p)
		if e != nil {
			return nil, false, e
		}
		st, e := f.Stat()
		if e != nil || !st.Mode().IsRegular() || st.Size() > rawSegmentMapRows*rawSegmentMapBytes || st.Size()%rawSegmentMapBytes != 0 {
			f.Close()
			return nil, false, fmt.Errorf("invalid Raw inventory map")
		}
		start, stop := int64(0), st.Size()
		if filter != nil {
			if filter.From != nil && *filter.From > base {
				start = (*filter.From - base) * rawSegmentMapBytes
			}
			if filter.To != nil && *filter.To-base+1 < stop/rawSegmentMapBytes {
				stop = (*filter.To - base + 1) * rawSegmentMapBytes
			}
		}
		for offset := start; offset < stop; offset += rawSegmentMapBytes {
			if e = ctx.Err(); e != nil {
				f.Close()
				return nil, false, e
			}
			if remaining < rawSegmentMapBytes || len(out) >= timelineSourceLimit {
				f.Close()
				return out, false, nil
			}
			remaining -= rawSegmentMapBytes
			raw := make([]byte, rawSegmentMapBytes)
			if _, e = f.ReadAt(raw, offset); e != nil {
				f.Close()
				return nil, false, e
			}
			r, valid := decodeRawBodyCatalog(raw)
			if !valid {
				if !bytes.Equal(raw, make([]byte, rawSegmentMapBytes)) {
					complete = false
				}
				continue
			}
			if r.Height < 0 {
				continue
			}
			if r.Height != base+offset/rawSegmentMapBytes {
				complete = false
				continue
			}
			pruned := false
			for _, gap := range prunes.Locations {
				pruned = pruned || r.Location == gap
			}
			if !pruned {
				out = append(out, r)
			}
		}
		f.Close()
	}
	return out, complete, nil
}

func rawSegmentFramePresent(root string, r rawSegmentInventoryRow) bool {
	p := rawSegmentPath(root, r.Location.File)
	if updateapply.CheckPath(p) != nil {
		return false
	}
	f, e := os.Open(p)
	if e != nil {
		return false
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() < r.Location.Offset+rawSegmentFrameBytes+r.Bytes {
		return false
	}
	frame := make([]byte, rawSegmentFrameBytes)
	_, e = io.ReadFull(io.NewSectionReader(f, r.Location.Offset, rawSegmentFrameBytes), frame)
	storedHeight := int64(binary.LittleEndian.Uint64(frame[12:]))
	return e == nil && bytes.Equal(frame[:8], rawSegmentMagic[:]) && (storedHeight == r.Height || storedHeight == -1) && reverseHex(frame[20:]) == r.Hash && int64(binary.LittleEndian.Uint32(frame[8:])) == r.Bytes
}

// Scheduling hint only. A positive result defers to the real local verifier;
// no receipt or provider configuration is evidence of body integrity here.
func (a *app) hasLocalBlockSource(target blockTarget) bool {
	if !validHash(target.HashDisplay) {
		return false
	}
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	if !settings.CoreMountDisabled {
		if _, e := a.mountedCoreLocationByHash(target.HashDisplay); e == nil {
			return true
		}
	}
	present := func(p string) bool { st, e := os.Stat(p); return e == nil && st.Mode().IsRegular() && st.Size() >= 81 }
	if present(filepath.Join(a.dataDir, "indexes", "sources", target.HashDisplay+".block")) {
		return true
	}
	if p, e := indexedRawBodyPath(a.dataDir, target.HashDisplay); e == nil && present(p) {
		return true
	}
	a.cacheMu.RLock()
	entry, known := a.cacheIndex.Blocks[target.HashDisplay]
	a.cacheMu.RUnlock()
	if known && entry.Prefix == filepath.Base(entry.Prefix) && present(filepath.Join(a.dataDir, "blocks", "raw", entry.Prefix+".block")) {
		return true
	}
	archive := strings.TrimSpace(settings.ArchiveDir)
	if archive == "" {
		archive = filepath.Join(a.dataDir, "archive")
	}
	if present(filepath.Join(archive, "blocks", target.HashDisplay[:2], target.HashDisplay+".blk")) {
		return true
	}
	if r, e := rawSegmentReceiptAt(context.Background(), a.dataDir, target); e == nil && r != nil && r.Segment != nil {
		if pruned, e := rawSegmentPruned(context.Background(), a.dataDir, *r.Segment); e == nil && !pruned {
			return rawSegmentFramePresent(a.dataDir, rawSegmentInventoryRow{Height: target.Height, Hash: target.HashDisplay, Location: *r.Segment, Bytes: int64(r.Bytes)})
		}
	}
	return false
}
