package main

// Every complete verified body enters this fanout. It selects existing consent
// only; obtaining a body never arms a track or starts a preceding source scan.
import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

type resolvedIngestionKey struct{}

var errResolvedAlreadyIndexed = errors.New("resolved block already indexed")
var errResolvedNotApplicable = errors.New("resolved block is not the next applicable transition")

func prepareResolvedBlockExtraction(ctx context.Context, root string, b *blockView) {
	if b.inscriptionExtraction != nil || ctx.Err() != nil {
		return
	}
	d, err := readIndexRunDocument(root)
	if err != nil || !d.Configured {
		return
	}
	for _, c := range d.Prepared.Consumers {
		if c.Armed && (c.Index == "inscriptions" || c.Index == "sat-state" || bitmapConsumerID(c.Index)) && resolvedAdmissionAllowed(d, c.Index, b.Height) == nil {
			if x, err := extractInscriptionOccurrences(*b); err == nil {
				b.inscriptionExtraction = &x
			}
			return
		}
	}
}

type resolvedIngestionQueue struct {
	drainMu           sync.Mutex
	mu                sync.Mutex
	blocks            []resolvedQueuedBlock
	seen              map[string]bool
	results           []indexBrowseResult
	fanoutErrors      []indexRunSourceFactError
	problem           error
	closed            bool
	spoolDir          string
	maxResidentBlocks int
	maxResidentBytes  int64
	residentBlocks    int
	residentBytes     int64
	maxQueuedBytes    int64
	queuedBytes       int64
}

// Dependency fetches made while deriving a consumer enqueue their completed
// bodies. Drain after that consumer releases its locks, avoiding recursive
// Bitmap/TXIO/address writers without dropping internally obtained blocks.
func (a *app) withDeferredBlockIngestion(ctx context.Context) (context.Context, func()) {
	if _, ok := ctx.Value(resolvedIngestionKey{}).(*resolvedIngestionQueue); ok {
		return ctx, func() {}
	}
	q := &resolvedIngestionQueue{seen: map[string]bool{}}
	work := context.WithValue(ctx, resolvedIngestionKey{}, q)
	return work, func() {
		a.drainResolvedBlockIngestion(work, q)
		if err := q.close(); err != nil {
			q.fail(err)
			q.addResults(indexBrowseResult{"resolved", "unavailable", err.Error()})
		}
	}
}
func (a *app) ingestResolvedVerifiedBlock(ctx context.Context, block blockView) []indexBrowseResult {
	if ctx.Err() != nil || !integrityVerified(block) || block.Height < 0 || len(block.Transactions) == 0 || block.TransactionCount != uint64(len(block.Transactions)) || (!block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated) {
		return nil
	}
	// Speculation and source loading never publish before the coordinator
	// has built the exact per-body owner plan. Dependency bodies do not match.
	if deferred, _ := ctx.Value(indexRunCoordinatorSourceKey{}).(bool); deferred {
		if owner, ok := ctx.Value(indexRunSourceOwnershipKey{}).(indexRunSourceOwnership); ok && owner.Height == block.Height && owner.Hash == block.Hash {
			return nil
		}
	}
	if q, ok := ctx.Value(resolvedIngestionKey{}).(*resolvedIngestionQueue); ok {
		if err := queueResolvedBlock(q, block); err != nil {
			return []indexBrowseResult{{"resolved", "unavailable", err.Error()}}
		}
		return nil
	}
	q := &resolvedIngestionQueue{seen: map[string]bool{}}
	if err := queueResolvedBlock(q, block); err != nil {
		_ = q.close()
		return []indexBrowseResult{{"resolved", "unavailable", err.Error()}}
	}
	a.drainResolvedBlockIngestion(context.WithValue(ctx, resolvedIngestionKey{}, q), q)
	if err := q.close(); err != nil {
		q.addResults(indexBrowseResult{"resolved", "unavailable", err.Error()})
	}
	return q.results
}
func (a *app) drainResolvedBlockIngestion(ctx context.Context, q *resolvedIngestionQueue) {
	q.drainMu.Lock()
	defer q.drainMu.Unlock()
	for ctx.Err() == nil {
		block, have, err := q.next()
		if err != nil {
			q.fail(err)
			q.addResults(indexBrowseResult{"resolved", "unavailable", err.Error()})
			break
		}
		if !have {
			break
		}
		rows := a.ingestArmedResolvedBlock(ctx, block)
		q.addResults(rows...)
		q.mu.Lock()
		q.fanoutErrors = append(q.fanoutErrors, sourceFactFailures(block.Height, block.Hash, rows)...)
		if len(q.fanoutErrors) > 32 {
			q.fanoutErrors = append([]indexRunSourceFactError(nil), q.fanoutErrors[len(q.fanoutErrors)-32:]...)
		}
		q.mu.Unlock()
	}

}

