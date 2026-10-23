package main

// Comparisons read saved evaluator results, never Chain fingerprints as a
// substitute for results and never a source fetch or implicit index build.
import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

func bitmapHeadAtSnapshot(ctx context.Context, root, id, commit string, height int64) (*bitmapDevelopmentHead, error) {
	h, err := readBitmapDevelopmentCommit(ctx, root, id, commit)
	if err != nil {
		return nil, err
	}
	if height < h.SegmentStart || height > h.Height {
		return nil, nil
	}
	if height == h.Height {
		return h, nil
	}
	if bitmapConsumerKind(id) == "chain" {
		coverage, err := bitmapChainRetentionStatus(ctx, root, id)
		if err != nil {
			return nil, err
		}
		if !bitmapHeightRetained(coverage.RetainedIntervals, height) {
			return nil, nil
		}
		for _, point := range coverage.RestorePoints {
			if point.Height == height {
				at, err := readBitmapDevelopmentCommit(ctx, root, id, point.Commitment)
				if err != nil {
					return nil, err
				}
				var row bitmapDevelopmentBlock
				have, err := newOrdinalTree(filepath.Join(bitmapDevelopmentDir(root, id), "pages"), h.BlockRoot).withContext(ctx).get(fmt.Sprintf("%016x", height), &row)
				if err != nil {
					return nil, err
				}
				if !have || indexDigest(row) != indexDigest(at.LastBlock) {
					return nil, nil
				}
				return at, nil
			}
		}
	}
	var next bitmapDevelopmentBlock
	have, err := newOrdinalTree(filepath.Join(bitmapDevelopmentDir(root, id), "pages"), h.BlockRoot).withContext(ctx).get(fmt.Sprintf("%016x", height+1), &next)
	if err != nil {
		return nil, err
	}
	if !have || next.PreviousCommitment == "" {
		return nil, nil
	}
	at, err := readBitmapDevelopmentCommit(ctx, root, id, next.PreviousCommitment)
	if err == nil && (at.Height != height || at.ProfileHash != h.ProfileHash || at.SegmentStart != h.SegmentStart) {
		return nil, fmt.Errorf("Bitmap snapshot ancestry mismatch")
	}
	return at, err
}

type bitmapComparisonSide struct {
	State          string                    `json:"state"`
	BlockHash      string                    `json:"block_hash,omitempty"`
	WinnerIDs      []string                  `json:"ordered_winner_ids"`
	Mappings       []bitmapComparisonMapping `json:"resolved_mappings"`
	CandidateCount int                       `json:"candidate_count"`
	AuditHash      string                    `json:"audit_digest,omitempty"`
	OutcomeHash    string                    `json:"isolated_outcome_hash,omitempty"`
	Note           string                    `json:"note,omitempty"`
}
type bitmapComparisonMapping struct {
	ID          string  `json:"id"`
	District    uint64  `json:"district"`
	Transaction *uint64 `json:"transaction,omitempty"`
}
type bitmapComparisonRow struct {
	Height            int64                `json:"height"`
	State             string               `json:"state"`
	WinnerDifference  bool                 `json:"winner_difference"`
	MappingDifference bool                 `json:"mapping_difference"`
	AuditDifference   bool                 `json:"audit_difference"`
	Baseline          bitmapComparisonSide `json:"baseline"`
	Variant           bitmapComparisonSide `json:"variant"`
}
type bitmapComparisonCounts struct {
	Compared  int64 `json:"compared_blocks"`
	Differing int64 `json:"differing_blocks"`
	Winner    int64 `json:"winner_differing_blocks"`
	Mapping   int64 `json:"mapping_differing_blocks"`
	Audit     int64 `json:"audit_differing_blocks"`
	Unknown   int64 `json:"unknown_blocks"`
}
type bitmapComparisonView struct {
	Bindings           map[string]bitmapConfigurationBinding `json:"bindings"`
	State              string                                `json:"state"`
	Kind               string                                `json:"kind"`
	From               int64                                 `json:"from"`
	To                 int64                                 `json:"to"`
	CommonFrontier     int64                                 `json:"common_frontier"`
	CommonFrontierHash string                                `json:"common_frontier_hash"`
	ComparedThrough    int64                                 `json:"compared_through"`
	BaselineID         string                                `json:"baseline"`
	VariantID          string                                `json:"variant"`
	Snapshots          map[string]*bitmapDevelopmentSnapshot `json:"snapshots"`
	Lag                map[string]int64                      `json:"lag"`
	RulesDiffer        bool                                  `json:"rules_differ"`
	bitmapComparisonCounts
	Rows       []bitmapComparisonRow `json:"rows"`
	NextCursor string                `json:"next_cursor"`
	HasMore    bool                  `json:"has_more"`
	Note       string                `json:"note"`
}
type bitmapComparisonCursor struct {
	BaselineProfile, VariantProfile   string
	BaselineRevision, VariantRevision uint64
	Baseline, Variant                 string
	BaselineSnapshot, VariantSnapshot string
	From, To, Next                    int64
	Counts                            bitmapComparisonCounts
	MAC                               string
}

