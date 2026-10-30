package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
)

func recipeRangePublishFixture(t *testing.T, lineage *recipeLineagePlan, stores []*recipeStore, height int64, hash string) {
	t.Helper()
	refs := map[string]string{}
	for _, store := range stores {
		var definition recipeDefinition
		for _, node := range lineage.nodes {
			if node.store.instance == store.instance {
				definition = node.definition
				break
			}
		}
		unit := recipeStoreUnit{Schema: 1, Fingerprint: store.fingerprint, Height: height, BlockHash: hash, Rows: []recipeStoreRow{}}
		for _, dep := range definition.Dependencies {
			unit.Lineage = append(unit.Lineage, recipeDependencyReceipt{Name: dep.Name, Instance: dep.Instance, SemanticHash: dep.SemanticHash, Height: height, BlockHash: hash, Unit: refs[dep.Instance]})
		}
		if err := store.publish(context.Background(), unit, nil, recipeTestGuard, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(unit)
		refs[store.instance] = recipeStoreHash(data)
	}
}

func TestRecipeRangeOutputSurvivesLiveAppendAndRejectsPriorPageChanges(t *testing.T) {
	ctx := context.Background()
	a, plan, _, lineage, first, middle, child, _ := recipeDAGInspectFixture(t)
	stores := []*recipeStore{first, middle, child}
	header, _ := a.readSelectedHeader(1)
	digest := hash256(header)
	hash := reverseHex(digest[:])
	recipeRangePublishFixture(t, lineage, stores, 1, hash)
	page, err := a.readRecipeRangeCoverage(ctx, plan.Index, plan.Revision, plan.SemanticHash, 0, 1, nil, 1, "")
	if err != nil || len(page.Anchors) != 1 || page.Anchors[0].Height != 0 || page.Next == nil || page.Snapshot == "" {
		t.Fatal("first historical range page", page, err)
	}
	// Synthetic local append beyond the requested interval, not a claim of
	// externally valid Bitcoin chain/body data at height two.
	header2 := append([]byte(nil), header...)
	previous, _ := hex.DecodeString(hash)
	for i := 0; i < 32; i++ {
		header2[4+i] = previous[31-i]
	}
	header2[79] ^= 1
	digest = hash256(header2)
	hash2 := reverseHex(digest[:])
	a.headerChainMu.Lock()
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err == nil {
		_, err = f.WriteAt(header2, 160)
		_ = f.Close()
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	recipeRangePublishFixture(t, lineage, stores, 2, hash2)
	continued, err := a.readRecipeRangeCoverage(ctx, plan.Index, plan.Revision, plan.SemanticHash, 0, 1, page.Next, 1, page.Snapshot)
	if err != nil || len(continued.Anchors) != 1 || continued.Anchors[0].Height != 1 || continued.Next != nil || continued.Snapshot != page.Snapshot || continued.Revision != page.Revision {
		t.Fatal("unrelated Live append invalidated historical continuation", continued, err)
	}
	// Replacing a prior-page header must be caught even though the range's
	// last header and the newly appended tip remain byte-for-byte unchanged.
	genesis, _ := a.readSelectedHeader(0)
	changed := append([]byte(nil), genesis...)
	changed[79] ^= 1
	a.headerChainMu.Lock()
	f, err = os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err == nil {
		_, err = f.WriteAt(changed, 0)
		_ = f.Close()
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if result, err := a.readRecipeRangeCoverage(ctx, plan.Index, plan.Revision, plan.SemanticHash, 0, 1, page.Next, 1, page.Snapshot); err == nil || len(result.Anchors) != 0 {
		t.Fatal("changed prior-page selected header accepted", result, err)
	}
	a.headerChainMu.Lock()
	f, err = os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err == nil {
		_, err = f.WriteAt(genesis, 0)
		_ = f.Close()
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.readRecipeRangeCoverage(ctx, plan.Index, plan.Revision, plan.SemanticHash, 0, 1, page.Next, 1, page.Snapshot); err != nil {
		t.Fatal("restored exact historical prefix cannot be continued", err)
	}
	if err = first.Reconcile(ctx, map[int64]string{0: genesisHashDisplay}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if result, err := a.readRecipeRangeCoverage(ctx, plan.Index, plan.Revision, plan.SemanticHash, 0, 1, page.Next, 1, page.Snapshot); err == nil || len(result.Anchors) != 0 {
		t.Fatal("changed prior-page transitive ancestor accepted", result, err)
	}
}

func TestRecipeRangeOutputHTTPAndIndependentEmptyCoverage(t *testing.T) {
	a := runTestApp(t)
	view := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(registryRecipe, "")))
	store, err := openRecipeStore(a.dataDir, view.Recipe.ID, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Publish(context.Background(), recipeStoreUnit{Schema: 1, Fingerprint: view.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Rows: []recipeStoreRow{}}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/v1/index/recipes/coverage?id=%s&revision=%s&semantic_hash=%s&from=0&to=1&limit=1", view.Recipe.ID, view.Revision, view.SemanticHash)
	h := indexInterfaceHandler(a)
	response := indexInterfaceCall(h, "GET", base, "")
	var page recipeRangeCoveragePage
	if err = json.Unmarshal(response.Body.Bytes(), &page); err != nil || response.Code != http.StatusOK || len(page.Anchors) != 1 || page.Next != nil || page.Snapshot == "" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("independent evaluated-empty coverage is not explicit", response.Code, response.Body.String(), err)
	}
	for _, path := range []string{base + "&after=0", base + "&unknown=1", base + "&from=0"} {
		if response := indexInterfaceCall(h, "GET", path, ""); response.Code != 400 {
			t.Fatal("invalid range query accepted", path, response.Code)
		}
	}
	if response := indexInterfaceCall(h, "POST", base, "{}"); response.Code != 405 {
		t.Fatal("range coverage mutation method accepted", response.Code)
	}
	if response := indexInterfaceCall(h, "GET", base+"&after=0&snapshot="+url.QueryEscape(page.Snapshot), ""); response.Code != 200 {
		t.Fatal("explicit exhausted continuation failed", response.Code, response.Body.String())
	}
	if _, err := a.readRecipeRangeCoverage(context.Background(), view.Recipe.ID, view.Revision, view.SemanticHash, 0, 9, nil, 1, ""); err == nil {
		t.Fatal("unavailable selected-header interval was called covered")
	}
}
