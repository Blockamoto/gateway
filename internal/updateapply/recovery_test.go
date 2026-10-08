package updateapply

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"../cleanup"
	"../updates"
)

func prepareInterrupted(t *testing.T, runtimePayload []byte) (*fixture, *Plan, string) {
	t.Helper()
	f := newFixture(t, runtimePayload)
	plan, e := f.prepare(t, 2147483646)
	if e != nil {
		t.Fatal(e)
	}
	backup := filepath.Join(f.install, ".gateway-update-backup-"+filepath.Base(plan.WorkDir))
	if e = os.Mkdir(backup, 0700); e != nil {
		t.Fatal(e)
	}
	name := "README.txt"
	if e = copyBackup(filepath.Join(f.install, name), filepath.Join(backup, name), plan.OriginalFiles[name]); e != nil {
		t.Fatal(e)
	}
	mustWrite(t, filepath.Join(f.install, name), f.payload[name])
	mustWrite(t, filepath.Join(f.install, "COMPATIBILITY.json"), f.payload["COMPATIBILITY.json"])
	// Simulate interruption between atomic replacement and its next journal write.
	ledger := journal{State: "applying", Files: []changedFile{{Name: "README.txt", HadOriginal: true, Installed: false}, {Name: "COMPATIBILITY.json", HadOriginal: false, Installed: false}}}
	if e = saveJSON(filepath.Join(plan.WorkDir, "journal.json"), ledger); e != nil {
		t.Fatal(e)
	}
	mustWrite(t, filepath.Join(plan.WorkDir, HelperName(plan.Platform)), []byte("installed old helper"))
	stage, _ := json.Marshal(map[string]string{"apply_plan_path": plan.PlanPath})
	mustWrite(t, filepath.Join(f.data, "updates", "stage.json"), stage)
	return f, plan, backup
}

func TestInterruptedRecoveryRestoresAuthenticatedBackupsAndPreservesState(t *testing.T) {
	t.Setenv("GATEWAY_UPDATE_TEST_RUNTIME", "1")
	runtimePayload, e := os.ReadFile(os.Args[0])
	if e != nil {
		t.Fatal(e)
	}
	f, plan, backup := prepareInterrupted(t, runtimePayload)
	request, e := PendingRecovery(f.install, f.data)
	if e != nil || request == nil || request.Busy {
		t.Fatalf("pending recovery missing: %+v %v", request, e)
	}
	if request.PlanSHA256 != plan.PlanSHA256 || request.HelperPath != filepath.Join(plan.WorkDir, HelperName(plan.Platform)) {
		t.Fatal("recovery request differs from approved helper/plan")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, e := Recover(ctx, request.PlanPath, request.PlanSHA256)
	if e != nil || result.State != "rolled_back" {
		t.Fatalf("recovery failed: %+v %v", result, e)
	}
	process, _ := os.FindProcess(result.StartedPID)
	defer process.Kill()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if runtimeHealthy(f.data, result.StartedPID, "0.6.4") == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restored runtime not healthy")
		}
		time.Sleep(25 * time.Millisecond)
	}
	for path, expected := range map[string]string{filepath.Join(f.install, "README.txt"): "old", filepath.Join(f.data, "settings.json"): `{"private_settings":"preserve"}`, filepath.Join(f.data, "indexes", "bitmap", "records.json"): `{"block":792435}`, filepath.Join(f.install, "user-file.txt"): "keep this"} {
		b, e := os.ReadFile(path)
		if e != nil || string(b) != expected {
			t.Fatalf("state changed after recovery: %s %q %v", path, b, e)
		}
	}
	if _, e = os.Stat(filepath.Join(f.install, "COMPATIBILITY.json")); !os.IsNotExist(e) {
		t.Fatal("new application metadata was not removed")
	}
	if _, e = os.Stat(filepath.Join(backup, "README.txt")); !os.IsNotExist(e) {
		t.Fatal("restored original not consumed")
	}
	if request, e = PendingRecovery(f.install, f.data); e != nil || request != nil {
		t.Fatalf("completed recovery not cleared: %+v %v", request, e)
	}
	process.Kill()
	deadline = time.Now().Add(3 * time.Second)
	for processAlive(result.StartedPID) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
}

