package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func indexPresentationRequest(a *app, method, body string) *httptest.ResponseRecorder {
	return indexInterfaceCall(indexInterfaceHandler(a), method, "/api/v1/index/layout/hierarchy", body)
}
func TestIndexPresentationPreservesLayoutAndDoesNotStartWork(t *testing.T) {
	root := t.TempDir()
	a := &app{dataDir: root}
	before := indexPresentationRequest(a, "GET", "")
	var initial indexPresentationView
	if before.Code != 200 || json.Unmarshal(before.Body.Bytes(), &initial) != nil || initial.Revision != 0 || len(initial.Nodes) != 0 {
		t.Fatalf("initial: %d %s", before.Code, before.Body.String())
	}
	if _, err := os.Stat(indexLayoutPath(root)); !os.IsNotExist(err) {
		t.Fatal("read created layout")
	}
	old := indexLayoutDocument{Schema: 2, Revision: 9, Order: map[string][]string{"root": {"unknown-retired-track", "unknown-retired-track", "group:blocks"}, "group:future": {"unavailable-legacy-index"}, "group:txio": {"txio-links", "old-child"}}}
	old.Checksum = indexLayoutDigest(old)
	if err := atomicWriteJSON(indexLayoutPath(root), old); err != nil {
		t.Fatal(err)
	}
	oldBytes, err := os.ReadFile(indexLayoutPath(root))
	if err != nil {
		t.Fatal(err)
	}
	read := indexPresentationRequest(a, "GET", "")
	if read.Code != 200 {
		t.Fatalf("read prior layout: %d", read.Code)
	}
	reread, err := os.ReadFile(indexLayoutPath(root))
	if err != nil || !bytes.Equal(oldBytes, reread) {
		t.Fatal("hierarchy read changed old layout", err)
	}
	saved := indexPresentationRequest(a, "POST", `{"expected_revision":9,"nodes":[{"id":"category-a","kind":"category","name":"Category A"},{"id":"track-a","kind":"track","name":"Parent track","parent":"category-a","index":"old/not-installed-index"},{"id":"track-b","kind":"track","name":"Child track","parent":"track-a","index":"custom-draft"}]}`)
	var view indexPresentationView
	if saved.Code != 200 || json.Unmarshal(saved.Body.Bytes(), &view) != nil || view.Revision != 10 || len(view.Nodes) != 3 || view.Nodes[2].Parent != "track-a" {
		t.Fatalf("save: %d %s", saved.Code, saved.Body.String())
	}
	raw, err := os.ReadFile(indexLayoutPath(root))
	if err != nil {
		t.Fatal(err)
	}
	var stored indexLayoutDocument
	if err = json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("unknown-retired-track")) || !bytes.Contains(raw, []byte("unavailable-legacy-index")) || !bytes.Contains(raw, []byte("old-child")) || stored.Schema != 3 || len(stored.Hierarchy) != 3 || stored.Order["group:txio"][0] != "txio-links" {
		t.Fatalf("saved order changed: %s", raw)
	}
	restart := &app{dataDir: root}
	reloaded := indexPresentationRequest(restart, "GET", "")
	if reloaded.Code != 200 || json.Unmarshal(reloaded.Body.Bytes(), &view) != nil || view.Revision != 10 || view.Nodes[1].Index != "old/not-installed-index" {
		t.Fatalf("reload: %d %s", reloaded.Code, reloaded.Body.String())
	}
	projected := indexInterfaceCall(http.HandlerFunc(restart.handleIndexLayout), "GET", "/api/v1/index/layout", "")
	var projectedLayout indexLayoutDocument
	if projected.Code != 200 || json.Unmarshal(projected.Body.Bytes(), &projectedLayout) != nil || projectedLayout.Schema != 3 || len(projectedLayout.Hierarchy) != 3 {
		t.Fatalf("schema 3 read: %d %s", projected.Code, projected.Body.String())
	}
	// Existing client writes only order; the hierarchy and unknown entries survive.
	reorder := indexInterfaceCall(http.HandlerFunc(restart.handleIndexLayout), "POST", "/api/v1/index/layout", `{"order":{"root":["group:blocks"],"group:txio":["txio-links"]}}`)
	if reorder.Code != 200 {
		t.Fatalf("old client reorder: %d %s", reorder.Code, reorder.Body.String())
	}
	raw, err = os.ReadFile(indexLayoutPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(raw, &stored) != nil || len(stored.Hierarchy) != 3 || len(stored.Order["root"]) < 3 || stored.Order["root"][0] != "unknown-retired-track" || stored.Order["root"][1] != "unknown-retired-track" || !bytes.Contains(raw, []byte("unknown-retired-track")) || !bytes.Contains(raw, []byte("unavailable-legacy-index")) || !bytes.Contains(raw, []byte("old-child")) {
		t.Fatalf("old client erased preserved presentation: %s", raw)
	}
	stale := indexPresentationRequest(restart, "POST", `{"expected_revision":10,"nodes":[]}`)
	if stale.Code != 409 {
		t.Fatalf("stale edit accepted: %d", stale.Code)
	}
	for _, name := range []string{"run.json", "job.json", "queue.json", "live.json"} {
		if _, err = os.Stat(filepath.Join(root, "indexes", name)); !os.IsNotExist(err) {
			t.Fatal("presentation touched indexing:", name, err)
		}
	}
}
func TestIndexPresentationRejectsInvalidTrees(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	cases := []string{
		`{"expected_revision":0,"nodes":[{"id":"a","kind":"category","name":"A","parent":"b"},{"id":"b","kind":"track","name":"B","parent":"a","index":"x"}]}`,
		`{"expected_revision":0,"nodes":[{"id":"a","kind":"track","name":"A","index":"x","parent":"missing"}]}`,
		`{"expected_revision":0,"nodes":[{"id":"a","kind":"category","name":"A"},{"id":"a","kind":"category","name":"B"}]}`,
		`{"expected_revision":0,"nodes":[{"id":"../a","kind":"category","name":"A"}]}`,
		`{"expected_revision":0,"nodes":[{"id":"a","kind":"category","name":"A","index":"blocks"}]}`,
		`{"expected_revision":0,"nodes":[{"id":"a","kind":"track","name":"A","index":""}]}`,
		`{"expected_revision":0,"nodes":[{"id":"a","kind":"track","name":"A","index":"x","dependencies":["blocks"]}]}`,
		`{"expected_revision":0,"nodes":[],"nodes":[]}`,
		`{"expected_revision":0,"nodes":[]} {}`,
		`{"nodes":[]}`,
	}
	for _, body := range cases {
		r := indexPresentationRequest(a, "POST", body)
		if r.Code != 400 {
			t.Fatalf("invalid accepted: %d %s", r.Code, r.Body.String())
		}
	}
	var deep []indexPresentationNode
	for i := 0; i < indexPresentationMaxDepth+2; i++ {
		id := fmt.Sprintf("n%d", i)
		parent := ""
		if i > 0 {
			parent = fmt.Sprintf("n%d", i-1)
		}
		deep = append(deep, indexPresentationNode{ID: id, Kind: "category", Name: id, Parent: parent})
	}
	if err := validateIndexPresentation(deep); err == nil {
		t.Fatal("depth limit not enforced")
	}
	if _, err := os.Stat(indexLayoutPath(a.dataDir)); !os.IsNotExist(err) {
		t.Fatal("invalid write changed layout")
	}
}
func TestIndexPresentationConcurrentWritesConflict(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, id := range []string{"a", "b"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			body := `{"expected_revision":0,"nodes":[{"id":"` + id + `","kind":"category","name":"` + id + `"}]}`
			codes <- indexPresentationRequest(a, "POST", body).Code
		}(id)
	}
	wg.Wait()
	close(codes)
	count := map[int]int{}
	for c := range codes {
		count[c]++
	}
	if count[200] != 1 || count[409] != 1 {
		t.Fatal(count)
	}
}