type bitmapConfigurationBinding struct {
	Instance              string `json:"instance"`
	ProfileHash           string `json:"profile_hash"`
	ConfigurationRevision uint64 `json:"configuration_revision"`
}

func bitmapConfigurationOf(i bitmapProfileInstance) bitmapConfigurationBinding {
	return bitmapConfigurationBinding{i.ID, i.ProfileHash, bitmapEffectiveConfigurationRevision(i)}
}

var bitmapComparisonKey struct {
	sync.Once
	value [32]byte
	err   error
}

func bitmapComparisonMAC(c bitmapComparisonCursor) (string, error) {
	bitmapComparisonKey.Do(func() { _, bitmapComparisonKey.err = rand.Read(bitmapComparisonKey.value[:]) })
	if bitmapComparisonKey.err != nil {
		return "", bitmapComparisonKey.err
	}
	c.MAC = ""
	raw, _ := json.Marshal(c)
	h := hmac.New(sha256.New, bitmapComparisonKey.value[:])
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil)), nil
}
func bitmapReadComparisonCursor(raw string) (bitmapComparisonCursor, error) {
	var c bitmapComparisonCursor
	if len(raw) > 4096 {
		return c, fmt.Errorf("comparison cursor exceeds budget")
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return c, fmt.Errorf("invalid comparison cursor")
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("invalid comparison cursor")
	}
	mac, err := bitmapComparisonMAC(c)
	if err != nil {
		return c, err
	}
	if !hmac.Equal([]byte(mac), []byte(c.MAC)) {
		return c, fmt.Errorf("comparison cursor authentication mismatch; restart comparison")
	}
	return c, nil
}

func bitmapComparisonReadSide(ctx context.Context, root, id, snapshot string, height int64, budget *int) (bitmapComparisonSide, error) {
	out := bitmapComparisonSide{State: "unknown", WinnerIDs: []string{}, Mappings: []bitmapComparisonMapping{}}
	h, err := bitmapHeadAtSnapshot(ctx, root, id, snapshot, height)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		out.Note = "Saved evidence unavailable: " + err.Error()
		return out, nil
	}
	if h == nil {
		out.Note = "Evaluation is absent or not retained."
		return out, nil
	}
	row := h.LastBlock
	out.BlockHash = h.BlockHash
	if !row.BlockEvaluated || !row.CompleteScope {
		out.Note = "This source block was not fully evaluated."
		return out, nil
	}
	out.CandidateCount = row.CandidateCount
	if row.CandidateCount > 4096 || row.CandidateCount > *budget {
		out.Note = "Candidate audit exceeds this bounded comparison page; narrow the request or inspect the block."
		return out, nil
	}
	*budget -= row.CandidateCount
	out.WinnerIDs = append(out.WinnerIDs, row.OrderedWinnerIDs...)
	if bitmapConsumerKind(id) == "district" {
		out.OutcomeHash, _ = bitmapIsolatedOutcome(out.WinnerIDs)
	}
	tree := newOrdinalTree(filepath.Join(bitmapDevelopmentDir(root, id), "pages"), h.CandidateRoot).withContext(ctx)
	before := fmt.Sprintf("%016x", height) + strings.Repeat("f", 16)
	type auditRow struct {
		ID                             string
		District                       *uint64
		Transaction                    *uint64
		Eligibility, Selection, Reason string
		ContentHash                    string
	}
	audit := []auditRow{}
	mappings := map[string]bitmapComparisonMapping{}
	for n := 0; n <= row.CandidateCount; n++ {
		var c bitmapDevelopmentCandidate
		have, e := tree.predecessor(before, &c)
		if e != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			out.Note = "Candidate evidence unavailable: " + e.Error()
			return out, nil
		}
		if !have || c.RevealHeight != height {
			break
		}
		if n == row.CandidateCount {
			out.Note = "Candidate count does not match saved block evaluation."
			return out, nil
		}
		if c.RevealHash != h.BlockHash || c.Eligibility == "unresolved" {
			out.Note = "Candidate evidence does not match completed source evaluation."
			return out, nil
		}
		if err := verifyBitmapParentEvidence(ctx, root, c.Provenance); err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			out.Note = "Parent evidence unavailable: " + err.Error()
			return out, nil
		}
		audit = append(audit, auditRow{c.ID, c.District, c.Transaction, c.Eligibility, c.Selection, c.Reason, c.ContentHash})
		if c.Selection == "selected" && c.District != nil {
			mappings[c.ID] = bitmapComparisonMapping{c.ID, *c.District, c.Transaction}
		}
		before = addressHistoryBefore(bitmapCandidateKey(c))
		if before == "" {
			break
		}
	}
	if len(audit) != row.CandidateCount {
		out.Note = "Candidate audit coverage is missing."
		return out, nil
	}
	for _, id := range out.WinnerIDs {
		m, have := mappings[id]
		if !have {
			out.Note = "A winning ID has no authenticated resolved claim record."
			return out, nil
		}
		out.Mappings = append(out.Mappings, m)
	}
	if len(mappings) != len(out.WinnerIDs) {
		out.Note = "Selected candidates and outcome IDs disagree."
		return out, nil
	}
	out.AuditHash = indexDigest(audit)
	out.State = "complete"
	return out, nil
}

