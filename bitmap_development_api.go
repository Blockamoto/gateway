package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type bitmapDevelopmentSnapshot struct {
	Height             int64  `json:"height"`
	BlockHash          string `json:"block_hash"`
	Commitment         string `json:"commitment"`
	ProfileID          string `json:"profile_id"`
	ProfileHash        string `json:"profile_hash"`
	SegmentStart       int64  `json:"segment_start"`
	Verification       string `json:"verification"`
	ConsensusValidated bool   `json:"consensus_validated"`
}
type bitmapDevelopmentView struct {
	State             string                       `json:"state"`
	Kind              string                       `json:"kind"`
	Snapshot          *bitmapDevelopmentSnapshot   `json:"snapshot"`
	Profile           bitmapDevelopmentProfile     `json:"profile"`
	ProfileHash       string                       `json:"profile_hash"`
	Coverage          []heightInterval             `json:"coverage"`
	Gaps              []heightInterval             `json:"gaps"`
	Result            any                          `json:"result"`
	Candidates        []bitmapDevelopmentCandidate `json:"candidates"`
	Rows              []any                        `json:"rows"`
	PendingCandidates []bitmapDevelopmentCandidate `json:"pending_candidates,omitempty"`
	NextCursor        string                       `json:"next_cursor"`
	HasMore           bool                         `json:"has_more"`
	BlockEvaluated    bool                         `json:"block_evaluated"`
	OrderedWinnerIDs  []string                     `json:"ordered_winner_ids"`
	IsolatedHash      string                       `json:"isolated_outcome_hash,omitempty"`
	ChainHash         string                       `json:"outcome_chain_hash,omitempty"`
	ScopedCommitment  *string                      `json:"bitmap_chain_commitment"`
	CompleteScope     bool                         `json:"complete_scope"`
	Ownership         string                       `json:"ownership"`
	Note              string                       `json:"note"`
}
type bitmapDevelopmentCursor struct {
	Index      string `json:"index"`
	Commitment string `json:"commitment"`
	Target     string `json:"target"`
	Before     string `json:"before"`
	Digest     string `json:"digest"`
}

