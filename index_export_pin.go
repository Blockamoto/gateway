package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
	"time"
)

// Pins are process-local, bounded and never hold storage leases. Each download
// rechecks live membership and chain ancestry, then re-reads authenticated data.
const indexExportPinLifetime = 15 * time.Minute
const indexExportPinLimit = 4
const indexExportPinMemoryLimit = 256 << 20
const indexExportResolvedReviewMemoryLimit = 256 << 20

var errIndexExportExpired = errors.New("export review expired or was lost; review again")
var indexExportGenerationSlot = make(chan struct{}, 1)

type indexExportPinKey struct {
	app                             *app
	index, representation, snapshot string
}
type indexExportPin struct {
	plan    *indexExportSession
	digest  string
	size    int64
	expires time.Time
	weight  int
	timer   *time.Timer
}

var indexExportPins = struct {
	sync.Mutex
	items map[indexExportPinKey]*indexExportPin
}{items: make(map[indexExportPinKey]*indexExportPin)}

type indexExportCountWriter struct {
	w io.Writer
	n int64
}

func (w *indexExportCountWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.n += int64(n)
	return n, err
}

func cloneExportSession(src *indexExportSession) *indexExportSession {
	out := *src
	out.ctx, out.hashAt, out.rawRead = nil, nil, nil
	out.anchors = nil
	out.rawView, out.rawIDs = nil, nil
	out.rawTried, out.rawHeight, out.rawHash = false, -1, ""
	out.recordMissing, out.replayMissing, out.missingBudget, out.resolvedBudget = false, false, nil, nil
	out.resolvedDecisions = nil
	out.readBytes, out.readBlocks = 0, 0
	out.meta.Rows, out.meta.Blocks, out.meta.MissingCount = 0, 0, 0
	out.meta.OmittedWinners, out.meta.Partial = 0, false
	out.meta.Missing, out.meta.Requirements = []indexExportMissing{}, []indexExportRequirement{}
	out.meta.MissingTruncated, out.meta.Ready, out.meta.Complete = false, false, false
	out.meta.LookupRange = nil
	stores := map[*indexExportPinnedStore]*indexExportPinnedStore{}
	cloneStore := func(p *indexExportPinnedStore) *indexExportPinnedStore {
		if p == nil {
			return nil
		}
		if c := stores[p]; c != nil {
			return c
		}
		c := *p
		store := *p.s
		c.s = &store
		c.cache = map[int64]indexBatch{}
		c.spill = nil
		c.recordLookups = false
		c.pinBudget = nil
		stores[p] = &c
		return &c
	}
	views := map[*indexExportBrowse]*indexExportBrowse{}
	cloneView := func(p *indexExportBrowse) *indexExportBrowse {
		if p == nil {
			return nil
		}
		if c := views[p]; c != nil {
			return c
		}
		c := *p
		store := *p.s
		c.s = &store
		c.cache = map[int64]indexBatch{}
		views[p] = &c
		return &c
	}
	out.primary, out.browse = cloneStore(src.primary), cloneView(src.browse)
	out.lookups = make([]*indexExportPinnedStore, len(src.lookups))
	for i, p := range src.lookups {
		out.lookups[i] = cloneStore(p)
	}
	out.lookupViews = make([]*indexExportBrowse, len(src.lookupViews))
	for i, p := range src.lookupViews {
		out.lookupViews[i] = cloneView(p)
	}
	out.memberships = make([]*indexExportBrowse, len(src.memberships))
	for i, p := range src.memberships {
		out.memberships[i] = cloneView(p)
	}
	return &out
}
func newIndexExportPin(s *indexExportSession) *indexExportPin {
	p := &indexExportPin{plan: cloneExportSession(s), weight: 4096}
	metadata, err := json.Marshal(struct {
		Metadata         indexExportMetadata
		Bitmap, District *bitmapDevelopmentHead
	}{s.meta, s.bitmap, s.district})
	if err != nil {
		p.weight = indexExportPinMemoryLimit + 1
	} else {
		p.weight += 2 * len(metadata)
	}
	seen := map[*indexExportBrowse]bool{}
	for _, v := range append(append([]*indexExportBrowse{}, s.memberships...), s.lookupViews...) {
		if v == nil || seen[v] {
			continue
		}
		seen[v] = true
		for k, value := range v.blocks {
			p.weight += 128 + len(k) + len(value)
		}
	}
	seenStores := map[*indexExportPinnedStore]bool{}
	for _, store := range s.lookups {
		if store == nil || seenStores[store] {
			continue
		}
		seenStores[store] = true
		p.weight += len(store.pinnedLookups) * 160
	}
	for k, item := range s.missingDecisions {
		p.weight += 160 + len(k) + len(item.BlockHash) + len(item.Reason)
	}
	return p
}
func (p *indexExportPin) session(ctx context.Context, a *app, q indexExportRequest, authority chainAuthorityView, hashAt func(int64) (string, error), _ func(context.Context, int64, string) (*blockView, error)) *indexExportSession {
	s := cloneExportSession(p.plan)
	s.a, s.ctx, s.q, s.authority, s.hashAt = a, ctx, q, authority, hashAt
	s.anchors = map[int64]string{}
	s.rawRead = s.readRaw
	s.replayMissing = true
	for _, store := range s.lookups {
		if store != nil {
			store.forcePinned = true
		}
	}
	return s
}
func saveIndexExportPin(a *app, q indexExportRequest, snapshot string, p *indexExportPin) error {
	if p.weight > indexExportPinMemoryLimit {
		return fmt.Errorf("export review exceeds the bounded snapshot memory budget")
	}
	indexExportPins.Lock()
	defer indexExportPins.Unlock()
	key := indexExportPinKey{a, q.Index, q.Representation, snapshot}
	if indexExportPins.items[key] != nil {
		return fmt.Errorf("export review token collision")
	}
	for k, old := range indexExportPins.items {
		if !time.Now().Before(old.expires) {
			if old.timer != nil {
				old.timer.Stop()
			}
			delete(indexExportPins.items, k)
		}
	}
	for {
		weight := p.weight
		for _, old := range indexExportPins.items {
			weight += old.weight
		}
		if len(indexExportPins.items) < indexExportPinLimit && weight <= indexExportPinMemoryLimit {
			break
		}
		var oldestKey indexExportPinKey
		var oldest *indexExportPin
		for k, old := range indexExportPins.items {
			if oldest == nil || old.expires.Before(oldest.expires) {
				oldestKey, oldest = k, old
			}
		}
		if oldest.timer != nil {
			oldest.timer.Stop()
		}
		delete(indexExportPins.items, oldestKey)
	}
	indexExportPins.items[key] = p
	deadline := p.expires
	token := snapshot
	p.timer = time.AfterFunc(time.Until(deadline), func() {
		indexExportPins.Lock()
		defer indexExportPins.Unlock()
		for k, current := range indexExportPins.items {
			if k.snapshot == token && current.expires.Equal(deadline) && !time.Now().Before(deadline) {
				delete(indexExportPins.items, k)
			}
		}
	})
	return nil
}
func findIndexExportPin(a *app, q indexExportRequest) (*indexExportPin, error) {
	indexExportPins.Lock()
	defer indexExportPins.Unlock()
	key := indexExportPinKey{a, q.Index, q.Representation, q.Snapshot}
	p := indexExportPins.items[key]
	if p == nil || !time.Now().Before(p.expires) {
		if p != nil && p.timer != nil {
			p.timer.Stop()
		}
		delete(indexExportPins.items, key)
		return nil, errIndexExportExpired
	}
	return p, nil
}