func resolvedSelectionEnabled(c indexRunSelection, id string) bool {
	switch id {
	case "txio-values":
		return c.Index == "txo-spender" && c.RetainOutputValues != nil && *c.RetainOutputValues
	case "txio-scripts":
		return c.Index == "txo-spender" && c.RetainOutputScripts != nil && *c.RetainOutputScripts
	}
	return c.Armed
}
func resolvedSelectionOwner(id string) string {
	if id == "txio-values" || id == "txio-scripts" {
		return "txo-spender"
	}
	return id
}
func resolvedAdmissionAllowed(d indexRunDocument, id string, height int64) error {
	if bitmapParcelExecutionDeferred(resolvedSelectionOwner(id)) {
		return errBitmapParcelsDeferred
	}
	if d.Run == nil {
		return nil
	}
	switch d.Run.State {
	case "running", "waiting", "pausing", "paused":
	default:
		return nil
	}
	if p, ok := indexRunProgressFor(d.Run, resolvedSelectionOwner(id)); ok {
		if p.AdmissionState == "blocked" || p.AdmissionState == "off" {
			return errIndexRunAdmissionChanged
		}
		if p.EffectiveFrom != nil && height < *p.EffectiveFrom {
			return errIndexRunAdmissionChanged
		}
	}
	return nil
}

