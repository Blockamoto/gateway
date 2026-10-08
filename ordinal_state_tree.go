package main

// Content-addressed radix pages keep ordinal state bounded by the touched
// pages, rather than loading the global UTXO set into a Go map. A block's roots
// become visible only through its existing atomic index head. Interrupted page
// writes are unreachable and cannot advance a snapshot.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const ordinalPageEntries = 64
const ordinalPageMaxBytes = 8 << 20

type ordinalKV struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}
type ordinalPage struct {
	Entries  []ordinalKV `json:"entries,omitempty"`
	Children []string    `json:"children,omitempty"`
}
type ordinalTree struct {
	ctx              context.Context
	dir              string
	root             string
	changes          map[string]json.RawMessage
	cache            map[string]ordinalPage
	dirtyDirectories map[string]bool
}

func newOrdinalTree(dir, root string) *ordinalTree {
	return &ordinalTree{ctx: context.Background(), dir: dir, root: root, changes: map[string]json.RawMessage{}, cache: map[string]ordinalPage{}, dirtyDirectories: map[string]bool{}}
}
func (t *ordinalTree) withContext(ctx context.Context) *ordinalTree {
	if ctx != nil {
		t.ctx = ctx
	}
	return t
}
func (t *ordinalTree) pagePath(hash string) string {
	return filepath.Join(t.dir, hash[:2], hash+".json")
}
func ordinalKeyValid(key string) bool {
	if len(key) == 0 || len(key) > 128 {
		return false
	}
	for _, c := range key {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (t *ordinalTree) read(hash string) (ordinalPage, error) {
	if e := t.ctx.Err(); e != nil {
		return ordinalPage{}, e
	}
	if hash == "" {
		return ordinalPage{}, nil
	}
	if !validHash(hash) {
		return ordinalPage{}, fmt.Errorf("invalid ordinal state page reference")
	}
	if p, ok := t.cache[hash]; ok {
		return p, nil
	}
	path := t.pagePath(hash)
	f, e := os.Open(path)
	if e != nil {
		return ordinalPage{}, fmt.Errorf("ordinal state page unavailable (resume or rebuild required): %w", e)
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || info.Size() > ordinalPageMaxBytes {
		return ordinalPage{}, fmt.Errorf("ordinal state page exceeds budget")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return ordinalPage{}, e
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != hash {
		return ordinalPage{}, fmt.Errorf("ordinal state page checksum mismatch")
	}
	var p ordinalPage
	if e = json.Unmarshal(b, &p); e != nil {
		return p, e
	}
	if len(p.Children) > 0 {
		if len(p.Children) != 16 || len(p.Entries) != 0 {
			return p, fmt.Errorf("invalid ordinal branch")
		}
		for _, child := range p.Children {
			if child != "" && !validHash(child) {
				return p, fmt.Errorf("invalid ordinal child")
			}
		}
	} else {
		if len(p.Entries) > ordinalPageEntries {
			return p, fmt.Errorf("ordinal leaf exceeds entry budget")
		}
		for i, kv := range p.Entries {
			if !ordinalKeyValid(kv.Key) || len(kv.Value) == 0 || !json.Valid(kv.Value) || i > 0 && p.Entries[i-1].Key >= kv.Key {
				return p, fmt.Errorf("invalid ordinal leaf")
			}
		}
	}
	if len(t.cache) >= 512 {
		t.cache = map[string]ordinalPage{}
	}
	t.cache[hash] = p
	return p, nil
}
func (t *ordinalTree) write(p ordinalPage) (string, error) {
	if e := t.ctx.Err(); e != nil {
		return "", e
	}
	if len(p.Children) == 0 && len(p.Entries) == 0 {
		return "", nil
	}
	b, e := json.Marshal(p)
	if e != nil {
		return "", e
	}
	if len(b) > ordinalPageMaxBytes {
		return "", fmt.Errorf("ordinal page byte budget exceeded")
	}
	sum := sha256.Sum256(b)
	hash := hex.EncodeToString(sum[:])
	path := t.pagePath(hash)
	if _, e = os.Stat(path); os.IsNotExist(e) {
		if e = atomicWriteBytes(path, b); e != nil {
			return "", e
		}
		t.dirtyDirectories[filepath.Dir(path)] = true
	} else if e != nil {
		return "", e
	} else if _, e = t.read(hash); e != nil {
		return "", e
	}
	// Existence is not a durability receipt: an earlier interrupted flush may
	// have written this exact immutable page without syncing its directory.
	t.dirtyDirectories[filepath.Dir(path)] = true
	return hash, nil
}

func (t *ordinalTree) walk(visit func(ordinalKV) error) error {
	var walk func(string, int) error
	walk = func(hash string, depth int) error {
		if hash == "" {
			return nil
		}
		if depth > 128 {
			return fmt.Errorf("ordinal walk depth exceeded")
		}
		page, e := t.read(hash)
		if e != nil {
			return e
		}
		for _, kv := range page.Entries {
			if e = visit(kv); e != nil {
				return e
			}
		}
		for _, child := range page.Children {
			if e = walk(child, depth+1); e != nil {
				return e
			}
		}
		return nil
	}
	return walk(t.root, 0)
}
func ordinalNibble(key string, depth int) int {
	c := key[depth]
	if c <= '9' {
		return int(c - '0')
	}
	return int(c-'a') + 10
}
func (t *ordinalTree) get(key string, target any) (bool, error) {
	if !ordinalKeyValid(key) {
		return false, fmt.Errorf("invalid ordinal key")
	}
	if value, ok := t.changes[key]; ok {
		if value == nil {
			return false, nil
		}
		return true, json.Unmarshal(value, target)
	}
	hash := t.root
	for depth := 0; hash != ""; depth++ {
		p, e := t.read(hash)
		if e != nil {
			return false, e
		}
		if len(p.Children) == 0 {
			i := sort.Search(len(p.Entries), func(i int) bool { return p.Entries[i].Key >= key })
			if i == len(p.Entries) || p.Entries[i].Key != key {
				return false, nil
			}
			return true, json.Unmarshal(p.Entries[i].Value, target)
		}
		if depth >= len(key) {
			return false, fmt.Errorf("ordinal tree key length mismatch")
		}
		hash = p.Children[ordinalNibble(key, depth)]
	}
	return false, nil
}
func (t *ordinalTree) put(key string, value any) error {
	if e := t.ctx.Err(); e != nil {
		return e
	}
	if !ordinalKeyValid(key) {
		return fmt.Errorf("invalid ordinal key")
	}
	b, e := json.Marshal(value)
	if e == nil {
		t.changes[key] = b
	}
	return e
}
func (t *ordinalTree) remove(key string) { t.changes[key] = nil }
func (t *ordinalTree) flush() (string, error) {
	if e := t.ctx.Err(); e != nil {
		return "", e
	}
	keys := make([]string, 0, len(t.changes))
	for key := range t.changes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	root, e := t.update(t.root, 0, keys)
	if e != nil {
		return "", e
	}
	// Flush each changed directory once for the entire transaction. The root
	// is not publishable until these new page entries and ancestors are durable.
	for dir := range t.dirtyDirectories {
		if e = syncDirectory(dir); e != nil {
			return "", e
		}
	}
	if len(t.dirtyDirectories) > 0 {
		for dir, n := t.dir, 0; n < 4; n++ {
			if e = syncDirectory(dir); e != nil {
				return "", e
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	t.dirtyDirectories = map[string]bool{}
	t.root = root
	t.changes = map[string]json.RawMessage{}
	return root, nil
}
func (t *ordinalTree) update(hash string, depth int, keys []string) (string, error) {
	if len(keys) == 0 {
		return hash, nil
	}
	p, e := t.read(hash)
	if e != nil {
		return "", e
	}
	if len(p.Children) == 0 {
		entries := map[string]json.RawMessage{}
		for _, kv := range p.Entries {
			entries[kv.Key] = kv.Value
		}
		for _, key := range keys {
			if t.changes[key] == nil {
				delete(entries, key)
			} else {
				entries[key] = t.changes[key]
			}
		}
		ordered := make([]string, 0, len(entries))
		for key := range entries {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		return t.build(depth, ordered, entries)
	}
	children := append([]string(nil), p.Children...)
	for start := 0; start < len(keys); {
		if depth >= len(keys[start]) {
			return "", fmt.Errorf("ordinal key depth exhausted")
		}
		branch := ordinalNibble(keys[start], depth)
		end := start + 1
		for end < len(keys) && depth < len(keys[end]) && ordinalNibble(keys[end], depth) == branch {
			end++
		}
		children[branch], e = t.update(children[branch], depth+1, keys[start:end])
		if e != nil {
			return "", e
		}
		start = end
	}
	for _, child := range children {
		if child != "" {
			return t.write(ordinalPage{Children: children})
		}
	}
	return "", nil
}
func (t *ordinalTree) build(depth int, keys []string, values map[string]json.RawMessage) (string, error) {
	if e := t.ctx.Err(); e != nil {
		return "", e
	}
	if len(keys) <= ordinalPageEntries {
		p := ordinalPage{}
		for _, key := range keys {
			p.Entries = append(p.Entries, ordinalKV{key, values[key]})
		}
		return t.write(p)
	}
	p := ordinalPage{Children: make([]string, 16)}
	for start := 0; start < len(keys); {
		if depth >= len(keys[start]) {
			return "", fmt.Errorf("ordinal key collision")
		}
		branch := ordinalNibble(keys[start], depth)
		end := start + 1
		for end < len(keys) && depth < len(keys[end]) && ordinalNibble(keys[end], depth) == branch {
			end++
		}
		child, e := t.build(depth+1, keys[start:end], values)
		if e != nil {
			return "", e
		}
		p.Children[branch] = child
		start = end
	}
	return t.write(p)
}

// predecessor is a bounded-height radix descent, not a scan of the UTXO set.
// It is used only on an immutable, committed numeric sat-range tree.
func (t *ordinalTree) predecessor(key string, target any) (bool, error) {
	if !ordinalKeyValid(key) {
		return false, fmt.Errorf("invalid ordinal key")
	}
	var find func(string, string) (*ordinalKV, error)
	find = func(hash, prefix string) (*ordinalKV, error) {
		if hash == "" {
			return nil, nil
		}
		p, e := t.read(hash)
		if e != nil {
			return nil, e
		}
		if len(p.Children) == 0 {
			i := sort.Search(len(p.Entries), func(i int) bool { return p.Entries[i].Key > key }) - 1
			if i < 0 {
				return nil, nil
			}
			kv := p.Entries[i]
			return &kv, nil
		}
		if len(prefix) >= len(key) {
			return nil, fmt.Errorf("ordinal predecessor depth mismatch")
		}
		for i := 15; i >= 0; i-- {
			next := prefix + "0123456789abcdef"[i:i+1]
			if next > key[:len(next)] {
				continue
			}
			kv, e := find(p.Children[i], next)
			if e != nil || kv != nil {
				return kv, e
			}
		}
		return nil, nil
	}
	kv, e := find(t.root, "")
	if e != nil || kv == nil {
		return false, e
	}
	return true, json.Unmarshal(kv.Value, target)
}

// Only roots selected by a committed snapshot or explicit retained history may
// be supplied. GC is optional maintenance; failure never alters selected state.
var ordinalMaintenanceMu sync.Mutex
var ordinalGCProcessOwner = fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
var errOrdinalMaintenancePending = errors.New("ordinal cleanup mark pass has more incremental work")

type ordinalGCGeneration struct {
	Next    int64  `json:"next"`
	Pending bool   `json:"pending"`
	Owner   string `json:"owner"`
}

func collectOrdinalPages(dir string, roots []string) error {
	for {
		e := collectOrdinalPagesContext(context.Background(), dir, func(visit func(string) error) error {
			for _, root := range roots {
				if e := visit(root); e != nil {
					return e
				}
			}
			return nil
		})
		if !errors.Is(e, errOrdinalMaintenancePending) {
			return e
		}
	}
}

// Private page mtimes are exact mark generations, not user data or integrity
// receipts. This avoids retaining a map with millions of live page hashes.
// A durable increasing generation is reserved before marking. Cancellation or
// any incomplete traversal returns before sweep; the next pass uses a new
// generation. Directory enumeration is streamed in fixed-size chunks.
// The sole index writer calls this outside the global locator publication lock.
func collectOrdinalPagesContext(ctx context.Context, dir string, roots func(func(string) error) error) error {
	if !ordinalMaintenanceMu.TryLock() {
		return nil
	}
	defer ordinalMaintenanceMu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	var generation ordinalGCGeneration
	path := filepath.Join(dir, "gc-generation.json")
	data, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if e == nil && json.Unmarshal(data, &generation) != nil {
		return fmt.Errorf("invalid ordinal maintenance generation")
	}
	if !generation.Pending || generation.Owner != ordinalGCProcessOwner {
		generation.Next++
	}
	generation.Pending = true
	generation.Owner = ordinalGCProcessOwner
	if generation.Next <= 0 || generation.Next > 315360000 {
		return fmt.Errorf("ordinal maintenance generation exhausted")
	}
	if e = atomicWriteJSON(path, generation); e != nil {
		return e
	}
	if e = syncDirectory(dir); e != nil {
		return e
	}
	mark := time.Unix(946684800+generation.Next, 0)
	tree := newOrdinalTree(dir, "")
	workSpent := time.Duration(0)
	markRoot := func(root string) error {
		type frame struct {
			hash string
			exit bool
		}
		stack := []frame{{hash: root}}
		for len(stack) > 0 {
			if e := ctx.Err(); e != nil {
				return e
			}
			current := stack[len(stack)-1]
			hash := current.hash
			stack = stack[:len(stack)-1]
			if hash == "" {
				continue
			}
			if !validHash(hash) {
				return fmt.Errorf("invalid ordinal cleanup root")
			}
			name := tree.pagePath(hash)
			info, e := os.Stat(name)
			if e != nil {
				return e
			}
			if info.ModTime().Equal(mark) {
				continue
			}
			// Completed root/receipt enumeration is cancellable, but must not
			// consume the marking allowance or a long archive could starve
			// every later unfinished root forever.
			if workSpent >= 100*time.Millisecond {
				return errOrdinalMaintenancePending
			}
			started := time.Now()
			if current.exit {
				if e = os.Chtimes(name, mark, mark); e != nil {
					return e
				}
			} else {
				page, e := tree.read(hash)
				if e != nil {
					return e
				}
				// Mark a branch only after every descendant is marked. Thus a
				// bounded pass may discard its frontier safely: the next pass
				// restarts at roots and skips only fully completed subtrees.
				children := append([]string(nil), page.Children...)
				for _, kv := range page.Entries {
					var ref struct {
						RangesRoot string `json:"ranges_root"`
					}
					if json.Unmarshal(kv.Value, &ref) == nil && ref.RangesRoot != "" {
						if !validHash(ref.RangesRoot) {
							return fmt.Errorf("invalid segmented sat range root")
						}
						children = append(children, ref.RangesRoot)
					}
				}
				if len(children) == 0 {
					if e = os.Chtimes(name, mark, mark); e != nil {
						return e
					}
				} else {
					stack = append(stack, frame{hash: hash, exit: true})
					for _, child := range children {
						stack = append(stack, frame{hash: child})
					}
				}
			}
			workSpent += time.Since(started)
			// A radix DFS has at most 15 sibling references per key digit.
			if len(stack) > 4096 {
				return fmt.Errorf("ordinal cleanup depth budget exceeded")
			}
		}
		return nil
	}
	if e = roots(markRoot); e != nil {
		return e
	}
	shards, e := os.ReadDir(dir)
	if e != nil {
		return e
	}
	for _, shard := range shards {
		if !shard.IsDir() || len(shard.Name()) != 2 || !ordinalKeyValid(shard.Name()) {
			continue
		}
		folder := filepath.Join(dir, shard.Name())
		f, e := os.Open(folder)
		if e != nil {
			return e
		}
		for {
			files, readErr := f.ReadDir(128)
			for _, file := range files {
				if e = ctx.Err(); e != nil {
					f.Close()
					return e
				}
				hash := strings.TrimSuffix(file.Name(), ".json")
				if file.IsDir() || !validHash(hash) || file.Name() != hash+".json" {
					continue
				}
				info, e := file.Info()
				if e != nil {
					f.Close()
					return e
				}
				if !info.ModTime().Equal(mark) {
					if e = os.Remove(filepath.Join(folder, file.Name())); e != nil {
						f.Close()
						return e
					}
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				f.Close()
				return readErr
			}
		}
		f.Close()
	}
	generation.Pending = false
	return atomicWriteJSON(path, generation)
}

func ordinalCollectionPending(dir string) bool {
	data, e := os.ReadFile(filepath.Join(dir, "gc-generation.json"))
	if e != nil {
		return false
	}
	var generation ordinalGCGeneration
	return json.Unmarshal(data, &generation) == nil && generation.Pending
}
