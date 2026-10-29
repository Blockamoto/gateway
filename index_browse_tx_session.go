package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

type indexBrowseTXPointSessionKey struct{}

var errBrowseTXPointStaged = errors.New("complete TX point facts are staged, not yet durably available")
var errBrowseTXPointInvalidated = errors.New("staged TX point admission or intent was invalidated")

const browseTXFutureBytes = 16 << 20
const browseTXFutureRows = 120000

type browseTXPendingPoint struct {
	point         indexBrowseTXPoint
	json          []byte
	selection     string
	rawProduced   bool
	admission     bool
	boundaryKnown bool
	boundary      int64
}
type browseTXPointSession struct {
	mu             sync.Mutex
	a              *app
	writer         *indexStore
	global         context.Context
	runID          string
	entries        map[int64]browseTXPendingPoint
	window         int64
	bytes          int
	rows           int
	closed         bool
	allRawProduced bool
}

func newBrowseTXPointSession(a *app, global context.Context, runID string) *browseTXPointSession {
	return &browseTXPointSession{a: a, writer: a.newBrowseStore("tx-locator"), global: global, runID: runID, entries: map[int64]browseTXPendingPoint{}, allRawProduced: true}
}
func (s *browseTXPointSession) clearLocked() {
	s.entries = map[int64]browseTXPendingPoint{}
	s.bytes = 0
	s.rows = 0
	s.allRawProduced = true
}
func browseTXPointExpectedInvalidation(err error) bool {
	var anchor satPublicationAnchorError077
	var raw blockUnavailableError
	return errors.Is(err, errIndexRunConsumerStopped) || errors.Is(err, errIndexRunAdmissionChanged) || errors.As(err, &anchor) || errors.As(err, &raw)
}
func browseTXPointGuardError(err error) error {
	if browseTXPointExpectedInvalidation(err) {
		return fmt.Errorf("%w: %w", errBrowseTXPointInvalidated, err)
	}
	return err
}
func (s *browseTXPointSession) Abort() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearLocked()
	s.closed = true
}
func (s *browseTXPointSession) guard(ctx context.Context, e browseTXPendingPoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.global.Err(); err != nil {
		return err
	}
	if s.closed {
		return errIndexRunConsumerStopped
	}
	d, err := readIndexRunDocument(s.a.dataDir)
	if err != nil {
		return err
	}
	if !d.Configured || d.Run == nil || d.Run.ID != s.runID {
		return errIndexRunConsumerStopped
	}
	switch d.Run.State {
	case "running", "waiting":
	default:
		return errIndexRunConsumerStopped
	}
	choice, on := runSelection(indexRunEffectiveRequest(d), "tx-locator")
	if !on || !choice.Armed {
		return errIndexRunConsumerStopped
	}
	if indexRunLocatorBuildSelectionHash(choice) != e.selection {
		return errIndexRunAdmissionChanged
	}
	if err := resolvedAdmissionAllowed(d, "tx-locator", e.point.Height); err != nil {
		return err
	}
	progress, admitted := indexRunProgressFor(d.Run, "tx-locator")
	if admitted != e.admission {
		return errIndexRunAdmissionChanged
	}
	if admitted {
		known := progress.EffectiveFrom != nil
		if known != e.boundaryKnown || known && *progress.EffectiveFrom != e.boundary {
			return errIndexRunAdmissionChanged
		}
	}
	if raw, on := runSelection(indexRunEffectiveRequest(d), "blocks"); on && raw.Armed && !e.rawProduced {
		return blockUnavailableError{fmt.Errorf("Raw was turned On while TX point facts were staged")}
	}
	target, err := s.a.utxoSelectedTarget(ctx, s.a.currentChainAuthority(), e.point.Height)
	if err != nil {
		return err
	}
	if target.HashDisplay != e.point.BlockHash {
		return satPublicationAnchorError077{fmt.Errorf("staged TX point left the selected chain")}
	}
	return nil
}
func (s *browseTXPointSession) Prune(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if err := s.guard(ctx, e); err != nil {
			if browseTXPointExpectedInvalidation(err) {
				s.clearLocked()
			}
			return browseTXPointGuardError(err)
		}
	}
	return nil
}
func (s *browseTXPointSession) Check(ctx context.Context) error { return s.Prune(ctx) }