// The scheduled body has one durable owner per admitted run member. This
// excludes duplicate sparse publication only for that exact body; unrelated
// dependency bodies and armed tracks outside the run retain normal fanout.
func resolvedOwnedByCoordinator(ctx context.Context, d indexRunDocument, id string, block blockView) bool {
	owner, ok := ctx.Value(indexRunSourceOwnershipKey{}).(indexRunSourceOwnership)
	if !ok || owner.RunID == "" || owner.Height != block.Height || owner.Hash != block.Hash || d.Run == nil || d.Run.ID != owner.RunID || (d.Run.State != "running" && d.Run.State != "waiting") {
		return false
	}
	selection, ok := runSelection(d.Run.Request, resolvedSelectionOwner(id))
	if !ok || !resolvedSelectionEnabled(selection, id) {
		return false
	}
	prepared, ok := runSelection(d.Prepared, resolvedSelectionOwner(id))
	if !ok || indexDigest(selection) != indexDigest(prepared) {
		return false
	}
	p, ok := indexRunProgressFor(d.Run, resolvedSelectionOwner(id))
	if !ok || (p.AdmissionState != "" && p.AdmissionState != "admitted" && p.AdmissionState != "pending") || p.State == "failed" || p.State == "rebuild_required" {
		return false
	}
	if p.EffectiveFrom != nil && block.Height < *p.EffectiveFrom {
		return false
	}
	if block.Height < p.From || (d.Run.Request.Out != nil || !selection.Live) && block.Height > p.To {
		return false
	}
	if owners, ok := ctx.Value(indexRunCohortOwnersKey{}).(map[string]bool); ok {
		return owners[resolvedSelectionOwner(id)]
	}
	// Older callers have no explicit plan. Reserve only their exact next
	// transition, never every future body inside the admitted range.
	next := max(p.From, p.Height+1)
	if p.StagedHeight != nil {
		next = *p.StagedHeight + 1
	}
	return block.Height == next
}
func resolvedSelectionContext(ctx context.Context, root, id string, captured indexRunSelection, hashAt func(int64) (string, error), block blockView) context.Context {
	return context.WithValue(ctx, indexRunPublicationKey{}, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Capture the incoming context, rather than querying the context being
		// constructed: the outer caller's cancellation/publication fence stays
		// effective without recursively invoking this new per-track guard.
		if err := checkIndexRunPublication(ctx); err != nil {
			return err
		}
		d, err := readIndexRunDocument(root)
		if err != nil {
			return err
		}
		current, ok := runSelection(d.Prepared, resolvedSelectionOwner(id))
		if !d.Configured || !ok || !resolvedSelectionEnabled(current, id) {
			return errIndexRunConsumerStopped
		}
		if err := resolvedAdmissionAllowed(d, id, block.Height); err != nil {
			return err
		}
		if indexDigest(current) != indexDigest(captured) {
			return errIndexRunAdmissionChanged
		}
		selected, err := hashAt(block.Height)
		if err != nil {
			return err
		}
		if selected != block.Hash {
			return fmt.Errorf("selected resolved block changed before publication")
		}
		return nil
	})
}
func (a *app) ingestArmedResolvedBlock(ctx context.Context, block blockView) []indexBrowseResult {
	d, err := readIndexRunDocument(a.dataDir)
	if err != nil {
		return []indexBrowseResult{{Index: "resolved", State: "error", Error: err.Error()}}
	}
	if !d.Configured {
		return nil
	}
	hashAt := a.addressSelectedHashes(ctx, a.currentChainAuthority())
	selected, err := hashAt(block.Height)
	if err != nil || selected != block.Hash {
		return []indexBrowseResult{{Index: "resolved", State: "unavailable", Error: "selected source anchor unavailable"}}
	}
	choices := map[string]indexRunSelection{}
	for _, c := range d.Prepared.Consumers {
		choices[c.Index] = c
	}
	native, _ := ctx.Value(indexRunBlockConsumersKey{}).(map[string]indexRunBlockConsumer)
	prepared := runPrepared(ctx)
	if prepared == nil || prepared.block.Height != block.Height || prepared.block.Hash != block.Hash {
		native = nil
	}
	if prepared == nil || prepared.block.Hash != block.Hash {
		prepared = &indexRunPrepared{block: block, extraction: block.inscriptionExtraction, refs: map[string]map[string]outputFactRef{}, prevouts: map[string]utxoCoin{}}
	}
	// A queued dependency preserves verified facts, but its new prepared view
	// has no producer acknowledgement. Re-establish real Raw membership through
	// the normal idempotent producer before either lane captures that scalar.
	// A verification receipt alone does not prove durable retention.
	if !prepared.rawProduced && timelineRawBlocksOn(a.dataDir) {
		prepared.rawProduced, err = a.rawProducerReceiptContext(ctx, block)
		if err != nil {
			return []indexBrowseResult{{Index: "resolved", State: "unavailable", Error: err.Error()}}
		}
	}
	prepareSharedBlockInventory(prepared, d)
	ctx = context.WithValue(ctx, indexRunPreparedKey{}, prepared)

	out := []indexBrowseResult{}
	var resultsMu sync.Mutex
	appendResult := func(row indexBrowseResult) { resultsMu.Lock(); out = append(out, row); resultsMu.Unlock() }
	apply := func(id string, fn func(context.Context, indexRunSelection) error) {
		if !releaseIndexExecutionAvailable(resolvedSelectionOwner(id)) {
			return
		}
		// The actual cohort owns exactly this body. An unrelated admitted future
		// interval is not ownership and still receives a committed point fact.
		if operation := native[id]; operation != nil {
			err := operation(ctx)
			if errors.Is(err, errIndexRunConsumerStopped) || errors.Is(err, errIndexRunAdmissionChanged) || errors.Is(err, context.Canceled) {
				appendResult(indexBrowseResult{id, "off", err.Error()})
			} else if err != nil {
				appendResult(indexBrowseResult{id, "unavailable", err.Error()})
			} else {
				appendResult(indexBrowseResult{Index: id, State: "indexed"})
			}
			return
		}
		c, ok := choices[resolvedSelectionOwner(id)]
		if !ok || !resolvedSelectionEnabled(c, id) {
			return
		}
		if resolvedOwnedByCoordinator(ctx, d, id, block) {
			return
		}
		work := resolvedSelectionContext(ctx, a.dataDir, id, c, hashAt, block)
		if err := checkIndexRunPublication(work); err != nil {
			state := "error"
			if errors.Is(err, errIndexRunConsumerStopped) || errors.Is(err, errIndexRunAdmissionChanged) {
				state = "off"
			}
			appendResult(indexBrowseResult{id, state, err.Error()})
			return
		}
		if prepared.extractionErr != nil && resolvedNeedsInventory(id) {
			appendResult(indexBrowseResult{id, "unavailable", prepared.extractionErr.Error()})
			return
		}
		err := fn(work, c)
		if errors.Is(err, errBrowseTXPointStaged) {
			appendResult(indexBrowseResult{Index: id, State: "staged"})
		} else if errors.Is(err, errResolvedNotApplicable) {
			appendResult(indexBrowseResult{id, "not_applicable", err.Error()})
		} else if errors.Is(err, errIndexRunConsumerStopped) || errors.Is(err, errIndexRunAdmissionChanged) || errors.Is(err, context.Canceled) {
			appendResult(indexBrowseResult{id, "off", err.Error()})
		} else if errors.Is(err, errResolvedAlreadyIndexed) {
			appendResult(indexBrowseResult{Index: id, State: "already_indexed"})
		} else if err != nil {
			appendResult(indexBrowseResult{id, "unavailable", err.Error()})
		} else {
			appendResult(indexBrowseResult{Index: id, State: "indexed"})
		}
	}
	sparse := func(id string) {
		apply(id, func(work context.Context, c indexRunSelection) error {
			if err := lockIndexBrowse(work); err != nil {
				return err
			}
			defer indexBrowseMu.Unlock()
			return a.ingestSparseBatch(work, c, block)
		})
	}
	// A single TX owner overlaps the existing ordered stateful group. Every
	// worker joins before returning, releasing gates or draining dependencies.
	txDone := make(chan struct{})
	go func() { defer close(txDone); sparse("tx-locator") }()
	if native["blocks"] != nil {
		apply("blocks", nil)
	}
	apply("utxo-state", func(work context.Context, c indexRunSelection) error {
		s, err := openUTXOStoreContext(work, a.dataDir)
		if err != nil {
			return err
		}
		next := int64(0)
		if h := s.Head(); h != nil {
			if h.Height == block.Height && h.BlockHash == block.Hash {
				return nil
			}
			next = h.Height + 1
			if block.Height == next && block.PreviousBlockHash != h.BlockHash {
				return fmt.Errorf("UTXO awaits its exact next selected transition")
			}
		}
		if block.Height != next {
			return fmt.Errorf("%w: UTXO requires next block %d", errResolvedNotApplicable, next)
		}
		return s.ApplyBlock(work, block)
	})

	sparse("txo-spender")
	if native["txo-spender"] == nil {
		for _, id := range []string{"txio-values", "txio-scripts"} {
			id := id
			apply(id, func(work context.Context, _ indexRunSelection) error {
				if err := lockIndexBrowse(work); err != nil {
					return err
				}
				defer indexBrowseMu.Unlock()
				return a.ingestSparseOutputLayer(work, id, block)
			})
		}
	}
	sparse("inscriptions")
	for _, id := range []string{bitmapStandardDistrictID, bitmapStandardParcelID} {
		id := id
		apply(id, func(work context.Context, _ indexRunSelection) error {
			work = context.WithValue(work, bitmapIngestOnlyKey{}, id)
			return a.ingestArmedBitmapBlock(work, block, prepared.extraction)
		})
	}
	apply("sat-state", func(work context.Context, c indexRunSelection) error {
		s, err := indexStoreHead(a.dataDir, "sat-state")
		if err != nil {
			return err
		}
		next := int64(0)
		if s.checkpoint != nil {
			if s.checkpoint.Height == block.Height && s.checkpoint.BlockHash == block.Hash {
				return nil
			}
			next = s.checkpoint.Height + 1
			if block.Height == next && block.PreviousBlockHash != s.checkpoint.BlockHash {
				return fmt.Errorf("Sats awaits its exact next selected transition")
			}
		}
		if block.Height != next {
			return fmt.Errorf("%w: Sats requires next block %d", errResolvedNotApplicable, next)
		}
		s.locatorContext = work
		s.head.Mode = "lean"
		s.retainSatHistory = c.RetainSatHistory
		return appendSatSelectedBlock077(work, s, block, 0, "ephemeral", prepared.extraction, hashAt)
	})
	apply("address-history", func(work context.Context, c indexRunSelection) error {
		s, err := openAddressHistoryStoreContext(work, a.dataDir)
		if err != nil {
			return err
		}
		var saved addressHistoryBlock
		if have, err := s.tree("blocks", s.head.BlocksRoot, work).get(addressHistoryHeightKey(block.Height), &saved); err != nil {
			return err
		} else if have && saved.BlockHash == block.Hash && saved.DestinationVersion == 1 {
			return errResolvedAlreadyIndexed
		}
		return s.CommitBlockContext(work, block, hashAt)
	})
	apply("address-state", func(work context.Context, c indexRunSelection) error {
		u, err := utxoStoreHead(a.dataDir)
		if err != nil {
			return err
		}
		h := u.Head()
		if h == nil || h.Height != block.Height || h.BlockHash != block.Hash {
			return fmt.Errorf("%w: Current Addresses requires matching complete UTXO context", errResolvedNotApplicable)
		}
		prior, err := readAddressStateContext(work, a.dataDir)
		if err != nil {
			if block.Height == 0 {
				_, err = buildAddressStateContext(work, a.dataDir, nil)
				return err
			}
			return fmt.Errorf("Current Addresses requires its prior complete projection: %v", err)
		}
		if prior.Height == block.Height && prior.BlockHash == block.Hash {
			return nil
		}
		if prior.Height+1 != block.Height {
			return fmt.Errorf("%w: Current Addresses requires its exact next projection", errResolvedNotApplicable)
		}
		return resumeAddressStateContext(work, a.dataDir)
	})

	<-txDone
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}

