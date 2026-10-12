package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func (a *app) timelineIndexedBodiesSource(headers *timelineLocalReader, problems *[]string) indexTimelineSource {
	source := timelineSource("gateway_indexed_raw_bodies", "Present private durable serialized Blocks bodies; full integrity is rechecked on use. Missing bytes remain distinct from saved transaction-location knowledge.")
	root := filepath.Join(a.dataDir, "indexes", "blocks", "bodies-v2")
	shards, complete, err := timelineDirectory(root, 256)
	source.Complete = complete
	if err != nil {
		*problems = append(*problems, "gateway_indexed_raw_bodies: inventory unavailable")
		return source
	}
	remaining := timelineSourceLimit
	for _, shard := range shards {
		if !shard.IsDir() || len(shard.Name()) != 2 {
			continue
		}
		if remaining <= 0 {
			source.Complete = false
			break
		}
		entries, complete, err := timelineDirectory(filepath.Join(root, shard.Name()), remaining)
		remaining -= len(entries)
		if err != nil || !complete {
			source.Complete = false
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".block.receipt.json") {
				continue
			}
			hash := strings.TrimSuffix(entry.Name(), ".block.receipt.json")
			if !validHash(hash) || !strings.HasPrefix(hash, shard.Name()) {
				continue
			}
			f, err := os.Open(filepath.Join(root, shard.Name(), entry.Name()))
			if err != nil {
				source.Complete = false
				continue
			}
			var inventory indexedRawInventory
			err = json.NewDecoder(io.LimitReader(f, 16<<10)).Decode(&inventory)
			f.Close()
			if err != nil || inventory.Schema != 2 || inventory.Hash != hash || inventory.Body == nil || inventory.Body.Bytes < 81 || inventory.Body.Bytes > maxMessageSize {
				source.Complete = false
				continue
			}
			if headers.matches(inventory.Height, hash) && timelineRawPresent(filepath.Join(root, shard.Name(), hash+".block"), hash, 0, int64(inventory.Body.Bytes), [8]byte{}) {
				source.Ranges = append(source.Ranges, heightInterval{inventory.Height, inventory.Height})
			}
		}
	}
	return source
}
