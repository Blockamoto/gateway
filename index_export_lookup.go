package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"./internal/updateapply"
)

func (s *indexExportSession) missing(blocator, hash, reason string) {
	position, _ := parseBODCoordinate(blocator)
	s.meta.MissingCount++
	if len(s.meta.Missing) < 100 {
		s.meta.Missing = append(s.meta.Missing, indexExportMissing{Height: position.Height, BlockHash: hash, Blocator: blocator, Reason: reason})
	}
	if s.meta.LookupRange == nil {
		s.meta.LookupRange = &heightInterval{position.Height, position.Height}
	} else {
		if position.Height < s.meta.LookupRange.From {
			s.meta.LookupRange.From = position.Height
		}
		if position.Height > s.meta.LookupRange.To {
			s.meta.LookupRange.To = position.Height
		}
	}
}
func (s *indexExportSession) identity(blocator, blockHash string) (string, error) {
	position, ok := parseBODCoordinate(blocator)
	if !ok || !bitmapValidInscriptionBlocator(blocator) {
		return "", fmt.Errorf("export identity is not a canonical inscription blocator")
	}
	if err := s.anchor(position.Height, blockHash); err != nil {
		return "", err
	}
	if s.q.Representation == "native" {
		return blocator, nil
	}
	var found string
	merge := func(txid string) error {
		if txid == "" {
			return nil
		}
		if !validHash(txid) || strings.ToLower(txid) != txid {
			return fmt.Errorf("invalid committed transaction-ID lookup evidence")
		}
		if found != "" && found != txid {
			return fmt.Errorf("conflicting transaction-ID evidence for %s", blocator)
		}
		found = txid
		return nil
	}
	// One verified Raw block is retained in memory at a time. It is never fetched,
	// published, repaired, or sent through the shared on-demand ingestion hook.
	if !s.rawTried || s.rawHeight != position.Height || s.rawHash != blockHash {
		s.rawTried, s.rawHeight, s.rawHash = true, position.Height, blockHash
		s.rawView = nil
		s.rawIDs = nil
		view, err := s.rawRead(s.ctx, position.Height, blockHash)
		if err != nil {
			return "", err
		}
		s.rawView = view
		if view != nil {
			extracted, err := extractInscriptionOccurrences(*view)
			if err != nil {
				return "", err
			}
			s.rawIDs = map[string]string{}
			for _, o := range extracted.Occurrences {
				s.rawIDs[inscriptionCoordinate(uint32(o.TxIndex), o.Index, o.BlockHeight)] = o.ID
			}
		}
	}
	if s.rawView != nil {
		id, have := s.rawIDs[blocator]
		if !have {
			return "", fmt.Errorf("saved occurrence is absent from its verified Raw reveal block: %s", blocator)
		}
		if err := merge(id[:64]); err != nil {
			return "", err
		}
	}
	for _, p := range s.lookups {
		batch, have, err := s.lookupBatch(p, position.Height)
		if err != nil {
			return "", err
		}
		if !have {
			continue
		}
		if batch.Checkpoint.BlockHash != blockHash {
			return "", fmt.Errorf("lookup source differs from the frozen reveal block at height %d", position.Height)
		}
		txid, err := s.batchTxID(p.s, batch, position.TxIndex, position.InscriptionIndex)
		if err != nil {
			return "", err
		}
		if err = merge(txid); err != nil {
			return "", err
		}
	}
	for _, view := range s.lookupViews {
		commit := view.blocks[strconv.FormatInt(position.Height, 10)]
		if commit == "" {
			continue
		}
		batch, err := s.browseBatch(view, position.Height)
		if err != nil {
			return "", err
		}
		if batch.Checkpoint.Height != position.Height || batch.Checkpoint.BlockHash != blockHash || batch.Checkpoint.From != position.Height || batch.Checkpoint.PreviousCommitment != "" {
			return "", fmt.Errorf("captured sparse lookup differs from reveal identity")
		}
		txid, err := s.batchTxID(view.s, batch, position.TxIndex, position.InscriptionIndex)
		if err != nil {
			return "", err
		}
		if err = merge(txid); err != nil {
			return "", err
		}
	}
	if found == "" {
		s.missing(blocator, blockHash, "transaction-ID lookup evidence and retained Raw reveal bytes are unavailable")
		return "", nil
	}
	return fmt.Sprintf("%si%d", found, position.InscriptionIndex), nil
}
func (s *indexExportSession) lookupBatch(p *indexExportPinnedStore, height int64) (indexBatch, bool, error) {
	if height < p.from || height > p.to {
		return indexBatch{}, false, nil
	}
	if batch, ok := p.cache[height]; ok {
		return batch, true, nil
	}
	var commit string
	var err error
	if p.spill == nil {
		var ready bool
		commit, ready, err = p.s.activeLocatorCommitContext(s.ctx, height)
		if err != nil && s.ctx.Err() != nil {
			return indexBatch{}, false, s.ctx.Err()
		}
		if err != nil || !ready || commit == "" {
			if err = s.buildLookupSpill(p); err != nil {
				return indexBatch{}, false, err
			}
		}
	}
	if p.spill != nil {
		var value [32]byte
		if _, err = p.spill.ReadAt(value[:], (height-p.from)*32); err != nil {
			return indexBatch{}, false, err
		}
		commit = hex.EncodeToString(value[:])
	}
	b, err := s.readBatch(p.s, commit)
	if err != nil {
		return b, false, err
	}
	if b.Checkpoint.Height != height || b.Checkpoint.Commitment == "" {
		return b, false, fmt.Errorf("captured lookup prefix is incomplete")
	}
	if err = s.anchor(height, b.Checkpoint.BlockHash); err != nil {
		return b, false, err
	}
	if len(p.cache) >= 2 {
		p.cache = map[int64]indexBatch{}
	}
	p.cache[height] = b
	return b, true, nil
}

