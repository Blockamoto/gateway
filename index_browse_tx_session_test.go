package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func futurePointSessionFixture(t *testing.T, count int) (*app, []string, *browseTXPointSession, context.Context, func(func(*indexRunDocument))) {
	t.Helper()
	a, hashes := browseTXPointFixture(t, count)
	d := a.indexRunSnapshot()
	d.Run = &indexRunRecord{ID: "future-window-fixture", State: "waiting", Request: d.Prepared,
		Consumers: []indexRunProgress{{Index: "tx-locator", From: 0, To: int64(count - 1), Height: -1, AdmissionState: "admitted"}}}
	save := func(mutate func(*indexRunDocument)) {
		t.Helper()
		if mutate != nil {
			mutate(&d)
		}
		a.indexRunMu.Lock()
		a.indexRunState = cloneIndexRunDocument(d)
		err := a.persistIndexRunLocked()
		a.indexRunMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	save(nil)
	s := newBrowseTXPointSession(a, context.Background(), d.Run.ID)
	t.Cleanup(s.Abort)
	ctx := context.WithValue(context.Background(), indexBrowseTXPointSessionKey{}, s)
	ctx = context.WithValue(ctx, indexRunCoordinatorSourceKey{}, true)
	ctx = context.WithValue(ctx, indexRunSourceOwnershipKey{}, indexRunSourceOwnership{RunID: d.Run.ID})
	return a, hashes, s, ctx, save
}

func stageFuturePoint(t *testing.T, a *app, ctx context.Context, height int64, hash string) indexBatch {
	t.Helper()
	b := browseTXPointBatch(t, height, hash)
	if err := lockIndexBrowse(ctx); err != nil {
		t.Fatal(err)
	}
	added, err := a.persistBrowseBatch(ctx, "tx-locator", b)
	indexBrowseMu.Unlock()
	if added || !errors.Is(err, errBrowseTXPointStaged) {
		t.Fatal("future facts were not retained as a private pending batch", added, err)
	}
	return b
}

func TestBrowseTXFutureBatchPrivateFactsAndAcknowledgedWindow(t *testing.T) {
	a, hashes, session, ctx, save := futurePointSessionFixture(t, 4)
	for _, height := range []int64{1, 2} {
		b := stageFuturePoint(t, a, ctx, height, hashes[height])
		pending, have, err := session.Lookup(ctx, height, hashes[height])
		if err != nil || !have || pending.Checkpoint.Commitment != b.Checkpoint.Commitment {
			t.Fatal("private reuse lost complete ordered facts", have, err)
		}
		if _, have, err := a.browsedBatchContext(context.Background(), "tx-locator", height, hashes[height]); err != nil || have {
			t.Fatal("volatile point entered saved coverage", have, err)
		}
	}
	if err := session.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	window, have, err := readBrowseTXPointWindow(context.Background(), a.dataDir, 1)
	if err != nil || !have || len(window.Points) != 2 || window.Window != 0 {
		t.Fatal("points were not acknowledged together in their genesis-aligned window", window, have, err)
	}
	for _, height := range []int64{1, 2} {
		if _, have, err := a.browsedBatchContext(context.Background(), "tx-locator", height, hashes[height]); err != nil || !have {
			t.Fatal("acknowledged point was not saved", height, have, err)
		}
	}
	if native, err := indexStoreHead(a.dataDir, "tx-locator"); err != nil || native.checkpoint != nil {
		t.Fatal("future window fabricated a native prefix", native, err)
	}
	save(func(d *indexRunDocument) {
		choice, _ := runSelection(d.Prepared, "tx-locator")
		choice.Armed = false
		replaceIndexRunSelection(&d.Prepared, choice)
	})
	before := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes", "browse", "tx-locator"))
	if _, have, err := a.browsedBatchContext(context.Background(), "tx-locator", 2, hashes[2]); err != nil || !have {
		t.Fatal("Off hid a saved point window", have, err)
	}
	if after := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes", "browse", "tx-locator")); !reflect.DeepEqual(before, after) {
		t.Fatal("Off window read changed saved files")
	}
}

func TestBrowseTXFutureBatchGenesisBoundaryAndAbort(t *testing.T) {
	a, hashes, session, ctx, _ := futurePointSessionFixture(t, 338)
	stageFuturePoint(t, a, ctx, 334, hashes[334])
	stageFuturePoint(t, a, ctx, 335, hashes[335])
	stageFuturePoint(t, a, ctx, 336, hashes[336])
	window, have, err := readBrowseTXPointWindow(context.Background(), a.dataDir, 335)
	if err != nil || !have || window.Window != 0 || len(window.Points) != 2 {
		t.Fatal("window crossing did not flush exactly the earlier group", window, have, err)
	}
	if _, have, err := a.browsedBatchContext(context.Background(), "tx-locator", 336, hashes[336]); err != nil || have {
		t.Fatal("next-window pending point was advertised as saved", have, err)
	}
	session.Abort()
	if _, have, err := a.browsedBatchContext(context.Background(), "tx-locator", 334, hashes[334]); err != nil || !have {
		t.Fatal("Abort lost an already acknowledged window", have, err)
	}
	if _, have, err := a.browsedBatchContext(context.Background(), "tx-locator", 336, hashes[336]); err != nil || have {
		t.Fatal("Abort published its volatile next window", have, err)
	}
}

