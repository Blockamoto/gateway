package main

// Chain storage policy is operational metadata. It never changes the District
// rules, source, isolated outcome, cumulative hash or scoped commitment.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"./internal/updateapply"
)

type bitmapChainPoint struct {
	Height     int64  `json:"height"`
	BlockHash  string `json:"block_hash"`
	Commitment string `json:"commitment"`
}
type bitmapChainManifest struct {
	Schema            int                `json:"schema"`
	Index             string             `json:"index"`
	ProfileHash       string             `json:"profile_hash"`
	Mode              string             `json:"mode"`
	Generation        uint64             `json:"generation"`
	Current           string             `json:"current"`
	Points            []bitmapChainPoint `json:"restore_points"`
	RetainedIntervals []heightInterval   `json:"retained_intervals"`
	Digest            string             `json:"digest"`
	Recovered         bool               `json:"-"`
}
type bitmapChainManifestPointer struct {
	Current  string `json:"current"`
	Previous string `json:"previous,omitempty"`
	Digest   string `json:"digest"`
}
type bitmapChainRetentionView struct {
	Mode              string             `json:"mode"`
	Generation        uint64             `json:"generation"`
	RestorePoints     []bitmapChainPoint `json:"restore_points"`
	RetainedIntervals []heightInterval   `json:"retained_intervals"`
	Note              string             `json:"note"`
	RecoveredPrevious bool               `json:"recovered_previous_generation,omitempty"`
	MaintenanceNote   string             `json:"maintenance_note,omitempty"`
}

