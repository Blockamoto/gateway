package updateapply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"../cleanup"
)

type Result struct {
	Version     string `json:"version"`
	State       string `json:"state"`
	Message     string `json:"message"`
	StartedPID  int    `json:"started_pid,omitempty"`
	BackupDir   string `json:"backup_dir,omitempty"`
	CompletedAt string `json:"completed_at"`
}
type changedFile struct {
	Name        string `json:"name"`
	HadOriginal bool   `json:"had_original"`
	Installed   bool   `json:"installed"`
}
type journal struct {
	State      string        `json:"state"`
	Files      []changedFile `json:"files"`
	StartedPID int           `json:"started_pid,omitempty"`
}

func saveJSON(path string, value interface{}) error {
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	if e = CheckPath(path); e != nil {
		return e
	}
	temp := path + ".tmp"
	if e = CheckPath(temp); e != nil {
		return e
	}
	f, e := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return replaceMetadata(temp, path)
}
func replaceMetadata(temp, path string) error {
	// Go uses MoveFileEx(REPLACE_EXISTING) on Windows. Retain the previous
	// journal if replacement fails; never create a deletion gap in metadata.
	return os.Rename(temp, path)
}

func waitForOld(ctx context.Context, plan *Plan) (*cleanup.Lock, error) {
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	for {
		if !processAlive(plan.OldPID) {
			if lock, e := cleanup.Acquire(plan.DataDir); e == nil {
				return lock, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("old runtime/profile did not close: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func waitForProfile(ctx context.Context, dataDir string) (*cleanup.Lock, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if lock, e := cleanup.Acquire(dataDir); e == nil {
			return lock, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("profile writer did not close: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// Run is the helper entry point. It repeats trust, signature, digest, layout,
// schema and current-binary checks before touching application files. Backups
// remain available after success; no profile data are part of the transaction.
func Run(ctx context.Context, planPath, planDigest string) (result Result, retErr error) {
	plan, e := ReadPlan(planPath, planDigest)
	if e != nil {
		return result, e
	}
	operation, e := cleanup.Acquire(plan.WorkDir)
	if e != nil {
		return result, fmt.Errorf("another helper owns this update transaction: %w", e)
	}
	defer operation.Close()
	// Place transactional payload and backups on the installation volume. Profiles
	// may live on a different disk; per-file Rename must remain atomic.
	transactionName := filepath.Base(plan.WorkDir)
	result = Result{Version: plan.Version, State: "failed", BackupDir: filepath.Join(plan.InstallDir, ".gateway-update-backup-"+transactionName)}
	defer func() {
		result.CompletedAt = time.Now().UTC().Format(time.RFC3339)
		if retErr != nil {
			result.Message = retErr.Error()
		}
		if e := saveJSON(ResultPath(plan), result); retErr == nil && e != nil {
			retErr = e
		}
	}()
	_, artifact, e := verify(plan)
	if e != nil {
		return result, e
	}
	if e = verifyPackage(plan.PackagePath, artifact); e != nil {
		return result, e
	}
	if e = ValidateArchive(plan.PackagePath, plan.Version, plan.Platform); e != nil {
		return result, e
	}
	if e = validateCompatibility(plan.PackagePath, plan.Version, plan.Platform, plan.DataDir); e != nil {
		return result, e
	}
	digest, _, e := fileDigest(filepath.Join(plan.InstallDir, RuntimeName(plan.Platform)), 256<<20)
	if e != nil {
		return result, e
	}
	if digest != plan.CurrentRuntimeSHA256 {
		return result, fmt.Errorf("current runtime changed since staging")
	}
	self, e := os.Executable()
	if e != nil {
		return result, e
	}
	if within(plan.InstallDir, self) {
		return result, fmt.Errorf("update helper must run outside application directory")
	}
	stage := filepath.Join(plan.InstallDir, ".gateway-update-stage-"+transactionName)
	for _, path := range []string{stage, result.BackupDir} {
		if within(plan.DataDir, path) {
			return result, fmt.Errorf("update transaction overlaps profile")
		}
		if e = CheckPath(path); e != nil {
			return result, e
		}
	}
	if e = os.Mkdir(stage, 0700); e != nil {
		return result, e
	}
	defer func() {
		if within(plan.InstallDir, stage) && !samePath(plan.InstallDir, stage) && CheckPath(stage) == nil {
			_ = os.RemoveAll(stage)
		}
	}()
	names, stagedHashes, e := extractArchive(plan.PackagePath, stage, plan.Version, plan.Platform, artifact)
	if e != nil {
		return result, e
	}
	if e = os.Mkdir(result.BackupDir, 0700); e != nil {
		return result, e
	}
	if e = saveJSON(ReadyPath(plan), map[string]interface{}{"pid": os.Getpid(), "plan_sha256": plan.PlanSHA256}); e != nil {
		return result, e
	}
	waitCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	lock, e := waitForOld(waitCtx, plan)
	cancel()
	if e != nil {
		return result, e
	}
	defer func() {
		if lock != nil {
			_ = lock.Close()
		}
	}()
	if e = requireAutomaticConsent(plan); e != nil {
		// Opting out never restarts or installs anything. Retire only this
		// transaction's pending pointer so the launcher cannot resume it.
		if clearErr := completeStage(plan); clearErr != nil {
			return result, fmt.Errorf("%v; pending update metadata could not be cleared: %w", e, clearErr)
		}
		return result, e
	}
	ledger := journal{State: "applying"}
	journalPath := filepath.Join(plan.WorkDir, "journal.json")
	rollback := func(cause error) (Result, error) {
		ledger.State = "rolling_back"
		_ = saveJSON(journalPath, ledger)
		if lock == nil {
			waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			for {
				lock, e = cleanup.Acquire(plan.DataDir)
				if e == nil {
					break
				}
				select {
				case <-waitCtx.Done():
					result.State = "rollback_blocked"
					return result, fmt.Errorf("%v; profile is busy; backups retained: %w", cause, waitCtx.Err())
				case <-time.After(100 * time.Millisecond):
				}
			}
		}
		if restoreErr := restore(plan, result.BackupDir, ledger.Files); restoreErr != nil {
			result.State = "rollback_failed"
			return result, fmt.Errorf("%v; rollback incomplete: %w", cause, restoreErr)
		}
		if e = checkInstalledState(plan, plan.OriginalFiles); e != nil {
			result.State = "rollback_failed"
			return result, fmt.Errorf("%v; previous application identity changed; restart refused: %w", cause, e)
		}
		ledger.State = "rolled_back"
		_ = saveJSON(journalPath, ledger)
		_ = completeStage(plan)
		_ = lock.Close()
		lock = nil
		old, e := startRuntime(plan, false)
		if e != nil {
			result.State = "rollback_restart_failed"
			return result, fmt.Errorf("%v; files restored but old client failed to restart: %w", cause, e)
		}
		result.StartedPID = old.cmd.Process.Pid
		result.State = "rolled_back"
		return result, fmt.Errorf("update failed; previous application restored and restarted: %w", cause)
	}
	// Recheck after shutdown, before replacement. If anything changed, restart
	// the preserved runtime rather than leaving a stopped client behind.
	_, artifact, e = verify(plan)
	if e != nil {
		return rollback(e)
	}
	if e = verifyPackage(plan.PackagePath, artifact); e != nil {
		return rollback(e)
	}
	if e = validateCompatibility(plan.PackagePath, plan.Version, plan.Platform, plan.DataDir); e != nil {
		return rollback(e)
	}
	for _, name := range names {
		digest, _, checkErr := fileDigest(filepath.Join(stage, filepath.FromSlash(name)), 256<<20)
		if checkErr != nil || digest != stagedHashes[name] || digest != plan.CandidateFiles[name] {
			return rollback(fmt.Errorf("extracted signed payload changed before application: %s", name))
		}
	}
	for name, hash := range plan.OriginalFiles {
		current, _, checkErr := fileDigest(filepath.Join(plan.InstallDir, filepath.FromSlash(name)), 256<<20)
		if hash == "" && os.IsNotExist(checkErr) {
			continue
		}
		if checkErr != nil || current != hash {
			return rollback(fmt.Errorf("approved original application file changed: %s", name))
		}
	}
	digest, _, e = fileDigest(filepath.Join(plan.InstallDir, RuntimeName(plan.Platform)), 256<<20)
	if e != nil || digest != plan.CurrentRuntimeSHA256 {
		return rollback(fmt.Errorf("current runtime changed before application"))
	}
	for _, name := range names {
		destination := filepath.Join(plan.InstallDir, filepath.FromSlash(name))
		source := filepath.Join(stage, filepath.FromSlash(name))
		backup := filepath.Join(result.BackupDir, filepath.FromSlash(name))
		if within(plan.DataDir, destination) {
			return rollback(fmt.Errorf("application payload would replace profile data"))
		}
		if e = CheckPath(destination); e != nil {
			return rollback(e)
		}
		if e = CheckPath(backup); e != nil {
			return rollback(e)
		}
		if e = os.MkdirAll(filepath.Dir(destination), 0755); e != nil {
			return rollback(e)
		}
		if e = os.MkdirAll(filepath.Dir(backup), 0700); e != nil {
			return rollback(e)
		}
		row := changedFile{Name: name}
		if st, statErr := os.Lstat(destination); statErr == nil {
			if !st.Mode().IsRegular() {
				return rollback(fmt.Errorf("application payload destination is not a regular file: %s", name))
			}
			row.HadOriginal = true
		} else if !os.IsNotExist(statErr) {
			return rollback(statErr)
		}
		ledger.Files = append(ledger.Files, row)
		if e = saveJSON(journalPath, ledger); e != nil {
			return rollback(e)
		}
		if row.HadOriginal {
			if e = copyBackup(destination, backup, plan.OriginalFiles[name]); e != nil {
				return rollback(e)
			}
		}
		if e = os.Rename(source, destination); e != nil {
			return rollback(e)
		}
		ledger.Files[len(ledger.Files)-1].Installed = true
		if e = saveJSON(journalPath, ledger); e != nil {
			return rollback(e)
		}
	}
	ledger.State = "checking_startup"
	if e = saveJSON(journalPath, ledger); e != nil {
		return rollback(e)
	}
	_ = lock.Close()
	lock = nil
	started, e := startRuntime(plan, true)
	if e != nil {
		return rollback(e)
	}
	result.StartedPID = started.cmd.Process.Pid
	ledger.StartedPID = result.StartedPID
	if e = saveJSON(journalPath, ledger); e != nil {
		_ = started.cmd.Process.Kill()
		<-started.done
		return rollback(e)
	}
	healthCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	e = waitHealthy(healthCtx, plan, started)
	cancel()
	if e != nil {
		_ = started.cmd.Process.Kill()
		select {
		case <-started.done:
		case <-time.After(10 * time.Second):
			return result, fmt.Errorf("new runtime could not be stopped for rollback: %w", e)
		}
		return rollback(e)
	}
	ledger.State = "succeeded"
	if e = saveJSON(journalPath, ledger); e != nil {
		return result, e
	}
	result.State = "installed"
	result.Message = "Signed update installed; new runtime acknowledged healthy startup. Profile and indexes retained."
	if e = completeStage(plan); e != nil {
		result.Message += " Recovery metadata could not be cleared: " + e.Error()
	}
	return result, nil
}

func completeStage(plan *Plan) error {
	path := filepath.Join(plan.DataDir, "updates", "stage.json")
	b, e := readSmall(path, 64<<10)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(b, &fields); e != nil {
		return e
	}
	var active string
	if raw, ok := fields["apply_plan_path"]; ok {
		if e = json.Unmarshal(raw, &active); e != nil {
			return e
		}
	}
	if active == "" {
		return nil
	}
	if !samePath(active, plan.PlanPath) {
		return fmt.Errorf("another application transaction owns stage metadata")
	}
	fields["apply_plan_path"] = json.RawMessage(`""`)
	fields["last_apply_plan_path"], e = json.Marshal(plan.PlanPath)
	if e != nil {
		return e
	}
	return saveJSON(path, fields)
}

func restore(plan *Plan, backupRoot string, files []changedFile) error {
	failures := []error{}
	for i := len(files) - 1; i >= 0; i-- {
		row := files[i]
		destination := filepath.Join(plan.InstallDir, filepath.FromSlash(row.Name))
		backup := filepath.Join(backupRoot, filepath.FromSlash(row.Name))
		if e := CheckPath(destination); e != nil {
			failures = append(failures, e)
			continue
		}
		if e := CheckPath(backup); e != nil {
			failures = append(failures, e)
			continue
		}
		original := plan.OriginalFiles[row.Name]
		backupHash, _, backupErr := fileDigest(backup, 256<<20)
		if row.HadOriginal {
			if backupErr != nil || backupHash != original {
				current, _, currentErr := fileDigest(destination, 256<<20)
				if currentErr == nil && current == original {
					continue
				}
				failures = append(failures, fmt.Errorf("authenticated rollback backup unavailable: %s", row.Name))
				continue
			}
			if e := os.Rename(backup, destination); e != nil {
				failures = append(failures, e)
			}
		} else {
			current, _, currentErr := fileDigest(destination, 256<<20)
			if os.IsNotExist(currentErr) {
				continue
			}
			if currentErr != nil || current != plan.CandidateFiles[row.Name] {
				failures = append(failures, fmt.Errorf("rollback refuses modified new payload: %s", row.Name))
				continue
			}
			if e := os.Remove(destination); e != nil {
				failures = append(failures, e)
			}
		}
	}
	return errors.Join(failures...)
}
