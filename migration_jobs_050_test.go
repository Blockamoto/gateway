package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func Test050ArchiveCommitAndTamperDetection(t *testing.T) {
	a, v := initGraphTestApp(t)
	raw := testGenesisBlockPayload(t)
	if e := a.archivePut(raw, v); e != nil {
		t.Fatal(e)
	}
	got, e := a.archiveBlock(v.Hash)
	if e != nil || !bytes.Equal(got, raw) {
		t.Fatal("archive readback", e)
	}
	path, _, _ := a.archivePaths(v.Hash)
	raw[len(raw)-1] ^= 1
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = a.archiveBlock(v.Hash); e == nil {
		t.Fatal("tampered archive accepted")
	}
	if e = a.archivePut(raw, v); e == nil {
		t.Fatal("receipt for a different body accepted")
	}
}
func Test050ArchivePathCannotEnterCoreThroughSymlink(t *testing.T) {
	core := t.TempDir()
	base := t.TempDir()
	alias := filepath.Join(base, "alias")
	if e := os.Symlink(core, alias); e != nil {
		t.Skip(e)
	}
	if e := archiveRootAllowed(filepath.Join(alias, "not-created", "archive"), core, ""); e == nil {
		t.Fatal("archive can enter Core through a symlink ancestor")
	}
	if e := archiveRootAllowed(filepath.Join(base, "safe"), core, ""); e != nil {
		t.Fatal(e)
	}
}
func Test050MigrationCopiesWithoutModifyingCore(t *testing.T) {
	if !releaseFeatureAvailable("txo-spender") {
		t.Skip("0.6.6 release lock: migration requires the replacement graph index")
	}
	a, v := initGraphTestApp(t)
	a.status.TipHash = v.Hash
	a.status.HeaderHeight = 0
	coreFile := filepath.Join(a.settings.BitcoinBlocksDir, "blk00000.dat")
	before, e := os.ReadFile(coreFile)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.startMigration(migrationRequest{Action: "start", Mode: "move", Scope: "range", From: 0, To: 0, Confirm: "MOVE VERIFIED DATA"}); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(5 * time.Second)
	var job migrationJob
	for time.Now().Before(deadline) {
		a.migrationMu.Lock()
		job = a.migration
		a.migrationMu.Unlock()
		if !job.Running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Running || job.Copied != 1 || job.State != "reclaim_pending" || !job.Reclaim {
		t.Fatalf("migration %+v", job)
	}
	after, e := os.ReadFile(coreFile)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("Core file modified", e)
	}
	if _, e = a.archiveBlock(v.Hash); e != nil {
		t.Fatal(e)
	}
	locations, e := a.graphFindTx(v.Transactions[0].TxID)
	if e != nil || len(locations) != 1 {
		t.Fatal("migration lost transaction location")
	}
	a2 := &app{dataDir: a.dataDir}
	a2.loadMigration()
	if a2.migration.Copied != 1 || a2.migration.State != "reclaim_pending" {
		t.Fatal("durable migration journal lost")
	}
}
func Test050MigrationRequiresExplicitMoveAndGraph(t *testing.T) {
	a, _ := initGraphTestApp(t)
	if e := a.startMigration(migrationRequest{Mode: "move", From: 0, To: 0}); e == nil {
		t.Fatal("move without confirmation")
	}
	a.settings.GraphIndex = false
	if e := a.startMigration(migrationRequest{Mode: "copy", From: 0, To: 0}); e == nil {
		t.Fatal("migration without replacement graph")
	}
	if e := a.preflightReclaim(context.Background(), 0); e == nil {
		t.Fatal("reclamation allowed without Core handoff")
	}
}

func TestMigrationRetainsCachePublicationPolicy(t *testing.T) {
	if !releaseFeatureAvailable("txo-spender") {
		t.Skip("0.6.6 release lock: migration requires the replacement graph index")
	}
	for _, private := range []bool{false, true} {
		name := "public"
		if private {
			name = "private"
		}
		t.Run(name, func(t *testing.T) {
			a, v := initGraphTestApp(t)
			prefix := "0-" + v.Hash
			dir := filepath.Join(a.dataDir, "blocks", "raw")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, prefix+".block"), testGenesisBlockPayload(t), 0600); err != nil {
				t.Fatal(err)
			}
			a.cacheIndex.Blocks[v.Hash] = cachedBlockEntry{Height: 0, Hash: v.Hash, Prefix: prefix, Private: private}
			a.cacheIndex.Heights["0"] = v.Hash
			if err := a.startMigration(migrationRequest{Action: "start", Mode: "copy", Scope: "range", From: 0, To: 0}); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			var job migrationJob
			for time.Now().Before(deadline) {
				a.migrationMu.Lock()
				job = a.migration
				a.migrationMu.Unlock()
				if !job.Running {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if job.Running {
				t.Fatal("migration did not stop")
			}
			_, archiveErr := a.archiveBlock(v.Hash)
			if private {
				if job.State != "paused" || job.Copied != 0 || !strings.Contains(job.Error, "private") || archiveErr == nil || len(a.graphCoverage()) != 0 {
					t.Fatalf("private cache migrated to public storage: job=%+v archive=%v", job, archiveErr)
				}
				if _, err := a.cachedBlockPayloadUnverified(v.Hash); err != nil {
					t.Fatalf("private source was altered: %v", err)
				}
			} else if job.State != "copied" || job.Copied != 1 || archiveErr != nil || !intervalsCoverRange(a.graphCoverage(), 0, 0) {
				t.Fatalf("public cache migration failed: job=%+v archive=%v", job, archiveErr)
			}
		})
	}
}
func Test050InterruptedJobsRestartPaused(t *testing.T) {
	dir := t.TempDir()
	a := &app{dataDir: dir}
	q, _ := parseSatlineQuery("123")
	j := &satlineJob{ID: "fixture050", Query: q, Status: "running", Done: false, Operation: "resolve", Started: time.Now(), Updated: time.Now()}
	a.persistJobLocked(j)
	a2 := &app{dataDir: dir}
	a2.loadDurableJobs()
	got := a2.satlineJobs[j.ID]
	if got == nil || got.Status != "paused" || !got.Done {
		t.Fatalf("job not recoverable %+v", got)
	}
	if !strings.Contains(got.Stage, "Resume") {
		t.Fatal("missing resume action explanation")
	}
}
func Test050JobsRejectMismatchedIdentity(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	j := satlineJob{ID: "../../bad", Query: satlineQuery{Kind: "sat", Input: "1"}}
	b, _ := json.Marshal(j)
	if e := atomicWriteBytes(filepath.Join(a.dataDir, "satline", "jobs", "safe.json"), b); e != nil {
		t.Fatal(e)
	}
	a.loadDurableJobs()
	if len(a.satlineJobs) != 0 {
		t.Fatal("unsafe durable identity accepted")
	}
}
func Test050OldSatlineReceiptCannotResume(t *testing.T) {
	a := &app{}
	rec := satlineRecord{Schema: 1, VerifierVersion: 0, Kind: "sat", Key: "1", Result: satlineResult{State: "INVALID"}}
	if _, ok := a.cachedSatlineResult(rec, 1); ok {
		t.Fatal("old verifier result reused")
	}
}
