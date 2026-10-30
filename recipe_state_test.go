package main

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
)

// A test adapter only. Real durable backends must additionally atomically bind
// selected-chain/dependency evidence, checkpoints, coverage and undo records.
type recipeStateMemoryTest struct {
	mu      sync.Mutex
	version recipeStateVersion
	values  map[recipeStateKey]recipeStateValue
}

func (m *recipeStateMemoryTest) ReadState(ctx context.Context, rev recipeStateVersion, key recipeStateKey) (recipeStateValue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return recipeStateValue{}, err
	}
	if m.version != rev {
		return recipeStateValue{}, errRecipeStateConflict
	}
	return recipeStateCloneValue(m.values[key]), nil
}
func (m *recipeStateMemoryTest) ApplyState(ctx context.Context, rev recipeStateVersion, checkpoint string, batch recipeStateBatch) (recipeStateVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return recipeStateVersion{}, err
	}
	if m.version != rev {
		return recipeStateVersion{}, errRecipeStateConflict
	}
	for _, image := range batch.BeforeImages {
		if !recipeStateEqualValue(m.values[image.Key], image.Value) {
			return recipeStateVersion{}, errRecipeStateConflict
		}
	}
	for _, c := range batch.Changes {
		if !recipeStateEqualValue(m.values[c.Key], c.Before) {
			return recipeStateVersion{}, errRecipeStateConflict
		}
	}
	if err := ctx.Err(); err != nil {
		return recipeStateVersion{}, err
	}
	for _, c := range batch.Changes {
		if c.After.Present {
			m.values[c.Key] = recipeStateCloneValue(c.After)
		} else {
			delete(m.values, c.Key)
		}
	}
	m.version = recipeStateVersion{Revision: m.version.Revision + 1, Checkpoint: checkpoint}
	return m.version, nil
}
func recipeStateTestLimits() recipeStateLimits {
	return recipeStateLimits{MaxTouched: 100, MaxOperations: 500, MaxValueBytes: 1024, MaxBytes: 4096}
}
func recipeStateTestKey(table, key string) recipeStateKey {
	return recipeStateKey{Table: table, Key: key}
}
func recipeStateTestValue(s string) recipeStateValue {
	return recipeStateValue{Present: true, Bytes: []byte(s)}
}

func TestRecipeStateOrderedBeforeImagesNetAndUndo(t *testing.T) {
	ctx := context.Background()
	live, replacement, transient := recipeStateTestKey("live", "a"), recipeStateTestKey("live", "b"), recipeStateTestKey("live", "c")
	other := recipeStateTestKey("bridge", "z")
	m := &recipeStateMemoryTest{version: recipeStateVersion{Revision: 3, Checkpoint: "origin"}, values: map[recipeStateKey]recipeStateValue{live: recipeStateTestValue("original"), replacement: recipeStateTestValue("old"), other: recipeStateTestValue("bridge")}}
	tx, err := recipeStateBegin(m, m.version, "block-1", recipeStateTestLimits())
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("first")
	if err = tx.Put(ctx, live, input); err != nil {
		t.Fatal(err)
	}
	input[0] = 'X'
	v, err := tx.Get(ctx, live)
	if err != nil || string(v.Bytes) != "first" {
		t.Fatalf("read-your-writes: %+v %v", v, err)
	}
	v.Bytes[0] = 'X'
	v, _ = tx.Get(ctx, live)
	if string(v.Bytes) != "first" {
		t.Fatal("read aliases staged state")
	}
	if err = tx.Delete(ctx, live); err != nil {
		t.Fatal(err)
	}
	if err = tx.Put(ctx, live, []byte("final")); err != nil {
		t.Fatal(err)
	}
	if err = tx.Put(ctx, transient, []byte("created")); err != nil {
		t.Fatal(err)
	}
	if err = tx.Delete(ctx, transient); err != nil {
		t.Fatal(err)
	}
	if err = tx.Put(ctx, replacement, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if err = tx.Delete(ctx, other); err != nil {
		t.Fatal(err)
	}
	preview, err := tx.Preview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.BeforeImages) != 4 || len(preview.Changes) != 3 || preview.BeforeImages[0].Key != other || preview.BeforeImages[1].Key != live || preview.BeforeImages[2].Key != replacement || preview.BeforeImages[3].Key != transient {
		t.Fatalf("deterministic first-touch order: %+v", preview)
	}
	if string(preview.BeforeImages[1].Value.Bytes) != "original" || string(preview.Changes[2].Before.Bytes) != "old" || string(preview.Changes[2].After.Bytes) != "new" || preview.BeforeImages[3].Value.Present {
		t.Fatal("first image or net replacement lost", preview)
	}
	preview.BeforeImages[1].Value.Bytes[0] = 'X'
	receipt, err := tx.Commit(ctx)
	if err != nil || receipt.Base.Revision != 3 || receipt.Committed.Revision != 4 {
		t.Fatalf("commit: %+v %v", receipt, err)
	}
	if string(receipt.BeforeImages[1].Value.Bytes) != "original" || string(m.values[live].Bytes) != "final" || string(m.values[replacement].Bytes) != "new" || m.values[other].Present || m.values[transient].Present {
		t.Fatal("wrong committed state or preview alias")
	}
	if _, err = tx.Commit(ctx); err == nil {
		t.Fatal("committed twice")
	}
	if next, err := recipeStateUndo(ctx, m, receipt, m.version, recipeStateTestLimits()); err != nil || next.Revision != 5 || next.Checkpoint != "origin" {
		t.Fatalf("undo: %+v %v", next, err)
	}
	if string(m.values[live].Bytes) != "original" || string(m.values[replacement].Bytes) != "old" || string(m.values[other].Bytes) != "bridge" || m.values[transient].Present {
		t.Fatal("undo failed to restore before images")
	}
	if _, err = recipeStateUndo(ctx, m, receipt, m.version, recipeStateTestLimits()); err == nil {
		t.Fatal("replayed undo was accepted", err)
	}
}

