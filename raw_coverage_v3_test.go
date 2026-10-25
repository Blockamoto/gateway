package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func rawCoverageTestSnapshot(t *testing.T, a *app, from, to *int64) rawCoverageView {
	t.Helper()
	f, e := os.Open(a.headersPath)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		t.Fatal(e)
	}
	return a.rawCoverageSnapshot(&timelineLocalReader{file: f, count: st.Size() / 80, hashes: map[int64]string{}}, from, to)
}
func waitRawCoverage(t *testing.T, a *app) rawCoverageView {
	t.Helper()
	// Sparse recovery advances through bounded height pages, including empty
	// pages between distant ranges, and durably saves its cursor per turn.
	// Windows suites can spend over 30 seconds making healthy progress under
	// filesystem contention. Keep a separate stall limit and an absolute bound.
	end := time.Now().Add(2 * time.Minute)
	stalled := time.Now().Add(30 * time.Second)
	revision := ""
	for time.Now().Before(end) {
		v := rawCoverageTestSnapshot(t, a, nil, nil)
		if v.Complete {
			return v
		}
		if v.Revision != revision {
			revision = v.Revision
			stalled = time.Now().Add(30 * time.Second)
		} else if time.Now().After(stalled) {
			t.Fatal("coverage recovery stopped making progress", v)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("coverage did not finish", rawCoverageTestSnapshot(t, a, nil, nil))
	return rawCoverageView{}
}

// These synthetic selected headers/frame catalogs isolate metadata projection.
// Full Bitcoin-body authentication is covered by the genuine producer tests.
func rawCoverageMetadataFixture(t *testing.T, a *app, ranges []heightInterval) []rawSegmentInventoryRow {
	t.Helper()
	if e := os.MkdirAll(rawSegmentDir(a.dataDir), 0700); e != nil {
		t.Fatal(e)
	}
	if e := atomicWriteJSON(filepath.Join(rawSegmentDir(a.dataDir), "writer.json"), rawSegmentCursor{Schema: 3}); e != nil {
		t.Fatal(e)
	}
	hf, e := os.OpenFile(a.headersPath, os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer hf.Close()
	last := ranges[len(ranges)-1].To
	if e = hf.Truncate((last + 1) * 80); e != nil {
		t.Fatal(e)
	}
	bf, e := os.OpenFile(rawSegmentPath(a.dataDir, 0), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer bf.Close()
	files := map[string]*os.File{}
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	rows := []rawSegmentInventoryRow{}
	for _, interval := range ranges {
		for h := interval.From; h <= interval.To; h++ {
			raw := make([]byte, 81)
			binary.LittleEndian.PutUint64(raw, uint64(h+1))
			hash := hash256(raw[:80])
			display := reverseHex(hash[:])
			raw[80] = 1
			if _, e = hf.WriteAt(raw[:80], h*80); e != nil {
				t.Fatal(e)
			}
			offset := int64(len(rows)) * (rawSegmentFrameBytes + 81)
			frame := make([]byte, rawSegmentFrameBytes)
			copy(frame, rawSegmentMagic[:])
			binary.LittleEndian.PutUint32(frame[8:], 81)
			binary.LittleEndian.PutUint64(frame[12:], uint64(h))
			copy(frame[20:], hash[:])
			if _, e = bf.WriteAt(append(frame, raw...), offset); e != nil {
				t.Fatal(e)
			}
			sum := sha256.Sum256(raw)
			body := &indexedRawBody{Segment: &rawSegmentLocation{File: 0, Offset: offset}, SHA256: hex.EncodeToString(sum[:]), Bytes: 81, Header: hex.EncodeToString(raw[:80])}
			encoded, e := encodeRawBodyCatalog(h, display, body, 1)
			if e != nil {
				t.Fatal(e)
			}
			path := rawBodyCatalogHeightPath(a.dataDir, h)
			f := files[path]
			if f == nil {
				f, e = os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
				if e != nil {
					t.Fatal(e)
				}
				files[path] = f
			}
			if _, e = f.WriteAt(encoded, (h%rawSegmentMapRows)*rawSegmentMapBytes); e != nil {
				t.Fatal(e)
			}
			rows = append(rows, rawSegmentInventoryRow{Height: h, Hash: display, Location: *body.Segment, Bytes: 81, Count: 1})
		}
	}
	return rows
}
func TestRawCoverageMoreThan4096AcrossTwoRangesRestartDeletionAndReorg(t *testing.T) {
	a := independentTestApp(t)
	rows := rawCoverageMetadataFixture(t, a, []heightInterval{{0, 2499}, {792435, 794934}})
	initial := rawCoverageTestSnapshot(t, a, nil, nil)
	if initial.Complete {
		t.Fatal("existing catalog claimed complete before metadata recovery")
	}
	v := waitRawCoverage(t, a)
	if v.Total == nil || *v.Total != 5000 || len(v.Ranges) != 2 || v.To == nil || *v.To != 794934 {
		t.Fatal(v)
	}
	lo, hi := int64(794900), int64(794934)
	visible := rawCoverageTestSnapshot(t, a, &lo, &hi)
	if visible.Visible == nil || visible.Visible.Total == nil || *visible.Visible.Total != 35 || *visible.Total != 5000 {
		t.Fatal(visible)
	}
	key, _ := filepath.Abs(a.dataDir)
	rawCoverageStates.Delete(key)
	again := waitRawCoverage(t, a)
	if *again.Total != 5000 {
		t.Fatal("restart lost sparse retained coverage", again)
	}
	prunes := rawSegmentPruneState{Schema: 3, Locations: []rawSegmentLocation{rows[len(rows)-1].Location}}
	prunes.Digest = indexDigest(prunes)
	if e := atomicWriteJSON(rawSegmentPrunePath(a.dataDir), prunes); e != nil {
		t.Fatal(e)
	}
	rawCoverageInvalidated(a.dataDir)
	deleted := waitRawCoverage(t, a)
	if *deleted.Total != 4999 || *deleted.To != 794933 {
		t.Fatal("pruned body remained counted", deleted)
	}
	f, e := os.OpenFile(a.headersPath, os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	header := make([]byte, 80)
	if _, e = f.ReadAt(header, 794933*80); e != nil {
		t.Fatal(e)
	}
	header[12] ^= 1
	_, e = f.WriteAt(header, 794933*80)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	// Alter the saved tip too so the selected-chain anchor detects the reorg.
	f, _ = os.OpenFile(a.headersPath, os.O_RDWR, 0600)
	_, e = f.WriteAt(header, 794934*80)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	changed := rawCoverageTestSnapshot(t, a, nil, nil)
	if changed.Complete {
		t.Fatal("reorg reused old coverage as exact")
	}
	reorg := waitRawCoverage(t, a)
	if *reorg.Total != 4998 {
		t.Fatal(reorg)
	}
}

func TestRawCoverageRestartDetectsTruncatedOrRemovedBodySegment(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "truncated", true: "removed"}[remove], func(t *testing.T) {
			a := independentTestApp(t)
			rawCoverageMetadataFixture(t, a, []heightInterval{{0, 2}})
			before := waitRawCoverage(t, a)
			if *before.Total != 3 {
				t.Fatal(before)
			}
			var e error
			if remove {
				e = os.Remove(rawSegmentPath(a.dataDir, 0))
			} else {
				e = os.Truncate(rawSegmentPath(a.dataDir, 0), 8)
			}
			if e != nil {
				t.Fatal(e)
			}
			key, _ := filepath.Abs(a.dataDir)
			rawCoverageStates.Delete(key)
			first := rawCoverageTestSnapshot(t, a, nil, nil)
			if first.Complete {
				t.Fatal("changed segment reused exact old summary", first)
			}
			after := waitRawCoverage(t, a)
			if *after.Total != 0 {
				t.Fatal("absent bytes remained counted", after)
			}
		})
	}
}
func TestRawCoveragePublicationDoesNotHideReorgOrUnavailableHeaders(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "reorg-before-status", true: "header-read-unavailable"}[unavailable], func(t *testing.T) {
			a := independentTestApp(t)
			rows := rawCoverageMetadataFixture(t, a, []heightInterval{{0, 2}})
			if v := waitRawCoverage(t, a); *v.Total != 3 {
				t.Fatal(v)
			}
			headerBytes, e := os.ReadFile(a.headersPath)
			if e != nil {
				t.Fatal(e)
			}
			changed := append([]byte(nil), headerBytes...)
			if unavailable {
				changed = append(changed, 1)
			} else {
				changed[2*80+12] ^= 1
			}
			a.headerChainMu.Lock()
			e = os.WriteFile(a.headersPath, changed, 0600)
			a.headerChainMu.Unlock()
			if e != nil {
				t.Fatal(e)
			}
			// A new durable publication occurs before any status snapshot can
			// notice the changed selected-chain anchor.
			rawSegmentMu.Lock()
			rawCoveragePublished(a.dataDir, 0, rows[0].Hash, 0)
			rawSegmentMu.Unlock()
			m := a.rawCoverageState()
			m.mu.Lock()
			exact := m.validated && m.d.Complete
			m.mu.Unlock()
			if exact {
				t.Fatal("publication kept stale coverage exact")
			}
			if unavailable {
				a.headerChainMu.Lock()
				e = os.WriteFile(a.headersPath, headerBytes, 0600)
				a.headerChainMu.Unlock()
				if e != nil {
					t.Fatal(e)
				}
			}
			v := waitRawCoverage(t, a)
			want := int64(2)
			if unavailable {
				want = 3
			}
			if *v.Total != want {
				t.Fatal("recovery retained stale branch or lost saved body", v)
			}
		})
	}
}
func TestRawCoverageRecoveryInitializationSerializesPublicationAndMissingWriter(t *testing.T) {
	a := independentTestApp(t)
	rawCoverageMetadataFixture(t, a, []heightInterval{{0, 0}})
	if e := os.Remove(filepath.Join(rawSegmentDir(a.dataDir), "writer.json")); e != nil {
		t.Fatal(e)
	}
	m := a.rawCoverageState()
	m.mu.Lock()
	m.running = true
	m.done = make(chan struct{})
	m.mu.Unlock()
	rawSegmentMu.Lock()
	go a.rebuildRawCoverage(m)
	time.Sleep(40 * time.Millisecond)
	m.mu.Lock()
	initialized := m.validated
	m.mu.Unlock()
	if initialized {
		rawSegmentMu.Unlock()
		t.Fatal("recovery initialized while Raw publication held its writer lock")
	}
	rows := rawCoverageMetadataFixture(t, a, []heightInterval{{0, 1}})
	rawCoveragePublished(a.dataDir, 1, rows[1].Hash, 0)
	rawSegmentMu.Unlock()
	select {
	case <-m.done:
	case <-time.After(10 * time.Second):
		t.Fatal("recovery blocked")
	}
	v := waitRawCoverage(t, a)
	if *v.Total != 2 || *v.To != 1 {
		t.Fatal("initialization dropped concurrent publication", v)
	}
	key, _ := filepath.Abs(a.dataDir)
	rawCoverageStates.Delete(key)
	if e := os.Remove(filepath.Join(rawSegmentDir(a.dataDir), "writer.json")); e != nil {
		t.Fatal(e)
	}
	if v = waitRawCoverage(t, a); *v.Total != 2 {
		t.Fatal("missing writer cursor hid durable catalog", v)
	}
}
func TestRawCoverageLegacyOnlyManifestDetectsBodyTruncation(t *testing.T) {
	a := independentTestApp(t)
	target, body := acquisitionFixture(t)
	if _, e := storeIndexedRawBody(a.dataDir, 0, body.Hash, target.ExpectedHeader, body.verifiedRaw); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(rawSegmentDir(a.dataDir)); !os.IsNotExist(e) {
		t.Fatal("legacy fixture unexpectedly has v3", e)
	}
	before, e := rawCoverageManifest(context.Background(), a.dataDir)
	if e != nil {
		t.Fatal(e)
	}
	if len(before) != 1 {
		t.Fatal("legacy fingerprints missing or per-body", before)
	}
	if v := waitRawCoverage(t, a); *v.Total != 1 {
		t.Fatal(v)
	}
	path, e := indexedRawBodyPath(a.dataDir, body.Hash)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Truncate(path, 8); e != nil {
		t.Fatal(e)
	}
	after, e := rawCoverageManifest(context.Background(), a.dataDir)
	if e != nil {
		t.Fatal(e)
	}
	if indexDigest(before) == indexDigest(after) {
		t.Fatal("legacy aggregate ignored truncation")
	}
	key, _ := filepath.Abs(a.dataDir)
	rawCoverageStates.Delete(key)
	if v := waitRawCoverage(t, a); *v.Total != 0 {
		t.Fatal("legacy truncated body remained exact", v)
	}
}
func TestRawCoverageLegacyMigrationUpdatesPreviouslyEmptyAndCompleteMapping(t *testing.T) {
	a, raws, views := segmentFixtureV3(t)
	if v := rawCoverageTestSnapshot(t, a, nil, nil); !v.Complete || *v.Total != 0 {
		t.Fatal(v)
	}
	for h, view := range views {
		if _, e := storeIndexedRawBody(a.dataDir, int64(h), view.Hash, raws[h][:80], raws[h]); e != nil {
			t.Fatal(e)
		}
		v := rawCoverageTestSnapshot(t, a, nil, nil)
		if !v.Complete || *v.Total != int64(h+1) {
			t.Fatal("migration publication left a stale coverage summary", v)
		}
	}
}
func TestRawCoverageSelectedHeaderReadFailureIsNotAbsence(t *testing.T) {
	a := independentTestApp(t)
	f, e := os.Open(a.headersPath)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	reader := &timelineLocalReader{file: f, count: 2, hashes: map[int64]string{}}
	if hash, e := rawCoverageSelectedHash(reader, 1); e == nil || hash != "" {
		t.Fatal("unreadable selected header was treated as absent", hash, e)
	}
	if _, ok := reader.hashes[1]; ok {
		t.Fatal("failed header read was cached")
	}
}
func TestRawCoverageDoesNotUseInterruptedHeaderAppend(t *testing.T) {
	a := independentTestApp(t)
	rows := rawCoverageMetadataFixture(t, a, []heightInterval{{0, 1}})
	if v := waitRawCoverage(t, a); *v.Total != 2 {
		t.Fatal(v)
	}
	marker := headerAppendMarkerPath051(a.headersPath)
	if e := os.WriteFile(marker, []byte("pending recovery"), 0600); e != nil {
		t.Fatal(e)
	}
	if hash, tip, _ := rawCoverageHeader(a.headersPath, 0); hash != "" || tip != -1 {
		t.Fatal("pending append authorized Raw mapping", hash, tip)
	}
	rawSegmentMu.Lock()
	rawCoveragePublished(a.dataDir, 0, rows[0].Hash, 0)
	rawSegmentMu.Unlock()
	m := a.rawCoverageState()
	m.mu.Lock()
	exact := m.validated && m.d.Complete
	m.mu.Unlock()
	if exact {
		t.Fatal("pending append left coverage exact")
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/index/raw-coverage", nil)
	r.RemoteAddr = "127.0.0.1:12000"
	a.handleRawCoverage(w, r)
	if w.Code != 503 {
		t.Fatal("direct API accepted interrupted headers", w.Code, w.Body.String())
	}
	if _, e := os.Stat(marker); e != nil {
		t.Fatal("GET repaired append marker", e)
	}
	if e := os.Remove(marker); e != nil {
		t.Fatal(e)
	}
	if v := waitRawCoverage(t, a); *v.Total != 2 {
		t.Fatal(v)
	}
}
func TestRawCoverageReorgBackFindsPriorHashCatalogBody(t *testing.T) {
	a := independentTestApp(t)
	rows := rawCoverageMetadataFixture(t, a, []heightInterval{{0, 1}})
	if v := waitRawCoverage(t, a); *v.Total != 2 {
		t.Fatal(v)
	}
	// Publish both branches at height1. Its height projection now names B,
	// but the append-only hash catalog still owns A.
	hf, e := os.Open(a.headersPath)
	if e != nil {
		t.Fatal(e)
	}
	header := make([]byte, 80)
	_, e = hf.ReadAt(header, 80)
	hf.Close()
	if e != nil {
		t.Fatal(e)
	}
	header[12] ^= 1
	bh := hash256(header)
	bHash := reverseHex(bh[:])
	f, e := os.OpenFile(rawBodyCatalogHeightPath(a.dataDir, 1), os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	aRow := make([]byte, rawSegmentMapBytes)
	_, e = f.ReadAt(aRow, rawSegmentMapBytes)
	if e != nil {
		f.Close()
		t.Fatal(e)
	}
	if e = os.WriteFile(rawBodyCatalogHashPath(a.dataDir, rows[1].Hash), aRow, 0600); e != nil {
		f.Close()
		t.Fatal(e)
	}
	var record indexedRawBody
	record.Segment = &rows[1].Location
	decoded, ok := decodeRawBodyCatalog(aRow)
	if !ok {
		f.Close()
		t.Fatal("invalid fixture row")
	}
	record.SHA256 = decoded.SHA256
	record.Bytes = int(rows[1].Bytes)
	bRow, e := encodeRawBodyCatalog(1, bHash, &record, 1)
	if e != nil {
		f.Close()
		t.Fatal(e)
	}
	_, e = f.WriteAt(bRow, rawSegmentMapBytes)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	rawCoverageInvalidated(a.dataDir)
	v := waitRawCoverage(t, a)
	if *v.Total != 2 || *v.To != 1 {
		t.Fatal("selected retained A disappeared behind B's height projection", v)
	}
}
func TestRawCoveragePersistsPartialCursorAndBoundsLegacyResume(t *testing.T) {
	a := independentTestApp(t)
	rows := rawCoverageMetadataFixture(t, a, []heightInterval{{0, 599}})
	manifest, e := rawCoverageManifest(context.Background(), a.dataDir)
	if e != nil {
		t.Fatal(e)
	}
	_, tip, hash := rawCoverageHeader(a.headersPath, 0)
	m := a.rawCoverageState()
	rawSegmentMu.Lock()
	rawCoveragePublished(a.dataDir, 0, rows[0].Hash, 0)
	rawSegmentMu.Unlock()
	m.mu.Lock()
	m.d = rawCoverageDocument{Schema: 1, Revision: 1, Ranges: []heightInterval{{0, 511}}, Cursor: 512, AnchorHeight: tip, AnchorHash: hash, Manifest: manifest}
	if e = m.saveLocked(); e != nil {
		t.Fatal(e)
	}
	m.mu.Unlock()
	var saved rawCoverageDocument
	if e = utxoReadJSON(context.Background(), rawCoveragePath(a.dataDir), 32<<20, &saved); e != nil || saved.Cursor != 512 || saved.Complete || rawCoverageCount(saved.Ranges) != 512 || saved.Checksum != rawCoverageDigest(saved) {
		t.Fatal("partial cursor was not durably saved before restart", saved, e)
	}
	key, _ := filepath.Abs(a.dataDir)
	rawCoverageStates.Delete(key)
	v := waitRawCoverage(t, a)
	if *v.Total != 600 {
		t.Fatal("partial recovery lost prior mapped prefix", v)
	}
	legacy := filepath.Join(a.dataDir, "indexes", "blocks", "bodies-v2", "00")
	if e = os.MkdirAll(legacy, 0700); e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 700; n++ {
		if e = os.WriteFile(filepath.Join(legacy, strconv.Itoa(n)), nil, 0600); e != nil {
			t.Fatal(e)
		}
	}
	state := &rawCoverageState{root: a.dataDir, headers: a.headersPath, d: rawCoverageDocument{LegacyOffset: 256, AnchorHeight: tip}}
	defer func() {
		if state.legacyDir != nil {
			state.legacyDir.Close()
		}
	}()
	if e = state.scanLegacyRawLocked(context.Background()); e != nil {
		t.Fatal(e)
	}
	if state.legacyRead != 256 || state.d.LegacyOffset != 256 {
		t.Fatal("legacy resume scanned an unbounded old prefix", state.legacyRead, state.d.LegacyOffset)
	}
	if e = state.scanLegacyRawLocked(context.Background()); e != nil {
		t.Fatal(e)
	}
	if state.legacyRead != 512 || state.d.LegacyOffset != 512 {
		t.Fatal("legacy cursor reopened/rescanned a completed prefix", state.legacyRead, state.d.LegacyOffset)
	}
}
func TestRawCoverageEmptyStatusIsReadOnlyAndPrivate(t *testing.T) {
	a := independentTestApp(t)
	v := rawCoverageTestSnapshot(t, a, nil, nil)
	if !v.Complete || v.Total == nil || *v.Total != 0 {
		t.Fatal(v)
	}
	if _, e := os.Stat(filepath.Join(a.dataDir, "indexes", "blocks")); !os.IsNotExist(e) {
		t.Fatal("empty status created a Raw store", e)
	}
	r := httptest.NewRequest("GET", "http://evil.example/api/v1/index/raw-coverage", nil)
	r.RemoteAddr = "192.0.2.1:1000"
	w := httptest.NewRecorder()
	a.handleRawCoverage(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestRawCoverageWholeDeletionStatusRestartAndPublicationDoNotResurrectStore(t *testing.T) {
	a, _, views := segmentFixtureV3(t)
	foreign := deleteFixtureFile(t, a.dataDir, "indexes/blocks/bodies-v2/ff/unproven.block", "foreign source")
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "blocks", Armed: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := a.ensureRawProducerContext(context.Background(), views[0]); err != nil {
		t.Fatal(err)
	}
	if v := waitRawCoverage(t, a); *v.Total != 1 {
		t.Fatal(v)
	}
	waitRawCoverageStopped(a.dataDir)
	if _, err := os.Stat(rawCoveragePath(a.dataDir)); err != nil {
		t.Fatal("owned coverage was not saved", err)
	}
	q := indexDeleteRequest{Indices: []string{"blocks"}}
	deleteConfirmBatch(t, a, q)
	m := a.rawCoverageState()
	// A recovery page already queued before deletion must respect the same
	// suppression gate, even when its worker later reaches saveLocked().
	m.mu.Lock()
	err := m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	assertCleared := func() {
		t.Helper()
		if v := waitRawCoverage(t, a); *v.Total != 0 {
			t.Fatal("deleted body remained mapped", v)
		}
		waitRawCoverageStopped(a.dataDir)
		if _, err := os.Stat(rawCoveragePath(a.dataDir)); !os.IsNotExist(err) {
			t.Fatal("inspection recreated deleted summary", err)
		}
		r, err := a.reviewIndexDeletionBatch(context.Background(), q)
		if err != nil || !r.Ready || !r.Items[0].Noop || r.Items[0].Raw.RetainedFiles != 1 {
			t.Fatal("inspection resurrected removable store", r, err)
		}
		if b, err := os.ReadFile(foreign); err != nil || string(b) != "foreign source" {
			t.Fatal("foreign source changed", err)
		}
	}
	assertCleared()
	key, _ := filepath.Abs(a.dataDir)
	rawCoverageStates.Delete(key)
	assertCleared()
	if _, err = a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "blocks", Armed: true}}}); err != nil {
		t.Fatal(err)
	}
	if err = a.ensureRawProducerContext(context.Background(), views[1]); err != nil {
		t.Fatal(err)
	}
	if v := waitRawCoverage(t, a); *v.Total != 1 || *v.To != 1 {
		t.Fatal("real publication failed to restore coverage", v)
	}
	if _, err = os.Stat(rawCoveragePath(a.dataDir)); err != nil {
		t.Fatal("real publication did not restore durable summary", err)
	}
}
func TestRawProducerPreparedRetryDoesNotRefetchOrPublishDerivedBeforeReceipt(t *testing.T) {
	a := independentTestApp(t)
	target, body := acquisitionFixture(t)
	var calls atomic.Int32
	a.blockBaseAcquire = func(context.Context, blockTarget, appSettings) (blockView, error) { calls.Add(1); return body, nil }
	if e := os.MkdirAll(filepath.Dir(rawSegmentDir(a.dataDir)), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(rawSegmentDir(a.dataDir), []byte("storage unavailable"), 0600); e != nil {
		t.Fatal(e)
	}
	zero := int64(0)
	req := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: "blocks", Armed: true}, {Index: "utxo-state", Armed: true}}}
	if _, e := a.startIndexRun(context.Background(), req, ""); e != nil {
		t.Fatal(e)
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool { return d.Run != nil && d.Run.State == "waiting" })
	u, e := utxoStoreHead(a.dataDir)
	if e != nil || u.Head() != nil {
		t.Fatal("derived publication preceded durable Raw", e)
	}
	if e = os.Remove(rawSegmentDir(a.dataDir)); e != nil {
		t.Fatal(e)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" || calls.Load() != 1 || done.Run.Counts.SourceReads != 1 {
		t.Fatal("verified retry fetched again", done.Run, calls.Load())
	}
	if receipt, e := rawBodyCatalogReceipt(context.Background(), a.dataDir, target); e != nil || receipt == nil {
		t.Fatal(receipt, e)
	}
}
func TestRawProducerOnFlipPromotesVerifiedPreparedBodyAndCancellation(t *testing.T) {
	a := independentTestApp(t)
	target, body := acquisitionFixture(t)
	if e := a.ensureRawProducerContext(context.Background(), body); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(rawSegmentDir(a.dataDir)); !os.IsNotExist(e) {
		t.Fatal("RawOff retained temporary body", e)
	}
	if _, e := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "blocks", Armed: true}}}); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := a.ensureRawProducerContext(ctx, body); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := os.Stat(rawSegmentDir(a.dataDir)); !os.IsNotExist(e) {
		t.Fatal("cancelled producer wrote Raw", e)
	}
	if e := a.ensureRawProducerContext(context.Background(), body); e != nil {
		t.Fatal(e)
	}
	if receipt, e := rawBodyCatalogReceipt(context.Background(), a.dataDir, target); e != nil || receipt == nil {
		t.Fatal(receipt, e)
	}
	if _, e := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "blocks", Armed: false}}}); e != nil {
		t.Fatal(e)
	}
	if e := a.ensureRawProducerContext(context.Background(), body); e != nil {
		t.Fatal(e)
	}
	if receipt, e := rawBodyCatalogReceipt(context.Background(), a.dataDir, target); e != nil || receipt == nil {
		t.Fatal("RawOff removed prior receipt", receipt, e)
	}
}
func TestRawProducerOnDuringApplyWaitsBeforeDerivedHead(t *testing.T) {
	a := independentTestApp(t)
	target, body := acquisitionFixture(t)
	p := &indexRunPrepared{block: body}
	if e := a.ensurePreparedRawProducer(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	u, e := openUTXOStoreContext(context.Background(), a.dataDir)
	if e != nil {
		t.Fatal(e)
	}
	u.commitHook = func(stage string) error {
		if stage == "before_head" {
			_, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "blocks", Armed: true}, {Index: "utxo-state", Armed: true}}})
			return err
		}
		return nil
	}
	ctx := context.WithValue(context.Background(), indexRunPublicationKey{}, func() error { return a.checkPreparedRawProducer(context.Background(), p) })
	err := u.ApplyBlock(ctx, body)
	var waiting blockUnavailableError
	if !errors.As(err, &waiting) || u.Head() != nil {
		t.Fatal("RawOn flip allowed derived head before receipt", err, u.Head())
	}
	if e = a.ensurePreparedRawProducer(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	if receipt, e := rawBodyCatalogReceipt(context.Background(), a.dataDir, target); e != nil || receipt == nil {
		t.Fatal(receipt, e)
	}
	u.commitHook = nil
	// The completed-source hook has already offered this retained body to the
	// armed UTXO owner. The native adapter must adopt that durable head rather
	// than applying the same transition through this earlier store snapshot.
	consumer := &indexRunConsumer{selection: indexRunSelection{Index: "utxo-state", Armed: true}, utxo: u}
	if e = a.applyRunConsumer(ctx, consumer, p, a.currentChainAuthority(), "ephemeral"); e != nil {
		t.Fatal("prepared retry did not adopt shared committed state after Raw receipt", e)
	}
	if h := consumer.utxo.Head(); h == nil || h.Height != body.Height || h.BlockHash != body.Hash {
		t.Fatal("prepared retry did not retain the exact UTXO transition", h)
	}
}
