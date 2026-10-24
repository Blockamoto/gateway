package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"

	"./internal/updateapply"
)

type indexDeleteGenericRange struct {
	Store      *indexStore
	Head       indexHead
	Checkpoint *indexCheckpoint
	Removed    []indexBatch
}
type indexDeleteBrowseRange struct {
	Store   *indexStore
	Head    indexBrowseHead
	Removed []string
}

func (a *app) planIndexDeleteBrowse(ctx context.Context, act *indexDeleteAction, requested *indexDeleteRange) error {
	if !indexBrowseAvailable(act.Item.Index) {
		return nil
	}
	s, head, e := a.browseStore(act.Item.Index)
	if e != nil {
		return e
	}
	first, last := int64(-1), int64(-1)
	for raw := range head.Blocks {
		h, e := strconv.ParseInt(raw, 10, 64)
		if e != nil {
			return e
		}
		if first < 0 || h < first {
			first = h
		}
		if h > last {
			last = h
		}
	}
	r := indexDeleteEffectiveRange(requested, first, last)
	if r == nil {
		return nil
	}
	b := &indexDeleteBrowseRange{Store: s, Head: head}
	for raw, commit := range head.Blocks {
		h, _ := strconv.ParseInt(raw, 10, 64)
		if h >= r.From && h <= r.To {
			b.Removed = append(b.Removed, commit)
			delete(b.Head.Blocks, raw)
		}
	}
	if len(b.Removed) > 0 {
		act.Browse = b
		act.Item.Noop = false
		act.Item.Scope = append(act.Item.Scope, fmt.Sprintf("%d independently saved browse blocks in [%d, %d]", len(b.Removed), r.From, r.To))
		if act.Item.Range == nil {
			act.Item.Range = r
		}
	}
	return ctx.Err()
}
func (a *app) applyIndexDeleteBrowse(ctx context.Context, b *indexDeleteBrowseRange, backup func(string) error, move func(string) error) error {
	head := filepath.Join(b.Store.dir, "head.json")
	if e := backup(head); e != nil {
		return e
	}
	if e := indexDeleteWriteJSON(head, b.Head); e != nil {
		return e
	}
	for _, commit := range b.Removed {
		if e := move(filepath.Join(b.Store.dir, "commits", commit+".json")); e != nil {
			return e
		}
	}
	return ctx.Err()
}