func resolvedNeedsInventory(id string) bool {
	return id == "inscriptions" || id == "sat-state" || bitmapConsumerID(id)
}
func prepareSharedBlockInventory(p *indexRunPrepared, d indexRunDocument) {
	if p.extractionAttempted {
		return
	}
	needed := false
	for _, c := range d.Prepared.Consumers {
		if c.Armed && resolvedNeedsInventory(c.Index) && resolvedAdmissionAllowed(d, c.Index, p.block.Height) == nil {
			needed = true
			break
		}
	}
	if !needed {
		return
	}
	p.extractionAttempted = true
	if p.extraction == nil {
		x, err := extractInscriptionOccurrences(p.block)
		p.extractionErr = err
		if err == nil {
			p.extraction = &x
		}
	}
	if p.extraction != nil {
		p.extractionErr = validateCompleteInscriptionInventory(p.block, p.extraction)
		if p.extractionErr == nil {
			// A later dependency/cache offer carries the same frozen complete
			// inventory, rather than parsing the already consumed body again.
			p.block.inscriptionExtraction = p.extraction
		}
	}
}

func (a *app) ingestSparseBatch(ctx context.Context, c indexRunSelection, block blockView) error {
	if c.Index == "tx-locator" {
		if point, ok := ctx.Value(indexRunPointSessionKey{}).(*indexRunPointSession); ok {
			ctx = point.context(ctx)
		}
	}
	if c.Index != "txo-spender" {
		if complete, err := a.completeIndexedBrowseBlock(ctx, c.Index, block); err != nil {
			return err
		} else if complete {
			if c.Index == "tx-locator" || c.Mode != "full" {
				return errResolvedAlreadyIndexed
			}
			if prior, have, err := a.committedInscriptionBatch(ctx, block.Height, block.Hash); err != nil {
				return err
			} else if have && inscriptionBatchMode(prior) == "full" {
				return errResolvedAlreadyIndexed
			}
		}
		var x *inscriptionBlockResult
		if p := runPrepared(ctx); p != nil {
			x = p.extraction
		}
		// Keep native IDs in the armed shared Transaction Index when it owns
		// this block. Only an independently requested inscription-ID option
		// needs a reveal-scoped projection when that owner is Off.
		if c.Index == "inscriptions" {
			if on, _ := timelineTrackArmed(a.dataDir, "tx-locator"); on {
				c.ConventionalIDs = boolPointer(false)
			}
		}
		b, err := singletonBrowseBatchMode(c, block, x)
		if err != nil {
			return err
		}
		if prior, have, e := a.browsedBatchContext(ctx, c.Index, block.Height, block.Hash); e != nil {
			return e
		} else if have && c.Index == "inscriptions" {
			if inscriptionBatchMode(prior) == "full" && inscriptionBatchMode(b) != "full" {
				return nil
			}
			if prior.InscriptionBlock != nil && len(prior.InscriptionBlock.ConventionalIDs) > 0 && len(b.InscriptionBlock.ConventionalIDs) == 0 {
				b.InscriptionBlock.ConventionalIDs = prior.InscriptionBlock.ConventionalIDs
				b.Checkpoint.RecordsHash = batchRecordsHash(b)
				b.Checkpoint.Commitment = checkpointHash(b.Checkpoint)
			}
		}
		added, err := a.persistBrowseBatch(ctx, c.Index, b)
		if c.Index == "tx-locator" && errors.Is(err, errBrowseTXPointInvalidated) {
			if point, ok := ctx.Value(indexRunPointSessionKey{}).(*indexRunPointSession); ok {
				point.reset()
			}
		}
		if err == nil && !added {
			return errResolvedAlreadyIndexed
		}
		return err
	}
	s, _, err := a.browseStore(c.Index)
	if err != nil {
		return err
	}
	s.locatorContext = ctx
	// Read known positional creation catalogues only. Missing translations
	// remain pending; sparse ingestion never fetches a prerequisite block.
	s.spenderOrigin = func(work context.Context, in inputView, h int64, tx int) (txioCreation, error) {
		return a.sparseTXIOCreation(work, in, h, tx)
	}
	p, err := s.deriveTXIOBlock(block)
	if err != nil {
		return err
	}
	d := s.definition
	b := indexBatch{PreviousBlockHash: block.PreviousBlockHash, Spenders: p}
	b.Checkpoint = indexCheckpoint{Schema: d.CheckpointSchema, Definition: d.ID, Version: d.Version, RuleHash: d.RuleHash, Network: d.Network, From: block.Height, Height: block.Height, BlockHash: block.Hash, RecordsHash: batchRecordsHash(b), DependencyFingerprint: indexDigest(p.CreationAnchors)}
	b.Checkpoint.Commitment = checkpointHash(b.Checkpoint)
	added, err := a.persistBrowseBatch(ctx, c.Index, b)
	if err == nil && !added {
		return errResolvedAlreadyIndexed
	}
	return err
}

