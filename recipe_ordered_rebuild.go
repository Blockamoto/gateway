package main

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

type recipeOrderedRebuildMarker struct {
	Schema   int
	Identity recipeOrderedGenerationIdentity
	JobID    string
	Reviewed string
	Prior    bool
	Complete bool
	Checksum string
}

func recipeOrderedRebuildChecksum(m recipeOrderedRebuildMarker) string {
	m.Checksum = ""
	return indexDigest(m)
}

// Only mutable publication metadata needs a review fingerprint. Immutable
// pages need not be traversed (and may be damaged); the rebuild preserves the
// entire old generation including its owned facts, without interpreting it.
func (g *recipeOrderedGeneration) reviewToken(ctx context.Context) (string, error) {
	if err := updateapply.CheckPath(g.profileRoot); err != nil {
		return "", err
	}
	_, err := os.Stat(g.profileRoot)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	prior := err == nil
	sums := []string{}
	for _, path := range []string{g.markerPath(), g.headPath()} {
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
		Identity recipeOrderedGenerationIdentity
		Exists   bool
		Files    []string
	}{"gateway-ordered-generation-review-v1", g.identity, prior, sums}), nil
}

// The work slot is taken before selected-header/run/registry authority to
// avoid waiting for a writer which itself needs that authority to publish.
// The generation mutex then fences publication and queries.
// No recursive removal occurs: even damaged prior state is preserved intact.
func (g *recipeOrderedGeneration) resetForRebuild(ctx context.Context, jobID, reviewed string, authority recipeStoreGuard) error {
	if ctx == nil || authority == nil || strings.TrimSpace(jobID) == "" || len(jobID) > 4096 || !recipeStoreHashValid(reviewed) {
		return fmt.Errorf("ordered rebuild requires its persistent job and reviewed generation")
	}
	select {
	case recipeOrderedWorkSlots <- struct{}{}:
		defer func() { <-recipeOrderedWorkSlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	completed := false
	err := authority(func() error {
		if completed {
			return fmt.Errorf("ordered rebuild authority invoked twice")
		}
		if err := g.resetForRebuildLocked(ctx, jobID, reviewed); err != nil {
			return err
		}
		completed = true
		return nil
	})
	if err == nil && !completed {
		return fmt.Errorf("ordered rebuild authority returned without resetting")
	}
	return err
}

func (g *recipeOrderedGeneration) resetForRebuildLocked(ctx context.Context, jobID, reviewed string) error {
	recipeOrderedGenerationMu.Lock()
	defer recipeOrderedGenerationMu.Unlock()
	parent := filepath.Dir(g.profileRoot)
	key := indexDigest(struct{ Semantic, Job string }{g.identity.Semantic, jobID})
	archives, markers := filepath.Join(parent, "archives"), filepath.Join(parent, "rebuilds")
	archive, path := filepath.Join(archives, key), filepath.Join(markers, key+".json")
	for _, target := range []string{g.profileRoot, archive, path} {
		// Computed moves must stay in this exact instance's directory.
		rel, err := filepath.Rel(parent, target)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return fmt.Errorf("ordered rebuild path escaped its instance")
		}
		if err := updateapply.CheckPath(target); err != nil {
			return err
		}
	}
	var marker recipeOrderedRebuildMarker
	err := utxoReadJSON(ctx, path, 16<<10, &marker)
	if err == nil {
		if marker.Schema != 1 || marker.Identity != g.identity || marker.JobID != jobID || marker.Reviewed != reviewed || marker.Checksum != recipeOrderedRebuildChecksum(marker) {
			return fmt.Errorf("invalid ordered rebuild marker")
		}
		if marker.Complete {
			if marker.Prior {
				if info, err := os.Stat(archive); err != nil || !info.IsDir() {
					return fmt.Errorf("preserved ordered rebuild archive is missing: %v", err)
				}
			}
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		current, err := g.reviewToken(ctx)
		if err != nil {
			return err
		}
		if current != reviewed {
			return fmt.Errorf("ordered generation changed since rebuild review")
		}
		_, err = os.Stat(g.profileRoot)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		marker = recipeOrderedRebuildMarker{Schema: 1, Identity: g.identity, JobID: jobID, Reviewed: reviewed, Prior: err == nil}
		marker.Checksum = recipeOrderedRebuildChecksum(marker)
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
			current, err := g.reviewToken(ctx)
			if err != nil || current != reviewed {
				return fmt.Errorf("ordered rebuild source changed or disappeared: %v", err)
			}
			if err = os.MkdirAll(archives, 0755); err != nil {
				return err
			}
			if err = os.Rename(g.profileRoot, archive); err != nil {
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
		current, err := g.reviewToken(ctx)
		if err != nil || current != reviewed {
			return fmt.Errorf("ordered rebuild empty source changed: %v", err)
		}
	}
	marker.Complete = true
	marker.Checksum = recipeOrderedRebuildChecksum(marker)
	if err = atomicWriteJSON(path, marker); err != nil {
		return err
	}
	return syncDirectory(markers)
}