func (a *app) bitmapCompareContext(ctx context.Context, baseline, variant string, from, to *int64, limit int, cursor string, after *int64, afterHash string) (bitmapComparisonView, error) {
	return a.bitmapCompareBoundContext(ctx, baseline, variant, from, to, limit, cursor, after, afterHash, nil)
}

func (a *app) bitmapCompareBoundContext(ctx context.Context, baseline, variant string, from, to *int64, limit int, cursor string, after *int64, afterHash string, expected map[string]bitmapConfigurationBinding) (bitmapComparisonView, error) {
	out := bitmapComparisonView{State: "unavailable", BaselineID: baseline, VariantID: variant, Rows: []bitmapComparisonRow{}, Snapshots: map[string]*bitmapDevelopmentSnapshot{}, Lag: map[string]int64{}, CommonFrontier: -1, ComparedThrough: -1, Note: "Compare actual saved results under declared Gateway profiles. Unknown evidence is excluded from compared counts; no work is started."}
	if limit < 1 || limit > 100 {
		return out, fmt.Errorf("comparison limit must be1..100")
	}
	b, err := bitmapResolvedInstance(ctx, a.dataDir, baseline)
	if err != nil {
		return out, err
	}
	v, err := bitmapResolvedInstance(ctx, a.dataDir, variant)
	if err != nil {
		return out, err
	}
	if b.Kind != v.Kind || b.Kind == "chain" {
		return out, fmt.Errorf("choose two District or two Parcel instances")
	}
	out.Kind = b.Kind
	out.RulesDiffer = b.ProfileHash != v.ProfileHash
	out.Bindings = map[string]bitmapConfigurationBinding{"baseline": bitmapConfigurationOf(b), "variant": bitmapConfigurationOf(v)}
	for side, binding := range expected {
		current, ok := out.Bindings[side]
		if !ok || binding.Instance != "" && binding.Instance != current.Instance || binding.ProfileHash != "" && binding.ProfileHash != current.ProfileHash || binding.ConfigurationRevision != 0 && binding.ConfigurationRevision != current.ConfigurationRevision {
			out.State = "reset_required"
			out.Note = "A selected track configuration changed. Clear the previous comparison and review its current rules."
			return out, nil
		}
	}
	var bh, vh *bitmapDevelopmentHead
	var cur bitmapComparisonCursor
	if cursor != "" {
		cur, err = bitmapReadComparisonCursor(cursor)
		if err != nil {
			return out, err
		}
		if cur.Baseline != baseline || cur.Variant != variant || from != nil && *from != cur.From || to != nil && *to != cur.To || after != nil {
			return out, fmt.Errorf("comparison cursor does not match selected instances/scope")
		}
		if cur.BaselineProfile != b.ProfileHash || cur.VariantProfile != v.ProfileHash || cur.BaselineRevision != bitmapEffectiveConfigurationRevision(b) || cur.VariantRevision != bitmapEffectiveConfigurationRevision(v) {
			out.State = "reset_required"
			out.Note = "Comparison cursor belongs to an earlier track configuration. Start a fresh comparison."
			return out, nil
		}
		bh, err = readBitmapDevelopmentCommit(ctx, a.dataDir, baseline, cur.BaselineSnapshot)
		if err == nil {
			vh, err = readBitmapDevelopmentCommit(ctx, a.dataDir, variant, cur.VariantSnapshot)
		}
	} else {
		bh, err = readBitmapDevelopmentHead(ctx, a.dataDir, baseline)
		if err == nil {
			vh, err = readBitmapDevelopmentHead(ctx, a.dataDir, variant)
		}
	}
	if err != nil {
		out.Note = "Pinned saved comparison evidence is unavailable; restart or review retained history: " + err.Error()
		return out, nil
	}
	if bh == nil || vh == nil {
		out.Note = "Both tracks need completed saved evaluations. Missing data is not an empty result."
		return out, nil
	}
	out.Snapshots["baseline"] = bitmapSnapshot(bh)
	out.Snapshots["variant"] = bitmapSnapshot(vh)
	if bh.SegmentStart != vh.SegmentStart {
		out.State = "scope_mismatch"
		out.Note = "Declared segment starts differ; cumulative outcome scopes are not comparable."
		return out, nil
	}
	authority := a.currentChainAuthority()
	for _, head := range []*bitmapDevelopmentHead{bh, vh} {
		state := a.utxoSelectedChainState(ctx, &utxoCheckpoint{Height: head.Height, BlockHash: head.BlockHash}, authority)
		if state != "selected_chain" {
			if state == "stale_reorg_resume_required" {
				out.State = "reset_required"
				out.Note = "A pinned source checkpoint left the selected branch. Clear accumulated comparison results."
			} else {
				out.State = "unavailable"
				out.Note = "Selected-source authentication is temporarily unavailable. No new results were counted; this is not evidence of a reorg."
			}
			return out, nil
		}
	}
	frontier := bh.Height
	if vh.Height < frontier {
		frontier = vh.Height
	}
	out.CommonFrontier = frontier
	out.Lag["baseline"] = vh.Height - bh.Height
	if out.Lag["baseline"] < 0 {
		out.Lag["baseline"] = 0
	}
	out.Lag["variant"] = bh.Height - vh.Height
	if out.Lag["variant"] < 0 {
		out.Lag["variant"] = 0
	}
	bf, e := bitmapHeadAtSnapshot(ctx, a.dataDir, baseline, bh.Commitment, frontier)
	if e == nil && bf != nil {
		vf, e := bitmapHeadAtSnapshot(ctx, a.dataDir, variant, vh.Commitment, frontier)
		if e == nil && vf != nil && vf.BlockHash == bf.BlockHash {
			out.CommonFrontierHash = bf.BlockHash
		}
	}
	if after != nil {
		if !validHash(afterHash) || *after < bh.SegmentStart || *after > frontier {
			return out, fmt.Errorf("incremental comparison requires a valid prior common source anchor")
		}
		for _, pair := range []struct{ id, commit string }{{baseline, bh.Commitment}, {variant, vh.Commitment}} {
			h, e := bitmapHeadAtSnapshot(ctx, a.dataDir, pair.id, pair.commit, *after)
			if e != nil || h == nil {
				out.State = "unavailable"
				out.Note = "The prior comparison frontier is unavailable or no longer retained. No new results were counted."
				return out, nil
			}
			if h.BlockHash != afterHash {
				out.State = "reset_required"
				out.Note = "Prior comparison frontier changed or is no longer retained; clear accumulated results."
				return out, nil
			}
		}
		start := *after + 1
		from = &start
	}
	start, end := bh.SegmentStart, frontier
	if from != nil {
		start = *from
	}
	if to != nil {
		end = *to
	}
	if start < bh.SegmentStart || end < start-1 || end > frontier {
		return out, fmt.Errorf("comparison scope must stay within the common completed frontier")
	}
	out.From, out.To = start, end
	if cursor == "" {
		cur = bitmapComparisonCursor{Baseline: baseline, Variant: variant, BaselineProfile: b.ProfileHash, VariantProfile: v.ProfileHash, BaselineRevision: bitmapEffectiveConfigurationRevision(b), VariantRevision: bitmapEffectiveConfigurationRevision(v), BaselineSnapshot: bh.Commitment, VariantSnapshot: vh.Commitment, From: start, To: end, Next: start}
	} else {
		start, end = cur.From, cur.To
		out.From, out.To = start, end
	}
	out.bitmapComparisonCounts = cur.Counts
	if cur.Next < start || cur.Next > end+1 {
		return out, fmt.Errorf("comparison cursor position exceeds scope")
	}
	budget := 16384
	for h := cur.Next; h <= end && len(out.Rows) < limit; h++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		left, e := bitmapComparisonReadSide(ctx, a.dataDir, baseline, bh.Commitment, h, &budget)
		if e != nil {
			return out, e
		}
		right, e := bitmapComparisonReadSide(ctx, a.dataDir, variant, vh.Commitment, h, &budget)
		if e != nil {
			return out, e
		}
		r := bitmapComparisonRow{Height: h, State: "unknown", Baseline: left, Variant: right}
		if left.State == "complete" && right.State == "complete" && left.BlockHash == right.BlockHash {
			r.State = "compared"
			r.WinnerDifference = indexDigest(left.WinnerIDs) != indexDigest(right.WinnerIDs)
			r.MappingDifference = indexDigest(left.Mappings) != indexDigest(right.Mappings)
			r.AuditDifference = left.AuditHash != right.AuditHash
			out.Compared++
			if r.WinnerDifference {
				out.Winner++
			}
			if r.MappingDifference {
				out.Mapping++
			}
			if r.AuditDifference {
				out.Audit++
			}
			if r.WinnerDifference || r.MappingDifference || r.AuditDifference {
				out.Differing++
			}
		} else {
			out.Unknown++
			if left.BlockHash != "" && right.BlockHash != "" && left.BlockHash != right.BlockHash {
				r.State = "source_mismatch"
			}
		}
		out.Rows = append(out.Rows, r)
		cur.Next = h + 1
		out.ComparedThrough = h
	}
	out.State = "complete"
	if out.Unknown > 0 {
		out.State = "partial"
	}
	// A selected branch can change while reading a page. Authenticate again
	// before exposing newly counted rows; temporary source loss is not a reorg.
	finalAuthority := a.currentChainAuthority()
	for _, head := range []*bitmapDevelopmentHead{bh, vh} {
		state := a.utxoSelectedChainState(ctx, &utxoCheckpoint{Height: head.Height, BlockHash: head.BlockHash}, finalAuthority)
		if state != "selected_chain" {
			out.Rows = []bitmapComparisonRow{}
			out.bitmapComparisonCounts = cur.Counts
			out.ComparedThrough = -1
			out.HasMore = false
			out.NextCursor = ""
			out.State = "unavailable"
			out.Note = "Source authentication changed during the page; no new rows were counted."
			if state == "stale_reorg_resume_required" {
				out.State = "reset_required"
				out.Note = "The source branch changed during comparison. Clear accumulated results."
			}
			return out, nil
		}
	}
	out.HasMore = cur.Next <= end
	if out.HasMore {
		cur.Counts = out.bitmapComparisonCounts
		cur.MAC, err = bitmapComparisonMAC(cur)
		if err != nil {
			return out, err
		}
		raw, _ := json.Marshal(cur)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out, nil
}
func bitmapCompareHeight(r *http.Request, key string) (*int64, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return nil, nil
	}
	n, ok := bitmapCanonicalDecimal(raw)
	if !ok || n > uint64(^uint64(0)>>1) {
		return nil, fmt.Errorf("invalid comparison %s", key)
	}
	v := int64(n)
	return &v, nil
}
func (a *app) handleBitmapComparison(w http.ResponseWriter, r *http.Request) {
	if !safeLocalRequest(r) {
		jsonError(w, 403, fmt.Errorf("Bitmap comparisons are restricted to the local management interface"))
		return
	}
	if r.Method != "GET" {
		jsonError(w, 405, fmt.Errorf("GET required"))
		return
	}
	if !bitmapCapabilities().Comparisons {
		jsonError(w, http.StatusForbidden, errBitmapComparisonDeferred)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	from, err := bitmapCompareHeight(r, "from")
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	to, err := bitmapCompareHeight(r, "to")
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	after, err := bitmapCompareHeight(r, "after_height")
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			jsonError(w, 400, err)
			return
		}
	}
	expected := map[string]bitmapConfigurationBinding{}
	for _, side := range []string{"baseline", "variant"} {
		revision, err := bitmapQueryConfigurationRevision(r, side+"_configuration_revision")
		if err != nil {
			jsonError(w, 400, err)
			return
		}
		profile := r.URL.Query().Get(side + "_profile")
		if profile != "" && !validHash(profile) {
			jsonError(w, 400, fmt.Errorf("invalid comparison profile binding"))
			return
		}
		if revision != 0 || profile != "" {
			expected[side] = bitmapConfigurationBinding{ProfileHash: profile, ConfigurationRevision: revision}
		}
	}
	bitmapSnapshotMu.RLock()
	defer bitmapSnapshotMu.RUnlock()
	out, err := a.bitmapCompareBoundContext(r.Context(), r.URL.Query().Get("baseline"), r.URL.Query().Get("variant"), from, to, limit, r.URL.Query().Get("cursor"), after, r.URL.Query().Get("after_hash"), expected)
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	writeJSON(w, out)
}
