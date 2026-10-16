package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexLayoutPersistsAcrossPortsWithoutChangingIndexing(t *testing.T) {
	root := t.TempDir()
	request := func(a *app, port, method, body string) *httptest.ResponseRecorder {
		origin := "http://127.0.0.1:" + port
		r := httptest.NewRequest(method, origin+"/api/v1/index/layout", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:40000"
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.handleIndexLayout(w, r)
		return w
	}
	a := &app{dataDir: root}
	if w := request(a, "50101", "GET", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := os.Stat(indexLayoutPath(root)); !os.IsNotExist(err) {
		t.Fatal("reading layout wrote a file")
	}
	w := request(a, "50101", "POST", `{"order":{"root":["inscriptions","group:bitmap","group:txio"],"group:txio":["txio-sat-ranges","txio-links","txio-values","txio-scripts"],"group:bitmap":["bitmap-parcels","bitmap-districts"],"district:bitmap-districts":["bitmap-chain"]}}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	restarted := &app{dataDir: root}
	w = request(restarted, "50999", "GET", "")
	var got indexLayoutDocument
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Revision != 1 || got.Order["root"][0] != "inscriptions" || got.Order["group:txio"][0] != "txio-sat-ranges" {
		t.Fatal(w.Code, w.Body.String())
	}
	if got.Checksum != "" {
		t.Fatal("internal file checksum leaked into public layout DTO")
	}
	for _, path := range []string{"run.json", "live.json", "job.json", "queue.json"} {
		if _, err := os.Stat(filepath.Join(root, "indexes", path)); !os.IsNotExist(err) {
			t.Fatal("layout changed indexing state:", path)
		}
	}
	w = request(restarted, "50999", "POST", `{"order":{"root":["bitmap-chain"]}}`)
	if w.Code != 400 {
		t.Fatal("reparent accepted", w.Code, w.Body.String())
	}
	w = request(restarted, "50999", "GET", "")
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.Revision != 1 || got.Order["root"][0] != "inscriptions" {
		t.Fatal("invalid layout overwrote saved order")
	}
	w = request(restarted, "50999", "POST", `{"order":{"group:bitmap":["unknown-old-track","bitmap-districts","bitmap-districts"]}}`)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got.Order["group:bitmap"]) != 1 {
		t.Fatal("stale/duplicate identities not filtered", w.Code, w.Body.String())
	}
}

func TestIndexLayoutRejectsForeignOriginAndNonJSON(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	for _, tc := range []struct {
		origin, content, body string
		want                  int
	}{
		{"https://untrusted.invalid", "application/json", `{"order":{}}`, 403},
		{"http://127.0.0.1:5000", "text/plain", `{"order":{}}`, 400},
		{"http://127.0.0.1:5000", "application/json", `{"order":{},"unexpected":true}`, 400},
		{"http://127.0.0.1:5000", "application/json", `{"order":{}} {}`, 400},
	} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:5000/api/v1/index/layout", strings.NewReader(tc.body))
		r.RemoteAddr = "127.0.0.1:40000"
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", tc.content)
		w := httptest.NewRecorder()
		a.handleIndexLayout(w, r)
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if _, err := os.Stat(indexLayoutPath(a.dataDir)); !os.IsNotExist(err) {
		t.Fatal("rejected requests created layout state")
	}
}
