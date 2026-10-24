package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"./internal/updateapply"
)

// Browsed singleton receipts have their own visibility boundary. They never
// advance the contiguous range head, enable Live, or imply intervening coverage.
const indexBrowseBudget int64 = 64 << 20

var indexBrowseMu sync.Mutex

type indexBrowsePolicy struct {
	Index      string `json:"index"`
	Enabled    bool   `json:"enabled"`
	Available  bool   `json:"available"`
	Capability string `json:"capability"`
	Reason     string `json:"reason"`
}
type indexBrowseRequest struct {
	Index   string `json:"index"`
	Enabled *bool  `json:"enabled"`
}
type indexBrowseResult struct {
	Index string `json:"index"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}
type indexBrowseHead struct {
	Schema int               `json:"schema"`
	Blocks map[string]string `json:"blocks"` // actual height -> immutable singleton commit
}

func lockIndexBrowse(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if indexBrowseMu.TryLock() {
		return nil
	}
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if indexBrowseMu.TryLock() {
				return nil
			}
		}
	}
}
func indexBrowseAvailable(id string) bool {
	return (id == "tx-locator" || id == "inscriptions" || id == "txo-spender") && releaseFeatureAvailable(id)
}
func boundedBrowseJSON(path string, value any) error {
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, indexBrowseBudget+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > indexBrowseBudget {
		return fmt.Errorf("browsed index metadata exceeds budget")
	}
	return json.Unmarshal(data, value)
}
func (a *app) indexBrowsePolicies() ([]indexBrowsePolicy, error) {
	return a.indexBrowsePoliciesContext(context.Background())
}
func (a *app) indexBrowsePoliciesContext(ctx context.Context) ([]indexBrowsePolicy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	doc, err := readIndexRunDocument(a.dataDir)
	if err != nil {
		return nil, err
	}
	return resolvedIngestionCapabilities(doc), nil
}
func (a *app) setIndexBrowsePolicy(ctx context.Context, req indexBrowseRequest) (indexBrowsePolicy, error) {
	req.Index = strings.ToLower(strings.TrimSpace(req.Index))
	if req.Enabled == nil {
		return indexBrowsePolicy{}, fmt.Errorf("explicit enabled choice required")
	}
	if !indexBrowseAvailable(req.Index) {
		return indexBrowsePolicy{}, fmt.Errorf("optional facts from viewed blocks are not available for this index")
	}
	if err := lockIndexBrowse(ctx); err != nil {
		return indexBrowsePolicy{}, err
	}
	defer indexBrowseMu.Unlock()
	// Compatibility API: an explicit old browse choice now changes the same
	// timeline Arm control. No second policy file or hidden prefix build exists.
	// Serialize with Start while choosing idle preparation versus active admission.
	a.indexLiveControlMu.Lock()
	doc := a.indexRunSnapshot()
	if doc.Error != "" {
		a.indexLiveControlMu.Unlock()
		return indexBrowsePolicy{}, fmt.Errorf("%s", doc.Error)
	}
	choice, exists := runSelection(doc.Prepared, req.Index)
	if !exists {
		choice = indexRunSelection{Index: req.Index}
	}
	choice.Armed = *req.Enabled
	var err error
	if r := doc.Run; r != nil && (r.State == "running" || r.State == "waiting" || r.State == "paused") {
		// The amendment carries its own admission fence and acquires this lock.
		a.indexLiveControlMu.Unlock()
		_, err = a.amendIndexRun(ctx, indexRunAmendment{RunID: r.ID, AdmissionRevision: effectiveIndexRunAdmissionRevision(r), Consumers: []indexRunSelection{choice}})
	} else {
		replaceIndexRunSelection(&doc.Prepared, choice)
		if err = ctx.Err(); err == nil {
			_, err = a.prepareIndexRun(doc.Prepared)
		}
		a.indexLiveControlMu.Unlock()
	}
	if err != nil {
		return indexBrowsePolicy{}, err
	}
	return indexBrowsePolicy{Index: req.Index, Enabled: *req.Enabled, Available: true, Capability: "sparse", Reason: "Verified observed blocks only; no predecessor scan."}, nil
}
func (a *app) browseStore(id string) (*indexStore, indexBrowseHead, error) {
	d, err := findIndexDefinition(id)
	if err != nil || !indexBrowseAvailable(id) {
		return nil, indexBrowseHead{}, fmt.Errorf("unsupported browsed index")
	}
	s := &indexStore{profileRoot: a.dataDir, dir: filepath.Join(a.dataDir, "indexes", "browse", id), definition: d, head: indexHead{Mode: "lean"}}
	head := indexBrowseHead{Schema: 1, Blocks: map[string]string{}}
	err = boundedBrowseJSON(filepath.Join(s.dir, "head.json"), &head)
	if os.IsNotExist(err) {
		err = nil
	}
	if err != nil {
		return nil, head, err
	}
	if head.Schema != 1 || head.Blocks == nil {
		return nil, head, fmt.Errorf("unsupported browsed index head")
	}
	for height, commit := range head.Blocks {
		n, e := strconv.ParseInt(height, 10, 64)
		if e != nil || n < 0 || strconv.FormatInt(n, 10) != height || !validHash(commit) {
			return nil, head, fmt.Errorf("invalid browsed index membership")
		}
	}
	return s, head, nil
}
func singletonBrowseBatch(id string, block blockView, extracted *inscriptionBlockResult) (indexBatch, error) {
	return singletonBrowseBatchMode(indexRunSelection{Index: id, indexRunOptions: indexRunOptions{Mode: "lean", ConventionalIDs: boolPointer(false)}}, block, extracted)
}
func singletonBrowseBatchMode(choice indexRunSelection, block blockView, extracted *inscriptionBlockResult) (indexBatch, error) {
	id := choice.Index
	if !integrityVerified(block) || (!block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated) || block.Height < 0 || len(block.Transactions) == 0 || block.TransactionCount != uint64(len(block.Transactions)) {
		return indexBatch{}, fmt.Errorf("browsing index requires verified selected-chain block")
	}
	for i, tx := range block.Transactions {
		if tx.Index != i || !validHash(tx.TxID) {
			return indexBatch{}, fmt.Errorf("browsing index requires complete ordered transactions")
		}
	}
	d, err := findIndexDefinition(id)
	if err != nil {
		return indexBatch{}, err
	}
	b := indexBatch{PreviousBlockHash: block.PreviousBlockHash}
	if id == "tx-locator" {
		b.TransactionLocator = transactionLocatorFromBlock(block)
	} else if id == "inscriptions" {
		if extracted == nil {
			result, e := extractInscriptionOccurrences(block)
			if e != nil {
				return b, e
			}
			extracted = &result
		}
		if extracted.BlockHeight != block.Height || extracted.BlockHash != block.Hash || extracted.ParserProfile != inscriptionParserProfile {
			return b, fmt.Errorf("inscription extraction does not match browsed block")
		}
		mode := choice.Mode
		if mode == "" {
			mode = "lean"
		}
		b.InscriptionBlock = &inscriptionBlockMetadata{Mode: mode, TransactionCount: uint32(len(block.Transactions))}
		b.InscriptionCoordinates = []string{}
		for _, o := range extracted.Occurrences {
			b.InscriptionCoordinates = append(b.InscriptionCoordinates, inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height))
		}
		if mode == "full" {
			b.Inscriptions = append([]inscriptionOccurrence(nil), extracted.Occurrences...)
		}
		if inscriptionIDsEnabled(choice.ConventionalIDs) {
			seen := map[int]bool{}
			for _, o := range extracted.Occurrences {
				if !seen[o.TxIndex] {
					b.InscriptionBlock.ConventionalIDs = append(b.InscriptionBlock.ConventionalIDs, transactionLocatorEntry{TxIndex: uint32(o.TxIndex), TxID: o.TxID})
					seen[o.TxIndex] = true
				}
			}
		}
	} else {
		return b, fmt.Errorf("unsupported browsing index")
	}
	c := indexCheckpoint{Schema: d.CheckpointSchema, Definition: id, Version: d.Version, RuleHash: d.RuleHash, Network: d.Network, From: block.Height, Height: block.Height, BlockHash: block.Hash, DependencyFingerprint: indexDigest(struct{ Block, Parser, Reference string }{block.Hash, inscriptionParserProfile, inscriptionReferenceCommit}), RecordsHash: batchRecordsHash(b)}
	if b.TransactionLocator != nil {
		c.DependencyFingerprint = indexDigest(struct{ Block, Selection, Source string }{block.Hash, b.TransactionLocator.Selection, b.TransactionLocator.SourceCommitment})
	}
	c.Commitment = checkpointHash(c)
	b.Checkpoint = c
	if id == "inscriptions" {
		err = validateInscriptionPayload(b)
	} else {
		err = validateTransactionPayload(b, id)
	}
	return b, err
}
func (a *app) persistBrowseBatch(ctx context.Context, id string, b indexBatch) (bool, error) {
	if err := checkTimelineTrackArmed(ctx, a.dataDir, id); err != nil {
		return false, err
	}
	s, head, err := a.browseStore(id)
	if err != nil {
		return false, err
	}
	key := strconv.FormatInt(b.Checkpoint.Height, 10)
	if head.Blocks[key] == b.Checkpoint.Commitment {
		_, err = s.readBatch(b.Checkpoint.Commitment)
		return false, err
	}
	if err = updateapply.CheckPath(s.dir); err != nil {
		return false, err
	}
	if err = atomicWriteJSON(filepath.Join(s.dir, "commits", b.Checkpoint.Commitment+".json"), b); err != nil {
		return false, err
	}
	if err = syncDirectory(filepath.Join(s.dir, "commits")); err != nil {
		return false, err
	}
	// TX acceleration is advisory. The immutable batch and head membership are
	// validated on every lookup, so interrupted new pointers are invisible.
	if id == "tx-locator" {
		groups := map[string][]indexLocatorRecord{}
		for _, entry := range transactionBatchEntries(b) {
			prefix := entry.TxID[:2]
			groups[prefix] = append(groups[prefix], indexLocatorRecord{TxID: entry.TxID, Commit: b.Checkpoint.Commitment, Height: b.Checkpoint.Height, TxIndex: int(entry.TxIndex)})
		}
		prefixes := []string{}
		for prefix := range groups {
			prefixes = append(prefixes, prefix)
		}
		sort.Strings(prefixes)
		for _, prefix := range prefixes {
			if err = ctx.Err(); err != nil {
				return false, err
			}
			path := filepath.Join(s.dir, "locators", prefix+".json")
			rows := []indexLocatorRecord{}
			err = boundedBrowseJSON(path, &rows)
			if err != nil && !os.IsNotExist(err) {
				return false, err
			}
			seen := map[string]bool{}
			for _, r := range rows {
				seen[fmt.Sprintf("%s:%s:%d", r.TxID, r.Commit, r.TxIndex)] = true
			}
			for _, r := range groups[prefix] {
				k := fmt.Sprintf("%s:%s:%d", r.TxID, r.Commit, r.TxIndex)
				if !seen[k] {
					rows = append(rows, r)
					seen[k] = true
				}
			}
			data, e := json.Marshal(rows)
			if e != nil {
				return false, e
			}
			if int64(len(data)) > indexBrowseBudget {
				return false, fmt.Errorf("browsed transaction shard exceeds budget")
			}
			if err = atomicWriteBytes(path, data); err != nil {
				return false, err
			}
		}
		if err = syncDirectory(filepath.Join(s.dir, "locators")); err != nil {
			return false, err
		}
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	hash, err := a.canonicalHashAtHeight(b.Checkpoint.Height)
	if err != nil {
		return false, err
	}
	if hash != b.Checkpoint.BlockHash {
		return false, fmt.Errorf("selected chain changed while indexing browsed block")
	}
	head.Blocks[key] = b.Checkpoint.Commitment
	data, err := json.Marshal(head)
	if err != nil {
		return false, err
	}
	if int64(len(data)) > indexBrowseBudget {
		return false, fmt.Errorf("browsed index coverage exceeds budget")
	}
	if err = checkTimelineTrackArmed(ctx, a.dataDir, id); err != nil {
		return false, err
	}
	if err = atomicWriteBytes(filepath.Join(s.dir, "head.json"), data); err != nil {
		return false, err
	}
	if err = syncDirectory(s.dir); err != nil {
		return false, err
	}
	return true, nil
}
func (a *app) indexBrowsedBlock(ctx context.Context, block blockView, extracted *inscriptionBlockResult) []indexBrowseResult {
	return a.ingestResolvedVerifiedBlock(ctx, block)
}

// Consult only authenticated point membership. A navigation must not repair an
// old locator, walk an index prefix, or duplicate a complete saved block.
func (a *app) completeIndexedBrowseBlock(ctx context.Context, id string, block blockView) (bool, error) {
	s, err := indexStoreHead(a.dataDir, id)
	if err != nil {
		return false, err
	}
	if s.checkpoint == nil || block.Height < s.checkpoint.From || block.Height > s.checkpoint.Height {
		return false, nil
	}
	commit, ready, err := s.activeLocatorCommitContext(ctx, block.Height)
	if err != nil || !ready || commit == "" {
		return false, err
	}
	b, err := s.readBatch(commit)
	if err != nil || b.Checkpoint.BlockHash != block.Hash {
		return false, err
	}
	canonical, err := a.canonicalHashAtHeight(block.Height)
	if err != nil || canonical != block.Hash {
		return false, err
	}
	if id == "tx-locator" {
		return b.TransactionLocator != nil && b.TransactionLocator.Selection == "all" || b.Bitcoin != nil && block.TransactionCount > 0 && uint64(len(b.Bitcoin.TxIDs)) == block.TransactionCount, nil
	}
	return id == "inscriptions" && inscriptionBatchCount(b) != nil, nil
}
func (a *app) browsedBatchContext(ctx context.Context, id string, height int64, hash string) (indexBatch, bool, error) {
	if err := ctx.Err(); err != nil {
		return indexBatch{}, false, err
	}
	s, head, err := a.browseStore(id)
	if err != nil {
		return indexBatch{}, false, err
	}
	commit := head.Blocks[strconv.FormatInt(height, 10)]
	if commit == "" {
		return indexBatch{}, false, nil
	}
	b, err := s.readBatch(commit)
	if err != nil {
		return b, false, err
	}
	if b.Checkpoint.Height != height || b.Checkpoint.From != height || b.Checkpoint.PreviousCommitment != "" {
		return b, false, fmt.Errorf("invalid browsed singleton receipt")
	}
	if hash == "" {
		hash, err = a.canonicalHashAtHeight(height)
		if err != nil {
			return b, false, err
		}
	}
	return b, b.Checkpoint.BlockHash == hash, nil
}
func (a *app) browsedTransactionLocations(ctx context.Context, txid, hash string, before int64) ([]txLocation, error) {
	if !validHash(txid) {
		return nil, fmt.Errorf("invalid transaction ID")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, head, err := a.browseStore("tx-locator")
	if err != nil {
		return nil, err
	}
	rows := []indexLocatorRecord{}
	err = boundedBrowseJSON(filepath.Join(s.dir, "locators", txid[:2]+".json"), &rows)
	if os.IsNotExist(err) {
		return a.browsedInscriptionIDLocations(ctx, txid, hash, before)
	}
	if err != nil {
		return nil, err
	}
	matches := []txLocation{}
	for _, r := range rows {
		if r.TxID != txid || head.Blocks[strconv.FormatInt(r.Height, 10)] != r.Commit || before >= 0 && r.Height > before {
			continue
		}
		b, current, e := a.browsedBatchContext(ctx, "tx-locator", r.Height, "")
		if e != nil {
			return nil, e
		}
		if !current || hash != "" && hash != b.Checkpoint.BlockHash {
			continue
		}
		entries := transactionBatchEntries(b)
		if r.TxIndex < 0 || r.TxIndex >= len(entries) || entries[r.TxIndex].TxID != txid {
			return nil, fmt.Errorf("browsed transaction pointer differs from committed position")
		}
		matches = append(matches, txLocation{TxID: txid, Height: r.Height, BlockHash: b.Checkpoint.BlockHash, TxIndex: r.TxIndex})
	}
	additional, err := a.browsedInscriptionIDLocations(ctx, txid, hash, before)
	return append(matches, additional...), err
}

func (a *app) browsedInscriptionIDLocations(ctx context.Context, txid, hash string, before int64) ([]txLocation, error) {
	s, head, err := a.browseStore("inscriptions")
	if err != nil {
		return nil, err
	}
	out := []txLocation{}
	n := 0
	for _, commit := range head.Blocks {
		if n >= indexExplorerMaxBatches {
			break
		}
		n++
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		b, err := s.readBatch(commit)
		if err != nil {
			return nil, err
		}
		if before >= 0 && b.Checkpoint.Height > before || hash != "" && b.Checkpoint.BlockHash != hash {
			continue
		}
		selected, err := a.canonicalHashAtHeight(b.Checkpoint.Height)
		if err != nil {
			return nil, err
		}
		if selected != b.Checkpoint.BlockHash {
			continue
		}
		if b.InscriptionBlock == nil {
			continue
		}
		for _, entry := range b.InscriptionBlock.ConventionalIDs {
			if entry.TxID == txid {
				out = append(out, txLocation{TxID: txid, Height: b.Checkpoint.Height, BlockHash: b.Checkpoint.BlockHash, TxIndex: int(entry.TxIndex)})
			}
		}
	}
	return out, nil
}
func (a *app) browseTimelineSource(id string, h *timelineLocalReader) (indexTimelineSource, error) {
	s, head, err := a.browseStore(id)
	if err != nil {
		return indexTimelineSource{}, err
	}
	out := indexTimelineSource{ID: "gateway_browsed_index", Kind: "browse_records", Ranges: []heightInterval{}, Complete: true, Verification: "selected_chain", Note: "Individually committed browsed blocks; no sequential resume or intervening coverage is implied."}
	heights := make([]int64, 0, len(head.Blocks))
	for height := range head.Blocks {
		n, _ := strconv.ParseInt(height, 10, 64)
		heights = append(heights, n)
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] > heights[j] })
	if len(heights) > timelineSourceLimit {
		heights, out.Complete = heights[:timelineSourceLimit], false
	}
	for _, n := range heights {
		commit := head.Blocks[strconv.FormatInt(n, 10)]
		b, e := s.readBatch(commit)
		if e != nil {
			return out, e
		}
		if b.Checkpoint.Height != n || b.Checkpoint.From != n || b.Checkpoint.PreviousCommitment != "" {
			return out, fmt.Errorf("invalid browsed coverage receipt")
		}
		if h.matches(n, b.Checkpoint.BlockHash) {
			out.Ranges = append(out.Ranges, heightInterval{n, n})
		}
	}
	out.Ranges = mergeIntervals(out.Ranges)
	return out, nil
}

// Provider status shares the same receipt checks as the timeline. Pin local
// headers and inspect bounded saved metadata only; never ask remote providers.
func (a *app) browseProviderSource(id string) indexTimelineSource {
	a.headerChainMu.RLock()
	defer a.headerChainMu.RUnlock()
	unknown := indexTimelineSource{Complete: false}
	if _, err := os.Stat(headerAppendMarkerPath051(a.headersPath)); !os.IsNotExist(err) {
		return unknown
	}
	f, err := os.Open(a.headersPath)
	if err != nil {
		return unknown
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size()%80 != 0 {
		return unknown
	}
	source, err := a.browseTimelineSource(id, &timelineLocalReader{file: f, count: info.Size() / 80, hashes: map[int64]string{}})
	if err != nil {
		return unknown
	}
	return source
}

func (a *app) browseQueryRows(ctx context.Context, id string, limit int, seen map[string]bool) ([]any, int, int, bool, error) {
	s, head, err := a.browseStore(id)
	if err != nil {
		return nil, 0, 0, false, err
	}
	heights := make([]int64, 0, len(head.Blocks))
	for key := range head.Blocks {
		n, _ := strconv.ParseInt(key, 10, 64)
		heights = append(heights, n)
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] > heights[j] })
	rows := []any{}
	total, inspected := 0, 0
	for _, height := range heights {
		if inspected >= 512 || len(rows) >= limit {
			return rows, total, inspected, false, nil
		}
		if err = ctx.Err(); err != nil {
			return rows, total, inspected, false, err
		}
		b, e := s.readBatch(head.Blocks[strconv.FormatInt(height, 10)])
		if e != nil {
			return rows, total, inspected, false, e
		}
		inspected++
		if b.Checkpoint.Height != height || b.Checkpoint.From != height || b.Checkpoint.PreviousCommitment != "" {
			return rows, total, inspected, false, fmt.Errorf("invalid browsed query receipt")
		}
		hash, e := a.canonicalHashAtHeight(height)
		if e != nil {
			return rows, total, inspected, false, e
		}
		if hash != b.Checkpoint.BlockHash || seen[fmt.Sprintf("%d:%s", height, hash)] {
			continue
		}
		if id == "tx-locator" {
			rows = append(rows, b.TransactionLocator)
			total++
		} else if id == "txo-spender" {
			for i := range b.Spenders.Rows {
				rows = append(rows, indexSpendMatch{txioSpendRow(b.Spenders, i), height, b.Checkpoint.BlockHash})
				total++
			}
		} else {
			coordinates := inscriptionBatchCoordinates(b)
			rows = append(rows, struct {
				Inscriptions     []string `json:"inscriptions"`
				Height           int64    `json:"block_height"`
				TransactionCount *uint32  `json:"transaction_count,omitempty"`
				Mode             string   `json:"mode"`
			}{coordinates, height, inscriptionBatchCount(b), "lean"})
			total += len(coordinates)
		}
	}
	return rows, total, inspected, true, nil
}
