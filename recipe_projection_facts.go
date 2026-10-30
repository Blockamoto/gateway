package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"./internal/updateapply"
)

// A derived generation owns its immutable fact links. Source retention/GC can
// remove the original name without invalidating a retained derived snapshot.
// The copy fallback preserves the same content identity across volumes.
type recipeProjectionFactImporter struct {
	from, to string
	seen     map[string]bool
	order    []string
}

func newRecipeProjectionFactImporter(from, to string) *recipeProjectionFactImporter {
	return &recipeProjectionFactImporter{from: from, to: to, seen: map[string]bool{}}
}
func (i *recipeProjectionFactImporter) copy(ctx context.Context, member utxoMember) error {
	if ctx == nil {
		return fmt.Errorf("fact import context required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	hash := member.FactRef.Page
	if i.seen[hash] {
		return nil
	}
	target, err := outputFactPath(i.to, hash)
	if err != nil {
		return err
	}
	if err = updateapply.CheckPath(target); err != nil {
		return err
	}
	if _, err = os.Stat(target); err == nil {
		page, e := readOutputFactPageContext(ctx, i.to, hash)
		if e != nil {
			return e
		}
		if !page.Values || !page.Scripts {
			return fmt.Errorf("projection fact page is incomplete")
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		page, e := readOutputFactPageContext(ctx, i.from, hash)
		if e != nil {
			return e
		}
		if !page.Values || !page.Scripts {
			return fmt.Errorf("source fact page is incomplete")
		}
		source, e := outputFactPath(i.from, hash)
		if e != nil {
			return e
		}
		if e = updateapply.CheckPath(source); e != nil {
			return e
		}
		stat, e := os.Stat(source)
		if e != nil {
			return e
		}
		if !stat.Mode().IsRegular() {
			return fmt.Errorf("source fact is not regular")
		}
		if e = recipeStorePutImmutableFile(ctx, filepath.Dir(target), recipeSparseStagedUnit{Path: source, Digest: hash, Bytes: stat.Size()}); e != nil {
			return e
		}
	}
	if len(i.order) >= utxoFactCachePages {
		delete(i.seen, i.order[0])
		i.order = i.order[1:]
	}
	i.seen[hash] = true
	i.order = append(i.order, hash)
	return ctx.Err()
}