// Current membership must still contain each pinned source. Append is allowed;
// deletion, replacement and reorg must not silently resurrect old artifacts.
func (s *indexExportSession) validateExportPin(pin *indexExportPin) error {
	invalid := func(err error) error {
		if s.ctx.Err() != nil {
			return s.ctx.Err()
		}
		return fmt.Errorf("%w: %v", errIndexExportSnapshotChanged, err)
	}
	old := pin.plan
	currentStores := map[string]*indexExportPinnedStore{}
	for _, p := range append(append([]*indexExportPinnedStore{}, s.lookups...), s.primary) {
		if p != nil {
			currentStores[p.s.definition.ID] = p
		}
	}
	seen := map[*indexExportPinnedStore]bool{}
	for _, p := range append(append([]*indexExportPinnedStore{}, old.lookups...), old.primary) {
		if p == nil || seen[p] {
			continue
		}
		seen[p] = true
		cur := currentStores[p.s.definition.ID]
		if cur == nil || cur.to < p.to {
			return invalid(fmt.Errorf("saved index membership changed"))
		}
		next := cur.s.head.Commitment
		var child *indexBatch
		for {
			if err := s.ctx.Err(); err != nil {
				return err
			}
			if next == "" {
				return invalid(fmt.Errorf("pinned index is not an ancestor"))
			}
			b, err := s.readBatch(cur.s, next)
			if err != nil {
				return invalid(err)
			}
			if child != nil && (child.Checkpoint.Height != b.Checkpoint.Height+1 || child.Checkpoint.From != b.Checkpoint.From || child.PreviousBlockHash != b.Checkpoint.BlockHash) {
				return invalid(fmt.Errorf("index ancestry is discontinuous"))
			}
			if err = s.anchor(b.Checkpoint.Height, b.Checkpoint.BlockHash); err != nil {
				return invalid(err)
			}
			if next == p.s.head.Commitment {
				if p.s.checkpoint == nil || !reflect.DeepEqual(b.Checkpoint, *p.s.checkpoint) {
					return invalid(fmt.Errorf("pinned checkpoint changed"))
				}
				break
			}
			if b.Checkpoint.Height <= p.to {
				return invalid(fmt.Errorf("pinned index is not an ancestor"))
			}
			child = &b
			next = b.Checkpoint.PreviousCommitment
		}
	}
	currentViews := map[string]*indexExportBrowse{}
	for _, v := range append(append([]*indexExportBrowse{}, s.lookupViews...), s.browse) {
		if v != nil {
			currentViews[v.s.definition.ID] = v
		}
	}
	seenViews := map[*indexExportBrowse]bool{}
	for _, v := range append(append([]*indexExportBrowse{}, old.lookupViews...), old.browse) {
		if v == nil || seenViews[v] {
			continue
		}
		seenViews[v] = true
		cur := currentViews[v.s.definition.ID]
		if cur == nil {
			return invalid(fmt.Errorf("saved sparse membership removed"))
		}
		for h, c := range v.blocks {
			if cur.blocks[h] != c {
				return invalid(fmt.Errorf("saved sparse membership changed"))
			}
		}
	}
	for _, pair := range [][2]*bitmapDevelopmentHead{{old.bitmap, s.bitmap}, {old.district, s.district}} {
		want, cur := pair[0], pair[1]
		if want == nil {
			continue
		}
		if cur == nil || cur.Height < want.Height {
			return invalid(fmt.Errorf("saved Bitmap membership removed"))
		}
		for cur.Commitment != want.Commitment {
			if cur.Height <= want.Height || cur.Previous == "" {
				return invalid(fmt.Errorf("pinned Bitmap is not an ancestor"))
			}
			prev, err := readBitmapDevelopmentCommit(s.ctx, s.a.dataDir, cur.Index, cur.Previous)
			if err != nil {
				return invalid(err)
			}
			if prev.Height+1 != cur.Height || prev.BlockHash != cur.PreviousBlockHash {
				return invalid(fmt.Errorf("Bitmap ancestry is discontinuous"))
			}
			cur = prev
		}
		if !reflect.DeepEqual(cur, want) {
			return invalid(fmt.Errorf("pinned Bitmap checkpoint changed"))
		}
		if err := s.anchor(want.Height, want.BlockHash); err != nil {
			return invalid(err)
		}
	}
	return nil
}
