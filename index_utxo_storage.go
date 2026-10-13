package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"./internal/updateapply"
)

// Storage inspection is explicit and bounded. Automatic status only reads a
// completed measurement for the same atomic checkpoint; it never walks files.
type indexUTXOStorageReport struct {
	indexStorageReport
	Checkpoint string `json:"checkpoint,omitempty"`
	Revision   string `json:"revision,omitempty"`
	MeasuredAt string `json:"measured_at"`
	TotalBytes int64  `json:"total_bytes"`
	Note       string `json:"note"`
}

var utxoStorageMeasurements = struct {
	sync.Mutex
	ByRoot map[string]indexUTXOStorageReport
}{ByRoot: map[string]indexUTXOStorageReport{}}

func cachedUTXOStorage(root, revision string) (indexUTXOStorageReport, bool) {
	utxoStorageMeasurements.Lock()
	defer utxoStorageMeasurements.Unlock()
	measurement, ok := utxoStorageMeasurements.ByRoot[root]
	return measurement, ok && measurement.State == "complete" && measurement.Revision == revision
}

func (a *app) inspectUTXOStorage(ctx context.Context) (indexUTXOStorageReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	s, err := openUTXOStoreContext(ctx, a.dataDir)
	if err != nil {
		return indexUTXOStorageReport{}, err
	}
	report := indexUTXOStorageReport{indexStorageReport: indexStorageReport{State: "complete", Bytes: map[string]int64{}, Limit: indexStorageFileLimit, EntryLimit: indexStorageEntryLimit}, MeasuredAt: time.Now().UTC().Format(time.RFC3339), Note: "File bytes for current engine, preserved rebuild archives, and their shared output pages counted once. Bridge and live membership pages are included in engine files. Raw blocks and optional TXIO-only history pages are excluded; shared pages may also serve TXIO. A partial result is a lower bound."}
	if cp := s.Head(); cp != nil {
		report.Checkpoint = cp.Commitment
		report.Revision = s.head.Checksum
	}
	partial := func(reason string) {
		report.State = "partial"
		if len(report.Errors) < 8 {
			report.Errors = append(report.Errors, reason)
		}
	}
	budget := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if report.Files >= report.Limit || report.Entries >= report.EntryLimit {
			return fmt.Errorf("storage inspection budget reached")
		}
		return nil
	}
	for _, group := range []struct{ relative, category string }{{"indexes/utxo-state", "engine_files"}, {"indexes/utxo-state-archives", "archived_engine_files"}, {"indexes/utxo-rebuild-jobs", "rebuild_metadata"}} {
		report.Bytes[group.category] = 0
		root := filepath.Join(a.dataDir, filepath.FromSlash(group.relative))
		if _, err := os.Lstat(root); os.IsNotExist(err) {
			continue
		}
		if err := updateapply.CheckPath(root); err != nil {
			partial(group.relative + ": unsafe storage path")
			continue
		}
		err := walkIndexStorage(ctx, root, func(path string, entry fs.DirEntry, entryErr error) error {
			if err := budget(); err != nil {
				return err
			}
			report.Entries++
			if entryErr != nil || entry == nil {
				partial(group.relative + ": unreadable entry")
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				partial(group.relative + ": symbolic link excluded")
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				partial(group.relative + ": nonregular or unreadable file")
				return nil
			}
			report.Files++
			report.Bytes[group.category] += info.Size()
			return nil
		}, 0)
		if err != nil {
			partial(err.Error())
			break
		}
	}
	report.Bytes["shared_output_fact_pages"] = 0
	remaining := indexStorageReadLimit
	seen := map[string]bool{}
	if report.State == "complete" {
		err = s.VisitFactPages(ctx, func(hash string) error {
			if err := budget(); err != nil {
				return err
			}
			report.Entries++
			if seen[hash] {
				return nil
			}
			seen[hash] = true
			path, err := outputFactPath(a.dataDir, hash)
			if err != nil {
				return err
			}
			if err = updateapply.CheckPath(path); err != nil {
				return err
			}
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("shared output page is unavailable")
			}
			if info.Size() > remaining {
				return fmt.Errorf("shared output page read budget reached")
			}
			if _, err = readOutputFactPageContext(ctx, a.dataDir, hash); err != nil {
				return err
			}
			remaining -= info.Size()
			report.Files++
			report.Bytes["shared_output_fact_pages"] += info.Size()
			return nil
		})
		if err != nil {
			partial(err.Error())
		}
	}
	for _, bytes := range report.Bytes {
		report.TotalBytes += bytes
	}
	current, err := utxoStoreHead(a.dataDir)
	if err != nil {
		partial("state became unavailable during inspection")
	} else if cp := current.Head(); cp == nil && report.Checkpoint != "" || cp != nil && current.head.Checksum != report.Revision {
		partial("state checkpoint changed during inspection; refresh after processing pauses")
	}
	if report.State == "complete" {
		utxoStorageMeasurements.Lock()
		if len(utxoStorageMeasurements.ByRoot) >= 128 {
			utxoStorageMeasurements.ByRoot = map[string]indexUTXOStorageReport{}
		}
		utxoStorageMeasurements.ByRoot[a.dataDir] = report
		utxoStorageMeasurements.Unlock()
	}
	return report, nil
}