func bitmapCursorDigest(c bitmapDevelopmentCursor) string { c.Digest = ""; return indexDigest(c) }
func bitmapSnapshot(h *bitmapDevelopmentHead) *bitmapDevelopmentSnapshot {
	if h == nil {
		return nil
	}
	return &bitmapDevelopmentSnapshot{Height: h.Height, BlockHash: h.BlockHash, Commitment: h.Commitment, ProfileID: h.Profile.ID, ProfileHash: h.ProfileHash, SegmentStart: h.SegmentStart, Verification: "locally_evaluated_experimental_profile; header_anchored; full_consensus_deferred"}
}
func readBitmapPending(ctx context.Context, root, id string, h *bitmapDevelopmentHead) (*bitmapDevelopmentPending, error) {
	var p bitmapDevelopmentPending
	err := readBitmapJSON(ctx, filepath.Join(bitmapDevelopmentDir(root, id), "pending.json"), &p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	previous := ""
	height := bitmapDevelopmentStart
	if h != nil {
		previous = h.Commitment
		height = h.Height + 1
	}
	if p.Schema != 1 || p.Index != id || p.Digest != bitmapPendingDigest(p) || p.ProfileHash != bitmapProfileHash(bitmapProfile(bitmapConsumerKind(id))) {
		return nil, fmt.Errorf("Bitmap pending evidence authentication mismatch")
	}
	if p.Previous != previous || p.Height != height {
		return nil, nil
	}
	return &p, nil
}

func bitmapNewView(kind string) bitmapDevelopmentView {
	p := bitmapProfile(kind)
	return bitmapDevelopmentView{State: "not_built", Kind: kind, Profile: p, ProfileHash: bitmapProfileHash(p), Coverage: []heightInterval{}, Gaps: []heightInterval{}, Candidates: []bitmapDevelopmentCandidate{}, Rows: []any{}, OrderedWinnerIDs: []string{}, Ownership: "unknown", Note: "Saved development-profile evidence only. Opening this view starts no index, trace or block scan. The canonical Bitmap semantic profile is not frozen."}
}
func (a *app) bitmapDevelopmentViewContext(ctx context.Context, kind string, district, transaction, height *uint64, cursor string, limit int, records bool) (bitmapDevelopmentView, error) {
	out := bitmapNewView(kind)
	id := "bitmap-districts"
	if kind == "parcel" {
		id = "bitmap-parcels"
	} else if kind == "chain" {
		id = "bitmap-chain"
	} else if kind != "district" {
		return out, fmt.Errorf("Bitmap kind must be district, parcel or chain")
	}
	if err := requireReleaseFeature(id); err != nil {
		return out, err
	}
	if limit < 1 || limit > 100 {
		return out, fmt.Errorf("Bitmap page limit must be 1..100")
	}
	h, err := readBitmapDevelopmentHead(ctx, a.dataDir, id)
	if err != nil {
		out.State = "unavailable"
		out.Note = err.Error()
		return out, nil
	}
	pending, err := readBitmapPending(ctx, a.dataDir, id, h)
	if err != nil {
		out.State = "unavailable"
		out.Note = err.Error()
		return out, nil
	}
	authority := a.currentChainAuthority()
	if h != nil {
		if a.utxoSelectedChainState(ctx, &utxoCheckpoint{Height: h.Height, BlockHash: h.BlockHash}, authority) != "selected_chain" {
			out.State = "stale"
			out.Snapshot = bitmapSnapshot(h)
			out.Note = "Saved Bitmap source left the selected chain or its anchor is unavailable. Resume reconciliation; no query work was started."
			return out, nil
		}
		out.State = "ready"
		out.Snapshot = bitmapSnapshot(h)
		out.Coverage = []heightInterval{{h.SegmentStart, h.Height}}
		out.CompleteScope = true
		out.Result = map[string]any{"winner_count": h.WinnerCount, "candidate_count": h.CandidateCount}
	}
	if pending != nil {
		target, e := a.utxoSelectedTarget(ctx, authority, pending.Height)
		if e == nil && target.HashDisplay == pending.BlockHash {
			out.PendingCandidates = append(out.PendingCandidates, pending.Candidates...)
			out.Gaps = []heightInterval{{pending.Height, pending.Height}}
			out.Note = pending.Note
			if h == nil {
				out.State = "unresolved"
			}
		} else {
			pending = nil
		}
	}
	if h == nil {
		return out, nil
	}
	tree := func(root string) *ordinalTree {
		return newOrdinalTree(filepath.Join(bitmapDevelopmentDir(a.dataDir, id), "pages"), root).withContext(ctx)
	}
	if !records && kind == "chain" {
		wanted := uint64(h.Height)
		if height != nil {
			wanted = *height
		}
		if wanted > uint64(h.Height) || wanted < uint64(h.SegmentStart) {
			out.State = "unavailable"
			out.CompleteScope = false
			out.Note = "This block has no completed outcome in saved contiguous chain coverage."
			if pending != nil && uint64(pending.Height) == wanted {
				out.State = "unresolved"
			}
			return out, nil
		}
		var row bitmapDevelopmentBlock
		have, e := tree(h.BlockRoot).get(fmt.Sprintf("%016x", wanted), &row)
		if e != nil {
			return out, e
		}
		if !have {
			return out, fmt.Errorf("completed Bitmap outcome is missing")
		}
		out.Result = row
		out.BlockEvaluated = row.BlockEvaluated
		out.OrderedWinnerIDs = row.OrderedWinnerIDs
		out.IsolatedHash = row.IsolatedHash
		out.ChainHash = row.ChainHash
		out.ScopedCommitment = row.ScopedCommitment
		out.CompleteScope = row.CompleteScope
		out.Note = row.Note
		return out, nil
	}
	target := ""
	if district != nil {
		if kind == "parcel" && transaction == nil {
			return out, fmt.Errorf("Parcel transaction coordinate required")
		}
		target = bitmapTargetKey(*district, transaction)
	}
	if !records && target != "" {
		var winner bitmapDevelopmentCandidate
		have, e := tree(h.WinnerRoot).get(target, &winner)
		if e != nil {
			return out, e
		}
		out.State = "unclaimed_at_snapshot"
		out.Result = map[string]any{"district": district, "transaction": transaction, "winner_id": nil, "ownership": "unknown"}
		if have {
			if err := verifyBitmapParentEvidence(ctx, a.dataDir, winner.Provenance); err != nil {
				return out, err
			}
			out.State = "selected"
			out.Result = map[string]any{"district": district, "transaction": transaction, "winner_id": winner.ID, "winner": winner, "ownership": "unknown"}
		} else if pending != nil {
			for _, row := range out.PendingCandidates {
				if row.District == nil || *row.District == *district && (transaction == nil || row.Transaction != nil && *row.Transaction == *transaction) {
					out.State = "unresolved"
					break
				}
			}
		}
	}
	root, before := h.CandidateRoot, strings.Repeat("f", 32)
	if target != "" {
		root = h.TargetRoot
		before = target + strings.Repeat("f", 32)
	}
	if kind == "chain" {
		root = h.BlockRoot
		before = strings.Repeat("f", 16)
	}
	if cursor != "" {
		if len(cursor) > 2048 {
			return out, fmt.Errorf("Bitmap cursor exceeds budget")
		}
		raw, e := base64.RawURLEncoding.DecodeString(cursor)
		var c bitmapDevelopmentCursor
		if e != nil || json.Unmarshal(raw, &c) != nil || c.Index != id || c.Commitment != h.Commitment || c.Target != target || c.Digest != bitmapCursorDigest(c) || !ordinalKeyValid(c.Before) {
			return out, fmt.Errorf("Bitmap cursor does not match resource/checkpoint")
		}
		before = c.Before
	}
	t := tree(root)
	last := ""
	for i := 0; i <= limit; i++ {
		if kind == "chain" {
			var row bitmapDevelopmentBlock
			have, e := t.predecessor(before, &row)
			if e != nil {
				return out, e
			}
			if !have {
				break
			}
			key := fmt.Sprintf("%016x", row.Height)
			if i == limit {
				out.HasMore = true
				break
			}
			out.Rows = append(out.Rows, row)
			last = key
			before = addressHistoryBefore(key)
		} else {
			var row bitmapDevelopmentCandidate
			have, e := t.predecessor(before, &row)
			if e != nil {
				return out, e
			}
			if !have {
				break
			}
			key := bitmapCandidateKey(row)
			if target != "" {
				if row.District == nil || bitmapTargetKey(*row.District, row.Transaction) != target {
					break
				}
				key = target + key
			}
			if key > before {
				return out, fmt.Errorf("Bitmap row order mismatch")
			}
			if i == limit {
				out.HasMore = true
				break
			}
			out.Rows = append(out.Rows, row)
			if err := verifyBitmapParentEvidence(ctx, a.dataDir, row.Provenance); err != nil {
				return out, err
			}
			out.Candidates = append(out.Candidates, row)
			last = key
			before = addressHistoryBefore(key)
		}
		if before == "" {
			break
		}
	}
	if out.HasMore {
		c := bitmapDevelopmentCursor{Index: id, Commitment: h.Commitment, Target: target, Before: addressHistoryBefore(last)}
		c.Digest = bitmapCursorDigest(c)
		raw, _ := json.Marshal(c)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	latest, e := readBitmapDevelopmentHead(ctx, a.dataDir, id)
	if e != nil {
		return out, e
	}
	if latest == nil || latest.Commitment != h.Commitment {
		return out, fmt.Errorf("Bitmap checkpoint changed; restart from the first page")
	}
	return out, nil
}
func bitmapQueryNumber(r *http.Request, key string) (*uint64, error) {
	value := r.URL.Query().Get(key)
	if value == "" {
		return nil, nil
	}
	n, ok := bitmapCanonicalDecimal(value)
	if !ok {
		return nil, fmt.Errorf("%s must be a canonical unsigned integer", key)
	}
	return &n, nil
}
func (a *app) registerBitmapDevelopmentRoutes(mux *http.ServeMux) {
	for _, path := range []string{"/api/v1/bitmap/view", "/api/v1/bitmap/records"} {
		mux.HandleFunc(path, a.handleBitmapDevelopmentView)
	}
}
func (a *app) handleBitmapDevelopmentView(w http.ResponseWriter, r *http.Request) {
	if !safeLocalRequest(r) {
		jsonError(w, 403, fmt.Errorf("Bitmap development evidence is restricted to the local management interface"))
		return
	}
	if r.Method != "GET" {
		jsonError(w, 405, fmt.Errorf("GET required"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "district"
	}
	district, err := bitmapQueryNumber(r, "district")
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	transaction, err := bitmapQueryNumber(r, "transaction")
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	height, err := bitmapQueryNumber(r, "height")
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
	result, err := a.bitmapDevelopmentViewContext(r.Context(), kind, district, transaction, height, r.URL.Query().Get("cursor"), limit, strings.HasSuffix(r.URL.Path, "/records"))
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	writeJSON(w, result)
}
func (a *app) bitmapDevelopmentInstancesContext(ctx context.Context) ([]indexInstance, error) {
	out := []indexInstance{}
	for _, id := range []string{"bitmap-districts", "bitmap-parcels", "bitmap-chain"} {
		h, err := readBitmapDevelopmentHead(ctx, a.dataDir, id)
		if err != nil {
			out = append(out, indexInstance{Definition: id, Version: 1, Network: "bitcoin-mainnet", Coverage: []heightInterval{}, Gaps: []heightInterval{}, Completeness: "unavailable", Queryable: false, Note: "Saved Bitmap evidence is unavailable: " + err.Error()})
			continue
		}
		if h == nil {
			continue
		}
		s := bitmapRunConsumer{id: id, head: h}
		state := a.utxoSelectedChainState(ctx, &utxoCheckpoint{Height: h.Height, BlockHash: h.BlockHash}, a.currentChainAuthority())
		out = append(out, indexInstance{Definition: id, Version: 1, Network: "bitcoin-mainnet", Coverage: []heightInterval{{h.SegmentStart, h.Height}}, Gaps: []heightInterval{}, Completeness: "complete_contiguous_evaluation_under_experimental_profile", Verification: "header_anchored_processing; " + state + "; consensus_validated=false", Provenance: "locally_derived_development_profile", Retention: "pruned", Queryable: state == "selected_chain", Checkpoint: s.Checkpoint(), Note: "Explicit Gateway development profile, not a frozen canonical Bitmap standard. Ownership is unknown; unresolved evidence never advances coverage."})
	}
	return out, nil
}
