package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRecipeArchiveRetryRechecksReviewAndStopsAtPermanentError(t *testing.T) {
	busy := errors.New("temporary descendant handle")
	changed := errors.New("reviewed generation changed")
	checks, moves := 0, 0
	err := recipeRetryArchiveMove(context.Background(), func() error {
		checks++
		if checks > 1 {
			return changed
		}
		return nil
	}, func() error { moves++; return busy }, func(err error) bool { return errors.Is(err, busy) })
	if !errors.Is(err, changed) || checks != 2 || moves != 1 {
		t.Fatal("archive retried a stale review", checks, moves, err)
	}
	checks, moves = 0, 0
	err = recipeRetryArchiveMove(context.Background(), func() error { checks++; return nil }, func() error { moves++; return os.ErrExist }, func(error) bool { return false })
	if !errors.Is(err, os.ErrExist) || checks != 1 || moves != 1 {
		t.Fatal("permanent archive error was retried", checks, moves, err)
	}
}

func TestRecipeArchiveWindowsHeldReaderAndCancelledResume(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows descendant-handle rename semantics")
	}
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "released-reader", true: "cancelled-then-resumed"}[cancelled], func(t *testing.T) {
			ctx := context.Background()
			g, err := openRecipeTablesGeneration(t.TempDir(), recipeTablesTestProgram(t, "archive-read-handle", 0, false))
			if err != nil {
				t.Fatal(err)
			}
			if err = g.create(ctx); err != nil {
				t.Fatal(err)
			}
			prior, err := os.ReadFile(g.path("table-head.json"))
			if err != nil {
				t.Fatal(err)
			}
			reviewed, err := g.reviewToken(ctx)
			if err != nil {
				t.Fatal(err)
			}
			reader, err := os.Open(g.path("table-head.json"))
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			job := "held-reader-job"
			if cancelled {
				work, cancel := context.WithCancel(ctx)
				timer := time.AfterFunc(40*time.Millisecond, cancel)
				err = g.resetForRebuild(work, job, reviewed, orderedGenerationTestGuard)
				timer.Stop()
				cancel()
				if !errors.Is(err, context.Canceled) {
					t.Fatal("archive move ignored cancellation", err)
				}
				if after, _ := os.ReadFile(g.path("table-head.json")); string(after) != string(prior) {
					t.Fatal("cancelled archive changed original state")
				}
				if err = reader.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				closed := make(chan struct{})
				go func() { time.Sleep(150 * time.Millisecond); reader.Close(); close(closed) }()
				defer func() { reader.Close(); <-closed }()
			}
			if err = g.resetForRebuild(ctx, job, reviewed, orderedGenerationTestGuard); err != nil {
				t.Fatal("archive did not recover after reader release", err)
			}
			key := indexDigest(struct{ Semantic, Job string }{g.identity.Semantic, job})
			archived, err := os.ReadFile(filepath.Join(filepath.Dir(g.profileRoot), "archives", key, "table-head.json"))
			if err != nil || string(archived) != string(prior) {
				t.Fatal("retry changed archived state", err)
			}
			if _, err = os.Stat(g.profileRoot); !os.IsNotExist(err) {
				t.Fatal("archive retry left a second original generation", err)
			}
		})
	}
}
