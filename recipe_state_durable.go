package main

// Experimental durable ordered-state adapter. It is not wired to the scheduler,
// authenticates no external chain by itself, and exports no snapshots.
import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"

	"./internal/updateapply"
)

const recipeDurableBatchMax = 8 << 20
const recipeDurableTouchedMax = 4096

var recipeDurableMu sync.Mutex // application profile remains single-process owned

type recipeDurableIdentity struct{ Instance, Semantic, Network, Genesis string }
type recipeDurablePoint struct {
	Checkpoint, Root, Receipt, BlockHash string
	Height                               int64
}
type recipeDurableHead struct {
	Schema   int
	Identity recipeDurableIdentity
	Revision uint64
	Point    recipeDurablePoint
	Checksum string
}
type recipeDurableReceipt struct {
	Schema            int
	Identity          recipeDurableIdentity
	Previous, Current recipeDurablePoint
	Batch             recipeStateBatch
}
type recipeDurableEntry struct{ Table, Key, Value []byte }
type recipeDurableStore struct {
	dir      string
	identity recipeDurableIdentity
	hook     func(string) error
}
type recipeDurableForward struct {
	store                       *recipeDurableStore
	base                        recipeStateVersion
	height                      int64
	blockHash, parentHash, next string
	guard                       recipeStoreGuard
}

