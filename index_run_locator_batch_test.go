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

func TestTimelineRunLocatorBatchDurableFinishWithoutSparseDuplicate(t *testing.T) {
	a := runTestApp(t)
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}}}
	if _, err := a.startIndexRun(context.Background(), req, ""); err != nil {
		t.Fatal(err)
	}
	d := waitRun(t, a)
	p, _ := indexRunProgressFor(d.Run, "tx-locator")
	if d.Run.State != "complete" || p.Height != 1 || p.AddedBlocks != 2 || p.StagedHeight != nil || p.StagedBlocks != 0 {
		t.Fatal(d.Run, p)
	}
	c := d.Run.Counts
	if c.LocatorBatchFlushes != 1 || c.LocatorHeadWrites != 1 || c.LocatorActiveWrites != 1 || c.LocatorCommitWrites != 2 || c.LocatorRecordsWritten != 2 {
		t.Fatal("physical batch writes", c)
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", "browse", "tx-locator", "head.json")); !os.IsNotExist(err) {
		t.Fatal("coordinated source duplicated sparse membership", err)
	}
	s, err := indexStoreHead(a.dataDir, "tx-locator")
	if err != nil || s.checkpoint == nil || s.checkpoint.Height != 1 {
		t.Fatal(s, err)
	}
}

func TestTimelineRunLocatorBatchPauseDiscardsOnlyStagedPrefix(t *testing.T) {
	a := runTestApp(t)
	zero, one := int64(0), int64(1)
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, retention string) (blockView, error) {
		if target.Height == 1 {
			<-ctx.Done()
			return blockView{}, ctx.Err()
		}
		return a.indexSourceBlock(ctx, target, retention)
	}
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}}}
	started, err := a.startIndexRun(context.Background(), req, "")
	if err != nil {
		t.Fatal(err)
	}
	d := waitRunCondition(t, a, func(d indexRunDocument) bool {
		if d.Run == nil {
			return false
		}
		p, _ := indexRunProgressFor(d.Run, "tx-locator")
		return p.StagedBlocks == 1
	})
	p, _ := indexRunProgressFor(d.Run, "tx-locator")
	if p.Height != -1 || p.StagedHeight == nil || *p.StagedHeight != 0 || p.AddedBlocks != 0 {
		t.Fatal("staging advertised as durable", p)
	}
	if _, err = a.controlIndexRun(context.Background(), started.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	d = waitRun(t, a)
	p, _ = indexRunProgressFor(d.Run, "tx-locator")
	if p.Height != -1 || p.StagedHeight != nil || p.AddedBlocks != 0 {
		t.Fatal("pause retained phantom progress", p)
	}
	s, err := indexStoreHead(a.dataDir, "tx-locator")
	if err != nil || s.checkpoint != nil {
		t.Fatal("pause published partial batch", s, err)
	}
	a.indexRunSourceBlock = nil
	if _, err = a.controlIndexRun(context.Background(), started.Run.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	d = waitRun(t, a)
	p, _ = indexRunProgressFor(d.Run, "tx-locator")
	if d.Run.State != "complete" || p.Height != 1 || p.AddedBlocks != 2 || d.Run.Counts.LocatorBatchFlushes != 1 {
		t.Fatal(d.Run, p)
	}
}

func TestTimelineRunLogicalBatchWindowsAreGenesisAligned(t *testing.T) {
	for _, test := range []struct{ from, height, start, end int64 }{{0, 0, 0, 335}, {0, 335, 0, 335}, {0, 336, 336, 671}, {792435, 792435, 792435, 792623}, {792435, 792623, 792435, 792623}, {792435, 792624, 792624, 792959}} {
		from, to := indexRunLogicalBatchWindow(test.from, test.height)
		if from != test.start || to != test.end {
			t.Fatal(test, from, to)
		}
	}
}

func TestTimelineRunLocatorRestartDropsVolatileStagingMetadata(t *testing.T) {
	a := runTestApp(t)
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}}}); err != nil {
		t.Fatal(err)
	}
	height := int64(2)
	a.indexRunMu.Lock()
	a.indexRunState.Run = &indexRunRecord{ID: "interrupted-stage", State: "running", Request: a.indexRunState.Prepared, Consumers: []indexRunProgress{{Index: "tx-locator", Height: -1, StagedHeight: &height, StagedBlocks: 3, StagedBytes: 1234, StagedRecords: 3, LogicalBatchFrom: &height, LogicalBatchTo: &height}}}
	err := a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	restarted := &app{dataDir: a.dataDir}
	d := restarted.indexRunSnapshot()
	p, _ := indexRunProgressFor(d.Run, "tx-locator")
	if d.Run.State != "paused" || p.Height != -1 || p.StagedBlocks != 0 || p.StagedHeight != nil || p.LogicalBatchFrom != nil {
		t.Fatal(d.Run, p)
	}
}

