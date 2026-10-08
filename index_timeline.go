package main

// Timeline reads describe present local evidence. A decoded checkpoint is not
// raw block possession, a provider claim is not a local file, and a display
// request must never fetch blocks or start an index build.
import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const timelineSourceLimit = 4096
const timelineHeaderSearchLimit = 2000000

type indexTimelineView struct {
	Schema       int                  `json:"schema"`
	ObservedAt   string               `json:"observed_at"`
	TipKnown     bool                 `json:"tip_known"`
	TipHeight    int64                `json:"tip_height"`
	TipHash      string               `json:"tip_hash,omitempty"`
	TipTime      string               `json:"tip_time,omitempty"`
	TargetHeight int64                `json:"target_height"`
	TargetSource string               `json:"target_source,omitempty"`
	HeaderState  string               `json:"header_state"`
	Syncing      bool                 `json:"syncing"`
	LastProgress string               `json:"last_progress,omitempty"`
	Tracks       []indexTimelineTrack `json:"tracks"`
	Errors       []string             `json:"errors,omitempty"`
}

type indexTimelineTrack struct {
	ID               string                `json:"id"`
	Locked           bool                  `json:"locked"`
	Coverage         []heightInterval      `json:"coverage"`
	Gaps             []heightInterval      `json:"gaps"`
	CoverageComplete bool                  `json:"coverage_complete"`
	RecordedCoverage []heightInterval      `json:"recorded_coverage"`
	ReportedCoverage []heightInterval      `json:"reported_coverage"`
	Sources          []indexTimelineSource `json:"sources"`
	Note             string                `json:"note"`
}

type indexTimelineSource struct {
	ID           string           `json:"id"`
	Kind         string           `json:"kind"`
	Ranges       []heightInterval `json:"ranges"`
	Complete     bool             `json:"complete"`
	Verification string           `json:"verification"`
	Note         string           `json:"note"`
}

type timelineHeaderHint struct {
	Height int64
	Tip    string // Negative results are reusable only for this selected tip.
}

type timelineLocalReader struct {
	file   *os.File
	count  int64
	hashes map[int64]string
}

func (h *timelineLocalReader) matches(height int64, hash string) bool {
	if h == nil || h.file == nil || height < 0 || height >= h.count || !validHash(hash) {
		return false
	}
	known, ok := h.hashes[height]
	if !ok {
		var raw [80]byte
		if _, err := h.file.ReadAt(raw[:], height*80); err != nil {
			return false
		}
		digest := hash256(raw[:])
		known = reverseHex(digest[:])
		h.hashes[height] = known
	}
	return known == strings.ToLower(hash)
}

