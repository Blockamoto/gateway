package updateapply

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"../cleanup"
)

type RecoveryRequest struct {
	HelperPath string
	PlanPath   string
	PlanSHA256 string
	Busy       bool
}

// PendingRecovery is safe to call from the desktop launcher before starting the
// client. A running updater keeps the installation closed; an interrupted one
// is resumed only with authenticated plan, helper and signed package identities.
func PendingRecovery(installDir, dataDir string) (*RecoveryRequest, error) {
	path := filepath.Join(dataDir, "updates", "stage.json")
	b, e := readSmall(path, 64<<10)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var stage struct {
		ApplyPlanPath string `json:"apply_plan_path"`
	}
	if e = json.Unmarshal(b, &stage); e != nil {
		return nil, e
	}
	if stage.ApplyPlanPath == "" {
		return nil, nil
	}
	plan, e := ReadApprovedPlan(dataDir, stage.ApplyPlanPath)
	if e != nil {
		return nil, e
	}
	ledger, e := readJournal(plan)
	if e == nil && (ledger.State == "succeeded" || ledger.State == "rolled_back") {
		expected := plan.CandidateFiles
		if ledger.State == "rolled_back" {
			expected = plan.OriginalFiles
		}
		if e = checkInstalledState(plan, expected); e != nil {
			return nil, fmt.Errorf("completed update payload no longer matches its authenticated state: %w", e)
		}
		return nil, nil
	}
	if !samePath(plan.InstallDir, installDir) {
		return nil, fmt.Errorf("recovery installation mismatch")
	}
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	req := &RecoveryRequest{PlanPath: plan.PlanPath, PlanSHA256: plan.PlanSHA256, HelperPath: filepath.Join(plan.WorkDir, HelperName(plan.Platform))}
	// Persisted PIDs are not identities after a reboot. The helper operation lock
	// and exact profile writer lock establish actual ownership without killing
	// or trusting an unrelated process which reused a historical PID.
	operation, lockErr := cleanup.Acquire(plan.WorkDir)
	if lockErr != nil {
		req.Busy = true
		return req, nil
	}
	_ = operation.Close()
	profile, lockErr := cleanup.Acquire(plan.DataDir)
	if lockErr != nil {
		req.Busy = true
		return req, nil
	}
	_ = profile.Close()
	if _, _, e = verifyRecovery(plan); e != nil {
		return nil, e
	}
	digest, _, e := fileDigest(req.HelperPath, 256<<20)
	if e != nil || digest != plan.HelperSHA256 {
		return nil, fmt.Errorf("authenticated recovery helper unavailable")
	}
	return req, nil
}

func checkInstalledState(plan *Plan, expected map[string]string) error {
	for name, want := range expected {
		actual, _, e := fileDigest(filepath.Join(plan.InstallDir, filepath.FromSlash(name)), 256<<20)
		if want == "" && os.IsNotExist(e) {
			continue
		}
		if e != nil || actual != want {
			return fmt.Errorf("application file identity mismatch: %s", name)
		}
	}
	return nil
}

func readJournal(plan *Plan) (journal, error) {
	var ledger journal
	b, e := readSmall(filepath.Join(plan.WorkDir, "journal.json"), 64<<10)
	if e != nil {
		return ledger, e
	}
	if e = json.Unmarshal(b, &ledger); e != nil {
		return ledger, e
	}
	switch ledger.State {
	case "applying", "checking_startup", "rolling_back", "rolled_back", "succeeded":
	default:
		return ledger, fmt.Errorf("unknown recovery journal state")
	}
	seen := map[string]bool{}
	for _, row := range ledger.Files {
		if seen[row.Name] {
			return ledger, fmt.Errorf("duplicate recovery journal payload")
		}
		seen[row.Name] = true
		old, ok := plan.OriginalFiles[row.Name]
		if !ok || row.HadOriginal != (old != "") {
			return ledger, fmt.Errorf("recovery journal exceeds authenticated application plan")
		}
	}
	return ledger, nil
}

func copyBackup(source, destination, expected string) error {
	if e := CheckPath(source); e != nil {
		return e
	}
	if e := CheckPath(destination); e != nil {
		return e
	}
	info, e := os.Stat(source)
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("backup source not regular")
	}
	input, e := os.Open(source)
	if e != nil {
		return e
	}
	defer input.Close()
	output, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if e != nil {
		return e
	}
	_, e = io.Copy(output, io.LimitReader(input, 256<<20+1))
	if e == nil {
		e = output.Sync()
	}
	ce := output.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	hash, _, e := fileDigest(destination, 256<<20)
	if e != nil {
		return e
	}
	if hash != expected {
		return fmt.Errorf("backup differs from approved original")
	}
	return nil
}

