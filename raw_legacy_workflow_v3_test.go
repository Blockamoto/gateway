package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func armLegacyBuildTracks(t *testing.T, a *app, req indexBuildRequest) {
	t.Helper()
	prepared := a.indexRunSnapshot().Prepared
	for _, id := range append([]string{req.Index}, req.Outputs...) {
		// Related IDs are owned by the inscription consumer, not a separate Arm.
		if id == inscriptionLocatorIndex {
			continue
		}
		choice, ok := runSelection(prepared, id)
		if !ok {
			choice = indexRunSelection{Index: id}
		}
		choice.Armed = true
		replaceIndexRunSelection(&prepared, choice)
	}
	if _, e := a.prepareIndexRun(prepared); e != nil {
		t.Fatal(e)
	}
}

func TestLegacyRawBuildWithExplicitTimelineArmStillCompletes(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	zero := int64(0)
	req := indexBuildRequest{Index: "blocks", From: &zero, To: &zero, Retention: "ephemeral"}
	armLegacyBuildTracks(t, a, req)
	if _, e := a.startIndexBuild(req); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	job, e := a.waitIndexBuild(ctx)
	if e != nil || job.State != "complete" || job.Height != 0 {
		t.Fatal(job, e)
	}
	s, e := indexStoreHead(a.dataDir, "blocks")
	if e != nil || s.checkpoint == nil {
		t.Fatal(e)
	}
	b, e := s.readBatch(s.head.Commitment)
	if e != nil || b.Bitcoin == nil || b.Bitcoin.BodySchema != 3 || len(b.Bitcoin.TxIDs) != 0 {
		t.Fatal("explicit Raw workflow did not retain block-only evidence", e)
	}
}

func TestLegacyRawBuildRequiresTimelineArmAtAdmissionAndLive(t *testing.T) {
	for _, state := range []string{"unconfigured", "off"} {
		t.Run(state, func(t *testing.T) {
			a, _ := testAppWithGenesis(t)
			a.updateBackgroundWork.Wait()
			if state == "off" {
				if _, e := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "blocks", Armed: false}}}); e != nil {
					t.Fatal(e)
				}
			}
			zero := int64(0)
			for _, req := range []indexBuildRequest{{Index: "blocks", From: &zero, To: &zero}, {Index: "tx-locator", From: &zero, To: &zero, Outputs: []string{"blocks"}}} {
				for _, live := range []bool{false, true} {
					if _, e := a.startIndexBuildInternal(req, live); e == nil || !strings.Contains(e.Error(), "arm Raw Blocks in the timeline") {
						t.Fatal("legacy admission bypass", state, req.Index, live, e)
					}
				}
				if _, e := a.startIndexBuild(req); e == nil || !strings.Contains(e.Error(), "arm Raw Blocks in the timeline") {
					t.Fatal("user admission bypass", e)
				}
			}
			for _, rel := range []string{"indexes/job.json", "indexes/blocks/head.json", "indexes/blocks/segments-v3"} {
				if _, e := os.Stat(filepath.Join(a.dataDir, filepath.FromSlash(rel))); !os.IsNotExist(e) {
					t.Fatal("rejected route created work or Raw storage", rel, e)
				}
			}
		})
	}
}

func TestLegacyRawWorkerAndPublicationRequireCurrentTimelineArm(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	a.updateBackgroundWork.Wait()
	// Restoring an older saved Live job cannot bypass the new admission gate.
	s, e := indexStoreHead(a.dataDir, "blocks")
	if e != nil {
		t.Fatal(e)
	}
	a.runIndexBuild(context.Background(), indexJob{ID: "restored-old-live", Index: "blocks", Live: true, From: 0, To: 0, Height: -1, Retention: "retain"}, s)
	if _, e = os.Stat(filepath.Join(a.dataDir, "indexes", "blocks", "head.json")); !os.IsNotExist(e) {
		t.Fatal("old Live worker wrote Raw", e)
	}
	armLegacyBuildTracks(t, a, indexBuildRequest{Index: "blocks"})
	ctx := legacyRawPublicationContext(context.Background(), a.dataDir, "blocks")
	s, e = openIndexStoreContext(ctx, a.dataDir, "blocks")
	if e != nil {
		t.Fatal(e)
	}
	view, e := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "explicit storage fixture", false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "blocks", Armed: false}}}); e != nil {
		t.Fatal(e)
	}
	if e = s.appendBlock(view, 0, "retain"); e == nil {
		t.Fatal("legacy publication outlived Raw Off")
	}
	if _, e = os.Stat(rawSegmentPath(a.dataDir, 0)); !os.IsNotExist(e) {
		t.Fatal("Off legacy worker retained bytes", e)
	}
	// The isolated low-level API remains available to an explicit storage test.
	direct, e := openIndexStore(t.TempDir(), "blocks")
	if e != nil {
		t.Fatal(e)
	}
	if e = direct.appendBlock(view, 0, "ephemeral"); e != nil {
		t.Fatal("unit storage seam was globally changed", e)
	}
}