func (a *app) planIndexDeletionRange(ctx context.Context, act *indexDeleteAction, requested *indexDeleteRange) error {
	id := act.Item.Index
	if id == "txio-values" || id == "txio-scripts" {
		act.Item.Blockers = append(act.Item.Blockers, "Range deletion of this independently owned fact layer requires a separate sparse-manifest rewrite; select whole layer data to clear it. Positional links are preserved.")
		return nil
	}
	act.Item.Operation = "delete_range"
	act.Item.Scope = nil
	act.Item.Note = "The track definition stays. Only the explicitly displayed interval may be removed."
	if id == "address-history" {
		s, e := openAddressHistoryStoreContext(ctx, a.dataDir)
		if e != nil {
			return e
		}
		first, last := int64(-1), int64(-1)
		if len(s.head.Coverage) > 0 {
			first = s.head.Coverage[0].From
			last = s.head.Coverage[len(s.head.Coverage)-1].To
		}
		r := indexDeleteEffectiveRange(requested, first, last)
		act.Item.Range = r
		if r == nil || !indexDeleteIntersects(r, first, last) {
			act.Item.Noop = true
			act.Item.Note = "No saved historical address blocks intersect this scope."
			return nil
		}
		e = s.tree("blocks", s.head.BlocksRoot, ctx).walk(func(kv ordinalKV) error {
			var b addressHistoryBlock
			if e := json.Unmarshal(kv.Value, &b); e != nil {
				return e
			}
			if kv.Key != addressHistoryHeightKey(b.Height) {
				return fmt.Errorf("invalid historical block key")
			}
			if b.Height >= r.From && b.Height <= r.To {
				act.HistoryRemoved = append(act.HistoryRemoved, b)
			}
			return ctx.Err()
		})
		if e != nil {
			return e
		}
		act.History = s
		act.Item.Noop = len(act.HistoryRemoved) == 0
		act.Item.Scope = []string{fmt.Sprintf("Active historical address output rows from %d scanned blocks in [%d, %d], their totals and exact coverage", len(act.HistoryRemoved), r.From, r.To)}
		act.Item.Retained = append(act.Item.Retained, "Immutable prior generations and shared output facts remain separately owned; active queries exclude the deleted interval.")
		return nil
	}
	if id == "tx-locator" || id == "txo-spender" || id == "inscriptions" {
		s, e := indexStoreHead(a.dataDir, id)
		if e != nil {
			return e
		}
		if s.checkpoint == nil {
			act.Item.Noop = true
			act.Item.Note = "No saved index records intersect this scope."
			return nil
		}
		r := indexDeleteEffectiveRange(requested, s.checkpoint.From, s.checkpoint.Height)
		act.Item.Range = r
		if r == nil || !indexDeleteIntersects(r, s.checkpoint.From, s.checkpoint.Height) {
			act.Item.Noop = true
			act.Item.Note = "No saved index records intersect this scope."
			return nil
		}
		if r.To < s.checkpoint.Height {
			act.Item.Blockers = append(act.Item.Blockers, "This format has a continuous authenticated prefix. A middle/prefix deletion would invalidate later records; select an exact suffix through the saved end or delete the whole built-in index. No later data will be removed implicitly.")
			return nil
		}
		g := &indexDeleteGenericRange{Store: s, Head: cloneTXIOLayerCoverage(s.head)}
		g.Head.Commitment = ""
		next := s.head.Commitment
		for next != "" {
			if len(g.Removed) >= 8192 {
				return fmt.Errorf("suffix review exceeds 8192 block records; select a smaller suffix")
			}
			b, e := s.readBatch(next)
			if e != nil {
				return e
			}
			if b.Checkpoint.Height < r.From {
				g.Checkpoint = &b.Checkpoint
				g.Head.Commitment = b.Checkpoint.Commitment
				break
			}
			g.Removed = append(g.Removed, b)
			next = b.Checkpoint.PreviousCommitment
			if e = ctx.Err(); e != nil {
				return e
			}
		}
		end := int64(-1)
		if g.Checkpoint != nil {
			end = g.Checkpoint.Height
		}
		if id == "txo-spender" {
			for layer, ranges := range g.Head.TXIOLayers {
				var kept []heightInterval
				for _, v := range ranges {
					if v.From <= end {
						if v.To > end {
							v.To = end
						}
						kept = append(kept, v)
					}
				}
				g.Head.TXIOLayers[layer] = kept
			}
			g.Head.TXIOLayersHash = txioLayerCoverageHash(g.Head)
		}
		act.Generic = g
		act.Item.Operation = "rewind"
		act.Item.Noop = len(g.Removed) == 0
		act.Item.Scope = []string{fmt.Sprintf("%d active suffix records inside [%d, %d]; restore the exact retained predecessor at block %d", len(g.Removed), r.From, r.To, end)}
		act.Item.Retained = append(act.Item.Retained, "Earlier authenticated records, shared facts and unrelated browse/source caches are preserved.")
		if id == "inscriptions" {
			act.Item.Blockers = append(act.Item.Blockers, "Inscription suffix removal also needs its exact related-ID locator and browse closure. Until that closure is reviewed together, use whole-index deletion; no related records are silently erased.")
		}
		return nil
	}
	first, last := int64(-1), int64(-1)
	if bitmapConsumerID(id) {
		i, e := bitmapResolvedInstance(ctx, a.dataDir, id)
		if e != nil {
			return e
		}
		first = i.StartHeight
		if h, e := readBitmapDevelopmentHead(ctx, a.dataDir, id); e != nil {
			return e
		} else if h != nil {
			last = h.Height
		}
	}
	if id == "utxo-state" {
		u, e := utxoStoreHead(a.dataDir)
		if e != nil {
			return e
		}
		if u.Head() != nil {
			first, last = 0, u.Head().Height
		}
	}
	if id == "sat-state" {
		s, e := indexStoreHead(a.dataDir, id)
		if e != nil {
			return e
		}
		if s.checkpoint != nil {
			first, last = 0, s.checkpoint.Height
		}
	}
	if id == "address-state" {
		s, e := readAddressStateContext(ctx, a.dataDir)
		if e != nil && !errors.Is(e, ErrAddressStateMissing) {
			return e
		}
		if e == nil && s != nil {
			first, last = 0, s.Height
		}
	}
	act.Item.Range = indexDeleteEffectiveRange(requested, first, last)
	if last < 0 {
		act.Item.Noop = true
		act.Item.Note = "No saved state intersects this range; the track remains."
		return nil
	}
	if act.Item.Range != nil && !indexDeleteIntersects(act.Item.Range, first, last) {
		act.Item.Noop = true
		act.Item.Note = "The selected interval does not intersect this saved state."
		return nil
	}
	reason := "This index stores a cumulative state, not independently deletable block rows. Range deletion is unavailable; use an explicitly reviewed whole-data deletion."
	switch id {
	case "utxo-state":
		reason = "A UTXO snapshot cannot contain arbitrary block holes. A separate exact undo rollback is limited to its retained 288 transitions and dependent Current Addresses; no rollback or later deletion is inferred here."
	case "address-state":
		reason = "Current Addresses is one whole UTXO projection. Removing a block interval would fabricate its balance; delete the whole projection or explicitly rebuild from a chosen complete UTXO state."
	case "sat-state":
		reason = "Live Sat ranges are cumulative. Only an explicitly selected authenticated retained 12/144 savepoint could be restored; arbitrary range deletion and rounding to another savepoint are blocked."
	default:
		if bitmapConsumerID(id) {
			if bitmapConsumerKind(id) == "chain" {
				reason = "Chain commits are cumulative. A separate exact retained predecessor/restore-point operation is required; arbitrary interval deletion cannot preserve a valid continuous commitment and never rewinds the District producer."
			} else {
				reason = "Bitmap winner selection depends on a complete earlier prefix. Arbitrary interval deletion would invalidate later winners; an exact suffix rewind with all dependent tracks must be reviewed separately."
			}
		}
	}
	act.Item.Blockers = append(act.Item.Blockers, reason)
	return nil
}