func TestRecipeStateEmptyAbsentAndConcurrentConflict(t *testing.T) {
	ctx := context.Background()
	m := &recipeStateMemoryTest{version: recipeStateVersion{Checkpoint: "origin"}, values: map[recipeStateKey]recipeStateValue{}}
	key := recipeStateTestKey("live", "empty")
	one, _ := recipeStateBegin(m, m.version, "block-1", recipeStateTestLimits())
	two, _ := recipeStateBegin(m, m.version, "block-1", recipeStateTestLimits())
	if err := one.Put(ctx, key, nil); err != nil {
		t.Fatal(err)
	}
	v, err := one.Get(ctx, key)
	if err != nil || !v.Present || len(v.Bytes) != 0 {
		t.Fatal("present empty became absent", v, err)
	}
	r, err := one.Commit(ctx)
	if err != nil || len(r.Changes) != 1 || !r.Changes[0].After.Present {
		t.Fatal("empty value lost", r, err)
	}
	if err = two.Put(ctx, recipeStateTestKey("live", "other"), []byte("x")); !errors.Is(err, errRecipeStateConflict) {
		t.Fatal("stale read accepted", err)
	}
	if _, err = two.Commit(ctx); !errors.Is(err, errRecipeStateConflict) {
		t.Fatal("stale empty transaction committed", err)
	}
	three, _ := recipeStateBegin(m, m.version, "block-2", recipeStateTestLimits())
	if err = three.Put(ctx, key, []byte{}); err != nil {
		t.Fatal(err)
	}
	r, err = three.Commit(ctx)
	if err != nil || len(r.BeforeImages) != 1 || len(r.Changes) != 0 || r.Committed.Revision != 2 {
		t.Fatal("empty net receipt", r, err)
	}
	if rev, err := recipeStateUndo(ctx, m, r, m.version, recipeStateTestLimits()); err != nil || rev.Revision != 3 || rev.Checkpoint != "block-1" || !m.values[key].Present {
		t.Fatal("empty undo", rev, err)
	}
}

