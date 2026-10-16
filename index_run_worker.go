package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Development protocols implement this adapter without borrowing another
// index's coverage or introducing another source-fetching loop.
type indexRunExternalConsumer interface {
	NextHeight() int64
	Reconcile(context.Context, func(int64) (string, error)) error
	Apply(context.Context, blockView, *inscriptionBlockResult) error
	Checkpoint() *indexCheckpoint
	Close()
}
type indexRunBlockSource interface {
	SetBlockSource(func(context.Context, int64) (blockView, error))
}
type indexRunOutcomeConsumer interface {
	ApplyRetainedOutcome(context.Context, int64) error
}
type indexRunRetentionConsumer interface{ ConfigureRetention(string) }
type indexRunSourceFunc func(context.Context, blockTarget, string) (blockView, error)
type indexRunConsumer struct {
	selection  indexRunSelection
	progress   indexRunProgress
	store      *indexStore
	utxo       *utxoStore
	history    *addressHistoryStore
	external   indexRunExternalConsumer
	enrich     bool
	retryAfter time.Time
	reconciled bool
	authority  chainAuthorityView
}
type indexRunPreparedKey struct{}
type indexRunPublicationKey struct{}
type indexRunAddressProjectionKey struct{}

var errIndexRunConsumerStopped = errors.New("consumer is Off in the prepared timeline")
var errIndexRunAdmissionChanged = errors.New("the track's active admission changed before publication")

func checkIndexRunPublication(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if guard, ok := ctx.Value(indexRunPublicationKey{}).(func() error); ok {
		return guard()
	}
	return ctx.Err()
}

func checkIndexConsumerPublication(ctx context.Context, root, index string) error {
	if err := checkIndexRunPublication(ctx); err != nil {
		return err
	}
	if !indexRunOptionalWorkAllowed(root, index) {
		return errIndexRunConsumerStopped
	}
	return nil
}

func (a *app) indexRunConsumerContext(ctx context.Context, index string, admitted ...indexRunProgress) context.Context {
	return context.WithValue(ctx, indexRunPublicationKey{}, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		d, err := readIndexRunDocument(a.dataDir)
		if err != nil {
			return err
		}
		if !d.Configured {
			live, err := readIndexLiveState(a.dataDir)
			if err != nil {
				return err
			}
			if live.Policies[index].Stopped {
				return errIndexRunConsumerStopped
			}
			return nil
		}
		selection, ok := runSelection(d.Prepared, index)
		if !ok || !selection.Armed {
			return errIndexRunConsumerStopped
		}
		if d.Run != nil {
			if d.Run.State != "running" && d.Run.State != "waiting" {
				return errIndexRunConsumerStopped
			}
			selection, ok = runSelection(d.Run.Request, index)
			if !ok || !selection.Armed {
				return errIndexRunConsumerStopped
			}
			if len(admitted) > 0 {
				current, exists := indexRunProgressFor(d.Run, index)
				if !exists || !sameIndexRunAdmission(current, admitted[0]) || current.AdmissionState == "blocked" {
					return errIndexRunAdmissionChanged
				}
			}
		}
		return nil
	})
}

type indexRunPrepared struct {
	cacheBytes int
	block      blockView
	extraction *inscriptionBlockResult
	refs       map[string]map[string]outputFactRef
	prevouts   map[string]utxoCoin
}

func runPrepared(ctx context.Context) *indexRunPrepared {
	p, _ := ctx.Value(indexRunPreparedKey{}).(*indexRunPrepared)
	return p
}
func runRememberPrevout(ctx context.Context, tx, vin int, coin utxoCoin) {
	if p := runPrepared(ctx); p != nil {
		p.prevouts[fmt.Sprintf("%d:%d", tx, vin)] = coin
	}
}
func runPrevout(ctx context.Context, tx, vin int) (utxoCoin, bool) {
	if p := runPrepared(ctx); p != nil {
		c, ok := p.prevouts[fmt.Sprintf("%d:%d", tx, vin)]
		return c, ok
	}
	return utxoCoin{}, false
}

