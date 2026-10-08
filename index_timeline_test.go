package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Synthetic selected headers isolate inventory semantics from consensus sync.
// Nothing in this fixture is offered to a peer or used to test block validity.
func timelineFixture(t *testing.T, count int) (*app, [][]byte, []string) {
	t.Helper()
	dir := t.TempDir()
	a := &app{dataDir: dir, headersPath: filepath.Join(dir, "headers.bin"), cacheIndex: newCacheIndex(), settings: appSettings{CoreDisabled: true, CoreMountDisabled: true, NetworkDisabled: true}, status: appStatus{HeaderHeight: int64(count - 1), HeaderCount: int64(count), Ready: count > 0}}
	var all []byte
	var raws [][]byte
	var hashes []string
	for i := 0; i < count; i++ {
		raw := make([]byte, 81)
		binary.LittleEndian.PutUint32(raw[:4], 1)
		binary.LittleEndian.PutUint32(raw[68:72], uint32(1231006505+i*600))
		binary.LittleEndian.PutUint32(raw[76:80], uint32(i))
		digest := hash256(raw[:80])
		raws, hashes = append(raws, raw), append(hashes, reverseHex(digest[:]))
		all = append(all, raw[:80]...)
	}
	if err := os.WriteFile(a.headersPath, all, 0600); err != nil {
		t.Fatal(err)
	}
	return a, raws, hashes
}

func timelineTrack(t *testing.T, view indexTimelineView, id string) indexTimelineTrack {
	t.Helper()
	for _, track := range view.Tracks {
		if track.ID == id {
			return track
		}
	}
	t.Fatalf("missing track %s", id)
	return indexTimelineTrack{}
}

func timelineWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestTimelineSparseRawCoveragePreservesGapsAndIgnoresStaleEntries(t *testing.T) {
	a, raws, hashes := timelineFixture(t, 6)
	for _, height := range []int{1, 2, 3, 4, 5} {
		prefix := fmt.Sprintf("%d-%s", height, hashes[height])
		a.cacheIndex.Blocks[hashes[height]] = cachedBlockEntry{Height: int64(height), Hash: hashes[height], Prefix: prefix, Private: true}
		data := raws[height]
		if height == 2 {
			continue
		} // evicted but metadata remains
		if height == 4 {
			data = data[:40]
		} // truncated header
		if height == 5 {
			data = raws[0]
		} // wrong file at recorded path
		timelineWrite(t, filepath.Join(a.dataDir, "blocks", "raw", prefix+".block"), data)
	}
	a.coverage.IndexedBlocks = []heightInterval{{From: 0, To: 5}}
	a.status.HeaderTargetHeight, a.status.HeaderTargetSource = 100, "peer estimate"
	a.status.HeaderState, a.status.HeaderLastProgress = "waiting", "2026-10-08T10:00:00Z"
	view := a.indexTimeline(indexDefinitions(), nil, nil)
	blocks := timelineTrack(t, view, "blocks")
	if !reflect.DeepEqual(blocks.Coverage, []heightInterval{{1, 1}, {3, 3}}) || !reflect.DeepEqual(blocks.Gaps, []heightInterval{{0, 0}, {2, 2}, {4, 5}}) || !blocks.CoverageComplete {
		t.Fatalf("invented or merged missing bodies: %+v", blocks)
	}
	if !view.TipKnown || view.TipHeight != 5 || view.TipHash != hashes[5] || view.TargetHeight != 100 || view.HeaderState != "waiting" || view.LastProgress != a.status.HeaderLastProgress {
		t.Fatalf("lost selected tip/freshness: %+v", view)
	}
	if got := timelineTrack(t, view, "headers"); !reflect.DeepEqual(got.Coverage, []heightInterval{{0, 5}}) {
		t.Fatal(got)
	}
	if a.indexCancel != nil || a.headerNeededHeight != 0 || len(a.cacheIndex.Blocks) != 5 {
		t.Fatal("timeline started work or changed cache")
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes")); !os.IsNotExist(err) {
		t.Fatal("timeline created index state", err)
	}
}

