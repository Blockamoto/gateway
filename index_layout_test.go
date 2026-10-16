package main

import (
	"context"
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
	w := request(a, "50101", "POST", `{"order":{"root":["inscriptions","group:bitmap","group:txio"],"group:txio":["txio-sat-ranges","txio-links","txio-values","txio-scripts"],"group:bitmap":["bitmap-districts"],"district:bitmap-districts":["bitmap-parcels"]}}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	restarted := &app{dataDir: root}
	w = request(restarted, "50999", "GET", "")
	var got indexLayoutDocument
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Schema != 2 || got.Revision != 1 || got.Order["root"][0] != "inscriptions" || got.Order["group:txio"][0] != "txio-sat-ranges" || got.Order["district:bitmap-districts"][0] != "bitmap-parcels" {
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
	w = request(restarted, "50999", "POST", `{"order":{"root":["bitmap-parcels"]}}`)
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

func TestIndexLayoutMigratesParcelOrderWithoutWritingOrReparenting(t *testing.T) {
	ctx, root := context.Background(), t.TempDir()
	district := bitmapTestCreateProfile(t, root, "Custom District", 1, true)
	instances, err := bitmapProfileInstances(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	var parcel, chain string
	for _, i := range instances {
		if i.DistrictInstance == district.ID {
			if i.Kind == "parcel" {
				parcel = i.ID
			} else if i.Kind == "chain" {
				chain = i.ID
			}
		}
	}
	if parcel == "" || chain == "" {
		t.Fatal("missing linked fixtures")
	}
	legacy := indexLayoutDocument{Schema: 1, Revision: 7, Order: map[string][]string{
		"root":                      {"group:bitmap", "group:txio"},
		"group:bitmap":              {parcel, "bitmap-parcels", district.ID, "bitmap-districts"},
		"district:" + district.ID:   {chain},
		"district:bitmap-districts": {"bitmap-chain"},
	}}
	legacy.Checksum = indexLayoutDigest(legacy)
	if err = atomicWriteJSON(indexLayoutPath(root), legacy); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(indexLayoutPath(root))
	if err != nil {
		t.Fatal(err)
	}
	got, err := readIndexLayout(ctx, root)
	if err != nil || got.Schema != 2 || got.Revision != 7 {
		t.Fatal(got, err)
	}
	if ids := got.Order["group:bitmap"]; len(ids) != 2 || ids[0] != district.ID || ids[1] != "bitmap-districts" {
		t.Fatal("District order changed", ids)
	}
	if ids := got.Order["district:"+district.ID]; len(ids) != 1 || ids[0] != parcel {
		t.Fatal("custom Parcel escaped parent", ids)
	}
	if ids := got.Order["district:bitmap-districts"]; len(ids) != 1 || ids[0] != "bitmap-parcels" {
		t.Fatal("legacy Parcel escaped parent", ids)
	}
	after, err := os.ReadFile(indexLayoutPath(root))
	if err != nil || string(before) != string(after) {
		t.Fatal("reading migration rewrote saved layout", err)
	}
	if _, err = normalizeIndexLayout(ctx, root, map[string][]string{"district:bitmap-districts": {parcel}}); err == nil {
		t.Fatal("Parcel moved to a different District")
	}
	if _, err = normalizeIndexLayout(ctx, root, map[string][]string{"group:bitmap": {parcel}}); err == nil {
		t.Fatal("new request used legacy migration")
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
