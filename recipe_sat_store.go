package main

import (
	"context"
	"fmt"
	"path/filepath"
)

// Native publication retains its exact authority/write/sync sequence. A saved
// generation supplies an owned receipt publisher and never writes native head
// metadata. Private staging does not acquire native global state mutexes.
func (s *indexStore) publishSatCheckpointHead(ctx context.Context, head indexHead, cp *indexCheckpoint) error {
	if s.recipeSatPrivate {
		if s.satPublish == nil {
			return fmt.Errorf("private Sat publication authority missing")
		}
		return s.satPublish(head, cp)
	}
	if err := checkIndexConsumerPublication(ctx, s.profileRoot, "sat-state"); err != nil {
		return err
	}
	if err := atomicWriteJSON(filepath.Join(s.dir, "head.json"), head); err != nil {
		return err
	}
	return syncDirectory(s.dir)
}

func (s *indexStore) recipeSatProgram() (*recipeProgram, error) {
	if s.recipeSatPrivate {
		if s.satRecipe == nil || s.satRecipe.ordered == nil || s.satRecipe.definition.Source != recipeSatSource || s.satRecipe.definition.Storage != recipeSatStorage {
			return nil, fmt.Errorf("bound private Sat recipe missing")
		}
		return s.satRecipe, nil
	}
	programs, err := builtinSatRecipes()
	if err != nil {
		return nil, err
	}
	if err = recipeSatCheckEquivalence(builtinSatSemantic, 1); err != nil {
		return nil, err
	}
	if s.retainSatHistory {
		return programs[1], nil
	}
	return programs[0], nil
}