func (a *app) sparseTXIOCreation(ctx context.Context, in inputView, before int64, txIndex int) (txioCreation, error) {
	canonical, err := indexStoreHead(a.dataDir, "txo-spender")
	if err == nil && canonical.checkpoint != nil {
		found, err := canonical.ownTXIOCreation(ctx, in, before)
		if err != nil {
			return found, err
		}
		if found.Position != "" {
			return found, nil
		}
	}
	s, head, err := a.browseStore("txo-spender")
	if err != nil {
		return txioCreation{}, err
	}
	heights := []int64{}
	for key := range head.Blocks {
		var h int64
		if _, err := fmt.Sscan(key, &h); err == nil && h <= before {
			heights = append(heights, h)
		}
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] > heights[j] })
	for n, h := range heights {
		if n >= indexExplorerMaxBatches {
			break
		}
		b, have, err := a.browsedBatchContext(ctx, "txo-spender", h, "")
		if err != nil {
			return txioCreation{}, err
		}
		if !have || b.Spenders == nil {
			continue
		}
		p, err := s.readTXIOTranslation(ctx, b.Spenders.LocatorRef)
		if err != nil {
			return txioCreation{}, err
		}
		for _, t := range p.Transactions {
			if t.TxID == in.PrevTxID && (h < before || t.TxIndex < txIndex) && transactionFitsHeightContext(h, t.Coinbase, before) {
				if in.PrevVout >= t.Outputs {
					return txioCreation{}, fmt.Errorf("previous output absent from known transaction incarnation")
				}
				return txioCreation{outputCoordinate(int(in.PrevVout), t.TxIndex, h), b.Checkpoint.BlockHash}, nil
			}
		}
	}
	return txioCreation{}, nil
}
