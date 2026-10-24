package main

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestShellEmbeddedWorkspaceAssetReferencesAreServed(t *testing.T) {
	refs := regexp.MustCompile(`(?:src|href)="(/shell/[^"?#]+)"`)
	app := &app{}
	seen := map[string]bool{}
	for _, page := range []string{"index.html", "indexes.html"} {
		body, err := gatewayShell.ReadFile("ui/shell/" + page)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range refs.FindAllSubmatch(body, -1) {
			path := string(match[1])
			if seen[path] {
				continue
			}
			seen[path] = true
			t.Run(path, func(t *testing.T) {
				w := httptest.NewRecorder()
				app.handleShellAsset(w, httptest.NewRequest("GET", path, nil))
				if w.Code != 200 || w.Body.Len() == 0 {
					t.Fatalf("embedded reference unavailable: status=%d bytes=%d", w.Code, w.Body.Len())
				}
				if strings.HasSuffix(path, ".js") && !strings.HasPrefix(w.Header().Get("Content-Type"), "application/javascript") {
					t.Fatal("script MIME type", w.Header())
				}
			})
		}
	}
	for _, name := range []string{"explorer-features.js", "explorer-features.css", "index-help.js", "index-help-content.js", "index-help.css", "index-scrollbar-geometry.js"} {
		if !seen["/shell/"+name] {
			t.Fatal("new workspace asset not referenced", name)
		}
	}
}