func TestTimelineRunLocatorAuthorityChangeDropsUnselectedStaging(t *testing.T) {
	a := runTestApp(t)
	s, err := openIndexStoreContext(context.Background(), a.dataDir, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BeginLocatorBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	b := utxoFixtureBlock(0, "", utxoFixtureCoinbase(998100, 50))
	b.Hash = testID(998101)
	if err = s.appendPreparedBlock(b, 0, "ephemeral", nil); err != nil {
		t.Fatal(err)
	}
	c := &indexRunConsumer{selection: indexRunSelection{Index: "tx-locator", Armed: true}, progress: indexRunProgress{Index: "tx-locator", From: 0, Height: 0, AddedBlocks: 1}, store: s}
	if err = a.reconcileRunConsumer(context.Background(), c, a.currentChainAuthority()); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := s.LocatorBatchStats(); n != 0 || s.DurableCheckpoint() != nil || c.progress.AddedBlocks != 0 || c.progress.Height != -1 {
		t.Fatal("unselected staged prefix survived", n, c.progress, s.DurableCheckpoint())
	}
}

func TestTimelineRunLocatorStageGuardReleasesPreparedBody(t *testing.T) {
	a := runTestApp(t)
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}}}); err != nil {
		t.Fatal(err)
	}
	s, err := openIndexStoreContext(context.Background(), a.dataDir, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lean, release := a.indexRunLocatorBatchGuard(runCtx, "gc-stage", indexRunSelection{Index: "tx-locator", Armed: true}, indexRunProgress{Index: "tx-locator"}, 0, genesisHashDisplay, false)
	defer release()
	if err = s.BeginLocatorBatch(runCtx); err != nil {
		t.Fatal(err)
	}
	collected := make(chan struct{})
	func() {
		p := &indexRunPrepared{block: utxoFixtureBlock(0, "", utxoFixtureCoinbase(998120, 50))}
		p.block.verifiedRaw = make([]byte, 8<<20)
		runtime.SetFinalizer(p, func(*indexRunPrepared) { close(collected) })
		bodyCtx := context.WithValue(runCtx, indexRunPreparedKey{}, p)
		bodyCtx = context.WithValue(bodyCtx, resolvedIngestionKey{}, &resolvedIngestionQueue{})
		bodyCtx = context.WithValue(bodyCtx, indexRunPublicationKey{}, func() error { runtime.KeepAlive(p); return nil })
		s.locatorContext = context.WithValue(bodyCtx, indexLocatorBatchGuardKey{}, lean)
		if err := s.appendPreparedBlock(p.block, 0, "ephemeral", nil); err != nil {
			t.Fatal(err)
		}
		s.locatorContext = nil // the next Apply replaces this one current-body context
	}()
	guard := s.locatorBatch.commits[0].guard
	if guard.Value(indexRunPreparedKey{}) != nil || guard.Value(resolvedIngestionKey{}) != nil {
		t.Fatal("staged guard retained full source context")
	}
	deadline := time.After(3 * time.Second)
	for {
		runtime.GC()
		select {
		case <-collected:
			runtime.KeepAlive(s)
			cancel()
			select {
			case <-guard.Done():
			case <-time.After(time.Second):
				t.Fatal("lean guard lost run cancellation")
			}
			return
		case <-deadline:
			t.Fatal("staged guard retained the previous prepared body")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestTimelineRunLocatorStageGuardRechecksCompactFences(t *testing.T) {
	a := runTestApp(t)
	d, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}}})
	if err != nil {
		t.Fatal(err)
	}
	selection, _ := runSelection(d.Prepared, "tx-locator")
	progress := indexRunProgress{Index: "tx-locator", AdmissionState: "admitted", From: 0, To: 1}
	base := cloneIndexRunDocument(d)
	base.Run = &indexRunRecord{ID: "compact-fence", State: "running", Request: d.Prepared, Consumers: []indexRunProgress{progress}}
	save := func(doc indexRunDocument) {
		a.indexRunMu.Lock()
		a.indexRunState = doc
		err := a.persistIndexRunLocked()
		a.indexRunMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	save(base)
	guard, release := a.indexRunLocatorBatchGuard(context.Background(), "compact-fence", selection, progress, 0, genesisHashDisplay, false)
	defer release()
	if err = checkIndexRunPublication(guard); err != nil {
		t.Fatal("valid compact fence", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*indexRunDocument)
		want   error
	}{
		{"replacement run", func(d *indexRunDocument) { d.Run.ID = "replacement" }, errIndexRunConsumerStopped},
		{"prepared Off", func(d *indexRunDocument) { d.Prepared.Consumers[0].Armed = false }, errIndexRunAdmissionChanged},
		{"changed options", func(d *indexRunDocument) { d.Prepared.Consumers[0].Mode = "full" }, errIndexRunAdmissionChanged},
		{"changed admission", func(d *indexRunDocument) { one := int64(1); d.Run.Consumers[0].EffectiveFrom = &one }, errIndexRunAdmissionChanged},
		{"Raw enabled", func(d *indexRunDocument) {
			d.Prepared.Consumers = append(d.Prepared.Consumers, indexRunSelection{Index: "blocks", Armed: true, indexRunOptions: indexRunOptions{Mode: "lean"}})
		}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneIndexRunDocument(base)
			test.mutate(&changed)
			save(changed)
			err := checkIndexRunPublication(guard)
			if err == nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatal("stale compact fence published", err)
			}
		})
	}
}

