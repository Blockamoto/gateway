package main

import (
	"context"
	"fmt"
)

// Work consent is broader than the logical links Arm: the two independent
// fact-layer flags can request an explicit source pass with links left Off.
func indexRunSelectionEnabled(c indexRunSelection) bool {
	return c.Armed || c.Index == "txo-spender" && (c.RetainOutputValues != nil && *c.RetainOutputValues || c.RetainOutputScripts != nil && *c.RetainOutputScripts)
}
func indexRunFactsOnly(c indexRunSelection) bool {
	return c.Index == "txo-spender" && !c.Armed && indexRunSelectionEnabled(c)
}

type sparseFactsRunConsumer struct {
	a          *app
	choice     indexRunSelection
	from, next int64
	cp         *indexCheckpoint
	hashAt     func(int64) (string, error)
}

func (s *sparseFactsRunConsumer) NextHeight() int64            { return s.next }
func (s *sparseFactsRunConsumer) Checkpoint() *indexCheckpoint { return s.cp }
func (s *sparseFactsRunConsumer) Close()                       {}
func (s *sparseFactsRunConsumer) Reconcile(ctx context.Context, hashAt func(int64) (string, error)) error {
	s.hashAt = hashAt
	if s.cp != nil {
		hash, err := hashAt(s.cp.Height)
		if err != nil {
			return err
		}
		if hash != s.cp.BlockHash {
			s.cp = nil
			s.next = s.from
		}
	}
	return ctx.Err()
}
func (s *sparseFactsRunConsumer) Apply(ctx context.Context, b blockView, _ *inscriptionBlockResult) error {
	if b.Height != s.next || s.hashAt == nil {
		return fmt.Errorf("fact-layer pass requires its next selected block")
	}
	if err := lockIndexBrowse(ctx); err != nil {
		return err
	}
	defer indexBrowseMu.Unlock()
	for _, id := range []string{"txio-values", "txio-scripts"} {
		if !resolvedSelectionEnabled(s.choice, id) {
			continue
		}
		work := resolvedSelectionContext(ctx, s.a.dataDir, id, s.choice, s.hashAt, b)
		if err := s.a.ingestSparseOutputLayer(work, id, b); err != nil {
			return err
		}
	}
	d, _ := findIndexDefinition("txo-spender")
	cp := indexCheckpoint{Schema: 1, Definition: d.ID, Version: d.Version, RuleHash: d.RuleHash, Network: d.Network, From: s.from, Height: b.Height, BlockHash: b.Hash, RecordsHash: indexDigest(struct {
		Source          string
		Values, Scripts bool
	}{b.Hash, resolvedSelectionEnabled(s.choice, "txio-values"), resolvedSelectionEnabled(s.choice, "txio-scripts")})}
	cp.Commitment = checkpointHash(cp)
	s.cp = &cp
	s.next = b.Height + 1
	return nil
}
