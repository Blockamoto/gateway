package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func Test063SparseShellStylesAreServed(t *testing.T) {
	a := &app{}
	response := httptest.NewRecorder()
	a.handleShellAsset(response, httptest.NewRequest("GET", "/shell/sparse.css", nil))
	if response.Code != 200 || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/css") || !strings.Contains(response.Body.String(), ".home-state") {
		t.Fatalf("sparse styles unavailable: %d %s", response.Code, response.Body.String())
	}
	body, err := gatewayShell.ReadFile("ui/shell/index.html")
	if err != nil || !strings.Contains(string(body), `href="/shell/sparse.css"`) {
		t.Fatalf("sparse stylesheet not linked: %v", err)
	}
}
