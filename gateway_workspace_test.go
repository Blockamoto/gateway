package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestGatewayWorkspaceServesEveryReferencedScript(t *testing.T) {
	a := &app{}
	w := httptest.NewRecorder()
	a.serveGatewayShell(w, httptest.NewRequest("GET", "http://127.0.0.1:9090/", nil))
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("shell protection or initial response missing", w.Code)
	}
	refs := regexp.MustCompile(`<script src="([^"]+)"`).FindAllStringSubmatch(w.Body.String(), -1)
	if len(refs) < 4 {
		t.Fatal("workspace dependencies missing")
	}
	for _, ref := range refs {
		asset := httptest.NewRecorder()
		a.handleShellAsset(asset, httptest.NewRequest("GET", "http://127.0.0.1:9090"+ref[1], nil))
		if asset.Code != 200 || asset.Body.Len() == 0 || !strings.Contains(asset.Header().Get("Content-Type"), "javascript") {
			t.Fatal("referenced shell dependency unavailable", ref[1], asset.Code)
		}
	}
}

func TestGatewayReleaseVersionSourcesAgree(t *testing.T) {
	b, err := os.ReadFile("VERSION.txt")
	if err != nil || strings.TrimSpace(string(b)) != appVersion {
		t.Fatal("build version differs from application", err)
	}
	b, err = os.ReadFile("COMPATIBILITY.json")
	var c struct {
		Version string `json:"app_version"`
	}
	if err != nil || json.Unmarshal(b, &c) != nil || c.Version != appVersion {
		t.Fatal("compatibility version differs")
	}
	b, err = os.ReadFile("packaging/windows-installer/main.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, versioned := range []string{`{"DisplayVersion", "REG_SZ", "__GATEWAY_VERSION__"}`, `"Gateway Client __GATEWAY_VERSION__"`, `"Gateway Client __GATEWAY_VERSION__ Setup"`} {
		if !strings.Contains(string(b), versioned) {
			t.Fatal("installer must use build version for registration and visible labels", versioned)
		}
	}
	for _, name := range []string{"ui/shell/index.html", "ui/shell/network.js", "ui/shell/app.js"} {
		b, err = os.ReadFile(name)
		if err != nil || strings.Contains(string(b), "0.5.4") {
			t.Fatal("stale current release label", name)
		}
	}
}
