package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
)

var errRecipeStateBudget = errors.New("recipe state transaction resource budget exceeded")
var errRecipeStateConflict = errors.New("recipe state revision or before-image changed")

// Keys are ordered lexicographically by their exact table and key bytes; no
// normalization, display identifier translation or protocol rule is implicit.
type recipeStateKey struct{ Table, Key string }
type recipeStateValue struct {
	Present bool
	Bytes   []byte
}
type recipeStateBefore struct {
	Key   recipeStateKey
	Value recipeStateValue
}
type recipeStateChange struct {
	Key           recipeStateKey
	Before, After recipeStateValue
}
type recipeStateVersion struct {
	Revision   uint64
	Checkpoint string
}
type recipeStateReceipt struct {
	Base         recipeStateVersion
	Committed    recipeStateVersion
	BeforeImages []recipeStateBefore
	Changes      []recipeStateChange // net changes only, in deterministic key order
}
type recipeStateBatch struct {
	BeforeImages []recipeStateBefore
	Changes      []recipeStateChange
}

// A production backend must provide revision-consistent reads and atomically
// compare the revision and every Before value before applying the entire batch.
// It must publish state, evidence, checkpoint and undo in one durable commit.
// Cancellation or error must not publish a partial batch. A no-change batch
// still advances its revision so a completed empty source unit can be recorded.
// This interface alone does not assert that any backend meets these conditions.
type recipeStateBackend interface {
	ReadState(context.Context, recipeStateVersion, recipeStateKey) (recipeStateValue, error)
	ApplyState(context.Context, recipeStateVersion, string, recipeStateBatch) (recipeStateVersion, error)
}

type recipeStateLimits struct {
	MaxTouched    int
	MaxOperations int
	MaxValueBytes int
	MaxBytes      int
}

const (
	recipeStateHardTouched    = 1_000_000
	recipeStateHardOperations = 4_000_000
	recipeStateHardValueBytes = 4_000_000
	recipeStateHardBytes      = 256 << 20
	recipeStateMaxTableBytes  = 256
	recipeStateMaxKeyBytes    = 4096
)

type recipeStateEntry struct{ before, after recipeStateValue }

// A transaction is owned by one worker; the backend serializes publication.
type recipeStateTxn struct {
	backend        recipeStateBackend
	base           recipeStateVersion
	nextCheckpoint string
	limits         recipeStateLimits
	entries        map[recipeStateKey]recipeStateEntry
	bytes          int
	operations     int
	closed         bool
}

func recipeStateValidateLimits(l recipeStateLimits) error {
	if l.MaxTouched <= 0 || l.MaxTouched > recipeStateHardTouched || l.MaxOperations <= 0 || l.MaxOperations > recipeStateHardOperations || l.MaxValueBytes <= 0 || l.MaxValueBytes > recipeStateHardValueBytes || l.MaxBytes <= 0 || l.MaxBytes > recipeStateHardBytes {
		return fmt.Errorf("invalid recipe state limits")
	}
	return nil
}

func recipeStateBegin(backend recipeStateBackend, base recipeStateVersion, nextCheckpoint string, limits recipeStateLimits) (*recipeStateTxn, error) {
	if backend == nil {
		return nil, fmt.Errorf("recipe state backend required")
	}
	if !recipeStateValidCheckpoint(base.Checkpoint) || !recipeStateValidCheckpoint(nextCheckpoint) || base.Checkpoint == nextCheckpoint {
		return nil, fmt.Errorf("distinct recipe state checkpoints required")
	}
	if err := recipeStateValidateLimits(limits); err != nil {
		return nil, err
	}
	return &recipeStateTxn{backend: backend, base: base, nextCheckpoint: nextCheckpoint, limits: limits, entries: map[recipeStateKey]recipeStateEntry{}}, nil
}

func recipeStateValidCheckpoint(checkpoint string) bool {
	return checkpoint != "" && len(checkpoint) <= 1024
}

func recipeStateCheckKey(k recipeStateKey) error {
	if k.Table == "" || len(k.Table) > recipeStateMaxTableBytes || k.Key == "" || len(k.Key) > recipeStateMaxKeyBytes {
		return fmt.Errorf("invalid recipe state key")
	}
	return nil
}
func recipeStateCloneValue(v recipeStateValue) recipeStateValue {
	if !v.Present {
		return recipeStateValue{}
	}
	return recipeStateValue{Present: true, Bytes: append([]byte(nil), v.Bytes...)}
}
func recipeStateEqualValue(a, b recipeStateValue) bool {
	return a.Present == b.Present && (!a.Present || bytes.Equal(a.Bytes, b.Bytes))
}
func recipeStateLess(a, b recipeStateKey) bool {
	if a.Table != b.Table {
		return a.Table < b.Table
	}
	return a.Key < b.Key
}