func TestRecipeStateBudgetAbortCancellationAndUndoValidation(t *testing.T) {
	ctx := context.Background()
	m := &recipeStateMemoryTest{version: recipeStateVersion{Checkpoint: "origin"}, values: map[recipeStateKey]recipeStateValue{}}
	limits := recipeStateLimits{MaxTouched: 1, MaxOperations: 20, MaxValueBytes: 4, MaxBytes: 12}
	tx, err := recipeStateBegin(m, m.version, "block-1", limits)
	if err != nil {
		t.Fatal(err)
	}
	key := recipeStateTestKey("t", "a")
	if err = tx.Put(ctx, key, []byte("ok")); err != nil {
		t.Fatal(err)
	}
	if err = tx.Put(ctx, key, []byte("12345")); !errors.Is(err, errRecipeStateBudget) {
		t.Fatal("value budget", err)
	}
	if err = tx.Put(ctx, recipeStateTestKey("t", "b"), []byte("x")); !errors.Is(err, errRecipeStateBudget) {
		t.Fatal("key budget", err)
	}
	v, err := tx.Get(ctx, key)
	if err != nil || string(v.Bytes) != "ok" {
		t.Fatal("budget failure changed staged state", v, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = tx.Commit(cancelled); !errors.Is(err, context.Canceled) || m.version.Revision != 0 || len(m.values) != 0 {
		t.Fatal("cancelled commit mutated backend", err)
	}
	tx.Abort()
	if _, err = tx.Commit(ctx); err == nil {
		t.Fatal("aborted commit accepted")
	}
	if _, err = recipeStateBegin(m, m.version, "block-1", recipeStateLimits{}); err == nil {
		t.Fatal("unbounded limits accepted")
	}

	tx, _ = recipeStateBegin(m, m.version, "block-1", recipeStateTestLimits())
	if err = tx.Put(ctx, key, []byte("ok")); err != nil {
		t.Fatal(err)
	}
	receipt, err := tx.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bad := receipt
	bad.Changes = recipeStateCloneChanges(receipt.Changes)
	bad.Changes[0].After.Bytes[0] = 'X'
	before := recipeStateCloneValue(m.values[key])
	if _, err = recipeStateUndo(ctx, m, bad, m.version, recipeStateTestLimits()); !errors.Is(err, errRecipeStateConflict) {
		t.Fatal("wrong after-image accepted", err)
	}
	if !reflect.DeepEqual(m.values[key], before) || m.version != receipt.Committed {
		t.Fatal("failed undo mutated state")
	}
	bad = receipt
	bad.BeforeImages = append(append([]recipeStateBefore(nil), receipt.BeforeImages...), receipt.BeforeImages[0])
	if _, err = recipeStateUndo(ctx, m, bad, m.version, recipeStateTestLimits()); err == nil {
		t.Fatal("duplicate before images accepted")
	}
}

func TestRecipeStateConcurrentCommitHasOneWinner(t *testing.T) {
	ctx := context.Background()
	backend := &recipeStateMemoryTest{version: recipeStateVersion{Checkpoint: "origin"}, values: map[recipeStateKey]recipeStateValue{}}
	first, _ := recipeStateBegin(backend, backend.version, "block-1", recipeStateTestLimits())
	second, _ := recipeStateBegin(backend, backend.version, "block-1", recipeStateTestLimits())
	if err := first.Put(ctx, recipeStateTestKey("a", "x"), []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := second.Put(ctx, recipeStateTestKey("b", "x"), []byte("second")); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, tx := range []*recipeStateTxn{first, second} {
		go func(tx *recipeStateTxn) { <-start; _, err := tx.Commit(ctx); results <- err }(tx)
	}
	close(start)
	a, b := <-results, <-results
	if !((a == nil && errors.Is(b, errRecipeStateConflict)) || (b == nil && errors.Is(a, errRecipeStateConflict))) {
		t.Fatalf("expected one atomic winner: %v, %v", a, b)
	}
	if backend.version.Revision != 1 || len(backend.values) != 1 {
		t.Fatalf("partial or duplicate commit: revision=%d values=%v", backend.version.Revision, backend.values)
	}
}

func TestRecipeStateConsecutiveUndoPreservesLogicalChain(t *testing.T) {
	ctx := context.Background()
	m := &recipeStateMemoryTest{version: recipeStateVersion{Checkpoint: "base"}, values: map[recipeStateKey]recipeStateValue{}}
	key := recipeStateTestKey("live", "key")
	a, err := recipeStateBegin(m, m.version, "A", recipeStateTestLimits())
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Put(ctx, key, []byte("one")); err != nil {
		t.Fatal(err)
	}
	ra, err := a.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := recipeStateBegin(m, ra.Committed, "B", recipeStateTestLimits())
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Put(ctx, key, []byte("two")); err != nil {
		t.Fatal(err)
	}
	rb, err := b.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	current, err := recipeStateUndo(ctx, m, rb, rb.Committed, recipeStateTestLimits())
	if err != nil || current != (recipeStateVersion{Revision: 3, Checkpoint: "A"}) || string(m.values[key].Bytes) != "one" {
		t.Fatalf("undo B: %+v %v", current, err)
	}
	current, err = recipeStateUndo(ctx, m, ra, current, recipeStateTestLimits())
	if err != nil || current != (recipeStateVersion{Revision: 4, Checkpoint: "base"}) || m.values[key].Present {
		t.Fatalf("undo A: %+v %v", current, err)
	}
	if _, err = recipeStateUndo(ctx, m, rb, current, recipeStateTestLimits()); err == nil {
		t.Fatal("undo of noncurrent logical checkpoint accepted")
	}
}
