package main

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

type bitmapCandidateAuditPage struct {
	ConfigurationRevision uint64                       `json:"configuration_revision"`
	State                 string                       `json:"state"`
	Instance              string                       `json:"instance"`
	ProfileHash           string                       `json:"profile_hash"`
	Snapshot              *bitmapDevelopmentSnapshot   `json:"snapshot"`
	Height                int64                        `json:"height"`
	BlockHash             string                       `json:"block_hash"`
	CandidateCount        int                          `json:"candidate_count"`
	Rows                  []bitmapDevelopmentCandidate `json:"rows"`
	NextCursor            string                       `json:"next_cursor"`
	HasMore               bool                         `json:"has_more"`
	Note                  string                       `json:"note"`
}
type bitmapCandidateAuditCursor struct {
	ConfigurationRevision       uint64
	Instance, Profile, Snapshot string
	Height                      int64
	Before                      string
	Read                        int
	MAC                         string
}

func bitmapCandidateCursorMAC(c bitmapCandidateAuditCursor) (string, error) {
	// Reuse the process-local HMAC key but distinguish the message domain.
	encoded, _ := json.Marshal(struct {
		Domain string
		Cursor bitmapCandidateAuditCursor
	}{"bitmap/candidate-audit-cursor-v1", func() bitmapCandidateAuditCursor { c.MAC = ""; return c }()})
	return bitmapComparisonMAC(bitmapComparisonCursor{Baseline: string(encoded)})
}
func (a *app) bitmapCandidateAuditContext(ctx context.Context, id, snapshot, expected string, height int64, limit int, rawCursor string) (bitmapCandidateAuditPage, error) {
	out := bitmapCandidateAuditPage{State: "unavailable", Instance: id, Height: height, Rows: []bitmapDevelopmentCandidate{}, Note: "Saved candidate decisions at one immutable snapshot; no source fetch or indexing is started."}
	if limit < 1 || limit > 100 {
		return out, fmt.Errorf("candidate audit limit must be1..100")
	}
	i, err := bitmapResolvedInstance(ctx, a.dataDir, id)
	if err != nil {
		return out, err
	}
	if i.Kind == "chain" {
		return out, fmt.Errorf("choose the District or Parcel producer for candidate audit")
	}
	out.ProfileHash = i.ProfileHash
	out.ConfigurationRevision = bitmapEffectiveConfigurationRevision(i)
	if expected != "" && expected != i.ProfileHash {
		return out, fmt.Errorf("candidate audit profile mismatch")
	}
	if !validHash(snapshot) {
		return out, fmt.Errorf("candidate audit requires a pinned snapshot")
	}
	head, err := readBitmapDevelopmentCommit(ctx, a.dataDir, id, snapshot)
	if err != nil {
		out.Note = "Pinned audit snapshot is unavailable: " + err.Error()
		return out, nil
	}
	out.Snapshot = bitmapSnapshot(head)
	authority := a.currentChainAuthority()
	if state := a.utxoSelectedChainState(ctx, &utxoCheckpoint{Height: head.Height, BlockHash: head.BlockHash}, authority); state != "selected_chain" {
		out.Note = "Pinned candidate source is not currently authenticated."
		if state == "stale_reorg_resume_required" {
			out.State = "reset_required"
		}
		return out, nil
	}
	at, err := bitmapHeadAtSnapshot(ctx, a.dataDir, id, snapshot, height)
	if err != nil {
		out.Note = "Candidate checkpoint is unavailable: " + err.Error()
		return out, nil
	}
	if at == nil {
		out.State = "not_retained"
		out.Note = "This height has no retained completed candidate evaluation."
		return out, nil
	}
	out.BlockHash = at.BlockHash
	out.CandidateCount = at.LastBlock.CandidateCount
	before := fmt.Sprintf("%016x", height) + strings.Repeat("f", 16)
	read := 0
	if rawCursor != "" {
		if len(rawCursor) > 4096 {
			return out, fmt.Errorf("candidate audit cursor exceeds budget")
		}
		raw, e := base64.RawURLEncoding.DecodeString(rawCursor)
		var c bitmapCandidateAuditCursor
		if e != nil || json.Unmarshal(raw, &c) != nil {
			return out, fmt.Errorf("invalid candidate audit cursor")
		}
		mac, e := bitmapCandidateCursorMAC(c)
		if e != nil {
			return out, e
		}
		if !hmac.Equal([]byte(mac), []byte(c.MAC)) || c.Instance != id || c.Profile != i.ProfileHash || c.ConfigurationRevision != bitmapEffectiveConfigurationRevision(i) || c.Snapshot != snapshot || c.Height != height || len(c.Before) != 32 || !ordinalKeyValid(c.Before) || !strings.HasPrefix(c.Before, fmt.Sprintf("%016x", height)) || c.Read < 0 || c.Read > out.CandidateCount {
			return out, fmt.Errorf("candidate audit cursor does not match source/profile/scope")
		}
		before, read = c.Before, c.Read
	}
	tree := newOrdinalTree(filepath.Join(bitmapDevelopmentDir(a.dataDir, id), "pages"), at.CandidateRoot).withContext(ctx)
	last := ""
	for n := 0; n <= limit; n++ {
		var c bitmapDevelopmentCandidate
		have, e := tree.predecessor(before, &c)
		if e != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			out.Rows = []bitmapDevelopmentCandidate{}
			out.Note = "Saved candidate evidence is unavailable: " + e.Error()
			return out, nil
		}
		if !have || c.RevealHeight != height {
			break
		}
		if c.RevealHash != at.BlockHash || c.Kind != i.Kind || c.Eligibility == "unresolved" {
			out.Rows = []bitmapDevelopmentCandidate{}
			out.Note = "Candidate row does not match the completed source evaluation."
			return out, nil
		}
		if n == limit {
			if read >= out.CandidateCount {
				out.Rows = []bitmapDevelopmentCandidate{}
				out.Note = "More candidate rows exist than the completed block count."
				return out, nil
			}
			out.HasMore = true
			break
		}
		if err := verifyBitmapParentEvidence(ctx, a.dataDir, c.Provenance); err != nil {
			out.Rows = []bitmapDevelopmentCandidate{}
			out.Note = "Candidate dependency evidence is unavailable: " + err.Error()
			return out, nil
		}
		out.Rows = append(out.Rows, c)
		read++
		last = bitmapCandidateKey(c)
		before = addressHistoryBefore(last)
		if before == "" {
			break
		}
	}
	if read > out.CandidateCount || !out.HasMore && read != out.CandidateCount {
		out.Rows = []bitmapDevelopmentCandidate{}
		out.Note = "Retained candidate coverage does not match the completed block count."
		return out, nil
	}
	if out.HasMore {
		c := bitmapCandidateAuditCursor{Instance: id, Profile: i.ProfileHash, ConfigurationRevision: bitmapEffectiveConfigurationRevision(i), Snapshot: snapshot, Height: height, Before: addressHistoryBefore(last), Read: read}
		c.MAC, err = bitmapCandidateCursorMAC(c)
		if err != nil {
			return out, err
		}
		raw, _ := json.Marshal(c)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	state := a.utxoSelectedChainState(ctx, &utxoCheckpoint{Height: head.Height, BlockHash: head.BlockHash}, a.currentChainAuthority())
	if state != "selected_chain" {
		out.Rows = []bitmapDevelopmentCandidate{}
		out.NextCursor = ""
		out.HasMore = false
		out.Note = "Source authentication changed while reading candidates."
		if state == "stale_reorg_resume_required" {
			out.State = "reset_required"
		}
		return out, nil
	}
	out.State = "complete"
	return out, nil
}
func (a *app) handleBitmapCandidateAudit(w http.ResponseWriter, r *http.Request) {
	if !safeLocalRequest(r) {
		jsonError(w, 403, fmt.Errorf("Bitmap candidate audit is restricted to the local management interface"))
		return
	}
	if r.Method != "GET" {
		jsonError(w, 405, fmt.Errorf("GET required"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	height, err := bitmapCompareHeight(r, "height")
	if err != nil || height == nil {
		jsonError(w, 400, fmt.Errorf("candidate audit height required"))
		return
	}
	limit := 25
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			jsonError(w, 400, err)
			return
		}
	}
	bitmapSnapshotMu.RLock()
	defer bitmapSnapshotMu.RUnlock()
	out, err := a.bitmapCandidateAuditContext(r.Context(), r.URL.Query().Get("instance"), r.URL.Query().Get("snapshot"), r.URL.Query().Get("expected_profile"), *height, limit, r.URL.Query().Get("cursor"))
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	expectedRevision, err := bitmapQueryConfigurationRevision(r, "expected_configuration_revision")
	if err != nil || expectedRevision != 0 && expectedRevision != out.ConfigurationRevision {
		jsonError(w, 400, fmt.Errorf("candidate audit configuration changed; refresh the selected track"))
		return
	}
	writeJSON(w, out)
}
