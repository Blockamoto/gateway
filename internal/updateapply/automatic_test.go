package updateapply

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"../cleanup"
)

func automaticFixturePlan(t *testing.T) (*fixture, *Plan) {
	t.Helper()
	f := newFixture(t, []byte("old runtime remains installed"))
	cfg, _ := json.Marshal(map[string]interface{}{"trusted_key": f.trusted, "minimum_sequence": uint64(1), "publisher_url": "http://127.0.0.1:1", "auto_check": true, "auto_install": true})
	mustWrite(t, ConfigPath(f.data), cfg)
	plan, err := Prepare(PrepareOptions{InstallDir: f.install, DataDir: f.data, PackagePath: f.archive, ManifestPath: f.envelope, CurrentVersion: "0.6.4", Platform: f.platform, OldPID: 2147483646, RestartArgs: []string{"-data", f.data, "-no-open"}, Automatic: true})
	if err != nil {
		t.Fatal(err)
	}
	return f, plan
}

func TestAutomaticPlanRequiresLatestSavedConsent(t *testing.T) {
	f, plan := automaticFixturePlan(t)
	if err := requireAutomaticConsent(plan); err != nil {
		t.Fatal("explicit current permission was rejected", err)
	}
	if err := os.Remove(ConfigPath(f.data)); err != nil {
		t.Fatal(err)
	}
	if err := requireAutomaticConsent(plan); err == nil {
		t.Fatal("missing permission allowed unattended application")
	}
	plan.Automatic = false
	if err := requireAutomaticConsent(plan); err != nil {
		t.Fatal("manual install unexpectedly required automatic permission", err)
	}
}

func TestAutomaticPlanFlagCannotBeStrippedFromApprovedBytes(t *testing.T) {
	_, plan := automaticFixturePlan(t)
	b, err := os.ReadFile(plan.PlanPath)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(b, []byte(`"automatic": true`), []byte(`"automatic": false`), 1)
	if bytes.Equal(b, changed) {
		t.Fatal("automatic requirement missing from authenticated plan")
	}
	if err = os.WriteFile(plan.PlanPath, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadPlan(plan.PlanPath, digestBytes(changed)); err == nil {
		t.Fatal("recomputed digest removed automatic permission requirement")
	}
}

func TestAutomaticHelperHonoursOptOutAfterOldRuntimeQuits(t *testing.T) {
	for _, mode := range []string{"withdrawn", "missing", "malformed", "checks_disabled"} {
		t.Run(mode, func(t *testing.T) {
			f, plan := automaticFixturePlan(t)
			old, err := cleanup.Acquire(f.data)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			stage := map[string]interface{}{"manifest_path": plan.ManifestPath, "package_path": plan.PackagePath, "apply_plan_path": plan.PlanPath, "last_apply_plan_path": plan.PlanPath}
			if err = saveJSON(filepath.Join(f.data, "updates", "stage.json"), stage); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			type completed struct {
				result Result
				err    error
			}
			done := make(chan completed, 1)
			go func() {
				result, runErr := Run(ctx, plan.PlanPath, plan.PlanSHA256)
				done <- completed{result, runErr}
			}()
			for {
				if _, err = os.Stat(ReadyPath(plan)); err == nil {
					break
				}
				select {
				case finished := <-done:
					t.Fatal("helper stopped before reaching the old process", finished.err)
				case <-ctx.Done():
					t.Fatal("helper did not prepare", ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			// The helper has already read permission once. Persist an opt-out,
			// then release the old writer as if it quit/crashed before it could
			// tell the waiting helper to stop.
			switch mode {
			case "missing":
				err = os.Remove(ConfigPath(f.data))
			case "malformed":
				err = os.WriteFile(ConfigPath(f.data), []byte(`{"auto_install":`), 0600)
			default:
				cfg := map[string]interface{}{"trusted_key": f.trusted, "minimum_sequence": uint64(1), "auto_check": mode != "checks_disabled", "auto_install": mode == "checks_disabled"}
				err = saveJSON(ConfigPath(f.data), cfg)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = old.Close(); err != nil {
				t.Fatal(err)
			}
			finished := <-done
			if finished.err == nil || !strings.Contains(finished.err.Error(), "automatic installation stopped") || finished.result.StartedPID != 0 {
				t.Fatalf("withdrawn automatic permission did not stop helper: %+v, %v", finished.result, finished.err)
			}
			for path, want := range map[string]string{filepath.Join(f.install, "README.txt"): "old", filepath.Join(f.data, "settings.json"): `{"private_settings":"preserve"}`, filepath.Join(f.install, RuntimeName(f.platform)): "old runtime remains installed"} {
				b, readErr := os.ReadFile(path)
				if readErr != nil || string(b) != want {
					t.Fatal("opt-out changed installed application or profile", path, readErr)
				}
			}
			if _, err = os.Stat(filepath.Join(plan.WorkDir, "journal.json")); !os.IsNotExist(err) {
				t.Fatal("opt-out reached mutation journal", err)
			}
			if recovery, err := PendingRecovery(f.install, f.data); err != nil || recovery != nil {
				t.Fatal("cancelled update left an automatic recovery attempt", recovery, err)
			}
		})
	}
}
