package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func Test065QueueCancelledIntentSurvivesInterruptedStop(t *testing.T) {
	for _, state := range []string{"running", "waiting", "pausing", "paused"} {
		t.Run(state, func(t *testing.T) {
			a := &app{dataDir: t.TempDir(), indexQueueLoaded: true}
			j := indexJob{ID: "cancel-before-worker-exits", Index: "blocks", State: state, From: 0, To: 20, Height: 7, Progress: []indexOutputProgress{{Index: "blocks", State: "running", Height: 7}, {Index: "inscriptions", State: "complete", Height: 20}}}
			a.indexQueue = []indexQueueEntry{{StopState: "cancelled", Request: indexBuildRequest{Index: "blocks"}, Job: j}}
			if err := a.persistIndexQueueLocked(); err != nil {
				t.Fatal(err)
			}
			if err := atomicWriteJSON(filepath.Join(a.dataDir, "indexes", "job.json"), j); err != nil {
				t.Fatal(err)
			}
			restarted := &app{dataDir: a.dataDir}
			if legacy := restarted.indexJobSnapshot(); legacy.State != "cancelled" {
				t.Fatalf("first legacy snapshot contradicted durable queue: %+v", legacy)
			}
			rows, err := restarted.indexJobsSnapshot()
			if err != nil || len(rows) != 1 {
				t.Fatal(rows, err)
			}
			if rows[0].State != "cancelled" || rows[0].Height != 7 || rows[0].Progress[0].State != "cancelled" || rows[0].Progress[1].State != "complete" {
				t.Fatalf("durable cancellation lost: %+v", rows[0])
			}
			if _, err = restarted.controlIndexQueue(indexQueueRequest{j.ID, "resume"}); err == nil {
				t.Fatal("cancelled job became resumable after restart")
			}
		})
	}
}

func Test065QueueWaitRejectsDisappearedHistory(t *testing.T) {
	a := &app{dataDir: t.TempDir(), indexQueueLoaded: true}
	for i := 0; i < 65; i++ {
		a.indexQueue = append(a.indexQueue, indexQueueEntry{Job: indexJob{ID: fmt.Sprint(i), State: "complete"}})
	}
	if err := a.persistIndexQueueLocked(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	_, err := a.waitQueuedIndexJob(ctx, "0")
	if err == nil || errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "no longer available") {
		t.Fatalf("missing history waited for cancellation instead of reporting unavailable: %v", err)
	}
}

func Test065QueueTerminalJobsCannotBecomeResumableByPausing(t *testing.T) {
	for _, state := range []string{"complete", "cancelled", "yielded"} {
		t.Run(state, func(t *testing.T) {
			a := &app{dataDir: t.TempDir(), indexQueueLoaded: true}
			a.indexQueue = []indexQueueEntry{{Request: indexBuildRequest{Index: "blocks"}, Job: indexJob{ID: "terminal", Index: "blocks", State: state}}}
			if _, err := a.controlIndexQueue(indexQueueRequest{"terminal", "pause"}); err == nil {
				t.Fatal("pausing resurrected terminal work")
			}
			if a.indexQueue[0].Job.State != state {
				t.Fatalf("terminal state changed: %+v", a.indexQueue[0])
			}
		})
	}
}

func Test065QueueLiveHandoffHistoryIsBounded(t *testing.T) {
	a := &app{dataDir: t.TempDir(), indexQueueLoaded: true}
	for i := 0; i < 80; i++ {
		a.indexQueue = append(a.indexQueue, indexQueueEntry{Job: indexJob{ID: fmt.Sprint(i), State: "yielded"}})
	}
	a.indexQueue = append(a.indexQueue, indexQueueEntry{Job: indexJob{ID: "keep-paused", State: "paused"}})
	if err := a.persistIndexQueueLocked(); err != nil {
		t.Fatal(err)
	}
	if len(a.indexQueue) != 65 || a.indexQueue[0].Job.ID != "16" || a.indexQueue[64].Job.ID != "keep-paused" {
		t.Fatalf("Live handoff history grew without bound: %+v", a.indexQueue)
	}
}

func Test065QueueCoalescingPreservesExplicitLiveChoice(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	a.indexJob, a.indexCancel = indexJob{ID: "fixture-writer", Index: "bitmap", State: "running"}, func() {}
	defer func() {
		a.indexMu.Lock()
		a.indexCancel = nil
		a.indexMu.Unlock()
	}()
	zero := int64(0)
	first, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &zero, To: &zero})
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &zero, To: &zero, LiveConfigured: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("explicit Live-off consent was swallowed by a request which preserves Live")
	}
}

