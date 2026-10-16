package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"./internal/updateapply"
)

type indexDeleteRawFile struct {
	Path     string `json:"path"`
	Receipt  string `json:"receipt,omitempty"`
	Height   int64  `json:"height"`
	Hash     string `json:"hash"`
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
	Modified int64  `json:"modified"`
	Category string `json:"category"`
}
type indexDeleteRawSummary struct {
	SegmentBodies                int             `json:"segment_bodies"`
	SegmentLogicalBytes          int64           `json:"segment_logical_bytes"`
	SegmentPhysicalRetainedBytes int64           `json:"segment_physical_retained_bytes"`
	SegmentPhysicalRemovedBytes  int64           `json:"segment_physical_removed_bytes"`
	RemovableFiles               int             `json:"removable_files"`
	RemovableBytes               int64           `json:"removable_bytes"`
	RetainedFiles                int             `json:"retained_files"`
	RetainedBytes                int64           `json:"retained_bytes"`
	RetainedReasons              []string        `json:"retained_reasons"`
	Range                        *heightInterval `json:"range,omitempty"`
}
type indexDeleteRawAnchor struct {
	height int64
	header []byte
}

func (a *app) indexDeleteRawWindow(ctx context.Context, requested *indexDeleteRange) (map[string]indexDeleteRawAnchor, error) {
	if requested == nil {
		return nil, nil
	}
	// An omitted endpoint comes from actual saved source anchors, not the
	// selected header tip. Use the bounded inventory/streamed-anchor path;
	// a wide header prefix alone must not reject a small saved source set.
	if requested.From == nil || requested.To == nil {
		return nil, nil
	}
	f, e := os.Open(a.headersPath)
	if e != nil {
		return nil, nil
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return nil, e
	}
	last := st.Size()/80 - 1
	first := int64(0)
	if requested.From != nil {
		first = *requested.From
	}
	if requested.To != nil && *requested.To < last {
		last = *requested.To
	}
	if last < first {
		return map[string]indexDeleteRawAnchor{}, nil
	}
	if last-first+1 > 65536 {
		return nil, fmt.Errorf("Raw range review is bounded to 65536 selected headers; add the missing endpoint or review a smaller explicit interval")
	}
	out := map[string]indexDeleteRawAnchor{}
	reader := bufio.NewReaderSize(io.NewSectionReader(f, first*80, (last-first+1)*80), 64<<10)
	for h := first; h <= last; h++ {
		if h%256 == 0 {
			if e = ctx.Err(); e != nil {
				return nil, e
			}
		}
		raw := make([]byte, 80)
		if _, e = io.ReadFull(reader, raw); e != nil {
			return nil, e
		}
		hash := hash256(raw)
		out[reverseHex(hash[:])] = indexDeleteRawAnchor{h, raw}
	}
	return out, nil
}

