package main

import (
	"context"
	"fmt"
	"path/filepath"
)

func txioLayerCoverageHash(head indexHead) string {
	layers := head.TXIOLayers
	if len(layers) == 0 {
		layers = nil
	}
	return indexDigest(struct {
		Schema     int
		Commitment string
		Complete   bool
		Layers     map[string][]heightInterval
	}{head.TXIOLayersSchema, head.Commitment, head.TXIOLayersComplete, layers})
}

func validateTXIOLayerCoverage(head indexHead, cp *indexCheckpoint) error {
	if head.TXIOLayersSchema != 1 || head.TXIOLayersHash != txioLayerCoverageHash(head) {
		return fmt.Errorf("invalid TXIO layer coverage checkpoint")
	}
	for id, ranges := range head.TXIOLayers {
		if id != "txio-links" && id != "txio-values" && id != "txio-scripts" {
			return fmt.Errorf("unknown TXIO coverage layer")
		}
		previous := int64(-2)
		for _, interval := range ranges {
			if cp == nil || interval.From < cp.From || interval.To > cp.Height || interval.To < interval.From || interval.From <= previous+1 {
				return fmt.Errorf("invalid TXIO layer interval")
			}
			previous = interval.To
		}
	}
	return nil
}

func cloneTXIOLayerCoverage(head indexHead) indexHead {
	if head.TXIOLayers != nil {
		layers := make(map[string][]heightInterval, len(head.TXIOLayers))
		for id, ranges := range head.TXIOLayers {
			layers[id] = append([]heightInterval(nil), ranges...)
		}
		head.TXIOLayers = layers
	}
	return head
}

func updateTXIOLayerCoverage(head *indexHead, batch indexBatch) {
	if batch.Spenders == nil {
		return
	}
	if head.TXIOLayers == nil {
		head.TXIOLayers = map[string][]heightInterval{}
	}
	links, values, scripts := txioLayerAvailability(batch.Spenders)
	for id, available := range map[string]bool{"txio-links": links, "txio-values": values, "txio-scripts": scripts} {
		if available {
			head.TXIOLayers[id] = mergeIntervals(append(head.TXIOLayers[id], heightInterval{batch.Checkpoint.Height, batch.Checkpoint.Height}))
		}
	}
}

func (s *indexStore) addTXIOEnrichmentCoverage(ctx context.Context, head *indexHead, height int64, hash string) error {
	enrichment, found, err := s.txioEnrichmentContext(ctx, height, hash)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if head.TXIOLayers == nil {
		head.TXIOLayers = map[string][]heightInterval{}
	}
	for id, available := range map[string]bool{"txio-values": enrichment.Values, "txio-scripts": enrichment.Scripts} {
		if available {
			head.TXIOLayers[id] = mergeIntervals(append(head.TXIOLayers[id], heightInterval{height, height}))
		}
	}
	return nil
}

func (s *indexStore) txioHistoryNeedsEnrichment(ctx context.Context, height int64, hash string, values, scripts, verifyKnown bool) (bool, error) {
	commit, ready, err := s.activeLocatorCommitContext(ctx, height)
	if err != nil {
		return false, err
	}
	if !ready || commit == "" {
		return false, fmt.Errorf("TXIO history checkpoint unavailable at block %d", height)
	}
	batch, err := s.readBatch(commit)
	if err != nil {
		return false, err
	}
	if batch.Checkpoint.BlockHash != hash {
		return false, fmt.Errorf("TXIO history is no longer on the selected chain; resume to reconcile")
	}
	_, hasValues, hasScripts := txioLayerAvailability(batch.Spenders)
	enrichment, found, err := s.txioEnrichmentContext(ctx, height, hash)
	if err != nil {
		return false, err
	}
	if found {
		hasValues = hasValues || enrichment.Values
		hasScripts = hasScripts || enrichment.Scripts
	}
	if values && !hasValues || scripts && !hasScripts {
		return true, nil
	}
	if !verifyKnown {
		return false, nil
	}
	refs := map[string]outputFactRef{}
	if batch.Spenders != nil {
		for pos, ref := range batch.Spenders.OutputFacts {
			refs[pos] = ref
		}
	}
	if found {
		for pos, ref := range enrichment.OutputFacts {
			refs[pos] = ref
		}
	}
	pages := map[string]outputFactPage{}
	for pos, ref := range refs {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		page, ok := pages[ref.Page]
		if !ok {
			page, err = readOutputFactPageContext(ctx, s.profileRoot, ref.Page)
			if err != nil {
				if ctx.Err() != nil {
					return false, ctx.Err()
				}
				return true, nil // Explicit builder can restore this page from canonical raw bytes.
			}
			pages[ref.Page] = page
		}
		if uint64(ref.Row) >= uint64(len(page.Rows)) || page.Height != height || page.BlockHash != hash || values && !page.Values || scripts && !page.Scripts {
			return true, nil
		}
		row := page.Rows[ref.Row]
		if outputCoordinate(int(row.Vout), row.TxIndex, height) != pos {
			return true, nil
		}
	}
	return false, nil
}