func TestIndexPresentationOversizedStoredDocumentDoesNotReplaceLayout(t *testing.T) {
	root := t.TempDir()
	a := &app{dataDir: root}
	initial := indexPresentationRequest(a, "POST", `{"expected_revision":0,"nodes":[{"id":"original","kind":"category","name":"Original"}]}`)
	if initial.Code != 200 {
		t.Fatal(initial.Code, initial.Body.String())
	}
	before, err := os.ReadFile(indexLayoutPath(root))
	if err != nil {
		t.Fatal(err)
	}
	nodes := make([]indexPresentationNode, 0, indexPresentationMaxNodes)
	for i := 0; i < indexPresentationMaxNodes; i++ {
		nodes = append(nodes, indexPresentationNode{ID: fmt.Sprintf("track-%03d", i), Kind: "track", Name: strings.Repeat("n", 115), Index: "future-index"})
	}
	body, err := json.Marshal(map[string]any{"expected_revision": 1, "nodes": nodes})
	if err != nil || len(body) >= 64<<10 {
		t.Fatalf("fixture not within HTTP bound: %d %v", len(body), err)
	}
	r := indexPresentationRequest(a, "POST", string(body))
	if r.Code != 400 || !strings.Contains(r.Body.String(), "saved size") {
		t.Fatalf("oversized document accepted: %d %s", r.Code, r.Body.String())
	}
	after, err := os.ReadFile(indexLayoutPath(root))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("oversized layout modified existing data", err)
	}
}

type indexPresentationSlowBody struct {
	started chan struct{}
	release chan struct{}
	data    *strings.Reader
}

func (b *indexPresentationSlowBody) Read(p []byte) (int, error) {
	select {
	case <-b.started:
	default:
		close(b.started)
	}
	<-b.release
	return b.data.Read(p)
}
func TestIndexPresentationSlowRequestDoesNotHoldLayoutLock(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	started := make(chan struct{})
	release := make(chan struct{})
	r := httptest.NewRequest("POST", "http://127.0.0.1:9090/api/v1/index/layout/hierarchy", nil)
	r.RemoteAddr = "127.0.0.1:50000"
	r.Header.Set("Origin", "http://127.0.0.1:9090")
	r.Header.Set("Content-Type", "application/json")
	r.Body = io.NopCloser(&indexPresentationSlowBody{started: started, release: release, data: strings.NewReader(`{"expected_revision":0,"nodes":[]}`)})
	done := make(chan struct{})
	defer func() { close(release); <-done }()
	go func() { defer close(done); w := httptest.NewRecorder(); a.handleIndexPresentation(w, r) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("POST body did not begin")
	}
	readDone := make(chan int, 1)
	go func() { readDone <- indexPresentationRequest(a, "GET", "").Code }()
	select {
	case code := <-readDone:
		if code != 200 {
			t.Fatalf("GET status %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("slow POST blocked a layout read")
	}
}
