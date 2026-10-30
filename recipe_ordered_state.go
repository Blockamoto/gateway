package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// Ordered operations use typed values. A physical adapter may avoid transient
// JSON and a duplicate logical journal when it already owns private COW roots,
// first-before undo and guarded atomic publication. Reads return owned values;
// writes borrow immutable values. Abort must make staged writes unpublishable.
// No operation here derives an index or grants source/publication authority.
type recipeOrderedState interface {
	ReadOrdered(context.Context, recipeStateKey) (recipeOrderedValue, error)
	PutOrdered(context.Context, recipeStateKey, recipeOrderedValue) error
	DeleteOrdered(context.Context, recipeStateKey) error
	AbortOrdered()
}

type recipeOrderedJSONState struct{ transaction *recipeStateTxn }

func (s recipeOrderedJSONState) ReadOrdered(ctx context.Context, key recipeStateKey) (recipeOrderedValue, error) {
	v, err := s.transaction.Get(ctx, key)
	if err != nil {
		return recipeOrderedValue{}, err
	}
	if !v.Present {
		return recipeOrderedValue{Kind: "missing"}, nil
	}
	if err = recipeValidateJSON(v.Bytes); err != nil {
		return recipeOrderedValue{}, err
	}
	var value recipeOrderedValue
	if err = json.Unmarshal(v.Bytes, &value); err != nil {
		return value, err
	}
	if err = recipeOrderedCheck(value, 0); err != nil || value.Kind == "missing" {
		return value, fmt.Errorf("invalid stored ordered value")
	}
	return value, nil
}

func (s recipeOrderedJSONState) PutOrdered(ctx context.Context, key recipeStateKey, value recipeOrderedValue) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.transaction.Put(ctx, key, data)
}
func (s recipeOrderedJSONState) DeleteOrdered(ctx context.Context, key recipeStateKey) error {
	return s.transaction.Delete(ctx, key)
}
func (s recipeOrderedJSONState) AbortOrdered() { s.transaction.Abort() }
