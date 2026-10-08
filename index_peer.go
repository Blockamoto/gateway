package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	indexPeerSchema        = 1
	maxIndexPeerBlocks     = 4
	maxIndexPeerPageBytes  = 48 * 1024
	maxIndexPeerBatchBytes = 32 * 1024
	indexPeerClaimState    = "peer_claim_replay_required"
)

// Publication is a frozen opt-in snapshot. Building more private blocks does
// not expand it. The secret only authenticates local pagination cursors; it is
// never sent to peers and does not attest Bitcoin or index validity.
type indexPublication struct {
	Manifest indexPeerManifest `json:"manifest"`
	Secret   string            `json:"cursor_secret"`
}

type indexPeerManifest struct {
	Schema        int              `json:"schema"`
	Definition    string           `json:"definition"`
	Version       int              `json:"version"`
	Network       string           `json:"network"`
	RuleHash      string           `json:"rule_hash"`
	OutputSchema  int              `json:"output_schema"`
	Coverage      []heightInterval `json:"coverage"`
	Checkpoint    indexCheckpoint  `json:"checkpoint"`
	MaxPageBlocks int              `json:"max_page_blocks"`
	MaxPageBytes  int              `json:"max_page_bytes"`
	MaxBatchBytes int              `json:"max_batch_bytes"`
	RecordPolicy  string           `json:"record_policy"`
	Verification  string           `json:"verification"`
}

type indexPeerAnchor struct {
	Commitment string `json:"commitment"`
	Height     int64  `json:"height"`
	BlockHash  string `json:"block_hash"`
}

type indexPeerRangeRequest struct {
	Index      string           `json:"index"`
	RuleHash   string           `json:"rule_hash"`
	Checkpoint string           `json:"checkpoint"` // exact published snapshot
	From       int64            `json:"from"`
	To         int64            `json:"to"`
	Cursor     string           `json:"cursor,omitempty"`
	Anchor     *indexPeerAnchor `json:"anchor,omitempty"` // from the preceding checked page
}

type indexPeerPage struct {
	Manifest     indexPeerManifest `json:"manifest"`
	From         int64             `json:"from"`
	To           int64             `json:"to"`
	Batches      []indexBatch      `json:"batches"` // newest first, whole committed batches
	Next         *indexPeerAnchor  `json:"next,omitempty"`
	Cursor       string            `json:"cursor,omitempty"`
	Verification string            `json:"verification"`
}

type indexPeerCursor struct {
	Snapshot string          `json:"snapshot"`
	Anchor   indexPeerAnchor `json:"anchor"`
}

func readIndexPeerJSON(path string, limit int64, out any) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("index object unavailable")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return fmt.Errorf("index object could not be read")
	}
	if int64(len(b)) > limit {
		return fmt.Errorf("index object exceeds transfer budget; use local Bitcoin replay")
	}
	return json.Unmarshal(b, out)
}

func validIndexPeerCheckpoint(c indexCheckpoint, d indexDefinition) bool {
	if c.Schema != d.CheckpointSchema || c.Definition != d.ID || c.Version != d.Version || c.Network != d.Network || c.RuleHash != d.RuleHash || c.From < d.StartHeight || c.Height < c.From || !validHash(c.BlockHash) || !validHash(c.RecordsHash) || !validHash(c.DependencyFingerprint) || !validHash(c.Commitment) || checkpointHash(c) != c.Commitment {
		return false
	}
	if !d.ArbitraryStart && c.From != d.StartHeight {
		return false
	}
	if c.Height == c.From {
		return c.PreviousCommitment == ""
	}
	return validHash(c.PreviousCommitment)
}

func validateIndexPeerManifest(m indexPeerManifest) error {
	d, err := findIndexDefinition(m.Definition)
	if err != nil || !d.Buildable || m.Schema != indexPeerSchema || m.Version != d.Version || m.Network != d.Network || m.RuleHash != d.RuleHash || m.OutputSchema != d.OutputSchema || !validIndexPeerCheckpoint(m.Checkpoint, d) || len(m.Coverage) != 1 || m.Coverage[0].From != m.Checkpoint.From || m.Coverage[0].To != m.Checkpoint.Height || m.MaxPageBlocks != maxIndexPeerBlocks || m.MaxPageBytes != maxIndexPeerPageBytes || m.MaxBatchBytes != maxIndexPeerBatchBytes || m.RecordPolicy != "whole_batches_newest_first" {
		return fmt.Errorf("unsupported or inconsistent index manifest fingerprint, schema or coverage")
	}
	return nil
}

