package main

// Shared ownership/archive lifecycle. Callers supply their own immutable
// identity, publication metadata, authority fence and generation mutex.
// Existing ordered review/marker bytes retain their original field order.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"./internal/updateapply"
)

type recipeGenerationRebuildMarker[T comparable] struct {
	Schema   int
	Identity T
	JobID    string
	Reviewed string
	Prior    bool
	Complete bool
	Checksum string
}

func recipeGenerationRebuildChecksum[T comparable](m recipeGenerationRebuildMarker[T]) string {
	m.Checksum = ""
	return indexDigest(m)
}

func recipeGuardedGenerationRebuild(ctx context.Context, jobID, reviewed string, authority recipeStoreGuard, reset func() error) error {
	if ctx == nil || authority == nil || reset == nil || strings.TrimSpace(jobID) == "" || len(jobID) > 4096 || !recipeStoreHashValid(reviewed) {
		return fmt.Errorf("rebuild requires its persistent job and reviewed generation")
	}
	select {
	case recipeOrderedWorkSlots <- struct{}{}:
		defer func() { <-recipeOrderedWorkSlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	return recipeStoreGuarded(authority, reset)
}

func recipeReviewGenerationMetadata[T comparable](ctx context.Context, profileRoot, profile string, identity T, paths []string) (string, error) {
	if err := updateapply.CheckPath(profileRoot); err != nil {
		return "", err
	}
	_, err := os.Stat(profileRoot)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	prior := err == nil
	sums := []string{}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := updateapply.CheckPath(path); err != nil {
			return "", err
		}
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			sums = append(sums, "missing")
			continue
		}
		if err != nil {
			return "", err
		}
		sum := sha256.New()
		buf := make([]byte, 64<<10)
		for {
			if err = ctx.Err(); err != nil {
				break
			}
			var n int
			n, err = f.Read(buf)
			if n > 0 {
				_, _ = sum.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		closeErr := f.Close()
		if err != io.EOF {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		sums = append(sums, hex.EncodeToString(sum.Sum(nil)))
	}
	return indexDigest(struct {
		Profile  string
		Identity T
		Exists   bool
		Files    []string
	}{profile, identity, prior, sums}), nil
}

func recipeResetGenerationForRebuild[T comparable](ctx context.Context, profileRoot string, identity T, semantic, jobID, reviewed string, review func(context.Context) (string, error)) error {
	parent := filepath.Dir(profileRoot)
	key := indexDigest(struct{ Semantic, Job string }{semantic, jobID})
	archives, markers := filepath.Join(parent, "archives"), filepath.Join(parent, "rebuilds")
	archive, path := filepath.Join(archives, key), filepath.Join(markers, key+".json")
	for _, target := range []string{profileRoot, archive, path} {
		// Computed moves must stay in this exact instance's directory.
		rel, err := filepath.Rel(parent, target)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return fmt.Errorf("recipe rebuild path escaped its instance")
		}
		if err := updateapply.CheckPath(target); err != nil {
			return err
		}
	}
	var marker recipeGenerationRebuildMarker[T]
	err := utxoReadJSON(ctx, path, 16<<10, &marker)
	if err == nil {
		if marker.Schema != 1 || marker.Identity != identity || marker.JobID != jobID || marker.Reviewed != reviewed || marker.Checksum != recipeGenerationRebuildChecksum(marker) {
			return fmt.Errorf("invalid recipe rebuild marker")
		}
		if marker.Complete {
			if marker.Prior {
				if info, err := os.Stat(archive); err != nil || !info.IsDir() {
					return fmt.Errorf("preserved recipe rebuild archive is missing: %v", err)
				}
			}
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		current, err := review(ctx)
		if err != nil {
			return err
		}
		if current != reviewed {
			return fmt.Errorf("recipe generation changed since rebuild review")
		}
		_, err = os.Stat(profileRoot)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		marker = recipeGenerationRebuildMarker[T]{Schema: 1, Identity: identity, JobID: jobID, Reviewed: reviewed, Prior: err == nil}
		marker.Checksum = recipeGenerationRebuildChecksum(marker)
		if err = atomicWriteJSON(path, marker); err != nil {
			return err
		}
		if err = syncDirectory(markers); err != nil {
			return err
		}
		if err = syncDirectory(parent); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if marker.Prior {
		if _, err = os.Stat(archive); os.IsNotExist(err) {
			// On an interrupted intent the original publication metadata must
			// still match review before it can be archived.
			current, err := review(ctx)
			if err != nil || current != reviewed {
				return fmt.Errorf("recipe rebuild source changed or disappeared: %v", err)
			}
			if err = os.MkdirAll(archives, 0755); err != nil {
				return err
			}
			if err = os.Rename(profileRoot, archive); err != nil {
				return err
			}
			if err = syncDirectory(archives); err != nil {
				return err
			}
			if err = syncDirectory(parent); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	} else {
		current, err := review(ctx)
		if err != nil || current != reviewed {
			return fmt.Errorf("recipe rebuild empty source changed: %v", err)
		}
	}
	marker.Complete = true
	marker.Checksum = recipeGenerationRebuildChecksum(marker)
	if err = atomicWriteJSON(path, marker); err != nil {
		return err
	}
	return syncDirectory(markers)
}
