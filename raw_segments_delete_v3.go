package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"./internal/updateapply"
)

// Logical deletion is tied to immutable segment locations, never heights:
// reacquiring or rebuilding a height appends fresh bytes and is not hidden by
// an older deletion. Physical compaction is deliberately a separate operation.
type indexDeleteRawSegments struct {
	Whole         bool
	Rows          []rawSegmentInventoryRow
	Before        rawSegmentPruneState
	PhysicalBytes int64
}

// Whole-store removal may reclaim recognized, exclusively Raw-owned segment
// files, including their unpublished append tails. Unknown names/types stay.
func rawSegmentWholeDeletePaths(root string) ([]string, int64, int64, error) {
	entries, complete, e := timelineDirectory(rawSegmentDir(root), 4096)
	if e != nil {
		return nil, 0, 0, e
	}
	if !complete {
		return nil, 0, 0, fmt.Errorf("Raw segment whole-store inventory exceeds its bounded budget")
	}
	paths := []string{}
	var physical, retained int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		p := filepath.Join(rawSegmentDir(root), name)
		if e = updateapply.CheckPath(p); e != nil {
			continue
		}
		st, e := entry.Info()
		if e != nil {
			return nil, 0, 0, e
		}
		if !st.Mode().IsRegular() {
			continue
		}
		var shard uint32
		_, parseErr := fmt.Sscanf(name, "catalog%08d.dat", &shard)
		catalog := parseErr == nil && name == fmt.Sprintf("catalog%08d.dat", shard)
		_, hexErr := hex.DecodeString(strings.TrimSuffix(strings.TrimPrefix(name, "hash"), ".dat"))
		hash := len(name) == 10 && strings.HasPrefix(name, "hash") && strings.HasSuffix(name, ".dat") && hexErr == nil
		if catalog || hash || name == "writer.json" || name == "pruned.json" {
			paths = append(paths, p)
			continue
		}
		var number uint32
		if _, e = fmt.Sscanf(name, "blk%08d.dat", &number); e != nil || name != fmt.Sprintf("blk%08d.dat", number) {
			continue
		}
		if st.Size() < rawSegmentFrameBytes+81 || st.Size() > rawSegmentBytes {
			retained += st.Size()
			continue
		}
		f, e := os.Open(p)
		if e != nil {
			return nil, 0, 0, e
		}
		frame := make([]byte, rawSegmentFrameBytes)
		_, e = f.ReadAt(frame, 0)
		verified := false
		if e == nil && bytes.Equal(frame[:8], rawSegmentMagic[:]) {
			n := int64(binary.LittleEndian.Uint32(frame[8:]))
			height := int64(binary.LittleEndian.Uint64(frame[12:]))
			hash := reverseHex(frame[20:])
			if n >= 81 && n <= rawSegmentBlockBytes && n+rawSegmentFrameBytes <= st.Size() {
				raw := make([]byte, n)
				if _, er := f.ReadAt(raw, rawSegmentFrameBytes); er == nil {
					v, er := parseBlockDetailed(height, hash, raw[:80], raw, "Raw segment whole-delete ownership verification", false)
					verified = er == nil && integrityVerified(v)
				}
			}
		}
		f.Close()
		if verified {
			paths = append(paths, p)
			physical += st.Size()
		} else {
			retained += st.Size()
		}
	}
	return paths, physical, retained, nil
}

func (a *app) indexDeleteSegmentInventory(ctx context.Context, q *indexDeleteRange, locked bool) (*indexDeleteRawSegments, *heightInterval, error) {
	rows, complete, e := rawSegmentInventoryFiltered(ctx, a.dataDir, q)
	if e != nil {
		return nil, nil, e
	}
	if !complete {
		return nil, nil, fmt.Errorf("Raw segment inventory is incomplete; finish metadata recovery or review a bounded supported source scope")
	}
	if len(rows) == 0 {
		return nil, nil, nil
	}
	first, last := rows[0].Height, rows[0].Height
	for _, r := range rows {
		if r.Height < first {
			first = r.Height
		}
		if r.Height > last {
			last = r.Height
		}
	}
	interval := indexDeleteEffectiveRange(q, first, last)
	out := &indexDeleteRawSegments{}
	out.Before, e = readRawSegmentPrunes(ctx, a.dataDir)
	if e != nil {
		return nil, nil, e
	}
	files := map[uint32]bool{}
	for _, r := range rows {
		if q != nil && (interval == nil || r.Height < interval.From || r.Height > interval.To) {
			continue
		}
		if e = ctx.Err(); e != nil {
			return nil, nil, e
		}
		header, er := a.readSelectedHeader(r.Height)
		if er != nil {
			continue
		}
		hash := hash256(header)
		if reverseHex(hash[:]) != r.Hash {
			continue
		}
		if !rawSegmentFramePresent(a.dataDir, r) {
			continue
		}
		out.Rows = append(out.Rows, r)
		if !files[r.Location.File] {
			st, er := os.Stat(rawSegmentPath(a.dataDir, r.Location.File))
			if er != nil {
				return nil, nil, er
			}
			out.PhysicalBytes += st.Size()
			files[r.Location.File] = true
		}
	}
	if len(out.Rows) == 0 {
		return nil, interval, nil
	}
	sort.Slice(out.Rows, func(i, j int) bool { return out.Rows[i].Height < out.Rows[j].Height })
	return out, interval, nil
}

// Caller holds rawSegmentMu and the exclusive raw snapshot guard, acquired by
// the deletion transaction. No reader can observe a partial metadata change.
func (a *app) applyIndexDeleteSegments(ctx context.Context, p *indexDeleteRawSegments, backup func(string) error) error {
	if p.Whole {
		return nil
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	path := rawSegmentPrunePath(a.dataDir)
	if e := backup(path); e != nil {
		return e
	}
	next := p.Before
	seen := map[rawSegmentLocation]bool{}
	for _, r := range next.Locations {
		seen[r] = true
	}
	for _, r := range p.Rows {
		if !seen[r.Location] {
			next.Locations = append(next.Locations, r.Location)
			seen[r.Location] = true
		}
	}
	sort.Slice(next.Locations, func(i, j int) bool {
		if next.Locations[i].File != next.Locations[j].File {
			return next.Locations[i].File < next.Locations[j].File
		}
		return next.Locations[i].Offset < next.Locations[j].Offset
	})
	if len(next.Locations) > 200000 {
		return fmt.Errorf("Raw logical pruning metadata needs reviewed compaction before further deletion")
	}
	next.Schema = 3
	next.Digest = ""
	next.Digest = indexDigest(next)
	return indexDeleteWriteJSON(path, next)
}
