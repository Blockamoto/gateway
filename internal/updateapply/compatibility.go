package updateapply

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
)

// ValidateCompatibility rejects automatic updates which require a durable
// storage format migration. Such releases need a separate migration process.
func ValidateCompatibility(packagePath, version, platform, dataDir string) error {
	return validateCompatibility(packagePath, version, platform, dataDir)
}

func validateCompatibility(packagePath, version, platform, dataDir string) error {
	current, e := readSmall(filepath.Join(dataDir, "compatibility.json"), 64<<10)
	if e != nil {
		return fmt.Errorf("profile compatibility metadata required: %w", e)
	}
	reader, e := zip.OpenReader(packagePath)
	if e != nil {
		return e
	}
	defer reader.Close()
	expected := "gateway-client-v" + version + "-" + platform + "/COMPATIBILITY.json"
	var candidate []byte
	for _, entry := range reader.File {
		if entry.Name == expected {
			if entry.UncompressedSize64 > 64<<10 {
				return fmt.Errorf("oversized compatibility metadata")
			}
			src, e := entry.Open()
			if e != nil {
				return e
			}
			candidate, e = io.ReadAll(io.LimitReader(src, 64<<10+1))
			ce := src.Close()
			if e != nil {
				return e
			}
			if ce != nil {
				return ce
			}
			break
		}
	}
	if len(candidate) == 0 {
		return fmt.Errorf("update compatibility metadata required")
	}
	var before, after map[string]json.RawMessage
	if e = json.Unmarshal(current, &before); e != nil {
		return e
	}
	if e = json.Unmarshal(candidate, &after); e != nil {
		return e
	}
	var app string
	if json.Unmarshal(after["app_version"], &app) != nil || app != version {
		return fmt.Errorf("package compatibility version differs from signed release")
	}
	// These are the persistent formats opened at startup. A future release which
	// changes them needs an explicit migration and recovery process, not this path.
	for _, field := range []string{"storage_schema", "header_schema", "index_schema", "cache_metadata_schema", "core_mount_schema", "graph_schema", "satline_storage_schema", "ord_storage_schema", "archive_storage_schema"} {
		var oldValue, newValue int
		if json.Unmarshal(before[field], &oldValue) != nil || json.Unmarshal(after[field], &newValue) != nil || oldValue <= 0 || oldValue != newValue {
			return fmt.Errorf("automatic update requires unchanged %s", field)
		}
	}
	var oldRaw, newRaw string
	if json.Unmarshal(before["raw_block_format"], &oldRaw) != nil || json.Unmarshal(after["raw_block_format"], &newRaw) != nil || oldRaw != "bitcoin-serialized-block" || oldRaw != newRaw {
		return fmt.Errorf("automatic update requires unchanged raw block format")
	}
	return nil
}
