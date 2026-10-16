package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func deleteFixtureFile(t *testing.T, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestIndexDeleteReviewedOwnedStoreAndSharedDataPreserved(t *testing.T) {
	root := t.TempDir()
	a := &app{dataDir: root}
	deleteFixtureFile(t, root, "indexes/tx-locator/head.json", "{}")
	deleteFixtureFile(t, root, "indexes/browse/tx-locator/head.json", "{}")
	for _, p := range []string{"indexes/output-facts-v1/keep.json", "indexes/sat-state-archives/keep.json", "headers/keep.bin", "blocks/raw/keep.block", "settings.json"} {
		deleteFixtureFile(t, root, p, "preserved")
	}
	r, e := a.reviewIndexDeletion(context.Background(), "tx-locator")
	if e != nil || !r.Ready || !r.Exists || r.ReviewID == "" {
		t.Fatalf("review: %+v %v", r, e)
	}
	if _, e = os.Stat(filepath.Join(root, "indexes/run.json")); !os.IsNotExist(e) {
		t.Fatal("review wrote run state")
	}
	v, e := a.deleteIndexData(context.Background(), "tx-locator", r.ReviewID)
	if e != nil || v["deleted"] != true || v["cleanup_pending"] == true {
		t.Fatalf("delete: %+v %v", v, e)
	}
	for _, p := range []string{"indexes/tx-locator", "indexes/browse/tx-locator"} {
		if _, e = os.Stat(filepath.Join(root, p)); !os.IsNotExist(e) {
			t.Fatalf("active store remains: %s %v", p, e)
		}
	}
	for _, p := range []string{"indexes/output-facts-v1/keep.json", "indexes/sat-state-archives/keep.json", "headers/keep.bin", "blocks/raw/keep.block", "settings.json"} {
		b, e := os.ReadFile(filepath.Join(root, p))
		if e != nil || string(b) != "preserved" {
			t.Fatalf("other owner changed: %s %v", p, e)
		}
	}
	policies, e := readIndexLiveState(root)
	if e != nil || !policies.Policies["tx-locator"].Stopped || policies.Policies["tx-locator"].Enabled {
		t.Fatalf("not stopped: %+v %v", policies, e)
	}
	if indexRunOptionalWorkAllowed(root, "tx-locator") {
		t.Fatal("deleted legacy consumer can publish implicitly")
	}
	if !indexRunOptionalWorkAllowed(root, "inscriptions") {
		t.Fatal("deletion changed an unrelated legacy consumer")
	}
}
func TestIndexDeleteStaleReviewAndActiveWriterRefused(t *testing.T) {
	root := t.TempDir()
	a := &app{dataDir: root}
	path := deleteFixtureFile(t, root, "indexes/txo-spender/head.json", "{}")
	r, e := a.reviewIndexDeletion(context.Background(), "txo-spender")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("{\"changed\":true}"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = a.deleteIndexData(context.Background(), r.Index, r.ReviewID); e == nil || !strings.Contains(e.Error(), "review deletion again") {
		t.Fatalf("stale review accepted: %v", e)
	}
	a.indexCancel = func() {}
	r, e = a.reviewIndexDeletion(context.Background(), "txo-spender")
	if e != nil || r.Ready || len(r.Blockers) == 0 {
		t.Fatalf("active writer review: %+v %v", r, e)
	}
	if _, e = a.deleteIndexData(context.Background(), r.Index, r.ReviewID); e == nil {
		t.Fatal("active writer deletion accepted")
	}
	if _, e = os.Stat(path); e != nil {
		t.Fatal("refused deletion touched data")
	}
}

func TestIndexDeleteBitmapConfigurationAndEvidenceLockSurviveClearing(t *testing.T) {
	ctx, root := context.Background(), t.TempDir()
	a := &app{dataDir: root}
	i := bitmapTestCreateProfile(t, root, "Editable District", 1, false)
	before, err := a.reviewIndexDeletion(ctx, i.ID)
	if err != nil || !before.Ready {
		t.Fatal(before, err)
	}
	rules := bitmapReferenceAnswers("district")
	rules["text"] = 0
	updated, err := a.updateBitmapProfile(ctx, bitmapProfileRequest{Action: "update", ID: i.ID, Kind: "district", Label: i.Label, Rules: rules, ExpectedProfile: i.ProfileHash, ExpectedConfigurationRevision: bitmapEffectiveConfigurationRevision(i)})
	if err != nil {
		t.Fatal(err)
	}
	i = updated["instance"].(bitmapProfileInstance)
	if _, err = a.deleteIndexData(ctx, i.ID, before.ReviewID); err == nil || !strings.Contains(err.Error(), "review deletion again") {
		t.Fatal("stale rules confirmation accepted", err)
	}
	dir := bitmapDevelopmentDir(root, i.ID)
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "head.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	review, err := a.reviewIndexDeletion(ctx, i.ID)
	if err != nil || !review.Ready || !review.Exists {
		t.Fatal(review, err)
	}
	if _, err = a.deleteIndexData(ctx, i.ID, review.ReviewID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("saved data remains", err)
	}
	locked, err := bitmapResolvedInstance(ctx, root, i.ID)
	if err != nil || !locked.RulesLocked || locked.ProfileHash != i.ProfileHash {
		t.Fatal("deletion erased the indexed interpretation lock", locked, err)
	}
	_, err = a.updateBitmapProfile(ctx, bitmapProfileRequest{Action: "update", ID: i.ID, Kind: "district", Label: i.Label, Rules: bitmapReferenceAnswers("district"), ExpectedProfile: i.ProfileHash, ExpectedConfigurationRevision: bitmapEffectiveConfigurationRevision(i)})
	if err == nil {
		t.Fatal("clearing indexed data silently unlocked its rules")
	}
}
func TestIndexDeleteInscriptionsClearsOwnedLocatorButPreservesTransactionIndex(t *testing.T) {
	root := t.TempDir()
	a := &app{dataDir: root}
	deleteFixtureFile(t, root, "indexes/inscriptions/head.json", "{}")
	deleteFixtureFile(t, root, "indexes/"+inscriptionLocatorIndex+"/head.json", "{}")
	keep := deleteFixtureFile(t, root, "indexes/tx-locator/head.json", "independent")
	r, err := a.reviewIndexDeletion(context.Background(), "inscriptions")
	if err != nil || !r.Ready || !strings.Contains(strings.Join(r.Scope, " "), "Related inscription transaction locators") {
		t.Fatalf("incomplete deletion scope: %+v %v", r, err)
	}
	if _, err = a.deleteIndexData(context.Background(), r.Index, r.ReviewID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "indexes", inscriptionLocatorIndex)); !os.IsNotExist(err) {
		t.Fatal("dependent locator store remains")
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != "independent" {
		t.Fatal("independent Transaction Index changed")
	}
}
func TestIndexDeleteDependenciesAndCoreHeadersRefused(t *testing.T) {
	root := t.TempDir()
	a := &app{dataDir: root}
	deleteFixtureFile(t, root, "indexes/utxo-state/head.json", "{}")
	deleteFixtureFile(t, root, "indexes/address-state/head.json", "{}")
	r, e := a.reviewIndexDeletion(context.Background(), "utxo-state")
	if e != nil || r.Ready || len(r.Dependencies) != 1 || r.Dependencies[0].Index != "address-state" {
		t.Fatalf("projection dependency: %+v %v", r, e)
	}
	deleteFixtureFile(t, root, "indexes/bitmap-development-v1/bitmap-chain/head.json", "{}")
	r, e = a.reviewIndexDeletion(context.Background(), "bitmap-districts")
	if e != nil || r.Ready || len(r.Dependencies) != 1 {
		t.Fatalf("chain dependency: %+v %v", r, e)
	}
	if e = os.Remove(filepath.Join(root, "indexes", "bitmap-development-v1", "bitmap-chain", "head.json")); e != nil {
		t.Fatal(e)
	}
	deleteFixtureFile(t, root, "indexes/bitmap-development-v1/bitmap-chain/retention.json", "{}")
	r, e = a.reviewIndexDeletion(context.Background(), "bitmap-districts")
	if e != nil || r.Ready || len(r.Dependencies) != 1 {
		t.Fatalf("manifest-only chain dependency: %+v %v", r, e)
	}
	r, e = a.reviewIndexDeletion(context.Background(), "headers")
	if e != nil || r.Ready || len(r.Blockers) == 0 {
		t.Fatalf("headers: %+v %v", r, e)
	}
}
func TestIndexDeleteBusyPublicationAndPathEscapeRefused(t *testing.T) {
	root := t.TempDir()
	a := &app{dataDir: root}
	path := deleteFixtureFile(t, root, "indexes/tx-locator/head.json", "{}")
	r, e := a.reviewIndexDeletion(context.Background(), "tx-locator")
	if e != nil {
		t.Fatal(e)
	}
	indexBrowseMu.Lock()
	_, e = a.deleteIndexData(context.Background(), r.Index, r.ReviewID)
	indexBrowseMu.Unlock()
	if e == nil || !strings.Contains(e.Error(), "busy") {
		t.Fatalf("busy publication accepted: %v", e)
	}
	if _, e = os.Stat(path); e != nil {
		t.Fatal("busy deletion touched data")
	}
	blocksBodyMigrationMu.Lock()
	_, e = a.deleteIndexData(context.Background(), r.Index, r.ReviewID)
	blocksBodyMigrationMu.Unlock()
	if e == nil || !strings.Contains(e.Error(), "busy") {
		t.Fatalf("body migration guard missed: %v", e)
	}
	if e = indexDeleteRemoveTree(root, root); e == nil {
		t.Fatal("profile-root removal accepted")
	}
	if e = indexDeleteRemoveTree(root, filepath.Join(root, "indexes", "tx-locator")); e == nil {
		t.Fatal("unstaged removal accepted")
	}
	if e = indexDeleteOwnedPath(root, filepath.Dir(root)); e == nil {
		t.Fatal("outside target accepted")
	}
	r, e = a.reviewIndexDeletion(context.Background(), "../../outside")
	if e != nil || r.Ready {
		t.Fatalf("unknown identity accepted: %+v %v", r, e)
	}
}
func TestIndexDeleteLinkedTreeCannotRemoveExternalOwner(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	keep := deleteFixtureFile(t, outside, "keep", "outside")
	stage := filepath.Join(root, "indexes", "deleted-staging", "fixture")
	if e := os.MkdirAll(stage, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(stage, "escape")); e != nil {
		t.Skipf("symlink creation unavailable: %v", e)
	}
	if e := indexDeleteRemoveTree(root, stage); e == nil {
		t.Fatal("linked staged tree accepted")
	}
	b, e := os.ReadFile(keep)
	if e != nil || string(b) != "outside" {
		t.Fatal("external owner changed")
	}
}
func TestIndexDeleteHTTPRequiresLocalReviewAndConfirmation(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	for _, tc := range []struct {
		method, url, origin, body string
		status                    int
	}{
		{"GET", "http://untrusted.invalid/api/v1/index/delete?index=tx-locator", "", "", 403},
		{"POST", "http://127.0.0.1:9090/api/v1/index/delete", "https://untrusted.invalid", `{"index":"tx-locator"}`, 403},
		{"POST", "http://127.0.0.1:9090/api/v1/index/delete", "", `{"index":"tx-locator"}`, 409},
		{"DELETE", "http://127.0.0.1:9090/api/v1/index/delete", "", "", 405},
	} {
		req := httptest.NewRequest(tc.method, tc.url, strings.NewReader(tc.body))
		req.RemoteAddr = "127.0.0.1:40000"
		req.Header.Set("Content-Type", "application/json")
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		a.handleIndexDelete(w, req)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.origin, w.Code, w.Body.String())
		}
	}
}
