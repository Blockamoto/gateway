package main

// Isolated staging experiment. This generation is not registered with the
// scheduler, public status, the existing durable adapter, or ordered recipes.
// The host must authenticate the source commitment, cursor and executor.
import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"./internal/updateapply"
)

const recipeStageChunkOps = 128
const recipeStageChunkBytes = 1 << 20

var recipeStageMu sync.Mutex // application profile must be single-process owned

func recipeStageReservedTable(table string) bool { return strings.HasPrefix(table, "__gateway_") }

type recipeStageOperation struct {
	Key   recipeStateKey
	After recipeStateValue
}
type recipeStageJournalEntry struct {
	Table, Key    []byte
	Before, After recipeStateValue
}
type recipeStageHead struct {
	Schema                              int
	Identity                            recipeDurableIdentity
	Revision                            uint64
	Height                              int64
	BlockHash, Root, Manifest, Checksum string
}
type recipeStageChunk struct {
	Schema                                             int
	Stage                                              string
	Sequence                                           uint64
	Prior                                              string
	Start, End                                         uint64
	BeforeRoot, AfterRoot, BeforeJournal, AfterJournal string
	Operations                                         []recipeStateChange
}
type recipeStageManifest struct {
	Schema                                                int
	Identity                                              recipeDurableIdentity
	Base                                                  recipeStageHead
	Height                                                int64
	BlockHash, Root, Journal, SourceCommitment, LastChunk string
	SourceTotal, Processed, Chunks, Touched, Net          uint64
	Complete                                              bool
}
type recipeStageCheckpoint struct {
	Schema                                      int
	ID                                          string
	Identity                                    recipeDurableIdentity
	Base                                        recipeStageHead
	Height                                      int64
	BlockHash, ParentHash, SourceCommitment     string
	SourceTotal, Cursor, Sequence, Touched, Net uint64
	Root, Journal, LastChunk                    string
	Sealed                                      bool
	VerifyHash, VerifyRoot, VerifyJournal       string
	VerifyCursor, VerifySequence                uint64
	Verified                                    bool
	Checksum                                    string
}
type recipeStageStore struct {
	dir      string
	identity recipeDurableIdentity
	hook     func(string) error
}

