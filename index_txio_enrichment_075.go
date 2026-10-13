package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"./internal/updateapply"
)

// Independent history enrichment avoids rewriting an earlier canonical spend
// commitment. It authenticates the creation identity and is available only for
// that exact block; reorgs cannot reinterpret it at the same numeric height.
type txioOutputEnrichment struct {
	Schema      int                      `json:"schema"`
	Height      int64                    `json:"height"`
	BlockHash   string                   `json:"block_hash"`
	Values      bool                     `json:"values"`
	Scripts     bool                     `json:"scripts"`
	OutputFacts map[string]outputFactRef `json:"output_facts"`
	Checksum    string                   `json:"checksum"`
}

func txioEnrichmentDigest(p txioOutputEnrichment) string { p.Checksum = ""; return indexDigest(p) }
func (s *indexStore) txioEnrichmentPath(height int64, hash string) (string, error) {
	if height < 0 || !validHash(hash) {
		return "", fmt.Errorf("invalid TXIO enrichment identity")
	}
	return filepath.Join(s.dir, "output-layers", strconv.FormatInt(height/1000, 10), fmt.Sprintf("%d-%s.json", height, hash)), nil
}
func (s *indexStore) txioEnrichmentContext(ctx context.Context, height int64, hash string) (txioOutputEnrichment, bool, error) {
	var p txioOutputEnrichment
	if e := ctx.Err(); e != nil {
		return p, false, e
	}
	path, e := s.txioEnrichmentPath(height, hash)
	if e != nil {
		return p, false, e
	}
	if e = updateapply.CheckPath(path); e != nil {
		return p, false, e
	}
	f, e := os.Open(path)
	if os.IsNotExist(e) {
		return p, false, nil
	}
	if e != nil {
		return p, false, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (32<<20)+1))
	if e != nil {
		return p, false, e
	}
	if len(b) > 32<<20 {
		return p, false, fmt.Errorf("TXIO enrichment budget exceeded")
	}
	if e = json.Unmarshal(b, &p); e != nil {
		return p, false, e
	}
	if p.Schema != 1 || p.Height != height || p.BlockHash != hash || (!p.Values && !p.Scripts) || p.Checksum != txioEnrichmentDigest(p) {
		return p, false, fmt.Errorf("TXIO enrichment checksum/identity mismatch")
	}
	for pos, ref := range p.OutputFacts {
		c, ok := parseBODCoordinate(pos)
		if !ok || c.Kind != coordOutput || c.Height != height || pos != outputCoordinate(c.IOIndex, c.TxIndex, c.Height) || !validHash(ref.Page) || ref.Row >= outputFactPageRows {
			return p, false, fmt.Errorf("invalid TXIO enrichment output reference")
		}
	}
	return p, true, ctx.Err()
}
func (s *indexStore) enrichTXIOBlockContext(ctx context.Context, block blockView, values, scripts bool) error {
	if !values && !scripts {
		return nil
	}
	prior, have, e := s.txioEnrichmentContext(ctx, block.Height, block.Hash)
	if e != nil {
		return e
	}
	if have {
		values = values || prior.Values
		scripts = scripts || prior.Scripts
	}
	refs, e := storeBlockOutputFactLayersContext(ctx, s.profileRoot, block, values, scripts)
	if e != nil {
		return e
	}
	if have && values == prior.Values && scripts == prior.Scripts {
		if len(refs) != len(prior.OutputFacts) {
			return fmt.Errorf("TXIO enrichment facts mismatch")
		}
		for pos, ref := range refs {
			if ref != prior.OutputFacts[pos] {
				return fmt.Errorf("TXIO enrichment facts mismatch")
			}
		}
		return nil // verified/repaired exact pages; preserve the existing manifest
	}
	p := txioOutputEnrichment{Schema: 1, Height: block.Height, BlockHash: block.Hash, Values: values, Scripts: scripts, OutputFacts: refs}
	p.Checksum = txioEnrichmentDigest(p)
	path, e := s.txioEnrichmentPath(block.Height, block.Hash)
	if e != nil {
		return e
	}
	if e = updateapply.CheckPath(path); e != nil {
		return e
	}
	if e = atomicWriteJSON(path, p); e != nil {
		return e
	}
	for _, dir := range []string{filepath.Dir(path), filepath.Dir(filepath.Dir(path)), s.dir, filepath.Dir(s.dir), s.profileRoot} {
		if e = syncDirectory(dir); e != nil {
			return e
		}
	}
	return nil
}