func runConsumerOrder(id string) int {
	if bitmapConsumerID(id) {
		switch bitmapConsumerKind(id) {
		case "district":
			return 5
		case "parcel":
			return 6
		case "chain":
			return 7
		}
	}
	switch id {
	case "blocks":
		return 0
	case "utxo-state":
		return 1
	case "tx-locator":
		return 2
	case "txo-spender":
		return 3
	case "inscriptions":
		return 4
	case "bitmap-districts":
		return 5
	case "bitmap-parcels":
		return 6
	case "bitmap-chain":
		return 7
	case "sat-state":
		return 8
	case "address-history":
		return 9
	case "address-state":
		return 10
	}
	return 20
}
func (a *app) openRunConsumer(ctx context.Context, r *indexRunRecord, p indexRunProgress) (*indexRunConsumer, error) {
	c, _ := runSelection(r.Request, p.Index)
	v := &indexRunConsumer{selection: c, progress: p}
	var err error
	if bitmapConsumerID(p.Index) {
		if err = checkIndexRunPublication(ctx); err != nil {
			return v, err
		}
	}
	if c.Rebuild {
		switch c.Index {
		case "utxo-state":
			err = resetUTXOForRebuild(ctx, a.dataDir, r.ID+":"+c.Index)
		case "sat-state":
			err = resetSatStateForRebuildContext(ctx, a.dataDir, r.ID+":"+c.Index)
		}
	}
	if err != nil {
		return nil, err
	}
	switch {
	case bitmapConsumerID(c.Index):
		v.external, err = openBitmapRunConsumer(ctx, a, c.Index)
		if configured, ok := v.external.(indexRunRetentionConsumer); ok {
			configured.ConfigureRetention(c.Retention)
		}
	case c.Index == "utxo-state":
		v.utxo, err = openUTXOStoreContext(ctx, a.dataDir)
	case c.Index == "address-history":
		v.history, err = openAddressHistoryStoreContext(ctx, a.dataDir)
	case c.Index == "address-state": // Its source is acquired only after the armed UTXO consumer commits.
	default:
		v.store, err = openIndexStoreContext(ctx, a.dataDir, c.Index)
		if err == nil {
			v.store.head.Mode = c.Mode
			v.store.retainSatHistory = c.RetainSatHistory
			v.store.retainOutputValues = c.RetainOutputValues != nil && *c.RetainOutputValues
			v.store.retainOutputScripts = c.RetainOutputScripts != nil && *c.RetainOutputScripts
			a.configureTXIOStore(v.store)
		}
	}
	return v, err
}
func (c *indexRunConsumer) checkpoint() *indexCheckpoint {
	if c.external != nil {
		return c.external.Checkpoint()
	}
	if c.store != nil {
		return c.store.checkpoint
	}
	if c.utxo != nil && c.utxo.Head() != nil {
		d, _ := findIndexDefinition("utxo-state")
		return utxoIndexCheckpoint(c.utxo.Head(), d)
	}
	if c.history != nil {
		d, _ := findIndexDefinition("address-history")
		return addressHistoryIndexCheckpoint(c.history.head, d)
	}
	return nil
}
func (c *indexRunConsumer) next(target int64) int64 {
	c.enrich = false
	if c.external != nil {
		return c.external.NextHeight()
	}
	if c.history != nil {
		gaps := indexCoverageGaps(c.history.head.P2PKCoverage, c.progress.From, target)
		if len(gaps) > 0 {
			return gaps[0].From
		}
		return target + 1
	}
	if c.selection.Index == "address-state" {
		return -1
	}
	if c.store != nil && c.selection.Index == "txo-spender" && c.store.checkpoint != nil {
		gaps := c.store.txioEnrichmentRanges(c.progress.From, min(target, c.store.checkpoint.Height), c.store.retainOutputValues, c.store.retainOutputScripts)
		if len(gaps) > 0 {
			c.enrich = true
			return gaps[0].From
		}
	}
	if cp := c.checkpoint(); cp != nil {
		return cp.Height + 1
	}
	return c.progress.From
}