func indexManifestForCheckpoint(d indexDefinition, c indexCheckpoint) indexPeerManifest {
	return indexPeerManifest{Schema: indexPeerSchema, Definition: d.ID, Version: d.Version, Network: d.Network, RuleHash: d.RuleHash, OutputSchema: d.OutputSchema, Coverage: []heightInterval{{From: c.From, To: c.Height}}, Checkpoint: c, MaxPageBlocks: maxIndexPeerBlocks, MaxPageBytes: maxIndexPeerPageBytes, MaxBatchBytes: maxIndexPeerBatchBytes, RecordPolicy: "whole_batches_newest_first", Verification: indexPeerClaimState}
}

func (a *app) indexCheckpointCurrent(c indexCheckpoint) bool {
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	if core := inspectCore(settings); core.Connected {
		loc, err := coreBlockLocationByHeight(settings, c.Height)
		return err == nil && strings.EqualFold(loc.BlockHash, c.BlockHash)
	}
	return a.headerMatches(c.Height, c.BlockHash)
}

func (a *app) setIndexPublication(id string, published bool, expected ...string) (indexPeerManifest, error) {
	if err := requireReleaseFeature("gateway-peerhood"); err != nil {
		return indexPeerManifest{}, err
	}
	return a.storeIndexPublication(id, published, expected...)
}

// Store/encode helpers are retained for future peerhood validation. Public
// publication and serving entries enforce release availability above them.
func (a *app) storeIndexPublication(id string, published bool, expected ...string) (indexPeerManifest, error) {
	if published && id != "inscriptions" && id != "bitmap" {
		return indexPeerManifest{}, fmt.Errorf("Bitcoin derived index publication is not supported by this peer format; local reads remain available")
	}
	d, err := findIndexDefinition(id)
	if err != nil || !d.Buildable {
		return indexPeerManifest{}, fmt.Errorf("index is not publishable")
	}
	path := filepath.Join(a.dataDir, "indexes", id, "publication.json")
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	if !published {
		err = os.Remove(path)
		if os.IsNotExist(err) {
			return indexPeerManifest{}, nil
		}
		if err == nil {
			err = syncDirectory(filepath.Dir(path))
		}
		return indexPeerManifest{}, err
	}
	s, err := indexStoreHead(a.dataDir, id)
	if err != nil {
		return indexPeerManifest{}, err
	}
	if id == "bitmap" && s.head.Mode == "lean" {
		return indexPeerManifest{}, fmt.Errorf("lean Bitmap publication requires a compatible peer format; keep this instance local or use full mode for peer sharing")
	}
	if s.checkpoint == nil {
		return indexPeerManifest{}, fmt.Errorf("index has no committed snapshot")
	}
	if len(expected) > 1 || len(expected) == 1 && expected[0] != s.checkpoint.Commitment {
		return indexPeerManifest{}, fmt.Errorf("index checkpoint changed; review the current snapshot before publishing")
	}
	if !a.indexCheckpointCurrent(*s.checkpoint) {
		return indexPeerManifest{}, fmt.Errorf("index checkpoint is stale or lacks current chain authority")
	}
	m := indexManifestForCheckpoint(d, *s.checkpoint)
	if err := validateIndexPeerManifest(m); err != nil {
		return indexPeerManifest{}, err
	}
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		return indexPeerManifest{}, err
	}
	err = atomicWriteJSON(path, indexPublication{Manifest: m, Secret: hex.EncodeToString(secret[:])})
	if err == nil {
		err = syncDirectory(filepath.Dir(path))
	}
	return m, err
}

func (a *app) readIndexPublication(id string) (indexPublication, error) {
	var p indexPublication
	d, err := findIndexDefinition(id)
	if err != nil || !d.Buildable {
		return p, fmt.Errorf("unsupported index")
	}
	if err = readIndexPeerJSON(filepath.Join(a.dataDir, "indexes", id, "publication.json"), 12*1024, &p); err != nil {
		return p, err
	}
	if err = validateIndexPeerManifest(p.Manifest); err != nil {
		return p, err
	}
	if p.Manifest.Definition != id || !validHash(p.Secret) {
		return p, fmt.Errorf("invalid publication")
	}
	if info, err := os.Stat(filepath.Join(a.dataDir, "indexes", id, "commits", p.Manifest.Checkpoint.Commitment+".json")); err != nil || !info.Mode().IsRegular() {
		return p, fmt.Errorf("published checkpoint data is unavailable")
	}
	if !a.indexCheckpointCurrent(p.Manifest.Checkpoint) {
		return p, fmt.Errorf("published checkpoint is stale or lacks current chain authority")
	}
	return p, nil
}

