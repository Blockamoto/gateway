package main

import (
	"./internal/updateapply"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrSatArchivePinsUnavailable = errors.New("preserved Sat archive pins unavailable; cleanup deferred")

func satArchiveError077(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("%w: %v", ErrSatArchivePinsUnavailable, err)
}
func visitSatArchivePins077(ctx context.Context, root string, visit func(string) error) error {
	dir := filepath.Join(root, "indexes", "sat-state-archives")
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
	for {
		entries, readErr := f.ReadDir(64)
		for _, entry := range entries {
			if err = ctx.Err(); err != nil {
				return err
			}
			if !entry.IsDir() || !validHash(entry.Name()) {
				return satArchiveError077(ctx, fmt.Errorf("unexpected Sat archive identity"))
			}
			archive := filepath.Join(dir, entry.Name())
			if err = updateapply.CheckPath(archive); err != nil {
				return satArchiveError077(ctx, err)
			}
			var head indexHead
			if err = utxoReadJSON(ctx, filepath.Join(archive, "head.json"), 256<<10, &head); err != nil {
				return satArchiveError077(ctx, err)
			}
			if head.Commitment == "" {
				continue
			}
			if !validHash(head.Commitment) {
				return satArchiveError077(ctx, fmt.Errorf("archive current commitment"))
			}
			if err = updateapply.CheckPath(filepath.Join(archive, "commits", head.Commitment+".json")); err != nil {
				return satArchiveError077(ctx, err)
			}
			var current indexBatch
			if err = utxoReadJSON(ctx, filepath.Join(archive, "commits", head.Commitment+".json"), 64<<20, &current); err != nil {
				return satArchiveError077(ctx, err)
			}
			if current.Sats == nil || current.Checkpoint.Commitment != head.Commitment || checkpointHash(current.Checkpoint) != head.Commitment {
				return satArchiveError077(ctx, fmt.Errorf("archive Sat snapshot missing"))
			}
			if current.Sats.Profile == "gateway-sat-fifo-mainnet-v1" {
				// The old encoder predates shared facts; its records cannot be
				// rehashed through today's snapshot shape. A legacy label must
				// never be used to bypass ownership of new shared pages.
				if current.Sats.FactPinRoot != "" {
					return satArchiveError077(ctx, fmt.Errorf("legacy archive contains shared fact pins"))
				}
				continue
			}
			if current.Sats.Profile != satIndexProfile || head.SatSavepointsHash != satSavepointsDigest077(head) || len(head.SatSavepoints) > 4 {
				return satArchiveError077(ctx, fmt.Errorf("archive profile/manifest"))
			}
			commits := []string{head.Commitment}
			for _, p := range head.SatSavepoints {
				commits = append(commits, p.Commitment)
			}
			seen := map[string]bool{}
			for _, commit := range commits {
				if seen[commit] {
					continue
				}
				seen[commit] = true
				if !validHash(commit) {
					return satArchiveError077(ctx, fmt.Errorf("archive commit identity"))
				}
				if err = updateapply.CheckPath(filepath.Join(archive, "commits", commit+".json")); err != nil {
					return satArchiveError077(ctx, err)
				}
				var b indexBatch
				if err = utxoReadJSON(ctx, filepath.Join(archive, "commits", commit+".json"), 64<<20, &b); err != nil {
					return satArchiveError077(ctx, err)
				}
				if b.Sats == nil || b.Sats.Profile != satIndexProfile || b.Checkpoint.Commitment != commit || checkpointHash(b.Checkpoint) != commit || b.Checkpoint.RecordsHash != batchRecordsHash(b) {
					return satArchiveError077(ctx, fmt.Errorf("archive checkpoint authentication"))
				}
				if err = newOrdinalTree(filepath.Join(archive, "ordinal-pages"), b.Sats.FactPinRoot).withContext(ctx).walk(func(kv ordinalKV) error {
					var count uint64
					if !validHash(kv.Key) || json.Unmarshal(kv.Value, &count) != nil || count == 0 {
						return fmt.Errorf("archive fact pin shape")
					}
					return visit(kv.Key)
				}); err != nil {
					return satArchiveError077(ctx, err)
				}
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
func (s *indexStore) pruneSat077Commits(ctx context.Context) error {
	retained := map[string]bool{s.head.Commitment: true}
	for _, p := range s.head.SatSavepoints {
		retained[p.Commitment] = true
	}
	dir := filepath.Join(s.dir, "commits")
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
			commit := strings.TrimSuffix(entry.Name(), ".json")
			if !validHash(commit) || retained[commit] {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("Sat commits cleanup refuses linked files")
			}
			if info.ModTime().After(cutoff) {
				continue
			}
			if _, err = s.readBatch(commit); err != nil {
				return satArchiveError077(ctx, err)
			}
			path := filepath.Join(dir, entry.Name())
			if err = updateapply.CheckPath(path); err != nil {
				return err
			}
			if err = os.Remove(path); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return syncDirectory(dir)
}
