package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// An explicit inspection, never a status-refresh disk walk. Categories count
// file bytes, including stale/orphan commits still occupying disk. JSON value
// spans separate retained coordinate/body bytes from checkpoint overhead.
type indexStorageReport struct {
	State      string           `json:"state"`
	Bytes      map[string]int64 `json:"bytes"`
	Files      int              `json:"files_inspected"`
	Limit      int              `json:"file_limit"`
	Entries    int              `json:"entries_inspected"`
	EntryLimit int              `json:"entry_limit"`
	Errors     []string         `json:"limitations,omitempty"`
}

const indexStorageFileLimit = 8192
const indexStorageEntryLimit = indexStorageFileLimit * 2
const indexStorageReadLimit int64 = 64 << 20

// ReadDir in small batches avoids allocating a directory listing proportional
// to a large block cache or whole-chain index before applying the file budget.
func walkIndexStorage(ctx context.Context, path string, visit func(string, fs.DirEntry, error) error, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > 8 {
		return fmt.Errorf("storage directory depth exceeds inspection budget")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return visit(path, nil, err)
	}
	entry := fs.FileInfoToDirEntry(info)
	if err = visit(path, entry, nil); err != nil {
		return err
	}
	if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
		return nil
	}
	dir, err := os.Open(path)
	if err != nil {
		return visit(path, entry, err)
	}
	defer dir.Close()
	for {
		entries, err := dir.ReadDir(128)
		for _, child := range entries {
			if err := walkIndexStorage(ctx, filepath.Join(path, child.Name()), visit, depth+1); err != nil {
				return err
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return visit(path, entry, err)
		}
	}
}

func (a *app) indexStorage(ctx context.Context, id string) (any, error) {
	if err := requireReleaseFeature(id); err != nil {
		return nil, err
	}
	if id != "inscriptions" {
		return nil, fmt.Errorf("storage inspection currently describes Inscriptions")
	}
	report := indexStorageReport{State: "complete", Bytes: map[string]int64{}, Limit: indexStorageFileLimit, EntryLimit: indexStorageEntryLimit}
	for _, category := range []string{"coordinates", "full_reveal_records", "inscription_checkpoint_overhead", "inscription_reverse_shards", "unclassified_inscription_commits", "related_transaction_records", "related_transaction_reverse_shards", "source_block_cache", "resolved_content_cache"} {
		report.Bytes[category] = 0
	}
	remaining := indexStorageReadLimit
	partial := func(reason string) {
		report.State = "partial"
		if len(report.Errors) < 8 {
			report.Errors = append(report.Errors, reason)
		}
	}
	for _, group := range []struct{ relative, category string }{{"indexes/inscriptions", "inscription_reverse_shards"}, {"indexes/" + inscriptionLocatorIndex, "related_transaction_reverse_shards"}, {"blocks", "source_block_cache"}, {"ord", "resolved_content_cache"}} {
		root := filepath.Join(a.dataDir, filepath.FromSlash(group.relative))
		if _, err := os.Lstat(root); os.IsNotExist(err) {
			continue
		} else if err != nil {
			partial(group.relative + ": unavailable")
			continue
		}
		err := walkIndexStorage(ctx, root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				partial(group.relative + ": unreadable entry")
				if entry != nil && entry.IsDir() {
					return nil
				}
				return nil
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			if report.Files >= indexStorageFileLimit {
				return fmt.Errorf("inspection file budget reached")
			}
			if report.Entries >= indexStorageEntryLimit {
				return fmt.Errorf("inspection directory entry budget reached")
			}
			report.Entries++
			if entry.Type()&os.ModeSymlink != 0 {
				partial(group.relative + ": symbolic link excluded")
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				partial(group.relative + ": file unavailable")
				return nil
			}
			report.Files++
			category := group.category
			relative, err := filepath.Rel(root, path)
			if err != nil {
				partial("storage path unavailable")
				return nil
			}
			if group.relative == "indexes/inscriptions" && !strings.HasPrefix(relative, "occurrences-v1"+string(filepath.Separator)) {
				category = "inscription_checkpoint_overhead"
			}
			if group.relative == "indexes/"+inscriptionLocatorIndex && !strings.HasPrefix(relative, "locators-v1"+string(filepath.Separator)) {
				category = "related_transaction_records"
			}
			if group.relative == "indexes/"+inscriptionLocatorIndex && filepath.Base(filepath.Dir(path)) == "commits" {
				category = "related_transaction_records"
			}
			if group.relative != "indexes/inscriptions" || filepath.Base(filepath.Dir(path)) != "commits" || !strings.HasSuffix(path, ".json") {
				report.Bytes[category] += info.Size()
				return nil
			}
			if info.Size() > remaining {
				partial("inscription commit read budget reached")
				report.Bytes["unclassified_inscription_commits"] += info.Size()
				return nil
			}
			f, err := os.Open(path)
			if err != nil {
				partial("inscription commit unavailable")
				return nil
			}
			raw, readErr := io.ReadAll(io.LimitReader(f, remaining+1))
			closeErr := f.Close()
			if readErr != nil || closeErr != nil || int64(len(raw)) != info.Size() {
				partial("inscription commit changed or could not be read")
				return nil
			}
			remaining -= int64(len(raw))
			var fields map[string]json.RawMessage
			if err = json.Unmarshal(raw, &fields); err != nil {
				partial("inscription commit malformed")
				report.Bytes["unclassified_inscription_commits"] += info.Size()
				return nil
			}
			coordinates, full := int64(len(fields["inscriptions"])), int64(len(fields["full_inscriptions"]))
			// Legacy inscriptions arrays contain full objects, not coordinates.
			if value := strings.TrimSpace(string(fields["inscriptions"])); strings.HasPrefix(value, "[") && strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(value, "[")), "{") {
				full += coordinates
				coordinates = 0
			}
			report.Bytes["coordinates"] += coordinates
			report.Bytes["full_reveal_records"] += full
			report.Bytes["inscription_checkpoint_overhead"] += info.Size() - coordinates - full
			return nil
		}, 0)
		if err != nil {
			partial(err.Error())
			break
		}
	}
	return report, nil
}