func sameIndexRunAdmission(a, b indexRunProgress) bool {
	if a.EffectiveFrom == nil || b.EffectiveFrom == nil {
		return a.EffectiveFrom == nil && b.EffectiveFrom == nil
	}
	return *a.EffectiveFrom == *b.EffectiveFrom
}
func (a *app) reconcileRunConsumer(ctx context.Context, c *indexRunConsumer, authority chainAuthorityView) error {
	if err := checkIndexRunPublication(ctx); err != nil {
		return err
	}
	if c.reconciled && c.authority.Source == authority.Source && c.authority.Height == authority.Height && c.authority.Hash == authority.Hash {
		return nil
	}
	err := a.reconcileRunConsumerUncached(ctx, c, authority)
	if err == nil {
		c.reconciled, c.authority = true, authority
	}
	return err
}
func (a *app) reconcileRunConsumerUncached(ctx context.Context, c *indexRunConsumer, authority chainAuthorityView) error {
	hashAt := a.addressSelectedHashes(ctx, authority)
	if c.external != nil {
		return c.external.Reconcile(ctx, hashAt)
	}
	if c.utxo != nil {
		err := c.utxo.ReconcileSelected(ctx, authority.Height, func(ctx context.Context, h int64) (string, error) { return hashAt(h) })
		if optionalAddressProjectionError(err) {
			return nil
		}
		return err
	}
	if c.history != nil {
		return c.history.ReconcileContext(ctx, hashAt)
	}
	if c.store != nil {
		c.store.locatorContext = ctx
		if c.selection.Index == "sat-state" {
			return c.store.reconcileSatStateSelected(ctx, authority.Height, hashAt)
		}
		return c.store.reconcile(hashAt)
	}
	return nil
}
func (a *app) applyRunConsumer(ctx context.Context, c *indexRunConsumer, prepared *indexRunPrepared, authority chainAuthorityView, retention string) error {
	b := prepared.block
	hashAt := a.addressSelectedHashes(ctx, authority)
	if c.external != nil {
		return c.external.Apply(ctx, b, prepared.extraction)
	}
	if c.utxo != nil {
		err := c.utxo.ApplyBlock(ctx, b)
		if optionalAddressProjectionError(err) {
			return nil
		}
		return err
	}
	if c.history != nil {
		return c.history.CommitBlockContext(ctx, b, hashAt)
	}
	s := c.store
	s.locatorContext = ctx
	if c.enrich {
		if err := s.enrichTXIOBlockContext(ctx, b, s.retainOutputValues, s.retainOutputScripts); err != nil {
			return err
		}
		return s.recordTXIOEnrichmentCoverage(b.Height, b.Hash, s.retainOutputValues, s.retainOutputScripts)
	}
	if c.selection.Index == "sat-state" {
		return appendSatSelectedBlock077(ctx, s, b, 0, retention, prepared.extraction, hashAt)
	}
	from := c.progress.From
	if s.checkpoint != nil {
		from = s.checkpoint.From
	}
	if err := s.appendPreparedBlock(b, from, retention, prepared.extraction); err != nil {
		return err
	}
	if c.selection.Index == "inscriptions" && inscriptionIDsEnabled(c.selection.ConventionalIDs) {
		related, err := openIndexStoreContext(ctx, a.dataDir, inscriptionLocatorIndex)
		if err != nil {
			return err
		}
		source, ok, err := a.committedInscriptionBatch(ctx, b.Height, b.Hash)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("the inscription result did not commit")
		}
		return related.appendInscriptionLocators(source, &b, from, retention)
	}
	return nil
}
func runTerminal(err error) bool {
	return satTerminalError077(err) || errors.Is(err, ErrUTXORebuildRequired) || errors.Is(err, ErrUTXOStateCorrupt) || errors.Is(err, ErrAddressStateCorrupt)
}
func (a *app) saveRunProgress(id, state, reason string, consumers []*indexRunConsumer, counts indexRunCounts, target int64, hash string) error {
	a.indexRunMu.Lock()
	defer a.indexRunMu.Unlock()
	r := a.indexRunState.Run
	if r == nil || r.ID != id {
		return fmt.Errorf("run was replaced")
	}
	before := *r
	before.Updated = ""
	beforeDigest := indexDigest(before)
	if r.State != "pausing" && r.State != "cancelled" {
		r.State, r.Reason = state, reason
	}
	r.Counts = counts
	r.TargetHeight, r.TargetHash = target, hash
	// A click may have durably admitted a new member while this cohort was
	// applying. Progress must never erase that pending intent or an Off fence.
	saved := append([]indexRunProgress(nil), r.Consumers...)
	r.Consumers = nil
	seen := map[string]bool{}
	for _, c := range consumers {
		p := c.progress
		for _, pending := range saved {
			if pending.Index == p.Index && (!sameIndexRunAdmission(pending, p) || pending.AdmissionState == "blocked" && p.AdmissionState != "blocked") {
				p = pending
			}
		}
		if cp := c.checkpoint(); cp != nil {
			p.Height, p.BlockHash, p.Commitment = cp.Height, cp.BlockHash, cp.Commitment
		}
		if selection, ok := runSelection(r.Request, p.Index); !ok || !selection.Armed {
			p.State, p.AdmissionState, p.Reason = "off", "off", "Off; committed records are retained."
		}
		r.Consumers = append(r.Consumers, p)
		seen[p.Index] = true
	}
	for _, p := range saved {
		if !seen[p.Index] {
			r.Consumers = append(r.Consumers, p)
		}
	}
	after := *r
	after.Updated = ""
	if indexDigest(after) == beforeDigest {
		return nil
	}
	r.Updated = time.Now().UTC().Format(time.RFC3339)
	return a.persistIndexRunLocked()
}

