package main

// This is a compact, rebuildable projection of private durable body membership.
// It is not an index checkpoint. Recovery reads bounded metadata/frame headers;
// it never parses a transaction, obtains a block, or inspects external mounts.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"./internal/updateapply"
)

type rawCoverageStamp struct {
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
	Digest   string `json:"digest,omitempty"`
}
type rawCoverageDocument struct {
	Schema       int                         `json:"schema"`
	Revision     uint64                      `json:"revision"`
	Ranges       []heightInterval            `json:"ranges"`
	Complete     bool                        `json:"complete"`
	Cursor       int64                       `json:"cursor"`
	LegacyShard  int                         `json:"legacy_shard"`
	LegacyOffset int                         `json:"legacy_offset"`
	AnchorHeight int64                       `json:"anchor_height"`
	AnchorHash   string                      `json:"anchor_hash"`
	Manifest     map[string]rawCoverageStamp `json:"manifest"`
	Checksum     string                      `json:"checksum"`
}
type rawCoverageView struct {
	Scope    string              `json:"scope"`
	State    string              `json:"state"`
	Complete bool                `json:"complete"`
	Revision string              `json:"revision"`
	Ranges   []heightInterval    `json:"ranges"`
	Total    *int64              `json:"total_blocks,omitempty"`
	Observed int64               `json:"observed_blocks"`
	From     *int64              `json:"from,omitempty"`
	To       *int64              `json:"to,omitempty"`
	Reason   string              `json:"reason,omitempty"`
	Visible  *rawCoverageVisible `json:"visible,omitempty"`
}
type rawCoverageVisible struct {
	From     int64            `json:"from"`
	To       int64            `json:"to"`
	Ranges   []heightInterval `json:"ranges"`
	Total    *int64           `json:"total_blocks,omitempty"`
	Observed int64            `json:"observed_blocks"`
	Complete bool             `json:"complete"`
}
type rawBackfillView struct {
	From   int64  `json:"from"`
	Height int64  `json:"height"`
	Next   int64  `json:"next"`
	Target int64  `json:"target"`
	State  string `json:"state"`
}
type rawCoverageState struct {
	mu                         sync.Mutex
	root, headers              string
	d                          rawCoverageDocument
	loaded, validated, running bool
	knownEmpty                 bool
	reason                     string
	saved                      time.Time
	changes                    int
	done                       chan struct{}
	legacyDir                  *os.File
	legacyDirShard, legacyRead int
	headerMu                   *sync.RWMutex
}

var rawCoverageStates sync.Map