type txioOutputFactsResult struct {
	Position     string  `json:"position"`
	State        string  `json:"state"`
	BlockHash    string  `json:"block_hash,omitempty"`
	ValueSats    *uint64 `json:"value_sats,omitempty"`
	ScriptPubKey *string `json:"script_pub_key,omitempty"`
	Note         string  `json:"note,omitempty"`
}

func (a *app) lookupTXIOOutputFacts(ctx context.Context, pos string) (txioOutputFactsResult, error) {
	out := txioOutputFactsResult{Position: pos, State: "unknown", Note: "No retained historical output facts; absence is not proof of unspentness."}
	c, e := parseTXIOPosition("output", pos)
	if e != nil {
		return out, e
	}
	s, e := indexStoreHead(a.dataDir, "txo-spender")
	if e != nil {
		return out, e
	}
	if s.checkpoint == nil {
		return out, nil
	}
	if a.indexChainState(s.checkpoint) != "selected_chain" {
		return out, fmt.Errorf("TXIO needs selected-chain reconciliation")
	}
	if c.Height < s.checkpoint.From || c.Height > s.checkpoint.Height {
		return out, nil
	}
	if e = s.ensureIndexLocators(ctx); e != nil {
		return out, e
	}
	commit, have, e := s.activeLocatorCommitContext(ctx, c.Height)
	if e != nil || !have {
		return out, e
	}
	b, e := s.readBatch(commit)
	if e != nil {
		return out, e
	}
	actual, e := a.flowCanonicalHash(ctx, c.Height)
	if e != nil {
		return out, e
	}
	if actual != b.Checkpoint.BlockHash {
		return out, fmt.Errorf("TXIO output creation is disconnected")
	}
	var ref outputFactRef
	values, scripts := false, false
	if b.Spenders != nil {
		ref, have = b.Spenders.OutputFacts[pos]
		values, scripts = b.Spenders.Values, b.Spenders.Scripts
	}
	if p, exists, e := s.txioEnrichmentContext(ctx, c.Height, actual); e != nil {
		return out, e
	} else if exists {
		if r, ok := p.OutputFacts[pos]; ok {
			ref, have, values, scripts = r, true, p.Values, p.Scripts
		}
	}
	if !have {
		return out, nil
	}
	f, v, sc, e := readOutputFactLayersContext(ctx, a.dataDir, ref)
	if e != nil {
		return out, e
	}
	if f.Height != c.Height || f.TxIndex != c.TxIndex || int(f.Vout) != c.IOIndex || f.BlockHash != actual || values && !v || scripts && !sc {
		return out, fmt.Errorf("TXIO output fact identity mismatch")
	}
	out.State, out.BlockHash, out.Note = "known", actual, ""
	if values {
		out.ValueSats = &f.ValueSats
	}
	if scripts {
		out.ScriptPubKey = &f.ScriptPubKey
	}
	return out, nil
}
func (a *app) handleTXIOOutputFacts(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "txo-spender") {
		return
	}
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, fmt.Errorf("GET required"))
		return
	}
	ctx, done := a.indexLookupContext(r.Context())
	defer done()
	out, e := a.lookupTXIOOutputFacts(ctx, r.URL.Query().Get("position"))
	if e != nil {
		jsonError(w, http.StatusBadRequest, e)
		return
	}
	writeJSON(w, out)
}
