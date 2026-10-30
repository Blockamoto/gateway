package main

import (
	"./internal/updateapply"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
)

const recipeTablesInlineBytes = 32 << 10
const recipeTablesValueBytes = 16 << 20
const recipeTablesEncodedBytes = 32 << 20

// Small values stay in native COW leaves, avoiding a synced file per counter or
// ordinary row. Large values use independently checked immutable blobs. The
// inline threshold also bounds paired before/after journal leaves below the
// native page codec's maximum, even with maximally escaped logical keys.
type recipeTablesEntry struct {
	Table, Key string
	Inline     json.RawMessage `json:",omitempty"`
	Blob       string          `json:",omitempty"`
	Bytes      uint64
}
type recipeTablesDelta struct {
	Before, After *recipeTablesEntry
}

type recipeTablesBlobPin struct{ References, Bytes uint64 }

func recipeTablesKey(key recipeStateKey) string {
	prefix := recipeStoreHash([]byte(key.Table))[:4]
	return prefix + recipeDurableKey(key)
}
func recipeTablesBlobPath(root, hash string) string {
	return filepath.Join(root, "values", hash[:2], hash+".json")
}

func recipeTablesTree(ctx context.Context, dir, root string) *ordinalTree {
	tree := newOrdinalTree(dir, root).withContext(ctx)
	tree.checkPath = updateapply.CheckPath
	return tree
}

func recipeTablesCheckEntry(physical string, e recipeTablesEntry) error {
	key := recipeStateKey{Table: e.Table, Key: e.Key}
	if recipeStateCheckKey(key) != nil || physical != recipeTablesKey(key) || e.Bytes == 0 || e.Bytes > recipeTablesEncodedBytes || (e.Blob == "") == (len(e.Inline) == 0) {
		return fmt.Errorf("invalid typed table entry")
	}
	if e.Blob != "" {
		if !validHash(e.Blob) || e.Bytes <= recipeTablesInlineBytes {
			return fmt.Errorf("invalid table blob reference")
		}
	} else if uint64(len(e.Inline)) != e.Bytes || len(e.Inline) > recipeTablesInlineBytes {
		return fmt.Errorf("invalid inline table value")
	}
	return nil
}

func recipeTablesReadValue(ctx context.Context, root string, e recipeTablesEntry) (recipeOrderedValue, error) {
	if err := ctx.Err(); err != nil {
		return recipeOrderedValue{}, err
	}
	raw := e.Inline
	if e.Blob != "" {
		var err error
		raw, err = recipeStoreReadBounded(recipeTablesBlobPath(root, e.Blob), recipeTablesEncodedBytes)
		if err != nil {
			return recipeOrderedValue{}, err
		}
		if uint64(len(raw)) != e.Bytes || recipeStoreHash(raw) != e.Blob {
			return recipeOrderedValue{}, fmt.Errorf("typed table value checksum changed")
		}
	}
	if err := recipeValidateJSON(raw); err != nil {
		return recipeOrderedValue{}, err
	}
	var value recipeOrderedValue
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, err
	}
	if value.Kind == "missing" {
		return value, fmt.Errorf("stored table value cannot be missing")
	}
	return value, ctx.Err()
}

type recipeTablesEdit struct {
	ctx       context.Context
	root      string
	tree      *ordinalTree
	pins      *ordinalTree
	pinRoot   string
	tables    map[string]bool
	before    map[string]*recipeTablesEntry
	after     map[string]*recipeTablesEntry
	pending   map[string][]byte
	values    map[string]recipeOrderedValue
	ops, used int
	closed    bool
}