// The header read lock pins the selected file while this bounded projection is
// assembled. An interrupted append is reported rather than repaired by a GET.
func (a *app) indexTimeline(defs []indexDefinition, providers []indexProviderView, instances []indexInstance, statusErrors ...string) indexTimelineView {
	a.timelineMu.Lock()
	defer a.timelineMu.Unlock()
	st := a.getStatus()
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	out := indexTimelineView{Schema: 1, ObservedAt: time.Now().UTC().Format(time.RFC3339), TipHeight: -1, TargetHeight: -1, HeaderState: st.HeaderState, Syncing: st.Syncing, LastProgress: st.HeaderLastProgress, Tracks: []indexTimelineTrack{}}
	if st.HeaderTargetHeight >= 0 && (st.HeaderTargetSource != "" || st.HeaderTargetHeight > 0) {
		out.TargetHeight, out.TargetSource = st.HeaderTargetHeight, st.HeaderTargetSource
	}
	if st.Error != "" {
		out.Errors = append(out.Errors, "Header synchronization reports an error; inspect header status.")
	}
	if len(statusErrors) > 0 {
		out.Errors = append(out.Errors, "Some saved index metadata could not be inspected; see index status errors.")
	}
	a.headerChainMu.RLock()
	defer a.headerChainMu.RUnlock()
	h := &timelineLocalReader{hashes: map[int64]string{}}
	if _, err := os.Stat(headerAppendMarkerPath051(a.headersPath)); !os.IsNotExist(err) {
		out.Errors = append(out.Errors, "Selected header snapshot is unavailable while an append needs recovery.")
	} else if f, err := os.Open(a.headersPath); err == nil {
		defer f.Close()
		if info, err := f.Stat(); err == nil && info.Mode().IsRegular() && info.Size()%80 == 0 {
			h.file, h.count = f, info.Size()/80
			if h.count > 0 {
				var raw [80]byte
				if _, err := f.ReadAt(raw[:], (h.count-1)*80); err == nil {
					digest := hash256(raw[:])
					out.TipKnown, out.TipHeight, out.TipHash = true, h.count-1, reverseHex(digest[:])
					out.TipTime = time.Unix(int64(binary.LittleEndian.Uint32(raw[68:72])), 0).UTC().Format(time.RFC3339)
				} else {
					out.Errors = append(out.Errors, "Selected header tip could not be read.")
				}
			}
		} else {
			out.Errors = append(out.Errors, "Selected header file is incomplete or unreadable.")
		}
	} else if !os.IsNotExist(err) || st.HeaderCount > 0 {
		out.Errors = append(out.Errors, "Selected header file could not be opened.")
	}
	if !out.TipKnown {
		h.file, h.count = nil, 0
	}
	localSources := a.timelineLocalSources(h, out.TipHash, settings, &out.Errors)
	for _, def := range defs {
		track := indexTimelineTrack{ID: def.ID, Locked: def.Locked, Coverage: []heightInterval{}, Gaps: []heightInterval{}, RecordedCoverage: []heightInterval{}, ReportedCoverage: []heightInterval{}, Sources: []indexTimelineSource{}, CoverageComplete: out.TipKnown}
		switch def.ID {
		case "headers":
			track.Note = "Selected local header prefix. Header timestamps and sync state describe freshness; this is not full transaction validation."
			if out.TipKnown {
				track.Coverage = []heightInterval{{From: 0, To: out.TipHeight}}
			}
			track.Sources = append(track.Sources, indexTimelineSource{ID: "gateway_headers", Kind: "local_headers", Ranges: track.Coverage, Complete: out.TipKnown, Verification: "selected_chain", Note: track.Note})
		case "blocks":
			track.Note = "Present raw block files anchored to the selected local headers. File headers and extents are checked here; full block integrity is checked when opened. Committed decoded records and Core claims are separate."
			track.Sources = append(track.Sources, localSources...)
			for _, source := range localSources {
				track.Coverage = append(track.Coverage, source.Ranges...)
				track.CoverageComplete = track.CoverageComplete && source.Complete
			}
		default:
			track.Note = "Recorded derived-index coverage anchored to the selected local headers. This does not imply raw block retention."
		}
		for _, instance := range instances {
			if instance.Definition != def.ID || instance.Checkpoint == nil {
				continue
			}
			cp := instance.Checkpoint
			if !h.matches(cp.Height, cp.BlockHash) {
				track.CoverageComplete = false
				out.Errors = append(out.Errors, def.ID+": committed checkpoint is not anchored to this selected header snapshot.")
				continue
			}
			recorded := timelineRecordedCoverage(instance.Coverage, instance.Gaps)
			track.RecordedCoverage = append(track.RecordedCoverage, recorded...)
			track.Sources = append(track.Sources, indexTimelineSource{ID: "gateway_committed_index", Kind: "committed_records", Ranges: recorded, Complete: true, Verification: instance.Verification, Note: "Committed decoded records; the checkpoint is anchored, explicit gaps are excluded, and history is rechecked on resume. This is not a raw-block inventory."})
			if def.ID != "blocks" && def.ID != "headers" {
				track.Coverage = append(track.Coverage, recorded...)
			}
		}
		for _, provider := range providers {
			if provider.ID != def.ID || (provider.Provider != "bitcoin_core" && !strings.HasPrefix(provider.Provider, "bitcoin_core_")) {
				continue
			}
			track.ReportedCoverage = append(track.ReportedCoverage, provider.Coverage...)
			track.Sources = append(track.Sources, indexTimelineSource{ID: provider.Provider, Kind: "provider_reported", Ranges: append([]heightInterval{}, provider.Coverage...), Complete: false, Verification: provider.Verification, Note: provider.Note})
		}
		track.Coverage = mergeIntervals(track.Coverage)
		track.RecordedCoverage = mergeIntervals(track.RecordedCoverage)
		track.ReportedCoverage = mergeIntervals(track.ReportedCoverage)
		for _, problem := range statusErrors {
			if strings.HasPrefix(problem, def.ID+":") {
				track.CoverageComplete = false
			}
		}
		if track.CoverageComplete {
			track.Gaps = indexCoverageGaps(track.Coverage, def.StartHeight, out.TipHeight)
		}
		if def.Locked {
			track.Note = def.LockReason + " Saved coverage is retained; viewing the timeline does not enable this index."
		}
		out.Tracks = append(out.Tracks, track)
	}
	return out
}

