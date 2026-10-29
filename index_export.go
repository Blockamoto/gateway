package main

// Export is a read-only projection of authenticated, committed index snapshots.
// HTTP callers must spool this writer and attach it only when Complete is true.
import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"./internal/updateapply"
)

const indexExportMaxRecordBytes = 8 << 20

var errIndexExportBusy = errors.New("index export snapshot is busy; retry")
var errIndexExportSnapshotChanged = errors.New("committed export snapshot changed; review again")
var errIndexExportInventoryIncomplete = errors.New("complete inscription block inventory is unknown for this saved source")

type indexExportRequest struct {
	Index          string `json:"index"`
	Representation string `json:"representation"`
	Snapshot       string `json:"snapshot,omitempty"`
}
type indexExportSource struct {
	Index       string `json:"index"`
	Origin      string `json:"origin"`
	Commitment  string `json:"commitment"`
	Height      int64  `json:"height"`
	BlockHash   string `json:"block_hash"`
	From        int64  `json:"from"`
	ProfileHash string `json:"profile_hash,omitempty"`
	Count       uint64 `json:"count"`
}
type indexExportMissing struct {
	Height    int64  `json:"height"`
	BlockHash string `json:"block_hash"`
	Blocator  string `json:"blocator"`
	Reason    string `json:"reason"`
}
type indexExportRequirement struct {
	Index       string          `json:"index"`
	State       string          `json:"state"`
	Reason      string          `json:"reason"`
	Action      string          `json:"action"`
	LookupRange *heightInterval `json:"lookup_range,omitempty"`
}
type indexExportMetadata struct {
	Schema           int                      `json:"schema"`
	Index            string                   `json:"index"`
	Representation   string                   `json:"representation"`
	Format           string                   `json:"format"`
	Snapshot         string                   `json:"snapshot"`
	ExpiresAt        string                   `json:"expires_at,omitempty"`
	Sources          []indexExportSource      `json:"sources"`
	SourcesTotal     uint64                   `json:"sources_total"`
	Coverage         []heightInterval         `json:"coverage"`
	Rows             uint64                   `json:"rows"`
	Blocks           uint64                   `json:"blocks"`
	Ready            bool                     `json:"ready"`
	Complete         bool                     `json:"complete"`
	Partial          bool                     `json:"partial"`
	OmittedWinners   uint64                   `json:"omitted_winners"`
	MissingCount     uint64                   `json:"missing_count"`
	Missing          []indexExportMissing     `json:"missing"`
	MissingTruncated bool                     `json:"missing_truncated"`
	LookupRange      *heightInterval          `json:"lookup_range,omitempty"`
	Requirements     []indexExportRequirement `json:"requirements"`
	Note             string                   `json:"note"`
}
type indexExportPinnedStore struct {
	s             *indexStore
	from          int64
	to            int64
	cache         map[int64]indexBatch
	spill         *os.File
	forcePinned   bool
	recordLookups bool
	pinnedLookups map[int64]string
	pinBudget     *int
}
type indexExportBrowse struct {
	s      *indexStore
	blocks map[string]string
	origin string
	cache  map[int64]indexBatch
}
type indexExportSession struct {
	a                 *app
	ctx               context.Context
	q                 indexExportRequest
	meta              indexExportMetadata
	primary           *indexExportPinnedStore
	browse            *indexExportBrowse
	bitmap            *bitmapDevelopmentHead
	district          *bitmapDevelopmentHead
	lookups           []*indexExportPinnedStore
	lookupViews       []*indexExportBrowse
	memberships       []*indexExportBrowse
	anchors           map[int64]string
	authority         chainAuthorityView
	hashAt            func(int64) (string, error)
	readBytes         int64
	readBlocks        int
	rawHeight         int64
	rawHash           string
	rawView           *blockView
	rawTried          bool
	rawIDs            map[string]string
	rawRead           func(context.Context, int64, string) (*blockView, error)
	missingDecisions  map[string]indexExportMissing
	resolvedDecisions map[string]string
	recordMissing     bool
	replayMissing     bool
	missingBudget     *int
	resolvedBudget    *int
}
type indexExportEncoder struct {
	w      io.Writer
	digest hash.Hash
	bytes  int64
}