func TestTimelineKeepsCheckpointAndProviderRangesSeparateFromRawPossession(t *testing.T) {
	a, _, hashes := timelineFixture(t, 5)
	instances := []indexInstance{
		{Definition: "blocks", Coverage: []heightInterval{{0, 4}}, Checkpoint: &indexCheckpoint{From: 0, Height: 4, BlockHash: hashes[4]}},
		{Definition: "inscriptions", Coverage: []heightInterval{{2, 4}}, Checkpoint: &indexCheckpoint{From: 2, Height: 4, BlockHash: hashes[4]}},
	}
	providers := []indexProviderView{{ID: "blocks", Provider: "bitcoin_core", Coverage: []heightInterval{{0, 1000}}, Verification: "rechecked_on_read"}}
	view := a.indexTimeline(indexDefinitions(), providers, instances)
	blocks := timelineTrack(t, view, "blocks")
	if len(blocks.Coverage) != 0 || !reflect.DeepEqual(blocks.RecordedCoverage, []heightInterval{{0, 4}}) || !reflect.DeepEqual(blocks.ReportedCoverage, []heightInterval{{0, 1000}}) {
		t.Fatal(blocks)
	}
	locked := timelineTrack(t, view, "inscriptions")
	if !locked.Locked || !reflect.DeepEqual(locked.Coverage, []heightInterval{{2, 4}}) || releaseFeatureAvailable("inscriptions") {
		t.Fatal(locked)
	}
	instances[0].Checkpoint.BlockHash = strings.Repeat("f", 64)
	view = a.indexTimeline(indexDefinitions(), providers, instances)
	if blocks = timelineTrack(t, view, "blocks"); len(blocks.RecordedCoverage) != 0 || blocks.CoverageComplete || len(blocks.Gaps) != 0 || len(view.Errors) == 0 {
		t.Fatal("unanchored record was treated as current", view)
	}
}

func TestTimelineIncludesArchiveRetainedAndMountedThenRechecksFilesAndReorg(t *testing.T) {
	a, raws, hashes := timelineFixture(t, 5)
	archiveDir := filepath.Join(a.dataDir, "archive", "blocks", hashes[2][:2])
	archiveRaw := filepath.Join(archiveDir, hashes[2]+".blk")
	receipt, _ := json.Marshal(archiveReceipt{Schema: 1, VerifierVersion: blockVerifierVersion, Hash: hashes[2], Height: 2, Bytes: len(raws[2])})
	timelineWrite(t, filepath.Join(archiveDir, hashes[2]+".json"), receipt)
	timelineWrite(t, archiveRaw, raws[2])
	retainedPath := filepath.Join(a.dataDir, "indexes", "sources", hashes[4]+".block")
	timelineWrite(t, retainedPath, raws[4])
	mountedDir := filepath.Join(a.dataDir, "mounted")
	mountedPath := filepath.Join(mountedDir, "blk00000.dat")
	xor := [8]byte{1, 4, 7, 9, 2, 6, 0, 3}
	mountedRaw := append(make([]byte, 8), raws[0]...)
	coreXOR(mountedRaw, 0, xor)
	timelineWrite(t, mountedPath, mountedRaw)
	a.coreStore = coreBlockStore{BlocksDir: mountedDir, XORKey: xor, ByHeight: map[int64]string{0: hashes[0]}, ByHash: map[string]coreBlockLocator{hashes[0]: {Offset: 8, Length: uint32(len(raws[0])), Height: 0}}, Status: coreStoreStatus{Available: true, Mounted: true, ScanComplete: true}}
	a.settings.CoreMountDisabled = false
	view := a.indexTimeline(indexDefinitions(), nil, nil)
	if got := timelineTrack(t, view, "blocks"); !reflect.DeepEqual(got.Coverage, []heightInterval{{0, 0}, {2, 2}, {4, 4}}) || !got.CoverageComplete {
		t.Fatal(got, view.Errors)
	}
	if hint := a.timelineSourceHeights[hashes[4]]; hint.Height != 4 {
		t.Fatal("retained lookup hint missing", hint)
	}
	if err := os.Remove(archiveRaw); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(mountedPath, 12); err != nil {
		t.Fatal(err)
	}
	changed := append([]byte(nil), raws[4][:80]...)
	changed[76] = 99
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteAt(changed, 4*80); err != nil {
		t.Fatal(err)
	}
	f.Close()
	view = a.indexTimeline(indexDefinitions(), nil, nil)
	if got := timelineTrack(t, view, "blocks"); len(got.Coverage) != 0 {
		t.Fatal("stale files or reorg survived snapshot", got)
	}
	if bytes, err := os.ReadFile(retainedPath); err != nil || !reflect.DeepEqual(bytes, raws[4]) {
		t.Fatal("timeline mutated orphaned retained source")
	}
}