func newRecipeTablesEdit(ctx context.Context, root, stateRoot string, tables []recipeTable, pinRoots ...string) *recipeTablesEdit {
	declared := map[string]bool{}
	for _, t := range tables {
		declared[t.Name] = true
	}
	rootPins := ""
	if len(pinRoots) == 1 {
		rootPins = pinRoots[0]
	}
	return &recipeTablesEdit{ctx: ctx, root: root, tree: recipeTablesTree(ctx, filepath.Join(root, "pages"), stateRoot), pins: recipeTablesTree(ctx, filepath.Join(root, "pages"), rootPins), pinRoot: rootPins, tables: declared, before: map[string]*recipeTablesEntry{}, after: map[string]*recipeTablesEntry{}, pending: map[string][]byte{}, values: map[string]recipeOrderedValue{}}
}

func (e *recipeTablesEdit) changePin(entry *recipeTablesEntry, add bool) error {
	if entry == nil || entry.Blob == "" {
		return nil
	}
	var pin recipeTablesBlobPin
	have, err := e.pins.get(entry.Blob, &pin)
	if err != nil {
		return err
	}
	if have && (pin.References == 0 || pin.Bytes != entry.Bytes) {
		return fmt.Errorf("invalid table blob pin")
	}
	if add {
		if pin.References == ^uint64(0) {
			return fmt.Errorf("table blob reference overflow")
		}
		pin.References++
		pin.Bytes = entry.Bytes
		return e.pins.put(entry.Blob, pin)
	}
	if !have || pin.References == 0 {
		return fmt.Errorf("table blob reference missing")
	}
	if pin.References == 1 {
		e.pins.remove(entry.Blob)
		return nil
	}
	pin.References--
	return e.pins.put(entry.Blob, pin)
}
func (e *recipeTablesEdit) AbortOrdered() { e.closed = true }
func (e *recipeTablesEdit) check(ctx context.Context, key recipeStateKey) error {
	if e.closed || !e.tables[key.Table] {
		return fmt.Errorf("typed table transaction unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := recipeStateCheckKey(key); err != nil {
		return err
	}
	e.ops++
	if e.ops > recipeStateHardOperations {
		return errRecipeStateBudget
	}
	return nil
}
func (e *recipeTablesEdit) entry(key recipeStateKey) (*recipeTablesEntry, error) {
	physical := recipeTablesKey(key)
	if v, have := e.after[physical]; have {
		return v, nil
	}
	var value recipeTablesEntry
	have, err := e.tree.get(physical, &value)
	if err != nil || !have {
		return nil, err
	}
	if err = recipeTablesCheckEntry(physical, value); err != nil {
		return nil, err
	}
	if value.Table != key.Table || value.Key != key.Key {
		return nil, fmt.Errorf("typed table key collision")
	}
	return &value, nil
}
func (e *recipeTablesEdit) ReadOrdered(ctx context.Context, key recipeStateKey) (recipeOrderedValue, error) {
	if err := e.check(ctx, key); err != nil {
		return recipeOrderedValue{}, err
	}
	entry, err := e.entry(key)
	if err != nil {
		return recipeOrderedValue{}, err
	}
	if entry == nil {
		return recipeOrderedValue{Kind: "missing"}, nil
	}
	physical := recipeTablesKey(key)
	if value, have := e.values[physical]; have {
		return value, nil
	}
	value, err := recipeTablesReadValue(ctx, e.root, *entry)
	if err != nil {
		return value, err
	}
	if err = e.spend(int(entry.Bytes) + 128 + len(key.Table) + len(key.Key)); err != nil {
		return recipeOrderedValue{}, err
	}
	e.values[physical] = value
	return value, nil
}
func (e *recipeTablesEdit) spend(n int) error {
	if n < 0 || n > recipeStateHardBytes-e.used {
		return errRecipeStateBudget
	}
	e.used += n
	return nil
}
func (e *recipeTablesEdit) remember(key recipeStateKey) error {
	physical := recipeTablesKey(key)
	if _, have := e.before[physical]; have {
		return nil
	}
	if len(e.before) >= recipeStateHardTouched {
		return errRecipeStateBudget
	}
	v, err := e.entry(key)
	if err != nil {
		return err
	}
	if err = e.spend(256 + len(key.Table) + len(key.Key)); err != nil {
		return err
	}
	e.before[physical] = v
	return nil
}
func (e *recipeTablesEdit) PutOrdered(ctx context.Context, key recipeStateKey, value recipeOrderedValue) error {
	if err := e.check(ctx, key); err != nil {
		return err
	}
	if err := e.remember(key); err != nil {
		return err
	}
	remaining := recipeTablesValueBytes
	if err := recipeResumeValueBudget(value, 0, &remaining); err != nil {
		return err
	}
	if value.Kind == "missing" {
		return fmt.Errorf("cannot store missing typed table value")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(raw) > recipeTablesEncodedBytes {
		return errRecipeStateBudget
	}
	entry := &recipeTablesEntry{Table: key.Table, Key: key.Key, Bytes: uint64(len(raw))}
	if len(raw) <= recipeTablesInlineBytes {
		entry.Inline = raw
	} else {
		entry.Blob = recipeStoreHash(raw)
		if _, have := e.pending[entry.Blob]; !have {
			if err = e.spend(len(raw)); err != nil {
				return err
			}
			e.pending[entry.Blob] = raw
		}
	}
	if err = e.spend(len(raw) + 128); err != nil {
		return err
	}
	physical := recipeTablesKey(key)
	e.after[physical] = entry
	e.values[physical] = value
	return e.tree.put(physical, *entry)
}
func (e *recipeTablesEdit) DeleteOrdered(ctx context.Context, key recipeStateKey) error {
	if err := e.check(ctx, key); err != nil {
		return err
	}
	if err := e.remember(key); err != nil {
		return err
	}
	physical := recipeTablesKey(key)
	e.after[physical] = nil
	delete(e.values, physical)
	e.tree.remove(physical)
	return nil
}

// Final net deltas contain compact value references, never a second full-index
// map or whole large-value journal. All native COW mutations flush together.
func (e *recipeTablesEdit) flush(entries uint64) (root, journal string, count, nextEntries uint64, err error) {
	if e.closed {
		return "", "", 0, 0, fmt.Errorf("table transaction closed")
	}
	if err = e.ctx.Err(); err != nil {
		return
	}
	journalTree := recipeTablesTree(e.ctx, filepath.Join(e.root, "pages"), "")
	keys := make([]string, 0, len(e.before))
	for key := range e.before {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	nextEntries = entries
	for _, key := range keys {
		if err = e.ctx.Err(); err != nil {
			return
		}
		before, after := e.before[key], e.after[key]
		if indexDigest(before) == indexDigest(after) {
			continue
		}
		if before == nil && after != nil {
			if nextEntries == ^uint64(0) {
				err = fmt.Errorf("table entry count overflow")
				return
			}
			nextEntries++
		}
		if before != nil && after == nil {
			if nextEntries == 0 {
				err = fmt.Errorf("table entry count underflow")
				return
			}
			nextEntries--
		}
		if after != nil && after.Blob != "" {
			raw, have := e.pending[after.Blob]
			if !have {
				err = fmt.Errorf("staged table value missing")
				return
			}
			if err = recipeStorePutImmutable(filepath.Dir(recipeTablesBlobPath(e.root, after.Blob)), after.Blob, raw); err != nil {
				return
			}
		}
		if before == nil || after == nil || before.Blob != after.Blob {
			if err = e.changePin(before, false); err != nil {
				return
			}
			if err = e.changePin(after, true); err != nil {
				return
			}
		}
		if err = journalTree.put(key, recipeTablesDelta{Before: before, After: after}); err != nil {
			return
		}
		count++
	}
	if root, err = e.tree.flush(); err != nil {
		return
	}
	if journal, err = journalTree.flush(); err != nil {
		return
	}
	e.pinRoot, err = e.pins.flush()
	return
}