func (e *indexExportEncoder) line(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > indexExportMaxRecordBytes {
		return fmt.Errorf("individual export record exceeds its buffer bound; no complete export was produced")
	}
	b = append(b, '\n')
	if n, writeErr := e.w.Write(b); writeErr != nil {
		return writeErr
	} else if n != len(b) {
		return io.ErrShortWrite
	}
	if err != nil {
		return err
	}
	_, _ = e.digest.Write(b)
	e.bytes += int64(len(b))
	return nil
}
func (a *app) indexExportReview(ctx context.Context, q indexExportRequest) (indexExportMetadata, error) {
	return a.runIndexExport(ctx, q, io.Discard, true)
}
func (a *app) writeIndexExport(ctx context.Context, q indexExportRequest, w io.Writer) (indexExportMetadata, error) {
	return a.runIndexExport(ctx, q, w, false)
}
func (a *app) runIndexExport(ctx context.Context, q indexExportRequest, w io.Writer, review bool) (indexExportMetadata, error) {
	if err := ctx.Err(); err != nil {
		return indexExportMetadata{}, err
	}
	select {
	case indexExportGenerationSlot <- struct{}{}:
		defer func() { <-indexExportGenerationSlot }()
	default:
		return indexExportMetadata{}, errIndexExportBusy
	}
	s := &indexExportSession{a: a, ctx: ctx, q: q, anchors: map[int64]string{}, rawHeight: -1}
	defer func() { s.close() }()
	s.meta = indexExportMetadata{Schema: 1, Index: q.Index, Representation: q.Representation, Format: indexExportJSONLFormat, Sources: []indexExportSource{}, Coverage: []heightInterval{}, Missing: []indexExportMissing{}, Requirements: []indexExportRequirement{}, Note: "Coverage describes saved committed source blocks, not output completeness or complete chain coverage. Gaps remain unknown; see the end record for output completeness and omissions. Source commitments authenticate original records; exported bytes have a separate checksum."}
	if q.Index != "inscriptions" && q.Index != bitmapStandardDistrictID && q.Index != bitmapStandardParcelID {
		return s.meta, fmt.Errorf("export supports Inscriptions and Bitmap Districts and Bitmap Parcels only")
	}
	if q.Representation != "native" && q.Representation != "conventional" {
		return s.meta, fmt.Errorf("export representation must be native or conventional")
	}
	if err := ctx.Err(); err != nil {
		return s.meta, err
	}
	var pin *indexExportPin
	if q.Snapshot != "" {
		var err error
		pin, err = findIndexExportPin(a, q)
		if err != nil {
			return s.meta, err
		}
	}
	if err := s.capture(); err != nil {
		if errors.Is(err, errIndexExportBusy) {
			return s.meta, err
		}
		if pin != nil && ctx.Err() == nil {
			return s.meta, fmt.Errorf("%w: %v", errIndexExportSnapshotChanged, err)
		}
		return s.failed(err)
	}
	if pin != nil {
		if err := s.validateExportPin(pin); err != nil {
			return s.meta, err
		}
		s.close()
		s = pin.session(ctx, a, q, s.authority, s.hashAt, s.rawRead)
	}
	if len(s.meta.Coverage) == 0 {
		s.meta.Note = "No committed index coverage is available to export. Pending and staged work is excluded."
		return s.meta, nil
	}
	s.meta.Snapshot = indexDigest(struct {
		Index, Representation string
		Sources               []indexExportSource
		Coverage              []heightInterval
	}{q.Index, q.Representation, s.meta.Sources, s.meta.Coverage})
	if pin != nil {
		s.meta.Snapshot = pin.plan.meta.Snapshot
	} else if review {
		var nonce [32]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return s.meta, err
		}
		s.meta.Snapshot = indexDigest(struct {
			Snapshot string
			Nonce    [32]byte
		}{s.meta.Snapshot, nonce})
	}
	if q.Snapshot != "" && q.Snapshot != s.meta.Snapshot {
		return s.meta, errIndexExportSnapshotChanged
	}
	var draft *indexExportPin
	initialNote := s.meta.Note
	if review && q.Snapshot == "" {
		budget := indexExportPinMemoryLimit - newIndexExportPin(s).weight
		if budget < 0 {
			return s.meta, fmt.Errorf("export review exceeds the bounded snapshot memory budget")
		}
		if q.Index == bitmapStandardDistrictID && q.Representation == "conventional" {
			resolvedBudget := indexExportResolvedReviewMemoryLimit
			s.recordMissing = true
			s.missingDecisions = make(map[string]indexExportMissing)
			s.resolvedDecisions = make(map[string]string)
			s.missingBudget = &budget
			s.resolvedBudget = &resolvedBudget
		}
		for _, store := range s.lookups {
			if store != nil {
				store.recordLookups = true
				store.pinnedLookups = make(map[int64]string)
				store.pinBudget = &budget
			}
		}
	}
	whole := sha256.New()
	counter := &indexExportCountWriter{w: io.MultiWriter(w, whole)}
	e := &indexExportEncoder{w: counter, digest: sha256.New()}
	preview := s.meta.Coverage
	if len(preview) > 20 {
		preview = preview[:20]
	}
	if err := e.line(map[string]any{"type": "metadata", "schema": 1, "format": s.meta.Format, "index": q.Index, "representation": q.Representation, "snapshot": s.meta.Snapshot, "sources": s.meta.Sources, "sources_total": s.meta.SourcesTotal, "coverage": preview, "coverage_count": len(s.meta.Coverage), "coverage_digest": indexDigest(s.meta.Coverage), "scope": "saved_committed_source_coverage; gaps_unknown; output_completeness_in_end_record", "note": s.meta.Note}); err != nil {
		return s.meta, err
	}
	if err := s.membershipLines(e); err != nil {
		return s.meta, err
	}
	var err error
	if q.Index == "inscriptions" {
		err = s.inscriptions(e)
	} else {
		err = s.bitmapRows(e)
	}
	if err != nil {
		return s.failed(err)
	}
	// hashAt validates the captured selected-chain tip on every read. Recheck it
	// at completion as the ancestry fence, without retaining a map of all blocks.
	actual, anchorErr := s.hashAt(s.authority.Height)
	if anchorErr != nil || actual != s.authority.Hash {
		return s.meta, fmt.Errorf("export selected-chain snapshot changed; retry: %v", anchorErr)
	}
	if s.meta.MissingCount > 0 {
		s.meta.MissingTruncated = s.meta.MissingCount > uint64(len(s.meta.Missing))
		s.meta.Requirements = []indexExportRequirement{{Index: "tx-locator", State: "lookup_evidence_missing", Reason: "Conventional export needs verified transaction-ID lookup evidence at the missing reveal positions, or retained verified Raw blocks. This does not arm, fetch, repair, start or backfill anything.", Action: "inspect", LookupRange: s.meta.LookupRange}}
		if q.Index == bitmapStandardDistrictID && q.Representation == "conventional" && s.meta.OmittedWinners > 0 {
			s.meta.Partial = true
			s.meta.Note = "Partial District export: unresolved winner mappings are omitted. Coverage describes saved source blocks, not complete output. missing_count counts identity references; omitted_winners counts omitted mappings."
		} else {
			s.meta.Note = "Conventional export is incomplete; no download is available. missing_count counts identity references; missing is a bounded sample. lookup_range is the bounding span of all missing reference heights and may include gaps."
			return s.meta, nil
		}
	}
	checksum := hex.EncodeToString(e.digest.Sum(nil))
	if err = e.line(map[string]any{"type": "end", "snapshot": s.meta.Snapshot, "complete": !s.meta.Partial, "partial": s.meta.Partial, "omitted_winners": s.meta.OmittedWinners, "missing_count": s.meta.MissingCount, "rows": s.meta.Rows, "blocks": s.meta.Blocks, "payload_sha256": checksum}); err != nil {
		return s.meta, err
	}
	s.meta.Ready, s.meta.Complete = true, !s.meta.Partial
	checksumWhole := hex.EncodeToString(whole.Sum(nil))
	if pin != nil && (checksumWhole != pin.digest || counter.n != pin.size) {
		s.meta.Ready, s.meta.Complete = false, false
		return s.meta, errIndexExportSnapshotChanged
	}
	if pin != nil {
		if !time.Now().Before(pin.expires) {
			return s.meta, errIndexExportExpired
		}
		check := &indexExportSession{a: a, ctx: ctx, q: q, anchors: map[int64]string{}, rawHeight: -1}
		defer check.close()
		if err := check.capture(); err != nil {
			if errors.Is(err, errIndexExportBusy) {
				return s.meta, err
			}
			if ctx.Err() != nil {
				return s.meta, ctx.Err()
			}
			return s.meta, fmt.Errorf("%w: %v", errIndexExportSnapshotChanged, err)
		}
		if err := check.validateExportPin(pin); err != nil {
			return s.meta, err
		}
		if err := ctx.Err(); err != nil {
			return s.meta, err
		}
		if !time.Now().Before(pin.expires) {
			return s.meta, errIndexExportExpired
		}
	}
	if review && q.Snapshot == "" {
		if err := ctx.Err(); err != nil {
			return s.meta, err
		}
		draft = newIndexExportPin(s)
		draft.plan.meta.Note = initialNote
		check := &indexExportSession{a: a, ctx: ctx, q: q, anchors: map[int64]string{}, rawHeight: -1}
		defer check.close()
		if err := check.capture(); err != nil {
			if errors.Is(err, errIndexExportBusy) {
				return s.meta, err
			}
			if ctx.Err() != nil {
				return s.meta, ctx.Err()
			}
			return s.meta, fmt.Errorf("%w: %v", errIndexExportSnapshotChanged, err)
		}
		if err := check.validateExportPin(draft); err != nil {
			return s.meta, err
		}
		draft.digest, draft.size = checksumWhole, counter.n
		draft.expires = time.Now().Add(indexExportPinLifetime)
		if err := ctx.Err(); err != nil {
			return s.meta, err
		}
		if err := saveIndexExportPin(a, q, s.meta.Snapshot, draft); err != nil {
			return s.meta, err
		}
		s.meta.ExpiresAt = draft.expires.UTC().Format(time.RFC3339)
	} else if pin != nil {
		s.meta.ExpiresAt = pin.expires.UTC().Format(time.RFC3339)
	}
	return s.meta, nil
}

