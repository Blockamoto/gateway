package main

// Optional experimental transaction wrapper. It pins a fully authenticated
// durable head once, then checks the small head and marker on every read. The
// original backend still revalidates the full receipt and CAS under authority
// when publishing. It does not change the existing durable adapter API.
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
)

type recipeDurablePinnedForward struct {
	inner *recipeDurableForward
	head  recipeDurableHead
}

func (s *recipeDurableStore) beginPinned(ctx context.Context, height int64, hash, parent string, guard recipeStoreGuard, limits recipeStateLimits) (*recipeStateTxn, error) {
	tx, err := s.begin(ctx, height, hash, parent, guard, limits)
	if err != nil {
		return nil, err
	}
	inner, ok := tx.backend.(*recipeDurableForward)
	if !ok {
		return nil, fmt.Errorf("unexpected durable transaction backend")
	}
	head, err := s.status()
	if err != nil {
		return nil, err
	}
	if (recipeStateVersion{Revision: head.Revision, Checkpoint: head.Point.Checkpoint}) != inner.base {
		return nil, errRecipeStateConflict
	}
	tx.backend = &recipeDurablePinnedForward{inner: inner, head: head}
	return tx, nil
}
func (b *recipeDurablePinnedForward) ReadState(ctx context.Context, v recipeStateVersion, key recipeStateKey) (recipeStateValue, error) {
	if ctx == nil {
		return recipeStateValue{}, fmt.Errorf("missing context")
	}
	recipeDurableMu.Lock()
	defer recipeDurableMu.Unlock()
	if err := ctx.Err(); err != nil {
		return recipeStateValue{}, err
	}
	s := b.inner.store
	marker, err := recipeStoreReadBounded(filepath.Join(s.dir, "initialized.json"), recipeStoreHeadMaxBytes)
	if err != nil {
		return recipeStateValue{}, err
	}
	want, _ := json.Marshal(s.identity)
	if !bytes.Equal(marker, want) {
		return recipeStateValue{}, fmt.Errorf("ordered-state marker mismatch")
	}
	raw, err := recipeStoreReadBounded(filepath.Join(s.dir, "head.json"), recipeStoreHeadMaxBytes)
	if err != nil {
		return recipeStateValue{}, err
	}
	var head recipeDurableHead
	if err = json.Unmarshal(raw, &head); err != nil {
		return recipeStateValue{}, err
	}
	if head != b.head || v != b.inner.base {
		return recipeStateValue{}, errRecipeStateConflict
	}
	return s.get(ctx, head.Point.Root, key)
}
func (b *recipeDurablePinnedForward) ApplyState(ctx context.Context, v recipeStateVersion, next string, batch recipeStateBatch) (recipeStateVersion, error) {
	return b.inner.ApplyState(ctx, v, next, batch)
}
