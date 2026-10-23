package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