// Stage is called under the one browse writer. Only compact immutable JSON and
// intent/anchor scalars survive this call; ctx and its block ancestry do not.
func (s *browseTXPointSession) Stage(ctx context.Context, batch indexBatch, rawProduced bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := batch.Checkpoint
	definition, _ := findIndexDefinition("tx-locator")
	if c.Definition != "tx-locator" || c.Version != definition.Version || c.RuleHash != definition.RuleHash || c.Network != definition.Network || c.Schema != definition.CheckpointSchema || c.Height < 0 || c.From != c.Height || c.PreviousCommitment != "" || c.Commitment != checkpointHash(c) || c.RecordsHash != batchRecordsHash(batch) || c.DependencyFingerprint != indexDigest(struct{ Block, Selection, Source string }{c.BlockHash, "all", ""}) {
		return false, fmt.Errorf("invalid complete future TX point checkpoint")
	}
	if err := validateTransactionPayload(batch, "tx-locator"); err != nil {
		return false, err
	}
	if owner, ok := ctx.Value(indexRunSourceOwnershipKey{}).(indexRunSourceOwnership); ok && owner.RunID != "" && owner.RunID != s.runID {
		return false, errIndexRunConsumerStopped
	}
	if s.closed {
		return false, errIndexRunConsumerStopped
	}
	if err := checkIndexRunPublication(ctx); err != nil {
		return false, err
	}
	d, err := readIndexRunDocument(s.a.dataDir)
	if err != nil {
		return false, err
	}
	choice, on := runSelection(indexRunEffectiveRequest(d), "tx-locator")
	if !on || !choice.Armed {
		return false, errIndexRunConsumerStopped
	}
	e := browseTXPendingPoint{point: indexBrowseTXPoint{Schema: 2, Height: batch.Checkpoint.Height, BlockHash: batch.Checkpoint.BlockHash, Commitment: batch.Checkpoint.Commitment}, selection: indexRunLocatorBuildSelectionHash(choice), rawProduced: rawProduced}
	e.point.Digest = browseTXPointDigest(e.point)
	if d.Run == nil {
		return false, errIndexRunConsumerStopped
	}
	progress, admitted := indexRunProgressFor(d.Run, "tx-locator")
	e.admission = admitted
	if admitted && progress.EffectiveFrom != nil {
		e.boundaryKnown = true
		e.boundary = *progress.EffectiveFrom
	}
	if err := s.guard(ctx, e); err != nil {
		return false, err
	}
	// All entries share these intent scalars. Compare them once, not by
	// rereading run state and old block anchors for every incoming point.
	for _, old := range s.entries {
		if old.selection != e.selection || old.admission != e.admission || old.boundaryKnown != e.boundaryKnown || old.boundaryKnown && old.boundary != e.boundary {
			s.clearLocked()
		}
		break
	}
	if raw, on := runSelection(indexRunEffectiveRequest(d), "blocks"); on && raw.Armed && !s.allRawProduced {
		s.clearLocked()
	}
	if prior, have := s.entries[e.point.Height]; have && prior.point.BlockHash != e.point.BlockHash {
		s.clearLocked()
	}
	if saved, have, err := s.a.browsedBatchContext(ctx, "tx-locator", e.point.Height, e.point.BlockHash); err != nil {
		return false, err
	} else if have && saved.Checkpoint.Commitment == e.point.Commitment {
		if _, pending := s.entries[e.point.Height]; pending {
			if err := s.flushLocked(s.global); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	if prior, have := s.entries[e.point.Height]; have {
		if prior.point.Commitment != e.point.Commitment {
			return false, fmt.Errorf("conflicting staged TX point")
		}
		return false, errBrowseTXPointStaged
	}
	e.json, err = json.MarshalIndent(batch, "", "  ")
	if err != nil {
		return false, err
	}
	count := len(transactionBatchEntries(batch))
	if len(e.json) > browseTXFutureBytes || count > browseTXFutureRows {
		return false, fmt.Errorf("one TX point exceeds the bounded future batch")
	}
	window := e.point.Height / indexLocatorBatchBlocks * indexLocatorBatchBlocks
	if len(s.entries) > 0 && (window != s.window || len(s.entries) >= int(indexLocatorBatchBlocks) || s.bytes+len(e.json) > browseTXFutureBytes || s.rows+count > browseTXFutureRows) {
		if err := s.flushLocked(s.global); err != nil {
			return false, err
		}
	}
	s.window = window
	s.entries[e.point.Height] = e
	s.bytes += len(e.json)
	s.rows += count
	s.allRawProduced = s.allRawProduced && rawProduced
	if len(s.entries) == int(indexLocatorBatchBlocks) || s.bytes >= browseTXFutureBytes || s.rows >= browseTXFutureRows {
		if err := s.flushLocked(s.global); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, errBrowseTXPointStaged
}
func (s *browseTXPointSession) Lookup(ctx context.Context, height int64, hash string) (indexBatch, bool, error) {
	s.mu.Lock()
	e, have := s.entries[height]
	if !have || e.point.BlockHash != hash {
		s.mu.Unlock()
		return indexBatch{}, false, nil
	}
	err := s.guard(ctx, e)
	raw := e.json
	s.mu.Unlock()
	if err != nil {
		return indexBatch{}, false, browseTXPointGuardError(err)
	}
	var batch indexBatch
	if err := json.Unmarshal(raw, &batch); err != nil {
		return batch, false, err
	}
	return batch, true, nil
}
func (s *browseTXPointSession) Flush(ctx context.Context) error {
	if err := lockIndexBrowse(ctx); err != nil {
		return err
	}
	defer indexBrowseMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked(ctx)
}
func (s *browseTXPointSession) flushLocked(ctx context.Context) error {
	if len(s.entries) == 0 {
		return nil
	}
	heights := make([]int64, 0, len(s.entries))
	for h, e := range s.entries {
		if err := s.guard(ctx, e); err != nil {
			return browseTXPointGuardError(err)
		}
		heights = append(heights, h)
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
	store := s.writer
	if store == nil {
		store = s.a.newBrowseStore("tx-locator")
		s.writer = store
	}
	dir := filepath.Join(store.dir, "commits")
	if err := store.makeLocatorDirectories(dir); err != nil {
		return err
	}
	for _, h := range heights {
		e := s.entries[h]
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(dir, e.point.Commitment+".json")
		if _, err := os.Stat(path); err == nil {
			if _, err := store.readBatch(e.point.Commitment); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		} else if err := writeLocatorCommitBytes(path, e.json); err != nil {
			return err
		}
	}
	if err := store.syncLocatorDirectory(dir); err != nil {
		return err
	}
	w, have, err := readBrowseTXPointWindow(ctx, s.a.dataDir, s.window)
	if err != nil {
		return err
	}
	if !have {
		w = indexBrowseTXPointWindow{Schema: 3, Window: s.window}
	}
	for _, h := range heights {
		e := s.entries[h]
		if err := s.guard(ctx, e); err != nil {
			return browseTXPointGuardError(err)
		}
		mergeBrowseTXWindowPoint(&w, e.point)
	}
	if err := checkIndexRunPublication(ctx); err != nil {
		return err
	}
	// The final visibility fence is detached from the current prepared body.
	// Keep cancellation, but carry only bounded admission/anchor scalars.
	final, cancel := context.WithCancel(s.global)
	stop := context.AfterFunc(ctx, cancel)
	defer func() { stop(); cancel() }()
	lean := &browseTXPointSession{a: s.a, global: final, runID: s.runID, closed: s.closed}
	outerPublication, _ := ctx.Value(indexRunPublicationKey{}).(func() error)
	guards := make([]browseTXPendingPoint, 0, len(heights))
	for _, h := range heights {
		guard := s.entries[h]
		guard.json = nil
		guards = append(guards, guard)
	}
	publication := context.WithValue(final, indexRunPublicationKey{}, func() error {
		if err := checkIndexRunPublication(final); err != nil {
			return err
		}
		if outerPublication != nil {
			if err := outerPublication(); err != nil {
				return err
			}
		}
		for _, guard := range guards {
			if err := lean.guard(final, guard); err != nil {
				return browseTXPointGuardError(err)
			}
		}
		return nil
	})
	if err := writeBrowseTXPointWindow(publication, store, w); err != nil {
		return err
	}
	s.clearLocked()
	return nil
}