func rawCoveragePath(root string) string {
	return filepath.Join(rawSegmentDir(root), "coverage-v1.json")
}
func rawCoverageDigest(d rawCoverageDocument) string { d.Checksum = ""; return indexDigest(d) }
func rawCoverageFor(root, headers string) *rawCoverageState {
	key, _ := filepath.Abs(root)
	value, _ := rawCoverageStates.LoadOrStore(key, &rawCoverageState{root: root, headers: headers})
	m := value.(*rawCoverageState)
	m.mu.Lock()
	if headers != "" {
		m.headers = headers
	}
	if !m.loaded {
		m.loaded = true
		if e := utxoReadJSON(context.Background(), rawCoveragePath(root), 32<<20, &m.d); e != nil || m.d.Schema != 1 || m.d.Checksum != rawCoverageDigest(m.d) {
			m.d = rawCoverageDocument{Schema: 1, AnchorHeight: -1, Ranges: []heightInterval{}, Manifest: map[string]rawCoverageStamp{}}
			_, writerErr := os.Stat(rawSegmentDir(root))
			_, legacyErr := os.Stat(filepath.Join(root, "indexes", "blocks", "bodies-v2"))
			m.knownEmpty = os.IsNotExist(writerErr) && os.IsNotExist(legacyErr)
		}
	}
	m.mu.Unlock()
	return m
}
func rawCoverageCount(ranges []heightInterval) int64 {
	var n int64
	for _, r := range ranges {
		n += r.To - r.From + 1
	}
	return n
}
func (a *app) rawCoverageState() *rawCoverageState {
	m := rawCoverageFor(a.dataDir, a.headersPath)
	m.mu.Lock()
	m.headerMu = &a.headerChainMu
	m.mu.Unlock()
	return m
}
func (m *rawCoverageState) resetMappingLocked() {
	m.d.Complete, m.validated = false, false
	m.d.Cursor, m.d.LegacyShard, m.d.LegacyOffset = 0, 0, 0
	m.d.Ranges = []heightInterval{}
	m.d.Manifest = nil
	if m.legacyDir != nil {
		m.legacyDir.Close()
		m.legacyDir = nil
	}
	m.legacyRead = 0
}
func rawCoverageSet(ranges []heightInterval, h int64, present bool) []heightInterval {
	if h < 0 {
		return ranges
	}
	if present {
		i := sort.Search(len(ranges), func(i int) bool { return ranges[i].To >= h-1 })
		if i == len(ranges) {
			return append(ranges, heightInterval{h, h})
		}
		if ranges[i].From > h+1 {
			ranges = append(ranges, heightInterval{})
			copy(ranges[i+1:], ranges[i:])
			ranges[i] = heightInterval{h, h}
			return ranges
		}
		ranges[i].From = min(ranges[i].From, h)
		ranges[i].To = max(ranges[i].To, h)
		for i+1 < len(ranges) && ranges[i+1].From <= ranges[i].To+1 {
			ranges[i].To = max(ranges[i].To, ranges[i+1].To)
			ranges = append(ranges[:i+1], ranges[i+2:]...)
		}
		return ranges
	}
	i := sort.Search(len(ranges), func(i int) bool { return ranges[i].To >= h })
	if i == len(ranges) || ranges[i].From > h {
		return ranges
	}
	r := ranges[i]
	if r.From == h && r.To == h {
		return append(ranges[:i], ranges[i+1:]...)
	}
	if r.From == h {
		ranges[i].From++
		return ranges
	}
	if r.To == h {
		ranges[i].To--
		return ranges
	}
	ranges[i].To = h - 1
	ranges = append(ranges, heightInterval{})
	copy(ranges[i+2:], ranges[i+1:])
	ranges[i+1] = heightInterval{h + 1, r.To}
	return ranges
}
func rawCoverageStampFile(path string) (rawCoverageStamp, bool) {
	s, e := os.Stat(path)
	if e != nil || !s.Mode().IsRegular() {
		return rawCoverageStamp{}, false
	}
	return rawCoverageStamp{Size: s.Size(), Modified: s.ModTime().UnixNano()}, true
}
func rawCoverageManifest(ctx context.Context, root string) (map[string]rawCoverageStamp, error) {
	out := map[string]rawCoverageStamp{}
	f, e := os.Open(rawSegmentDir(root))
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	if f != nil {
		defer f.Close()
	}
	for f != nil {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		entries, er := f.ReadDir(256)
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !(strings.HasPrefix(name, "catalog") && strings.HasSuffix(name, ".dat") || strings.HasPrefix(name, "blk") && strings.HasSuffix(name, ".dat") || strings.HasPrefix(name, "hash") && len(name) == 10 && strings.HasSuffix(name, ".dat") || name == "pruned.json") {
				continue
			}
			if len(out) > 65536 {
				return nil, fmt.Errorf("Raw metadata manifest exceeds recovery budget")
			}
			path := filepath.Join(rawSegmentDir(root), name)
			if e = updateapply.CheckPath(path); e != nil {
				return nil, e
			}
			if stamp, ok := rawCoverageStampFile(path); ok {
				out[name] = stamp
			}
		}
		if er == io.EOF {
			break
		}
		if er != nil {
			return nil, er
		}
	}
	for shard := 0; shard < 256; shard++ {
		p := filepath.Join(root, "indexes", "blocks", "bodies-v2", fmt.Sprintf("%02x", shard))
		if e = updateapply.CheckPath(p); e != nil {
			return nil, e
		}
		legacy, er := os.Open(p)
		if os.IsNotExist(er) {
			continue
		}
		if er != nil {
			return nil, er
		}
		var aggregate [32]byte
		var count, size int64
		for {
			if er = ctx.Err(); er != nil {
				legacy.Close()
				return nil, er
			}
			entries, readErr := legacy.ReadDir(256)
			for _, entry := range entries {
				name := entry.Name()
				hash := strings.TrimSuffix(strings.TrimSuffix(name, ".receipt.json"), ".block")
				if entry.IsDir() || !validHash(hash) || name != hash+".block" && name != hash+".block.receipt.json" {
					continue
				}
				path := filepath.Join(p, name)
				if er = updateapply.CheckPath(path); er != nil {
					legacy.Close()
					return nil, er
				}
				stamp, ok := rawCoverageStampFile(path)
				if !ok {
					legacy.Close()
					return nil, fmt.Errorf("legacy Raw membership changed during metadata validation")
				}
				// Addition modulo 2^256 makes the streaming fingerprint independent
				// of filesystem enumeration order, without a per-body stamp table.
				digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%d:%d", len(name), name, stamp.Size, stamp.Modified)))
				carry := uint16(0)
				for n := len(aggregate) - 1; n >= 0; n-- {
					carry += uint16(aggregate[n]) + uint16(digest[n])
					aggregate[n] = byte(carry)
					carry >>= 8
				}
				count++
				size += stamp.Size
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				legacy.Close()
				return nil, readErr
			}
		}
		legacy.Close()
		if count > 0 {
			out["v2/"+fmt.Sprintf("%02x", shard)] = rawCoverageStamp{Size: size, Modified: count, Digest: hex.EncodeToString(aggregate[:])}
		}
	}
	return out, nil
}
func (m *rawCoverageState) saveLocked() error {
	m.d.Checksum = rawCoverageDigest(m.d)
	if e := updateapply.CheckPath(rawCoveragePath(m.root)); e != nil {
		return e
	}
	if e := atomicWriteJSON(rawCoveragePath(m.root), m.d); e != nil {
		return e
	}
	m.saved = time.Now()
	m.changes = 0
	return nil
}
func rawCoverageHeader(path string, height int64) (string, int64, string) {
	if _, err := os.Stat(headerAppendMarkerPath051(path)); !os.IsNotExist(err) {
		return "", -1, ""
	}
	f, e := os.Open(path)
	if e != nil {
		return "", -1, ""
	}
	defer f.Close()
	s, e := f.Stat()
	if e != nil || s.Size()%80 != 0 || s.Size() < 80 {
		return "", -1, ""
	}
	tip := s.Size()/80 - 1
	var head, tail [80]byte
	if height < 0 || height > tip {
		return "", tip, ""
	}
	if _, e = f.ReadAt(head[:], height*80); e != nil {
		return "", -1, ""
	}
	if _, e = f.ReadAt(tail[:], tip*80); e != nil {
		return "", -1, ""
	}
	h, t := hash256(head[:]), hash256(tail[:])
	return reverseHex(h[:]), tip, reverseHex(t[:])
}
func rawCoverageSelectedHash(reader *timelineLocalReader, height int64) (string, error) {
	if reader == nil || reader.file == nil || height < 0 || height >= reader.count {
		return "", nil
	}
	if hash, ok := reader.hashes[height]; ok {
		return hash, nil
	}
	var raw [80]byte
	if _, err := reader.file.ReadAt(raw[:], height*80); err != nil {
		return "", err
	}
	digest := hash256(raw[:])
	hash := reverseHex(digest[:])
	reader.hashes[height] = hash
	return hash, nil
}