func TestTimelineUnknownCorruptAndPendingHeadersDoNotInventGenesisOrRepair(t *testing.T) {
	for _, mode := range []string{"missing", "empty", "partial", "pending"} {
		t.Run(mode, func(t *testing.T) {
			a, raws, _ := timelineFixture(t, 1)
			switch mode {
			case "missing":
				if err := os.Remove(a.headersPath); err != nil {
					t.Fatal(err)
				}
			case "empty":
				timelineWrite(t, a.headersPath, nil)
			case "partial":
				timelineWrite(t, a.headersPath, raws[0])
			case "pending":
				timelineWrite(t, headerAppendMarkerPath051(a.headersPath), []byte("pending fixture"))
			}
			before, _ := os.ReadFile(a.headersPath)
			view := a.indexTimeline(indexDefinitions(), nil, nil)
			if view.TipKnown || view.TipHeight != -1 || view.TargetHeight != -1 {
				t.Fatal(view)
			}
			for _, track := range view.Tracks {
				if len(track.Coverage) != 0 || len(track.Gaps) != 0 || track.CoverageComplete {
					t.Fatal("unknown became genesis", track)
				}
			}
			if (mode == "partial" || mode == "pending") && len(view.Errors) == 0 {
				t.Fatal("unavailable header snapshot hid error")
			}
			after, _ := os.ReadFile(a.headersPath)
			if !bytes.Equal(before, after) {
				t.Fatal("timeline repaired or changed header file")
			}
			if mode == "pending" {
				if _, err := os.Stat(headerAppendMarkerPath051(a.headersPath)); err != nil {
					t.Fatal("timeline consumed append marker", err)
				}
			}
		})
	}
}

func TestTimelineBoundedInspectionAndMetadataErrorsWithholdUnknownGaps(t *testing.T) {
	a, _, hashes := timelineFixture(t, 1)
	for n := 0; n <= timelineSourceLimit; n++ {
		a.cacheIndex.Blocks[fmt.Sprintf("entry-%05d", n)] = cachedBlockEntry{Hash: hashes[0], Height: 0, Prefix: "missing"}
	}
	view := a.indexTimeline(indexDefinitions(), nil, nil, "inscriptions: head could not be read")
	for _, id := range []string{"blocks", "inscriptions"} {
		track := timelineTrack(t, view, id)
		if track.CoverageComplete || len(track.Gaps) != 0 {
			t.Fatal("partial inspection invented absence", track)
		}
	}
	if len(view.Errors) < 2 {
		t.Fatal("incomplete status missing diagnostics", view.Errors)
	}
}

func TestTimelineStatusProjectionIsAdditiveAndPreservesPrivatePolicy(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	entry := a.cacheIndex.Blocks[genesisHashDisplay]
	entry.Private = true
	a.cacheIndex.Blocks[genesisHashDisplay] = entry
	view := a.indexStatus()
	if !view.Timeline.TipKnown || view.Timeline.TipHeight != 0 || len(view.Definitions) == 0 || len(view.Providers) == 0 {
		t.Fatal(view)
	}
	if track := timelineTrack(t, view.Timeline, "blocks"); !reflect.DeepEqual(track.Coverage, []heightInterval{{0, 0}}) {
		t.Fatal(track)
	}
	if !a.cacheIndex.Blocks[genesisHashDisplay].Private || a.indexCancel != nil || a.indexJob.ID != "" {
		t.Fatal("status published data or started indexing")
	}
	b, err := json.Marshal(view)
	if err != nil || !strings.Contains(string(b), "\"timeline\":") {
		t.Fatal(string(b), err)
	}
}