func (t *recipeStateTxn) operation(ctx context.Context, key recipeStateKey) error {
	if t == nil || t.closed {
		return fmt.Errorf("recipe state transaction is closed")
	}
	if ctx == nil {
		return fmt.Errorf("recipe state context required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := recipeStateCheckKey(key); err != nil {
		return err
	}
	if t.operations >= t.limits.MaxOperations {
		return errRecipeStateBudget
	}
	t.operations++
	return nil
}
func (t *recipeStateTxn) baseValue(ctx context.Context, key recipeStateKey) (recipeStateValue, error) {
	v, err := t.backend.ReadState(ctx, t.base, key)
	if err != nil {
		return recipeStateValue{}, err
	}
	if !v.Present && len(v.Bytes) != 0 {
		return recipeStateValue{}, fmt.Errorf("invalid absent state value")
	}
	if len(v.Bytes) > t.limits.MaxValueBytes {
		return recipeStateValue{}, errRecipeStateBudget
	}
	return recipeStateCloneValue(v), nil
}

// Get distinguishes absent from present empty bytes. Later reads see staged
// writes and deletes; untouched keys are read from the fixed base revision.
func (t *recipeStateTxn) Get(ctx context.Context, key recipeStateKey) (recipeStateValue, error) {
	if err := t.operation(ctx, key); err != nil {
		return recipeStateValue{}, err
	}
	if entry, ok := t.entries[key]; ok {
		return recipeStateCloneValue(entry.after), nil
	}
	return t.baseValue(ctx, key)
}
func (t *recipeStateTxn) Put(ctx context.Context, key recipeStateKey, value []byte) error {
	return t.change(ctx, key, recipeStateValue{Present: true, Bytes: value})
}
func (t *recipeStateTxn) Delete(ctx context.Context, key recipeStateKey) error {
	return t.change(ctx, key, recipeStateValue{})
}
func (t *recipeStateTxn) change(ctx context.Context, key recipeStateKey, after recipeStateValue) error {
	if err := t.operation(ctx, key); err != nil {
		return err
	}
	if len(after.Bytes) > t.limits.MaxValueBytes {
		return errRecipeStateBudget
	}
	entry, exists := t.entries[key]
	if !exists {
		if len(t.entries) >= t.limits.MaxTouched {
			return errRecipeStateBudget
		}
		before, err := t.baseValue(ctx, key)
		if err != nil {
			return err
		}
		entry.before = before
	}
	oldSize := 0
	if exists {
		oldSize = len(key.Table) + len(key.Key) + len(entry.before.Bytes) + len(entry.after.Bytes)
	}
	newSize := len(key.Table) + len(key.Key) + len(entry.before.Bytes) + len(after.Bytes)
	if newSize > t.limits.MaxBytes-(t.bytes-oldSize) {
		return errRecipeStateBudget
	}
	entry.after = recipeStateCloneValue(after)
	t.entries[key] = entry
	t.bytes = t.bytes - oldSize + newSize
	return nil
}

// Preview preserves every first-touch before image, and emits only net changes.
// This is a detached copy: mutating it cannot change the transaction.
func (t *recipeStateTxn) Preview(ctx context.Context) (recipeStateReceipt, error) {
	if t == nil || t.closed || ctx == nil {
		return recipeStateReceipt{}, fmt.Errorf("recipe state transaction unavailable")
	}
	if err := ctx.Err(); err != nil {
		return recipeStateReceipt{}, err
	}
	keys := make([]recipeStateKey, 0, len(t.entries))
	for k := range t.entries {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return recipeStateLess(keys[i], keys[j]) })
	r := recipeStateReceipt{Base: t.base, BeforeImages: make([]recipeStateBefore, 0, len(keys)), Changes: []recipeStateChange{}}
	for _, k := range keys {
		if err := ctx.Err(); err != nil {
			return recipeStateReceipt{}, err
		}
		e := t.entries[k]
		r.BeforeImages = append(r.BeforeImages, recipeStateBefore{Key: k, Value: recipeStateCloneValue(e.before)})
		if !recipeStateEqualValue(e.before, e.after) {
			r.Changes = append(r.Changes, recipeStateChange{Key: k, Before: recipeStateCloneValue(e.before), After: recipeStateCloneValue(e.after)})
		}
	}
	return r, nil
}

func recipeStateCloneChanges(changes []recipeStateChange) []recipeStateChange {
	out := make([]recipeStateChange, 0, len(changes))
	for _, c := range changes {
		out = append(out, recipeStateChange{Key: c.Key, Before: recipeStateCloneValue(c.Before), After: recipeStateCloneValue(c.After)})
	}
	return out
}

func recipeStateCloneBefore(images []recipeStateBefore) []recipeStateBefore {
	out := make([]recipeStateBefore, 0, len(images))
	for _, image := range images {
		out = append(out, recipeStateBefore{Key: image.Key, Value: recipeStateCloneValue(image.Value)})
	}
	return out
}

func (t *recipeStateTxn) Commit(ctx context.Context) (recipeStateReceipt, error) {
	r, err := t.Preview(ctx)
	if err != nil {
		return recipeStateReceipt{}, err
	}
	if err = ctx.Err(); err != nil {
		return recipeStateReceipt{}, err
	}
	batch := recipeStateBatch{BeforeImages: recipeStateCloneBefore(r.BeforeImages), Changes: recipeStateCloneChanges(r.Changes)}
	next, err := t.backend.ApplyState(ctx, t.base, t.nextCheckpoint, batch)
	if err != nil {
		return recipeStateReceipt{}, err
	}
	// A backend violating this contract may already have changed state; the
	// host must treat this error as recovery-required, not as a safe retry.
	if next.Revision <= t.base.Revision || next.Checkpoint != t.nextCheckpoint {
		t.closed = true
		return recipeStateReceipt{}, fmt.Errorf("recipe state backend returned invalid revision; recovery required")
	}
	t.closed = true
	r.Committed = next
	return r, nil
}

