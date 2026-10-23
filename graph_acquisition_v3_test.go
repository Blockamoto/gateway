package main

import (
	"bytes"
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestGraphAndExplorerShareAcquisitionAndTimelineRawAuthority(t *testing.T) {
	for _, on := range []bool{false, true} {
		name := "off"
		if on {
			name = "on"
		}
		t.Run(name, func(t *testing.T) {
			a := independentTestApp(t)
			a.settings.GraphIndex = true
			if _, e := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "blocks", Armed: on}}}); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			target, e := a.resolveBlockTargetContext(ctx, "0")
			if e != nil {
				t.Fatal(e)
			}
			raw := testGenesisBlockPayload(t)
			base, e := parseBlockDetailed(target.Height, target.HashDisplay, target.ExpectedHeader, raw, "one verified shared source", false)
			if e != nil {
				t.Fatal(e)
			}
			base.SourceNetwork = "bitcoin"
			release := make(chan struct{})
			var calls atomic.Int32
			a.blockBaseAcquire = func(work context.Context, requested blockTarget, settings appSettings) (blockView, error) {
				calls.Add(1)
				select {
				case <-work.Done():
					return blockView{}, work.Err()
				case <-release:
					return base, nil
				}
			}
			done := make(chan error, 2)
			go func() {
				v, e := a.graphBlockForHeightContext(ctx, 0)
				if e == nil && (!v.Verification.HeaderChainMatch || v.Hash != target.HashDisplay) {
					t.Error("Graph lost its selected-chain requirement")
				}
				done <- e
			}()
			go func() { _, e := a.fetchAndDecodeTargetContext(ctx, target); done <- e }()
			acquisitionWaiters(t, a, 2)
			close(release)
			for i := 0; i < 2; i++ {
				if e := <-done; e != nil {
					t.Fatal(e)
				}
			}
			if calls.Load() != 1 {
				t.Fatal("Graph used an independent fetch or parse", calls.Load())
			}
			if len(a.graphCoverage()) != 0 {
				t.Fatal("shared fetch recursively started graph indexing")
			}
			if on {
				got, _, e := readIndexedRawBody(a.dataDir, target, nil)
				if e != nil || !bytes.Equal(got, raw) {
					t.Fatal("Graph/Explorer Raw On did not retain exact bytes", e)
				}
			} else if _, e := os.Stat(rawSegmentDir(a.dataDir)); !os.IsNotExist(e) {
				t.Fatal("Graph bypassed Raw Off", e)
			}
		})
	}
}

func TestGraphBuildCancellationReachesSharedSource(t *testing.T) {
	a := independentTestApp(t)
	a.settings.GraphIndex = true
	entered := make(chan struct{})
	stopped := make(chan struct{})
	a.blockBaseAcquire = func(work context.Context, requested blockTarget, settings appSettings) (blockView, error) {
		close(entered)
		<-work.Done()
		close(stopped)
		return blockView{}, work.Err()
	}
	a.graphBuild = graphBuildState{From: 0, To: 0, Current: -1, Running: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { a.runGraphBuild(ctx); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("graph build did not request shared source")
	}
	cancel()
	for _, signal := range []chan struct{}{done, stopped} {
		select {
		case <-signal:
		case <-time.After(time.Second):
			t.Fatal("graph cancellation did not drain shared acquisition")
		}
	}
	if len(a.graphCoverage()) != 0 {
		t.Fatal("canceled graph build published coverage")
	}
}