func timelineRecordedCoverage(ranges, gaps []heightInterval) []heightInterval {
	ranges = mergeIntervals(ranges)
	if len(gaps) == 0 {
		return ranges
	}
	gaps = mergeIntervals(gaps)
	retained := []heightInterval{}
	for _, interval := range ranges {
		retained = append(retained, indexCoverageGaps(gaps, interval.From, interval.To)...)
	}
	return retained
}

func timelineSource(id, note string) indexTimelineSource {
	return indexTimelineSource{ID: id, Kind: "local_raw_blocks", Ranges: []heightInterval{}, Complete: true, Verification: "header_and_file_extent_checked; integrity_rechecked_on_read", Note: note}
}

// Only files and metadata are read. Unknown/missing sources never cause a fetch.
func (a *app) timelineLocalSources(headers *timelineLocalReader, tip string, settings appSettings, problems *[]string) []indexTimelineSource {
	cache := timelineSource("gateway_cache", "Present evictable raw cache files, including private blocks. This local view does not change sharing policy.")
	a.cacheMu.RLock()
	keys := make([]string, 0, len(a.cacheIndex.Blocks))
	for key := range a.cacheIndex.Blocks {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > timelineSourceLimit {
		keys, cache.Complete = keys[:timelineSourceLimit], false
	}
	entries := make([]cachedBlockEntry, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, a.cacheIndex.Blocks[key])
	}
	a.cacheMu.RUnlock()
	for _, entry := range entries {
		if entry.Prefix != filepath.Base(entry.Prefix) || !headers.matches(entry.Height, entry.Hash) {
			continue
		}
		if timelineRawPresent(filepath.Join(a.dataDir, "blocks", "raw", entry.Prefix+".block"), entry.Hash, 0, 0, [8]byte{}) {
			cache.Ranges = append(cache.Ranges, heightInterval{entry.Height, entry.Height})
		}
	}
	archiveRoot := strings.TrimSpace(settings.ArchiveDir)
	if archiveRoot == "" {
		archiveRoot = filepath.Join(a.dataDir, "archive")
	}
	archive := a.timelineArchiveSource(headers, archiveRoot, problems)
	retained := a.timelineRetainedSource(headers, tip, problems)
	mounted := timelineSource("core_mount", "Present mounted file extents anchored to selected headers. Core files remain read-only; raw block integrity is checked on use.")
	if !settings.CoreMountDisabled {
		a.coreStoreMu.RLock()
		dir, xor := a.coreStore.BlocksDir, a.coreStore.XORKey
		status := a.coreStore.Status
		configured := strings.TrimSpace(dir) != "" || strings.TrimSpace(settings.BitcoinDataDir) != "" || strings.TrimSpace(settings.BitcoinBlocksDir) != ""
		if configured && (!status.ScanComplete || status.ScanRunning || status.Error != "" || !status.Available || !status.Mounted) {
			mounted.Complete = false
			mounted.Note += " The configured mount inventory is incomplete or unavailable; unobserved heights remain unknown."
		}
		keys := make([]int64, 0, len(a.coreStore.ByHeight))
		for height := range a.coreStore.ByHeight {
			keys = append(keys, height)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		if len(keys) > timelineSourceLimit {
			keys, mounted.Complete = keys[:timelineSourceLimit], false
		}
		type row struct {
			hash string
			loc  coreBlockLocator
		}
		rows := make([]row, 0, len(keys))
		for _, height := range keys {
			hash := a.coreStore.ByHeight[height]
			if loc, ok := a.coreStore.ByHash[hash]; ok && loc.Height == height {
				rows = append(rows, row{hash, loc})
			}
		}
		a.coreStoreMu.RUnlock()
		for _, row := range rows {
			loc := row.loc
			if dir != "" && loc.Offset >= 8 && loc.Length >= 81 && loc.Length <= maxCoreBlockBytes && headers.matches(loc.Height, row.hash) && timelineRawPresent(filepath.Join(dir, fmt.Sprintf("blk%05d.dat", loc.FileNum)), row.hash, loc.Offset, int64(loc.Length), xor) {
				mounted.Ranges = append(mounted.Ranges, heightInterval{loc.Height, loc.Height})
			}
		}
	}
	sources := []indexTimelineSource{cache, archive, retained, mounted}
	for i := range sources {
		sources[i].Ranges = mergeIntervals(sources[i].Ranges)
		if !sources[i].Complete {
			*problems = append(*problems, sources[i].ID+": local coverage inspection is incomplete; unobserved ranges remain unknown.")
		}
	}
	return sources
}

// Avoid reading entire block bodies merely to draw a range. Requiring the
// expected header and any recorded extent excludes deleted/pruned, mismatched
// and forked files. A cache file has no committed length, so this is expressly
// a presence observation, not a complete block-integrity audit.
func timelineRawPresent(path, hash string, offset, length int64, xor [8]byte) bool {
	if !validHash(hash) || offset < 0 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size()-offset < 81 || (length > 0 && info.Size()-offset < length) {
		return false
	}
	if offset == 0 && length > 0 && info.Size() != length {
		return false
	}
	var raw [80]byte
	if _, err = f.ReadAt(raw[:], offset); err != nil {
		return false
	}
	coreXOR(raw[:], offset, xor)
	digest := hash256(raw[:])
	return reverseHex(digest[:]) == strings.ToLower(hash)
}

func timelineDirectory(path string, limit int) ([]os.DirEntry, bool, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	entries, err := f.ReadDir(limit + 1)
	if err != nil && err != io.EOF {
		return nil, false, err
	}
	complete := len(entries) <= limit
	if !complete {
		entries = entries[:limit]
	}
	return entries, complete, nil
}

func (a *app) timelineArchiveSource(headers *timelineLocalReader, archiveRoot string, problems *[]string) indexTimelineSource {
	source := timelineSource("gateway_archive", "Present archive blocks with matching receipt sizes and selected header anchors. Receipt/body checksums are rechecked when read.")
	root := filepath.Join(archiveRoot, "blocks")
	shards, complete, err := timelineDirectory(root, 256)
	source.Complete = complete
	if err != nil {
		*problems = append(*problems, "gateway_archive: archive directory is unreadable.")
		return source
	}
	remaining := timelineSourceLimit
	for _, shard := range shards {
		if !shard.IsDir() || len(shard.Name()) != 2 {
			continue
		}
		if remaining <= 0 {
			source.Complete = false
			break
		}
		entries, complete, err := timelineDirectory(filepath.Join(root, shard.Name()), remaining)
		remaining -= len(entries)
		if err != nil || !complete {
			source.Complete = false
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".json") {
				continue
			}
			hash := strings.TrimSuffix(name, ".json")
			if !validHash(hash) || !strings.HasPrefix(hash, shard.Name()) {
				continue
			}
			f, err := os.Open(filepath.Join(root, shard.Name(), name))
			if err != nil {
				source.Complete = false
				continue
			}
			var receipt archiveReceipt
			err = json.NewDecoder(io.LimitReader(f, 16<<10)).Decode(&receipt)
			f.Close()
			if err != nil || receipt.Schema != 1 || receipt.VerifierVersion != blockVerifierVersion || receipt.Hash != hash || receipt.Bytes < 81 || receipt.Bytes > maxMessageSize {
				source.Complete = false
				continue
			}
			if headers.matches(receipt.Height, hash) && timelineRawPresent(filepath.Join(root, shard.Name(), hash+".blk"), hash, 0, int64(receipt.Bytes), [8]byte{}) {
				source.Ranges = append(source.Ranges, heightInterval{receipt.Height, receipt.Height})
			}
		}
	}
	return source
}