func (a *app) indexPublicationMatches(id string, checkpoint *indexCheckpoint) bool {
	if !releaseFeatureAvailable("gateway-peerhood") {
		return false
	}
	return a.storedIndexPublicationMatches(id, checkpoint)
}

func (a *app) storedIndexPublicationMatches(id string, checkpoint *indexCheckpoint) bool {
	if checkpoint == nil {
		return false
	}
	a.settingsMu.RLock()
	serving := a.settings.ServeGatewayData
	a.settingsMu.RUnlock()
	if !serving {
		return false
	}
	p, err := a.readIndexPublication(id)
	return err == nil && p.Manifest.Checkpoint.Commitment == checkpoint.Commitment
}

func (a *app) publishedIndexManifests() []indexPeerManifest {
	if !releaseFeatureAvailable("gateway-peerhood") {
		return nil
	}
	return a.readPublishedIndexManifests()
}
func (a *app) readPublishedIndexManifests() []indexPeerManifest {
	out := []indexPeerManifest{}
	a.settingsMu.RLock()
	serving := a.settings.ServeGatewayData
	a.settingsMu.RUnlock()
	if !serving {
		return out
	}
	for _, id := range []string{"inscriptions", "bitmap"} {
		if p, err := a.readIndexPublication(id); err == nil {
			out = append(out, p.Manifest)
		}
	}
	return out
}

func signIndexPeerCursor(p indexPublication, anchor indexPeerAnchor) string {
	raw, _ := json.Marshal(indexPeerCursor{Snapshot: p.Manifest.Checkpoint.Commitment, Anchor: anchor})
	secret, _ := hex.DecodeString(p.Secret)
	mac := hmac.New(sha256.New, secret)
	mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + hex.EncodeToString(mac.Sum(nil))
}

