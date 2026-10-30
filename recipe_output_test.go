package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRecipeOutputDistinguishesCoverageAndChecksAnchor(t *testing.T) {
	a, view, _ := savedRecipeRunFixture(t)
	handler := indexInterfaceHandler(a)
	path := fmt.Sprintf("/api/v1/index/recipes/output?id=%s&revision=%s&semantic_hash=%s&height=0", view.Recipe.ID, view.Revision, view.SemanticHash)
	get := func(s string) (int, map[string]any) {
		t.Helper()
		r := indexInterfaceCall(handler, "GET", s, "")
		var v map[string]any
		if err := json.Unmarshal(r.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return r.Code, v
	}
	code, v := get(path)
	if code != http.StatusOK || v["state"] != "uncovered" || v["rows"] != nil {
		t.Fatalf("uncovered: %d %+v", code, v)
	}
	block, _ := recipeConsumerGenesis(t)
	store, err := openRecipeStore(a.dataDir, view.Recipe.ID, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	unit := recipeStoreUnit{Schema: 1, Fingerprint: view.SemanticHash, Height: 0, BlockHash: block.Hash, Rows: []recipeStoreRow{{Key: `["items","a"]`, Value: json.RawMessage(`{"ref":"a"}`)}, {Key: `["items","b"]`, Value: json.RawMessage(`{"ref":"b"}`)}}}
	if err = store.Publish(context.Background(), unit, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	code, v = get(path + "&limit=1")
	if code != 200 || v["state"] != "covered" || v["row_count"] != float64(2) || v["next_offset"] != float64(1) {
		t.Fatalf("first page: %d %+v", code, v)
	}
	token, ok := v["unit"].(string)
	if !ok {
		t.Fatal("missing token")
	}
	code, v = get(path + "&limit=1&offset=1&unit=" + token)
	if code != 200 || v["state"] != "covered" || len(v["rows"].([]any)) != 1 || v["next_offset"] != nil {
		t.Fatalf("second page: %d %+v", code, v)
	}
	code, _ = get(path + "&offset=1")
	if code != 400 {
		t.Fatalf("unbound page accepted: %d", code)
	}
	code, _ = get(path + "&offset=1&unit=" + recipeTestHash("a"))
	if code != 409 {
		t.Fatalf("wrong token accepted: %d", code)
	}
	bad := recipeStoreUnit{Schema: 1, Fingerprint: view.SemanticHash, Height: 1, BlockHash: recipeTestHash("a"), Rows: []recipeStoreRow{}}
	if err = store.Publish(context.Background(), bad, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	code, _ = get(fmt.Sprintf("/api/v1/index/recipes/output?id=%s&revision=%s&semantic_hash=%s&height=1", view.Recipe.ID, view.Revision, view.SemanticHash))
	if code != 409 {
		t.Fatalf("stale anchor accepted: %d", code)
	}
	if err = os.Rename(a.headersPath, a.headersPath+".offline"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(a.headersPath+".offline", a.headersPath)
	code, _ = get(path)
	if code != 409 {
		t.Fatalf("missing authority accepted: %d", code)
	}
}

func TestRecipeOutputExplicitEmptyReceipt(t *testing.T) {
	a, view, _ := savedRecipeRunFixture(t)
	block, _ := recipeConsumerGenesis(t)
	store, err := openRecipeStore(a.dataDir, view.Recipe.ID, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Publish(context.Background(), recipeStoreUnit{Schema: 1, Fingerprint: view.SemanticHash, Height: 0, BlockHash: block.Hash, Rows: []recipeStoreRow{}}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/index/recipes/output?id=%s&revision=%s&semantic_hash=%s&height=0", view.Recipe.ID, view.Revision, view.SemanticHash)
	r := indexInterfaceCall(indexInterfaceHandler(a), "GET", path, "")
	var v map[string]any
	if err = json.Unmarshal(r.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || v["state"] != "covered" || v["row_count"] != float64(0) || v["rows"] == nil {
		t.Fatalf("empty coverage: %d %+v", r.Code, v)
	}
}

type recipeSlowWriter struct {
	header  http.Header
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *recipeSlowWriter) Header() http.Header { return w.header }
func (w *recipeSlowWriter) WriteHeader(int)     {}
func (w *recipeSlowWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	return len(p), nil
}
func TestRecipeOutputReleasesLocksBeforeSlowResponse(t *testing.T) {
	a, view, _ := savedRecipeRunFixture(t)
	path := fmt.Sprintf("http://127.0.0.1:9090/api/v1/index/recipes/output?id=%s&revision=%s&semantic_hash=%s&height=0", view.Recipe.ID, view.Revision, view.SemanticHash)
	req := httptest.NewRequest("GET", path, nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Origin", "http://127.0.0.1:9090")
	w := &recipeSlowWriter{header: http.Header{}, started: make(chan struct{}), release: make(chan struct{})}
	defer close(w.release)
	done := make(chan struct{})
	go func() { indexInterfaceHandler(a).ServeHTTP(w, req); close(done) }()
	select {
	case <-w.started:
	case <-time.After(15 * time.Second):
		t.Fatal("response did not begin")
	}
	acquired := make(chan struct{})
	go func() {
		a.headerChainMu.Lock()
		recipeRegistryMu.Lock()
		recipeRegistryMu.Unlock()
		a.headerChainMu.Unlock()
		close(acquired)
	}()
	select {
	case <-acquired:
	case <-time.After(3 * time.Second):
		t.Fatal("slow response retained authority locks")
	}
}

func TestRecipeOutputWireBudgetCountsEscapingAndEnvelope(t *testing.T) {
	ctx := context.Background()
	a, view, _ := savedRecipeRunFixture(t)
	block, _ := recipeConsumerGenesis(t)
	store, err := openRecipeStore(a.dataDir, view.Recipe.ID, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	value := json.RawMessage(`"` + strings.Repeat("<", 90000) + `"`)
	unit := recipeStoreUnit{Schema: 1, Fingerprint: view.SemanticHash, Height: 0, BlockHash: block.Hash, Rows: []recipeStoreRow{{Key: strings.Repeat("<", 1024) + "a", Value: value}, {Key: strings.Repeat("<", 1024) + "b", Value: value}}}
	if err = store.Publish(ctx, unit, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/index/recipes/output?id=%s&revision=%s&semantic_hash=%s&height=0&limit=2", view.Recipe.ID, view.Revision, view.SemanticHash)
	handler := indexInterfaceHandler(a)
	first := indexInterfaceCall(handler, "GET", path, "")
	if first.Code != 200 || first.Body.Len() > recipeOutputMaxResponseBytes {
		t.Fatalf("first wire response: %d bytes=%d", first.Code, first.Body.Len())
	}
	var body map[string]any
	if err = json.Unmarshal(first.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	rows, ok := body["rows"].([]any)
	if !ok || len(rows) != 1 || body["next_offset"] != float64(1) {
		t.Fatalf("escaped row paging: %+v", body)
	}
	token, ok := body["unit"].(string)
	if !ok {
		t.Fatal("missing unit token")
	}
	second := indexInterfaceCall(handler, "GET", path+"&offset=1&unit="+token, "")
	if second.Code != 200 || second.Body.Len() > recipeOutputMaxResponseBytes {
		t.Fatalf("second wire response: %d bytes=%d", second.Code, second.Body.Len())
	}
	body = nil
	if err = json.Unmarshal(second.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	rows, ok = body["rows"].([]any)
	if !ok || len(rows) != 1 || body["next_offset"] != nil {
		t.Fatal("second escaped page incorrect")
	}
}

func TestRecipeOutputOversizedValidRowRemainsReadable(t *testing.T) {
	ctx := context.Background()
	a, view, _ := savedRecipeRunFixture(t)
	block, _ := recipeConsumerGenesis(t)
	store, err := openRecipeStore(a.dataDir, view.Recipe.ID, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	value := json.RawMessage(`"` + strings.Repeat("<", 300000) + `"`)
	unit := recipeStoreUnit{Schema: 1, Fingerprint: view.SemanticHash, Height: 0, BlockHash: block.Hash, Rows: []recipeStoreRow{{Key: "large", Value: value}}}
	if err = store.Publish(ctx, unit, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/index/recipes/output?id=%s&revision=%s&semantic_hash=%s&height=0", view.Recipe.ID, view.Revision, view.SemanticHash)
	response := indexInterfaceCall(indexInterfaceHandler(a), "GET", path, "")
	if response.Code != 200 || response.Body.Len() <= recipeOutputMaxResponseBytes || response.Body.Len() > recipeOutputMaxSingleRowResponseBytes {
		t.Fatalf("large row response: %d bytes=%d", response.Code, response.Body.Len())
	}
	var body map[string]any
	if err = json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	rows, ok := body["rows"].([]any)
	if !ok || len(rows) != 1 || body["oversized_row"] != true || body["next_offset"] != nil {
		t.Fatalf("large row not delivered: %+v", body)
	}
}
