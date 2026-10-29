package main

import (
	"context"
	"errors"
)

type indexRunPointSessionKey struct{}

// This operation-local controller outlives individual body contexts. Only its
// clean run context, session-bounded serialized facts and small consent key are
// retained. It is read/changed by the single TX lane or its joined coordinator.
type indexRunPointSession struct {
	a       *app
	global  context.Context
	runID   string
	intent  string
	session *browseTXPointSession
}

func newIndexRunPointSession(a *app, global context.Context, runID string, doc indexRunDocument) *indexRunPointSession {
	p := &indexRunPointSession{a: a, global: global, runID: runID}
	p.refresh(doc)
	return p
}
func (p *indexRunPointSession) reset() {
	if p.session != nil {
		p.session.Abort()
	}
	p.session = newBrowseTXPointSession(p.a, p.global, p.runID)
}
func (p *indexRunPointSession) refresh(doc indexRunDocument) {
	selection, have := runSelection(doc.Prepared, "tx-locator")
	selection.Live = false // scheduling changes do not reinterpret pending facts
	var boundary *int64
	blocked := false
	if doc.Run != nil {
		if progress, ok := indexRunProgressFor(doc.Run, "tx-locator"); ok {
			boundary = progress.EffectiveFrom
			blocked = progress.AdmissionState == "off" || progress.AdmissionState == "blocked"
		}
	}
	raw, on := runSelection(doc.Prepared, "blocks")
	intent := indexDigest(struct {
		Have         bool
		Selection    indexRunSelection
		Boundary     *int64
		Blocked, Raw bool
	}{have, selection, boundary, blocked, on && raw.Armed})
	if p.session == nil || intent != p.intent {
		p.reset()
		p.intent = intent
	}
}
func (p *indexRunPointSession) context(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, indexRunPointSessionKey{}, p)
	return context.WithValue(ctx, indexBrowseTXPointSessionKey{}, p.session)
}
func (p *indexRunPointSession) lookup(ctx context.Context, height int64, hash string) (indexBatch, bool, error) {
	batch, have, err := p.session.Lookup(ctx, height, hash)
	if err != nil {
		if !errors.Is(err, errBrowseTXPointInvalidated) {
			return batch, false, err
		}
		// A legal consent/Raw/branch change invalidates only volatile facts.
		// The ordinary selected source remains available for the current step.
		p.reset()
	} else if have {
		return batch, true, nil
	}
	return p.a.browsedBatchContext(ctx, "tx-locator", height, hash)
}
func (a *app) runTransactionPoint(ctx context.Context, height int64, hash string) (indexBatch, bool, error) {
	if p, ok := ctx.Value(indexRunPointSessionKey{}).(*indexRunPointSession); ok {
		return p.lookup(ctx, height, hash)
	}
	return a.browsedBatchContext(ctx, "tx-locator", height, hash)
}

// Called only after a successful native Flush (including an automatic flush
// observed after successful Apply). The acknowledgement has its own visibility
// boundary: failure must not turn a valid native checkpoint into a failed Apply.
func (a *app) noteRunLocatorPublication(ctx context.Context, runID string, c *indexRunConsumer, explicitFlush bool) {
	if c.store == nil || c.selection.Index != "tx-locator" {
		return
	}
	stats := c.store.LocatorWriteStats()
	if !explicitFlush && stats.BatchFlushes <= c.pointAckFlushes {
		return
	}
	c.pointAckFlushes = stats.BatchFlushes
	cp := c.store.DurableCheckpoint()
	if cp == nil || cp.Commitment == c.pointAckCommit {
		return
	}
	if err := a.acknowledgeBrowseTXPointsNative(ctx, c.store); err != nil {
		a.recordRunSourceFactErrors(runID, cp.Height, cp.BlockHash, []indexBrowseResult{{Index: "tx-locator", State: "unavailable", Error: "Native TX checkpoint is retained; future-point reverse-lookup acknowledgement is deferred: " + err.Error()}})
		return
	}
	c.pointAckCommit = cp.Commitment
}