func openIndexPeerCursor(p indexPublication, token string) (indexPeerAnchor, error) {
	var c indexPeerCursor
	if len(token) > 1024 {
		return c.Anchor, fmt.Errorf("cursor exceeds budget")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return c.Anchor, fmt.Errorf("invalid cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return c.Anchor, err
	}
	sig, err := hex.DecodeString(parts[1])
	if err != nil {
		return c.Anchor, err
	}
	secret, _ := hex.DecodeString(p.Secret)
	mac := hmac.New(sha256.New, secret)
	mac.Write(raw)
	if !hmac.Equal(sig, mac.Sum(nil)) || json.Unmarshal(raw, &c) != nil || c.Snapshot != p.Manifest.Checkpoint.Commitment {
		return c.Anchor, fmt.Errorf("unauthenticated or stale cursor")
	}
	return c.Anchor, nil
}

func indexPeerExpectedAnchor(m indexPeerManifest, r indexPeerRangeRequest) (indexPeerAnchor, error) {
	if err := validateIndexPeerManifest(m); err != nil {
		return indexPeerAnchor{}, err
	}
	if r.Index != m.Definition || r.RuleHash != m.RuleHash || r.Checkpoint != m.Checkpoint.Commitment || r.From < m.Checkpoint.From || r.To > m.Checkpoint.Height || r.To < r.From || r.To-r.From >= maxIndexPeerBlocks || len(r.Cursor) > 1024 {
		return indexPeerAnchor{}, fmt.Errorf("index range or snapshot fingerprint mismatch")
	}
	if r.Cursor == "" {
		if r.To != m.Checkpoint.Height || r.Anchor != nil {
			return indexPeerAnchor{}, fmt.Errorf("initial page must start at published checkpoint")
		}
		return indexPeerAnchor{Commitment: m.Checkpoint.Commitment, Height: m.Checkpoint.Height, BlockHash: m.Checkpoint.BlockHash}, nil
	}
	if r.Anchor == nil || r.Anchor.Height != r.To || !validHash(r.Anchor.Commitment) || !validHash(r.Anchor.BlockHash) {
		return indexPeerAnchor{}, fmt.Errorf("continuation requires the preceding checked page anchor")
	}
	return *r.Anchor, nil
}

func readIndexPeerBatch(root string, d indexDefinition, commit string) (indexBatch, error) {
	var b indexBatch
	if !validHash(commit) {
		return b, fmt.Errorf("invalid commitment")
	}
	if err := readIndexPeerJSON(filepath.Join(root, "indexes", d.ID, "commits", commit+".json"), maxIndexPeerBatchBytes, &b); err != nil {
		return b, err
	}
	if b.Checkpoint.Commitment != commit || !validIndexPeerCheckpoint(b.Checkpoint, d) || batchRecordsHash(b) != b.Checkpoint.RecordsHash {
		return b, fmt.Errorf("index batch integrity failure")
	}
	return b, nil
}

func (a *app) serveIndexPeerPage(r indexPeerRangeRequest) (indexPeerPage, error) {
	if err := requireReleaseFeature("gateway-peerhood"); err != nil {
		return indexPeerPage{}, err
	}
	return a.readIndexPeerPage(r)
}
func (a *app) readIndexPeerPage(r indexPeerRangeRequest) (indexPeerPage, error) {
	var page indexPeerPage
	a.settingsMu.RLock()
	serving := a.settings.ServeGatewayData
	a.settingsMu.RUnlock()
	if !serving {
		return page, fmt.Errorf("index serving is disabled")
	}
	p, err := a.readIndexPublication(r.Index)
	if err != nil {
		return page, err
	}
	expected, err := indexPeerExpectedAnchor(p.Manifest, r)
	if err != nil {
		return page, err
	}
	if r.Cursor != "" {
		anchor, e := openIndexPeerCursor(p, r.Cursor)
		if e != nil || anchor != expected {
			return page, fmt.Errorf("invalid publication cursor")
		}
	}
	d, _ := findIndexDefinition(r.Index)
	page = indexPeerPage{Manifest: p.Manifest, From: r.From, To: r.To, Verification: indexPeerClaimState, Batches: []indexBatch{}}
	for h := r.To; h >= r.From; h-- {
		b, e := readIndexPeerBatch(a.dataDir, d, expected.Commitment)
		if e != nil {
			return indexPeerPage{}, e
		}
		if b.Checkpoint.Height != h || b.Checkpoint.From != p.Manifest.Checkpoint.From || b.Checkpoint.BlockHash != expected.BlockHash {
			return indexPeerPage{}, fmt.Errorf("missing range or checkpoint discontinuity")
		}
		page.Batches = append(page.Batches, b)
		expected = indexPeerAnchor{Commitment: b.Checkpoint.PreviousCommitment, Height: h - 1, BlockHash: b.PreviousBlockHash}
	}
	if r.From > p.Manifest.Checkpoint.From {
		page.Next = &expected
		page.Cursor = signIndexPeerCursor(p, expected)
	}
	raw, e := json.Marshal(page)
	if e != nil || len(raw) > maxIndexPeerPageBytes {
		return indexPeerPage{}, fmt.Errorf("index page exceeds transfer budget; request fewer blocks or use local Bitcoin replay")
	}
	// Also apply receiver checks to our own emitted page, retaining local records.
	if _, e = validateIndexPeerPage(p.Manifest, r, page); e != nil {
		return indexPeerPage{}, e
	}
	return page, nil
}

// This validates bounded structural claims only. It cannot authenticate the
// underlying Bitcoin or prove first-claim history; it performs no persistence.
// A continuation request must carry Next/ Cursor from a previously checked page.
func validateIndexPeerPage(manifest indexPeerManifest, r indexPeerRangeRequest, page indexPeerPage) (indexPeerPage, error) {
	expected, err := indexPeerExpectedAnchor(manifest, r)
	if err != nil {
		return indexPeerPage{}, err
	}
	raw, err := json.Marshal(page)
	if err != nil || len(raw) > maxIndexPeerPageBytes {
		return indexPeerPage{}, fmt.Errorf("index page exceeds budget")
	}
	if err = validateIndexPeerManifest(page.Manifest); err != nil {
		return indexPeerPage{}, err
	}
	if indexDigest(manifest) != indexDigest(page.Manifest) || page.From != r.From || page.To != r.To || len(page.Batches) != int(r.To-r.From+1) {
		return indexPeerPage{}, fmt.Errorf("index page omitted range or changed manifest")
	}
	d, _ := findIndexDefinition(r.Index)
	for i := range page.Batches {
		b := &page.Batches[i]
		c := b.Checkpoint
		encoded, _ := json.Marshal(b)
		if len(encoded) > maxIndexPeerBatchBytes || !validIndexPeerCheckpoint(c, d) || c.Commitment != expected.Commitment || c.Height != expected.Height || c.BlockHash != expected.BlockHash || c.From != manifest.Checkpoint.From || batchRecordsHash(*b) != c.RecordsHash {
			return indexPeerPage{}, fmt.Errorf("index records or checkpoint continuity mismatch")
		}
		wantDep := indexDigest(struct{ Block, Parser, Reference string }{c.BlockHash, inscriptionParserProfile, inscriptionReferenceCommit})
		if c.DependencyFingerprint != wantDep {
			return indexPeerPage{}, fmt.Errorf("incompatible index dependency fingerprint")
		}
		if (d.ID == "bitmap" && len(b.Inscriptions) > 0) || (d.ID == "inscriptions" && len(b.Bitmap) > 0) {
			return indexPeerPage{}, fmt.Errorf("wrong index record schema")
		}
		for j := range b.Inscriptions {
			b.Inscriptions[j].Verification = verificationView{}
			b.Inscriptions[j].VerificationState = indexPeerClaimState
		}
		expected = indexPeerAnchor{Commitment: c.PreviousCommitment, Height: c.Height - 1, BlockHash: b.PreviousBlockHash}
	}
	if r.From > manifest.Checkpoint.From {
		if page.Next == nil || *page.Next != expected || !validHash(expected.Commitment) || !validHash(expected.BlockHash) || page.Cursor == "" || len(page.Cursor) > 1024 {
			return indexPeerPage{}, fmt.Errorf("missing continuation checkpoint")
		}
	} else if page.Next != nil || page.Cursor != "" {
		return indexPeerPage{}, fmt.Errorf("unexpected continuation after range origin")
	}
	page.Verification = indexPeerClaimState
	page.Manifest.Verification = indexPeerClaimState
	return page, nil
}

func (a *app) answerIndexPeerRequest(req overlayRequest) (overlayResponse, string) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		return overlayResponse{}, "unsupported"
	}
	return a.buildIndexPeerResponse(req)
}
func (a *app) buildIndexPeerResponse(req overlayRequest) (overlayResponse, string) {
	resp := overlayResponse{Version: bodWireVersion, Type: req.Type}
	if req.Type == "index_manifest" {
		resp.IndexManifests = a.readPublishedIndexManifests()
		resp.OK = true
		return resp, "ok"
	}
	if req.IndexRange == nil {
		resp.Error = "missing index range request"
		return resp, "invalid"
	}
	page, err := a.readIndexPeerPage(*req.IndexRange)
	if err != nil {
		resp.Error = err.Error()
		return resp, "unknown"
	}
	resp.IndexPage = &page
	resp.OK = true
	return resp, "ok"
}

