package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"./internal/updateapply"
)

type utxoRebuildMarker struct {
	Schema    int    `json:"schema"`
	JobID     string `json:"job_id"`
	Complete  bool   `json:"complete"`
	PriorHead bool   `json:"prior_head"`
	Checksum  string `json:"checksum"`
}

func utxoRebuildDigest(m utxoRebuildMarker) string {
	m.Checksum = ""
	return indexDigest(m)
}

// Rebuild is an explicitly reviewed operation. Its persistent job marker makes
// retries idempotent: resuming the same job never archives its new prefix.
// Prior engine files are moved intact, including a corrupt head. Such an archive
// makes shared-fact collection fail closed until it can be inspected/repaired.
func resetUTXOForRebuild(ctx context.Context, root, jobID string) error {
	utxoWriterMu.Lock()
	defer utxoWriterMu.Unlock()
	utxoStateMu.Lock()
	defer utxoStateMu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	if strings.TrimSpace(jobID) == "" || len(jobID) > 4096 {
		return fmt.Errorf("UTXO rebuild requires a persistent job ID")
	}
	indexes := filepath.Join(root, "indexes")
	dir := filepath.Join(indexes, "utxo-state")
	archives := filepath.Join(indexes, "utxo-state-archives")
	key := indexDigest(jobID)
	archive := filepath.Join(archives, key)
	markers := filepath.Join(indexes, "utxo-rebuild-jobs")
	path := filepath.Join(markers, key+".json")
	for _, p := range []string{dir, archive, path} {
		if e := updateapply.CheckPath(p); e != nil {
			return e
		}
	}
	var marker utxoRebuildMarker
	e := utxoReadJSON(ctx, path, 16<<10, &marker)
	if e == nil {
		if marker.Schema != 1 || marker.JobID != jobID || marker.Checksum != utxoRebuildDigest(marker) {
			return fmt.Errorf("%w: invalid rebuild marker", ErrUTXOStateCorrupt)
		}
		if marker.Complete {
			return nil
		}
	} else if !os.IsNotExist(e) {
		return e
	} else {
		_, headErr := os.Stat(filepath.Join(dir, "head.json"))
		if headErr != nil && !os.IsNotExist(headErr) {
			return headErr
		}
		marker = utxoRebuildMarker{Schema: 1, JobID: jobID, PriorHead: headErr == nil}
		marker.Checksum = utxoRebuildDigest(marker)
		if e = atomicWriteJSON(path, marker); e != nil {
			return e
		}
		if e = syncDirectory(markers); e != nil {
			return e
		}
		if e = syncDirectory(indexes); e != nil {
			return e
		}
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	// An existing archive means the rename completed before an interrupted
	// marker publication. No new processing can begin until this call returns.
	if _, e = os.Stat(archive); os.IsNotExist(e) {
		if _, e = os.Stat(dir); e == nil {
			if e = os.MkdirAll(archives, 0755); e != nil {
				return e
			}
			if e = os.Rename(dir, archive); e != nil {
				return e
			}
			if e = syncDirectory(archives); e != nil {
				return e
			}
			if e = syncDirectory(indexes); e != nil {
				return e
			}
		} else if !os.IsNotExist(e) {
			return e
		}
	} else if e != nil {
		return e
	}
	if e = os.MkdirAll(dir, 0755); e != nil {
		return e
	}
	marker.Complete = true
	marker.Checksum = utxoRebuildDigest(marker)
	if e = atomicWriteJSON(path, marker); e != nil {
		return e
	}
	if e = syncDirectory(markers); e != nil {
		return e
	}
	return syncDirectory(indexes)
}

func (s *utxoStore) visitArchivedFactPages(ctx context.Context, visit func(string) error) error {
	dir := filepath.Join(s.root, "indexes", "utxo-state-archives")
	if e := updateapply.CheckPath(dir); e != nil {
		return e
	}
	f, e := os.Open(dir)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	defer f.Close()
	for {
		entries, readErr := f.ReadDir(64)
		for _, entry := range entries {
			if e = ctx.Err(); e != nil {
				return e
			}
			if !entry.IsDir() || !validHash(entry.Name()) {
				return fmt.Errorf("%w: invalid UTXO archive entry", ErrUTXOStateCorrupt)
			}
			old := &utxoStore{root: s.root, dir: filepath.Join(dir, entry.Name())}
			var marker utxoRebuildMarker
			markerPath := filepath.Join(s.root, "indexes", "utxo-rebuild-jobs", entry.Name()+".json")
			if e = utxoReadJSON(ctx, markerPath, 16<<10, &marker); e != nil || marker.Schema != 1 || marker.Checksum != utxoRebuildDigest(marker) || indexDigest(marker.JobID) != entry.Name() {
				return fmt.Errorf("%w: invalid UTXO archive marker (%v)", ErrUTXOStateCorrupt, e)
			}
			if e = old.reload(ctx); e != nil {
				return fmt.Errorf("archived UTXO state prevents fact collection: %w", e)
			}
			if marker.PriorHead && old.head == nil {
				return fmt.Errorf("%w: archived UTXO head is missing", ErrUTXOStateCorrupt)
			}
			if e = old.visitCurrentFactPages(ctx, visit); e != nil {
				return e
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