func (a *app) applyIndexDeleteGeneric(ctx context.Context, g *indexDeleteGenericRange, backup func(string) error, move func(string) error) error {
	s := g.Store
	for _, path := range []string{filepath.Join(s.dir, "head.json"), s.locatorActivePath(), s.locatorActivePath() + ".receipt.json"} {
		if e := backup(path); e != nil {
			return e
		}
	}
	if e := s.writeActiveLocatorHead(g.Checkpoint); e != nil {
		return e
	}
	if e := indexDeleteWriteJSON(filepath.Join(s.dir, "head.json"), g.Head); e != nil {
		return e
	}
	for _, b := range g.Removed {
		path := filepath.Join(s.dir, "commits", b.Checkpoint.Commitment+".json")
		if e := move(path); e != nil {
			return e
		}
	}
	return ctx.Err()
}

// This is the same subtraction used by reorg reconciliation, with an explicit
// user-selected block predicate instead of a provider disagreement. Store and
// shared-fact collection locks are owned by the batch deletion transaction.
func (a *app) applyIndexDeleteHistory(ctx context.Context, s *addressHistoryStore, stale []addressHistoryBlock, backup func(string) error) error {
	if e := s.requireCurrent(ctx); e != nil {
		return e
	}
	blocks := s.tree("blocks", s.head.BlocksRoot, ctx)
	addresses := s.tree("addresses", s.head.AddressRoot, ctx)
	type bucketEdit struct {
		bucket addressHistoryBucket
		rows   *ordinalTree
	}
	changed := map[string]*bucketEdit{}
	h := s.head
	h.Coverage = append([]heightInterval(nil), h.Coverage...)
	h.P2PKCoverage = append([]heightInterval(nil), h.P2PKCoverage...)
	h.PinnedPages = nil
	for _, b := range stale {
		if e := s.visitBlockFacts(ctx, b, func(address, typ string, ref outputFactRef, f outputFact) error {
			c := changed[address]
			if c == nil {
				var bucket addressHistoryBucket
				have, e := addresses.get(addressHistoryAddressKey(address), &bucket)
				if e != nil {
					return e
				}
				if !have || bucket.Address != address || bucket.ScriptType != typ {
					return fmt.Errorf("historical delete bucket mismatch")
				}
				c = &bucketEdit{bucket, s.tree("rows", bucket.RowsRoot, ctx)}
				changed[address] = c
			}
			pos := outputCoordinate(int(f.Vout), f.TxIndex, f.Height)
			key, e := addressHistoryPositionKey(pos)
			if e != nil {
				return e
			}
			var row addressHistoryRow
			have, e := c.rows.get(key, &row)
			if e != nil {
				return e
			}
			if !have || row.Output != pos || row.BlockHash != b.BlockHash || row.Fact != ref || c.bucket.OutputCount == 0 || c.bucket.ValueSats < f.ValueSats {
				return fmt.Errorf("historical delete output mismatch")
			}
			c.rows.remove(key)
			c.bucket.OutputCount--
			c.bucket.ValueSats -= f.ValueSats
			return nil
		}); e != nil {
			return e
		}
		blocks.remove(addressHistoryHeightKey(b.Height))
		h.Coverage = addressHistoryCoverage(h.Coverage, b.Height, false)
		h.P2PKCoverage = addressHistoryCoverage(h.P2PKCoverage, b.Height, false)
	}
	for address, c := range changed {
		root, e := c.rows.flush()
		if e != nil {
			return e
		}
		c.bucket.RowsRoot = root
		if c.bucket.OutputCount == 0 {
			addresses.remove(addressHistoryAddressKey(address))
		} else if e = addresses.put(addressHistoryAddressKey(address), c.bucket); e != nil {
			return e
		}
	}
	ar, e := addresses.flush()
	if e != nil {
		return e
	}
	br, e := blocks.flush()
	if e != nil {
		return e
	}
	h.AddressRoot, h.BlocksRoot = ar, br
	h.Height, h.BlockHash = -1, ""
	if len(h.Coverage) > 0 {
		h.Height = h.Coverage[len(h.Coverage)-1].To
		var b addressHistoryBlock
		have, e := blocks.get(addressHistoryHeightKey(h.Height), &b)
		if e != nil {
			return e
		}
		if !have || b.Height != h.Height {
			return fmt.Errorf("retained historical tip is missing")
		}
		h.BlockHash = b.BlockHash
	}
	h.Previous, h.Commitment = s.head.Commitment, ""
	h.Commitment = addressHistoryDigest(h)
	commit := filepath.Join(s.dir, "commits", h.Commitment[:2], h.Commitment+".json")
	if e = indexDeleteWriteJSON(commit, h); e != nil {
		return e
	}
	head := filepath.Join(s.dir, "head.json")
	if e = backup(head); e != nil {
		return e
	}
	if e = updateapply.CheckPath(head); e != nil {
		return e
	}
	if e = indexDeleteWriteJSON(head, struct {
		Commitment string `json:"commitment"`
	}{h.Commitment}); e != nil {
		return e
	}
	s.head = h
	return nil
}
