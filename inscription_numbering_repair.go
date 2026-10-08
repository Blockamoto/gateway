package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Missing numbering is an independent output. Enrichment replays immutable
// occurrence evidence without replacing its commitments, bodies or publication.
// Each successful block has a small durable sidecar; interrupted replay resumes.
type numberingEnrichment struct {
	Source     indexCheckpoint              `json:"source"`
	Snapshot   inscriptionNumberingSnapshot `json:"snapshot"`
	Previous   string                       `json:"previous,omitempty"`
	Commitment string                       `json:"commitment"`
}

func numberingEnrichmentHash(e numberingEnrichment) string { e.Commitment = ""; return indexDigest(e) }
func (s *indexStore) numberingDir() string                 { return filepath.Join(s.dir, "numbering") }
func (s *indexStore) readNumberingEnrichment(commit string) (*numberingEnrichment, error) {
	if !validHash(commit) {
		return nil, fmt.Errorf("invalid numbering enrichment identity")
	}
	data, e := os.ReadFile(filepath.Join(s.numberingDir(), "commits", commit+".json"))
	if e != nil {
		return nil, e
	}
	if len(data) > 8192 {
		return nil, fmt.Errorf("numbering enrichment exceeds budget")
	}
	var result numberingEnrichment
	if json.Unmarshal(data, &result) != nil || result.Commitment != commit || numberingEnrichmentHash(result) != commit || result.Snapshot.Profile != inscriptionNumberingProfile || result.Snapshot.State != "complete" || result.Source.Definition != "inscriptions" || result.Source.Commitment != checkpointHash(result.Source) {
		return nil, fmt.Errorf("invalid numbering enrichment checkpoint")
	}
	return &result, nil
}
func (s *indexStore) loadNumberingEnrichment() (*numberingEnrichment, error) {
	data, e := os.ReadFile(filepath.Join(s.numberingDir(), "head.json"))
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var head struct {
		Commitment string `json:"commitment"`
	}
	if json.Unmarshal(data, &head) != nil {
		return nil, fmt.Errorf("invalid numbering enrichment head")
	}
	if head.Commitment == "" {
		return nil, nil
	}
	return s.readNumberingEnrichment(head.Commitment)
}
func (s *indexStore) saveNumberingEnrichment(result numberingEnrichment) error {
	result.Commitment = numberingEnrichmentHash(result)
	dir := s.numberingDir()
	if e := atomicWriteJSON(filepath.Join(dir, "commits", result.Commitment+".json"), result); e != nil {
		return e
	}
	if e := syncDirectory(filepath.Join(dir, "commits")); e != nil {
		return e
	}
	if e := atomicWriteJSON(filepath.Join(dir, "head.json"), struct {
		Commitment string `json:"commitment"`
	}{result.Commitment}); e != nil {
		return e
	}
	return syncDirectory(dir)
}
func (a *app) repairIndexNumbering(ctx context.Context, s *indexStore, to int64) error {
	return repairIndexNumberingSources(ctx, s, to, a.indexTarget, func(ctx context.Context, target blockTarget) (blockView, error) {
		return a.indexSourceBlock(ctx, target, "ephemeral")
	}, a.indexNumberingBlockValues)
}
func repairIndexNumberingSources(ctx context.Context, s *indexStore, to int64, targetAt func(int64) (blockTarget, error), source func(context.Context, blockTarget) (blockView, error), values func(context.Context, blockView) func(transactionView, int64) ([]uint64, error)) error {
	if s.definition.ID != "inscriptions" || s.checkpoint == nil || s.checkpoint.From > firstMainnetInscriptionHeight || s.checkpoint.Height < firstMainnetInscriptionHeight {
		return nil
	}
	if to < 0 || to > s.checkpoint.Height {
		to = s.checkpoint.Height
	}
	if to < firstMainnetInscriptionHeight {
		return nil
	}
	if err := s.ensureIndexLocators(ctx); err != nil {
		return err
	}
	last, e := s.readBatch(s.checkpoint.Commitment)
	if e != nil {
		return e
	}
	if last.Numbering != nil && last.Numbering.Profile == inscriptionNumberingProfile && last.Numbering.State == "complete" {
		return nil
	}
	enrichment, e := s.loadNumberingEnrichment()
	if e != nil {
		return e
	}
	loadedCommit := ""
	if enrichment != nil {
		loadedCommit = enrichment.Commitment
	}
	// Reorg recovery uses authenticated source commitments, not sidecar height
	// alone. Unavailable old pages cause replay; they cannot imply empty state.
	for enrichment != nil {
		commit, ready, e := s.activeLocatorCommitContext(ctx, enrichment.Source.Height)
		if e != nil {
			return e
		}
		if ready && commit == enrichment.Source.Commitment {
			t := newOrdinalTree(s.ordinalPages(), enrichment.Snapshot.StateRoot)
			_, e1 := t.read(enrichment.Snapshot.StateRoot)
			_, e2 := t.read(enrichment.Snapshot.RecordsRoot)
			if e1 == nil && e2 == nil {
				break
			}
		}
		if enrichment.Previous == "" {
			enrichment = nil
			break
		}
		enrichment, e = s.readNumberingEnrichment(enrichment.Previous)
		if e != nil {
			return e
		}
	}
	selectedCommit := ""
	if enrichment != nil {
		selectedCommit = enrichment.Commitment
	}
	if selectedCommit != loadedCommit {
		if e = ctx.Err(); e != nil {
			return e
		}
		if enrichment != nil {
			target, e := targetAt(enrichment.Source.Height)
			if e != nil || target.HashDisplay != enrichment.Source.BlockHash {
				return fmt.Errorf("numbering recovery anchor unavailable or changed")
			}
			if e = s.saveNumberingEnrichment(*enrichment); e != nil {
				return e
			}
		} else {
			if e = atomicWriteJSON(filepath.Join(s.numberingDir(), "head.json"), struct {
				Commitment string `json:"commitment"`
			}{""}); e != nil {
				return e
			}
			if e = syncDirectory(s.numberingDir()); e != nil {
				return e
			}
		}
	}
	start := firstMainnetInscriptionHeight
	state := &inscriptionNumberingSnapshot{Profile: inscriptionNumberingProfile, State: "complete"}
	previous := ""
	if enrichment != nil {
		start = enrichment.Source.Height + 1
		copy := enrichment.Snapshot
		state = &copy
		previous = enrichment.Commitment
	}
	for height := start; height <= to; height++ {
		if e = ctx.Err(); e != nil {
			return e
		}
		commit, ready, e := s.activeLocatorCommitContext(ctx, height)
		if e != nil {
			return e
		}
		if !ready || commit == "" {
			return fmt.Errorf("numbering source coverage is unavailable at height %d", height)
		}
		batch, e := s.readBatch(commit)
		if e != nil {
			return e
		}
		target, e := targetAt(height)
		if e != nil {
			return e
		}
		if target.HashDisplay != batch.Checkpoint.BlockHash {
			return fmt.Errorf("numbering source changed chain; resume reconciliation before enrichment")
		}
		block, e := source(ctx, target)
		if e != nil {
			return fmt.Errorf("numbering needs block %d evidence: %w", height, e)
		}
		worker := *s
		worker.locatorContext = ctx
		worker.numberingValues = values(ctx, block)
		rows := append([]inscriptionOccurrence(nil), batch.Inscriptions...)
		next, e := worker.deriveInscriptionNumberingWithBase(block, s.checkpoint.From, rows, state)
		if e != nil {
			return e
		}
		if next.State != "complete" {
			return fmt.Errorf("numbering waiting at block %d: %s; occurrence records remain available", height, next.Reason)
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		target, e = targetAt(height)
		if e != nil || target.HashDisplay != batch.Checkpoint.BlockHash {
			return fmt.Errorf("numbering anchor changed or became unavailable before commit")
		}
		entry := numberingEnrichment{Source: batch.Checkpoint, Snapshot: *next, Previous: previous}
		entry.Commitment = numberingEnrichmentHash(entry)
		if e = s.saveNumberingEnrichment(entry); e != nil {
			return e
		}
		previous = entry.Commitment
		state = next
	}
	return nil
}

// Queries may use only enrichment belonging to this selected occurrence chain.
func (s *indexStore) selectedNumberingSnapshot() (*inscriptionNumberingSnapshot, error) {
	state, _, e := s.numberingView()
	return state, e
}
func (s *indexStore) numberingView() (*inscriptionNumberingSnapshot, *indexCheckpoint, error) {
	if s.checkpoint == nil {
		return nil, nil, nil
	}
	b, e := s.readBatch(s.checkpoint.Commitment)
	if e != nil {
		return nil, nil, e
	}
	if b.Numbering != nil && b.Numbering.State == "complete" {
		return b.Numbering, s.checkpoint, nil
	}
	enrichment, e := s.loadNumberingEnrichment()
	if e != nil || enrichment == nil {
		return nil, nil, e
	}
	if enrichment.Source.Height > s.checkpoint.Height {
		return nil, nil, nil
	}
	commit, ready, e := s.activeLocatorCommit(enrichment.Source.Height)
	if e != nil {
		return nil, nil, e
	}
	if !ready || commit != enrichment.Source.Commitment {
		return nil, nil, nil
	}
	return &enrichment.Snapshot, &enrichment.Source, nil
}
func (s *indexStore) numberingNeedsRepair() (bool, error) {
	if s.definition.ID != "inscriptions" || s.checkpoint == nil || s.checkpoint.From > firstMainnetInscriptionHeight || s.checkpoint.Height < firstMainnetInscriptionHeight {
		return false, nil
	}
	state, checkpoint, e := s.numberingView()
	if e != nil {
		return true, e
	}
	return state == nil || checkpoint == nil || checkpoint.Commitment != s.checkpoint.Commitment, nil
}
