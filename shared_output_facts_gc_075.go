package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"./internal/updateapply"
)

var outputFactCollectionMu sync.Mutex

// Run between index jobs / at a committed build boundary. Recent unpublished
// pages receive a staging grace period. Every committed owner is enumerated
// before any file is removed; damaged manifests fail closed without cleanup.
func collectSharedOutputFactsContext(ctx context.Context, root string) error {
	outputFactCollectionMu.Lock()
	defer outputFactCollectionMu.Unlock()
	pins := map[string]bool{}
	u, e := utxoStoreHead(root)
	if e != nil {
		return e
	}
	if e = u.VisitFactPages(ctx, func(hash string) error { pins[hash] = true; return nil }); e != nil {
		return e
	}
	s, e := indexStoreHead(root, "txo-spender")
	if e != nil {
		return e
	}
	if e = s.walk(func(b indexBatch) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if b.Spenders != nil {
			for _, ref := range b.Spenders.OutputFacts {
				pins[ref.Page] = true
			}
		}
		p, have, e := s.txioEnrichmentContext(ctx, b.Checkpoint.Height, b.Checkpoint.BlockHash)
		if e != nil {
			return e
		}
		if have {
			for _, ref := range p.OutputFacts {
				pins[ref.Page] = true
			}
		}
		return nil
	}); e != nil {
		return e
	}
	// Historical address outputs own their exact facts independently of
	// optional TXIO history and live UTXO membership. Retained branch roots are
	// authenticated before any shared page can be collected.
	history, e := openAddressHistoryStoreContext(ctx, root)
	if e != nil {
		return e
	}
	if e = history.VisitFactPagesContext(ctx, func(hash string) error { pins[hash] = true; return nil }); e != nil {
		return e
	}
	if e = visitSatFactPagesContext077(ctx, root, func(hash string) error { pins[hash] = true; return nil }); e != nil {
		return e
	}
	base := outputFactDirectory(root)
	if e = updateapply.CheckPath(base); e != nil {
		return e
	}
	// Authenticate retained pages once. Missing dependencies stop collection.
	for hash := range pins {
		if _, e = readOutputFactPageContext(ctx, root, hash); e != nil {
			return e
		}
	}
	cutoff := time.Now().Add(-time.Hour)
	return filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
		if os.IsNotExist(walkErr) && path == base {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("output fact collection refuses linked storage")
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".json" {
			return nil
		}
		hash := strings.TrimSuffix(d.Name(), ".json")
		if !validHash(hash) {
			return nil
		}
		expected, e := outputFactPath(root, hash)
		if e != nil || filepath.Clean(expected) != filepath.Clean(path) {
			return fmt.Errorf("output fact collection path mismatch")
		}
		if pins[hash] {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
			return nil
		}
		if e = updateapply.CheckPath(path); e != nil {
			return e
		}
		if e = os.Remove(path); e != nil {
			return e
		}
		return syncDirectory(filepath.Dir(path))
	})
}