// Raw files are fallible providers, unlike the independently retained facts,
// FIFO ranges and self-contained recovery records of derived indexes. Remove
// only verified, regular, explicitly Gateway-owned names, not shared facts or
// an externally configured archive. Unlinking one hard-link name never changes
// an independently retained inode through another name.
func (a *app) indexDeleteRawInventory(ctx context.Context, requested *indexDeleteRange) ([]indexDeleteRawFile, indexDeleteRawSummary, error) {
	summary := indexDeleteRawSummary{RetainedReasons: []string{}}
	window, windowErr := a.indexDeleteRawWindow(ctx, requested)
	if windowErr != nil {
		return nil, summary, windowErr
	}
	type candidate struct {
		path, hash, category string
		bytes                int64
		modified             int64
	}
	candidates := []candidate{}
	unverified := 0
	roots := []struct{ path, kind string }{{filepath.Join(a.dataDir, "indexes", "blocks", "bodies-v2"), "indexed_raw_body"}, {filepath.Join(a.dataDir, "indexes", "sources"), "retained_source"}, {filepath.Join(a.dataDir, "blocks", "raw"), "cache"}, {filepath.Join(a.dataDir, "archive", "blocks"), "gateway_archive"}}
	for _, root := range roots {
		e := filepath.WalkDir(root.path, func(path string, entry fs.DirEntry, e error) error {
			if os.IsNotExist(e) {
				return nil
			}
			if e != nil {
				return e
			}
			if e = ctx.Err(); e != nil {
				return e
			}
			if e = updateapply.CheckPath(path); e != nil {
				if entry != nil && entry.IsDir() {
					return filepath.SkipDir
				}
				unverified++
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".block") && !strings.HasSuffix(name, ".blk") {
				return nil
			}
			st, e := entry.Info()
			if e != nil {
				return e
			}
			if !st.Mode().IsRegular() || st.Size() < 81 || st.Size() > maxMessageSize {
				unverified++
				summary.RetainedFiles++
				summary.RetainedBytes += st.Size()
				return nil
			}
			hash := strings.TrimSuffix(strings.TrimSuffix(name, ".block"), ".blk")
			if len(hash) > 64 {
				hash = hash[len(hash)-64:]
			}
			if !validHash(hash) || indexDeleteSafeDataPath(a.dataDir, path) != nil {
				unverified++
				summary.RetainedFiles++
				summary.RetainedBytes += st.Size()
				return nil
			}
			if window != nil {
				if _, inWindow := window[hash]; !inWindow {
					summary.RetainedFiles++
					summary.RetainedBytes += st.Size()
					return nil
				}
			}
			if len(candidates) >= 4096 {
				return fmt.Errorf("Raw source inventory exceeds 4096 files; choose a smaller explicit range or use a dedicated storage cleanup")
			}
			candidates = append(candidates, candidate{path, hash, root.kind, st.Size(), st.ModTime().UnixNano()})
			return nil
		})
		if e != nil {
			return nil, summary, e
		}
	}
	a.settingsMu.RLock()
	archive := strings.TrimSpace(a.settings.ArchiveDir)
	a.settingsMu.RUnlock()
	if archive != "" && filepath.Clean(archive) != filepath.Join(a.dataDir, "archive") {
		summary.RetainedReasons = append(summary.RetainedReasons, "The separately configured archive and external Core files are preserved; their ownership is not transferred by Raw Blocks deletion.")
	}
	pending := map[string]bool{}
	for _, c := range candidates {
		pending[c.hash] = true
	}
	anchors := map[string]indexDeleteRawAnchor{}
	if window != nil {
		for _, c := range candidates {
			if an, ok := window[c.hash]; ok {
				anchors[c.hash] = an
				delete(pending, c.hash)
			}
		}
	}
	if len(pending) > 0 {
		f, e := os.Open(a.headersPath)
		if e == nil {
			defer f.Close()
			st, er := f.Stat()
			if er != nil {
				return nil, summary, er
			}
			limit := st.Size() / 80
			if limit > timelineHeaderSearchLimit {
				limit = timelineHeaderSearchLimit
				summary.RetainedReasons = append(summary.RetainedReasons, "Header lookup budget reached; raw names without an authenticated local height are preserved.")
			}
			reader := bufio.NewReaderSize(io.NewSectionReader(f, 0, limit*80), 64<<10)
			for h := int64(0); h < limit && len(pending) > 0; h++ {
				if h%256 == 0 {
					if e = ctx.Err(); e != nil {
						return nil, summary, e
					}
				}
				raw := make([]byte, 80)
				if _, e = io.ReadFull(reader, raw); e != nil {
					return nil, summary, e
				}
				hash := hash256(raw)
				display := reverseHex(hash[:])
				if pending[display] {
					anchors[display] = indexDeleteRawAnchor{h, raw}
					delete(pending, display)
				}
			}
		} else {
			summary.RetainedReasons = append(summary.RetainedReasons, "Selected local headers are unavailable; source copies cannot be assigned a trusted height and are retained.")
		}
	}
	first, last := int64(-1), int64(-1)
	for _, v := range anchors {
		if first < 0 || v.height < first {
			first = v.height
		}
		if v.height > last {
			last = v.height
		}
	}
	effective := indexDeleteEffectiveRange(requested, first, last)
	summary.Range = effective
	files := []indexDeleteRawFile{}
	for _, c := range candidates {
		if e := ctx.Err(); e != nil {
			return nil, summary, e
		}
		an, have := anchors[c.hash]
		if !have {
			unverified++
			summary.RetainedFiles++
			summary.RetainedBytes += c.bytes
			continue
		}
		if requested != nil && (effective == nil || an.height < effective.From || an.height > effective.To) {
			summary.RetainedFiles++
			summary.RetainedBytes += c.bytes
			continue
		}
		rawhash, _ := displayHashRaw(c.hash)
		target := blockTarget{Height: an.height, HashDisplay: c.hash, HashRaw: rawhash, ExpectedHeader: an.header}
		raw, view, e := readRetainedIndexBlock(c.path, target)
		if e != nil || !integrityVerified(view) {
			unverified++
			summary.RetainedFiles++
			summary.RetainedBytes += c.bytes
			continue
		}
		sum := sha256.Sum256(raw)
		f := indexDeleteRawFile{Path: c.path, Height: an.height, Hash: c.hash, Bytes: int64(len(raw)), SHA256: hex.EncodeToString(sum[:]), Modified: c.modified, Category: c.category}
		if c.category == "gateway_archive" {
			receipt := strings.TrimSuffix(c.path, ".blk") + ".json"
			if e = updateapply.CheckPath(receipt); e != nil {
				unverified++
				summary.RetainedFiles++
				summary.RetainedBytes += c.bytes
				continue
			}
			var r archiveReceipt
			if e = utxoReadJSON(ctx, receipt, 16<<10, &r); e != nil || r.Schema != 1 || r.VerifierVersion != blockVerifierVersion || r.Hash != c.hash || r.Height != an.height || int64(r.Bytes) != c.bytes || r.SHA256 != f.SHA256 {
				unverified++
				summary.RetainedFiles++
				summary.RetainedBytes += c.bytes
				continue
			}
			f.Receipt = receipt
		}
		if c.category == "indexed_raw_body" {
			if _, e = os.Stat(c.path + ".receipt.json"); e == nil {
				if e = updateapply.CheckPath(c.path + ".receipt.json"); e != nil {
					unverified++
					summary.RetainedFiles++
					summary.RetainedBytes += c.bytes
					continue
				}
				f.Receipt = c.path + ".receipt.json"
			}
		}
		files = append(files, f)
		summary.RemovableFiles++
		summary.RemovableBytes += f.Bytes
	}
	if unverified > 0 {
		summary.RetainedReasons = append(summary.RetainedReasons, fmt.Sprintf("%d unproven, unsafe, damaged or non-selected source entries are retained; no complete raw wipe is claimed.", unverified))
	}
	if requested != nil {
		summary.RetainedReasons = append(summary.RetainedReasons, "Serialized copies outside the displayed interval are retained.")
	}
	summary.RetainedReasons = append(summary.RetainedReasons, "Mandatory output facts, UTXO undo, Sat savepoint ranges, Bitmap recovery receipts and every external Core file are preserved.")
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, summary, nil
}
