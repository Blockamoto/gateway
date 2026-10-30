package main

// Compare immutable receipt trees only within a requested historical interval.
// Equal content-addressed subtrees reuse their authenticated identity. Changed
// boundary leaves are compared by exact entries, so an append which splits a
// leaf outside the interval does not invalidate the historical view.
import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
)

func recipeStoreRangeEqual(ctx context.Context, s *recipeStore, before, current string, from, to int64) (bool, error) {
	if ctx == nil || s == nil || from < 0 || to < from || before != "" && !recipeStoreHashValid(before) || current != "" && !recipeStoreHashValid(current) {
		return false, fmt.Errorf("invalid receipt range comparison")
	}
	left, right := s.tree(ctx, recipeStoreHead{Root: before}), s.tree(ctx, recipeStoreHead{Root: current})
	low, high := recipeStoreHeightKey(from), recipeStoreHeightKey(to)
	outside := func(prefix string) bool {
		return prefix+strings.Repeat("f", 16-len(prefix)) < low || prefix+strings.Repeat("0", 16-len(prefix)) > high
	}
	entries := func(page ordinalPage, prefix string) ([]ordinalKV, error) {
		out := []ordinalKV{}
		for _, kv := range page.Entries {
			if len(kv.Key) != 16 || !strings.HasPrefix(kv.Key, prefix) {
				return nil, fmt.Errorf("invalid receipt range key")
			}
			if kv.Key >= low && kv.Key <= high {
				out = append(out, kv)
			}
		}
		return out, nil
	}
	var walk func(*ordinalTree, string, string, func(ordinalKV) error) error
	walk = func(tree *ordinalTree, hash, prefix string, visit func(ordinalKV) error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(prefix) > 16 {
			return fmt.Errorf("receipt range tree is too deep")
		}
		if hash == "" || outside(prefix) {
			return nil
		}
		page, err := tree.read(hash)
		if err != nil {
			return err
		}
		if len(page.Children) != 0 {
			if len(prefix) == 16 {
				return fmt.Errorf("receipt range branch is too deep")
			}
			for i, child := range page.Children {
				if err := walk(tree, child, prefix+"0123456789abcdef"[i:i+1], visit); err != nil {
					return err
				}
			}
			return nil
		}
		rows, err := entries(page, prefix)
		if err != nil {
			return err
		}
		for _, kv := range rows {
			if err := visit(kv); err != nil {
				return err
			}
		}
		return nil
	}
	var equal func(string, string, string) (bool, error)
	equal = func(a, b, prefix string) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if len(prefix) > 16 {
			return false, fmt.Errorf("receipt range tree is too deep")
		}
		if a == b || outside(prefix) {
			return true, nil
		}
		p, err := left.read(a)
		if err != nil {
			return false, err
		}
		q, err := right.read(b)
		if err != nil {
			return false, err
		}
		if len(p.Children) != 0 && len(q.Children) != 0 {
			if len(prefix) == 16 {
				return false, fmt.Errorf("receipt range branch is too deep")
			}
			for i := range p.Children {
				matched, err := equal(p.Children[i], q.Children[i], prefix+"0123456789abcdef"[i:i+1])
				if err != nil || !matched {
					return matched, err
				}
			}
			return true, nil
		}
		if len(p.Children) == 0 && len(q.Children) == 0 {
			x, err := entries(p, prefix)
			if err != nil {
				return false, err
			}
			y, err := entries(q, prefix)
			if err != nil {
				return false, err
			}
			if len(x) != len(y) {
				return false, nil
			}
			for i := range x {
				if x[i].Key != y[i].Key || !bytes.Equal(x[i].Value, y[i].Value) {
					return false, nil
				}
			}
			return true, nil
		}
		// One old boundary leaf split, or the inverse after reconciliation.
		// Stop after its <=64 expected entries plus one mismatch; never build
		// an in-memory copy of the opposing subtree.
		leaf, branch, branchRoot := p, right, b
		if len(q.Children) == 0 {
			leaf, branch, branchRoot = q, left, a
		}
		expected, err := entries(leaf, prefix)
		if err != nil {
			return false, err
		}
		i := 0
		different := errors.New("receipt range differs")
		err = walk(branch, branchRoot, prefix, func(kv ordinalKV) error {
			if i >= len(expected) || kv.Key != expected[i].Key || !bytes.Equal(kv.Value, expected[i].Value) {
				return different
			}
			i++
			return nil
		})
		if errors.Is(err, different) {
			return false, nil
		}
		return i == len(expected), err
	}
	return equal(before, current, "")
}