func (t *recipeStateTxn) Abort() {
	if t != nil {
		t.closed = true
		t.entries = nil
	}
}

// Undo consumes a previously authenticated receipt. The backend atomically
// checks the current logical checkpoint, physical revision and all after
// values. A host must authenticate the receipt and both checkpoint identities.
func recipeStateUndo(ctx context.Context, backend recipeStateBackend, r recipeStateReceipt, current recipeStateVersion, limits recipeStateLimits) (recipeStateVersion, error) {
	if ctx == nil || backend == nil || r.Committed.Revision <= r.Base.Revision || !recipeStateValidCheckpoint(r.Base.Checkpoint) || !recipeStateValidCheckpoint(r.Committed.Checkpoint) || r.Base.Checkpoint == r.Committed.Checkpoint || current.Checkpoint != r.Committed.Checkpoint || current.Revision < r.Committed.Revision {
		return recipeStateVersion{}, fmt.Errorf("invalid recipe state undo or logical checkpoint")
	}
	if err := recipeStateValidateLimits(limits); err != nil {
		return recipeStateVersion{}, err
	}
	if err := ctx.Err(); err != nil {
		return recipeStateVersion{}, err
	}
	if len(r.BeforeImages) > limits.MaxTouched || len(r.BeforeImages) > limits.MaxOperations || len(r.Changes) > limits.MaxTouched {
		return recipeStateVersion{}, errRecipeStateBudget
	}
	before := make(map[recipeStateKey]recipeStateValue, len(r.BeforeImages))
	used := 0
	for i, b := range r.BeforeImages {
		if err := ctx.Err(); err != nil {
			return recipeStateVersion{}, err
		}
		if err := recipeStateCheckKey(b.Key); err != nil {
			return recipeStateVersion{}, err
		}
		if i > 0 && !recipeStateLess(r.BeforeImages[i-1].Key, b.Key) || !b.Value.Present && len(b.Value.Bytes) != 0 {
			return recipeStateVersion{}, fmt.Errorf("invalid recipe state before-images")
		}
		if len(b.Value.Bytes) > limits.MaxValueBytes {
			return recipeStateVersion{}, errRecipeStateBudget
		}
		used += len(b.Key.Table) + len(b.Key.Key) + len(b.Value.Bytes)
		if used > limits.MaxBytes {
			return recipeStateVersion{}, errRecipeStateBudget
		}
		before[b.Key] = b.Value
	}
	inverse := make([]recipeStateChange, 0, len(r.Changes))
	for i, c := range r.Changes {
		if err := ctx.Err(); err != nil {
			return recipeStateVersion{}, err
		}
		if err := recipeStateCheckKey(c.Key); err != nil {
			return recipeStateVersion{}, err
		}
		original, ok := before[c.Key]
		if !ok || !recipeStateEqualValue(original, c.Before) || recipeStateEqualValue(c.Before, c.After) || i > 0 && !recipeStateLess(r.Changes[i-1].Key, c.Key) || !c.Before.Present && len(c.Before.Bytes) != 0 || !c.After.Present && len(c.After.Bytes) != 0 {
			return recipeStateVersion{}, fmt.Errorf("invalid recipe state net changes")
		}
		if len(c.After.Bytes) > limits.MaxValueBytes {
			return recipeStateVersion{}, errRecipeStateBudget
		}
		if len(c.After.Bytes) > limits.MaxBytes-used {
			return recipeStateVersion{}, errRecipeStateBudget
		}
		used += len(c.After.Bytes)
		inverse = append(inverse, recipeStateChange{Key: c.Key, Before: recipeStateCloneValue(c.After), After: recipeStateCloneValue(c.Before)})
	}
	if err := ctx.Err(); err != nil {
		return recipeStateVersion{}, err
	}
	inverseBefore := recipeStateCloneBefore(r.BeforeImages)
	changed := make(map[recipeStateKey]recipeStateValue, len(r.Changes))
	for _, c := range r.Changes {
		changed[c.Key] = c.After
	}
	for i := range inverseBefore {
		if value, ok := changed[inverseBefore[i].Key]; ok {
			inverseBefore[i].Value = recipeStateCloneValue(value)
		}
	}
	next, err := backend.ApplyState(ctx, current, r.Base.Checkpoint, recipeStateBatch{BeforeImages: inverseBefore, Changes: inverse})
	if err != nil {
		return recipeStateVersion{}, err
	}
	if next.Revision <= current.Revision || next.Checkpoint != r.Base.Checkpoint {
		return recipeStateVersion{}, fmt.Errorf("recipe state backend returned invalid undo revision; recovery required")
	}
	return next, nil
}