// Retained sources are hash-named, so resolve only the bounded set of present
// hashes in one sequential pass. In-memory hints avoid repeating this pass on
// every UI poll, and each positive hint is re-anchored against selected headers.
func (a *app) timelineRetainedSource(headers *timelineLocalReader, tip string, problems *[]string) indexTimelineSource {
	source := timelineSource("gateway_retained_sources", "Present private raw sources retained by index jobs; their committed decoded records are listed separately.")
	root := filepath.Join(a.dataDir, "indexes", "sources")
	entries, complete, err := timelineDirectory(root, timelineSourceLimit)
	source.Complete = complete
	if err != nil {
		*problems = append(*problems, "gateway_retained_sources: source directory is unreadable.")
		return source
	}
	if a.timelineSourceHeights == nil {
		a.timelineSourceHeights = map[string]timelineHeaderHint{}
	}
	pending := map[string]bool{}
	hints := map[string]timelineHeaderHint{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".block") {
			continue
		}
		hash := strings.TrimSuffix(entry.Name(), ".block")
		if !validHash(hash) || !timelineRawPresent(filepath.Join(root, entry.Name()), hash, 0, 0, [8]byte{}) {
			continue
		}
		if hint, ok := a.timelineSourceHeights[hash]; ok && ((hint.Height >= 0 && headers.matches(hint.Height, hash)) || (hint.Height < 0 && hint.Tip == tip)) {
			hints[hash] = hint
			if hint.Height >= 0 {
				source.Ranges = append(source.Ranges, heightInterval{hint.Height, hint.Height})
			}
		} else {
			pending[hash] = true
		}
	}
	if len(pending) > 0 && headers.file != nil {
		limit := headers.count
		if limit > timelineHeaderSearchLimit {
			limit = timelineHeaderSearchLimit
			source.Complete = false
		}
		reader := bufio.NewReaderSize(io.NewSectionReader(headers.file, 0, limit*80), 64<<10)
		for height := int64(0); height < limit && len(pending) > 0; height++ {
			var raw [80]byte
			if _, err := io.ReadFull(reader, raw[:]); err != nil {
				source.Complete = false
				break
			}
			digest := hash256(raw[:])
			hash := reverseHex(digest[:])
			if pending[hash] {
				source.Ranges = append(source.Ranges, heightInterval{height, height})
				hints[hash] = timelineHeaderHint{Height: height, Tip: tip}
				delete(pending, hash)
			}
		}
		if source.Complete {
			for hash := range pending {
				hints[hash] = timelineHeaderHint{Height: -1, Tip: tip}
			}
		}
	} else if len(pending) > 0 {
		source.Complete = false
	}
	a.timelineSourceHeights = hints
	return source
}