// Recover restores a bounded interrupted transaction. A journal only describes
// progress; every replacement is authorized by the locally authenticated plan,
// independently signed release, and exact original/candidate file hashes.
func Recover(ctx context.Context, planPath, planDigest string) (result Result, retErr error) {
	plan, e := ReadPlan(planPath, planDigest)
	if e != nil {
		return result, e
	}
	operation, e := cleanup.Acquire(plan.WorkDir)
	if e != nil {
		return result, fmt.Errorf("another helper owns this recovery transaction: %w", e)
	}
	defer operation.Close()
	result = Result{Version: plan.Version, State: "rollback_failed", BackupDir: filepath.Join(plan.InstallDir, ".gateway-update-backup-"+filepath.Base(plan.WorkDir))}
	defer func() {
		result.CompletedAt = time.Now().UTC().Format(time.RFC3339)
		if retErr != nil {
			result.Message = retErr.Error()
		}
		_ = saveJSON(ResultPath(plan), result)
	}()
	_, artifact, e := verifyRecovery(plan)
	if e != nil {
		return result, e
	}
	if e = verifyPackage(plan.PackagePath, artifact); e != nil {
		return result, e
	}
	candidate, e := managedHashes(plan.PackagePath, plan.Version, plan.Platform)
	if e != nil {
		return result, e
	}
	if len(candidate) != len(plan.CandidateFiles) {
		return result, fmt.Errorf("recovery candidate file map mismatch")
	}
	for name, hash := range candidate {
		if plan.CandidateFiles[name] != hash {
			return result, fmt.Errorf("recovery payload authentication mismatch")
		}
	}
	if e = validateCompatibility(plan.PackagePath, plan.Version, plan.Platform, plan.DataDir); e != nil {
		return result, e
	}
	ledger, e := readJournal(plan)
	if os.IsNotExist(e) {
		for name, expected := range plan.OriginalFiles {
			actual, _, checkErr := fileDigest(filepath.Join(plan.InstallDir, filepath.FromSlash(name)), 256<<20)
			if expected == "" && os.IsNotExist(checkErr) {
				continue
			}
			if checkErr != nil || actual != expected {
				return result, fmt.Errorf("missing journal for modified application payload: %s", name)
			}
		}
		ledger = journal{State: "applying"}
		e = nil
	}
	if e != nil {
		return result, e
	}
	if ledger.State == "succeeded" || ledger.State == "rolled_back" {
		return result, fmt.Errorf("update transaction already completed")
	}
	if e = saveJSON(ReadyPath(plan), map[string]interface{}{"pid": os.Getpid(), "plan_sha256": plan.PlanSHA256}); e != nil {
		return result, e
	}
	// Recovery never kills arbitrary PIDs from a progress journal. Existing writers
	// must finish/close before the helper can acquire the same profile lock.
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	lock, e := waitForProfile(waitCtx, plan.DataDir)
	cancel()
	if e != nil {
		return result, e
	}
	defer lock.Close()
	// Validate every original backup and every current destination before changing
	// any file, so a modified backup cannot become executable through rollback.
	recorded := map[string]bool{}
	for _, row := range ledger.Files {
		recorded[row.Name] = true
	}
	for name, old := range plan.OriginalFiles {
		if recorded[name] {
			continue
		}
		actual, _, checkErr := fileDigest(filepath.Join(plan.InstallDir, filepath.FromSlash(name)), 256<<20)
		if old == "" && os.IsNotExist(checkErr) {
			continue
		}
		if checkErr != nil || actual != old {
			return result, fmt.Errorf("unjournaled application payload changed: %s", name)
		}
	}
	for _, row := range ledger.Files {
		destination := filepath.Join(plan.InstallDir, filepath.FromSlash(row.Name))
		backup := filepath.Join(result.BackupDir, filepath.FromSlash(row.Name))
		old := plan.OriginalFiles[row.Name]
		if e = CheckPath(destination); e != nil {
			return result, e
		}
		if e = CheckPath(backup); e != nil {
			return result, e
		}
		current, _, checkErr := fileDigest(destination, 256<<20)
		if checkErr != nil && !os.IsNotExist(checkErr) {
			return result, checkErr
		}
		if checkErr == nil && current != old && current != plan.CandidateFiles[row.Name] {
			return result, fmt.Errorf("application file changed after interrupted update: %s", row.Name)
		}
		backupHash, _, backupErr := fileDigest(backup, 256<<20)
		if row.HadOriginal {
			if backupErr == nil && backupHash != old {
				return result, fmt.Errorf("recovery backup digest mismatch: %s", row.Name)
			}
			if backupErr != nil && (!os.IsNotExist(backupErr) || current != old) {
				return result, fmt.Errorf("authenticated original backup missing: %s", row.Name)
			}
		} else if backupErr == nil {
			return result, fmt.Errorf("unexpected recovery backup: %s", row.Name)
		}
	}
	if e = restore(plan, result.BackupDir, ledger.Files); e != nil {
		return result, e
	}
	if e = checkInstalledState(plan, plan.OriginalFiles); e != nil {
		return result, e
	}
	ledger.State = "rolled_back"
	if e = saveJSON(filepath.Join(plan.WorkDir, "journal.json"), ledger); e != nil {
		return result, e
	}
	if e = completeStage(plan); e != nil {
		return result, fmt.Errorf("original files restored; recovery metadata could not be finalized: %w", e)
	}
	_ = lock.Close()
	old, e := startRuntime(plan, false)
	if e != nil {
		result.State = "rollback_restart_failed"
		return result, e
	}
	result.StartedPID = old.cmd.Process.Pid
	result.State = "rolled_back"
	result.Message = "Interrupted update recovered; previous application restored and restarted. Profile and indexes retained."
	return result, nil
}