func TestRecoveryCannotUseUnsignedOrTamperedPlanJournalBackupOrHelper(t *testing.T) {
	for _, name := range []string{"changed-plan-with-recomputed-sha", "journal-escape", "backup-tamper", "current-tamper", "helper-tamper", "missing-journal"} {
		t.Run(name, func(t *testing.T) {
			f, plan, backup := prepareInterrupted(t, []byte("fixture runtime"))
			switch name {
			case "changed-plan-with-recomputed-sha":
				b, _ := os.ReadFile(plan.PlanPath)
				b = bytes.Replace(b, []byte(`"old_pid": 2147483646`), []byte(`"old_pid": 12345`), 1)
				mustWrite(t, plan.PlanPath, b)
				plan.PlanSHA256 = digestBytes(b)
				if _, e := ReadPlan(plan.PlanPath, plan.PlanSHA256); e == nil {
					t.Fatal("recomputed SHA bypassed authenticated approval")
				}
				return
			case "journal-escape":
				if e := saveJSON(filepath.Join(plan.WorkDir, "journal.json"), journal{State: "applying", Files: []changedFile{{Name: "../../outside.exe", HadOriginal: true}}}); e != nil {
					t.Fatal(e)
				}
			case "backup-tamper":
				mustWrite(t, filepath.Join(backup, "README.txt"), []byte("unsigned replacement"))
			case "current-tamper":
				mustWrite(t, filepath.Join(f.install, "README.txt"), []byte("unrelated edited file"))
			case "helper-tamper":
				mustWrite(t, filepath.Join(plan.WorkDir, HelperName(plan.Platform)), []byte("untrusted executable"))
				if _, e := PendingRecovery(f.install, f.data); e == nil {
					t.Fatal("untrusted recovery helper allowed")
				}
				return
			case "missing-journal":
				if e := os.Remove(filepath.Join(plan.WorkDir, "journal.json")); e != nil {
					t.Fatal(e)
				}
			}
			before, _ := os.ReadFile(filepath.Join(f.install, "README.txt"))
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, e := Recover(ctx, plan.PlanPath, plan.PlanSHA256); e == nil {
				t.Fatalf("%s accepted", name)
			}
			after, _ := os.ReadFile(filepath.Join(f.install, "README.txt"))
			if !bytes.Equal(before, after) {
				t.Fatal("rejected recovery modified application")
			}
			settings, _ := os.ReadFile(filepath.Join(f.data, "settings.json"))
			if !strings.Contains(string(settings), "preserve") {
				t.Fatal("rejected recovery modified profile")
			}
		})
	}
}

func TestRecoveryRespectsActiveProfileAndCompletedJournal(t *testing.T) {
	f, plan, _ := prepareInterrupted(t, []byte("fixture runtime"))
	if e := saveJSON(ReadyPath(plan), map[string]interface{}{"pid": os.Getpid(), "plan_sha256": plan.PlanSHA256}); e != nil {
		t.Fatal(e)
	}
	operation, e := cleanup.Acquire(plan.WorkDir)
	if e != nil {
		t.Fatal(e)
	}
	request, e := PendingRecovery(f.install, f.data)
	if e != nil || request == nil || !request.Busy {
		t.Fatalf("running helper did not block parallel recovery: %+v %v", request, e)
	}
	operation.Close()
	if e = os.Remove(ReadyPath(plan)); e != nil {
		t.Fatal(e)
	}
	for _, state := range []string{"succeeded", "rolled_back"} {
		for name, hash := range plan.OriginalFiles {
			path := filepath.Join(f.install, filepath.FromSlash(name))
			if state == "succeeded" {
				mustWrite(t, path, f.payload[name])
			} else if hash == "" {
				if e = os.Remove(path); e != nil && !os.IsNotExist(e) {
					t.Fatal(e)
				}
			} else {
				switch name {
				case "README.txt":
					mustWrite(t, path, []byte("old"))
				case RuntimeName(f.platform):
					mustWrite(t, path, []byte("fixture runtime"))
				case HelperName(f.platform):
					mustWrite(t, path, []byte("installed old helper"))
				default:
					t.Fatalf("unexpected original fixture file: %s", name)
				}
			}
		}
		if e = saveJSON(filepath.Join(plan.WorkDir, "journal.json"), journal{State: state}); e != nil {
			t.Fatal(e)
		}
		if req, e := PendingRecovery(f.install, f.data); e != nil || req != nil {
			t.Fatalf("completed transaction offered recovery: %s %v", state, e)
		}
		if req, e := PendingRecovery(filepath.Join(f.root, "different-checkout"), f.data); e != nil || req != nil {
			t.Fatalf("completed transaction prevented using another executable checkout: %+v %v", req, e)
		}
	}
}

