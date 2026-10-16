package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func deleteHeight(v int64) *int64 { return &v }
func deleteConfirmBatch(t *testing.T, a *app, q indexDeleteRequest) map[string]any {
	t.Helper()
	r, e := a.reviewIndexDeletionBatch(context.Background(), q)
	if e != nil || !r.Ready {
		t.Fatal(r, e)
	}
	q.ReviewID = r.ReviewID
	v, e := a.deleteIndexDataBatch(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	return v
}

func TestIndexDeleteBatchCustomDefinitionsRequireExplicitDependencyClosure(t *testing.T) {
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	v, e := bitmapSaveProfile(ctx, a.dataDir, bitmapProfileRequest{Action: "create", Kind: "district", Label: "Remove this interpretation", Rules: bitmapReferenceAnswers("district"), LinkedParcel: true, ParcelLabel: "Remove linked parcel"})
	if e != nil {
		t.Fatal(e)
	}
	d := v["instance"].(bitmapProfileInstance)
	c := v["chain"].(bitmapProfileInstance)
	p := v["linked_parcel"].(bitmapProfileInstance)
	other := bitmapTestCreateProfile(t, a.dataDir, "Independent interpretation", 1, false)
	marker := deleteFixtureFile(t, a.dataDir, filepath.ToSlash(filepath.Join("indexes", "bitmap-development-v1", d.ID, "disposable.txt")), "owned")
	keep := deleteFixtureFile(t, a.dataDir, "indexes/output-facts-v1/keep.json", "independent fact")
	r, e := a.reviewIndexDeletionBatch(ctx, indexDeleteRequest{Indices: []string{d.ID}})
	if e != nil || r.Ready || len(r.Dependencies) != 2 {
		t.Fatal(r, e)
	}
	if r.Items[0].Operation != "remove_track" {
		t.Fatal(r)
	}
	if _, e = a.deleteIndexDataBatch(ctx, indexDeleteRequest{Indices: []string{d.ID}, ReviewID: r.ReviewID}); e == nil {
		t.Fatal("implicit dependency cascade accepted")
	}
	if _, e = os.Stat(marker); e != nil {
		t.Fatal("refused review touched data", e)
	}
	q := indexDeleteRequest{Indices: []string{d.ID, c.ID, p.ID}}
	result := deleteConfirmBatch(t, a, q)
	if len(result["removed_tracks"].([]string)) != 3 {
		t.Fatal(result)
	}
	instances, e := bitmapProfileInstances(ctx, a.dataDir)
	if e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	for _, i := range instances {
		seen[i.ID] = true
	}
	if seen[d.ID] || seen[c.ID] || seen[p.ID] || !seen[other.ID] {
		t.Fatal("custom closure or independent definition changed", seen)
	}
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("owned custom data remains", e)
	}
	if raw, e := os.ReadFile(keep); e != nil || string(raw) != "independent fact" {
		t.Fatal(e)
	}
	if _, e = bitmapResolvedInstance(ctx, a.dataDir, d.ID); e == nil {
		t.Fatal("removed interpretation still resolves")
	}
}

func TestIndexDeleteBatchStaleOneMemberAndBlockedScopeAreAtomic(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	left := deleteFixtureFile(t, a.dataDir, "indexes/tx-locator/head.json", "{}")
	right := deleteFixtureFile(t, a.dataDir, "indexes/txo-spender/head.json", "{}")
	q := indexDeleteRequest{Indices: []string{"tx-locator", "txo-spender"}}
	r, e := a.reviewIndexDeletionBatch(context.Background(), q)
	if e != nil || !r.Ready {
		t.Fatal(r, e)
	}
	if e = os.WriteFile(right, []byte("{\"changed\":true}"), 0600); e != nil {
		t.Fatal(e)
	}
	q.ReviewID = r.ReviewID
	if _, e = a.deleteIndexDataBatch(context.Background(), q); e == nil || !strings.Contains(e.Error(), "review deletion again") {
		t.Fatal("stale batch accepted", e)
	}
	for _, path := range []string{left, right} {
		if _, e = os.Stat(path); e != nil {
			t.Fatal("partial batch removal", path, e)
		}
	}
	r, e = a.reviewIndexDeletionBatch(context.Background(), indexDeleteRequest{Indices: []string{"tx-locator", "headers"}})
	if e != nil || r.Ready {
		t.Fatal(r, e)
	}
	if _, e = os.Stat(left); e != nil {
		t.Fatal(e)
	}
}

