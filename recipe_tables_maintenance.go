package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"./internal/updateapply"
)

func (g *recipeTablesGeneration) maintain(ctx context.Context) error {
	select {
	case recipeOrderedWorkSlots <- struct{}{}:
		defer func() { <-recipeOrderedWorkSlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	recipeOrderedGenerationMu.Lock()
	defer recipeOrderedGenerationMu.Unlock()
	h, _, initialized, err := g.inspect(ctx)
	if err != nil || !initialized {
		return err
	}
	roots, pinRoots := []string{}, []string{}
	retained := map[string]bool{}
	for _, ref := range h.History {
		p, err := g.readPoint(ctx, ref)
		if err != nil {
			return err
		}
		retained[ref.Commitment] = true
		roots = append(roots, p.Root, p.Journal, p.BlobPins, p.PreviousBlobPins)
		pinRoots = append(pinRoots, p.BlobPins, p.PreviousBlobPins)
	}
	pins := map[string]bool{}
	err = recipeVisitOrdinalRoots(recipeTablesTree(ctx, g.path("pages"), ""), pinRoots, func(kv ordinalKV) error {
		var pin recipeTablesBlobPin
		if !validHash(kv.Key) || json.Unmarshal(kv.Value, &pin) != nil || pin.References == 0 || pin.Bytes <= recipeTablesInlineBytes || pin.Bytes > recipeTablesEncodedBytes {
			return fmt.Errorf("%w: large-value pins", ErrRecipeTablesCorrupt)
		}
		if !pins[kv.Key] {
			raw, err := recipeStoreReadBounded(recipeTablesBlobPath(g.profileRoot, kv.Key), recipeTablesEncodedBytes)
			if err != nil || uint64(len(raw)) != pin.Bytes || recipeStoreHash(raw) != kv.Key {
				return fmt.Errorf("%w: required large value (%v)", ErrRecipeTablesCorrupt, err)
			}
		}
		pins[kv.Key] = true
		return ctx.Err()
	})
	if err != nil {
		return err
	}
	// Native marking authenticates every required state/journal/pin subtree
	// before any large-value sweep. Its bounded continuation is retained.
	if err = collectOrdinalPagesContext(ctx, g.path("pages"), func(visit func(string) error) error {
		for _, root := range roots {
			if err := visit(root); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err = g.prunePoints(ctx, retained); err != nil {
		return err
	}
	return g.pruneValues(ctx, pins)
}

func (g *recipeTablesGeneration) prunePoints(ctx context.Context, retained map[string]bool) error {
	dir := g.path("points")
	if err := updateapply.CheckPath(dir); err != nil {
		return err
	}
	f, err := os.Open(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	cutoff := time.Now().Add(-time.Hour)
	for {
		entries, readErr := f.ReadDir(128)
		for _, entry := range entries {
			if err = ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			hash := strings.TrimSuffix(entry.Name(), ".json")
			if !validHash(hash) || retained[hash] {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("table point cleanup refuses linked files")
			}
			if info.ModTime().After(cutoff) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if err = updateapply.CheckPath(path); err != nil {
				return err
			}
			var p recipeTablesPoint
			if err = utxoReadJSON(ctx, path, 16<<10, &p); err != nil {
				return err
			}
			if _, err = g.readPointMetadata(ctx, recipeTablesPointRef{Height: p.Height, BlockHash: p.BlockHash, Commitment: hash}); err != nil {
				return err
			}
			if err = os.Remove(path); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func (g *recipeTablesGeneration) pruneValues(ctx context.Context, pins map[string]bool) error {
	dir := g.path("values")
	if err := updateapply.CheckPath(dir); err != nil {
		return err
	}
	shards, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-time.Hour)
	for _, shard := range shards {
		if !shard.IsDir() || len(shard.Name()) != 2 {
			continue
		}
		path := filepath.Join(dir, shard.Name())
		if err = updateapply.CheckPath(path); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		prune := func() error {
			defer f.Close()
			for {
				entries, readErr := f.ReadDir(128)
				for _, entry := range entries {
					if err = ctx.Err(); err != nil {
						return err
					}
					if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
						continue
					}
					hash := strings.TrimSuffix(entry.Name(), ".json")
					if !validHash(hash) || hash[:2] != shard.Name() || pins[hash] {
						continue
					}
					info, err := entry.Info()
					if err != nil {
						return err
					}
					if !info.Mode().IsRegular() {
						return fmt.Errorf("table value cleanup refuses linked files")
					}
					if info.ModTime().After(cutoff) {
						continue
					}
					file := recipeTablesBlobPath(g.profileRoot, hash)
					raw, err := recipeStoreReadBounded(file, recipeTablesEncodedBytes)
					if err != nil {
						return err
					}
					if recipeStoreHash(raw) != hash {
						return fmt.Errorf("unreachable table value checksum changed")
					}
					if err = os.Remove(file); err != nil {
						return err
					}
				}
				if readErr == io.EOF {
					return nil
				}
				if readErr != nil {
					return readErr
				}
			}
		}
		if err = prune(); err != nil {
			return err
		}
	}
	return ctx.Err()
}