func Test065QueueActiveCancelPersistsLivePauseBeforeStopping(t *testing.T) {
	a := &app{dataDir: t.TempDir(), indexQueueLoaded: true}
	j := indexJob{ID: "active-live", Index: "blocks", State: "running", Live: true}
	a.indexJob = j
	a.indexQueue = []indexQueueEntry{{Request: indexBuildRequest{Index: "blocks", Live: true}, Job: j}}
	if err := a.writeIndexLivePolicy(indexLivePolicy{Index: "blocks", Enabled: true, Retention: "ephemeral"}); err != nil {
		t.Fatal(err)
	}
	called := false
	a.indexCancel = func() {
		called = true
		policy, err := readIndexLiveState(a.dataDir)
		if err != nil || !policy.Policies["blocks"].Paused {
			t.Errorf("worker stopped before its Live pause was durable: %+v %v", policy, err)
		}
		b, err := os.ReadFile(filepath.Join(a.dataDir, "indexes", "queue.json"))
		var queue indexQueueState
		if err != nil || json.Unmarshal(b, &queue) != nil || len(queue.Entries) != 1 || queue.Entries[0].StopState != "cancelled" {
			t.Errorf("worker stopped before cancellation intent was durable: %s %v", b, err)
		}
	}
	if _, err := a.controlIndexQueue(indexQueueRequest{j.ID, "cancel"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("active worker was not cancelled")
	}
}

func Test065QueueSharedChildPauseKeepsParentPolicyPaused(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	j := indexJob{ID: "shared-live", Index: "blocks", Outputs: []string{"inscriptions"}, State: "running", Live: true}
	a.indexJob, a.indexQueueLoaded = j, true
	a.indexQueue = []indexQueueEntry{{Request: indexBuildRequest{Index: "blocks", Outputs: j.Outputs, Live: true}, Job: j}}
	if err := a.writeIndexLivePolicy(indexLivePolicy{Index: "blocks", Outputs: j.Outputs, Enabled: true, Retention: "ephemeral"}); err != nil {
		t.Fatal(err)
	}
	called := false
	a.indexCancel = func() {
		called = true
		policies, err := readIndexLiveState(a.dataDir)
		if err != nil || !policies.Policies["blocks"].Paused {
			t.Errorf("shared child stopped writer before durable parent pause: %+v %v", policies, err)
		}
	}
	defer func() { a.indexMu.Lock(); a.indexCancel = nil; a.indexMu.Unlock() }()
	paused, err := a.pauseIndexBuildFor("inscriptions")
	if err != nil || !called || paused.ID != j.ID || paused.State != "pausing" {
		t.Fatal(paused, err, called)
	}
	j.State = "paused"
	if err := a.saveIndexJob(j); err != nil {
		t.Fatal(err)
	}
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: a.cacheIndex}
	policies, err := restarted.indexLivePolicies()
	if err != nil || !policies["blocks"].Paused {
		t.Fatalf("shared parent Live policy lost pause: %+v %v", policies, err)
	}
	restarted.liveIndexTick()
	rows, err := restarted.indexJobsSnapshot()
	if err != nil || len(rows) != 1 || rows[0].ID != j.ID || rows[0].State != "paused" {
		t.Fatalf("shared child pause restarted work: %+v %v", rows, err)
	}
}

func Test065QueueSharedChildPauseFailureDoesNotCancelWorker(t *testing.T) {
	a := &app{dataDir: t.TempDir(), indexJob: indexJob{ID: "active", Index: "blocks", Outputs: []string{"inscriptions"}, State: "running", Live: true}}
	if err := os.MkdirAll(filepath.Join(a.dataDir, "indexes"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.dataDir, "indexes", "live.json"), []byte("invalid policy fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	a.indexCancel = func() { called = true }
	if _, err := a.pauseIndexBuildFor("inscriptions"); err == nil || called {
		t.Fatalf("pause policy failure cancelled worker or claimed success: called=%v err=%v", called, err)
	}
}

func Test065QueueLiveHandoffYieldsWriterToNextReviewedJob(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	zero := int64(0)
	waiting := indexJob{ID: "live-yield", Index: "blocks", State: "waiting", Live: true, Error: "fixture source unavailable"}
	next := indexJob{ID: "next-reviewed", Index: "blocks", State: "queued", From: 0, To: 0, Height: -1}
	a.indexJob, a.indexQueueLoaded, a.indexCancel = waiting, true, func() {}
	a.indexQueue = []indexQueueEntry{
		{Request: indexBuildRequest{Index: "blocks", Live: true}, Job: waiting},
		{Request: indexBuildRequest{Index: "blocks", From: &zero, To: &zero, Mode: "lean", Retention: "ephemeral"}, Job: next},
	}
	if err := a.persistIndexQueueLocked(); err != nil {
		t.Fatal(err)
	}
	a.indexBuildFinished()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	handoff, err := a.waitQueuedIndexJob(ctx, waiting.ID)
	if err != nil || handoff.State != "yielded" {
		t.Fatalf("Live entry kept claiming writer: %+v %v", handoff, err)
	}
	done, err := a.waitQueuedIndexJob(ctx, next.ID)
	if err != nil || done.State != "complete" {
		t.Fatalf("reviewed work was stranded behind Live: %+v %v", done, err)
	}
}
