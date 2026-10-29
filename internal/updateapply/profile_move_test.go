package updateapply

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"../cleanup"
)

func profileMoveFixture(t *testing.T, state string, active bool) (*fixture, *Plan, []byte) {
	t.Helper()
	f := newFixture(t, []byte("fixture runtime"))
	plan, err := f.prepare(t, 2147483646)
	if err != nil {
		t.Fatal(err)
	}
	if state == "succeeded" {
		for name := range plan.CandidateFiles {
			mustWrite(t, filepath.Join(f.install, filepath.FromSlash(name)), f.payload[name])
		}
	}
	if err = saveJSON(filepath.Join(plan.WorkDir, "journal.json"), journal{State: state}); err != nil {
		t.Fatal(err)
	}
	stage := map[string]string{"manifest_path": f.envelope, "package_path": f.archive, "last_apply_plan_path": plan.PlanPath}
	if active {
		stage["apply_plan_path"] = plan.PlanPath
	}
	raw, _ := json.Marshal(stage)
	mustWrite(t, filepath.Join(f.data, "updates", "stage.json"), raw)
	return f, plan, raw
}

func TestPrepareProfileMovePreservesAuthenticatedHistoryAcrossRename(t *testing.T) {
	for _, state := range []string{"succeeded", "rolled_back"} {
		t.Run(state, func(t *testing.T) {
			f, plan, stage := profileMoveFixture(t, state, state == "succeeded")
			planBytes, _ := os.ReadFile(plan.PlanPath)
			approvalBytes, _ := os.ReadFile(filepath.Join(plan.WorkDir, "approval.json"))
			configBytes, _ := os.ReadFile(ConfigPath(f.data))
			keyBytes, _ := os.ReadFile(approvalKeyPath(f.data))
			profile, err := cleanup.AcquireForMove(f.data)
			if err != nil {
				t.Fatal(err)
			}
			defer profile.Close()
			guard, err := PrepareProfileMove(f.install, f.data)
			if err != nil || guard == nil {
				t.Fatalf("prepare move: %v; guard %v", err, guard)
			}
			defer guard.Close()
			if other, err := cleanup.Acquire(plan.WorkDir); err == nil {
				other.Close()
				t.Fatal("completed helper was not excluded throughout the move")
			}
			if runtime.GOOS == "windows" {
				// Windows refuses a directory rename while any descendant file is
				// open, even with delete sharing. Production migration serializes
				// clients outside the profile and releases these immediately before
				// rename, which still refuses any raced-in active profile writer.
				if err = guard.Close(); err != nil {
					t.Fatal(err)
				}
				if err = profile.Close(); err != nil {
					t.Fatal(err)
				}
			}
			newData := filepath.Join(f.root, "Gateway", "data")
			if err = os.MkdirAll(filepath.Dir(newData), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.Rename(f.data, newData); err != nil {
				t.Fatalf("profile and helper guards prevented safe directory rename: %v", err)
			}
			if runtime.GOOS == "windows" {
				movedProfile, err := cleanup.Acquire(newData)
				if err != nil {
					t.Fatalf("cannot regain exclusive moved-profile ownership: %v", err)
				}
				defer movedProfile.Close()
			}
			newWorkDir := filepath.Join(newData, "updates", "apply", filepath.Base(plan.WorkDir))
			for path, want := range map[string][]byte{
				filepath.Join(newData, "updates", profileMoveStageName): stage,
				filepath.Join(newWorkDir, "plan.json"):                  planBytes,
				filepath.Join(newWorkDir, "approval.json"):              approvalBytes,
				ConfigPath(newData):                                     configBytes,
				approvalKeyPath(newData):                                keyBytes,
			} {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("move changed preserved update evidence %s: %v", path, err)
				}
			}
			if request, err := PendingRecovery(f.install, newData); err != nil || request != nil {
				t.Fatalf("historical transaction was reactivated after moving profile: %v, %v", request, err)
			}
		})
	}
}