func (s *indexExportSession) failed(err error) (indexExportMetadata, error) {
	if errors.Is(err, errIndexExportInventoryIncomplete) {
		// A private export spool can encounter an older incomplete recipe
		// after processing valid blocks. Its error metadata must not advertise
		// that captured range as complete inventory, or offer a partial file.
		s.meta.Coverage = []heightInterval{}
		s.meta.Rows, s.meta.Blocks = 0, 0
		s.meta.Ready, s.meta.Complete = false, false
		s.meta.Note = "Complete inscription block inventory is unknown for a saved source. Known occurrences remain queryable; no complete inventory export is available."
	}
	return s.meta, err
}

func requireExportInscriptionInventory(b indexBatch) error {
	if !inscriptionBatchInventoryComplete(b) {
		return fmt.Errorf("%w at height %d", errIndexExportInventoryIncomplete, b.Checkpoint.Height)
	}
	return nil
}
func (s *indexExportSession) charge(path string) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return fmt.Errorf("individual export source exceeds its read buffer bound")
	}
	s.readBytes += info.Size()
	return nil
}
func (s *indexExportSession) readBatch(store *indexStore, commit string) (indexBatch, error) {
	if !validHash(commit) {
		return indexBatch{}, fmt.Errorf("invalid export source commitment")
	}
	if err := s.charge(filepath.Join(store.dir, "commits", commit+".json")); err != nil {
		return indexBatch{}, err
	}
	s.readBlocks++
	return store.readBatch(commit)
}
func (s *indexExportSession) anchor(height int64, hash string) error {
	if !validHash(hash) || height < 0 {
		return fmt.Errorf("invalid export reveal anchor")
	}
	if known, ok := s.anchors[height]; ok {
		if known != hash {
			return fmt.Errorf("conflicting export block hashes at height %d", height)
		}
		return nil
	}
	if len(s.anchors) >= 256 {
		s.anchors = map[int64]string{}
	}
	actual, err := s.hashAt(height)
	if err != nil || actual != hash {
		return fmt.Errorf("export source is stale or its selected anchor is unavailable at %d: %v", height, err)
	}
	s.anchors[height] = hash
	return nil
}
func (s *indexExportSession) store(id, origin string) (*indexExportPinnedStore, error) {
	store, err := indexStoreHead(s.a.dataDir, id)
	if err != nil {
		return nil, err
	}
	if store.checkpoint == nil {
		return nil, nil
	}
	c := store.checkpoint
	if err = s.anchor(c.Height, c.BlockHash); err != nil {
		return nil, err
	}
	s.addSource(indexExportSource{Index: id, Origin: origin, Commitment: c.Commitment, Height: c.Height, BlockHash: c.BlockHash, From: c.From, ProfileHash: c.RuleHash, Count: 1})
	return &indexExportPinnedStore{s: store, from: c.From, to: c.Height, cache: map[int64]indexBatch{}}, nil
}
func (s *indexExportSession) addSource(source indexExportSource) {
	s.meta.Sources = append(s.meta.Sources, source)
	s.meta.SourcesTotal += source.Count
}