func recipeStageHeadHash(h recipeStageHead) string { h.Checksum = ""; return recipeDurableDigest(h) }
func recipeStageCheckpointHash(s recipeStageCheckpoint) string {
	s.Checksum = ""
	return recipeDurableDigest(s)
}
func recipeStageOpen(root string, id recipeDurableIdentity) (*recipeStageStore, error) {
	if id.Instance == "" || len(id.Instance) > 1024 || id.Network == "" || len(id.Network) > 128 || !recipeStoreHashValid(id.Semantic) || !recipeStoreHashValid(id.Genesis) {
		return nil, fmt.Errorf("invalid staged state identity")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(abs, "indexes", "recipe-staged-experimental-v1", recipeStoreHash([]byte(id.Instance)), id.Semantic, recipeStoreHash([]byte(id.Network)), id.Genesis)
	if err = updateapply.CheckPath(dir); err != nil {
		return nil, err
	}
	return &recipeStageStore{dir: dir, identity: id}, nil
}
func (s *recipeStageStore) headPath() string { return filepath.Join(s.dir, "head.json") }
func (s *recipeStageStore) stagePath(id string) string {
	return filepath.Join(s.dir, "stages", id+".json")
}
func (s *recipeStageStore) tree(ctx context.Context, root string) *ordinalTree {
	t := newOrdinalTree(filepath.Join(s.dir, "pages"), root).withContext(ctx)
	t.checkPath = updateapply.CheckPath
	return t
}
func (s *recipeStageStore) initial() recipeStageHead {
	return recipeStageHead{Schema: 1, Identity: s.identity, Height: -1}
}
func (s *recipeStageStore) create() error {
	recipeStageMu.Lock()
	defer recipeStageMu.Unlock()
	if _, e := os.Stat(s.dir); e == nil {
		return fmt.Errorf("staged generation exists")
	} else if !os.IsNotExist(e) {
		return e
	}
	if e := updateapply.CheckPath(s.dir); e != nil {
		return e
	}
	if e := os.MkdirAll(s.dir, 0700); e != nil {
		return e
	}
	marker, _ := json.Marshal(s.identity)
	if e := atomicWriteBytes(filepath.Join(s.dir, "initialized.json"), marker); e != nil {
		return e
	}
	return s.writeHead(s.initial())
}
func (s *recipeStageStore) writeHead(h recipeStageHead) error {
	h.Checksum = recipeStageHeadHash(h)
	b, e := json.Marshal(h)
	if e != nil {
		return e
	}
	if e = updateapply.CheckPath(s.headPath()); e != nil {
		return e
	}
	return atomicWriteBytes(s.headPath(), b)
}
func (s *recipeStageStore) manifest(hash string) (recipeStageManifest, error) {
	var m recipeStageManifest
	if !recipeStoreHashValid(hash) {
		return m, fmt.Errorf("invalid stage manifest")
	}
	b, e := recipeStoreReadBounded(filepath.Join(s.dir, "manifests", hash+".json"), recipeDurableBatchMax)
	if e != nil {
		return m, e
	}
	if recipeStoreHash(b) != hash {
		return m, fmt.Errorf("stage manifest checksum mismatch")
	}
	if e = json.Unmarshal(b, &m); e != nil {
		return m, e
	}
	if m.Schema != 1 || m.Identity != s.identity || !m.Complete || m.Processed != m.SourceTotal || m.Height != m.Base.Height+1 || !recipeStoreHashValid(m.BlockHash) || !recipeStoreHashValid(m.SourceCommitment) || m.Root != "" && !recipeStoreHashValid(m.Root) || m.Journal != "" && !recipeStoreHashValid(m.Journal) || m.Chunks == 0 && m.LastChunk != "" || m.Chunks > 0 && !recipeStoreHashValid(m.LastChunk) {
		return m, fmt.Errorf("invalid stage manifest")
	}
	return m, nil
}
func (s *recipeStageStore) head() (recipeStageHead, error) {
	var h recipeStageHead
	b, e := recipeStoreReadBounded(filepath.Join(s.dir, "initialized.json"), recipeStoreHeadMaxBytes)
	if e != nil {
		return h, e
	}
	want, _ := json.Marshal(s.identity)
	if !bytes.Equal(b, want) {
		return h, fmt.Errorf("stage identity marker mismatch")
	}
	b, e = recipeStoreReadBounded(s.headPath(), recipeStoreHeadMaxBytes)
	if e != nil {
		return h, e
	}
	if e = json.Unmarshal(b, &h); e != nil {
		return h, e
	}
	if h.Schema != 1 || h.Identity != s.identity || h.Checksum != recipeStageHeadHash(h) || h.Height < -1 || h.Root != "" && !recipeStoreHashValid(h.Root) {
		return h, fmt.Errorf("invalid staged head")
	}
	if h.Height == -1 {
		z := s.initial()
		z.Revision = h.Revision
		z.Checksum = h.Checksum
		if h != z {
			return h, fmt.Errorf("invalid staged origin")
		}
	} else {
		m, err := s.manifest(h.Manifest)
		if err != nil {
			return h, err
		}
		if m.Height != h.Height || m.BlockHash != h.BlockHash || m.Root != h.Root {
			return h, fmt.Errorf("staged head and manifest disagree")
		}
	}
	return h, nil
}
func (s *recipeStageStore) status() (recipeStageHead, error) {
	recipeStageMu.Lock()
	defer recipeStageMu.Unlock()
	return s.head()
}
func (s *recipeStageStore) checkpoint(id string) (recipeStageCheckpoint, error) {
	var st recipeStageCheckpoint
	if !recipeStoreHashValid(id) {
		return st, fmt.Errorf("invalid stage id")
	}
	b, e := recipeStoreReadBounded(s.stagePath(id), recipeStoreHeadMaxBytes)
	if e != nil {
		return st, e
	}
	if e = json.Unmarshal(b, &st); e != nil {
		return st, e
	}
	if st.Schema != 1 || st.ID != id || st.Identity != s.identity || st.Checksum != recipeStageCheckpointHash(st) || st.Cursor > st.SourceTotal || st.Sequence == 0 && st.LastChunk != "" || st.Sequence > 0 && !recipeStoreHashValid(st.LastChunk) {
		return st, fmt.Errorf("invalid stage checkpoint")
	}
	return st, nil
}
func (s *recipeStageStore) saveCheckpoint(st recipeStageCheckpoint) error {
	st.Checksum = recipeStageCheckpointHash(st)
	b, e := json.Marshal(st)
	if e != nil {
		return e
	}
	if len(b) > recipeStoreHeadMaxBytes {
		return errRecipeStateBudget
	}
	if e = updateapply.CheckPath(s.stagePath(st.ID)); e != nil {
		return e
	}
	return atomicWriteBytes(s.stagePath(st.ID), b)
}
func (s *recipeStageStore) begin(ctx context.Context, height int64, hash, parent, source string, total uint64) (recipeStageCheckpoint, error) {
	var st recipeStageCheckpoint
	if ctx == nil || !recipeStoreHashValid(hash) || !recipeStoreHashValid(source) {
		return st, fmt.Errorf("invalid staged source")
	}
	recipeStageMu.Lock()
	defer recipeStageMu.Unlock()
	if e := ctx.Err(); e != nil {
		return st, e
	}
	head, e := s.head()
	if e != nil {
		return st, e
	}
	if head.Revision == ^uint64(0) || height != head.Height+1 || height == 0 && hash != s.identity.Genesis || height == 0 && parent != "" || height > 0 && parent != head.BlockHash {
		return st, fmt.Errorf("staged source is not the next block")
	}
	var random [32]byte
	if _, e = rand.Read(random[:]); e != nil {
		return st, e
	}
	st = recipeStageCheckpoint{Schema: 1, ID: hex.EncodeToString(random[:]), Identity: s.identity, Base: head, Height: height, BlockHash: hash, ParentHash: parent, SourceCommitment: source, SourceTotal: total, Root: head.Root}
	if e = s.saveCheckpoint(st); e != nil {
		return recipeStageCheckpoint{}, e
	}
	return st, nil
}
func recipeStageValidateKey(k recipeStateKey) error {
	if e := recipeStateCheckKey(k); e != nil {
		return e
	}
	if !utf8.ValidString(k.Table) || !utf8.ValidString(k.Key) {
		return fmt.Errorf("invalid staged key encoding")
	}
	return nil
}
func recipeStageGet(t *ordinalTree, k recipeStateKey) (recipeStateValue, error) {
	if e := recipeStageValidateKey(k); e != nil {
		return recipeStateValue{}, e
	}
	var entry recipeDurableEntry
	found, e := t.get(recipeDurableKey(k), &entry)
	if e != nil {
		return recipeStateValue{}, e
	}
	if !found {
		return recipeStateValue{}, nil
	}
	if !bytes.Equal(entry.Table, []byte(k.Table)) || !bytes.Equal(entry.Key, []byte(k.Key)) {
		return recipeStateValue{}, fmt.Errorf("staged key collision")
	}
	return recipeStateValue{Present: true, Bytes: append([]byte(nil), entry.Value...)}, nil
}
func recipeStageJournalGet(t *ordinalTree, k recipeStateKey) (recipeStageJournalEntry, bool, error) {
	var j recipeStageJournalEntry
	found, e := t.get(recipeDurableKey(k), &j)
	if e != nil || !found {
		return j, found, e
	}
	if !bytes.Equal(j.Table, []byte(k.Table)) || !bytes.Equal(j.Key, []byte(k.Key)) {
		return j, false, fmt.Errorf("staged journal collision")
	}
	return j, true, nil
}
func (s *recipeStageStore) append(ctx context.Context, id string, expected, start, end uint64, ops []recipeStageOperation) (recipeStageCheckpoint, error) {
	var zero recipeStageCheckpoint
	if ctx == nil || len(ops) > recipeStageChunkOps || end < start || end == start && len(ops) == 0 {
		return zero, fmt.Errorf("invalid staged chunk")
	}
	// Bound inputs before JSON serialization, not after its allocations.
	inputBytes := 0
	for _, op := range ops {
		if err := recipeStageValidateKey(op.Key); err != nil {
			return zero, err
		}
		if !op.After.Present && len(op.After.Bytes) != 0 {
			return zero, fmt.Errorf("invalid staged value")
		}
		for _, n := range []int{len(op.Key.Table), len(op.Key.Key), len(op.After.Bytes)} {
			if n > recipeStageChunkBytes/2-inputBytes {
				return zero, errRecipeStateBudget
			}
			inputBytes += n
		}
	}
	encoded, e := json.Marshal(ops)
	if e != nil {
		return zero, e
	}
	if len(encoded) > recipeStageChunkBytes {
		return zero, errRecipeStateBudget
	}
	recipeStageMu.Lock()
	defer recipeStageMu.Unlock()
	if e = ctx.Err(); e != nil {
		return zero, e
	}
	st, e := s.checkpoint(id)
	if e != nil {
		return zero, e
	}
	if st.Sealed || st.Sequence != expected || st.Cursor != start || end > st.SourceTotal || st.Sequence == ^uint64(0) {
		return zero, errRecipeStateConflict
	}
	state := s.tree(ctx, st.Root)
	journal := s.tree(ctx, st.Journal)
	changes := make([]recipeStateChange, 0, len(ops))
	workBytes := inputBytes
	for _, op := range ops {
		if e = ctx.Err(); e != nil {
			return zero, e
		}
		if e = recipeStageValidateKey(op.Key); e != nil {
			return zero, e
		}
		if !op.After.Present && len(op.After.Bytes) != 0 || len(op.After.Bytes) > recipeStageChunkBytes {
			return zero, fmt.Errorf("invalid staged value")
		}
		before, err := recipeStageGet(state, op.Key)
		if err != nil {
			return zero, err
		}
		if len(before.Bytes) > recipeStageChunkBytes-workBytes {
			return zero, errRecipeStateBudget
		}
		workBytes += len(before.Bytes)
		j, found, err := recipeStageJournalGet(journal, op.Key)
		if err != nil {
			return zero, err
		}
		if !found {
			if st.Touched == ^uint64(0) {
				return zero, errRecipeStateBudget
			}
			st.Touched++
			j = recipeStageJournalEntry{Table: []byte(op.Key.Table), Key: []byte(op.Key.Key), Before: before, After: before}
		}
		was := !recipeStateEqualValue(j.Before, j.After)
		j.After = recipeStateCloneValue(op.After)
		now := !recipeStateEqualValue(j.Before, j.After)
		if was && !now {
			if st.Net == 0 {
				return zero, fmt.Errorf("invalid staged net count")
			}
			st.Net--
		} else if !was && now {
			if st.Net == ^uint64(0) {
				return zero, errRecipeStateBudget
			}
			st.Net++
		}
		if e = journal.put(recipeDurableKey(op.Key), j); e != nil {
			return zero, e
		}
		if op.After.Present {
			e = state.put(recipeDurableKey(op.Key), recipeDurableEntry{Table: []byte(op.Key.Table), Key: []byte(op.Key.Key), Value: op.After.Bytes})
		} else {
			state.remove(recipeDurableKey(op.Key))
		}
		if e != nil {
			return zero, e
		}
		changes = append(changes, recipeStateChange{Key: op.Key, Before: before, After: recipeStateCloneValue(op.After)})
	}
	root, e := state.flush()
	if e != nil {
		return zero, e
	}
	jr, e := journal.flush()
	if e != nil {
		return zero, e
	}
	if s.hook != nil {
		if e = s.hook("pages"); e != nil {
			return zero, e
		}
	}
	chunk := recipeStageChunk{Schema: 1, Stage: id, Sequence: st.Sequence + 1, Prior: st.LastChunk, Start: start, End: end, BeforeRoot: st.Root, AfterRoot: root, BeforeJournal: st.Journal, AfterJournal: jr, Operations: changes}
	b, e := json.Marshal(chunk)
	if e != nil {
		return zero, e
	}
	if len(b) > recipeDurableBatchMax {
		return zero, errRecipeStateBudget
	}
	hash := recipeStoreHash(b)
	if e = recipeStorePutImmutable(filepath.Join(s.dir, "chunks"), hash, b); e != nil {
		return zero, e
	}
	if s.hook != nil {
		if e = s.hook("chunk"); e != nil {
			return zero, e
		}
	}
	if e = ctx.Err(); e != nil {
		return zero, e
	}
	st.Root, st.Journal, st.Cursor, st.LastChunk = root, jr, end, hash
	st.Sequence++
	if e = s.saveCheckpoint(st); e != nil {
		return zero, e
	}
	return st, nil
}

func (s *recipeStageStore) seal(ctx context.Context, id string, expected uint64) (recipeStageCheckpoint, error) {
	var z recipeStageCheckpoint
	if ctx == nil {
		return z, fmt.Errorf("missing context")
	}
	recipeStageMu.Lock()
	defer recipeStageMu.Unlock()
	if e := ctx.Err(); e != nil {
		return z, e
	}
	st, e := s.checkpoint(id)
	if e != nil {
		return z, e
	}
	if st.Sealed || st.Sequence != expected || st.Cursor != st.SourceTotal {
		return z, errRecipeStateConflict
	}
	st.Sealed = true
	st.VerifyHash = st.LastChunk
	st.VerifySequence = st.Sequence
	st.VerifyRoot = st.Root
	st.VerifyJournal = st.Journal
	st.VerifyCursor = st.Cursor
	if st.Sequence == 0 {
		if st.Root != st.Base.Root || st.Cursor != 0 || st.Journal != "" {
			return z, fmt.Errorf("invalid empty stage")
		}
		st.Verified = true
	}
	if e = s.saveCheckpoint(st); e != nil {
		return z, e
	}
	return st, nil
}
func (s *recipeStageStore) readChunk(hash string) (recipeStageChunk, error) {
	var c recipeStageChunk
	if !recipeStoreHashValid(hash) {
		return c, fmt.Errorf("invalid staged chunk hash")
	}
	b, e := recipeStoreReadBounded(filepath.Join(s.dir, "chunks", hash+".json"), recipeDurableBatchMax)
	if e != nil {
		return c, e
	}
	if recipeStoreHash(b) != hash {
		return c, fmt.Errorf("staged chunk checksum mismatch")
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, e
	}
	if c.Schema != 1 || len(c.Operations) > recipeStageChunkOps || c.End < c.Start {
		return c, fmt.Errorf("invalid staged chunk")
	}
	return c, nil
}

// Verify performs a bounded number of chunk checks and persists its cursor.
// It checks every changed key against both roots and its first-before journal;
// inherited untouched tree pages remain governed by the prior checkpoint.
func (s *recipeStageStore) verify(ctx context.Context, id string, budget int) (bool, error) {
	if ctx == nil || budget < 1 || budget > 128 {
		return false, fmt.Errorf("invalid stage verification budget")
	}
	recipeStageMu.Lock()
	defer recipeStageMu.Unlock()
	st, e := s.checkpoint(id)
	if e != nil {
		return false, e
	}
	if !st.Sealed {
		return false, fmt.Errorf("stage not sealed")
	}
	if st.Verified {
		return true, nil
	}
	cache := newRecipeStageVerifyCache(s, ctx)
	for n := 0; n < budget && st.VerifySequence > 0; n++ {
		if e = ctx.Err(); e != nil {
			return false, e
		}
		c, err := s.readChunk(st.VerifyHash)
		if err != nil {
			return false, err
		}
		if c.Stage != id || c.Sequence != st.VerifySequence || c.End != st.VerifyCursor || c.AfterRoot != st.VerifyRoot || c.AfterJournal != st.VerifyJournal || c.Start > c.End {
			return false, fmt.Errorf("staged chunk chain mismatch")
		}
		before := cache.tree(c.BeforeRoot)
		after := cache.tree(c.AfterRoot)
		journal := cache.tree(c.AfterJournal)
		base := cache.tree(st.Base.Root)
		current := map[recipeStateKey]recipeStateValue{}
		for _, op := range c.Operations {
			if e = ctx.Err(); e != nil {
				return false, e
			}
			v, ok := current[op.Key]
			if !ok {
				v, err = cache.get(before, op.Key)
				if err != nil {
					return false, err
				}
			}
			if !recipeStateEqualValue(v, op.Before) {
				return false, fmt.Errorf("staged operation before-image mismatch")
			}
			current[op.Key] = op.After
		}
		for key, want := range current {
			got, err := cache.get(after, key)
			if err != nil {
				return false, err
			}
			if !recipeStateEqualValue(got, want) {
				return false, fmt.Errorf("staged operation after-image mismatch")
			}
			j, found, err := cache.journalGet(journal, key)
			if err != nil || !found {
				return false, fmt.Errorf("staged journal unavailable: %v", err)
			}
			original, err := cache.get(base, key)
			if err != nil {
				return false, err
			}
			if !recipeStateEqualValue(j.Before, original) || !recipeStateEqualValue(j.After, want) {
				return false, fmt.Errorf("staged journal mismatch")
			}
		}
		st.VerifyHash, st.VerifyRoot, st.VerifyJournal, st.VerifyCursor = c.Prior, c.BeforeRoot, c.BeforeJournal, c.Start
		st.VerifySequence--
	}
	if st.VerifySequence == 0 {
		if st.VerifyHash != "" || st.VerifyRoot != st.Base.Root || st.VerifyJournal != "" || st.VerifyCursor != 0 {
			return false, fmt.Errorf("staged chain does not reach its predecessor")
		}
		st.Verified = true
	}
	if e = ctx.Err(); e != nil {
		return false, e
	}
	if e = s.saveCheckpoint(st); e != nil {
		return false, e
	}
	return st.Verified, nil
}
func recipeStageMakeManifest(st recipeStageCheckpoint) recipeStageManifest {
	return recipeStageManifest{Schema: 1, Identity: st.Identity, Base: st.Base, Height: st.Height, BlockHash: st.BlockHash, Root: st.Root, Journal: st.Journal, SourceCommitment: st.SourceCommitment, LastChunk: st.LastChunk, SourceTotal: st.SourceTotal, Processed: st.Cursor, Complete: st.Sealed && st.Verified && st.Cursor == st.SourceTotal, Chunks: st.Sequence, Touched: st.Touched, Net: st.Net}
}

// The guard must synchronously authenticate the complete source, executor,
// selected block and admission while invoking the callback. This storage layer
// cannot infer whether a producer faithfully evaluated its claimed cursor.
func (s *recipeStageStore) publish(ctx context.Context, id string, guard func(recipeStageManifest, func() error) error) (recipeStageHead, error) {
	var result recipeStageHead
	if ctx == nil || guard == nil {
		return result, fmt.Errorf("missing staged publication authority")
	}
	recipeStageMu.Lock()
	st, e := s.checkpoint(id)
	recipeStageMu.Unlock()
	if e != nil {
		return result, e
	}
	if !st.Sealed || !st.Verified {
		return result, fmt.Errorf("staged manifest is not verified")
	}
	candidate := recipeStageMakeManifest(st)
	e = recipeStoreGuarded(func(f func() error) error { return guard(candidate, f) }, func() error {
		recipeStageMu.Lock()
		defer recipeStageMu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		now, err := s.checkpoint(id)
		if err != nil {
			return err
		}
		if !now.Sealed || !now.Verified || recipeDurableDigest(recipeStageMakeManifest(now)) != recipeDurableDigest(candidate) {
			return errRecipeStateConflict
		}
		head, err := s.head()
		if err != nil {
			return err
		}
		if head != now.Base || head.Revision == ^uint64(0) {
			return errRecipeStateConflict
		}
		data, err := json.Marshal(candidate)
		if err != nil {
			return err
		}
		hash := recipeStoreHash(data)
		if err = recipeStorePutImmutable(filepath.Join(s.dir, "manifests"), hash, data); err != nil {
			return err
		}
		if s.hook != nil {
			if err = s.hook("manifest"); err != nil {
				return err
			}
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		head.Height, head.BlockHash, head.Root, head.Manifest = now.Height, now.BlockHash, now.Root, hash
		head.Revision++
		head.Checksum = recipeStageHeadHash(head)
		if err = s.writeHead(head); err != nil {
			return err
		}
		result = head
		return nil
	})
	return result, e
}
func (s *recipeStageStore) read(ctx context.Context, key recipeStateKey) (recipeStateValue, error) {
	if ctx == nil {
		return recipeStateValue{}, fmt.Errorf("missing context")
	}
	if recipeStageReservedTable(key.Table) {
		return recipeStateValue{}, fmt.Errorf("reserved control state is not an indexed fact")
	}
	recipeStageMu.Lock()
	defer recipeStageMu.Unlock()
	if e := ctx.Err(); e != nil {
		return recipeStateValue{}, e
	}
	head, e := s.head()
	if e != nil {
		return recipeStateValue{}, e
	}
	return recipeStageGet(s.tree(ctx, head.Root), key)
}

// readStaged exposes only this private stage at an exact chunk revision. The
// result is not chain-authorized or published coverage.
func (s *recipeStageStore) readStaged(ctx context.Context, id string, sequence uint64, key recipeStateKey) (recipeStateValue, error) {
	if ctx == nil {
		return recipeStateValue{}, fmt.Errorf("missing context")
	}
	recipeStageMu.Lock()
	defer recipeStageMu.Unlock()
	if err := ctx.Err(); err != nil {
		return recipeStateValue{}, err
	}
	stage, err := s.checkpoint(id)
	if err != nil {
		return recipeStateValue{}, err
	}
	if stage.Sequence != sequence {
		return recipeStateValue{}, errRecipeStateConflict
	}
	return recipeStageGet(s.tree(ctx, stage.Root), key)
}

// Undo swaps to the authenticated predecessor root; the caller must hold chain
// and admission authority. No in-place mutation or raw body is needed.
func (s *recipeStageStore) undo(ctx context.Context, expected recipeStageHead, guard recipeStoreGuard) (recipeStageHead, error) {
	var result recipeStageHead
	if ctx == nil || guard == nil {
		return result, fmt.Errorf("missing undo authority")
	}
	err := recipeStoreGuarded(guard, func() error {
		recipeStageMu.Lock()
		defer recipeStageMu.Unlock()
		if e := ctx.Err(); e != nil {
			return e
		}
		head, e := s.head()
		if e != nil {
			return e
		}
		if head != expected || head.Height < 0 || head.Revision == ^uint64(0) {
			return errRecipeStateConflict
		}
		m, e := s.manifest(head.Manifest)
		if e != nil {
			return e
		}
		prior := m.Base
		if prior.Identity != s.identity || prior.Schema != 1 || prior.Checksum != recipeStageHeadHash(prior) || prior.Height != head.Height-1 {
			return fmt.Errorf("invalid staged predecessor")
		}
		if prior.Height >= 0 {
			pm, e := s.manifest(prior.Manifest)
			if e != nil {
				return e
			}
			if pm.Height != prior.Height || pm.BlockHash != prior.BlockHash || pm.Root != prior.Root {
				return fmt.Errorf("invalid predecessor manifest")
			}
		} else {
			zero := s.initial()
			zero.Revision = prior.Revision
			zero.Checksum = prior.Checksum
			if prior != zero {
				return fmt.Errorf("invalid predecessor origin")
			}
		}
		if prior.Root != "" {
			if _, e = s.tree(ctx, prior.Root).read(prior.Root); e != nil {
				return e
			}
		}
		if s.hook != nil {
			if e = s.hook("undo"); e != nil {
				return e
			}
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		prior.Revision = head.Revision + 1
		prior.Checksum = recipeStageHeadHash(prior)
		if e = s.writeHead(prior); e != nil {
			return e
		}
		result = prior
		return nil
	})
	return result, err
}
