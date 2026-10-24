package main

import (
	"./internal/updateapply"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Optional Values and ScriptPubKeys each own sparse receipts independently of
// TXIO links. The shared immutable pages remain owned until every owner drops
// its reference. Neither a missing layer nor a missing link means zero/unspent.
type sparseOutputLayerReceipt struct {
	Schema      int                      `json:"schema"`
	Layer       string                   `json:"layer"`
	Height      int64                    `json:"height"`
	BlockHash   string                   `json:"block_hash"`
	OutputFacts map[string]outputFactRef `json:"output_facts"`
	Digest      string                   `json:"digest"`
}

func sparseOutputLayerDir(root, id string) string {
	return filepath.Join(root, "indexes", "browse", id)
}
func sparseOutputLayerDigest(r sparseOutputLayerReceipt) string { r.Digest = ""; return indexDigest(r) }
func readSparseOutputLayerHead(root, id string) (indexBrowseHead, error) {
	h := indexBrowseHead{Schema: 1, Blocks: map[string]string{}}
	if id != "txio-values" && id != "txio-scripts" {
		return h, fmt.Errorf("unknown sparse output layer")
	}
	err := boundedBrowseJSON(filepath.Join(sparseOutputLayerDir(root, id), "head.json"), &h)
	if os.IsNotExist(err) {
		err = nil
	}
	if err != nil {
		return h, err
	}
	if h.Schema != 1 || h.Blocks == nil {
		return h, fmt.Errorf("invalid sparse output-layer head")
	}
	for key, digest := range h.Blocks {
		n, err := strconv.ParseInt(key, 10, 64)
		if err != nil || n < 0 || strconv.FormatInt(n, 10) != key || !validHash(digest) {
			return h, fmt.Errorf("invalid sparse output-layer membership")
		}
	}
	return h, nil
}
func readSparseOutputLayer(root, id, digest string) (sparseOutputLayerReceipt, error) {
	var r sparseOutputLayerReceipt
	if id != "txio-values" && id != "txio-scripts" || !validHash(digest) {
		return r, fmt.Errorf("invalid output-layer identity")
	}
	err := boundedBrowseJSON(filepath.Join(sparseOutputLayerDir(root, id), "commits", digest+".json"), &r)
	if err != nil {
		return r, err
	}
	if r.Schema != 1 || r.Layer != id || r.Height < 0 || !validHash(r.BlockHash) || r.Digest != digest || sparseOutputLayerDigest(r) != digest {
		return r, fmt.Errorf("sparse output-layer authentication mismatch")
	}
	for pos, ref := range r.OutputFacts {
		c, ok := parseBODCoordinate(pos)
		if !ok || c.Kind != coordOutput || c.Height != r.Height || pos != outputCoordinate(c.IOIndex, c.TxIndex, c.Height) || !validHash(ref.Page) || ref.Row >= outputFactPageRows {
			return r, fmt.Errorf("invalid sparse output-layer fact position")
		}
	}
	return r, nil
}
func (a *app) ingestSparseOutputLayer(ctx context.Context, id string, block blockView) error {
	outputFactCollectionMu.Lock()
	defer outputFactCollectionMu.Unlock()
	refs, err := storeBlockOutputFactLayersContext(ctx, a.dataDir, block, id == "txio-values", id == "txio-scripts")
	if err != nil {
		return err
	}
	r := sparseOutputLayerReceipt{Schema: 1, Layer: id, Height: block.Height, BlockHash: block.Hash, OutputFacts: refs}
	r.Digest = sparseOutputLayerDigest(r)
	h, err := readSparseOutputLayerHead(a.dataDir, id)
	if err != nil {
		return err
	}
	key := strconv.FormatInt(block.Height, 10)
	if h.Blocks[key] == r.Digest {
		_, err = readSparseOutputLayer(a.dataDir, id, r.Digest)
		return err
	}
	dir := sparseOutputLayerDir(a.dataDir, id)
	if err := updateapply.CheckPath(dir); err != nil {
		return err
	}
	if err := atomicWriteJSON(filepath.Join(dir, "commits", r.Digest+".json"), r); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Join(dir, "commits")); err != nil {
		return err
	}
	if err := checkIndexRunPublication(ctx); err != nil {
		return err
	}
	h.Blocks[key] = r.Digest
	if raw, err := json.Marshal(h); err != nil {
		return err
	} else if int64(len(raw)) > indexBrowseBudget {
		return fmt.Errorf("sparse output-layer coverage exceeds metadata budget")
	}
	if err := atomicWriteJSON(filepath.Join(dir, "head.json"), h); err != nil {
		return err
	}
	return syncDirectory(dir)
}
func (a *app) sparseOutputFacts(ctx context.Context, pos string) (txioOutputFactsResult, error) {
	out := txioOutputFactsResult{Position: pos, State: "unknown", Note: "No retained historical output facts; absence is not proof of unspentness."}
	c, err := parseTXIOPosition("output", pos)
	if err != nil {
		return out, err
	}
	hash, err := a.canonicalHashAtHeight(c.Height)
	if err != nil {
		return out, err
	}
	for _, id := range []string{"txio-values", "txio-scripts"} {
		h, err := readSparseOutputLayerHead(a.dataDir, id)
		if err != nil {
			return out, err
		}
		digest := h.Blocks[strconv.FormatInt(c.Height, 10)]
		if digest == "" {
			continue
		}
		r, err := readSparseOutputLayer(a.dataDir, id, digest)
		if err != nil {
			return out, err
		}
		if r.Height != c.Height || r.BlockHash != hash {
			continue
		}
		ref, ok := r.OutputFacts[pos]
		if !ok {
			continue
		}
		f, values, scripts, err := readOutputFactLayersContext(ctx, a.dataDir, ref)
		if err != nil {
			return out, err
		}
		if f.BlockHash != hash || f.Height != c.Height || f.TxIndex != c.TxIndex || int(f.Vout) != c.IOIndex {
			return out, fmt.Errorf("sparse output fact identity mismatch")
		}
		if id == "txio-values" {
			if !values {
				return out, fmt.Errorf("value layer missing its value")
			}
			v := f.ValueSats
			out.ValueSats = &v
		} else {
			if !scripts {
				return out, fmt.Errorf("script layer missing its script")
			}
			v := f.ScriptPubKey
			out.ScriptPubKey = &v
		}
		out.BlockHash = hash
		out.State = "known"
		out.Note = "Authenticated sparse creation facts; no TXIO links or complete UTXO state is implied."
	}
	return out, nil
}
func visitSparseOutputFactPages(ctx context.Context, root string, visit func(string) error) error {
	for _, id := range []string{"txio-values", "txio-scripts"} {
		h, err := readSparseOutputLayerHead(root, id)
		if err != nil {
			return err
		}
		for _, digest := range h.Blocks {
			if err := ctx.Err(); err != nil {
				return err
			}
			r, err := readSparseOutputLayer(root, id, digest)
			if err != nil {
				return err
			}
			for _, ref := range r.OutputFacts {
				if err := visit(ref.Page); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func sparseOutputLayerTimeline(root, id string, headers *timelineLocalReader) (indexTimelineSource, error) {
	out := indexTimelineSource{ID: "gateway_sparse_output_layer", Kind: "sparse_owned_facts", Ranges: []heightInterval{}, Complete: true, Verification: "selected_chain", Note: "Independently owned sparse creation facts; no intervening TXIO links or live UTXO membership is inferred."}
	h, err := readSparseOutputLayerHead(root, id)
	if err != nil {
		return out, err
	}
	for _, digest := range h.Blocks {
		r, err := readSparseOutputLayer(root, id, digest)
		if err != nil {
			return out, err
		}
		if headers.matches(r.Height, r.BlockHash) {
			out.Ranges = append(out.Ranges, heightInterval{r.Height, r.Height})
		}
	}
	out.Ranges = mergeIntervals(out.Ranges)
	return out, nil
}