// Sparse membership is already bounded by its native head file. Capture its
// immutable commitment map, without materializing all payloads or one metadata
// descriptor per block. Download emits every membership in small records.
func (s *indexExportSession) browseStore(id, origin string) (*indexExportBrowse, error) {
	store, head, err := s.a.browseStore(id)
	if err != nil {
		return nil, err
	}
	view := &indexExportBrowse{s: store, blocks: map[string]string{}, origin: origin, cache: map[int64]indexBatch{}}
	var from, to int64
	for key, commit := range head.Blocks {
		height, e := strconv.ParseInt(key, 10, 64)
		if e != nil || height < 0 || strconv.FormatInt(height, 10) != key || !validHash(commit) {
			return nil, fmt.Errorf("invalid captured sparse membership")
		}
		if len(view.blocks) == 0 || height < from {
			from = height
		}
		if len(view.blocks) == 0 || height > to {
			to = height
		}
		view.blocks[key] = commit
	}
	if len(view.blocks) > 0 {
		s.addSource(indexExportSource{Index: id, Origin: origin, Commitment: indexDigest(indexBrowseHead{Schema: 1, Blocks: view.blocks}), From: from, Height: to, ProfileHash: store.definition.RuleHash, Count: uint64(len(view.blocks))})
		s.memberships = append(s.memberships, view)
	}
	return view, nil
}
func (s *indexExportSession) membershipLines(e *indexExportEncoder) error {
	for i := 0; i < len(s.meta.Coverage); i += 256 {
		end := i + 256
		if end > len(s.meta.Coverage) {
			end = len(s.meta.Coverage)
		}
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if err := e.line(map[string]any{"type": "coverage", "intervals": s.meta.Coverage[i:end]}); err != nil {
			return err
		}
	}
	for _, view := range s.memberships {
		heights := make([]int64, 0, len(view.blocks))
		for key := range view.blocks {
			h, _ := strconv.ParseInt(key, 10, 64)
			heights = append(heights, h)
		}
		sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
		for i := 0; i < len(heights); i += 256 {
			if err := s.ctx.Err(); err != nil {
				return err
			}
			end := i + 256
			if end > len(heights) {
				end = len(heights)
			}
			members := make([]map[string]any, 0, end-i)
			for _, height := range heights[i:end] {
				members = append(members, map[string]any{"height": height, "commitment": view.blocks[strconv.FormatInt(height, 10)]})
			}
			if err := e.line(map[string]any{"type": "source_membership", "index": view.s.definition.ID, "origin": view.origin, "members": members}); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *indexExportSession) close() {
	seen := map[*indexExportPinnedStore]bool{}
	for _, p := range append(s.lookups, s.primary) {
		if p == nil || seen[p] {
			continue
		}
		seen[p] = true
		if p.spill != nil {
			name := p.spill.Name()
			_ = p.spill.Close()
			_ = os.Remove(name)
			p.spill = nil
		}
	}
}
func exportCoverage(ranges []heightInterval) []heightInterval {
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].From < ranges[j].From })
	out := []heightInterval{}
	for _, r := range ranges {
		if len(out) > 0 && r.From <= out[len(out)-1].To+1 {
			if r.To > out[len(out)-1].To {
				out[len(out)-1].To = r.To
			}
		} else {
			out = append(out, r)
		}
	}
	return out
}
func (s *indexExportSession) capture() error {
	authority := s.a.currentChainAuthority()
	s.authority = authority
	s.hashAt = s.a.addressSelectedHashes(s.ctx, authority)
	s.rawRead = s.readRaw
	if s.q.Index == "inscriptions" {
		var err error
		s.primary, err = s.store("inscriptions", "range")
		if err != nil {
			return err
		}
		s.browse, err = s.browseStore("inscriptions", "viewed_block")
		if err != nil {
			return err
		}
		if s.primary != nil {
			s.meta.Coverage = append(s.meta.Coverage, heightInterval{s.primary.from, s.primary.to})
		}
		for key := range s.browse.blocks {
			height, _ := strconv.ParseInt(key, 10, 64)
			s.meta.Coverage = append(s.meta.Coverage, heightInterval{height, height})
		}
	} else {
		var err error
		if !bitmapSnapshotMu.TryRLock() {
			return errIndexExportBusy
		}
		s.bitmap, err = readBitmapDevelopmentHead(s.ctx, s.a.dataDir, s.q.Index)
		if err == nil && s.bitmap != nil && s.q.Index == bitmapStandardParcelID && s.q.Representation == "conventional" {
			s.district, err = readBitmapDevelopmentHead(s.ctx, s.a.dataDir, bitmapStandardDistrictID)
		}
		bitmapSnapshotMu.RUnlock()
		if err != nil {
			return err
		}
		if s.bitmap != nil {
			h := s.bitmap
			if err = s.anchor(h.Height, h.BlockHash); err != nil {
				return err
			}
			s.meta.Coverage = []heightInterval{{h.SegmentStart, h.Height}}
			s.addSource(indexExportSource{Index: h.Index, Origin: "bitmap", Commitment: h.Commitment, Height: h.Height, BlockHash: h.BlockHash, From: h.SegmentStart, ProfileHash: h.ProfileHash, Count: 1})
			if s.q.Index == bitmapStandardParcelID && s.q.Representation == "conventional" {
				if s.district == nil {
					return fmt.Errorf("selected District source required for Parcel reference translation")
				}
				d := s.district
				if err = s.anchor(d.Height, d.BlockHash); err != nil {
					return err
				}
				s.addSource(indexExportSource{Index: d.Index, Origin: "parent_identity", Commitment: d.Commitment, Height: d.Height, BlockHash: d.BlockHash, From: d.SegmentStart, ProfileHash: d.ProfileHash, Count: 1})
			}
		}
	}
	s.meta.Coverage = exportCoverage(s.meta.Coverage)
	if s.q.Index == "inscriptions" {
		s.meta.Note += " Scope: committed occurrence mappings and compact saved Full metadata; content bodies and witness payloads are excluded."
	} else {
		s.meta.Note += " Scope: selected Bitmap winner mappings and their saved evidence/source outcomes; rejected and competing candidate history is excluded."
	}
	if s.q.Representation == "conventional" {
		for _, id := range []string{"inscriptions", inscriptionLocatorIndex, "tx-locator"} {
			if id == "inscriptions" && s.primary != nil {
				s.lookups = append(s.lookups, s.primary)
				continue
			}
			store, err := s.store(id, "identity_lookup")
			if err != nil {
				return err
			}
			if store != nil {
				s.lookups = append(s.lookups, store)
			}
		}
		for _, id := range []string{"inscriptions", "tx-locator"} {
			if id == "inscriptions" && s.browse != nil {
				s.lookupViews = append(s.lookupViews, s.browse)
				continue
			}
			view, err := s.browseStore(id, "identity_lookup_viewed_block")
			if err != nil {
				return err
			}
			s.lookupViews = append(s.lookupViews, view)
		}
	}
	return nil
}
func (s *indexExportSession) row(e *indexExportEncoder, v any) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	s.meta.Rows++
	return e.line(v)
}