func TestBrowseTXFutureBatchPauseAndAdmissionFences(t *testing.T) {
	for _, change := range []string{"pausing", "paused", "admission"} {
		t.Run(change, func(t *testing.T) {
			a, hashes, session, ctx, save := futurePointSessionFixture(t, 3)
			stageFuturePoint(t, a, ctx, 1, hashes[1])
			save(func(d *indexRunDocument) {
				if change == "admission" {
					one := int64(1)
					d.Run.Consumers[0].EffectiveFrom = &one
				} else {
					d.Run.State = change
				}
			})
			if err := session.Flush(ctx); err == nil {
				t.Fatal("post-intent future batch was published")
			}
			if _, have, err := a.browsedBatchContext(context.Background(), "tx-locator", 1, hashes[1]); err != nil || have {
				t.Fatal("rejected pending batch entered saved coverage", have, err)
			}
		})
	}
}

func TestBrowseTXFutureBatchReadFailureRemainsObservable(t *testing.T) {
	a, hashes, session, ctx, _ := futurePointSessionFixture(t, 3)
	stageFuturePoint(t, a, ctx, 1, hashes[1])
	path := indexRunPath(a.dataDir)
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("{damaged coordinated settings"), 0600); err != nil {
		t.Fatal(err)
	}
	_, have, err := session.Lookup(ctx, 1, hashes[1])
	if have || err == nil || errors.Is(err, errBrowseTXPointInvalidated) {
		t.Fatal("storage corruption was mistaken for a legal intent change", have, err)
	}
	if err = os.WriteFile(path, saved, 0600); err != nil {
		t.Fatal(err)
	}
	if _, have, err := session.Lookup(ctx, 1, hashes[1]); err != nil || !have {
		t.Fatal("observable read failure silently discarded the pending facts", have, err)
	}
	if err = session.Flush(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestBrowseTXFutureBatchSameBodyReplayReacknowledgesWindow(t *testing.T) {
	a, hashes, session, ctx, _ := futurePointSessionFixture(t, 3)
	batch := stageFuturePoint(t, a, ctx, 1, hashes[1])
	windowPath := browseTXPointWindowPath(a.dataDir, 1)
	want := errors.New("injected window directory acknowledgement failure")
	attempts := 0
	fail := true
	session.writer.locatorSyncDirectory = func(dir string) error {
		if dir == filepath.Dir(windowPath) {
			if _, err := os.Stat(windowPath); err == nil {
				attempts++
				if fail {
					return want
				}
			}
		}
		return nil
	}
	if err := session.Flush(ctx); !errors.Is(err, want) {
		t.Fatal("post-rename acknowledgement failure was hidden", err)
	}
	if _, have, err := readBrowseTXPointWindow(context.Background(), a.dataDir, 1); err != nil || !have {
		t.Fatal("fixture did not reach the visible-window boundary", have, err)
	}
	replay := func() (bool, error) {
		if err := lockIndexBrowse(ctx); err != nil {
			t.Fatal(err)
		}
		added, err := a.persistBrowseBatch(ctx, "tx-locator", batch)
		indexBrowseMu.Unlock()
		return added, err
	}
	if added, err := replay(); added || !errors.Is(err, want) || attempts < 2 {
		t.Fatal("same-body replay claimed saved success without retrying acknowledgement", added, attempts, err)
	}
	fail = false
	if added, err := replay(); added || err != nil || attempts < 3 {
		t.Fatal("same-body acknowledgement retry did not recover", added, attempts, err)
	}
	if len(session.entries) != 0 {
		t.Fatal("acknowledged pending facts remained staged")
	}
	window, have, err := readBrowseTXPointWindow(context.Background(), a.dataDir, 1)
	if err != nil || !have || len(window.Points) != 1 || window.Points[0].Commitment != batch.Checkpoint.Commitment {
		t.Fatal("replay duplicated or replaced window membership", window, have, err)
	}
}

func TestBrowseTXFutureBatchRechecksPauseAtWindowVisibility(t *testing.T) {
	a, hashes, session, ctx, save := futurePointSessionFixture(t, 3)
	batch := stageFuturePoint(t, a, ctx, 1, hashes[1])
	commitPath := filepath.Join(a.dataDir, "indexes", "browse", "tx-locator", "commits", batch.Checkpoint.Commitment+".json")
	triggered := false
	ctx = context.WithValue(ctx, indexRunPublicationKey{}, func() error {
		if _, err := os.Stat(commitPath); err == nil && !triggered {
			triggered = true
			save(func(d *indexRunDocument) { d.Run.State = "pausing" })
		}
		return nil
	})
	if err := session.Flush(ctx); err == nil || !triggered {
		t.Fatal("late pause intent was not checked at the visibility boundary", triggered, err)
	}
	if _, have, err := readBrowseTXPointWindow(context.Background(), a.dataDir, 1); err != nil || have {
		t.Fatal("late pause published a pending window", have, err)
	}
}