// A missing/stale optional acceleration table never triggers source repair.
// Spill the immutable prefix once to private temporary fixed-width slots; later
// arbitrary lookups are constant-time and memory does not grow with the range.
func (s *indexExportSession) buildLookupSpill(p *indexExportPinnedStore) error {
	if p.from < 0 || p.to < p.from || p.to-p.from > (int64(^uint64(0)>>1)/32)-1 {
		return fmt.Errorf("invalid export lookup span")
	}
	file, err := os.CreateTemp("", "gateway-export-lookup-*.bin")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			name := file.Name()
			_ = file.Close()
			_ = os.Remove(name)
		}
	}()
	count := int64(0)
	err = s.walkStore(p, func(b indexBatch) error {
		height := b.Checkpoint.Height
		if height < p.from || height > p.to {
			return fmt.Errorf("lookup checkpoint outside captured span")
		}
		value, e := hex.DecodeString(b.Checkpoint.Commitment)
		if e != nil || len(value) != 32 {
			return fmt.Errorf("invalid lookup commitment")
		}
		if n, e := file.WriteAt(value, (height-p.from)*32); e != nil {
			return e
		} else if n != 32 {
			return io.ErrShortWrite
		}
		count++
		return nil
	})
	if err != nil {
		return err
	}
	if count != p.to-p.from+1 {
		return fmt.Errorf("captured lookup prefix is incomplete")
	}
	p.spill = file
	keep = true
	return nil
}
func (s *indexExportSession) batchTxID(store *indexStore, b indexBatch, txIndex, inscriptionIndex int) (string, error) {
	var found string
	merge := func(txid string) error {
		if found != "" && found != txid {
			return fmt.Errorf("committed lookup sources disagree at transaction position %d", txIndex)
		}
		found = txid
		return nil
	}
	if b.InscriptionBlock != nil {
		for _, entry := range b.InscriptionBlock.ConventionalIDs {
			if int(entry.TxIndex) == txIndex {
				if err := merge(entry.TxID); err != nil {
					return "", err
				}
			}
		}
	}
	for _, o := range b.Inscriptions {
		if o.TxIndex == txIndex {
			if err := merge(o.TxID); err != nil {
				return "", err
			}
		}
	}
	if store.definition.ID == "inscriptions" {
		coordinate := inscriptionCoordinate(uint32(txIndex), uint32(inscriptionIndex), b.Checkpoint.Height)
		have := false
		for _, value := range inscriptionBatchCoordinates(b) {
			if value == coordinate {
				have = true
				break
			}
		}
		if !have {
			return "", nil
		}
	}
	if b.TransactionLocator != nil && store.definition.ID == inscriptionLocatorIndex {
		definition, err := findIndexDefinition("inscriptions")
		if err != nil {
			return "", err
		}
		sourceStore := &indexStore{definition: definition, dir: filepath.Join(s.a.dataDir, "indexes", "inscriptions")}
		source, err := s.readBatch(sourceStore, b.TransactionLocator.SourceCommitment)
		if err != nil {
			return "", err
		}
		if source.Checkpoint.BlockHash != b.Checkpoint.BlockHash {
			return "", fmt.Errorf("related locator reveal source anchor differs")
		}
		if err = validateSparseInscriptionSource(b.TransactionLocator, source); err != nil {
			return "", err
		}
	}
	// Sparse array offsets are not transaction positions. Both the modern and
	// authenticated legacy payload adapters expose explicit TxIndex values.
	for _, entry := range transactionBatchEntries(b) {
		if int(entry.TxIndex) == txIndex {
			if err := merge(entry.TxID); err != nil {
				return "", err
			}
		}
	}
	return found, nil
}
func (s *indexExportSession) readRaw(ctx context.Context, height int64, hash string) (*blockView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rawHash, _ := displayHashRaw(hash)
	target := blockTarget{Height: height, HashDisplay: hash, HashRaw: rawHash}
	if header, e := s.a.readSelectedHeader(height); e == nil {
		h := hash256(header)
		if reverseHex(h[:]) == hash {
			target.ExpectedHeader = header
		}
	}
	receipt, err := rawSegmentReceiptAt(ctx, s.a.dataDir, target)
	if err != nil {
		return nil, err
	}
	if receipt != nil && receipt.Segment != nil {
		view, e := s.readRawSegment(ctx, target, receipt)
		if e != nil {
			return nil, e
		}
		return &view, ctx.Err()
	}
	legacyPath, err := indexedRawBodyPath(s.a.dataDir, hash)
	if err != nil {
		return nil, err
	}
	if _, e := os.Stat(legacyPath); e == nil {
		if e = s.charge(legacyPath); e != nil {
			return nil, e
		}
		_, view, e := readRetainedIndexBlock(legacyPath, target)
		if e != nil {
			return nil, e
		}
		return &view, ctx.Err()
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	// These are leaf reads only. In particular do not use the unified local
	// provider helper, which also includes live Core RPC and ingestion paths.
	archiveRaw, archiveReceipt, e := s.a.archivePaths(hash)
	if e != nil {
		return nil, e
	}
	if _, e = os.Stat(archiveReceipt); e == nil {
		if e = s.charge(archiveReceipt); e != nil {
			return nil, e
		}
		if e = s.charge(archiveRaw); e != nil {
			return nil, e
		}
		raw, e := s.a.archiveBlock(hash)
		if e != nil {
			return nil, e
		}
		return s.decodeLocalRaw(ctx, target, raw, "read-only local archive export source")
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	s.a.cacheMu.RLock()
	entry, have := s.a.cacheIndex.Blocks[hash]
	s.a.cacheMu.RUnlock()
	if have {
		if entry.Hash != hash || entry.Height != height {
			return nil, fmt.Errorf("cached export block anchor differs")
		}
		path := filepath.Join(s.a.dataDir, "blocks", "raw", entry.Prefix+".block")
		if e = s.charge(path); e != nil {
			return nil, e
		}
		data, e := s.a.cachedBlockPayloadUnverified(hash)
		if e != nil {
			return nil, e
		}
		if data.Height != height || data.BlockHash != hash {
			return nil, fmt.Errorf("cached export block changed")
		}
		return s.decodeLocalRaw(ctx, target, data.Raw, "read-only local cache export source")
	}
	s.a.coreStoreMu.RLock()
	loc, mounted := s.a.coreStore.ByHash[hash]
	s.a.coreStoreMu.RUnlock()
	if mounted {
		if loc.Length < 81 || loc.Length > rawSegmentBlockBytes || loc.Height != height {
			return nil, fmt.Errorf("mounted export block identity or size differs")
		}
		data, e := s.a.mountedCoreBlock(hash)
		if e != nil {
			return nil, e
		}
		if data.Height != height || data.BlockHash != hash {
			return nil, fmt.Errorf("mounted export block changed")
		}
		return s.decodeLocalRaw(ctx, target, data.Raw, "read-only mounted block export source")
	}
	return nil, nil
}
func (s *indexExportSession) decodeLocalRaw(ctx context.Context, target blockTarget, raw []byte, source string) (*blockView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(raw) > rawSegmentBlockBytes {
		return nil, fmt.Errorf("local export block exceeds verified block bound")
	}
	view, err := parseBlockDetailed(target.Height, target.HashDisplay, target.ExpectedHeader, raw, source, false)
	if err != nil {
		return nil, err
	}
	if !integrityVerified(view) {
		return nil, fmt.Errorf("local export block integrity unavailable")
	}
	return &view, ctx.Err()
}
func (s *indexExportSession) readRawSegment(ctx context.Context, target blockTarget, r *indexedRawBody) (blockView, error) {
	if !rawSegmentSnapshotMu.TryRLock() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return blockView{}, ctx.Err()
			case <-ticker.C:
				if rawSegmentSnapshotMu.TryRLock() {
					goto locked
				}
			}
		}
	}