func TestTimelineConfiguredMountMustFinishInventoryBeforeClaimingGaps(t *testing.T) {
	ready := coreStoreStatus{Available: true, Mounted: true, ScanComplete: true}
	for _, tc := range []struct {
		name       string
		configured bool
		disabled   bool
		status     coreStoreStatus
		complete   bool
	}{
		{name: "unconfigured", status: coreStoreStatus{Error: "not configured"}, complete: true},
		{name: "disabled", configured: true, disabled: true, complete: true},
		{name: "pending", configured: true},
		{name: "scanning", configured: true, status: coreStoreStatus{Available: true, Mounted: true, ScanRunning: true}},
		{name: "failed", configured: true, status: coreStoreStatus{Available: true, Mounted: true, ScanComplete: true, Error: "read failed"}},
		{name: "unavailable", configured: true, status: coreStoreStatus{ScanComplete: true}},
		{name: "ready", configured: true, status: ready, complete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _, _ := timelineFixture(t, 3)
			a.settings.CoreMountDisabled = tc.disabled
			if tc.configured {
				a.settings.BitcoinBlocksDir = filepath.Join(a.dataDir, "configured-mount")
			}
			a.coreStore.Status = tc.status
			view := a.indexTimeline(indexDefinitions(), nil, nil)
			track := timelineTrack(t, view, "blocks")
			if track.CoverageComplete != tc.complete {
				t.Fatalf("complete=%v want=%v: %+v", track.CoverageComplete, tc.complete, track)
			}
			if !tc.complete && (len(track.Gaps) != 0 || len(view.Errors) == 0) {
				t.Fatal("incomplete mounted inventory invented missing ranges", track, view.Errors)
			}
		})
	}
	// Useful observed files remain visible while the broader mount scan runs.
	a, raws, hashes := timelineFixture(t, 3)
	dir := filepath.Join(a.dataDir, "mount")
	timelineWrite(t, filepath.Join(dir, "blk00000.dat"), append(make([]byte, 8), raws[1]...))
	a.settings.CoreMountDisabled = false
	a.coreStore = coreBlockStore{BlocksDir: dir, Status: coreStoreStatus{Available: true, Mounted: true, ScanRunning: true}, ByHeight: map[int64]string{1: hashes[1]}, ByHash: map[string]coreBlockLocator{hashes[1]: {Height: 1, Offset: 8, Length: uint32(len(raws[1]))}}}
	track := timelineTrack(t, a.indexTimeline(indexDefinitions(), nil, nil), "blocks")
	if track.CoverageComplete || len(track.Gaps) != 0 || !reflect.DeepEqual(track.Coverage, []heightInterval{{1, 1}}) {
		t.Fatal("partial positives were lost or absence inferred", track)
	}
}

func TestTimelineExplicitInstanceGapsAreNeverPaintedAsIndexed(t *testing.T) {
	a, _, hashes := timelineFixture(t, 10)
	instances := []indexInstance{{Definition: "inscriptions", Coverage: []heightInterval{{1, 8}}, Gaps: []heightInterval{{6, 6}, {2, 3}, {3, 4}, {12, 20}}, Checkpoint: &indexCheckpoint{From: 1, Height: 8, BlockHash: hashes[8]}}}
	track := timelineTrack(t, a.indexTimeline(indexDefinitions(), nil, instances), "inscriptions")
	want := []heightInterval{{1, 1}, {5, 5}, {7, 8}}
	if !reflect.DeepEqual(track.Coverage, want) || !reflect.DeepEqual(track.RecordedCoverage, want) {
		t.Fatal("explicit gaps were filled", track)
	}
	if !reflect.DeepEqual(track.Gaps, []heightInterval{{0, 0}, {2, 4}, {6, 6}, {9, 9}}) {
		t.Fatal(track.Gaps)
	}
	for _, source := range track.Sources {
		if source.Kind == "committed_records" && !reflect.DeepEqual(source.Ranges, want) {
			t.Fatal("source detail filled explicit gaps", source)
		}
	}
	if !reflect.DeepEqual(instances[0].Coverage, []heightInterval{{1, 8}}) || len(instances[0].Gaps) != 4 {
		t.Fatal("projection mutated source instance")
	}
}