func TestIndexDeleteAttachedChainClearPreservesValidDescriptorAndLock(t *testing.T) {
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	d := bitmapTestCreateProfile(t, a.dataDir, "Keep parent capability", 1, false)
	chain := d.ChainInstance
	deleteFixtureFile(t, a.dataDir, filepath.ToSlash(filepath.Join("indexes", "bitmap-development-v1", chain, "partial.json")), "evidence")
	r, e := a.reviewIndexDeletionBatch(ctx, indexDeleteRequest{Indices: []string{chain}})
	if e != nil || !r.Ready || r.Items[0].Operation != "clear_data" {
		t.Fatal(r, e)
	}
	result := deleteConfirmBatch(t, a, indexDeleteRequest{Indices: []string{chain}})
	if len(result["removed_tracks"].([]string)) != 0 {
		t.Fatal(result)
	}
	if _, e = bitmapResolvedInstance(ctx, a.dataDir, d.ID); e != nil {
		t.Fatal("parent descriptor became invalid", e)
	}
	i, e := bitmapResolvedInstance(ctx, a.dataDir, chain)
	if e != nil || !i.RulesLocked || i.DistrictInstance != d.ID {
		t.Fatal(i, e)
	}
}

func TestIndexDeleteNarrowRawRangeFiltersBeforeCandidateBudget(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	dir := filepath.Join(a.dataDir, "indexes", "sources")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	// Unrelated hashes are never admitted to the requested genesis window;
	// this tests the bounded decision without parsing thousands of fake bodies.
	for i := 0; i < 4100; i++ {
		name := fmt.Sprintf("%064x.block", i+1)
		if e := os.WriteFile(filepath.Join(dir, name), raw[:81], 0600); e != nil {
			t.Fatal(e)
		}
	}
	q := indexDeleteRequest{Indices: []string{"blocks"}, Range: &indexDeleteRange{From: deleteHeight(0), To: deleteHeight(0)}}
	r, e := a.reviewIndexDeletionBatch(context.Background(), q)
	if e != nil || !r.Ready || r.Items[0].Raw.RemovableFiles != 1 {
		t.Fatal(r, e)
	}
	if r.Items[0].Raw.RetainedFiles != 4100 {
		t.Fatal("outside candidates were not disclosed", r.Items[0].Raw)
	}
}

func TestIndexDeleteOneEndedRawRangeUsesSavedExtentBeforeHeaderBudget(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	// The genuine saved genesis source is small, while the selected header
	// file advertises a much longer prefix. Its unrelated tail is never
	// mistaken for saved body coverage or an implicit delete endpoint.
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(80 * 793930)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		requested *indexDeleteRange
		want      heightInterval
	}{
		{"Out only", &indexDeleteRange{To: deleteHeight(793929)}, heightInterval{From: 0, To: 793929}},
		{"In only", &indexDeleteRange{From: deleteHeight(0)}, heightInterval{From: 0, To: 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := a.reviewIndexDeletionBatch(context.Background(), indexDeleteRequest{Indices: []string{"blocks"}, Range: tc.requested})
			if err != nil || !r.Ready || r.Items[0].Raw.RemovableFiles != 1 || r.Items[0].Range == nil || *r.Items[0].Range != tc.want {
				t.Fatal("one-ended deletion used unrelated header extent", r, err)
			}
		})
	}
}

func TestIndexDeleteRawRangeRemovesActualOwnedSourcesOnly(t *testing.T) {
	a, zero := testAppWithGenesis(t)
	one, hash := installBlockOneHeader063(t, a)
	zeroSource := deleteFixtureFile(t, a.dataDir, "indexes/sources/"+genesisHashDisplay+".block", string(zero))
	oneSource := deleteFixtureFile(t, a.dataDir, "indexes/sources/"+hash+".block", string(one))
	keep := deleteFixtureFile(t, a.dataDir, "indexes/output-facts-v1/keep.json", "shared fact")
	outside := deleteFixtureFile(t, a.dataDir, "unrelated/keep.block", "unrelated")
	q := indexDeleteRequest{Indices: []string{"blocks"}, Range: &indexDeleteRange{From: deleteHeight(0), To: deleteHeight(0)}}
	r, e := a.reviewIndexDeletionBatch(context.Background(), q)
	if e != nil || !r.Ready || !r.Exists {
		t.Fatal(r, e)
	}
	if r.Items[0].Raw.RemovableFiles < 2 || r.Items[0].Operation != "prune_raw_bodies" {
		t.Fatal(r)
	}
	result := deleteConfirmBatch(t, a, q)
	if result["noop"] == true {
		t.Fatal(result)
	}
	if _, e = os.Stat(zeroSource); !os.IsNotExist(e) {
		t.Fatal("selected source retained", e)
	}
	if _, e = os.Stat(filepath.Join(a.dataDir, "blocks", "raw", "0-"+genesisHashDisplay+".block")); !os.IsNotExist(e) {
		t.Fatal("selected cache retained", e)
	}
	if _, e = os.Stat(oneSource); e != nil {
		t.Fatal("source beyond Out removed", e)
	}
	for _, p := range []string{keep, outside} {
		if _, e = os.Stat(p); e != nil {
			t.Fatal("other owner changed", p, e)
		}
	}
	a.cacheMu.RLock()
	_, hasZero := a.cacheIndex.Blocks[genesisHashDisplay]
	a.cacheMu.RUnlock()
	if hasZero {
		t.Fatal("raw possession metadata survived its removed cache name")
	}
	r, e = a.reviewIndexDeletionBatch(context.Background(), q)
	if e != nil || !r.Ready || !r.Items[0].Noop || r.Items[0].Raw.RemovableFiles != 0 {
		t.Fatal(r, e)
	}
	if _, e = os.Stat(oneSource); e != nil {
		t.Fatal(e)
	}
}