func (s *indexExportSession) walkStore(p *indexExportPinnedStore, visit func(indexBatch) error) error {
	if p == nil {
		return nil
	}
	next := p.s.head.Commitment
	var child *indexBatch
	for next != "" {
		b, err := s.readBatch(p.s, next)
		if err != nil {
			return err
		}
		if child != nil && (child.Checkpoint.Height != b.Checkpoint.Height+1 || child.Checkpoint.From != b.Checkpoint.From || child.PreviousBlockHash != b.Checkpoint.BlockHash) {
			return fmt.Errorf("export checkpoint prefix is discontinuous")
		}
		if err = s.anchor(b.Checkpoint.Height, b.Checkpoint.BlockHash); err != nil {
			return err
		}
		if err = visit(b); err != nil {
			return err
		}
		next = b.Checkpoint.PreviousCommitment
		if next == "" && b.Checkpoint.Height != b.Checkpoint.From {
			return fmt.Errorf("export checkpoint has a missing committed prefix")
		}
		child = &b
	}
	return nil
}
func exportSourceForBatch(b indexBatch) map[string]any {
	return map[string]any{"checkpoint": b.Checkpoint, "records_hash": b.Checkpoint.RecordsHash, "attestation": "original_source_bytes; not a checksum of converted export rows"}
}
func exportObject(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	err = decoder.Decode(&out)
	return out, err
}
func (s *indexExportSession) inscriptions(e *indexExportEncoder) error {
	seen := map[string]bool{}
	emit := func(b indexBatch) error {
		if err := requireExportInscriptionInventory(b); err != nil {
			return err
		}
		key := strconv.FormatInt(b.Checkpoint.Height, 10)
		if seen[key] {
			return fmt.Errorf("duplicate export block membership")
		}
		if s.browse != nil && s.browse.blocks[key] != "" {
			seen[key] = true
		}
		s.meta.Blocks++
		coordinates := inscriptionBatchCoordinates(b)
		fullByPosition := map[string]inscriptionOccurrence{}
		for _, occurrence := range b.Inscriptions {
			coordinate := inscriptionCoordinate(uint32(occurrence.TxIndex), occurrence.Index, b.Checkpoint.Height)
			if _, duplicate := fullByPosition[coordinate]; duplicate {
				return fmt.Errorf("duplicate saved Full inscription position")
			}
			fullByPosition[coordinate] = occurrence
		}
		for _, coordinate := range coordinates {
			identity, err := s.identity(coordinate, b.Checkpoint.BlockHash)
			if err != nil {
				return err
			}
			record := map[string]any{"block_height": b.Checkpoint.Height, "block_hash": b.Checkpoint.BlockHash, "mode": inscriptionBatchMode(b)}
			position, ok := parseBODCoordinate(coordinate)
			if !ok || position.Kind != coordInscription {
				return fmt.Errorf("invalid saved inscription identity")
			}
			record["tx_index"], record["inscription_index"] = position.TxIndex, position.InscriptionIndex
			if o, retained := fullByPosition[coordinate]; retained {
				record["full"] = map[string]any{"input_index": o.InputIndex, "envelope_index": o.EnvelopeIndex, "block_order": o.BlockOrder, "witness_script_index": o.WitnessScriptIndex, "script_offset": o.ScriptOffset, "script_sha256": o.ScriptSHA256, "content_sha256": o.ContentSHA256, "content_type": o.Envelope.ContentType, "body_bytes": len(o.Body), "has_content_type": o.HasContentType, "parser_flags": o.ParserFlags, "parser_profile": o.ParserProfile, "canonical_number_known": o.CanonicalNumberKnown, "canonical_number": o.CanonicalNumber, "sat_number": o.SatNumber, "numbering_profile": o.NumberingProfile, "numbering_state": o.NumberingState}
			}
			if err = s.row(e, map[string]any{"type": "inscription", "id": identity, "record": record, "source": exportSourceForBatch(b)}); err != nil {
				return err
			}
		}
		return s.row(e, map[string]any{"type": "source_block", "record": map[string]any{"block_height": b.Checkpoint.Height, "block_hash": b.Checkpoint.BlockHash, "transaction_count": inscriptionBatchCount(b), "mode": inscriptionBatchMode(b), "occurrences": len(coordinates)}, "source": exportSourceForBatch(b)})
	}
	if err := s.walkStore(s.primary, func(b indexBatch) error {
		key := strconv.FormatInt(b.Checkpoint.Height, 10)
		if s.browse != nil && s.browse.blocks[key] != "" {
			viewed, err := s.browseBatch(s.browse, b.Checkpoint.Height)
			if err != nil {
				return err
			}
			b, err = exportCoherentInscriptionBatch(b, viewed)
			if err != nil {
				return err
			}
		}
		return emit(b)
	}); err != nil {
		return err
	}
	if s.browse != nil {
		heights := make([]int64, 0, len(s.browse.blocks))
		for key := range s.browse.blocks {
			h, _ := strconv.ParseInt(key, 10, 64)
			heights = append(heights, h)
		}
		sort.Slice(heights, func(i, j int) bool { return heights[i] > heights[j] })
		for _, h := range heights {
			key := strconv.FormatInt(h, 10)
			if seen[key] {
				continue
			}
			b, err := s.browseBatch(s.browse, h)
			if err != nil {
				return err
			}
			if err = s.anchor(h, b.Checkpoint.BlockHash); err != nil {
				return err
			}

			if err = emit(b); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *indexExportSession) browseBatch(view *indexExportBrowse, height int64) (indexBatch, error) {
	if b, ok := view.cache[height]; ok {
		return b, nil
	}
	b, err := s.readBatch(view.s, view.blocks[strconv.FormatInt(height, 10)])
	if err != nil {
		return b, err
	}
	if b.Checkpoint.Height != height || b.Checkpoint.From != height || b.Checkpoint.PreviousCommitment != "" {
		return b, fmt.Errorf("invalid committed sparse export membership")
	}
	if err = s.anchor(height, b.Checkpoint.BlockHash); err != nil {
		return b, err
	}
	if len(view.cache) >= 2 || view.cache == nil {
		view.cache = map[int64]indexBatch{}
	}
	view.cache[height] = b
	return b, nil
}
func exportCoherentInscriptionBatch(primary, viewed indexBatch) (indexBatch, error) {
	for _, b := range []indexBatch{primary, viewed} {
		if err := requireExportInscriptionInventory(b); err != nil {
			return primary, err
		}
	}
	if primary.Checkpoint.Height != viewed.Checkpoint.Height || primary.Checkpoint.BlockHash != viewed.Checkpoint.BlockHash || !reflect.DeepEqual(inscriptionBatchCoordinates(primary), inscriptionBatchCoordinates(viewed)) {
		return primary, fmt.Errorf("range and viewed-block inscription occurrences disagree at height %d", primary.Checkpoint.Height)
	}
	pc, vc := inscriptionBatchCount(primary), inscriptionBatchCount(viewed)
	if pc != nil && vc != nil && *pc != *vc {
		return primary, fmt.Errorf("range and viewed-block transaction denominators disagree")
	}
	// Known conventional references must agree even for native exports.
	ids := map[int]string{}
	for _, b := range []indexBatch{primary, viewed} {
		for _, o := range b.Inscriptions {
			if old := ids[o.TxIndex]; old != "" && old != o.TxID {
				return primary, fmt.Errorf("conflicting saved inscription transaction identities")
			}
			ids[o.TxIndex] = o.TxID
		}
		if b.InscriptionBlock != nil {
			for _, o := range b.InscriptionBlock.ConventionalIDs {
				if old := ids[int(o.TxIndex)]; old != "" && old != o.TxID {
					return primary, fmt.Errorf("conflicting saved inscription transaction identities")
				}
				ids[int(o.TxIndex)] = o.TxID
			}
		}
	}
	if inscriptionBatchMode(primary) == "full" && inscriptionBatchMode(viewed) == "full" {
		if len(primary.Inscriptions) != len(viewed.Inscriptions) {
			return primary, fmt.Errorf("conflicting saved Full occurrence count")
		}
		for i, o := range primary.Inscriptions {
			v := viewed.Inscriptions[i]
			// Numbering/verification annotations may evolve independently. The reveal
			// extraction, content and transaction identity must remain identical.
			o.CanonicalNumberKnown = false
			o.CanonicalNumber = nil
			o.SatNumber = nil
			o.NumberingProfile = ""
			o.NumberingState = ""
			o.Verification = verificationView{}
			o.VerificationState = ""
			v.CanonicalNumberKnown = false
			v.CanonicalNumber = nil
			v.SatNumber = nil
			v.NumberingProfile = ""
			v.NumberingState = ""
			v.Verification = verificationView{}
			v.VerificationState = ""
			if !reflect.DeepEqual(o, v) {
				return primary, fmt.Errorf("conflicting saved Full occurrence evidence")
			}
		}
	}
	if inscriptionBatchMode(primary) != "full" && inscriptionBatchMode(viewed) == "full" {
		return viewed, nil
	}
	return primary, nil
}

// Walk without retaining all visited radix pages. Parent branch metadata is
// bounded by depth; one authenticated leaf is consumed at a time.
func (s *indexExportSession) treeWalk(dir, root string, visit func(ordinalKV) error) error {
	var walk func(string, string) error
	walk = func(hash, prefix string) error {
		if hash == "" {
			return nil
		}
		if len(prefix) > 128 {
			return fmt.Errorf("export tree depth exceeded")
		}
		if err := s.charge(filepath.Join(dir, hash[:2], hash+".json")); err != nil {
			return err
		}
		tree := newOrdinalTree(dir, hash).withContext(s.ctx)
		page, err := tree.read(hash)
		if err != nil {
			return err
		}
		for _, kv := range page.Entries {
			if !strings.HasPrefix(kv.Key, prefix) {
				return fmt.Errorf("export tree leaf is outside its authenticated branch")
			}
			if err = visit(kv); err != nil {
				return err
			}
		}
		for i, child := range page.Children {
			if child != "" {
				if !validHash(child) {
					return fmt.Errorf("invalid export tree child")
				}
				if err = walk(child, prefix+fmt.Sprintf("%x", i)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if root != "" && !validHash(root) {
		return fmt.Errorf("invalid export tree root")
	}
	return walk(root, "")
}
func (s *indexExportSession) bitmapRows(e *indexExportEncoder) error {
	h := s.bitmap
	if h == nil {
		return nil
	}
	dir := filepath.Join(bitmapDevelopmentDir(s.a.dataDir, h.Index), "pages")
	outcomes := newOrdinalTree(dir, h.BlockRoot).withContext(s.ctx)
	winnerCount := uint64(0)
	if err := s.treeWalk(dir, h.WinnerRoot, func(kv ordinalKV) error {
		var c bitmapDevelopmentCandidate
		if err := json.Unmarshal(kv.Value, &c); err != nil {
			return err
		}
		if c.District == nil || c.Selection != "selected" || c.Eligibility != "eligible" || c.ID != "" || c.TxIndex < 1 || uint64(c.TxIndex) > uint64(^uint32(0)) || c.RevealHeight < h.SegmentStart || c.RevealHeight > h.Height || c.Blocator != inscriptionCoordinate(uint32(c.TxIndex), c.InscriptionIndex, c.RevealHeight) || !bitmapValidInscriptionBlocator(c.Blocator) || kv.Key != bitmapTargetKey(*c.District, c.Transaction) {
			return fmt.Errorf("invalid selected Bitmap export record")
		}
		if c.Kind != bitmapConsumerKind(h.Index) {
			return fmt.Errorf("Bitmap export kind differs from saved profile")
		}
		if c.Kind == "district" && (c.Transaction != nil || len(c.ParentBlocators) != 0 || c.Provenance != nil || c.SelectedParentDeclared) {
			return fmt.Errorf("District export contains Parcel target or parent provenance")
		}
		if c.Kind == "parcel" && c.Transaction == nil {
			return fmt.Errorf("Parcel export lost its transaction target")
		}
		if err := s.anchor(c.RevealHeight, c.RevealHash); err != nil {
			return err
		}
		if err := verifyBitmapParentEvidence(s.ctx, s.a.dataDir, c.Provenance); err != nil {
			return err
		}
		if c.Kind == "parcel" && (c.Provenance == nil || c.Provenance.ChildBlocator != c.Blocator || c.Provenance.ChildBlockHash != c.RevealHash) {
			return fmt.Errorf("Parcel export parent receipt differs from its reveal")
		}
		var outcome bitmapDevelopmentBlock
		have, outcomeErr := outcomes.get(fmt.Sprintf("%016x", c.RevealHeight), &outcome)
		if outcomeErr != nil {
			return outcomeErr
		}
		inOutcome := false
		for _, winner := range outcome.WinnerBlocators {
			if winner == c.Blocator {
				inOutcome = true
				break
			}
		}
		if !have || outcome.Height != c.RevealHeight || outcome.BlockHash != c.RevealHash || !outcome.BlockEvaluated || !outcome.CompleteScope || !inOutcome {
			return fmt.Errorf("selected Bitmap export record lacks its committed reveal outcome")
		}
		record, err := s.bitmapRecord(c)
		if err != nil {
			return err
		}
		identity, err := s.identity(c.Blocator, c.RevealHash)
		if err != nil {
			return err
		}
		winnerCount++
		if identity == "" && s.q.Index == bitmapStandardDistrictID && s.q.Representation == "conventional" {
			s.meta.OmittedWinners++
			return nil
		}
		source := map[string]any{"commitment": h.Commitment, "profile_hash": h.ProfileHash, "winner_root": h.WinnerRoot, "candidate_root": h.CandidateRoot, "block_root": h.BlockRoot, "key": kv.Key, "attestation": "original_source_roots; converted export has a separate checksum"}
		row := map[string]any{"type": "bitmap_winner", "id": identity, "record": record, "source": source}
		if c.Kind == "district" {
			row["bitmap"] = *c.District
		} else {
			row["district"], row["transaction"] = *c.District, *c.Transaction
			row["parcel"] = fmt.Sprintf("%d.%d.bitmap", *c.Transaction, *c.District)
		}
		return s.row(e, row)
	}); err != nil {
		return err
	}
	if winnerCount != h.WinnerCount {
		return fmt.Errorf("Bitmap export winner count differs from committed snapshot")
	}
	expectedHeight := h.SegmentStart
	if err := s.treeWalk(dir, h.BlockRoot, func(kv ordinalKV) error {
		var block bitmapDevelopmentBlock
		if err := json.Unmarshal(kv.Value, &block); err != nil {
			return err
		}
		if block.Height != expectedHeight || kv.Key != fmt.Sprintf("%016x", block.Height) || !block.BlockEvaluated || !block.CompleteScope || block.Height > h.Height {
			return fmt.Errorf("Bitmap export source outcome prefix is incomplete")
		}
		expectedHeight++
		if err := s.anchor(block.Height, block.BlockHash); err != nil {
			return err
		}
		type plain bitmapDevelopmentBlock
		record, err := exportObject(plain(block))
		if err != nil {
			return err
		}
		delete(record, "winner_blocators")
		delete(record, "ordered_winner_ids")
		winners := make([]string, 0, len(block.WinnerBlocators))
		omitted := 0
		for _, blocator := range block.WinnerBlocators {
			identity, err := s.identity(blocator, block.BlockHash)
			if err != nil {
				return err
			}
			if identity == "" && s.q.Index == bitmapStandardDistrictID && s.q.Representation == "conventional" {
				omitted++
				continue
			}
			winners = append(winners, identity)
		}
		record["winner_inscriptions"] = winners
		record["omitted_winner_inscriptions"] = omitted
		record["winner_ids_complete"] = omitted == 0
		s.meta.Blocks++
		return s.row(e, map[string]any{"type": "source_block", "record": record, "source": map[string]any{"commitment": h.Commitment, "block_root": h.BlockRoot, "profile_hash": h.ProfileHash}})
	}); err != nil {
		return err
	}
	if expectedHeight != h.Height+1 {
		return fmt.Errorf("Bitmap export source outcome coverage is incomplete")
	}
	return nil
}
func (s *indexExportSession) bitmapRecord(c bitmapDevelopmentCandidate) (map[string]any, error) {
	type plain bitmapDevelopmentCandidate
	record, err := exportObject(plain(c))
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"id", "blocator", "reveal_coordinate", "parents", "parent_blocators"} {
		delete(record, key)
	}
	parents := make([]string, 0, len(c.ParentBlocators))
	for _, parent := range c.ParentBlocators {
		hash, err := s.parentHash(parent, c.RevealHeight)
		if err != nil {
			return nil, err
		}
		identity, err := s.identity(parent, hash)
		if err != nil {
			return nil, err
		}
		parents = append(parents, identity)
	}
	record["parent_inscriptions"] = parents
	if c.Provenance != nil {
		proof, err := exportObject(c.Provenance)
		if err != nil {
			return nil, err
		}
		delete(proof, "parent_id")
		delete(proof, "parent_blocator")
		delete(proof, "child_blocator")
		parentHash, err := s.parentHash(c.Provenance.ParentBlocator, c.RevealHeight)
		if err != nil {
			return nil, err
		}
		parent, err := s.identity(c.Provenance.ParentBlocator, parentHash)
		if err != nil {
			return nil, err
		}
		child, err := s.identity(c.Provenance.ChildBlocator, c.Provenance.ChildBlockHash)
		if err != nil {
			return nil, err
		}
		proof["parent_inscription"], proof["child_inscription"] = parent, child
		record["provenance"] = proof
	}
	return record, nil
}
func (s *indexExportSession) parentHash(blocator string, childHeight int64) (string, error) {
	position, ok := parseBODCoordinate(blocator)
	if !ok || !bitmapValidInscriptionBlocator(blocator) || position.Height > childHeight {
		return "", fmt.Errorf("invalid saved Bitmap parent reference")
	}
	// Native references need no ID lookup, but still use their own reveal anchor.
	if s.q.Representation == "native" {
		return s.hashAt(position.Height)
	}
	if s.district == nil {
		return "", fmt.Errorf("selected District snapshot is required for parent reference translation")
	}
	d := s.district
	key := bitmapCandidateKey(bitmapDevelopmentCandidate{RevealHeight: position.Height, TxIndex: position.TxIndex, InscriptionIndex: uint32(position.InscriptionIndex)})
	var parent bitmapDevelopmentCandidate
	found, err := newOrdinalTree(filepath.Join(bitmapDevelopmentDir(s.a.dataDir, d.Index), "pages"), d.CandidateRoot).withContext(s.ctx).get(key, &parent)
	if err != nil {
		return "", err
	}
	if !found || parent.Blocator != blocator || parent.Kind != "district" || parent.Selection != "selected" || parent.Eligibility != "eligible" {
		return "", fmt.Errorf("parent reference lacks its exact selected District reveal evidence")
	}
	if err = s.anchor(parent.RevealHeight, parent.RevealHash); err != nil {
		return "", err
	}
	return parent.RevealHash, nil
}