func TestPrepareProfileMoveWindowsRenameRejectsRacedWriter(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows directory rename rejects open descendants; Unix retains the original locks")
	}
	f := newFixture(t, []byte("fixture runtime"))
	profile, err := cleanup.AcquireForMove(f.data)
	if err != nil {
		t.Fatal(err)
	}
	if err = profile.Close(); err != nil {
		t.Fatal(err)
	}
	// Model an older binary that does not know the external migration
	// coordinator, opening the original profile just before the rename.
	oldWriter, err := cleanup.Acquire(f.data)
	if err != nil {
		t.Fatal(err)
	}
	defer oldWriter.Close()
	newData := filepath.Join(f.root, "moved-data")
	if err = os.Rename(f.data, newData); err == nil {
		t.Fatal("Windows renamed a profile with a raced-in active writer")
	}
	if _, err = os.Stat(newData); !os.IsNotExist(err) {
		t.Fatal("failed rename exposed a second profile")
	}
	if err = oldWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(f.data, newData); err != nil {
		t.Fatalf("retry after writer closed failed: %v", err)
	}
	moved, err := cleanup.Acquire(newData)
	if err != nil {
		t.Fatal(err)
	}
	defer moved.Close()
	got, err := os.ReadFile(filepath.Join(newData, "settings.json"))
	if err != nil || string(got) != `{"private_settings":"preserve"}` {
		t.Fatalf("rename lost profile contents: %v", err)
	}
}

