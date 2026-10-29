package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestPortablePreviewDeniesProductionBrowserRouting(t *testing.T) {
	previous := portablePreviewBuild
	portablePreviewBuild = "test"
	t.Cleanup(func() { portablePreviewBuild = previous })
	a := &app{dataDir: t.TempDir()}
	for _, body := range []string{`{"action":"browser-routing","enabled":true,"consent":true}`, `{"action":"browser-routing","enabled":false,"consent":true}`, `{"action":"register-native","consent":true}`} {
		w := httptest.NewRecorder()
		a.handleSetup(w, httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(body)))
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "TESTING-PREVIEW.md") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, enabled := range []bool{true, false} {
		result := a.configureBrowserRouting(enabled)
		if result.Complete || len(result.Steps) != 1 || result.Steps[0].Success || result.Steps[0].ID != "portable-preview" {
			t.Fatal(result)
		}
	}
	entries, err := os.ReadDir(a.dataDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("blocked routing mutated profile state", entries, err)
	}
}

func TestPortablePreviewDeniesUpdateMutation(t *testing.T) {
	previous := portablePreviewBuild
	portablePreviewBuild = "test"
	t.Cleanup(func() { portablePreviewBuild = previous })
	a := &app{dataDir: t.TempDir()}
	mux := http.NewServeMux()
	a.registerUpdateRoutes(mux)
	for _, op := range []string{"config", "check", "download", "apply"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/updates/"+op, strings.NewReader("{}")))
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", op, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/updates/status", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"can_apply":false`) {
		t.Fatal(w.Body.String())
	}
}
