package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"./internal/updateapply"
)

// Walk each distinct immutable page once across related retained roots. This
// enumerates changed COW history rather than repeating 289 whole index walks.
func recipeVisitOrdinalRoots(tree *ordinalTree, roots []string, leaf func(ordinalKV) error) error {
	seen := map[string]bool{}
	var walk func(string, int) error
	walk = func(hash string, depth int) error {
		if err := tree.ctx.Err(); err != nil {
			return err
		}
		if hash == "" || seen[hash] {
			return nil
		}
		if depth > 128 {
			return fmt.Errorf("projection maintenance radix depth exceeded")
		}
		page, err := tree.read(hash)
		if err != nil {
			return err
		}
		for _, entry := range page.Entries {
			if err = leaf(entry); err != nil {
				return err
			}
		}
		for _, child := range page.Children {
			if err = walk(child, depth+1); err != nil {
				return err
			}
		}
		seen[hash] = true
		return nil
	}
	for _, root := range roots {
		if err := walk(root, 0); err != nil {
			return err
		}
	}
	return nil
}

func (g *recipeProjectionGeneration) maintain(ctx context.Context) error {
	select {
	case recipeOrderedWorkSlots <- struct{}{}:
		defer func() { <-recipeOrderedWorkSlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	recipeProjectionMu.Lock()
	defer recipeProjectionMu.Unlock()
	head, initialized, err := g.inspect(ctx)
	if err != nil || !initialized {
		return err
	}
	type roots struct{ summaries, pins, members []string }
	groups := map[string]*roots{}
	group := func(generation string) *roots {
		if groups[generation] == nil {
			groups[generation] = &roots{}
		}
		return groups[generation]
	}
	kept := map[string]bool{}
	for _, point := range head.History {
		cp, err := g.snapshot(ctx, point)
		if err != nil {
			return err
		}
		kept[point.Digest] = true
		r := group(cp.PageGeneration)
		r.summaries = append(r.summaries, cp.Root)
		r.pins = append(r.pins, cp.Source.PagePinRoot)
	}
	facts := map[string]bool{}
	staged, err := readAddressBuildContext(ctx, g.profileRoot)
	if err != nil {
		return err
	}
	if staged != nil {
		if staged.ProjectionParent == nil || indexDigest(staged.ProjectionParent.Dependency) != indexDigest(g.identity.Dependency) || !validHash(staged.ProjectionParent.Namespace) || staged.ProjectionParent.Checkpoint.Commitment != staged.Source.Commitment {
			return fmt.Errorf("staged projection dependency differs from its owner")
		}
		r := group(staged.Projection.PageGeneration)
		r.summaries = append(r.summaries, staged.Projection.Root)
		// A partial traversal owns only the member facts copied so far. Its
		// complete source pin tree is installed before snapshot publication.
		dir := addressStatePages(g.profileRoot, staged.Projection.PageGeneration)
		if err = recipeVisitOrdinalRoots(newOrdinalTree(dir, staged.Projection.Root).withContext(ctx), []string{staged.Projection.Root}, func(kv ordinalKV) error {
			var bucket addressStateBucket
			if json.Unmarshal(kv.Value, &bucket) != nil || validateAddressBucket(bucket) != nil {
				return fmt.Errorf("invalid staged projection bucket")
			}
			return addressWalkBucketAfter(newOrdinalTree(dir, bucket.Root).withContext(ctx), bucket, "", func(memberRow ordinalKV) error {
				var member utxoMember
				if json.Unmarshal(memberRow.Value, &member) != nil || !validHash(member.FactRef.Page) {
					return fmt.Errorf("invalid staged projection fact pin")
				}
				facts[member.FactRef.Page] = true
				return ctx.Err()
			})
		}); err != nil {
			return err
		}
	}
	for generation, r := range groups {
		dir := addressStatePages(g.profileRoot, generation)
		if err = updateapply.CheckPath(dir); err != nil {
			return err
		}
		tree := newOrdinalTree(dir, "").withContext(ctx)
		if err = recipeVisitOrdinalRoots(tree, r.summaries, func(kv ordinalKV) error {
			var bucket addressStateBucket
			if !validHash(kv.Key) || json.Unmarshal(kv.Value, &bucket) != nil || validateAddressBucket(bucket) != nil {
				return fmt.Errorf("invalid retained projection bucket")
			}
			r.members = append(r.members, bucket.Root)
			return nil
		}); err != nil {
			return err
		}
		if err = recipeVisitOrdinalRoots(tree, r.pins, func(kv ordinalKV) error {
			var count uint64
			if !validHash(kv.Key) || json.Unmarshal(kv.Value, &count) != nil || count == 0 {
				return fmt.Errorf("invalid retained projection fact pin")
			}
			facts[kv.Key] = true
			return nil
		}); err != nil {
			return err
		}
	}
	// All owners and fact bytes are authenticated before any fact deletion.
	if err = collectOutputFactPagesContext(ctx, g.profileRoot, facts); err != nil {
		return err
	}
	for generation, r := range groups {
		dir := addressStatePages(g.profileRoot, generation)
		if _, err = os.Stat(dir); os.IsNotExist(err) {
			continue // evaluated empty genesis has no pages
		} else if err != nil {
			return err
		}
		err = collectOrdinalPagesContext(ctx, dir, func(visit func(string) error) error {
			for _, roots := range [][]string{r.summaries, r.members, r.pins} {
				for _, root := range roots {
					if err := visit(root); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil && !errors.Is(err, errOrdinalMaintenancePending) {
			return err
		}
	}
	return g.collectSnapshots(ctx, kept)
}

func (g *recipeProjectionGeneration) collectSnapshots(ctx context.Context, kept map[string]bool) error {
	dir := g.path("snapshots")
	if err := updateapply.CheckPath(dir); err != nil {
		return err
	}
	file, err := os.Open(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	changed := false
	for {
		entries, err := file.ReadDir(128)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			hash := strings.TrimSuffix(entry.Name(), ".json")
			if entry.IsDir() || !validHash(hash) || entry.Name() != hash+".json" || kept[hash] {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if err := updateapply.CheckPath(path); err != nil {
				return err
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			changed = true
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if changed {
		return syncDirectory(dir)
	}
	return ctx.Err()
}