func (a *app) runCoordinatedIndexes(ctx context.Context, id string) {
	defer func() { a.indexMu.Lock(); a.indexRunActiveID = ""; a.indexMu.Unlock(); a.indexBuildFinished() }()
	d := a.indexRunSnapshot()
	if d.Run == nil || d.Run.ID != id {
		return
	}
	record := d.Run
	consumers := []*indexRunConsumer{}
	counts := record.Counts
	for _, p := range record.Consumers {
		if p.AdmissionState == "blocked" {
			selection, _ := runSelection(record.Request, p.Index)
			consumers = append(consumers, &indexRunConsumer{selection: selection, progress: p})
			continue
		}
		c, err := a.openRunConsumer(a.indexRunBoundConsumerContext(ctx, p), record, p)
		if c == nil {
			s, _ := runSelection(record.Request, p.Index)
			c = &indexRunConsumer{selection: s, progress: p}
		}
		if err != nil {
			c.progress.State = "failed"
			if runTerminal(err) {
				c.progress.State = "rebuild_required"
			}
			c.progress.Reason = err.Error()
		}
		consumers = append(consumers, c)
	}
	// Small stable consumer ordering establishes same-height dependency order.
	for i := 1; i < len(consumers); i++ {
		for j := i; j > 0 && runConsumerOrder(consumers[j].selection.Index) < runConsumerOrder(consumers[j-1].selection.Index); j-- {
			consumers[j], consumers[j-1] = consumers[j-1], consumers[j]
		}
	}
	defer func() {
		for _, c := range consumers {
			if c.external != nil {
				c.external.Close()
			}
		}
	}()
	target, targetHash := record.TargetHeight, record.TargetHash
	finish := func(state, reason string) {
		if state == "paused" {
			memberState := "paused"
			if current := a.indexRunSnapshot().Run; current != nil && current.ID == id && current.State == "cancelled" {
				memberState = "cancelled"
			}
			for _, c := range consumers {
				if c.progress.State == "running" || c.progress.State == "waiting" || c.progress.State == "ready" || c.progress.State == "pending" {
					c.progress.State, c.progress.Reason = memberState, reason
				}
			}
		}
		if state == "complete" && ctx.Err() == nil {
			for _, c := range consumers {
				var err error
				if c.utxo != nil {
					err = c.utxo.CollectPages(ctx)
				}
				if c.store != nil && c.selection.Index == "sat-state" {
					err = c.store.collectOrdinalState()
				}
				if err != nil && !errors.Is(err, errOrdinalMaintenancePending) {
					reason = "Committed state retained; reclamation is deferred: " + err.Error()
				}
			}
			if err := collectSharedOutputFactsContext(ctx, a.dataDir); err != nil {
				reason = "Committed state retained; shared reclamation is deferred: " + err.Error()
			}
		}
		if err := a.saveRunProgress(id, state, reason, consumers, counts, target, targetHash); err != nil {
			a.indexRunMu.Lock()
			if r := a.indexRunState.Run; r != nil && r.ID == id {
				r.State = "failed"
				r.Reason = "Run status could not be saved: " + err.Error() + ". Published index checkpoints remain authoritative."
			}
			a.indexRunMu.Unlock()
			return
		}
		a.indexRunMu.Lock()
		if r := a.indexRunState.Run; r != nil && r.ID == id && r.State == "pausing" {
			r.State = "paused"
			_ = a.persistIndexRunLocked()
		}
		a.indexRunMu.Unlock()
	}
	cache := map[string]*indexRunPrepared{}
	order := []string{}
	cacheBytes := 0
	source := a.indexSourceBlock
	if a.indexRunSourceBlock != nil {
		source = a.indexRunSourceBlock
	}
	prefetch := newIndexRunPrefetch(ctx, func(callCtx context.Context, target blockTarget) (blockView, error) {
		return source(callCtx, target, "ephemeral")
	})
	defer prefetch.Close()
	peerWindow := &indexRunDownloadWindow{a: a, ctx: ctx}
	defer peerWindow.Reset()
	admissionRevision := effectiveIndexRunAdmissionRevision(record)
	var load func(context.Context, int64) (*indexRunPrepared, error)
	load = func(callCtx context.Context, h int64) (*indexRunPrepared, error) {
		authority := a.currentChainAuthority()
		t, err := a.utxoSelectedTarget(callCtx, authority, h)
		if err != nil {
			return nil, err
		}
		if p := cache[t.HashDisplay]; p != nil {
			counts.CacheHits++
			return p, nil
		}
		// Shared acquisition may retain sparse verified Raw body membership
		// while global Raw is On. Only the Raw consumer's Apply advances its
		// sequential checkpoint; this compatibility policy cannot override On.
		retention := "ephemeral"
		started := time.Now()
		b, elapsed, queued, err := prefetch.Take(callCtx, t)
		if !queued {
			// A different frontier won the fair scheduling turn. Do not leave
			// an obsolete request occupying the only speculative source slot.
			prefetch.Reset()
			b, err = source(callCtx, t, retention)
			elapsed = time.Since(started)
		}
		counts.SourceMilliseconds += elapsed.Milliseconds()
		if err != nil {
			return nil, err
		}
		counts.SourceReads++
		counts.DecodedBlocks++
		if b.Height != h || b.Hash != t.HashDisplay || !integrityVerified(b) {
			return nil, fmt.Errorf("source block does not match the selected target")
		}
		p := &indexRunPrepared{block: b, refs: map[string]map[string]outputFactRef{}, prevouts: map[string]utxoCoin{}}
		// Bound the cache by decoded JSON as well as entry count. An oversized
		// block still serves its ready consumers without being retained here.
		encoded, _ := json.Marshal(b)
		size := len(encoded) + len(b.verifiedRaw)
		p.cacheBytes = size
		if size <= 32<<20 {
			for len(order) > 0 && (len(order) >= 8 || cacheBytes+size > 32<<20) {
				old := order[0]
				order = order[1:]
				cacheBytes -= cache[old].cacheBytes
				delete(cache, old)
			}
			cache[b.Hash] = p
			order = append(order, b.Hash)
			cacheBytes += size
		}
		return p, nil
	}
	for _, c := range consumers {
		if source, ok := c.external.(indexRunBlockSource); ok {
			source.SetBlockSource(func(callCtx context.Context, h int64) (blockView, error) {
				p, err := load(callCtx, h)
				if err != nil {
					return blockView{}, err
				}
				return p.block, nil
			})
		}
	}
	last := 0
	processed := int64(0)
	for {
		if ctx.Err() != nil {
			finish("paused", "Paused; resume continues from each committed checkpoint.")
			return
		}
		d = a.indexRunSnapshot()
		if d.Run == nil || d.Run.ID != id {
			return
		}
		record = d.Run
		if revision := effectiveIndexRunAdmissionRevision(record); revision != admissionRevision {
			prefetch.Reset()
			peerWindow.Reset()
			admissionRevision = revision
		}
		if record.State == "cancelled" || record.State == "pausing" {
			finish("paused", record.Reason)
			return
		}
		// Admit new members only between complete cohorts. A later Off/Stop is
		// still enforced by each consumer's final publication guard.
		for _, p := range record.Consumers {
			var existing *indexRunConsumer
			var position int
			for j, c := range consumers {
				if c.selection.Index == p.Index {
					existing, position = c, j
					break
				}
			}
			if existing != nil && sameIndexRunAdmission(existing.progress, p) && p.AdmissionState != "pending" && p.AdmissionState != "blocked" {
				continue
			}
			if existing != nil && p.AdmissionState == "blocked" {
				existing.progress = p
				continue
			}
			if existing != nil && sameIndexRunAdmission(existing.progress, p) && existing.progress.AdmissionState == "admitted" && p.AdmissionState == "pending" {
				continue
			}
			selection, _ := runSelection(record.Request, p.Index)
			if existing != nil && existing.external != nil {
				existing.external.Close()
			}
			c := &indexRunConsumer{selection: selection, progress: p}
			if p.AdmissionState != "blocked" && selection.Armed {
				opened, err := a.openRunConsumer(a.indexRunBoundConsumerContext(ctx, p), record, p)
				if opened != nil {
					c = opened
				}
				if err != nil {
					c.progress.State, c.progress.Reason = "failed", err.Error()
				} else {
					c.progress.AdmissionState = "admitted"
					c.progress.State = "ready"
				}
			}
			if source, ok := c.external.(indexRunBlockSource); ok {
				source.SetBlockSource(func(callCtx context.Context, h int64) (blockView, error) {
					p, err := load(callCtx, h)
					if err != nil {
						return blockView{}, err
					}
					return p.block, nil
				})
			}
			if existing != nil {
				consumers[position] = c
			} else {
				consumers = append(consumers, c)
			}
		}
		for i := 1; i < len(consumers); i++ {
			for j := i; j > 0 && runConsumerOrder(consumers[j].selection.Index) < runConsumerOrder(consumers[j-1].selection.Index); j-- {
				consumers[j], consumers[j-1] = consumers[j-1], consumers[j]
			}
		}
		authority := a.currentChainAuthority()
		hashAt := a.addressSelectedHashes(ctx, authority)
		anyLive := false
		for _, s := range record.Request.Consumers {
			anyLive = anyLive || s.Armed && s.Live
		}
		if record.Request.Out == nil && anyLive && authority.Height >= 0 {
			target, targetHash = authority.Height, authority.Hash
		}
		nexts := map[int]int64{}
		eligible := []int{}
		failed, waiting := false, false
		for i, c := range consumers {
			consumerTarget := record.InitialTargetHeight
			selection, _ := runSelection(record.Request, c.selection.Index)
			prepared, on := runSelection(d.Prepared, c.selection.Index)
			if !on || !prepared.Armed || !selection.Armed {
				c.progress.State = "off"
				c.progress.Reason = "Off; committed records are retained."
				continue
			}
			c.selection.Live = selection.Live
			if selection.Live && record.Request.Out == nil {
				consumerTarget = target
			}
			if c.progress.AdmissionState == "blocked" {
				c.progress.State = "blocked"
				waiting = true
				continue
			}
			if c.progress.State == "failed" || c.progress.State == "rebuild_required" {
				failed = true
				continue
			}
			dependencyFailed := false
			for _, dependency := range c.progress.Dependencies {
				if dependency.Kind != "state" || dependency.Index == c.selection.Index {
					continue
				}
				for _, upstream := range consumers {
					if upstream.selection.Index == dependency.Index && !bitmapConsumerID(c.selection.Index) && (upstream.progress.State == "failed" || upstream.progress.State == "rebuild_required" || upstream.progress.State == "off") {
						dependencyFailed = true
					}
				}
			}
			if dependencyFailed {
				c.progress.State = "failed"
				c.progress.Reason = "The explicitly selected prerequisite is stopped or failed; its saved data was preserved."
				failed = true
				continue
			}
			if time.Now().Before(c.retryAfter) {
				waiting = true
				continue
			}
			consumerCtx := a.indexRunBoundConsumerContext(ctx, c.progress)
			if bitmapConsumerID(c.selection.Index) {
				if err := checkIndexRunPublication(consumerCtx); err != nil {
					c.progress.State = "failed"
					c.progress.Reason = err.Error()
					failed = true
					continue
				}
			}
			reconcileStarted := time.Now()
			reconcileNeeded := !c.reconciled || c.authority.Source != authority.Source || c.authority.Height != authority.Height || c.authority.Hash != authority.Hash
			err := a.reconcileRunConsumer(consumerCtx, c, authority)
			if reconcileNeeded {
				counts.ReconcileMilliseconds += time.Since(reconcileStarted).Milliseconds()
			}
			if err != nil {
				c.progress.State = "waiting"
				if errors.Is(err, errIndexRunConsumerStopped) {
					c.progress.State = "off"
				}
				if errors.Is(err, errIndexRunAdmissionChanged) {
					c.progress.State = "waiting"
				}
				if runTerminal(err) {
					c.progress.State = "rebuild_required"
					failed = true
				}
				c.progress.Reason = err.Error()
				waiting = true
				continue
			}
			if c.selection.Index == "address-state" {
				u, err := utxoStoreHead(a.dataDir)
				if err != nil || u.Head() == nil {
					c.progress.State = "waiting"
					c.progress.Reason = "Waiting for an explicitly armed UTXO source."
					waiting = true
					continue
				}
				if source, ok := runSelection(record.Request, "utxo-state"); ok && source.Armed && u.Head().Height < consumerTarget {
					c.progress.State = "waiting"
					c.progress.Reason = "Waiting for the UTXO consumer to commit the selected target."
					waiting = true
					continue
				}
				if u.Head().Height > consumerTarget {
					if c.progress.State == "complete" {
						continue
					}
					c.progress.State = "failed"
					c.progress.Reason = "The UTXO source advanced beyond Out; no historical state was fabricated."
					failed = true
					continue
				}
				status, e := addressStateStatusContext(ctx, a.dataDir)
				if e == nil && status.State == "ready" {
					c.progress.State = "complete"
					c.progress.Height = u.Head().Height
					c.progress.Commitment = status.Checkpoint.Commitment
					c.progress.BlockHash = status.Checkpoint.BlockHash
					continue
				}
				projectionCtx := context.WithValue(consumerCtx, indexRunPublicationKey{}, func() error {
					if err := checkIndexRunPublication(consumerCtx); err != nil {
						return err
					}
					current, e := a.utxoSelectedTarget(ctx, a.currentChainAuthority(), u.Head().Height)
					if e != nil {
						return e
					}
					if current.HashDisplay != u.Head().BlockHash {
						return satPublicationAnchorError077{fmt.Errorf("selected UTXO source changed before address publication")}
					}
					return nil
				})
				cp, e := buildAddressStateContext(projectionCtx, a.dataDir, nil)
				if e != nil {
					c.progress.State = "failed"
					c.progress.Reason = e.Error()
					if errors.Is(e, errIndexRunConsumerStopped) {
						c.progress.State = "off"
					} else if isSelectedAuthorityRetry(e) {
						c.progress.State = "waiting"
						c.retryAfter = time.Now().Add(time.Second)
						waiting = true
					} else if ctx.Err() != nil {
						c.progress.State = "paused"
					} else {
						failed = true
					}
				} else {
					c.progress.State = "complete"
					c.progress.Height = cp.Height
					c.progress.Commitment = cp.Commitment
					c.progress.BlockHash = cp.BlockHash
				}
				continue
			}
			h := c.next(consumerTarget)
			c.progress.NextRequiredHeight = &h
			if c.progress.EffectiveFrom != nil && h < *c.progress.EffectiveFrom {
				c.progress.State, c.progress.AdmissionState = "blocked", "blocked"
				c.progress.RequiresLeadIn = true
				c.progress.Reason = "This saved state needs an earlier prefix than the active join boundary. Stop this run to explicitly build that prefix."
				waiting = true
				continue
			}
			c.progress.To = consumerTarget
			if h > consumerTarget {
				c.progress.State = "complete"
				c.progress.Reason = "Committed coverage satisfies this run target."
				continue
			}
			if record.Request.In != nil && h < *record.Request.In && !c.enrich {
				c.progress.State = "failed"
				c.progress.Reason = "Recovery requires a prefix outside In. Review the expanded global window; no hidden replay started."
				failed = true
				continue
			}
			c.progress.State = "running"
			c.progress.Reason = ""
			nexts[i] = h
			eligible = append(eligible, i)
		}
		if len(eligible) == 0 {
			if !waiting {
				_ = a.setIndexRunFrontier(id, target+1, false)
			}
			if failed && !waiting {
				finish("complete_with_errors", "Some consumers could not complete; successful checkpoints are retained.")
				return
			}
			if !waiting && !anyLive {
				finish("complete", "")
				return
			}
			if record.Request.Out != nil && !waiting {
				finish("complete", "Out reached. Saved Live preferences remain capped by the global window.")
				return
			}
			if err := a.saveRunProgress(id, "waiting", "Waiting for selected-chain evidence or an armed prerequisite.", consumers, counts, target, targetHash); err != nil {
				finish("failed", err.Error())
				return
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
			continue
		}
		// Rotate the next selected frontier; matching consumers share one pass.
		chosen := eligible[0]
		for _, i := range eligible {
			if i >= last {
				chosen = i
				break
			}
		}
		last = (chosen + 1) % max(1, len(consumers))
		h := nexts[chosen]
		if err := a.setIndexRunFrontier(id, h, true); err != nil {
			finish("paused", "The current stop intent was preserved.")
			return
		}
		// A Chain over retained completed outcomes has no Bitcoin-body input.
		// Do not route it through load(), even when the body is unavailable.
		rawRequired := false
		for _, i := range eligible {
			if nexts[i] == h && (!bitmapConsumerID(consumers[i].selection.Index) || bitmapConsumerKind(consumers[i].selection.Index) != "chain") {
				rawRequired = true
			}
		}
		if !rawRequired {
			peerWindow.Reset()
			for _, i := range eligible {
				if nexts[i] != h {
					continue
				}
				c := consumers[i]
				consumer, ok := c.external.(indexRunOutcomeConsumer)
				if !ok {
					c.progress.State = "failed"
					c.progress.Reason = "Outcome-only consumer adapter unavailable"
					continue
				}
				expected, e := hashAt(h)
				if e != nil {
					c.progress.State = "waiting"
					c.progress.Reason = e.Error()
					c.retryAfter = time.Now().Add(time.Second)
					continue
				}
				consumerCtx := a.indexRunBoundConsumerContext(ctx, c.progress)
				publishCtx := context.WithValue(consumerCtx, indexRunPublicationKey{}, func() error {
					if err := checkIndexRunPublication(consumerCtx); err != nil {
						return err
					}
					current, e := a.utxoSelectedTarget(ctx, a.currentChainAuthority(), h)
					if e != nil {
						return e
					}
					if current.HashDisplay != expected {
						return satPublicationAnchorError077{fmt.Errorf("selected outcome block changed before publication")}
					}
					return nil
				})
				counts.OutcomeReads++
				started := time.Now()
				err := consumer.ApplyRetainedOutcome(publishCtx, h)
				counts.ApplyMilliseconds += time.Since(started).Milliseconds()
				if err != nil {
					c.progress.State = "failed"
					if errors.Is(err, ErrBitmapEvidenceRequired) {
						c.progress.State = "waiting"
						c.retryAfter = time.Now().Add(time.Second)
					}
					if isSelectedAuthorityRetry(err) {
						c.progress.State = "waiting"
						c.retryAfter = time.Now().Add(time.Second)
					}
					if errors.Is(err, errIndexRunConsumerStopped) {
						c.progress.State = "off"
					}
					if errors.Is(err, errIndexRunAdmissionChanged) {
						c.progress.State = "waiting"
					}
					var changed satPublicationAnchorError077
					if errors.As(err, &changed) {
						c.progress.State = "waiting"
						c.retryAfter = time.Now().Add(time.Second)
					}
					c.progress.Reason = err.Error()
				} else {
					counts.ChainSteps++
					c.progress.AddedBlocks++
					c.progress.Height = h
					next := h + 1
					c.progress.NextRequiredHeight = &next
					c.progress.Reason = ""
				}
			}
			_ = a.setIndexRunFrontier(id, h+1, false)
			if err := a.saveRunProgress(id, "running", "", consumers, counts, target, targetHash); err != nil {
				finish("failed", err.Error())
				return
			}
			continue
		}
		// Prime the missing-body window before awaiting its first block. A
		// stalling first peer must not leave all other transports idle.
		initialRanges := []indexRunDownloadRange{}
		for _, i := range eligible {
			c := consumers[i]
			if bitmapConsumerID(c.selection.Index) && bitmapConsumerKind(c.selection.Index) == "chain" {
				continue
			}
			cap := record.InitialTargetHeight
			if c.selection.Live && record.Request.Out == nil {
				cap = target
			}
			if nexts[i] <= cap {
				initialRanges = append(initialRanges, indexRunDownloadRange{nexts[i], cap})
			}
		}
		peerWindow.Update(a.currentChainAuthority(), initialRanges)
		p, err := load(ctx, h)
		if err != nil {
			if ctx.Err() != nil {
				finish("paused", "Paused; committed records are retained.")
				return
			}
			var unavailable blockUnavailableError
			retry := errors.As(err, &unavailable) || isSelectedAuthorityRetry(err)
			for _, i := range eligible {
				if nexts[i] == h {
					consumers[i].progress.State = "waiting"
					if !retry {
						consumers[i].progress.State = "failed"
					}
					consumers[i].progress.Reason = err.Error()
				}
			}
			if e := a.saveRunProgress(id, "waiting", err.Error(), consumers, counts, target, targetHash); e != nil {
				finish("failed", e.Error())
				return
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
			continue
		}
		// Acquire at most one upcoming raw block while this cohort derives and
		// commits. The scheduling cap belongs to each admitted member; outcome-
		// only Chains never cause speculative Bitcoin reads.
		upcoming := int64(-1)
		peerRanges := []indexRunDownloadRange{}
		for _, i := range eligible {
			c := consumers[i]
			if bitmapConsumerID(c.selection.Index) && bitmapConsumerKind(c.selection.Index) == "chain" {
				continue
			}
			candidate := nexts[i]
			if candidate == h {
				candidate++
			}
			cap := record.InitialTargetHeight
			if c.selection.Live && record.Request.Out == nil {
				cap = target
			}
			if candidate <= cap && (upcoming < 0 || candidate < upcoming) {
				upcoming = candidate
			}
			if candidate <= cap {
				peerRanges = append(peerRanges, indexRunDownloadRange{candidate, cap})
			}
		}
		peerWindow.Update(a.currentChainAuthority(), peerRanges)
		if upcoming >= 0 {
			if candidate, e := a.utxoSelectedTarget(ctx, a.currentChainAuthority(), upcoming); e == nil && cache[candidate.HashDisplay] == nil {
				prefetch.Schedule(candidate)
			}
		}
		needsExtraction := false
		for _, i := range eligible {
			if nexts[i] == h && (consumers[i].selection.Index == "inscriptions" || consumers[i].selection.Index == "sat-state" || bitmapConsumerID(consumers[i].selection.Index) && bitmapConsumerKind(consumers[i].selection.Index) != "chain") {
				needsExtraction = true
			}
		}
		if needsExtraction && p.extraction == nil {
			started := time.Now()
			x, e := extractInscriptionOccurrences(p.block)
			counts.ExtractionMilliseconds += time.Since(started).Milliseconds()
			counts.InscriptionExtractions++
			if e != nil {
				for _, i := range eligible {
					if nexts[i] == h && consumers[i].selection.Index != "utxo-state" {
						consumers[i].progress.State = "failed"
						consumers[i].progress.Reason = e.Error()
					}
				}
			} else {
				p.extraction = &x
			}
		}
		blockCtx := context.WithValue(ctx, indexRunPreparedKey{}, p)
		for _, i := range eligible {
			if nexts[i] != h {
				continue
			}
			c := consumers[i]
			if c.progress.State == "failed" {
				continue
			}
			latest := a.indexRunSnapshot()
			active, ok := runSelection(latest.Prepared, c.selection.Index)
			if !ok || !active.Armed || ctx.Err() != nil {
				continue
			}
			selected, e := hashAt(h)
			if e != nil || selected != p.block.Hash {
				prefetch.Reset()
				cache = map[string]*indexRunPrepared{}
				order = nil
				cacheBytes = 0
				c.progress.State = "waiting"
				c.progress.Reason = "Selected branch changed before publication."
				continue
			}
			consumerCtx := a.indexRunBoundConsumerContext(blockCtx, c.progress)
			publishCtx := context.WithValue(consumerCtx, indexRunPublicationKey{}, func() error {
				if err := checkIndexRunPublication(consumerCtx); err != nil {
					return err
				}
				current, e := a.utxoSelectedTarget(ctx, a.currentChainAuthority(), h)
				if e != nil {
					return e
				}
				if current.HashDisplay != p.block.Hash {
					return satPublicationAnchorError077{fmt.Errorf("selected block changed before consumer publication")}
				}
				return nil
			})
			started := time.Now()
			if outcome, ok := c.external.(indexRunOutcomeConsumer); ok && bitmapConsumerKind(c.selection.Index) == "chain" {
				counts.OutcomeReads++
				err = outcome.ApplyRetainedOutcome(publishCtx, h)
				if err == nil {
					counts.ChainSteps++
				}
			} else {
				err = a.applyRunConsumer(publishCtx, c, p, authority, runRawRetention(record.Request))
			}
			counts.ApplyMilliseconds += time.Since(started).Milliseconds()
			if err != nil {
				c.progress.State = "failed"
				if errors.Is(err, errIndexRunConsumerStopped) {
					c.progress.State = "off"
				}
				if errors.Is(err, errIndexRunAdmissionChanged) {
					c.progress.State = "waiting"
				}
				var changed satPublicationAnchorError077
				if errors.As(err, &changed) {
					prefetch.Reset()
					c.progress.State = "waiting"
					c.retryAfter = time.Now().Add(time.Second)
					cache = map[string]*indexRunPrepared{}
					order = nil
					cacheBytes = 0
				}
				if errors.Is(err, ErrBitmapEvidenceRequired) {
					c.progress.State = "waiting"
					c.retryAfter = time.Now().Add(time.Second)
				}
				if isSelectedAuthorityRetry(err) {
					prefetch.Reset()
					c.progress.State = "waiting"
					c.retryAfter = time.Now().Add(time.Second)
					cache = map[string]*indexRunPrepared{}
					order = nil
					cacheBytes = 0
				}
				if runTerminal(err) {
					c.progress.State = "rebuild_required"
				}
				c.progress.Reason = err.Error()
			} else {
				c.progress.AddedBlocks++
				c.progress.Height = h
				next := h + 1
				c.progress.NextRequiredHeight = &next
			}
		}
		_ = a.setIndexRunFrontier(id, h+1, false)
		started := time.Now()
		if e := a.saveRunProgress(id, "running", "", consumers, counts, target, targetHash); e != nil {
			finish("failed", e.Error())
			return
		}
		counts.ProgressMilliseconds += time.Since(started).Milliseconds()
		processed++
		if processed%128 == 0 {
			sharedDue := processed%utxoSharedMaintenanceBlocks == 0
			for _, c := range consumers {
				if c.selection.Index == "sat-state" && c.store != nil {
					_ = c.store.collectOrdinalState()
				}
				if c.utxo != nil {
					_ = c.utxo.CollectPages(ctx)
					sharedDue = sharedDue || utxoSharedMaintenanceDue(c.utxo)
				}
			}
			// Live runs may never finish. Apply the existing shared-fact
			// maintenance cadence at a committed boundary rather than retaining
			// every expired creation page until a future manual pause/restart.
			if sharedDue && ctx.Err() == nil {
				if err := collectSharedOutputFactsContext(ctx, a.dataDir); err == nil {
					for _, c := range consumers {
						if c.utxo != nil {
							_ = recordUTXOSharedMaintenance(c.utxo)
						}
					}
				}
			}
		}
	}
}
