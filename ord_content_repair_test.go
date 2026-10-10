package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func ordContentSecurityApp(t *testing.T) *app {
	t.Helper()
	a := &app{dataDir: t.TempDir(), contentURL: "http://127.0.0.1:1234", runtimeURL: "http://127.0.0.1:9090", settings: appSettings{OrdEnabled: true}}
	if err := writeRuntimeInfo(a.dataDir, a.runtimeURL); err != nil {
		t.Fatal(err)
	}
	return a
}

func ordContentSameOriginRequest(a *app, method, path string) *http.Request {
	r := httptest.NewRequest(method, a.contentURL+path, nil)
	r.RemoteAddr = "127.0.0.1:45000"
	r.Header.Set("Origin", a.contentURL)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	return r
}

func TestOrdContentRepairNavigationAndCachedRecursion(t *testing.T) {
	if !releaseFeatureAvailable("inscriptions") {
		t.Skip("0.6.6 release lock: inscription content navigation is unavailable")
	}
	a := ordContentSecurityApp(t)
	one, two := strings.Repeat("aa", 32)+"i0", strings.Repeat("bb", 32)+"i0"
	saveOrdFixture050(t, a, one, "", []byte("<html>viewer</html>"))
	saveOrdFixture050(t, a, two, "", []byte("dependency"))
	srv := httptest.NewServer(http.HandlerFunc(a.serveOrdContent))
	defer srv.Close()
	a.contentURL = srv.URL
	for _, dest := range []string{"document", "iframe"} {
		req, _ := http.NewRequest("GET", a.ordContentURL(one), nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site") // friendly Gateway host -> loopback
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Sec-Fetch-Dest", dest)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != "<html>viewer</html>" || resp.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("intended %s navigation failed: status=%d body=%q headers=%v", dest, resp.StatusCode, body, resp.Header)
		}
		if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox allow-scripts allow-same-origin") {
			t.Fatal("content sandbox cannot retain its separate origin for recursion")
		}
	}
	for _, path := range []string{"/content/" + two, "/r/inscription/" + two} {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		req.Header.Set("Origin", srv.URL)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 || resp.Header.Get("Access-Control-Allow-Origin") != srv.URL {
			t.Fatalf("cached recursion failed: %s status=%d", path, resp.StatusCode)
		}
	}
	// Browsers may omit Origin for same-origin GET subresources.
	req, _ := http.NewRequest("GET", srv.URL+"/content/"+two, nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("same-origin GET without Origin failed")
	}
	js, err := gatewayShell.ReadFile("ui/shell/inscription-viewport.js")
	if err != nil || !strings.Contains(string(js), `view.setAttribute('sandbox','allow-scripts allow-same-origin')`) || !strings.Contains(string(js), `view.referrerPolicy='no-referrer'`) {
		t.Fatal("Gateway iframe sandbox does not match the isolated content origin")
	}
}

func TestOrdContentRepairRejectsUntrustedRequestsBeforeCache(t *testing.T) {
	a := ordContentSecurityApp(t)
	id, missing := strings.Repeat("aa", 32)+"i0", strings.Repeat("bb", 32)+"i0"
	saveOrdFixture050(t, a, id, "", []byte("private-cache"))
	tests := []struct {
		name, origin, site, mode, dest, host, remote string
		grant                                        bool
	}{
		{name: "foreign_origin", origin: "https://hostile.example", site: "cross-site", grant: true},
		{name: "other_local_port", origin: "http://127.0.0.1:9999", site: "same-site", grant: true},
		{name: "opaque_origin", origin: "null", site: "cross-site", grant: true},
		{name: "dns_rebinding_host", host: "hostile.example:1234", site: "same-origin", grant: true},
		{name: "non_loopback", remote: "192.0.2.3:45000", site: "same-origin", grant: true},
		{name: "external_image", site: "cross-site", mode: "no-cors", dest: "image", grant: true},
		{name: "external_fetch", site: "cross-site", mode: "cors", dest: "empty", grant: true},
		{name: "unknown_native_client"},
		{name: "unsigned_navigation", site: "cross-site", mode: "navigate", dest: "iframe"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, target := range []string{id, missing} {
				address := a.contentURL + "/content/" + target
				if tc.grant {
					address = a.ordContentURL(target)
				}
				r := httptest.NewRequest("GET", address, nil)
				r.RemoteAddr = "127.0.0.1:45000"
				if tc.remote != "" {
					r.RemoteAddr = tc.remote
				}
				if tc.host != "" {
					r.Host = tc.host
				}
				r.Header.Set("Origin", tc.origin)
				r.Header.Set("Sec-Fetch-Site", tc.site)
				r.Header.Set("Sec-Fetch-Mode", tc.mode)
				r.Header.Set("Sec-Fetch-Dest", tc.dest)
				w := httptest.NewRecorder()
				a.serveOrdContent(w, r)
				if w.Code != 403 || w.Header().Get("Access-Control-Allow-Origin") != "" || strings.Contains(w.Body.String(), "private-cache") {
					t.Fatalf("target %s leaked presence/content: %d %q %v", target, w.Code, w.Body.String(), w.Header())
				}
			}
		})
	}
}

func TestOrdContentRepairGrantScopeDisableAndNoManagement(t *testing.T) {
	if !releaseFeatureAvailable("inscriptions") {
		t.Skip("0.6.6 release lock: inscription content origin grants are unavailable")
	}
	a := ordContentSecurityApp(t)
	one, two := strings.Repeat("aa", 32)+"i0", strings.Repeat("bb", 32)+"i0"
	saveOrdFixture050(t, a, one, "", []byte("one"))
	saveOrdFixture050(t, a, two, "", []byte("two"))
	signed, _ := url.Parse(a.ordContentURL(one))
	signed.Path = "/content/" + two
	r := httptest.NewRequest("GET", signed.String(), nil)
	r.RemoteAddr = "127.0.0.1:45000"
	w := httptest.NewRecorder()
	a.serveOrdContent(w, r)
	if w.Code != 403 {
		t.Fatal("grant for one ID authorized another")
	}
	for _, path := range []string{"/api/v1/settings", "/api/v1/ord/resolve", "/content/../../settings.json"} {
		w := httptest.NewRecorder()
		a.serveOrdContent(w, ordContentSameOriginRequest(a, "GET", path))
		if w.Code != 404 {
			t.Fatalf("content origin exposed a management route: %s %d", path, w.Code)
		}
	}
	a.settings.OrdEnabled = false
	w = httptest.NewRecorder()
	a.serveOrdContent(w, ordContentSameOriginRequest(a, "GET", "/content/"+one))
	if w.Code != 403 {
		t.Fatal("disabled Ord listener still disclosed cache")
	}
}