func TestIndexDeleteRawChangedPayloadInvalidatesConfirmation(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	source := deleteFixtureFile(t, a.dataDir, "indexes/sources/"+genesisHashDisplay+".block", string(raw))
	q := indexDeleteRequest{Indices: []string{"blocks"}}
	r, e := a.reviewIndexDeletionBatch(context.Background(), q)
	if e != nil || !r.Ready {
		t.Fatal(r, e)
	}
	if e = os.WriteFile(source, append(raw, 0), 0600); e != nil {
		t.Fatal(e)
	}
	q.ReviewID = r.ReviewID
	if _, e = a.deleteIndexDataBatch(context.Background(), q); e == nil || !strings.Contains(e.Error(), "review deletion again") {
		t.Fatal(e)
	}
	if _, e = os.Stat(source); e != nil {
		t.Fatal(e)
	}
}

func TestIndexDeleteWholeRawPreservesUnprovenOwnedBodyEntries(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	unknown := deleteFixtureFile(t, a.dataDir, "indexes/blocks/bodies-v2/ff/unproven.block", "unproven source")
	metadata := deleteFixtureFile(t, a.dataDir, "indexes/blocks/head.json", "{}")
	verified := deleteFixtureFile(t, a.dataDir, "indexes/sources/"+genesisHashDisplay+".block", string(raw))
	q := indexDeleteRequest{Indices: []string{"blocks"}}
	r, e := a.reviewIndexDeletionBatch(context.Background(), q)
	if e != nil || !r.Ready || r.Items[0].Raw.RetainedFiles != 1 {
		t.Fatal(r, e)
	}
	deleteConfirmBatch(t, a, q)
	if b, e := os.ReadFile(unknown); e != nil || string(b) != "unproven source" {
		t.Fatal("unproven body erased by whole-store detach", e)
	}
	for _, path := range []string{metadata, verified} {
		if _, e := os.Stat(path); !os.IsNotExist(e) {
			t.Fatal("reviewed owned data remained", path, e)
		}
	}
	r, e = a.reviewIndexDeletionBatch(context.Background(), q)
	if e != nil || !r.Ready || !r.Items[0].Noop || r.Items[0].Raw.RetainedFiles != 1 {
		t.Fatal("retained unproven entries imply a false removable store", r, e)
	}
}

func TestIndexDeleteHistoricalRangeSubtractsRowsTotalsAndCoverage(t *testing.T) {
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	s, e := openAddressHistoryStoreContext(ctx, a.dataDir)
	if e != nil {
		t.Fatal(e)
	}
	script, address := addressHistoryTestScript(9)
	hashes := map[int64]string{10: strings.Repeat("1", 64), 20: strings.Repeat("2", 64), 30: strings.Repeat("3", 64)}
	hashAt := addressHistoryTestHashAt(hashes)
	for i, h := range []int64{10, 20, 30} {
		if e = s.CommitBlockContext(ctx, addressHistoryTestBlock(h, hashes[h], outputView{ValueSats: uint64(i + 1), ScriptPubKey: script}), hashAt); e != nil {
			t.Fatal(e)
		}
	}
	old, e := s.QueryContext(ctx, address, "", 1, hashAt)
	if e != nil || old.NextCursor == "" {
		t.Fatal(old, e)
	}
	q := indexDeleteRequest{Indices: []string{"address-history"}, Range: &indexDeleteRange{From: deleteHeight(10), To: deleteHeight(20)}}
	r, e := a.reviewIndexDeletionBatch(ctx, q)
	if e != nil || !r.Ready || r.Items[0].Noop {
		t.Fatal(r, e)
	}
	deleteConfirmBatch(t, a, q)
	cold, e := openAddressHistoryStoreContext(ctx, a.dataDir)
	if e != nil {
		t.Fatal(e)
	}
	got, e := cold.QueryContext(ctx, address, "", 10, hashAt)
	if e != nil || got.ReceivedSats != 3 || got.ReceivedOutputCount != 1 || len(got.Outputs) != 1 || got.Outputs[0].Output != "0.0.30" {
		t.Fatal(got, e)
	}
	if len(got.Coverage) != 1 || got.Coverage[0] != (heightInterval{30, 30}) {
		t.Fatal("out-of-window history changed", got.Coverage)
	}
	if _, e = cold.QueryContext(ctx, address, old.NextCursor, 1, hashAt); e == nil {
		t.Fatal("cursor borrowed a new deleted-range snapshot")
	}
	if _, e = os.Stat(filepath.Join(a.dataDir, "indexes", "address-history", "head.json")); e != nil {
		t.Fatal("range deletion removed the track store", e)
	}
}