func TestResolvedCoordinatorOwnershipPreservesOtherTracksAndDependencyBodies(t *testing.T) {
	a := runTestApp(t)
	loc := indexRunSelection{Index: "tx-locator", Armed: true}
	ins := indexRunSelection{Index: "inscriptions", Armed: true}
	d, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{loc, ins}})
	if err != nil {
		t.Fatal(err)
	}
	loc, _ = runSelection(d.Prepared, "tx-locator")
	a.indexRunMu.Lock()
	a.indexRunState.Run = &indexRunRecord{ID: "owned-source", State: "running", Request: indexRunRequest{Consumers: []indexRunSelection{loc}}, Consumers: []indexRunProgress{{Index: "tx-locator", AdmissionState: "admitted", From: 0, To: 1, Height: -1}}}
	err = a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), indexRunSourceOwnershipKey{}, indexRunSourceOwnership{"owned-source", 0, genesisHashDisplay})
	if _, err = a.fetchAndDecodeContext(ctx, "0"); err != nil {
		t.Fatal(err)
	}
	_, locHead, err := a.browseStore("tx-locator")
	if err != nil || len(locHead.Blocks) != 0 {
		t.Fatal("scheduled locator body duplicated", locHead, err)
	}
	_, insHead, err := a.browseStore("inscriptions")
	if err != nil || len(insHead.Blocks) != 1 {
		t.Fatal("unselected armed track lost fanout", insHead, err)
	}
	if _, err = a.fetchAndDecodeContext(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	_, locHead, err = a.browseStore("tx-locator")
	if err != nil || len(locHead.Blocks) != 1 {
		t.Fatal("dependency body lost fanout", locHead, err)
	}
}
