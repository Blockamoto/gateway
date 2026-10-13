package main

import (
	"./internal/updateapply"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var satStateMu sync.Mutex
var satWriterMu sync.Mutex

type satSavepoint077 struct {
	Height     int64  `json:"height"`
	BlockHash  string `json:"block_hash"`
	Commitment string `json:"commitment"`
	Intervals  []int  `json:"intervals"`
}

func (s *indexStore) readSatHeadContext077(ctx context.Context) error {
	if err := updateapply.CheckPath(s.dir); err != nil {
		return err
	}
	var head indexHead
	err := utxoReadJSON(ctx, filepath.Join(s.dir, "head.json"), 256<<10, &head)
	if os.IsNotExist(err) {
		s.head = indexHead{}
		s.checkpoint = nil
		return nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: head (%v)", ErrSatStateCorrupt, err)
	}
	if !validHash(head.Commitment) {
		return fmt.Errorf("%w: missing current commitment", ErrSatStateCorrupt)
	}
	b, err := s.readBatch(head.Commitment)
	if err != nil {
		if errors.Is(err, ErrSatProfileIncompatible) {
			return err
		}
		return fmt.Errorf("%w: current checkpoint (%v)", ErrSatStateCorrupt, err)
	}
	s.head = head
	s.checkpoint = &b.Checkpoint
	if err = s.validateSatSavepoints077(ctx); err != nil {
		return err
	}
	return s.validateSatRoots077(ctx, *b.Sats)
}

func satSavepointsDigest077(head indexHead) string {
	return indexDigest(struct {
		Head       string
		Savepoints []satSavepoint077
	}{head.Commitment, head.SatSavepoints})
}
func satRotateSavepoints077(old []satSavepoint077, cp indexCheckpoint) []satSavepoint077 {
	points := map[int64]satSavepoint077{}
	for _, p := range old {
		if p.Height <= cp.Height {
			p.Intervals = nil
			points[p.Height] = p
		}
	}
	if cp.Height%12 == 0 || cp.Height%144 == 0 {
		points[cp.Height] = satSavepoint077{Height: cp.Height, BlockHash: cp.BlockHash, Commitment: cp.Commitment}
	}
	kept := map[int64]satSavepoint077{}
	for _, interval := range []int{12, 144} {
		heights := []int64{}
		for h := range points {
			if h%int64(interval) == 0 {
				heights = append(heights, h)
			}
		}
		sort.Slice(heights, func(i, j int) bool { return heights[i] > heights[j] })
		if len(heights) > 2 {
			heights = heights[:2]
		}
		for _, h := range heights {
			p := points[h]
			if prior, ok := kept[h]; ok {
				p = prior
			}
			p.Intervals = append(p.Intervals, interval)
			kept[h] = p
		}
	}
	out := []satSavepoint077{}
	for _, p := range kept {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Height < out[j].Height })
	return out
}
func (s *indexStore) validateSatSavepoints077(ctx context.Context) error {
	if s.checkpoint == nil {
		return nil
	}
	if s.head.SatSavepointsHash != satSavepointsDigest077(s.head) || len(s.head.SatSavepoints) == 0 || len(s.head.SatSavepoints) > 4 {
		return fmt.Errorf("%w: savepoint manifest", ErrSatStateCorrupt)
	}
	previous := int64(-1)
	counts := map[int]int{}
	for _, p := range s.head.SatSavepoints {
		if p.Height <= previous || p.Height > s.checkpoint.Height || !validHash(p.BlockHash) || !validHash(p.Commitment) || len(p.Intervals) == 0 || len(p.Intervals) > 2 {
			return fmt.Errorf("%w: savepoint identity", ErrSatStateCorrupt)
		}
		previous = p.Height
		seen := map[int]bool{}
		for _, n := range p.Intervals {
			if n != 12 && n != 144 || p.Height%int64(n) != 0 || seen[n] {
				return fmt.Errorf("%w: savepoint interval", ErrSatStateCorrupt)
			}
			seen[n] = true
			counts[n]++
			if counts[n] > 2 {
				return fmt.Errorf("%w: too many savepoints", ErrSatStateCorrupt)
			}
		}
		b, err := s.readBatch(p.Commitment)
		if err != nil {
			return fmt.Errorf("%w: savepoint record (%v)", ErrSatStateCorrupt, err)
		}
		if b.Checkpoint.Height != p.Height || b.Checkpoint.BlockHash != p.BlockHash {
			return fmt.Errorf("%w: savepoint checkpoint mismatch", ErrSatStateCorrupt)
		}
	}
	return ctx.Err()
}
func (s *indexStore) validateSatRoots077(ctx context.Context, p satIndexSnapshot) error {
	if p.Profile != satIndexProfile || p.Layout != 1 {
		return ErrSatProfileIncompatible
	}
	for _, root := range satSnapshotRoots077(p) {
		if _, err := newOrdinalTree(s.ordinalPages(), root).withContext(ctx).read(root); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%w: %v", ErrSatStateCorrupt, err)
		}
	}
	return nil
}
func (s *indexStore) appendSat077Block(block blockView, from int64, retention string, prepared *inscriptionBlockResult) error {
	satWriterMu.Lock()
	defer satWriterMu.Unlock()
	satStateMu.Lock()
	defer satStateMu.Unlock()
	ctx := s.ordinalContext()
	latest, reloadErr := indexStoreHead(s.profileRoot, "sat-state")
	if reloadErr != nil {
		return reloadErr
	}
	s.head, s.checkpoint = latest.head, latest.checkpoint
	if s.head.Mode == "" {
		s.head.Mode = "lean"
	}
	if from != 0 {
		return fmt.Errorf("Sat state requires genesis")
	}
	if s.checkpoint != nil {
		if block.Height != s.checkpoint.Height+1 || block.PreviousBlockHash != s.checkpoint.BlockHash {
			return fmt.Errorf("Sat block is not the next committed transition")
		}
		if err := s.validateSatSavepoints077(ctx); err != nil {
			return err
		}
	} else if block.Height != 0 {
		return fmt.Errorf("Sat state requires genesis")
	}
	p, err := s.deriveSatPreparedBlock(block, prepared)
	if err != nil {
		return err
	}
	b := indexBatch{PreviousBlockHash: block.PreviousBlockHash, Sats: p}
	d := s.definition
	c := indexCheckpoint{Schema: d.CheckpointSchema, Definition: d.ID, Version: d.Version, RuleHash: d.RuleHash, Network: d.Network, From: 0, Height: block.Height, BlockHash: block.Hash, DependencyFingerprint: indexDigest(struct{ Block, Profile string }{block.Hash, satIndexProfile}), RecordsHash: batchRecordsHash(b)}
	if s.checkpoint != nil {
		c.PreviousCommitment = s.checkpoint.Commitment
	}
	c.Commitment = checkpointHash(c)
	b.Checkpoint = c
	if err = ctx.Err(); err != nil {
		return err
	}
	if s.satCommitHook != nil {
		if err = s.satCommitHook("pages_written"); err != nil {
			return err
		}
	}
	if err = atomicWriteJSON(filepath.Join(s.dir, "commits", c.Commitment+".json"), b); err != nil {
		return err
	}
	if err = syncDirectory(filepath.Join(s.dir, "commits")); err != nil {
		return err
	}
	head := s.head
	head.Commitment, head.Retention = c.Commitment, retention
	head.SatSavepoints = satRotateSavepoints077(head.SatSavepoints, c)
	head.SatSavepointsHash = satSavepointsDigest077(head)
	if s.satCommitHook != nil {
		if err = s.satCommitHook("before_head"); err != nil {
			return err
		}
	}
	if err = atomicWriteJSON(filepath.Join(s.dir, "head.json"), head); err != nil {
		return err
	}
	if err = syncDirectory(s.dir); err != nil {
		return err
	}
	s.head = head
	s.checkpoint = &c
	return nil
}
func (s *indexStore) reconcileSatStateSelected(ctx context.Context, selectedTipHeight int64, hashAt func(int64) (string, error)) error {
	satWriterMu.Lock()
	defer satWriterMu.Unlock()
	satStateMu.Lock()
	defer satStateMu.Unlock()
	latest, reloadErr := indexStoreHead(s.profileRoot, "sat-state")
	if reloadErr != nil {
		return reloadErr
	}
	s.head, s.checkpoint = latest.head, latest.checkpoint
	if s.checkpoint == nil {
		return nil
	}
	if err := s.validateSatSavepoints077(ctx); err != nil {
		return err
	}
	tipHash := ""
	if selectedTipHeight >= 0 {
		h, err := hashAt(selectedTipHeight)
		if err != nil {
			return err
		}
		if !validHash(h) {
			return fmt.Errorf("selected Sat tip unavailable")
		}
		tipHash = h
	}
	if selectedTipHeight < 0 || s.checkpoint.Height <= selectedTipHeight {
		h, err := hashAt(s.checkpoint.Height)
		if err != nil {
			return err
		}
		if !validHash(h) {
			return fmt.Errorf("selected Sat anchor unavailable")
		}
		if h == s.checkpoint.BlockHash {
			return nil
		}
	}
	var chosen *indexBatch
	for i := len(s.head.SatSavepoints) - 1; i >= 0; i-- {
		p := s.head.SatSavepoints[i]
		if selectedTipHeight >= 0 && p.Height > selectedTipHeight {
			continue
		}
		h, err := hashAt(p.Height)
		if err != nil {
			return err
		}
		if !validHash(h) {
			return fmt.Errorf("selected Sat savepoint anchor unavailable")
		}
		if h != p.BlockHash {
			continue
		}
		b, err := s.readBatch(p.Commitment)
		if err != nil {
			return err
		}
		if err = s.validateSatRoots077(ctx, *b.Sats); err != nil {
			return err
		}
		chosen = &b
		break
	}
	if chosen == nil {
		return ErrSatRebuildRequired
	}
	if selectedTipHeight >= 0 {
		h, err := hashAt(selectedTipHeight)
		if err != nil {
			return err
		}
		if h != tipHash {
			return fmt.Errorf("selected Sat tip changed during recovery")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	head := s.head
	head.Commitment = chosen.Checkpoint.Commitment
	var points []satSavepoint077
	for _, p := range head.SatSavepoints {
		if p.Height <= chosen.Checkpoint.Height {
			points = append(points, p)
		}
	}
	head.SatSavepoints = points
	head.SatSavepointsHash = satSavepointsDigest077(head)
	if s.satCommitHook != nil {
		if err := s.satCommitHook("before_recovery_head"); err != nil {
			return err
		}
	}
	if err := atomicWriteJSON(filepath.Join(s.dir, "head.json"), head); err != nil {
		return err
	}
	if err := syncDirectory(s.dir); err != nil {
		return err
	}
	s.head = head
	s.checkpoint = &chosen.Checkpoint
	return nil
}
func (s *indexStore) collectSat077State() error {
	satWriterMu.Lock()
	defer satWriterMu.Unlock()
	// A resumed worker may hold an older store object; never reclaim against
	// that object's superseded roots after another publication.
	satStateMu.Lock()
	latest, err := indexStoreHead(s.profileRoot, "sat-state")
	satStateMu.Unlock()
	if err != nil {
		return err
	}
	s.head, s.checkpoint = latest.head, latest.checkpoint
	if s.checkpoint == nil {
		return nil
	}
	ctx := s.ordinalContext()
	if err := s.validateSatSavepoints077(ctx); err != nil {
		return err
	}
	if err := s.pruneSat077Commits(ctx); err != nil {
		return err
	}
	return collectOrdinalPagesContext(ctx, s.ordinalPages(), func(visit func(string) error) error {
		commits := []string{s.head.Commitment}
		for _, p := range s.head.SatSavepoints {
			commits = append(commits, p.Commitment)
		}
		seen := map[string]bool{}
		for _, c := range commits {
			if seen[c] {
				continue
			}
			seen[c] = true
			b, err := s.readBatch(c)
			if err != nil {
				return err
			}
			for _, r := range satSnapshotRoots077(*b.Sats) {
				if err = visit(r); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
func visitSatFactPagesContext077(ctx context.Context, root string, visit func(string) error) error {
	satWriterMu.Lock()
	defer satWriterMu.Unlock()
	satStateMu.Lock()
	s, err := indexStoreHead(root, "sat-state")
	satStateMu.Unlock()
	if err != nil {
		return err
	}
	if s.checkpoint != nil && s.head.SatSavepointsHash != satSavepointsDigest077(s.head) {
		return fmt.Errorf("%w: Sat fact owner manifest", ErrSatStateCorrupt)
	}
	commits := []string{}
	if s.checkpoint != nil {
		commits = append(commits, s.head.Commitment)
	}
	for _, p := range s.head.SatSavepoints {
		commits = append(commits, p.Commitment)
	}
	seen := map[string]bool{}
	for _, c := range commits {
		if seen[c] {
			continue
		}
		seen[c] = true
		b, err := s.readBatch(c)
		if err != nil {
			return err
		}
		if err = newOrdinalTree(s.ordinalPages(), b.Sats.FactPinRoot).withContext(ctx).walk(func(kv ordinalKV) error {
			var n uint64
			if !validHash(kv.Key) || json.Unmarshal(kv.Value, &n) != nil || n == 0 {
				return fmt.Errorf("%w: fact pins", ErrSatStateCorrupt)
			}
			return visit(kv.Key)
		}); err != nil {
			return err
		}
	}
	return visitSatArchivePins077(ctx, root, visit)
}
func resetSatStateForRebuildContext(ctx context.Context, root, jobID string) error {
	satWriterMu.Lock()
	defer satWriterMu.Unlock()
	satStateMu.Lock()
	defer satStateMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	base := filepath.Join(root, "indexes")
	dir := filepath.Join(base, "sat-state")
	archive := filepath.Join(base, "sat-state-archives", indexDigest(jobID))
	marker := filepath.Join(dir, "rebuild-job.json")
	if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == indexDigest(jobID) {
		return nil
	}
	if err := updateapply.CheckPath(dir); err != nil {
		return err
	}
	if err := updateapply.CheckPath(archive); err != nil {
		return err
	}
	if _, err := os.Stat(dir); err == nil {
		if _, e := os.Stat(archive); e == nil {
			entries, readErr := os.ReadDir(dir)
			if readErr != nil {
				return readErr
			}
			if len(entries) != 0 {
				return fmt.Errorf("Sat rebuild archive already exists without authenticated marker")
			}
		} else {
			if !os.IsNotExist(e) {
				return e
			}
			if err = os.MkdirAll(filepath.Dir(archive), 0700); err != nil {
				return err
			}
			if err = os.Rename(dir, archive); err != nil {
				return err
			}
			if err = syncDirectory(filepath.Dir(archive)); err != nil {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := atomicWriteBytes(marker, []byte(indexDigest(jobID))); err != nil {
		return err
	}
	return syncDirectory(base)
}