func TestRecoveryIgnoresUnrelatedReusedPersistedPIDs(t *testing.T) {
	t.Setenv("GATEWAY_UPDATE_TEST_RUNTIME", "1")
	runtimePayload, e := os.ReadFile(os.Args[0])
	if e != nil {
		t.Fatal(e)
	}
	f, plan, _ := prepareInterrupted(t, runtimePayload)
	// Our live test process owns neither the helper operation nor this profile.
	// Reusing its PID in all historical records must not authorize a mixed launch
	// or prevent restoring the approved previous application after interruption.
	plan.OldPID = os.Getpid()
	encoded, _ := json.MarshalIndent(plan, "", "  ")
	mustWrite(t, plan.PlanPath, encoded)
	plan.PlanSHA256 = digestBytes(encoded)
	if e = os.Remove(filepath.Join(plan.WorkDir, "approval.json")); e != nil {
		t.Fatal(e)
	}
	if e = sealApproval(plan, encoded); e != nil {
		t.Fatal(e)
	}
	if e = saveJSON(ReadyPath(plan), map[string]interface{}{"pid": os.Getpid(), "plan_sha256": plan.PlanSHA256}); e != nil {
		t.Fatal(e)
	}
	ledger, e := readJournal(plan)
	if e != nil {
		t.Fatal(e)
	}
	ledger.StartedPID = os.Getpid()
	if e = saveJSON(filepath.Join(plan.WorkDir, "journal.json"), ledger); e != nil {
		t.Fatal(e)
	}
	request, e := PendingRecovery(f.install, f.data)
	if e != nil || request == nil || request.Busy {
		t.Fatalf("reused PID blocked recovery or allowed mixed launch: %+v %v", request, e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, e := Recover(ctx, request.PlanPath, request.PlanSHA256)
	if e != nil || result.State != "rolled_back" {
		t.Fatalf("reused PID prevented rollback: %+v %v", result, e)
	}
	process, _ := os.FindProcess(result.StartedPID)
	defer process.Kill()
	if !processAlive(os.Getpid()) {
		t.Fatal("unrelated live process was killed")
	}
	b, _ := os.ReadFile(filepath.Join(f.install, "README.txt"))
	if string(b) != "old" {
		t.Fatal("reused-PID recovery failed to restore original")
	}
	process.Kill()
}

func TestForgedTerminalJournalCannotHideInterruptedReplacement(t *testing.T) {
	f, plan, _ := prepareInterrupted(t, []byte("fixture runtime"))
	for _, state := range []string{"succeeded", "rolled_back"} {
		if e := saveJSON(filepath.Join(plan.WorkDir, "journal.json"), journal{State: state}); e != nil {
			t.Fatal(e)
		}
		if _, e := PendingRecovery(f.install, f.data); e == nil {
			t.Fatalf("forged %s state hid unmatched application payload", state)
		}
	}
}

func TestApprovedInterruptedRecoverySurvivesMetadataExpiry(t *testing.T) {
	t.Setenv("GATEWAY_UPDATE_TEST_RUNTIME", "1")
	runtimePayload, e := os.ReadFile(os.Args[0])
	if e != nil {
		t.Fatal(e)
	}
	f, plan, _ := prepareInterrupted(t, runtimePayload)
	var envelope updates.Envelope
	raw, _ := os.ReadFile(f.envelope)
	if e = json.Unmarshal(raw, &envelope); e != nil {
		t.Fatal(e)
	}
	payload, e := base64.StdEncoding.DecodeString(envelope.Payload)
	if e != nil {
		t.Fatal(e)
	}
	var manifest updates.Manifest
	if e = json.Unmarshal(payload, &manifest); e != nil {
		t.Fatal(e)
	}
	// Simulate a transaction approved yesterday, then interrupted until its feed
	// metadata expired. Only recovery may use its authenticated approval time.
	manifest.IssuedAt = time.Now().Add(-26 * time.Hour).UTC().Format(time.RFC3339)
	manifest.ExpiresAt = time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	payload, _ = json.Marshal(manifest)
	message := append([]byte("Gateway desktop update manifest v1\x00"), payload...)
	envelope.Payload = base64.StdEncoding.EncodeToString(payload)
	envelope.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(f.private, message))
	raw, _ = json.Marshal(envelope)
	mustWrite(t, f.envelope, raw)
	plan.ApprovedAt = time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339Nano)
	plan.ManifestSHA256 = digestBytes(raw)
	encoded, _ := json.MarshalIndent(plan, "", "  ")
	mustWrite(t, plan.PlanPath, encoded)
	plan.PlanSHA256 = digestBytes(encoded)
	if e = os.Remove(filepath.Join(plan.WorkDir, "approval.json")); e != nil {
		t.Fatal(e)
	}
	if e = sealApproval(plan, encoded); e != nil {
		t.Fatal(e)
	}
	if _, _, e = verify(plan); e == nil || !strings.Contains(e.Error(), "expired") {
		t.Fatalf("normal apply accepted expired manifest: %v", e)
	}
	request, e := PendingRecovery(f.install, f.data)
	if e != nil || request == nil {
		t.Fatalf("approved expired transaction not recoverable: %+v %v", request, e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, e := Recover(ctx, request.PlanPath, request.PlanSHA256)
	if e != nil || result.State != "rolled_back" {
		t.Fatalf("expired metadata blocked signed approved rollback: %+v %v", result, e)
	}
	process, _ := os.FindProcess(result.StartedPID)
	defer process.Kill()
	b, _ := os.ReadFile(filepath.Join(f.install, "README.txt"))
	if string(b) != "old" {
		t.Fatal("expired recovery did not restore previous application")
	}
	process.Kill()
	stage, _ := os.ReadFile(filepath.Join(f.data, "updates", "stage.json"))
	var fields map[string]string
	json.Unmarshal(stage, &fields)
	if fields["apply_plan_path"] != "" || fields["last_apply_plan_path"] != plan.PlanPath {
		t.Fatal("completed recovery left an active application pointer")
	}
}

func TestMetadataReplacementFailureRetainsPreviousJournal(t *testing.T) {
	root := t.TempDir()
	previous := filepath.Join(root, "journal.json")
	mustWrite(t, previous, []byte(`{"state":"applying","files":[]}`))
	if e := replaceMetadata(filepath.Join(root, "missing-staged-journal"), previous); e == nil {
		t.Fatal("expected rename failure")
	}
	b, e := os.ReadFile(previous)
	if e != nil || string(b) != `{"state":"applying","files":[]}` {
		t.Fatalf("metadata failure discarded last safe journal: %q %v", b, e)
	}
}

func TestHelperRejectsChangedExtractedPayloadAfterOldShutdown(t *testing.T) {
	t.Setenv("GATEWAY_UPDATE_TEST_RUNTIME", "1")
	runtimePayload, e := os.ReadFile(os.Args[0])
	if e != nil {
		t.Fatal(e)
	}
	f := newFixture(t, runtimePayload)
	plan, e := f.prepare(t, 2147483646)
	if e != nil {
		t.Fatal(e)
	}
	// Hold the actual writer lock while the helper authenticates and stages files.
	lock, e := cleanupLock(f.data)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	type response struct {
		result Result
		err    error
	}
	done := make(chan response, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	go func() { r, e := Run(ctx, plan.PlanPath, plan.PlanSHA256); done <- response{r, e} }()
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, e = os.Stat(ReadyPath(plan)); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper never staged")
		}
		time.Sleep(25 * time.Millisecond)
	}
	stage := filepath.Join(f.install, ".gateway-update-stage-"+filepath.Base(plan.WorkDir), "README.txt")
	mustWrite(t, stage, []byte("unsigned modified stage"))
	lock.Close()
	reply := <-done
	if reply.err == nil || reply.result.State != "rolled_back" || !strings.Contains(reply.err.Error(), "extracted signed payload changed") {
		t.Fatalf("want rejection+old restart: %+v %v", reply.result, reply.err)
	}
	process, _ := os.FindProcess(reply.result.StartedPID)
	defer process.Kill()
	b, _ := os.ReadFile(filepath.Join(f.install, "README.txt"))
	if string(b) != "old" {
		t.Fatal("tampered payload installed")
	}
	process.Kill()
}

// Keep the helper's real cross-platform profile-lock behavior in these tests.
func cleanupLock(path string) (*cleanup.Lock, error) { return cleanup.Acquire(path) }
