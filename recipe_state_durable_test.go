package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func durableTestID() recipeDurableIdentity {
	return recipeDurableIdentity{Instance: "state-example", Semantic: strings.Repeat("a", 64), Network: "testnet", Genesis: strings.Repeat("b", 64)}
}
func durableTestGuard(f func() error) error { return f() }
func durableTestStore(t *testing.T) (*recipeDurableStore, recipeDurableIdentity) {
	t.Helper()
	id := durableTestID()
	s, err := openRecipeDurableStore(t.TempDir(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Create(); err != nil {
		t.Fatal(err)
	}
	return s, id
}
func durableTestBegin(t *testing.T, s *recipeDurableStore, height int64, hash, parent string) *recipeStateTxn {
	t.Helper()
	tx, err := s.begin(context.Background(), height, hash, parent, durableTestGuard, recipeStateTestLimits())
	if err != nil {
		t.Fatal(err)
	}
	return tx
}
func TestRecipeDurableRestartUndoAndEmptyUnit(t *testing.T) {
	ctx := context.Background()
	s, id := durableTestStore(t)
	key := recipeStateTestKey("coins", "key")
	tx := durableTestBegin(t, s, 0, id.Genesis, "")
	if err := tx.Put(ctx, key, []byte("one")); err != nil {
		t.Fatal(err)
	}
	r0, err := tx.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reopened := *s
	h, err := reopened.status()
	if err != nil || h.Point.Height != 0 || h.Point.Checkpoint != r0.Committed.Checkpoint {
		t.Fatal(h, err)
	}
	v, err := reopened.get(ctx, h.Point.Root, key)
	if err != nil || string(v.Bytes) != "one" {
		t.Fatal(v, err)
	}
	block1 := strings.Repeat("c", 64)
	tx = durableTestBegin(t, &reopened, 1, block1, id.Genesis)
	if err = tx.Put(ctx, key, []byte("two")); err != nil {
		t.Fatal(err)
	}
	temp := recipeStateTestKey("coins", "temp")
	if err = tx.Put(ctx, temp, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err = tx.Delete(ctx, temp); err != nil {
		t.Fatal(err)
	}
	r1, err := tx.Commit(ctx)
	if err != nil || len(r1.Changes) != 1 {
		t.Fatal(r1, err)
	}
	block2 := strings.Repeat("d", 64)
	tx = durableTestBegin(t, &reopened, 2, block2, block1)
	r2, err := tx.Commit(ctx)
	if err != nil || len(r2.Changes) != 0 {
		t.Fatal(r2, err)
	}
	current, err := reopened.undo(ctx, r2.Committed, durableTestGuard)
	if err != nil || current.Checkpoint != r1.Committed.Checkpoint || current.Revision != 4 {
		t.Fatal(current, err)
	}
	current, err = reopened.undo(ctx, current, durableTestGuard)
	if err != nil || current.Checkpoint != r0.Committed.Checkpoint || current.Revision != 5 {
		t.Fatal(current, err)
	}
	h, err = reopened.status()
	if err != nil {
		t.Fatal(err)
	}
	v, err = reopened.get(ctx, h.Point.Root, key)
	if err != nil || string(v.Bytes) != "one" {
		t.Fatal(v, err)
	}
	current, err = reopened.undo(ctx, current, durableTestGuard)
	if err != nil || current.Checkpoint != recipeDurableOrigin(id) || current.Revision != 6 {
		t.Fatal(current, err)
	}
	if _, err = reopened.undo(ctx, current, durableTestGuard); err == nil {
		t.Fatal("undo origin accepted")
	}
}
func TestRecipeDurableFailureAndConflict(t *testing.T) {
	ctx := context.Background()
	s, id := durableTestStore(t)
	tx := durableTestBegin(t, s, 0, id.Genesis, "")
	stale := durableTestBegin(t, s, 0, id.Genesis, "")
	key := recipeStateTestKey("t", "k")
	if err := tx.Put(ctx, key, []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := stale.Put(ctx, key, []byte("bad")); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"pages", "receipt"} {
		s.hook = func(actual string) error {
			if actual == stage {
				return errors.New("injected")
			}
			return nil
		}
		if _, err := tx.Commit(ctx); err == nil {
			t.Fatal("failure ignored", stage)
		}
		h, err := s.status()
		if err != nil || h.Revision != 0 || h.Point.Height != -1 {
			t.Fatal(h, err)
		}
	}
	s.hook = nil
	r, err := tx.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stale.Commit(ctx); !errors.Is(err, errRecipeStateConflict) {
		t.Fatal("stale commit", err)
	}
	h, err := s.status()
	if err != nil || h.Point.Checkpoint != r.Committed.Checkpoint {
		t.Fatal(h, err)
	}
	denied := errors.New("authority revoked")
	tx, err = s.begin(ctx, 1, strings.Repeat("c", 64), id.Genesis, func(func() error) error { return denied }, recipeStateTestLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Commit(ctx); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	h2, err := s.status()
	if err != nil || h2 != h {
		t.Fatal("denied changed head", err)
	}
	if _, err = s.begin(ctx, 2, strings.Repeat("c", 64), id.Genesis, durableTestGuard, recipeStateTestLimits()); err == nil {
		t.Fatal("gap accepted")
	}
	if _, err = s.begin(ctx, 1, strings.Repeat("c", 64), strings.Repeat("e", 64), durableTestGuard, recipeStateTestLimits()); err == nil {
		t.Fatal("wrong parent accepted")
	}
}
func TestRecipeDurableCorruptPredecessorAndCancellation(t *testing.T) {
	ctx := context.Background()
	s, id := durableTestStore(t)
	tx := durableTestBegin(t, s, 0, id.Genesis, "")
	key := recipeStateTestKey("t", "k")
	if err := tx.Put(ctx, key, []byte("v")); err != nil {
		t.Fatal(err)
	}
	r0, err := tx.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.status()
	if err != nil {
		t.Fatal(err)
	}
	tx = durableTestBegin(t, s, 1, strings.Repeat("c", 64), id.Genesis)
	r1, err := tx.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.undo(cancelCtx, r1.Committed, durableTestGuard); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.dir, "receipts", first.Point.Receipt+".json"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.undo(ctx, r1.Committed, durableTestGuard); err == nil {
		t.Fatal("corrupt predecessor accepted")
	}
	h, err := s.status()
	if err != nil || h.Point.Checkpoint != r1.Committed.Checkpoint {
		t.Fatal(h, err)
	}
	_ = r0
}

func TestRecipeDurableReapplyAndKeyCollision(t *testing.T) {
	ctx := context.Background()
	s, id := durableTestStore(t)
	key := recipeStateTestKey("t", "key")
	tx := durableTestBegin(t, s, 0, id.Genesis, "")
	if err := tx.Put(ctx, key, []byte{0, 255}); err != nil {
		t.Fatal(err)
	}
	first, err := tx.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.undo(ctx, first.Committed, durableTestGuard); err != nil {
		t.Fatal(err)
	}
	tx = durableTestBegin(t, s, 0, id.Genesis, "")
	if err = tx.Put(ctx, key, []byte{0, 255}); err != nil {
		t.Fatal(err)
	}
	again, err := tx.Commit(ctx)
	if err != nil || again.Committed.Revision != 3 || again.Committed.Checkpoint != first.Committed.Checkpoint {
		t.Fatal(again, err)
	}
	h, err := s.status()
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.get(ctx, h.Point.Root, key)
	if err != nil || len(v.Bytes) != 2 || v.Bytes[1] != 255 {
		t.Fatal(v, err)
	}
	// A forged leaf under the same hash must not be read as the requested key.
	tree := s.tree(ctx, "")
	if err = tree.put(recipeDurableKey(key), recipeDurableEntry{Table: []byte("other"), Key: []byte("key"), Value: []byte("bad")}); err != nil {
		t.Fatal(err)
	}
	root, err := tree.flush()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.get(ctx, root, key); err == nil {
		t.Fatal("collision was silently accepted")
	}
}

func TestRecipeDurableMissingHeadAndBoundedInputs(t *testing.T) {
	s, id := durableTestStore(t)
	tx := durableTestBegin(t, s, 0, id.Genesis, "")
	if err := tx.Put(context.Background(), recipeStateTestKey("t", string([]byte{0xff})), []byte("x")); err == nil {
		t.Fatal("non-UTF-8 key accepted into JSON receipt")
	}
	tooLarge := recipeStateTestLimits()
	tooLarge.MaxTouched = recipeDurableTouchedMax + 1
	if _, err := s.begin(context.Background(), 0, id.Genesis, "", durableTestGuard, tooLarge); !errors.Is(err, errRecipeStateBudget) {
		t.Fatal("excessive batch accepted", err)
	}
	if err := os.Remove(filepath.Join(s.dir, "head.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.status(); err == nil {
		t.Fatal("missing initialized head accepted")
	}
	if err := s.Create(); err == nil {
		t.Fatal("damaged generation recreated as empty")
	}
}