// Caller holds the Raw writer lock. A source receipt is already durable here;
// failure of this rebuildable summary never invents or removes body membership.
func rawCoveragePublished(root string, height int64, hash string, segment uint32) {
	rawCoverageMembershipPublished(root, height, hash, &segment)
}
func rawCoverageMembershipPublished(root string, height int64, hash string, segment *uint32) {
	if height < 0 {
		return
	}
	m := rawCoverageFor(root, "")
	m.mu.Lock()
	headerMu := m.headerMu
	m.mu.Unlock()
	if headerMu != nil {
		headerMu.RLock()
		defer headerMu.RUnlock()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.knownEmpty = false
	if m.headers == "" {
		m.headers = filepath.Join(root, "headers", "headers.bin")
	}
	selected, tip, tipHash := rawCoverageHeader(m.headers, height)
	oldAnchor, _, _ := rawCoverageHeader(m.headers, m.d.AnchorHeight)
	if m.validated && m.d.AnchorHeight >= 0 && oldAnchor != m.d.AnchorHash {
		m.resetMappingLocked()
		m.reason = "Selected chain changed; retained-body mapping is rebuilding."
	}
	if selected == "" || tipHash == "" {
		m.resetMappingLocked()
		m.d.Revision++
		m.reason = "Selected headers are unavailable; retained-body mapping requires recovery."
		return
	}
	if selected != hash {
		return
	}
	m.d.Ranges = rawCoverageSet(m.d.Ranges, height, true)
	m.d.Revision++
	m.changes++
	if m.validated {
		m.d.AnchorHeight, m.d.AnchorHash = tip, tipHash
		if segment == nil {
			// Legacy migration is infrequent. Force a bounded metadata remap
			// after restart instead of re-enumerating this shard per body.
			m.d.Manifest = nil
		} else {
			if m.d.Manifest == nil {
				m.d.Manifest = map[string]rawCoverageStamp{}
			}
			for _, p := range []string{rawBodyCatalogHeightPath(root, height), rawBodyCatalogHashPath(root, hash), rawSegmentPath(root, *segment)} {
				if st, ok := rawCoverageStampFile(p); ok {
					m.d.Manifest[filepath.Base(p)] = st
				}
			}
		}
	}
	if m.validated && (m.changes >= 32 || time.Since(m.saved) > time.Second) {
		if e := m.saveLocked(); e != nil {
			m.d.Complete = false
			m.reason = "Raw coverage summary requires metadata recovery: " + e.Error()
		}
	}
}
func rawCoverageInvalidated(root string) {
	m := rawCoverageFor(root, "")
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resetMappingLocked()
	m.d.Revision++
	m.reason = "Raw membership changed; metadata coverage is being reconstructed."
	_, writerErr := os.Stat(rawSegmentDir(root))
	_, legacyErr := os.Stat(filepath.Join(root, "indexes", "blocks", "bodies-v2"))
	if os.IsNotExist(writerErr) && os.IsNotExist(legacyErr) {
		m.knownEmpty = true
		m.validated = true
		m.d.Complete = true
		return
	}
	_ = m.saveLocked()
}

func waitRawCoverageStopped(root string) {
	key, _ := filepath.Abs(root)
	value, ok := rawCoverageStates.Load(key)
	if !ok {
		return
	}
	m := value.(*rawCoverageState)
	m.mu.Lock()
	done := m.done
	m.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

func (a *app) rawCoverageSnapshot(headers *timelineLocalReader, from, to *int64) rawCoverageView {
	m := a.rawCoverageState()
	m.mu.Lock()
	if m.knownEmpty {
		m.validated, m.d.Complete = true, true
		m.d.Ranges = []heightInterval{}
		m.d.AnchorHeight = -1
		m.d.AnchorHash = ""
	}
	if m.validated && m.d.AnchorHeight >= 0 && !headers.matches(m.d.AnchorHeight, m.d.AnchorHash) {
		m.resetMappingLocked()
		m.d.Revision++
		m.reason = "Selected chain changed; retained-body mapping is rebuilding."
	}
	start := !m.running && (!m.validated || !m.d.Complete) && headers != nil && headers.file != nil
	if start {
		m.running = true
		m.done = make(chan struct{})
	}
	v := rawCoverageView{Scope: "gateway_retained_raw", State: "rebuilding", Revision: strconv.FormatUint(m.d.Revision, 10), Ranges: append([]heightInterval{}, m.d.Ranges...), Reason: m.reason}
	if !m.validated {
		v.Ranges = []heightInterval{}
	}
	v.Complete = m.validated && m.d.Complete
	if v.Complete {
		v.State = "complete"
		n := rawCoverageCount(v.Ranges)
		v.Total = &n
		if len(v.Ranges) > 0 {
			lo, hi := v.Ranges[0].From, v.Ranges[len(v.Ranges)-1].To
			v.From, v.To = &lo, &hi
		}
	}
	if !m.running && !v.Complete {
		v.State = "partial"
	}
	v.Observed = rawCoverageCount(v.Ranges)
	if from != nil || to != nil {
		lo, hi := int64(0), int64(0)
		if headers != nil {
			hi = headers.count - 1
		}
		if from != nil {
			lo = *from
		}
		if to != nil {
			hi = *to
		}
		visible := &rawCoverageVisible{From: lo, To: hi, Complete: v.Complete, Ranges: []heightInterval{}}
		for _, r := range v.Ranges {
			if r.To >= lo && r.From <= hi {
				visible.Ranges = append(visible.Ranges, heightInterval{max(lo, r.From), min(hi, r.To)})
			}
		}
		visible.Observed = rawCoverageCount(visible.Ranges)
		if visible.Complete {
			n := visible.Observed
			visible.Total = &n
		}
		v.Visible = visible
	}
	m.mu.Unlock()
	if start {
		a.startUpdateAwareWorker(func() { a.rebuildRawCoverage(m) })
	}
	return v
}

func (a *app) rebuildRawCoverage(m *rawCoverageState) {
	ctx := context.Background()
	if a.network != nil {
		ctx = a.network.ctx
	}
	defer func() {
		m.mu.Lock()
		if m.legacyDir != nil {
			m.legacyDir.Close()
			m.legacyDir = nil
		}
		m.running = false
		close(m.done)
		m.mu.Unlock()
	}()
	// Compare once per process/recovery, never once per status poll. A crash
	// between catalog publication and summary save is detected by this manifest.
	manifest, e := rawCoverageManifest(ctx, a.dataDir)
	if e != nil {
		m.mu.Lock()
		m.reason = e.Error()
		m.mu.Unlock()
		return
	}
	// Publication uses this same order. Re-capture the selected tip only after
	// obtaining the writer lock, so initialization cannot erase a newly saved
	// height and then finish against an older captured tip.
	for !rawSegmentMu.TryLock() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
	a.headerChainMu.RLock()
	_, tip, tipHash := rawCoverageHeader(a.headersPath, 0)
	m.mu.Lock()
	anchor, _, _ := rawCoverageHeader(a.headersPath, m.d.AnchorHeight)
	if tip >= 0 && m.d.Complete && anchor == m.d.AnchorHash && indexDigest(manifest) == indexDigest(m.d.Manifest) {
		m.validated = true
		m.reason = ""
		m.mu.Unlock()
		a.headerChainMu.RUnlock()
		rawSegmentMu.Unlock()
		return
	}
	if indexDigest(manifest) != indexDigest(m.d.Manifest) || anchor != m.d.AnchorHash {
		m.resetMappingLocked()
	}
	m.d.Manifest = manifest
	m.d.AnchorHeight, m.d.AnchorHash = tip, tipHash
	m.d.Complete = false
	m.validated = tip >= 0
	if !m.validated {
		m.reason = "Selected headers are unavailable; retained-body mapping requires recovery."
	}
	m.mu.Unlock()
	a.headerChainMu.RUnlock()
	rawSegmentMu.Unlock()
	if tip < 0 {
		return
	}
	for ctx.Err() == nil {
		if !rawSegmentMu.TryLock() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
			continue
		}
		a.headerChainMu.RLock()
		m.mu.Lock()
		anchor, _, _ := rawCoverageHeader(a.headersPath, m.d.AnchorHeight)
		if anchor != m.d.AnchorHash {
			m.resetMappingLocked()
			_, m.d.AnchorHeight, m.d.AnchorHash = rawCoverageHeader(a.headersPath, 0)
			m.validated = m.d.AnchorHeight >= 0
		}
		var done bool
		if !m.validated {
			e = fmt.Errorf("selected headers unavailable")
		} else if m.d.Cursor <= m.d.AnchorHeight {
			e = m.scanRawPageLocked(ctx)
		} else if m.d.LegacyShard < 256 {
			e = m.scanLegacyRawLocked(ctx)
		} else {
			done = true
		}
		if e == nil && done {
			// The startup manifest was gathered without blocking the writer.
			// Publications update v3 stamps incrementally. If startup raced a
			// write, an older stamp causes safe remapping at the next restart.
			m.d.Complete = true
			m.reason = ""
		}
		m.d.Revision++
		if e == nil {
			e = m.saveLocked()
		}
		if e != nil {
			m.d.Complete = false
			m.reason = "Raw coverage metadata recovery paused: " + e.Error()
		}
		m.mu.Unlock()
		a.headerChainMu.RUnlock()
		rawSegmentMu.Unlock()
		if e != nil || done {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Millisecond):
		}
	}
}
func (m *rawCoverageState) scanRawPageLocked(ctx context.Context) error {
	h := m.d.Cursor
	path := rawBodyCatalogHeightPath(m.root, h)
	if e := updateapply.CheckPath(path); e != nil {
		return e
	}
	f, e := os.Open(path)
	if os.IsNotExist(e) {
		m.d.Cursor = (h/rawSegmentMapRows + 1) * rawSegmentMapRows
		return nil
	}
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > rawSegmentMapRows*rawSegmentMapBytes || st.Size()%rawSegmentMapBytes != 0 {
		return fmt.Errorf("invalid Raw height metadata")
	}
	base := h / rawSegmentMapRows * rawSegmentMapRows
	stop := min(m.d.AnchorHeight+1, min(base+rawSegmentMapRows, h+512))
	offset := (h - base) * rawSegmentMapBytes
	if offset >= st.Size() {
		m.d.Cursor = base + rawSegmentMapRows
		return nil
	}
	n := min((stop-h)*rawSegmentMapBytes, st.Size()-offset)
	buf := make([]byte, n)
	if _, e = f.ReadAt(buf, offset); e != nil {
		return e
	}
	prunes, e := readRawSegmentPrunes(ctx, m.root)
	if e != nil {
		return e
	}
	pruned := map[rawSegmentLocation]bool{}
	for _, p := range prunes.Locations {
		pruned[p] = true
	}
	headers, e := os.Open(m.headers)
	if e != nil {
		return e
	}
	defer headers.Close()
	selectedReader := &timelineLocalReader{file: headers, count: m.d.AnchorHeight + 1, hashes: map[int64]string{}}
	for i := int64(0); i < n/rawSegmentMapBytes; i++ {
		if e = ctx.Err(); e != nil {
			return e
		}
		raw := buf[i*rawSegmentMapBytes : (i+1)*rawSegmentMapBytes]
		row, ok := decodeRawBodyCatalog(raw)
		height := h + i
		if !ok {
			if !bytes.Equal(raw, make([]byte, rawSegmentMapBytes)) {
				return fmt.Errorf("damaged Raw height metadata at %d", height)
			}
			continue
		}
		if row.Height != height {
			return fmt.Errorf("Raw height metadata coordinate differs")
		}
		selectedHash, er := rawCoverageSelectedHash(selectedReader, height)
		if er != nil {
			return er
		}
		selected := selectedHash == row.Hash
		present := selected && !pruned[row.Location] && rawSegmentFramePresent(m.root, row)
		if !selected || pruned[row.Location] {
			// A later branch can replace the height projection while the
			// authenticated hash catalog still owns the selected branch's body.
			// Only affected coordinates need this bounded shard lookup.
			if current, found, er := rawBodyCatalogRow(ctx, m.root, blockTarget{Height: height, HashDisplay: selectedHash}); er != nil && !errors.Is(er, errRawCatalogPruned) {
				return er
			} else if found {
				present = rawSegmentFramePresent(m.root, current)
			}
		}
		m.d.Ranges = rawCoverageSet(m.d.Ranges, height, present)
	}
	m.d.Cursor = h + n/rawSegmentMapBytes
	return nil
}
func (m *rawCoverageState) scanLegacyRawLocked(ctx context.Context) error {
	// Keep the directory cursor open between bounded batches. On restart,
	// consume the saved ordinal once, 256 names per turn, without metadata
	// reads or holding the writer lock for an ever-growing prefix.
	for attempts := 0; attempts < 16 && m.d.LegacyShard < 256; attempts++ {
		path := filepath.Join(m.root, "indexes", "blocks", "bodies-v2", fmt.Sprintf("%02x", m.d.LegacyShard))
		if m.legacyDir != nil && m.legacyDirShard != m.d.LegacyShard {
			m.legacyDir.Close()
			m.legacyDir = nil
		}
		if m.legacyDir == nil {
			if e := updateapply.CheckPath(path); e != nil {
				return e
			}
			f, e := os.Open(path)
			if os.IsNotExist(e) {
				m.d.LegacyShard++
				m.d.LegacyOffset = 0
				continue
			}
			if e != nil {
				return e
			}
			m.legacyDir, m.legacyDirShard, m.legacyRead = f, m.d.LegacyShard, 0
		}
		count := 256
		skipping := m.legacyRead < m.d.LegacyOffset
		if skipping {
			count = min(count, m.d.LegacyOffset-m.legacyRead)
		}
		entries, er := m.legacyDir.ReadDir(count)
		if er != nil && er != io.EOF {
			return er
		}
		m.legacyRead += len(entries)
		if !skipping {
			headers, e := os.Open(m.headers)
			if e != nil {
				return e
			}
			selected := &timelineLocalReader{file: headers, count: m.d.AnchorHeight + 1, hashes: map[int64]string{}}
			for _, entry := range entries {
				if e = ctx.Err(); e != nil {
					headers.Close()
					return e
				}
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".block.receipt.json") {
					continue
				}
				hash := strings.TrimSuffix(entry.Name(), ".block.receipt.json")
				if !validHash(hash) {
					continue
				}
				p := filepath.Join(path, entry.Name())
				var receipt indexedRawInventory
				if e = utxoReadJSON(ctx, p, 16<<10, &receipt); e != nil {
					headers.Close()
					return e
				}
				if receipt.Schema != 2 || receipt.Hash != hash || receipt.Body == nil {
					continue
				}
				selectedHash, er := rawCoverageSelectedHash(selected, receipt.Height)
				if er != nil {
					headers.Close()
					return er
				}
				if selectedHash == hash && timelineRawPresent(filepath.Join(path, hash+".block"), hash, 0, int64(receipt.Body.Bytes), [8]byte{}) {
					m.d.Ranges = rawCoverageSet(m.d.Ranges, receipt.Height, true)
				}
			}
			headers.Close()
			m.d.LegacyOffset += len(entries)
		}
		if er == io.EOF {
			m.legacyDir.Close()
			m.legacyDir = nil
			m.d.LegacyShard++
			m.d.LegacyOffset = 0
		}
		return nil
	}
	return nil
}
func (a *app) handleRawCoverage(w http.ResponseWriter, r *http.Request) {
	if !safeLocalRequest(r) {
		jsonError(w, 403, fmt.Errorf("Raw coverage is restricted to the local management interface"))
		return
	}
	if !indexMethod(w, r, http.MethodGet) {
		return
	}
	var from, to *int64
	for key, dst := range map[string]**int64{"from": &from, "to": &to} {
		if s := r.URL.Query().Get(key); s != "" {
			n, e := strconv.ParseInt(s, 10, 64)
			if e != nil || n < 0 {
				jsonError(w, 400, fmt.Errorf("invalid %s", key))
				return
			}
			*dst = &n
		}
	}
	if from != nil && to != nil && *from > *to {
		jsonError(w, 400, fmt.Errorf("from exceeds to"))
		return
	}
	a.headerChainMu.RLock()
	defer a.headerChainMu.RUnlock()
	if _, err := os.Stat(headerAppendMarkerPath051(a.headersPath)); !os.IsNotExist(err) {
		jsonError(w, 503, fmt.Errorf("selected header snapshot is unavailable while an append needs recovery"))
		return
	}
	f, e := os.Open(a.headersPath)
	if e != nil {
		jsonError(w, 503, e)
		return
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || st.Size()%80 != 0 {
		jsonError(w, 503, fmt.Errorf("selected headers unavailable"))
		return
	}
	h := &timelineLocalReader{file: f, count: st.Size() / 80, hashes: map[int64]string{}}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, a.rawCoverageSnapshot(h, from, to))
}
