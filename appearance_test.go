package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func Test064AppearanceSurvivesNewManagementOrigin(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	request := func(a *app, origin, method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, origin+"/api/v1/appearance", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:50234"
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		a.handleAppearance(w, r)
		return w
	}
	if w := request(a, "http://127.0.0.1:50123", "POST", `{"theme":"dark"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	restarted := &app{dataDir: a.dataDir}
	w := request(restarted, "http://127.0.0.1:50987", "GET", "")
	var p appearancePreference
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &p) != nil || p.Theme != "dark" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request(restarted, "http://127.0.0.1:50987", "POST", `{"theme":"unsupported"}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	w = request(restarted, "http://127.0.0.1:50987", "GET", "")
	if json.Unmarshal(w.Body.Bytes(), &p) != nil || p.Theme != "dark" {
		t.Fatal(w.Body.String())
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:50987/api/v1/appearance", strings.NewReader(`{"theme":"light"}`))
	r.RemoteAddr = "127.0.0.1:50234"
	r.Header.Set("Origin", "https://example.com")
	w = httptest.NewRecorder()
	restarted.handleAppearance(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
