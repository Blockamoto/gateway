package main

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

func snapshotFixtureJob() indexJob {
	return indexJob{ID: "snapshot-fixture", Index: "blocks", State: "running", Outputs: []string{"inscriptions"}, Limitations: []string{"dependency pending"}, Progress: []indexOutputProgress{{Index: "inscriptions", State: "waiting", Height: 12, Limitations: []string{"numbering pending"}}}}
}
func mutateFixtureJob(j indexJob) {
	j.Outputs[0] = "mutated output"
	j.Limitations[0] = "mutated limitation"
	j.Progress[0].State = "mutated state"
	j.Progress[0].Limitations[0] = "mutated nested limitation"
}
func requireOriginalFixtureJob(t *testing.T, j indexJob) {
	t.Helper()
	if j.Outputs[0] != "inscriptions" || j.Limitations[0] != "dependency pending" || j.Progress[0].State != "waiting" || j.Progress[0].Limitations[0] != "numbering pending" {
		t.Fatalf("job crossed an ownership boundary with shared mutable storage: %+v", j)
	}
}

func Test065IndexJobWorkerPublicationAndStatusSnapshotsAreIndependent(t *testing.T) {
	a := &app{dataDir: t.TempDir(), indexQueueLoaded: true, indexQueue: []indexQueueEntry{{Job: indexJob{ID: "snapshot-fixture"}}}}
	worker := snapshotFixtureJob()
	if e := a.saveIndexJob(worker); e != nil {
		t.Fatal(e)
	}
	mutateFixtureJob(worker)
	requireOriginalFixtureJob(t, a.indexJobSnapshot())
	rows, e := a.indexJobsSnapshot()
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	requireOriginalFixtureJob(t, rows[0])
	mutateFixtureJob(rows[0])
	view := a.indexJobSnapshot()
	mutateFixtureJob(view)
	requireOriginalFixtureJob(t, a.indexJobSnapshot())
	rows, e = a.indexJobsSnapshot()
	if e != nil {
		t.Fatal(e)
	}
	requireOriginalFixtureJob(t, rows[0])
}

func Test065IndexJobConcurrentProgressAndStatusEncoding(t *testing.T) {
	a := &app{dataDir: t.TempDir(), indexQueueLoaded: true, indexQueue: []indexQueueEntry{{Job: indexJob{ID: "snapshot-fixture"}}}}
	worker := snapshotFixtureJob()
	if e := a.saveIndexJob(worker); e != nil {
		t.Fatal(e)
	}
	// A worker keeps advancing its private progress between durable saves.
	// Readers encode snapshots after the mutex is released, as HTTP status does.
	start := make(chan struct{})
	failures := make(chan error, 3)
	var readers sync.WaitGroup
	for i := 0; i < 3; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			for n := 0; n < 100; n++ {
				snapshot := a.indexJobSnapshot()
				rows, e := a.indexJobsSnapshot()
				if e != nil {
					failures <- e
					return
				}
				encoded, e := json.Marshal(struct {
					Job  indexJob
					Jobs []indexJob
				}{snapshot, rows})
				if e != nil || len(encoded) == 0 {
					failures <- fmt.Errorf("encode: %v", e)
					return
				}
				if snapshot.Progress[0].Height != 12 || snapshot.Progress[0].Limitations[0] != "numbering pending" {
					failures <- fmt.Errorf("unpublished worker progress escaped: %+v", snapshot)
					return
				}
			}
		}()
	}
	close(start)
	for n := 0; n < 10000; n++ {
		worker.Progress[0].Height = int64(n + 100)
		worker.Progress[0].Limitations[0] = fmt.Sprint(n)
		worker.Outputs[0] = fmt.Sprint(n)
		worker.Limitations[0] = fmt.Sprint(n)
	}
	readers.Wait()
	close(failures)
	for e := range failures {
		t.Error(e)
	}
}

func Test065IndexJobQueueAdmissionDeduplicationAndControlReturnOwnedViews(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	a.indexJob = snapshotFixtureJob()
	a.indexCancel = func() {} // Keep admitted jobs queued; no worker is started.
	t.Cleanup(func() { a.indexMu.Lock(); a.indexCancel = nil; a.indexMu.Unlock() })
	zero := int64(0)
	request := indexBuildRequest{Index: "blocks", From: &zero, To: &zero, Outputs: []string{"blocks"}}
	created, e := a.startIndexBuild(request)
	if e != nil {
		t.Fatal(e)
	}
	created.State = "changed return"
	request.Outputs[0] = "changed request"
	check := func(wantState string) {
		t.Helper()
		rows, e := a.indexJobsSnapshot()
		if e != nil {
			t.Fatal(e)
		}
		for _, row := range rows {
			if row.ID == created.ID {
				if row.State != wantState {
					t.Fatalf("queue ownership escaped: %+v", row)
				}
				return
			}
		}
		t.Fatal("queued job missing")
	}
	check("queued")
	request.Outputs = []string{"blocks"}
	duplicate, e := a.startIndexBuild(request)
	if e != nil || duplicate.ID != created.ID {
		t.Fatal(duplicate, e)
	}
	duplicate.State = "changed duplicate"
	check("queued")
	paused, e := a.controlIndexQueue(indexQueueRequest{ID: created.ID, Action: "pause"})
	if e != nil {
		t.Fatal(e)
	}
	paused.State = "changed pause"
	check("paused")
	rejected, e := a.controlIndexQueue(indexQueueRequest{ID: created.ID, Action: "up"})
	if e == nil {
		t.Fatal("reordered a paused job")
	}
	rejected.State = "changed error"
	check("paused")
	busy, e := a.startIndexBuildInternal(request, false)
	if e == nil {
		t.Fatal("started a second writer")
	}
	mutateFixtureJob(busy)
	requireOriginalFixtureJob(t, a.indexJobSnapshot())
}

func Test065IndexJobRestartSeparatesLegacyStatusFromQueue(t *testing.T) {
	root := t.TempDir()
	a := &app{dataDir: root, indexQueueLoaded: true, indexQueue: []indexQueueEntry{{Request: indexBuildRequest{Index: "blocks"}, Job: indexJob{ID: "snapshot-fixture"}}}}
	if e := a.saveIndexJob(snapshotFixtureJob()); e != nil {
		t.Fatal(e)
	}
	restarted := &app{dataDir: root}
	requireOriginalFixtureJob(t, restarted.indexJobSnapshot())
	// Queue-control changes happen under the mutex; previously even these
	// altered the separate legacy status object through its shared arrays.
	restarted.indexMu.Lock()
	mutateFixtureJob(restarted.indexQueue[0].Job)
	restarted.indexMu.Unlock()
	requireOriginalFixtureJob(t, restarted.indexJobSnapshot())
}