locked:
	defer rawSegmentSnapshotMu.RUnlock()
	if err := ctx.Err(); err != nil {
		return blockView{}, err
	}
	if r == nil || r.Segment == nil || r.Segment.Offset < 0 || r.Bytes < 81 || r.Bytes > rawSegmentBlockBytes || r.Segment.Offset > rawSegmentBytes-int64(r.Bytes)-rawSegmentFrameBytes {
		return blockView{}, fmt.Errorf("invalid retained Raw export receipt")
	}
	if pruned, err := rawSegmentPruned(ctx, s.a.dataDir, *r.Segment); err != nil || pruned {
		if err != nil {
			return blockView{}, err
		}
		return blockView{}, fmt.Errorf("retained Raw export body was pruned")
	}
	path := rawSegmentPath(s.a.dataDir, r.Segment.File)
	if err := updateapply.CheckPath(path); err != nil {
		return blockView{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return blockView{}, err
	}
	defer f.Close()
	frame := make([]byte, rawSegmentFrameBytes)
	if _, err = io.ReadFull(io.NewSectionReader(f, r.Segment.Offset, rawSegmentFrameBytes), frame); err != nil {
		return blockView{}, err
	}
	height := int64(binary.LittleEndian.Uint64(frame[12:]))
	if !bytes.Equal(frame[:8], rawSegmentMagic[:]) || int(binary.LittleEndian.Uint32(frame[8:])) != r.Bytes || (height != target.Height && height != -1) || reverseHex(frame[20:52]) != target.HashDisplay {
		return blockView{}, fmt.Errorf("Raw export frame identity differs")
	}
	raw := make([]byte, r.Bytes)
	if _, err = io.ReadFull(io.NewSectionReader(f, r.Segment.Offset+rawSegmentFrameBytes, int64(r.Bytes)), raw); err != nil {
		return blockView{}, err
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != r.SHA256 || hex.EncodeToString(raw[:80]) != r.Header {
		return blockView{}, fmt.Errorf("Raw export body digest differs")
	}
	if err = ctx.Err(); err != nil {
		return blockView{}, err
	}
	view, err := parseBlockDetailed(target.Height, target.HashDisplay, target.ExpectedHeader, raw, "read-only retained Raw export source", false)
	if err == nil && r.transactionCount > 0 && view.TransactionCount != r.transactionCount {
		return view, fmt.Errorf("Raw export transaction denominator differs")
	}
	return view, err
}
