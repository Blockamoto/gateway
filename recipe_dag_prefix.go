package main

// Compare selected sparse-store keys between two immutable radix roots. Equal
// subtrees are skipped by digest, so appends beyond a checked prefix do not
// rescan that prefix. Caller must hold recipeStoreMutex and maintain immutable
// page storage; this does not assert selected-chain authority.
import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

func recipeStoreRootsEqualRange(ctx context.Context, store *recipeStore, oldRoot, newRoot string, from, to int64) (bool, error) {
	if ctx == nil || store == nil || from < 0 || to < from || oldRoot != "" && !recipeStoreHashValid(oldRoot) || newRoot != "" && !recipeStoreHashValid(newRoot) {
		return false, fmt.Errorf("invalid recipe prefix comparison")
	}
	tree := store.tree(ctx, recipeStoreHead{})
	low, high := recipeStoreHeightKey(from), recipeStoreHeightKey(to)
	pages := 0
	intersects := func(prefix string) bool {
		if len(prefix) > 16 {
			return false
		}
		return prefix+strings.Repeat("f", 16-len(prefix)) >= low && prefix+strings.Repeat("0", 16-len(prefix)) <= high
	}
	read := func(hash string) (ordinalPage, error) {
		if hash == "" {
			return ordinalPage{}, nil
		}
		pages++
		if pages > recipeCoverageMaxTreePages {
			return ordinalPage{}, fmt.Errorf("recipe prefix comparison page budget exceeded")
		}
		return tree.read(hash)
	}
	var collect func(string, string, *[]ordinalKV) error
	collect = func(hash, prefix string, out *[]ordinalKV) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if hash == "" || !intersects(prefix) || len(*out) > ordinalPageEntries {
			return nil
		}
		page, err := read(hash)
		if err != nil {
			return err
		}
		if len(page.Children) > 0 {
			if len(prefix) >= 16 {
				return fmt.Errorf("invalid recipe prefix branch")
			}
			for i, child := range page.Children {
				if err = collect(child, prefix+"0123456789abcdef"[i:i+1], out); err != nil {
					return err
				}
				if len(*out) > ordinalPageEntries {
					break
				}
			}
			return nil
		}
		for _, kv := range page.Entries {
			if len(kv.Key) != 16 || !strings.HasPrefix(kv.Key, prefix) {
				return fmt.Errorf("invalid recipe prefix key")
			}
			if kv.Key >= low && kv.Key <= high {
				*out = append(*out, kv)
				if len(*out) > ordinalPageEntries {
					break
				}
			}
		}
		return nil
	}
	var equal func(string, string, string) (bool, error)
	equal = func(a, b, prefix string) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if !intersects(prefix) || a == b {
			return true, nil
		}
		pa, err := read(a)
		if err != nil {
			return false, err
		}
		pb, err := read(b)
		if err != nil {
			return false, err
		}
		if len(pa.Children) > 0 && len(pb.Children) > 0 {
			if len(prefix) >= 16 {
				return false, fmt.Errorf("invalid recipe prefix branch")
			}
			for i := 0; i < 16; i++ {
				ok, e := equal(pa.Children[i], pb.Children[i], prefix+"0123456789abcdef"[i:i+1])
				if e != nil || !ok {
					return ok, e
				}
			}
			return true, nil
		}
		var left, right []ordinalKV
		if err = collect(a, prefix, &left); err != nil {
			return false, err
		}
		if err = collect(b, prefix, &right); err != nil {
			return false, err
		}
		if len(left) != len(right) || len(left) > ordinalPageEntries {
			return false, nil
		}
		for i := range left {
			if left[i].Key != right[i].Key || !bytes.Equal(left[i].Value, right[i].Value) {
				return false, nil
			}
		}
		return true, nil
	}
	return equal(oldRoot, newRoot, "")
}