func TestIndexDeleteGenericSuffixAndMiddleRefusal(t *testing.T) {
	ctx := context.Background()
	a, zero := testAppWithGenesis(t)
	one, hash := installBlockOneHeader063(t, a)
	b0, e := parseBlockDetailed(0, genesisHashDisplay, zero[:80], zero, "fixture", false)
	if e != nil {
		t.Fatal(e)
	}
	b1, e := parseBlockDetailed(1, hash, one[:80], one, "fixture", false)
	if e != nil {
		t.Fatal(e)
	}
	s, e := openIndexStoreContext(ctx, a.dataDir, "tx-locator")
	if e != nil {
		t.Fatal(e)
	}
	s.head.Mode = "lean"
	for _, b := range []blockView{b0, b1} {
		if e = s.appendBlock(b, 0, "ephemeral"); e != nil {
			t.Fatal(e)
		}
	}
	middle := indexDeleteRequest{Indices: []string{"tx-locator"}, Range: &indexDeleteRange{From: deleteHeight(0), To: deleteHeight(0)}}
	r, e := a.reviewIndexDeletionBatch(ctx, middle)
	if e != nil || r.Ready {
		t.Fatal("prefix deletion accepted", r, e)
	}
	q := indexDeleteRequest{Indices: []string{"tx-locator"}, Range: &indexDeleteRange{From: deleteHeight(1), To: deleteHeight(1)}}
	deleteConfirmBatch(t, a, q)
	cold, e := indexStoreHead(a.dataDir, "tx-locator")
	if e != nil || cold.checkpoint == nil || cold.checkpoint.Height != 0 {
		t.Fatal(cold, e)
	}
	if _, e = os.Stat(filepath.Join(s.dir, "commits", s.head.Commitment+".json")); !os.IsNotExist(e) {
		t.Fatal("removed suffix payload remains", e)
	}
	if e = cold.walk(func(b indexBatch) error {
		if b.Checkpoint.Height != 0 {
			return fmt.Errorf("unexpected retained block")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, ready, e := cold.activeLocatorCommitContext(ctx, 1); e != nil || !ready {
		t.Fatal("rewind locator became corrupt", ready, e)
	}
}

func TestIndexDeletePartialBoundsAliasesAndHTTPBatch(t *testing.T) {
	for _, url := range []string{"http://127.0.0.1:9090/api/v1/index/delete?index=blocks&index=tx-locator&from=10", "http://127.0.0.1:9090/api/v1/index/delete?indices=blocks,tx-locator&to=20"} {
		q, e := indexDeleteHTTPReviewRequest(httptest.NewRequest("GET", url, nil))
		if e != nil || len(q.Indices) != 2 || q.Range == nil {
			t.Fatal(q, e)
		}
	}
	q, e := indexDeleteHTTPReviewRequest(httptest.NewRequest("GET", "http://127.0.0.1:9090/api/v1/index/delete?index=blocks&from=20&to=10", nil))
	if e == nil {
		t.Fatal(q)
	}
	a := &app{dataDir: t.TempDir()}
	deleteFixtureFile(t, a.dataDir, "indexes/txo-spender/head.json", "{}")
	r, e := a.reviewIndexDeletionBatch(context.Background(), indexDeleteRequest{Indices: []string{"txio-values"}, Range: &indexDeleteRange{From: deleteHeight(0), To: deleteHeight(1)}})
	if e != nil || r.Ready {
		t.Fatal("leaf silently mapped to parent deletion", r, e)
	}
	req := httptest.NewRequest("GET", "http://127.0.0.1:9090/api/v1/index/delete?index=tx-locator&index=address-history", nil)
	req.RemoteAddr = "127.0.0.1:30000"
	w := httptest.NewRecorder()
	a.handleIndexDelete(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response indexDeleteReview
	if e = json.Unmarshal(w.Body.Bytes(), &response); e != nil || len(response.Items) != 2 || response.ScopeKind != "whole" {
		t.Fatal(response, e)
	}
}