func (a *app) queryIndexPeerManifests(peer overlayPeer) ([]indexPeerManifest, error) {
	resp, err := a.queryGatewayPeer(peer, overlayRequest{Type: "index_manifest"})
	if err != nil {
		return nil, err
	}
	if !resp.OK || resp.Type != "index_manifest" || len(resp.IndexManifests) > 2 {
		return nil, fmt.Errorf("invalid index manifests")
	}
	seen := map[string]bool{}
	for i, m := range resp.IndexManifests {
		if err = validateIndexPeerManifest(m); err != nil {
			return nil, err
		}
		if seen[m.Definition] {
			return nil, fmt.Errorf("duplicate index manifest")
		}
		seen[m.Definition] = true
		resp.IndexManifests[i].Verification = indexPeerClaimState
	}
	return resp.IndexManifests, nil
}

func (a *app) queryIndexPeerPage(peer overlayPeer, m indexPeerManifest, r indexPeerRangeRequest) (indexPeerPage, error) {
	if _, err := indexPeerExpectedAnchor(m, r); err != nil {
		return indexPeerPage{}, err
	}
	resp, err := a.queryGatewayPeer(peer, overlayRequest{Type: "index_range", IndexRange: &r})
	if err != nil {
		return indexPeerPage{}, err
	}
	if !resp.OK || resp.Type != "index_range" || resp.IndexPage == nil {
		return indexPeerPage{}, fmt.Errorf("missing peer index page")
	}
	return validateIndexPeerPage(m, r, *resp.IndexPage)
}
