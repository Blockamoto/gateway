package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const ordContentCacheMaxBytes = 256 * 1024 * 1024
const ordContentCacheMaxEntries = 4096

// This cache is distinct from committed Full inscription shards. Eviction
// deletes only validated ID filenames inside data/ord, never index records or
// Bitcoin source retention. Lean derivation does not call saveOrdRecord.
func (a *app) pruneOrdContentCache(keep string) error {
	entries, err := os.ReadDir(a.ordRoot())
	if err != nil {
		return err
	}
	type record struct {
		id   string
		size int64
		at   time.Time
	}
	records := []record{}
	var bytes int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".bin") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".bin")
		if !isInscriptionID(id) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		records = append(records, record{id, info.Size(), info.ModTime()})
		bytes += info.Size()
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].at.Equal(records[j].at) {
			return records[i].id < records[j].id
		}
		return records[i].at.Before(records[j].at)
	})
	count := len(records)
	for _, record := range records {
		if bytes <= ordContentCacheMaxBytes && count <= ordContentCacheMaxEntries {
			break
		}
		if record.id == keep {
			continue
		}
		// ID validation above makes these direct children, with no computed tree
		// deletion or recursive filesystem operation.
		if err := os.Remove(filepath.Join(a.ordRoot(), record.id+".json")); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("bounded inscription cache eviction: %w", err)
		}
		if err := os.Remove(filepath.Join(a.ordRoot(), record.id+".bin")); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("bounded inscription cache eviction: %w", err)
		}
		bytes -= record.size
		count--
	}
	if bytes > ordContentCacheMaxBytes || count > ordContentCacheMaxEntries {
		return fmt.Errorf("inscription content cache exceeds its limit")
	}
	return nil
}

func (a *app) ordContentCacheStatus() map[string]any {
	entries, _ := os.ReadDir(a.ordRoot())
	var bytes int64
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".bin") || !isInscriptionID(strings.TrimSuffix(entry.Name(), ".bin")) {
			continue
		}
		if info, err := entry.Info(); err == nil && info.Mode().IsRegular() {
			bytes += info.Size()
			count++
		}
	}
	return map[string]any{"entries": count, "bytes": bytes, "max_bytes": ordContentCacheMaxBytes, "max_entries": ordContentCacheMaxEntries, "eviction": "oldest_on_demand_write", "scope": "on_demand_content; separate from committed Full indexes"}
}
