package updateapply

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"../cleanup"
)

const profileMoveStageName = "profile-move-stage.json"

// PrepareProfileMove detaches completed update staging from the active profile
// before its directory moves. The caller must hold the exclusive profile lock.
// Keep the returned guard open while preparing the move: a completed helper can
// still be writing its final result after clearing the active stage. Windows
// callers must release descendant handles immediately before the directory
// rename, under a separate migration coordinator, then lock the destination and
// revalidate it. Other platforms can retain this guard throughout the rename.
// Approved plans are retained byte-for-byte as historical evidence, never
// rewritten or treated as executable approvals for the new profile location.
func PrepareProfileMove(installDir, dataDir string) (guard io.Closer, err error) {
	if request, e := PendingRecovery(installDir, dataDir); e != nil {
		return nil, fmt.Errorf("cannot move Gateway data until update recovery is resolved: %w", e)
	} else if request != nil {
		return nil, fmt.Errorf("Gateway has an unfinished update; let update recovery finish before moving its data")
	}
	stagePath := filepath.Join(dataDir, "updates", "stage.json")
	archivePath := filepath.Join(dataDir, "updates", profileMoveStageName)
	raw, e := readSmall(stagePath, 64<<10)
	archived := false
	if os.IsNotExist(e) {
		// A previous attempt may have archived staging before its directory move
		// was interrupted. Repeat validation and helper exclusion on that retry.
		raw, e = readSmall(archivePath, 64<<10)
		archived = true
	}
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, fmt.Errorf("cannot read update staging before moving Gateway data: %w", e)
	}
	var stage struct {
		ManifestPath      string `json:"manifest_path"`
		PackagePath       string `json:"package_path"`
		ApplyPlanPath     string `json:"apply_plan_path"`
		LastApplyPlanPath string `json:"last_apply_plan_path"`
	}
	if e = json.Unmarshal(raw, &stage); e != nil {
		return nil, fmt.Errorf("invalid update staging before moving Gateway data: %w", e)
	}
	for _, path := range []string{stage.ManifestPath, stage.PackagePath, stage.ApplyPlanPath, stage.LastApplyPlanPath} {
		if path == "" {
			continue
		}
		root := filepath.Join(dataDir, "updates")
		if !filepath.IsAbs(path) || samePath(root, path) || !within(root, path) {
			return nil, fmt.Errorf("update staging path lies outside the profile updates directory")
		}
		if e = CheckPath(path); e != nil {
			return nil, e
		}
	}
	if stage.ApplyPlanPath != "" && stage.LastApplyPlanPath != "" && !samePath(stage.ApplyPlanPath, stage.LastApplyPlanPath) {
		return nil, fmt.Errorf("conflicting update transactions prevent moving Gateway data")
	}
	planPath := stage.ApplyPlanPath
	if planPath == "" {
		planPath = stage.LastApplyPlanPath
	}
	if planPath != "" {
		plan, e := ReadApprovedPlan(dataDir, planPath)
		if e != nil {
			return nil, fmt.Errorf("cannot authenticate the previous update before moving Gateway data: %w", e)
		}
		operation, e := cleanup.AcquireForMove(plan.WorkDir)
		if e != nil {
			return nil, fmt.Errorf("an update helper is still finishing; close Gateway and retry after it finishes: %w", e)
		}
		guard = operation
		defer func() {
			if err != nil {
				_ = operation.Close()
				guard = nil
			}
		}()
		// Authenticate again after acquiring the helper lock. A helper that just
		// exited must have left an entirely completed, verified transaction.
		plan, e = ReadApprovedPlan(dataDir, planPath)
		if e != nil {
			return guard, e
		}
		ledger, e := readJournal(plan)
		if e != nil {
			return guard, fmt.Errorf("cannot confirm completed update before moving Gateway data: %w", e)
		}
		if ledger.State != "rolled_back" && ledger.State != "succeeded" {
			return guard, fmt.Errorf("Gateway has an unfinished update; let update recovery finish before moving its data")
		}
		if stage.ApplyPlanPath != "" {
			expected := plan.CandidateFiles
			if ledger.State == "rolled_back" {
				expected = plan.OriginalFiles
			}
			if e = checkInstalledState(plan, expected); e != nil {
				return guard, fmt.Errorf("completed update no longer matches its authenticated files: %w", e)
			}
		}
		// A last-only pointer is historical. A later manual installer can have
		// legitimately replaced application files since that completed update;
		// its old payload must not prevent migration into the current profile.
	}
	if archived {
		return guard, nil
	}
	if e = CheckPath(archivePath); e != nil {
		return guard, e
	}
	if _, e = os.Lstat(archivePath); !os.IsNotExist(e) {
		if e == nil {
			e = fmt.Errorf("an earlier profile migration staging archive already exists")
		}
		return guard, e
	}
	// The profile lock excludes another client. Check the exact bytes once more
	// because the last helper may have completed between the first read and its
	// operation lock. A retry can safely consume that newly completed record.
	latest, e := readSmall(stagePath, 64<<10)
	if e != nil {
		return guard, e
	}
	if !bytes.Equal(latest, raw) {
		return guard, fmt.Errorf("update staging changed while preparing the data move; retry once the update finishes")
	}
	if e = os.Rename(stagePath, archivePath); e != nil {
		return guard, fmt.Errorf("cannot preserve update staging before moving Gateway data: %w", e)
	}
	return guard, nil
}
