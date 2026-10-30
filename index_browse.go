package main

import (
	"container/list"
	"context"
	"crypto/sha256"
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

func (a *app) lockInscriptionBrowseRange(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.indexRunMu.TryLock() {
		return nil
	}
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if a.indexRunMu.TryLock() {
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
	// Serialize with Start. During an active run this choice is staged for the
	// next reviewed Start and does not change its admitted scope.
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
	active := doc.Run != nil && (doc.Run.State == "running" || doc.Run.State == "waiting" || doc.Run.State == "pausing" || doc.Run.State == "paused")
	var err error
	replaceIndexRunSelection(&doc.Prepared, choice)
	if err = ctx.Err(); err == nil {
		_, err = a.prepareIndexRun(doc.Prepared)
	}
	a.indexLiveControlMu.Unlock()
	if err != nil {
		return indexBrowsePolicy{}, err
	}
	if active {
		current, _ := runSelection(doc.Run.Request, req.Index)
		return indexBrowsePolicy{Index: req.Index, Enabled: current.Armed, Available: true, Capability: "sparse", Reason: "The choice is staged for a later reviewed Start; the admitted run is unchanged."}, nil
	}
	return indexBrowsePolicy{Index: req.Index, Enabled: *req.Enabled, Available: true, Capability: "sparse", Reason: "Verified observed blocks only; no predecessor scan."}, nil
}
func (a *app) newBrowseStore(id string) *indexStore {
	d, _ := findIndexDefinition(id)
	s := &indexStore{profileRoot: a.dataDir, dir: filepath.Join(a.dataDir, "indexes", "browse", id), definition: d, head: indexHead{Mode: "lean"}}
	if id == "tx-locator" {
		s.head.LocatorLayout = indexLocatorPackedLayout
	}
	return s
}
func (a *app) rawBrowseStore(id string) (*indexStore, indexBrowseHead, error) {
	if !indexBrowseAvailable(id) {
		return nil, indexBrowseHead{}, fmt.Errorf("unsupported browsed index")
	}
	s := a.newBrowseStore(id)
	head := indexBrowseHead{Schema: 1, Blocks: map[string]string{}}
	err := boundedBrowseJSON(filepath.Join(s.dir, "head.json"), &head)
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
func (a *app) browseStore(id string) (*indexStore, indexBrowseHead, error) {
	s, head, err := a.rawBrowseStore(id)
	if err == nil && id == "tx-locator" {
		remaining := indexBrowseBudget
		for height, commit := range head.Blocks {
			remaining -= int64(128 + len(height) + len(commit))
		}
		if remaining < 0 {
			return s, head, fmt.Errorf("TX point membership projection exceeds its memory budget; availability is unknown")
		}
		err = walkBrowseTXPoints(context.Background(), a.dataDir, func(point indexBrowseTXPoint) error {
			key := strconv.FormatInt(point.Height, 10)
			if _, exists := head.Blocks[key]; !exists {
				remaining -= int64(128 + len(key) + len(point.Commitment))
				if remaining < 0 {
					return fmt.Errorf("TX point membership projection exceeds its memory budget; availability is unknown")
				}
			}
			head.Blocks[key] = point.Commitment
			return nil
		})
	}
	return s, head, err
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
		b.TransactionLocator, err = transactionLocatorFromRecipe(context.Background(), block)
		if err != nil {
			return b, err
		}
	} else if id == "inscriptions" {
		if extracted == nil {
			result, e := extractInscriptionOccurrences(block)
			if e != nil {
				return b, e
			}
			extracted = &result
		}
		if err := validateCompleteInscriptionInventory(block, extracted); err != nil {
			return b, err
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
	if id == "inscriptions" {
		d, err := readIndexRunDocument(a.dataDir)
		if err != nil {
			return false, err
		}
		if err = resolvedAdmissionAllowedForRoot(a.dataDir, d, id, b.Checkpoint.Height); err != nil {
			return false, err
		}
	}
	if id == "tx-locator" {
		return a.persistBrowseTXPointBatch(ctx, a.newBrowseStore(id), b)
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
	commitPath := filepath.Join(s.dir, "commits", b.Checkpoint.Commitment+".json")
	if id == "inscriptions" {
		// Serialize this record write with prepared/run range changes. A
		// later head check alone would leave an out-of-range orphan commit.
		if err = a.lockInscriptionBrowseRange(ctx); err != nil {
			return false, err
		}
		current, e := readIndexRunDocument(a.dataDir)
		if e == nil {
			choice, ok := runSelection(indexRunEffectiveRequest(current), id)
			if !current.Configured || !ok || !choice.Armed {
				e = errIndexRunConsumerStopped
			} else {
				e = resolvedAdmissionAllowedForRoot(a.dataDir, current, id, b.Checkpoint.Height)
			}
		}
		if e == nil {
			e = ctx.Err()
		}
		if e == nil {
			e = atomicWriteJSON(commitPath, b)
		}
		a.indexRunMu.Unlock()
		err = e
	} else {
		err = atomicWriteJSON(commitPath, b)
	}
	if err != nil {
		return false, err
	}
	if err = syncDirectory(filepath.Join(s.dir, "commits")); err != nil {
		return false, err
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
	if id == "inscriptions" {
		d, e := readIndexRunDocument(a.dataDir)
		if e != nil {
			return false, e
		}
		if e = resolvedAdmissionAllowedForRoot(a.dataDir, d, id, b.Checkpoint.Height); e != nil {
			return false, e
		}
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
	if extracted != nil {
		if err := validateCompleteInscriptionInventory(block, extracted); err != nil {
			return []indexBrowseResult{{Index: "inscriptions", State: "unavailable", Error: err.Error()}}
		}
	}
	if validateCompleteInscriptionInventory(block, block.inscriptionExtraction) != nil {
		block.inscriptionExtraction = extracted
	}
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
	return id == "inscriptions" && inscriptionBatchInventoryComplete(b), nil
}
func (a *app) browsedBatchContext(ctx context.Context, id string, height int64, hash string) (indexBatch, bool, error) {
	if err := ctx.Err(); err != nil {
		return indexBatch{}, false, err
	}
	var s *indexStore
	var commit, pointHash string
	var err error
	if id == "tx-locator" {
		s, commit, pointHash, err = a.browseTXPointCommit(ctx, height)
	} else {
		var head indexBrowseHead
		s, head, err = a.browseStore(id)
		commit = head.Blocks[strconv.FormatInt(height, 10)]
	}
	if err != nil {
		return indexBatch{}, false, err
	}
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
	if pointHash != "" && b.Checkpoint.BlockHash != pointHash {
		return b, false, fmt.Errorf("TX point receipt differs from its committed block")
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
	s := a.newBrowseStore("tx-locator")
	rows := []indexLocatorRecord{}
	err := boundedBrowseJSON(filepath.Join(s.dir, "locators", txid[:2]+".json"), &rows)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	packed, err := s.bitcoinLocatorRecordsReadOnly(ctx, txid)
	if err != nil {
		return nil, err
	}
	rows = append(rows, packed...)
	matches := []txLocation{}
	seen := map[string]bool{}
	for _, r := range rows {
		if r.TxID != txid || before >= 0 && r.Height > before {
			continue
		}
		b, current, e := a.browsedBatchContext(ctx, "tx-locator", r.Height, "")
		if e != nil {
			return nil, e
		}
		if !current || b.Checkpoint.Commitment != r.Commit || hash != "" && hash != b.Checkpoint.BlockHash {
			continue
		}
		entries := transactionBatchEntries(b)
		if r.TxIndex < 0 || r.TxIndex >= len(entries) || entries[r.TxIndex].TxID != txid {
			return nil, fmt.Errorf("browsed transaction pointer differs from committed position")
		}
		key := fmt.Sprintf("%s:%d", r.Commit, r.TxIndex)
		if !seen[key] {
			if len(matches) >= 128 {
				return nil, fmt.Errorf("transaction has too many point incarnations; use its containing block")
			}
			seen[key] = true
			matches = append(matches, txLocation{TxID: txid, Height: r.Height, BlockHash: b.Checkpoint.BlockHash, TxIndex: r.TxIndex})
		}
	}
	future, err := a.browseTXFutureLocations(ctx, txid, hash, before)
	if err != nil {
		return nil, err
	}
	for _, location := range future {
		key := fmt.Sprintf("%d:%s:%d", location.Height, location.BlockHash, location.TxIndex)
		duplicate := false
		for _, prior := range matches {
			if key == fmt.Sprintf("%d:%s:%d", prior.Height, prior.BlockHash, prior.TxIndex) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			matches = append(matches, location)
		}
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
			if hash == "" {
				return nil, errIndexLookupIncomplete
			}
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
		var checkpoint indexCheckpoint
		var b indexBatch
		if id == "tx-locator" {
			checkpoint, err = browseStatusTXCheckpoint(s, commit)
		} else {
			b, err = s.readBatch(commit)
			checkpoint = b.Checkpoint
		}
		if err != nil {
			return out, err
		}
		if checkpoint.Height != n || checkpoint.From != n || checkpoint.PreviousCommitment != "" {
			return out, fmt.Errorf("invalid browsed coverage receipt")
		}
		if id == "inscriptions" && !inscriptionBatchInventoryComplete(b) {
			out.Complete = false
			out.Note = "Known inscription occurrences without a complete block-inventory recipe do not establish block coverage."
			continue
		}
		if h.matches(n, checkpoint.BlockHash) {
			out.Ranges = append(out.Ranges, heightInterval{n, n})
		}
	}
	out.Ranges = mergeIntervals(out.Ranges)
	return out, nil
}

// Browse coverage rechecks membership, selected headers, and the exact bytes of
// every batch on every inspection. Only a fully validated identical TX payload
// can reuse its checkpoint summary. It is never used for queries or writes.
const browseStatusCacheEntries = 4096

type browseStatusCacheKey struct {
	dir, commitment string
	digest          [32]byte
}
type browseStatusCacheEntry struct {
	key        browseStatusCacheKey
	checkpoint indexCheckpoint
}

var browseStatusBatchCache = struct {
	sync.Mutex
	items map[browseStatusCacheKey]*list.Element
	order list.List
}{items: make(map[browseStatusCacheKey]*list.Element)}

func browseStatusTXCheckpoint(s *indexStore, commit string) (indexCheckpoint, error) {
	data, err := s.readBatchBytes(commit)
	if err != nil {
		return indexCheckpoint{}, err
	}
	key := browseStatusCacheKey{filepath.Clean(s.dir), commit, sha256.Sum256(data)}
	c := &browseStatusBatchCache
	c.Lock()
	if e := c.items[key]; e != nil {
		c.order.MoveToFront(e)
		checkpoint := e.Value.(browseStatusCacheEntry).checkpoint
		c.Unlock()
		return checkpoint, nil
	}
	c.Unlock()
	batch, err := s.validateBatchBytes(commit, data)
	if err != nil {
		return indexCheckpoint{}, err
	}
	c.Lock()
	if e := c.items[key]; e != nil {
		c.order.MoveToFront(e)
	} else {
		if c.order.Len() >= browseStatusCacheEntries {
			oldest := c.order.Back()
			delete(c.items, oldest.Value.(browseStatusCacheEntry).key)
			c.order.Remove(oldest)
		}
		c.items[key] = c.order.PushFront(browseStatusCacheEntry{key, batch.Checkpoint})
	}
	c.Unlock()
	return batch.Checkpoint, nil
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
				Inscriptions      []string `json:"inscriptions"`
				Height            int64    `json:"block_height"`
				TransactionCount  *uint32  `json:"transaction_count,omitempty"`
				Mode              string   `json:"mode"`
				InventoryComplete bool     `json:"inventory_complete"`
			}{coordinates, height, inscriptionBatchCount(b), inscriptionBatchMode(b), inscriptionBatchInventoryComplete(b)})
			total += len(coordinates)
		}
	}
	return rows, total, inspected, true, nil
}