// A verified coverage catalogue lets live indexing revisit only missing
// optional layers, rather than rereading the entire history every turn.
func (s *indexStore) txioEnrichmentRanges(from, to int64, values, scripts bool) []heightInterval {
	if to < from || !values && !scripts {
		return nil
	}
	if !s.head.TXIOLayersComplete || s.head.TXIOLayersSchema != 1 {
		return []heightInterval{{from, to}}
	}
	ranges := []heightInterval{}
	if values {
		ranges = append(ranges, indexCoverageGaps(s.head.TXIOLayers["txio-values"], from, to)...)
	}
	if scripts {
		ranges = append(ranges, indexCoverageGaps(s.head.TXIOLayers["txio-scripts"], from, to)...)
	}
	return mergeIntervals(ranges)
}

func (s *indexStore) recordTXIOEnrichmentCoverage(height int64, hash string, values, scripts bool) error {
	if s.checkpoint == nil || height < s.checkpoint.From || height > s.checkpoint.Height {
		return fmt.Errorf("TXIO history outside active coverage")
	}
	head := cloneTXIOLayerCoverage(s.head)
	head.TXIOLayersSchema = 1
	if err := s.addTXIOEnrichmentCoverage(context.Background(), &head, height, hash); err != nil {
		return err
	}
	head.TXIOLayersHash = txioLayerCoverageHash(head)
	if err := atomicWriteJSON(filepath.Join(s.dir, "head.json"), head); err != nil {
		return err
	}
	if err := syncDirectory(s.dir); err != nil {
		return err
	}
	s.head = head
	return nil
}

// New heads carry independent, checkpoint-bound layer coverage. A legacy head
// gets only a bounded read projection; an uninspected prefix stays unknown.
func (a *app) timelineTXIOLayers(headers *timelineLocalReader, tip int64) ([]indexTimelineTrack, error) {
	s, err := indexStoreHead(a.dataDir, "txo-spender")
	if err != nil {
		return nil, err
	}
	head := s.head
	complete := head.TXIOLayersSchema == 1 && head.TXIOLayersComplete
	if !complete && s.checkpoint != nil {
		head.TXIOLayers = map[string][]heightInterval{}
		complete, err = walkExplorerIndex(context.Background(), s, func(batch indexBatch) error {
			if headers.matches(batch.Checkpoint.Height, batch.Checkpoint.BlockHash) {
				updateTXIOLayerCoverage(&head, batch)
				if err := s.addTXIOEnrichmentCoverage(context.Background(), &head, batch.Checkpoint.Height, batch.Checkpoint.BlockHash); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	anchored := s.checkpoint == nil || headers.matches(s.checkpoint.Height, s.checkpoint.BlockHash)
	tracks := []indexTimelineTrack{}
	for _, id := range []string{"txio-links", "txio-values", "txio-scripts"} {
		coverage := []heightInterval{}
		if anchored {
			coverage = append(coverage, head.TXIOLayers[id]...)
		}
		track := indexTimelineTrack{ID: id, Coverage: coverage, RecordedCoverage: append([]heightInterval{}, coverage...), ReportedCoverage: []heightInterval{}, Gaps: []heightInterval{}, Sources: []indexTimelineSource{}, CoverageComplete: complete && anchored && tip >= 0, Note: "Independent TXIO historical layer coverage; mandatory UTXO live and recovery facts do not imply optional historical coverage."}
		if id == "txio-links" {
			track.Note = "Blocks with complete resolved positional input/output links. Unresolved translations remain unknown."
		}
		if track.CoverageComplete {
			track.Gaps = indexCoverageGaps(coverage, 0, tip)
		}
		track.Sources = append(track.Sources, indexTimelineSource{ID: "gateway_txio_layer", Kind: "committed_records", Ranges: coverage, Complete: track.CoverageComplete, Verification: "checkpoint_anchored", Note: track.Note})
		tracks = append(tracks, track)
	}
	return tracks, nil
}
