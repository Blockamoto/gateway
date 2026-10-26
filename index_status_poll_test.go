package main

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIndexProgressFreshDurableRunAndExplicitOlderDetails(t *testing.T) {
	a := runTestApp(t)
	var builds atomic.Int64
	a.indexStatusPoll.build = func() indexStatusView {
		builds.Add(1)
		return indexStatusView{Instances: []indexInstance{{Definition: "tx-locator", Checkpoint: &indexCheckpoint{Height: 3}}}}
	}
	a.indexStatusSnapshot()
	a.indexRunMu.Lock()
	a.indexRunLoaded = true
	a.indexRunState.Run = &indexRunRecord{ID: "new-progress", State: "running", Consumers: []indexRunProgress{{Index: "tx-locator", Height: 9, AddedBlocks: 6}}}
	a.indexRunMu.Unlock()
	view, err := a.indexProgressStatus(context.Background())
	if err != nil || builds.Load() != 1 || view.Run.Run == nil || view.Run.Run.Consumers[0].Height != 9 {
		t.Fatal(view, err, builds.Load())
	}
	if view.Instances[0].Checkpoint.Height != 3 || view.Observation.Mode != "progress" || view.Observation.DetailsObservedAt == "" {
		t.Fatal("old detailed coverage was relabelled as current", view)
	}
	view.Instances[0].Checkpoint.Height = 999
	again, err := a.indexProgressStatus(context.Background())
	if err != nil || again.Instances[0].Checkpoint.Height != 3 {
		t.Fatal("poll response mutated cached evidence", again, err)
	}
}

func TestIndexProgressRefreshIsSingleFlightAndDoesNotBlockCachedPoll(t *testing.T) {
	a := runTestApp(t)
	var builds atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	a.indexStatusPoll.build = func() indexStatusView {
		n := builds.Add(1)
		if n == 2 {
			close(started)
			<-release
		}
		return indexStatusView{Timeline: indexTimelineView{TipHeight: n}}
	}
	a.indexStatusSnapshot()
	a.indexStatusPoll.mu.Lock()
	a.indexStatusPoll.at = time.Now().Add(-time.Minute)
	a.indexStatusPoll.completed = a.indexStatusPoll.at
	a.indexStatusPoll.mu.Unlock()
	begin := time.Now()
	view, err := a.indexProgressStatus(context.Background())
	if err != nil || time.Since(begin) > time.Second || !view.Observation.DetailsRefreshing || view.Timeline.TipHeight != 1 || view.Observation.DetailsAgeMilliseconds < 30000 {
		t.Fatal(view, err)
	}
	<-started
	for n := 0; n < 4; n++ {
		if _, err := a.indexProgressStatus(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if builds.Load() != 2 {
		t.Fatal("concurrent polls duplicated detailed inspection", builds.Load())
	}
	a.indexStatusPoll.mu.Lock()
	done := a.indexStatusPoll.done
	a.indexStatusPoll.mu.Unlock()
	close(release)
	<-done
	view, err = a.indexProgressStatus(context.Background())
	if err != nil || view.Timeline.TipHeight != 2 || view.Observation.DetailsRefreshing {
		t.Fatal(view, err)
	}
}

func TestIndexProgressSlowDetailsUseCompletionCooldown(t *testing.T) {
	a := runTestApp(t)
	var builds atomic.Int64
	a.indexStatusPoll.build = func() indexStatusView {
		builds.Add(1)
		return indexStatusView{}
	}
	a.indexStatusSnapshot()
	// Model a build that started over the refresh interval ago but has just
	// finished; the age must remain honest without restarting the work.
	a.indexStatusPoll.mu.Lock()
	a.indexStatusPoll.at = time.Now().Add(-time.Minute)
	a.indexStatusPoll.completed = time.Now()
	a.indexStatusPoll.mu.Unlock()
	view, err := a.indexProgressStatus(context.Background())
	if err != nil || view.Observation.DetailsAgeMilliseconds < 30000 || view.Observation.DetailsRefreshing || builds.Load() != 1 {
		t.Fatal("slow details should retain their age and observe completion cooldown", view.Observation, err, builds.Load())
	}
}

func TestIndexProgressInitialWaitCancelsAndSnapshotBudgetIsBounded(t *testing.T) {
	a := runTestApp(t)
	started, release := make(chan struct{}), make(chan struct{})
	a.indexStatusPoll.build = func() indexStatusView { close(started); <-release; return indexStatusView{} }
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := a.indexProgressStatus(ctx); err != context.DeadlineExceeded {
		t.Fatal("initial wait ignored cancellation", err)
	}
	<-started
	a.indexStatusPoll.mu.Lock()
	done := a.indexStatusPoll.done
	a.indexStatusPoll.mu.Unlock()
	close(release)
	<-done
	b := runTestApp(t)
	b.indexStatusPoll.build = func() indexStatusView {
		return indexStatusView{Errors: []string{strings.Repeat("x", indexStatusPollBudget+1)}}
	}
	b.indexStatusSnapshot()
	if _, err := b.indexProgressStatus(context.Background()); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatal("unbounded progress snapshot accepted", err)
	}
}

func TestIndexProgressFailedInitialRefreshRetriesAfterCooldown(t *testing.T) {
	a := runTestApp(t)
	var builds atomic.Int64
	a.indexStatusPoll.build = func() indexStatusView {
		if builds.Add(1) == 1 {
			return indexStatusView{Errors: []string{strings.Repeat("x", indexStatusPollBudget+1)}}
		}
		return indexStatusView{Timeline: indexTimelineView{TipHeight: 7}}
	}
	if _, err := a.indexProgressStatus(context.Background()); err == nil {
		t.Fatal("first oversized snapshot must fail")
	}
	if _, err := a.indexProgressStatus(context.Background()); err == nil || builds.Load() != 1 {
		t.Fatal("failure must respect retry cooldown", err, builds.Load())
	}
	a.indexStatusPoll.mu.Lock()
	a.indexStatusPoll.completed = time.Now().Add(-indexStatusDetailInterval)
	a.indexStatusPoll.mu.Unlock()
	view, err := a.indexProgressStatus(context.Background())
	if err != nil || builds.Load() != 2 || view.Timeline.TipHeight != 7 {
		t.Fatal("initial refresh did not recover after cooldown", view, err, builds.Load())
	}
}