func TestPrepareProfileMoveRetryRetainsHelperExclusion(t *testing.T) {
	f, plan, stage := profileMoveFixture(t, "succeeded", false)
	profile, err := cleanup.AcquireForMove(f.data)
	if err != nil {
		t.Fatal(err)
	}
	defer profile.Close()
	for attempt := 0; attempt < 2; attempt++ {
		guard, err := PrepareProfileMove(f.install, f.data)
		if err != nil || guard == nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if competing, err := cleanup.Acquire(plan.WorkDir); err == nil {
			competing.Close()
			t.Fatal("retry forgot completed-helper exclusion")
		}
		if err = guard.Close(); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(filepath.Join(f.data, "updates", profileMoveStageName))
	if err != nil || !bytes.Equal(got, stage) {
		t.Fatalf("retry modified historical staging: %v", err)
	}
}

func TestPrepareProfileMoveRejectsUnsafeOrUnfinishedUpdatesWithoutChanges(t *testing.T) {
	for _, scenario := range []string{"unfinished", "unfinished-last", "changed-plan", "changed-installed", "helper-busy", "invalid-json", "outside-path", "outside-last-plan", "archive-exists"} {
		t.Run(scenario, func(t *testing.T) {
			f, plan, stage := profileMoveFixture(t, "succeeded", false)
			switch scenario {
			case "unfinished", "unfinished-last":
				if err := saveJSON(filepath.Join(plan.WorkDir, "journal.json"), journal{State: "checking_startup"}); err != nil {
					t.Fatal(err)
				}
				if scenario == "unfinished" {
					stage, _ = json.Marshal(map[string]string{"apply_plan_path": plan.PlanPath})
				}
			case "changed-plan":
				raw, _ := os.ReadFile(plan.PlanPath)
				mustWrite(t, plan.PlanPath, bytes.Replace(raw, []byte(`"old_pid": 2147483646`), []byte(`"old_pid": 12345`), 1))
			case "changed-installed":
				mustWrite(t, filepath.Join(f.install, "README.txt"), []byte("changed after update"))
				stage, _ = json.Marshal(map[string]string{"apply_plan_path": plan.PlanPath})
			case "helper-busy":
				helper, err := cleanup.Acquire(plan.WorkDir)
				if err != nil {
					t.Fatal(err)
				}
				defer helper.Close()
			case "invalid-json":
				stage = []byte(`{"broken"`)
			case "outside-path":
				stage, _ = json.Marshal(map[string]string{"package_path": filepath.Join(f.root, "outside.zip")})
			case "outside-last-plan":
				stage, _ = json.Marshal(map[string]string{"last_apply_plan_path": filepath.Join(f.root, "outside.json")})
			case "archive-exists":
				mustWrite(t, filepath.Join(f.data, "updates", profileMoveStageName), []byte("older migration record"))
			}
			stagePath := filepath.Join(f.data, "updates", "stage.json")
			mustWrite(t, stagePath, stage)
			profile, err := cleanup.AcquireForMove(f.data)
			if err != nil {
				t.Fatal(err)
			}
			defer profile.Close()
			guard, err := PrepareProfileMove(f.install, f.data)
			if guard != nil {
				guard.Close()
				t.Fatal("failed preparation returned a retained helper guard")
			}
			if err == nil {
				t.Fatal("unsafe migration preparation succeeded")
			}
			got, err := os.ReadFile(stagePath)
			if err != nil || !bytes.Equal(got, stage) {
				t.Fatalf("failed preparation changed active staging: %v", err)
			}
			if scenario != "archive-exists" {
				if _, err = os.Stat(filepath.Join(f.data, "updates", profileMoveStageName)); !os.IsNotExist(err) {
					t.Fatal("failed preparation wrote historical staging")
				}
			}
		})
	}
}

func TestPrepareProfileMoveAllowsManualUpgradeAfterCompletedUpdate(t *testing.T) {
	f, _, originalStage := profileMoveFixture(t, "succeeded", false)
	// The installer is allowed to replace an older update's installed payload.
	// A completed historical pointer is not authority to roll that install back.
	mustWrite(t, filepath.Join(f.install, RuntimeName(f.platform)), []byte("manually installed next version"))
	profile, err := cleanup.AcquireForMove(f.data)
	if err != nil {
		t.Fatal(err)
	}
	defer profile.Close()
	guard, err := PrepareProfileMove(f.install, f.data)
	if err != nil || guard == nil {
		t.Fatalf("completed historical update blocked manual upgrade: %v", err)
	}
	defer guard.Close()
	got, err := os.ReadFile(filepath.Join(f.data, "updates", profileMoveStageName))
	if err != nil || !bytes.Equal(got, originalStage) {
		t.Fatalf("manual upgrade changed completed update history: %v", err)
	}
}

func TestPrepareProfileMoveArchivesOnlyUnappliedStaging(t *testing.T) {
	f := newFixture(t, []byte("fixture runtime"))
	stage, _ := json.Marshal(map[string]string{"manifest_path": f.envelope, "package_path": f.archive})
	stagePath := filepath.Join(f.data, "updates", "stage.json")
	mustWrite(t, stagePath, stage)
	profile, err := cleanup.AcquireForMove(f.data)
	if err != nil {
		t.Fatal(err)
	}
	defer profile.Close()
	guard, err := PrepareProfileMove(f.install, f.data)
	if err != nil || guard != nil {
		t.Fatalf("unapplied staging: %v, %v", guard, err)
	}
	got, err := os.ReadFile(filepath.Join(f.data, "updates", profileMoveStageName))
	if err != nil || !bytes.Equal(got, stage) {
		t.Fatalf("unapplied staging not preserved: %v", err)
	}
	if _, err = os.Stat(f.archive); err != nil {
		t.Fatalf("downloaded package was removed: %v", err)
	}
}

func TestPrepareProfileMoveWithoutStagingDoesNotWrite(t *testing.T) {
	f := newFixture(t, []byte("fixture runtime"))
	guard, err := PrepareProfileMove(f.install, f.data)
	if err != nil || guard != nil {
		t.Fatalf("profile without staging: %v, %v", guard, err)
	}
	if _, err = os.Stat(filepath.Join(f.data, "updates", profileMoveStageName)); !os.IsNotExist(err) {
		t.Fatal("migration invented staging history")
	}
}