func recipeDurableDigest(v any) string { b, _ := json.Marshal(v); return recipeStoreHash(b) }
func recipeDurableHeadChecksum(h recipeDurableHead) string {
	h.Checksum = ""
	return recipeDurableDigest(h)
}
func recipeDurableOrigin(id recipeDurableIdentity) string {
	return recipeDurableDigest(struct {
		Domain   string
		Identity recipeDurableIdentity
	}{"gateway-ordered-state-origin-v1", id})
}
func recipeDurableCheckpoint(id recipeDurableIdentity, previous string, height int64, block string) string {
	return recipeDurableDigest(struct {
		Domain   string
		Identity recipeDurableIdentity
		Previous string
		Height   int64
		Block    string
	}{"gateway-ordered-state-point-v1", id, previous, height, block})
}
func recipeDurableKey(k recipeStateKey) string {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(k.Table)))
	b := append([]byte("gateway-ordered-state-key-v1\x00"), n[:]...)
	b = append(b, []byte(k.Table)...)
	b = append(b, []byte(k.Key)...)
	return recipeStoreHash(b)
}
func openRecipeDurableStore(root string, id recipeDurableIdentity) (*recipeDurableStore, error) {
	if id.Instance == "" || len(id.Instance) > 1024 || id.Network == "" || len(id.Network) > 128 || !recipeStoreHashValid(id.Semantic) || !recipeStoreHashValid(id.Genesis) {
		return nil, fmt.Errorf("invalid ordered-state identity")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(abs, "indexes", "recipe-ordered-experimental-v1", recipeStoreHash([]byte(id.Instance)), id.Semantic, recipeStoreHash([]byte(id.Network)), id.Genesis)
	if err = updateapply.CheckPath(dir); err != nil {
		return nil, err
	}
	return &recipeDurableStore{dir: dir, identity: id}, nil
}
func (s *recipeDurableStore) initial() recipeDurableHead {
	return recipeDurableHead{Schema: 1, Identity: s.identity, Point: recipeDurablePoint{Checkpoint: recipeDurableOrigin(s.identity), Height: -1}}
}

// Create is explicit: once the marker exists, a missing head is corruption,
// including after interruption of first initialization.
func (s *recipeDurableStore) Create() error {
	recipeDurableMu.Lock()
	defer recipeDurableMu.Unlock()
	if _, err := os.Stat(s.dir); err == nil {
		return fmt.Errorf("ordered-state generation already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := updateapply.CheckPath(s.dir); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	marker, _ := json.Marshal(s.identity)
	if err := atomicWriteBytes(filepath.Join(s.dir, "initialized.json"), marker); err != nil {
		return err
	}
	h := s.initial()
	h.Checksum = recipeDurableHeadChecksum(h)
	b, _ := json.Marshal(h)
	return atomicWriteBytes(filepath.Join(s.dir, "head.json"), b)
}
func (s *recipeDurableStore) readReceipt(hash string) (recipeDurableReceipt, error) {
	var r recipeDurableReceipt
	if !recipeStoreHashValid(hash) {
		return r, fmt.Errorf("invalid ordered-state receipt hash")
	}
	b, err := recipeStoreReadBounded(filepath.Join(s.dir, "receipts", hash+".json"), recipeDurableBatchMax)
	if err != nil {
		return r, err
	}
	if recipeStoreHash(b) != hash {
		return r, fmt.Errorf("ordered-state receipt checksum mismatch")
	}
	if err = json.Unmarshal(b, &r); err != nil {
		return r, err
	}
	if r.Schema != 1 || r.Identity != s.identity || r.Current.Receipt != "" || r.Current.Height != r.Previous.Height+1 || !recipeStoreHashValid(r.Current.BlockHash) || r.Current.Checkpoint != recipeDurableCheckpoint(s.identity, r.Previous.Checkpoint, r.Current.Height, r.Current.BlockHash) || r.Current.Height == 0 && r.Current.BlockHash != s.identity.Genesis || r.Previous.Height < -1 {
		return r, fmt.Errorf("invalid ordered-state receipt")
	}
	return r, nil
}
func (s *recipeDurableStore) head() (recipeDurableHead, error) {
	var h recipeDurableHead
	marker, err := recipeStoreReadBounded(filepath.Join(s.dir, "initialized.json"), recipeStoreHeadMaxBytes)
	if err != nil {
		return h, err
	}
	want, _ := json.Marshal(s.identity)
	if !bytes.Equal(marker, want) {
		return h, fmt.Errorf("ordered-state marker mismatch")
	}
	b, err := recipeStoreReadBounded(filepath.Join(s.dir, "head.json"), recipeStoreHeadMaxBytes)
	if err != nil {
		return h, err
	}
	if err = json.Unmarshal(b, &h); err != nil {
		return h, err
	}
	if h.Schema != 1 || h.Identity != s.identity || h.Checksum != recipeDurableHeadChecksum(h) || h.Point.Root != "" && !recipeStoreHashValid(h.Point.Root) {
		return h, fmt.Errorf("invalid ordered-state head")
	}
	if h.Point.Height == -1 {
		if h.Point != s.initial().Point {
			return h, fmt.Errorf("invalid origin checkpoint")
		}
	} else {
		r, e := s.readReceipt(h.Point.Receipt)
		if e != nil {
			return h, e
		}
		p := h.Point
		p.Receipt = ""
		if r.Current != p {
			return h, fmt.Errorf("head and receipt disagree")
		}
	}
	if h.Point.Root != "" {
		t := s.tree(context.Background(), h.Point.Root)
		if _, err = t.read(h.Point.Root); err != nil {
			return h, err
		}
	}
	return h, nil
}
func (s *recipeDurableStore) tree(ctx context.Context, root string) *ordinalTree {
	t := newOrdinalTree(filepath.Join(s.dir, "pages"), root).withContext(ctx)
	t.checkPath = updateapply.CheckPath
	return t
}
func (s *recipeDurableStore) saveHead(h recipeDurableHead) error {
	h.Checksum = recipeDurableHeadChecksum(h)
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	if err = updateapply.CheckPath(filepath.Join(s.dir, "head.json")); err != nil {
		return err
	}
	return atomicWriteBytes(filepath.Join(s.dir, "head.json"), b)
}
func (s *recipeDurableStore) status() (recipeDurableHead, error) {
	recipeDurableMu.Lock()
	defer recipeDurableMu.Unlock()
	return s.head()
}
func recipeDurableLimits(l recipeStateLimits) error {
	if err := recipeStateValidateLimits(l); err != nil {
		return err
	}
	if l.MaxTouched > recipeDurableTouchedMax || l.MaxBytes > recipeDurableBatchMax/4 || l.MaxValueBytes > recipeDurableBatchMax/4 {
		return errRecipeStateBudget
	}
	return nil
}
func (s *recipeDurableStore) begin(ctx context.Context, height int64, blockHash, parentHash string, guard recipeStoreGuard, limits recipeStateLimits) (*recipeStateTxn, error) {
	if ctx == nil || guard == nil {
		return nil, fmt.Errorf("context and publication authority required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := recipeDurableLimits(limits); err != nil {
		return nil, err
	}
	h, err := s.status()
	if err != nil {
		return nil, err
	}
	if h.Revision == ^uint64(0) || height != h.Point.Height+1 || !recipeStoreHashValid(blockHash) || height == 0 && blockHash != s.identity.Genesis || height > 0 && parentHash != h.Point.BlockHash || height == 0 && parentHash != "" {
		return nil, fmt.Errorf("ordered-state requires exact next block and predecessor")
	}
	base := recipeStateVersion{Revision: h.Revision, Checkpoint: h.Point.Checkpoint}
	next := recipeDurableCheckpoint(s.identity, base.Checkpoint, height, blockHash)
	b := &recipeDurableForward{store: s, base: base, height: height, blockHash: blockHash, parentHash: parentHash, next: next, guard: guard}
	return recipeStateBegin(b, base, next, limits)
}
func (b *recipeDurableForward) ReadState(ctx context.Context, v recipeStateVersion, k recipeStateKey) (recipeStateValue, error) {
	recipeDurableMu.Lock()
	defer recipeDurableMu.Unlock()
	if err := ctx.Err(); err != nil {
		return recipeStateValue{}, err
	}
	h, err := b.store.head()
	if err != nil {
		return recipeStateValue{}, err
	}
	if v != b.base || v != (recipeStateVersion{Revision: h.Revision, Checkpoint: h.Point.Checkpoint}) {
		return recipeStateValue{}, errRecipeStateConflict
	}
	return b.store.get(ctx, h.Point.Root, k)
}
func (s *recipeDurableStore) get(ctx context.Context, root string, k recipeStateKey) (recipeStateValue, error) {
	if err := recipeStateCheckKey(k); err != nil {
		return recipeStateValue{}, err
	}
	// Receipt keys are JSON strings, which cannot losslessly encode invalid UTF-8.
	if !utf8.ValidString(k.Table) || !utf8.ValidString(k.Key) {
		return recipeStateValue{}, fmt.Errorf("ordered-state keys must be valid UTF-8")
	}
	var entry recipeDurableEntry
	found, err := s.tree(ctx, root).get(recipeDurableKey(k), &entry)
	if err != nil {
		return recipeStateValue{}, err
	}
	if !found {
		return recipeStateValue{}, nil
	}
	if !bytes.Equal(entry.Table, []byte(k.Table)) || !bytes.Equal(entry.Key, []byte(k.Key)) {
		return recipeStateValue{}, fmt.Errorf("ordered-state key hash collision")
	}
	return recipeStateValue{Present: true, Bytes: append([]byte(nil), entry.Value...)}, nil
}
func (b *recipeDurableForward) ApplyState(ctx context.Context, v recipeStateVersion, next string, batch recipeStateBatch) (recipeStateVersion, error) {
	var result recipeStateVersion
	raw, err := json.Marshal(batch)
	if err != nil {
		return result, err
	}
	if len(raw) > recipeDurableBatchMax/2 || len(batch.BeforeImages) > recipeDurableTouchedMax || len(batch.Changes) > recipeDurableTouchedMax {
		return result, errRecipeStateBudget
	}
	err = recipeStoreGuarded(b.guard, func() error {
		recipeDurableMu.Lock()
		defer recipeDurableMu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := b.store.head()
		if err != nil {
			return err
		}
		if v != b.base || next != b.next || v != (recipeStateVersion{Revision: h.Revision, Checkpoint: h.Point.Checkpoint}) || h.Revision == ^uint64(0) || h.Point.Height+1 != b.height || b.height > 0 && h.Point.BlockHash != b.parentHash {
			return errRecipeStateConflict
		}
		before := make(map[recipeStateKey]recipeStateValue, len(batch.BeforeImages))
		for i, image := range batch.BeforeImages {
			if i > 0 && !recipeStateLess(batch.BeforeImages[i-1].Key, image.Key) {
				return fmt.Errorf("invalid ordered before images")
			}
			actual, e := b.store.get(ctx, h.Point.Root, image.Key)
			if e != nil {
				return e
			}
			if !recipeStateEqualValue(actual, image.Value) {
				return errRecipeStateConflict
			}
			before[image.Key] = image.Value
		}
		t := b.store.tree(ctx, h.Point.Root)
		for i, c := range batch.Changes {
			v, ok := before[c.Key]
			if !ok || !recipeStateEqualValue(v, c.Before) || recipeStateEqualValue(c.Before, c.After) || i > 0 && !recipeStateLess(batch.Changes[i-1].Key, c.Key) {
				return fmt.Errorf("invalid ordered changes")
			}
			if c.After.Present {
				if err = t.put(recipeDurableKey(c.Key), recipeDurableEntry{[]byte(c.Key.Table), []byte(c.Key.Key), c.After.Bytes}); err != nil {
					return err
				}
			} else {
				t.remove(recipeDurableKey(c.Key))
			}
		}
		root, err := t.flush()
		if err != nil {
			return err
		}
		if b.store.hook != nil {
			if err = b.store.hook("pages"); err != nil {
				return err
			}
		}
		point := recipeDurablePoint{Checkpoint: next, Root: root, Height: b.height, BlockHash: b.blockHash}
		receipt := recipeDurableReceipt{Schema: 1, Identity: b.store.identity, Previous: h.Point, Current: point, Batch: batch}
		rb, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		if len(rb) > recipeDurableBatchMax {
			return errRecipeStateBudget
		}
		hash := recipeStoreHash(rb)
		if err = recipeStorePutImmutable(filepath.Join(b.store.dir, "receipts"), hash, rb); err != nil {
			return err
		}
		if b.store.hook != nil {
			if err = b.store.hook("receipt"); err != nil {
				return err
			}
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		point.Receipt = hash
		h.Point = point
		h.Revision++
		if err = b.store.saveHead(h); err != nil {
			return err
		}
		result = recipeStateVersion{Revision: h.Revision, Checkpoint: point.Checkpoint}
		return nil
	})
	return result, err
}

// Undo uses the authenticated current receipt and swaps the sole head to its
// exact predecessor root. Caller authority must hold throughout the callback.
func (s *recipeDurableStore) undo(ctx context.Context, expected recipeStateVersion, guard recipeStoreGuard) (recipeStateVersion, error) {
	var result recipeStateVersion
	if ctx == nil || guard == nil {
		return result, fmt.Errorf("context and authority required")
	}
	err := recipeStoreGuarded(guard, func() error {
		recipeDurableMu.Lock()
		defer recipeDurableMu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := s.head()
		if err != nil {
			return err
		}
		if expected != (recipeStateVersion{Revision: h.Revision, Checkpoint: h.Point.Checkpoint}) || h.Point.Height < 0 || h.Revision == ^uint64(0) {
			return errRecipeStateConflict
		}
		r, err := s.readReceipt(h.Point.Receipt)
		if err != nil {
			return err
		}
		// Authenticate predecessor as a head before making it reachable.
		prior := h
		prior.Point = r.Previous
		prior.Checksum = recipeDurableHeadChecksum(prior)
		if prior.Point.Height == -1 {
			if prior.Point != s.initial().Point {
				return fmt.Errorf("invalid predecessor origin")
			}
		} else {
			p, e := s.readReceipt(prior.Point.Receipt)
			if e != nil {
				return e
			}
			q := prior.Point
			q.Receipt = ""
			if p.Current != q {
				return fmt.Errorf("predecessor receipt mismatch")
			}
		}
		if prior.Point.Root != "" {
			if _, err = s.tree(ctx, prior.Point.Root).read(prior.Point.Root); err != nil {
				return err
			}
		}
		for _, c := range r.Batch.Changes {
			actual, e := s.get(ctx, h.Point.Root, c.Key)
			if e != nil {
				return e
			}
			if !recipeStateEqualValue(actual, c.After) {
				return errRecipeStateConflict
			}
			old, e := s.get(ctx, prior.Point.Root, c.Key)
			if e != nil {
				return e
			}
			if !recipeStateEqualValue(old, c.Before) {
				return errRecipeStateConflict
			}
		}
		if s.hook != nil {
			if err = s.hook("undo"); err != nil {
				return err
			}
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		h.Point = prior.Point
		h.Revision++
		if err = s.saveHead(h); err != nil {
			return err
		}
		result = recipeStateVersion{Revision: h.Revision, Checkpoint: h.Point.Checkpoint}
		return nil
	})
	return result, err
}
