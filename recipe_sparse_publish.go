package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"./internal/updateapply"
)

// Hash an immutable file through its checked descriptor, without a whole-unit
// buffer. Expected extent and the content digest are both part of the receipt.
func recipeSparseVerifyFile(ctx context.Context, path, digest string, size int64) error {
	if ctx == nil || !recipeStoreHashValid(digest) || size < 0 {
		return fmt.Errorf("invalid sparse file receipt")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return fmt.Errorf("sparse unit extent differs from its receipt")
	}
	hash := sha256.New()
	n, err := io.CopyBuffer(recipeJSONContextWriter{ctx: ctx, w: hash}, file, make([]byte, 64<<10))
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(hash.Sum(nil)) != digest {
		return fmt.Errorf("sparse unit checksum mismatch")
	}
	return ctx.Err()
}

func recipeSparseVerifyStagedHeader(ctx context.Context, unit recipeSparseStagedUnit) error {
	if len(unit.Header.Rows) != 0 {
		return fmt.Errorf("streamed unit receipt contains materialized rows")
	}
	header := unit.Header
	header.Rows = []recipeStoreRow{}
	if err := recipeStoreValidateUnit(header, header.Fingerprint); err != nil {
		return err
	}
	data, err := json.Marshal(header)
	if err != nil {
		return err
	}
	start := bytes.Index(data, []byte(`"rows":[]`))
	if start < 0 {
		return fmt.Errorf("invalid canonical sparse header")
	}
	start += len(`"rows":`)
	prefix, suffix := data[:start+1], data[start+1:]
	if err := updateapply.CheckPath(unit.Path); err != nil {
		return err
	}
	file, err := os.Open(unit.Path)
	if err != nil {
		return err
	}
	defer file.Close()
	if int64(len(prefix)+len(suffix)) > unit.Bytes {
		return fmt.Errorf("staged unit is shorter than its header")
	}
	got := make([]byte, len(prefix))
	if _, err = file.ReadAt(got, 0); err != nil {
		return err
	}
	if !bytes.Equal(got, prefix) {
		return fmt.Errorf("staged sparse prefix differs from its claimed header")
	}
	got = make([]byte, len(suffix))
	if _, err = file.ReadAt(got, unit.Bytes-int64(len(suffix))); err != nil {
		return err
	}
	if !bytes.Equal(got, suffix) {
		return fmt.Errorf("staged sparse lineage/footer differs from its claimed header")
	}
	return recipeSparseVerifyFile(ctx, unit.Path, unit.Digest, unit.Bytes)
}

// This is data staging, not checkpoint publication. Immutable bytes may be
// installed before the authority fence; a failed/stale/canceled publication
// leaves them unreachable. The head is still the sole visibility boundary.
func recipeStorePutImmutableFile(ctx context.Context, dir string, unit recipeSparseStagedUnit) error {
	path := filepath.Join(dir, unit.Digest+".json")
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		if err := recipeSparseVerifyFile(ctx, path, unit.Digest, unit.Bytes); err != nil {
			return err
		}
		return syncDirectory(dir)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// The spool normally lives on this profile's volume, so its already-synced
	// file can be linked without another payload copy. An alternate volume
	// falls back to a checked, synced temporary copy beside the destination.
	err := os.Link(unit.Path, path)
	if err != nil {
		if _, exists := os.Stat(path); exists == nil {
			if err := recipeSparseVerifyFile(ctx, path, unit.Digest, unit.Bytes); err != nil {
				return err
			}
			return syncDirectory(dir)
		}
		file, copyErr := os.CreateTemp(dir, ".recipe-unit-*.tmp")
		if copyErr != nil {
			return copyErr
		}
		temporary := file.Name()
		defer os.Remove(temporary)
		source, copyErr := os.Open(unit.Path)
		if copyErr != nil {
			_ = file.Close()
			return copyErr
		}
		hash := sha256.New()
		n, copyErr := io.CopyBuffer(recipeJSONContextWriter{ctx: ctx, w: io.MultiWriter(file, hash)}, source, make([]byte, 64<<10))
		_ = source.Close()
		if copyErr == nil && (n != unit.Bytes || hex.EncodeToString(hash.Sum(nil)) != unit.Digest) {
			copyErr = fmt.Errorf("staged unit changed during immutable copy")
		}
		if copyErr == nil {
			copyErr = file.Sync()
		}
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err = os.Link(temporary, path); err != nil {
			if verifyErr := recipeSparseVerifyFile(ctx, path, unit.Digest, unit.Bytes); verifyErr != nil {
				return fmt.Errorf("publish immutable sparse unit: %w", err)
			}
		}
	}
	return syncDirectory(dir)
}

// Sparse source consumers stage payloads before entering the authority fence.
// Legacy materialized helpers remain explicitly bounded internal utilities.
// Payload I/O completes outside header/run/registry locks.
func (s *recipeStore) publishSparseStaged(ctx context.Context, unit recipeSparseStagedUnit, revision uint64, guard recipeStoreGuard, check func() error) error {
	if s == nil || ctx == nil || guard == nil || unit.Header.Fingerprint != s.fingerprint || !recipeStoreHashValid(unit.Digest) {
		return fmt.Errorf("staged sparse publication identity differs")
	}
	if len(unit.Header.Lineage) > 0 && check == nil {
		return fmt.Errorf("dependency lineage requires a guarded store transaction")
	}
	if err := recipeSparseVerifyStagedHeader(ctx, unit); err != nil {
		return err
	}
	if err := recipeStorePutImmutableFile(ctx, filepath.Join(s.dir, "units"), unit); err != nil {
		return err
	}
	return recipeStoreGuarded(guard, func() error {
		recipeStoreMutex.Lock()
		defer recipeStoreMutex.Unlock()
		if err := s.rejectReconcileFenceLocked(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if check != nil {
			if err := check(); err != nil {
				return err
			}
		}
		head, err := s.head()
		if err != nil {
			return err
		}
		if head.Revision != revision {
			return fmt.Errorf("recipe coverage changed since planning")
		}
		head, err = s.initialize(head)
		if err != nil {
			return err
		}
		if err := updateapply.CheckPath(filepath.Join(s.dir, "pages")); err != nil {
			return err
		}
		tree := s.tree(ctx, head)
		key := recipeStoreHeightKey(unit.Header.Height)
		var previous recipeStoreRef
		found, err := tree.get(key, &previous)
		if err != nil {
			return err
		}
		if found {
			if previous.BlockHash != unit.Header.BlockHash || previous.Unit != unit.Digest {
				return fmt.Errorf("recipe height already published; reconcile before replacement")
			}
			return nil
		}
		if err := tree.put(key, recipeStoreRef{BlockHash: unit.Header.BlockHash, Unit: unit.Digest}); err != nil {
			return err
		}
		root, err := tree.flush()
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return s.publishHead(head, root)
	})
}