func bitmapChainManifestDigest(m bitmapChainManifest) string { m.Digest = ""; return indexDigest(m) }
func bitmapChainPointerDigest(p bitmapChainManifestPointer) string {
	p.Digest = ""
	return indexDigest(p)
}
func bitmapChainManifestPath(root, id, digest string) string {
	return filepath.Join(bitmapDevelopmentDir(root, id), "retention", digest+".json")
}
func bitmapChainReceiptPath(root, id, commit string) string {
	return filepath.Join(bitmapDevelopmentDir(root, id), "source-receipts", commit[:2], commit+".json")
}
func writeBitmapChainJSON(ctx context.Context, path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeBitmapChainBytes(ctx, path, raw)
}
func writeBitmapChainBytes(ctx context.Context, path string, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(raw) > 16<<20 {
		return fmt.Errorf("Bitmap metadata exceeds bounded read budget; checkpoint not advanced")
	}
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	return atomicWriteBytes(path, raw)
}
func bitmapHeightRetained(intervals []heightInterval, h int64) bool {
	for _, r := range intervals {
		if h >= r.From && h <= r.To {
			return true
		}
	}
	return false
}
func readBitmapChainPointer(ctx context.Context, root, id string) (*bitmapChainManifestPointer, error) {
	var p bitmapChainManifestPointer
	err := readBitmapJSON(ctx, filepath.Join(bitmapDevelopmentDir(root, id), "retention.json"), &p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !validHash(p.Current) || p.Previous != "" && !validHash(p.Previous) || p.Digest != bitmapChainPointerDigest(p) {
		return nil, fmt.Errorf("Bitmap Chain retention pointer damaged; reviewed recovery required")
	}
	return &p, nil
}
func readBitmapChainManifestAt(ctx context.Context, root, id, digest string) (*bitmapChainManifest, error) {
	if !validHash(digest) {
		return nil, fmt.Errorf("invalid Chain retention generation")
	}
	var m bitmapChainManifest
	if err := readBitmapJSON(ctx, bitmapChainManifestPath(root, id, digest), &m); err != nil {
		return nil, err
	}
	i, err := bitmapResolvedInstance(ctx, root, id)
	if err != nil {
		return nil, err
	}
	if m.Schema != 1 || m.Index != id || m.ProfileHash != i.ProfileHash || m.Digest != digest || bitmapChainManifestDigest(m) != digest || !validHash(m.Current) || m.Mode != "full" && m.Mode != "pruned" || len(m.Points) > 5 {
		return nil, fmt.Errorf("Bitmap Chain retention manifest authentication mismatch")
	}
	for _, p := range m.Points {
		if !validHash(p.Commitment) || !validHash(p.BlockHash) || p.Height < i.StartHeight {
			return nil, fmt.Errorf("invalid Chain recovery point")
		}
	}
	return &m, nil
}
func readBitmapChainManifest(ctx context.Context, root, id string) (*bitmapChainManifest, error) {
	p, err := readBitmapChainPointer(ctx, root, id)
	if err != nil || p == nil {
		return nil, err
	}
	read := func(digest string) (*bitmapChainManifest, error) {
		m, e := readBitmapChainManifestAt(ctx, root, id, digest)
		if e != nil {
			return nil, e
		}
		if _, e = readBitmapDevelopmentCommit(ctx, root, id, m.Current); e != nil {
			return nil, e
		}
		for _, point := range m.Points {
			h, e := readBitmapDevelopmentCommit(ctx, root, id, point.Commitment)
			if e != nil {
				return nil, e
			}
			if h.Height != point.Height || h.BlockHash != point.BlockHash {
				return nil, fmt.Errorf("Chain recovery point closure mismatch")
			}
		}
		return m, nil
	}
	m, err := read(p.Current)
	if err == nil {
		return m, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if p.Previous != "" {
		old, e := read(p.Previous)
		if e == nil {
			old.Recovered = true
			return old, nil
		}
	}
	return nil, fmt.Errorf("Chain current and previous publication are unavailable; reviewed recovery required: %w", err)
}
func bitmapChainRetentionStatus(ctx context.Context, root, id string) (bitmapChainRetentionView, error) {
	v := bitmapChainRetentionView{Mode: "full", RestorePoints: []bitmapChainPoint{}, RetainedIntervals: []heightInterval{}, Note: "Restore points are the newest two multiples of 12 and two multiples of 144, with a young-segment baseline. Actual heights do not guarantee 288-block recovery."}
	m, err := readBitmapChainManifest(ctx, root, id)
	if err != nil {
		return v, err
	}
	if m != nil {
		v.Mode = m.Mode
		v.Generation = m.Generation
		v.RestorePoints = m.Points
		v.RetainedIntervals = m.RetainedIntervals
		v.RecoveredPrevious = m.Recovered
		if m.Recovered {
			v.Note = "Current publication is damaged; displaying the authenticated previous generation. Resume reviews its selected-chain recovery before advancing. " + v.Note
		}
		var maintenance struct {
			Generation uint64 `json:"generation"`
			Reason     string `json:"reason"`
		}
		if e := readBitmapJSON(ctx, filepath.Join(bitmapDevelopmentDir(root, id), "maintenance.json"), &maintenance); e == nil && maintenance.Generation == m.Generation {
			v.MaintenanceNote = maintenance.Reason
		}
		return v, nil
	}
	h, err := readBitmapDevelopmentHead(ctx, root, id)
	if err == nil && h != nil {
		v.RetainedIntervals = []heightInterval{{h.SegmentStart, h.Height}}
	}
	return v, err
}
func validBitmapChainReceipt(h *bitmapDevelopmentHead, i bitmapProfileInstance) bool {
	d := h.DistrictReceipt
	return d != nil && (h.DistrictReceiptRef == "" || h.DistrictReceiptRef == d.Commitment) && d.Index == i.DistrictInstance && d.ProfileHash == i.DistrictProfileHash && d.ProfileHash == bitmapProfileHash(d.Profile) && d.Commitment == h.DependencyCommitment && bitmapDevelopmentDigest(*d) == d.Commitment && d.Height == h.Height && d.BlockHash == h.BlockHash && d.SegmentStart == h.SegmentStart && d.LastBlock.BlockEvaluated && d.LastBlock.CompleteScope && d.LastBlock.Height == h.Height && d.LastBlock.BlockHash == h.BlockHash && indexDigest(d.LastBlock.OrderedWinnerIDs) == indexDigest(h.LastBlock.OrderedWinnerIDs)
}

// This input path deliberately has no blockView: a saved completed outcome is
// not a fabricated Bitcoin block and does not justify decoding or extraction.
func (s *bitmapRunConsumer) ApplyRetainedOutcome(ctx context.Context, height int64) error {
	bitmapDevelopmentMu.Lock()
	defer bitmapDevelopmentMu.Unlock()
	if bitmapConsumerKind(s.id) != "chain" {
		return fmt.Errorf("outcome-only input belongs to Bitmap Chain")
	}
	if err := checkIndexRunPublication(ctx); err != nil {
		return err
	}
	d, err := bitmapHeadAt(ctx, s.root, s.instance.DistrictInstance, height)
	if err != nil {
		return err
	}
	if d == nil {
		return fmt.Errorf("%w: completed District outcome at %d was not retained; explicitly review producer replay or backfill", ErrBitmapEvidenceRequired, height)
	}
	return s.applyDistrictOutcome(ctx, d)
}
func (s *bitmapRunConsumer) applyDistrictOutcome(ctx context.Context, d *bitmapDevelopmentHead) error {
	if d.Height != s.NextHeight() || d.Index != s.instance.DistrictInstance || d.ProfileHash != s.instance.DistrictProfileHash || d.SegmentStart != s.instance.StartHeight || !d.LastBlock.BlockEvaluated || !d.LastBlock.CompleteScope || d.LastBlock.Height != d.Height || d.LastBlock.BlockHash != d.BlockHash {
		return fmt.Errorf("District outcome instance, rules or scope mismatch")
	}
	if s.head != nil && d.PreviousBlockHash != s.head.BlockHash {
		return fmt.Errorf("Bitmap Chain outcome ancestry changed; reconcile before resume")
	}
	if s.hashAt == nil {
		return fmt.Errorf("selected Bitcoin authority required for outcome-only Chain")
	}
	hash, err := s.hashAt(d.Height)
	if err != nil {
		return err
	}
	if hash != d.BlockHash {
		return fmt.Errorf("%w: District outcome is not on selected branch", ErrBitmapEvidenceRequired)
	}
	h := bitmapDevelopmentHead{Schema: 1, Index: s.id, Profile: s.instance.Profile, ProfileHash: s.instance.ProfileHash, SegmentStart: s.instance.StartHeight, Height: d.Height, BlockHash: d.BlockHash, PreviousBlockHash: d.PreviousBlockHash, DistrictInstance: s.instance.DistrictInstance, DistrictReceipt: d, DependencyCommitment: d.Commitment}
	previous := strings.Repeat("0", 64)
	if s.head != nil {
		h.Previous = s.head.Commitment
		h.BlockRoot = s.head.BlockRoot
		previous = s.head.LastBlock.ChainHash
	}
	r := bitmapDevelopmentBlock{Height: d.Height, BlockHash: d.BlockHash, BlockEvaluated: true, CompleteScope: true, OrderedWinnerIDs: append([]string{}, d.LastBlock.OrderedWinnerIDs...), OutcomeProfileHash: s.instance.DistrictProfileHash, DependencyCommitment: d.Commitment, PreviousCommitment: h.Previous, Note: "Completed District outcome receipt; source, rules and outcome identity are separate from independent consensus validation."}
	r.IsolatedHash, err = bitmapIsolatedOutcome(r.OrderedWinnerIDs)
	if err != nil {
		return err
	}
	r.ChainHash, err = bitmapCumulativeOutcome(previous, r.IsolatedHash)
	if err != nil {
		return err
	}
	f, err := bitmapScopedCommitment(h.SegmentStart, h.Height, h.BlockHash, r.ChainHash, s.instance.DistrictProfileHash)
	if err != nil {
		return err
	}
	r.ScopedCommitment = &f
	h.LastBlock = r
	t := newOrdinalTree(filepath.Join(s.dir, "pages"), h.BlockRoot).withContext(ctx)
	if err = t.put(fmt.Sprintf("%016x", h.Height), r); err != nil {
		return err
	}
	if err := s.lockProfileEvidence(ctx); err != nil {
		return err
	}
	h.BlockRoot, err = t.flush()
	if err != nil {
		return err
	}
	return s.publishBitmapChain(ctx, h)
}

func bitmapChainIntervals(heights []int64) []heightInterval {
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
	out := []heightInterval{}
	for _, h := range heights {
		if len(out) > 0 && h <= out[len(out)-1].To+1 {
			if h > out[len(out)-1].To {
				out[len(out)-1].To = h
			}
		} else {
			out = append(out, heightInterval{h, h})
		}
	}
	return out
}
func (s *bitmapRunConsumer) chainRecoveryHeads(ctx context.Context, h bitmapDevelopmentHead, m *bitmapChainManifest) ([]bitmapDevelopmentHead, error) {
	wanted := map[int64]bool{}
	for _, interval := range []int64{12, 144} {
		for n := int64(0); n < 2; n++ {
			v := h.Height/interval*interval - n*interval
			if v >= h.SegmentStart {
				wanted[v] = true
			}
		}
	}
	if h.Height/144*144-144 < h.SegmentStart {
		wanted[h.SegmentStart] = true
	}
	points := []bitmapDevelopmentHead{}
	for height := range wanted {
		if height == h.Height {
			points = append(points, h)
			continue
		}
		var p *bitmapDevelopmentHead
		var err error
		if m != nil {
			for _, r := range m.Points {
				if r.Height == height {
					p, err = readBitmapDevelopmentCommit(ctx, s.root, s.id, r.Commitment)
					break
				}
			}
		}
		if p == nil && err == nil {
			p, err = bitmapHeadAt(ctx, s.root, s.id, height)
		}
		if err != nil {
			return nil, err
		}
		if p == nil {
			continue
		} // Cadence before this instance existed is not invented.
		points = append(points, *p)
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Height < points[j].Height })
	return points, nil
}
func (s *bitmapRunConsumer) persistChainCommit(ctx context.Context, h bitmapDevelopmentHead) (bitmapDevelopmentHead, error) {
	if err := s.lockProfileEvidence(ctx); err != nil {
		return h, err
	}
	if h.DistrictReceipt == nil {
		d, err := readBitmapDevelopmentCommit(ctx, s.root, s.instance.DistrictInstance, h.DependencyCommitment)
		if err != nil {
			return h, err
		}
		h.DistrictReceipt = d
		h.DistrictInstance = s.instance.DistrictInstance
	}
	if !validBitmapChainReceipt(&h, s.instance) {
		return h, fmt.Errorf("Chain recovery dependency receipt mismatch")
	}
	if h.DistrictReceiptRef == "" {
		raw, err := json.Marshal(h.DistrictReceipt)
		if err != nil {
			return h, err
		}
		if len(raw) > 16<<20 {
			return h, fmt.Errorf("District outcome receipt exceeds bounded metadata budget; checkpoint not advanced")
		}
		path := bitmapChainReceiptPath(s.root, s.id, h.DistrictReceipt.Commitment)
		if err = writeBitmapChainBytes(ctx, path, raw); err != nil {
			return h, err
		}
		if err = syncDirectory(filepath.Dir(path)); err != nil {
			return h, err
		}
		if err = syncDirectory(filepath.Join(s.dir, "source-receipts")); err != nil {
			return h, err
		}
		h.DistrictReceiptRef = h.DistrictReceipt.Commitment
	} else if h.DistrictReceiptRef != h.DistrictReceipt.Commitment {
		return h, fmt.Errorf("District receipt address mismatch")
	}
	h.Commitment = bitmapDevelopmentDigest(h)
	path := filepath.Join(s.dir, "commits", h.Commitment[:2], h.Commitment+".json")
	wire := h
	wire.DistrictReceipt = nil
	raw, err := json.Marshal(wire)
	if err != nil {
		return h, err
	}
	if len(raw) > 16<<20 {
		return h, fmt.Errorf("Chain block outcome exceeds bounded metadata budget; checkpoint not advanced")
	}
	if err := writeBitmapChainBytes(ctx, path, raw); err != nil {
		return h, err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return h, err
	}
	return h, syncDirectory(filepath.Join(s.dir, "commits"))
}
func (s *bitmapRunConsumer) compactChainHead(ctx context.Context, h bitmapDevelopmentHead) (bitmapDevelopmentHead, error) {
	t := newOrdinalTree(filepath.Join(s.dir, "pages"), "").withContext(ctx)
	if err := t.put(fmt.Sprintf("%016x", h.Height), h.LastBlock); err != nil {
		return h, err
	}
	root, err := t.flush()
	if err != nil {
		return h, err
	}
	h.BlockRoot = root
	return s.persistChainCommit(ctx, h)
}

func (s *bitmapRunConsumer) compactChainGeneration(ctx context.Context, h bitmapDevelopmentHead, previous *bitmapChainManifest) (*bitmapChainManifest, error) {
	points, err := s.chainRecoveryHeads(ctx, h, previous)
	if err != nil {
		return nil, err
	}
	tree := newOrdinalTree(filepath.Join(s.dir, "pages"), "").withContext(ctx)
	m := bitmapChainManifest{Schema: 1, Index: s.id, ProfileHash: s.instance.ProfileHash, Mode: "pruned", Generation: previous.Generation, Points: []bitmapChainPoint{}}
	heights := []int64{h.Height}
	for _, point := range points {
		point, err = s.compactChainHead(ctx, point)
		if err != nil {
			return nil, err
		}
		m.Points = append(m.Points, bitmapChainPoint{point.Height, point.BlockHash, point.Commitment})
		heights = append(heights, point.Height)
		if err = tree.put(fmt.Sprintf("%016x", point.Height), point.LastBlock); err != nil {
			return nil, err
		}
	}
	if err = tree.put(fmt.Sprintf("%016x", h.Height), h.LastBlock); err != nil {
		return nil, err
	}
	h.BlockRoot, err = tree.flush()
	if err != nil {
		return nil, err
	}
	h, err = s.persistChainCommit(ctx, h)
	if err != nil {
		return nil, err
	}
	m.Current = h.Commitment
	m.RetainedIntervals = bitmapChainIntervals(heights)
	m.Digest = bitmapChainManifestDigest(m)
	if err = writeBitmapChainJSON(ctx, bitmapChainManifestPath(s.root, s.id, m.Digest), m); err != nil {
		return nil, err
	}
	if err = syncDirectory(filepath.Join(s.dir, "retention")); err != nil {
		return nil, err
	}
	return &m, nil
}
func (s *bitmapRunConsumer) publishBitmapChain(ctx context.Context, h bitmapDevelopmentHead) error {
	if err := checkIndexRunPublication(ctx); err != nil {
		return err
	}
	if err := updateapply.CheckPath(s.dir); err != nil {
		return err
	}
	current, err := readBitmapDevelopmentHead(ctx, s.root, s.id)
	if err != nil {
		return err
	}
	expected := ""
	if s.head != nil {
		expected = s.head.Commitment
	}
	if current != nil && current.Commitment != expected || current == nil && expected != "" {
		return fmt.Errorf("Bitmap Chain writer checkpoint changed")
	}
	if s.hashAt != nil {
		hash, err := s.hashAt(h.Height)
		if err != nil {
			return err
		}
		if hash != h.BlockHash {
			return fmt.Errorf("Bitmap Chain source changed before publication")
		}
	}
	m, err := readBitmapChainManifest(ctx, s.root, s.id)
	if err != nil {
		return err
	}
	if m == nil && s.head != nil {
		// First migration from the preserved preview layout owns a real previous
		// full generation before any optional historical files can be collected.
		m = &bitmapChainManifest{Schema: 1, Index: s.id, ProfileHash: s.instance.ProfileHash, Mode: "full", Current: s.head.Commitment, Points: []bitmapChainPoint{}, RetainedIntervals: []heightInterval{{s.head.SegmentStart, s.head.Height}}}
		m.Digest = bitmapChainManifestDigest(*m)
		if err = writeBitmapChainJSON(ctx, bitmapChainManifestPath(s.root, s.id, m.Digest), m); err != nil {
			return err
		}
		if err = syncDirectory(filepath.Join(s.dir, "retention")); err != nil {
			return err
		}
	}
	mode := s.retention
	if mode == "" && m != nil {
		mode = m.Mode
	}
	if mode == "" {
		mode = "full"
	}
	if mode != "full" && mode != "pruned" {
		return fmt.Errorf("unknown Chain retention")
	}
	points, err := s.chainRecoveryHeads(ctx, h, m)
	if err != nil {
		return err
	}
	bitmapSnapshotMu.Lock()
	defer bitmapSnapshotMu.Unlock()
	if mode == "pruned" {
		if m != nil && m.Mode == "full" && s.head != nil {
			// Project the preceding valid state into its own durable compact
			// generation before publishing. Keeping a full cumulative root as the
			// backup would defeat a reviewed finite Full-to-Pruned conversion.
			m, err = s.compactChainGeneration(ctx, *s.head, m)
			if err != nil {
				return err
			}
		}
		for n := range points {
			points[n], err = s.compactChainHead(ctx, points[n])
			if err != nil {
				return err
			}
		}
		t := newOrdinalTree(filepath.Join(s.dir, "pages"), "").withContext(ctx)
		for _, p := range points {
			if err = t.put(fmt.Sprintf("%016x", p.Height), p.LastBlock); err != nil {
				return err
			}
		}
		if err = t.put(fmt.Sprintf("%016x", h.Height), h.LastBlock); err != nil {
			return err
		}
		h.BlockRoot, err = t.flush()
		if err != nil {
			return err
		}
	}
	h, err = s.persistChainCommit(ctx, h)
	if err != nil {
		return err
	}
	if mode == "full" {
		for n := range points {
			if points[n].Height == h.Height {
				points[n] = h
			}
		}
	}
	manifest := bitmapChainManifest{Schema: 1, Index: s.id, ProfileHash: s.instance.ProfileHash, Mode: mode, Generation: 1, Current: h.Commitment, Points: []bitmapChainPoint{}}
	if m != nil {
		manifest.Generation = m.Generation + 1
	}
	heights := []int64{}
	for _, p := range points {
		manifest.Points = append(manifest.Points, bitmapChainPoint{p.Height, p.BlockHash, p.Commitment})
	}
	if mode == "pruned" {
		for _, p := range points {
			heights = append(heights, p.Height)
		}
		heights = append(heights, h.Height)
		manifest.RetainedIntervals = bitmapChainIntervals(heights)
	} else {
		if m != nil {
			manifest.RetainedIntervals = append([]heightInterval{}, m.RetainedIntervals...)
		} else if s.head != nil {
			manifest.RetainedIntervals = []heightInterval{{s.head.SegmentStart, s.head.Height}}
		}
		manifest.RetainedIntervals = bitmapMergeChainHeight(manifest.RetainedIntervals, h.Height)
	}
	manifest.Digest = bitmapChainManifestDigest(manifest)
	if err = writeBitmapChainJSON(ctx, bitmapChainManifestPath(s.root, s.id, manifest.Digest), manifest); err != nil {
		return err
	}
	if err = syncDirectory(filepath.Join(s.dir, "retention")); err != nil {
		return err
	}
	pointer := bitmapChainManifestPointer{Current: manifest.Digest}
	if m != nil {
		pointer.Previous = m.Digest
	}
	pointer.Digest = bitmapChainPointerDigest(pointer)
	// All semantic evidence, pages and recovery receipts are durable before the
	// one visible pointer changes. Never publish after a late Off/reorg/cancel.
	if err = checkIndexRunPublication(ctx); err != nil {
		return err
	}
	if s.hashAt != nil {
		hash, err := s.hashAt(h.Height)
		if err != nil {
			return err
		}
		if hash != h.BlockHash {
			return fmt.Errorf("Bitmap Chain source changed at publication")
		}
	}
	if err = writeBitmapChainJSON(ctx, filepath.Join(s.dir, "retention.json"), pointer); err != nil {
		return err
	}
	if err = syncDirectory(s.dir); err != nil {
		return err
	}
	s.head = &h
	if mode == "pruned" {
		if maintenanceErr := s.collectBitmapChain(ctx, manifest, pointer); maintenanceErr != nil && checkIndexRunPublication(ctx) == nil {
			// Publication already succeeded. Failed optional reclamation must not
			// masquerade as a failed/unapplied block or trigger a duplicate step.
			_ = writeBitmapChainJSON(ctx, filepath.Join(s.dir, "maintenance.json"), struct {
				Generation uint64 `json:"generation"`
				Reason     string `json:"reason"`
			}{manifest.Generation, "Committed state retained; reclamation deferred: " + maintenanceErr.Error()})
		}
	}
	return nil
}

func bitmapMergeChainHeight(intervals []heightInterval, height int64) []heightInterval {
	out := append([]heightInterval{}, intervals...)
	out = append(out, heightInterval{height, height})
	sort.Slice(out, func(i, j int) bool { return out[i].From < out[j].From })
	merged := []heightInterval{}
	for _, r := range out {
		if len(merged) > 0 && r.From <= merged[len(merged)-1].To+1 {
			if r.To > merged[len(merged)-1].To {
				merged[len(merged)-1].To = r.To
			}
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}

func (s *bitmapRunConsumer) ConfigureRetention(mode string) { s.retention = mode }

func bitmapCompletedOutcomeInterval(ctx context.Context, root, id string, from, to int64, profile string) (bool, error) {
	if from > to {
		return true, nil
	}
	h, err := readBitmapDevelopmentHead(ctx, root, id)
	if err != nil || h == nil {
		return false, err
	}
	if h.ProfileHash != profile || from < h.SegmentStart || to > h.Height {
		return false, nil
	}
	for _, height := range []int64{from, to} {
		var row bitmapDevelopmentBlock
		have, e := newOrdinalTree(filepath.Join(bitmapDevelopmentDir(root, id), "pages"), h.BlockRoot).withContext(ctx).get(fmt.Sprintf("%016x", height), &row)
		if e != nil {
			return false, e
		}
		if !have || !row.BlockEvaluated || !row.CompleteScope || row.Height != height {
			return false, nil
		}
	}
	return true, nil
}

func (s *bitmapRunConsumer) collectBitmapChain(ctx context.Context, m bitmapChainManifest, p bitmapChainManifestPointer) error {
	// Caller owns both writer and snapshot leases. Preserve the previous valid
	// generation as real recovery redundancy, rather than four digest strings.
	manifests := []bitmapChainManifest{m}
	if p.Previous != "" {
		previous, err := readBitmapChainManifestAt(ctx, s.root, s.id, p.Previous)
		if err != nil {
			return err
		}
		manifests = append(manifests, *previous)
		if previous.Mode == "full" {
			return nil
		}
	}
	keep := map[string]bool{}
	keepReceipts := map[string]bool{}
	roots := []string{}
	for _, manifest := range manifests {
		commits := []string{manifest.Current}
		for _, point := range manifest.Points {
			commits = append(commits, point.Commitment)
		}
		for _, commit := range commits {
			if keep[commit] {
				continue
			}
			h, err := readBitmapDevelopmentCommit(ctx, s.root, s.id, commit)
			if err != nil {
				return err
			}
			keep[commit] = true
			if h.DistrictReceiptRef != "" {
				keepReceipts[h.DistrictReceiptRef] = true
			}
			roots = append(roots, h.BlockRoot)
		}
	}
	if err := collectOrdinalPagesContext(ctx, filepath.Join(s.dir, "pages"), func(visit func(string) error) error {
		for _, root := range roots {
			if err := visit(root); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		if errors.Is(err, errOrdinalMaintenancePending) {
			return nil
		}
		return err
	}
	if err := filepath.WalkDir(filepath.Join(s.dir, "commits"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			commit := strings.TrimSuffix(d.Name(), ".json")
			if validHash(commit) && !keep[commit] {
				return os.Remove(path)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := filepath.WalkDir(filepath.Join(s.dir, "source-receipts"), func(path string, d os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			commit := strings.TrimSuffix(d.Name(), ".json")
			if validHash(commit) && !keepReceipts[commit] {
				return os.Remove(path)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return filepath.WalkDir(filepath.Join(s.dir, "retention"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			digest := strings.TrimSuffix(d.Name(), ".json")
			if validHash(digest) && digest != p.Current && digest != p.Previous {
				return os.Remove(path)
			}
		}
		return nil
	})
}
func (s *bitmapRunConsumer) reconcileBitmapChain(ctx context.Context, hashAt func(int64) (string, error)) error {
	m, err := readBitmapChainManifest(ctx, s.root, s.id)
	if err != nil {
		return err
	}
	if m == nil {
		m = &bitmapChainManifest{Schema: 1, Index: s.id, ProfileHash: s.instance.ProfileHash, Mode: "full", Current: s.head.Commitment, RetainedIntervals: []heightInterval{{s.head.SegmentStart, s.head.Height}}}
	}
	points := append([]bitmapChainPoint{}, m.Points...)
	if m.Mode == "full" {
		for h := s.head; h != nil; {
			selected, e := hashAt(h.Height)
			if e != nil {
				return e
			}
			if selected == h.BlockHash {
				points = append(points, bitmapChainPoint{h.Height, h.BlockHash, h.Commitment})
				break
			}
			if h.Previous == "" {
				break
			}
			var nextErr error
			h, nextErr = readBitmapDevelopmentCommit(ctx, s.root, s.id, h.Previous)
			if nextErr != nil {
				if os.IsNotExist(nextErr) {
					break
				}
				return nextErr
			}
		}
	}
	if p, e := readBitmapChainPointer(ctx, s.root, s.id); e == nil && p != nil && p.Previous != "" {
		old, e := readBitmapChainManifestAt(ctx, s.root, s.id, p.Previous)
		if e != nil {
			return e
		}
		points = append(points, old.Points...)
		previous, e := readBitmapDevelopmentCommit(ctx, s.root, s.id, old.Current)
		if e != nil {
			return e
		}
		points = append(points, bitmapChainPoint{previous.Height, previous.BlockHash, previous.Commitment})
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Height > points[j].Height })
	for _, point := range points {
		hash, e := hashAt(point.Height)
		if e != nil {
			return e
		}
		if hash != point.BlockHash {
			continue
		}
		h, e := readBitmapDevelopmentCommit(ctx, s.root, s.id, point.Commitment)
		if e != nil {
			return e
		}
		if !validBitmapChainReceipt(h, s.instance) {
			d, e := readBitmapDevelopmentCommit(ctx, s.root, s.instance.DistrictInstance, h.DependencyCommitment)
			if e != nil {
				return e
			}
			h.DistrictReceipt = d
			h.DistrictInstance = s.instance.DistrictInstance
			if !validBitmapChainReceipt(h, s.instance) {
				continue
			}
			copy, e := s.persistChainCommit(ctx, *h)
			if e != nil {
				return e
			}
			h = &copy
		}
		// Restore only this Chain; the producer's independent healthy head stays.
		manifest := *m
		manifest.Generation++
		manifest.Points = nil
		for _, p := range m.Points {
			if p.Height <= h.Height {
				manifest.Points = append(manifest.Points, p)
			}
		}
		if m.Mode == "pruned" {
			tree := newOrdinalTree(filepath.Join(s.dir, "pages"), "").withContext(ctx)
			for _, p := range manifest.Points {
				point, e := readBitmapDevelopmentCommit(ctx, s.root, s.id, p.Commitment)
				if e != nil {
					return e
				}
				if e = tree.put(fmt.Sprintf("%016x", point.Height), point.LastBlock); e != nil {
					return e
				}
			}
			if e = tree.put(fmt.Sprintf("%016x", h.Height), h.LastBlock); e != nil {
				return e
			}
			h.BlockRoot, e = tree.flush()
			if e != nil {
				return e
			}
			copy, e := s.persistChainCommit(ctx, *h)
			if e != nil {
				return e
			}
			h = &copy
		}
		manifest.Current = h.Commitment
		manifest.RetainedIntervals = []heightInterval{}
		if m.Mode == "full" {
			for _, r := range m.RetainedIntervals {
				if r.From <= h.Height {
					manifest.RetainedIntervals = append(manifest.RetainedIntervals, heightInterval{r.From, min(r.To, h.Height)})
				}
			}
		} else {
			heights := []int64{h.Height}
			for _, p := range manifest.Points {
				heights = append(heights, p.Height)
			}
			manifest.RetainedIntervals = bitmapChainIntervals(heights)
		}
		manifest.Digest = bitmapChainManifestDigest(manifest)
		if e = writeBitmapChainJSON(ctx, bitmapChainManifestPath(s.root, s.id, manifest.Digest), manifest); e != nil {
			return e
		}
		if e = syncDirectory(filepath.Join(s.dir, "retention")); e != nil {
			return e
		}
		if e = checkIndexRunPublication(ctx); e != nil {
			return e
		}
		selected, e := hashAt(h.Height)
		if e != nil || selected != h.BlockHash {
			return fmt.Errorf("Chain recovery selected branch changed")
		}
		pointer := bitmapChainManifestPointer{Current: manifest.Digest, Previous: m.Digest}
		pointer.Digest = bitmapChainPointerDigest(pointer)
		if e = writeBitmapChainJSON(ctx, filepath.Join(s.dir, "retention.json"), pointer); e != nil {
			return e
		}
		if e = syncDirectory(s.dir); e != nil {
			return e
		}
		s.head = h
		return nil
	}
	return fmt.Errorf("%w: no retained selected-branch Chain restore point; explicitly review recovery replay", ErrBitmapEvidenceRequired)
}
