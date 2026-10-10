package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The block receipt is a controlled authenticated fixture; the locator, parser,
// session, content and management handlers below are the production code paths.
func ordLocatedContentFixture072(t *testing.T) (*app, string) {
	t.Helper()
	a := prepareLiveTestApp063(t)
	a.settings.OrdEnabled = true
	a.contentURL, a.runtimeURL = "http://127.0.0.2:1234", "http://127.0.0.1:9090"
	if err := writeRuntimeInfo(a.dataDir, a.runtimeURL); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 80)
	binary.LittleEndian.PutUint64(header[72:], 997)
	digest := hash256(header)
	hash := reverseHex(digest[:])
	if err := os.WriteFile(a.headersPath, header, 0600); err != nil {
		t.Fatal(err)
	}
	a.status.HeaderCount, a.status.HeaderHeight = 1, 0
	_, batch, block := positionalInscriptionFixture(t, a.dataDir, "lean", 0, hash, "", true)
	s, err := openIndexStore(a.dataDir, inscriptionLocatorIndex)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.appendInscriptionLocators(batch, &block, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	target, err := a.localBlockTarget(0)
	if err != nil {
		t.Fatal(err)
	}
	ordDecodedFixture072(t, a, block, target)
	return a, block.Transactions[12].TxID + "i1"
}

func Test072ContentSessionRecursiveKnownLocatorWithoutCookies(t *testing.T) {
	a, dependency := ordLocatedContentFixture072(t)
	root := strings.Repeat("ab", 32) + "i0"
	saveOrdFixture050(t, a, root, "", []byte("<html>intended recursive viewer</html>"))
	record, _, err := a.loadOrdRecord(root)
	if err != nil {
		t.Fatal(err)
	}
	a.attachOrdViewer(&record)
	t.Cleanup(func() { a.ordViewer(record.ViewerSession).cancel() })
	if _, _, err = a.loadOrdRecord(dependency); !os.IsNotExist(err) {
		t.Fatal("dependency already cached", err)
	}
	// Root navigation carries a read-only grant, with no cross-site cookie.
	navigation := httptest.NewRequest("GET", record.PreviewURL, nil)
	navigation.RemoteAddr = "127.0.0.1:45000"
	navigation.Header.Set("Sec-Fetch-Site", "cross-site")
	navigation.Header.Set("Sec-Fetch-Mode", "navigate")
	navigation.Header.Set("Sec-Fetch-Dest", "iframe")
	response := httptest.NewRecorder()
	a.serveOrdContent(response, navigation)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "sandbox=\"allow-scripts allow-same-origin\"") || response.Header().Get("Referrer-Policy") != "same-origin" {
		t.Fatal("root wrapper denied", response.Code, response.Body.String())
	}
	// Browsers with third-party cookies blocked still retain this same-origin
	// Referer. It grants scoped fetching but no access to app controls.
	r := ordContentSameOriginRequest(a, "GET", "/content/"+dependency)
	r.Header.Set("Referer", record.ContentURL)
	response = httptest.NewRecorder()
	a.serveOrdContent(response, r)
	if response.Code != 200 || response.Body.String() != "body retained only in Full" {
		t.Fatal("native located recursion failed", response.Code, response.Body.String())
	}
	resolved, _, err := a.loadOrdRecord(dependency)
	if err != nil || resolved.Provider != "bitcoin_on_demand_witness" || resolved.Profile != inscriptionParserProfile {
		t.Fatal("dependency lost verified source", resolved, err)
	}
	session := a.ordViewer(record.ViewerSession)
	if session.dependencies[dependency].State != "resolved" || session.fetches != 1 {
		t.Fatal("dependency not tracked", session.dependencies, session.fetches)
	}
	missing := strings.Repeat("ef", 32) + "i0"
	r = ordContentSameOriginRequest(a, "GET", "/content/"+missing)
	r.Header.Set("Referer", record.ContentURL)
	response = httptest.NewRecorder()
	a.serveOrdContent(response, r)
	if response.Code != 404 || !strings.Contains(response.Body.String(), ordLocationUnknown) {
		t.Fatal("missing locator misreported", response.Code, response.Body.String())
	}
	// Scoped URLs are process-local, never serialized with the authenticated body.
	b, err := os.ReadFile(a.ordPath(dependency, ".json"))
	if err != nil || strings.Contains(string(b), record.ViewerSession) || strings.Contains(string(b), a.contentURL) {
		t.Fatal("viewer capability persisted", err)
	}
}

func Test072ContentViewerTokenHasNoManagementOrForeignProbePrivilege(t *testing.T) {
	a := ordContentSecurityApp(t)
	a.contentURL = "http://127.0.0.2:1234"
	id := strings.Repeat("ac", 32) + "i0"
	saveOrdFixture050(t, a, id, "", []byte("private fixture"))
	rec, _, err := a.loadOrdRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	a.attachOrdViewer(&rec)
	session := a.ordViewer(rec.ViewerSession)
	t.Cleanup(session.cancel)
	for _, dest := range []string{"image", "audio", "video", "empty", "script"} {
		r := httptest.NewRequest("GET", rec.ContentURL, nil)
		r.RemoteAddr = "127.0.0.1:40000"
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Sec-Fetch-Mode", "no-cors")
		r.Header.Set("Sec-Fetch-Dest", dest)
		w := httptest.NewRecorder()
		a.serveOrdContent(w, r)
		if w.Code != 403 {
			t.Fatal("foreign probe borrowed viewer token", dest, w.Code)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/ord/dependencies", a.handleOrdDependencies)
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, a.runtimeURL+rec.DependencyURL, strings.NewReader(fmt.Sprintf(`{"session":%q,"action":"close"}`, rec.ViewerSession)))
		r.RemoteAddr = "127.0.0.1:40000"
		r.Header.Set("Origin", a.contentURL)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.guardGateway(mux).ServeHTTP(w, r)
		if w.Code != 403 || session.ctx.Err() != nil {
			t.Fatal("content canceled/read management session", method, w.Code)
		}
	}
	r := httptest.NewRequest("POST", a.contentURL+"/api/v1/ord/dependencies", strings.NewReader(`{}`))
	r.RemoteAddr = "127.0.0.1:40000"
	w := httptest.NewRecorder()
	a.serveOrdContent(w, r)
	if w.Code != 405 {
		t.Fatal("read-only content exposed management", w.Code)
	}
	r = httptest.NewRequest("POST", a.runtimeURL+"/api/v1/ord/dependencies", strings.NewReader(fmt.Sprintf(`{"session":%q,"action":"close"}`, rec.ViewerSession)))
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Origin", a.runtimeURL)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.guardGateway(mux).ServeHTTP(w, r)
	if w.Code != 200 || session.ctx.Err() != context.Canceled {
		t.Fatal("trusted selection change did not cancel", w.Code, session.ctx.Err())
	}
}

func Test072PreviewMediaAndRawDelegateBytes(t *testing.T) {
	a := ordContentSecurityApp(t)
	a.contentURL = "http://127.0.0.2:1234"
	one, two := strings.Repeat("ae", 32)+"i0", strings.Repeat("af", 32)+"i0"
	saveOrdFixture050(t, a, one, two, []byte("original bytes"))
	saveOrdFixture050(t, a, two, "", []byte("delegated bytes"))
	rec, body, err := a.loadOrdRecord(one)
	if err != nil {
		t.Fatal(err)
	}
	rec.Envelope.ContentType = "image/png"
	if err = a.saveOrdRecord(rec, body); err != nil {
		t.Fatal(err)
	}
	a.attachOrdViewer(&rec)
	t.Cleanup(a.ordViewer(rec.ViewerSession).cancel)
	u, _ := url.Parse(rec.PreviewURL)
	r := ordContentSameOriginRequest(a, "GET", u.RequestURI())
	w := httptest.NewRecorder()
	a.serveOrdContent(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<img") || strings.Contains(w.Body.String(), a.runtimeURL) {
		t.Fatal("passive wrapper not confined", w.Code, w.Body.String())
	}
	u, _ = url.Parse(rec.RawURL)
	r = ordContentSameOriginRequest(a, "GET", u.RequestURI())
	w = httptest.NewRecorder()
	a.serveOrdContent(w, r)
	if w.Code != 200 || w.Body.String() != "original bytes" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("raw did not preserve reveal body", w.Code, w.Body.String())
	}
	s := a.ordViewer(rec.ViewerSession)
	s.bytes = ordViewerMaxBytes
	u, _ = url.Parse(rec.ContentURL)
	r = ordContentSameOriginRequest(a, "GET", u.RequestURI())
	w = httptest.NewRecorder()
	a.serveOrdContent(w, r)
	if w.Code != 429 {
		t.Fatal("body budget ignored", w.Code)
	}
}

func Test072OrdCacheBoundEvictsOnlyOnDemandRecords(t *testing.T) {
	a := ordContentSecurityApp(t)
	if err := os.MkdirAll(a.ordRoot(), 0700); err != nil {
		t.Fatal(err)
	}
	oldest := ""
	keep := ""
	for i := 0; i <= ordContentCacheMaxEntries; i++ {
		id := fmt.Sprintf("%064xi0", i+1)
		if i == 0 {
			oldest = id
		}
		keep = id
		if err := os.WriteFile(a.ordPath(id, ".bin"), []byte{1}, 0600); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err := os.WriteFile(a.ordPath(id, ".json"), []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}
			at := time.Now().Add(-time.Hour)
			if err := os.Chtimes(a.ordPath(id, ".bin"), at, at); err != nil {
				t.Fatal(err)
			}
		}
	}
	full := filepath.Join(a.dataDir, "indexes", "inscriptions", "retained-full.bin")
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("retained Full content"), 0600); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(a.ordRoot(), "unrelated.bin")
	if err := os.WriteFile(foreign, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.pruneOrdContentCache(keep); err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{".bin", ".json"} {
		if _, err := os.Stat(a.ordPath(oldest, ext)); !os.IsNotExist(err) {
			t.Fatal("oldest cache record retained", ext, err)
		}
	}
	for _, path := range []string{full, foreign, a.ordPath(keep, ".bin")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("evicted outside bounded cache", path, err)
		}
	}
	status := a.ordContentCacheStatus()
	if status["entries"] != ordContentCacheMaxEntries {
		b, _ := json.Marshal(status)
		t.Fatal(string(b))
	}
}

func Test072ViewerDependencyConsumersCancelIndependently(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	work, stop := context.WithCancel(context.Background())
	defer stop()
	s := &ordViewerSession{ctx: context.Background(), pending: map[string]*ordViewerFetch{}, dependencies: map[string]ordDependency{}}
	f := &ordViewerFetch{done: make(chan struct{}), cancel: stop, waiters: 2}
	s.pending["id"] = f
	finished := make(chan error, 1)
	go func() { _, _, err := waitOrdViewerFetch(ctx, s, "id", f); finished <- err }()
	cancel()
	if err := <-finished; err != context.Canceled || work.Err() != nil {
		t.Fatal("one dependency consumer canceled shared work", err, work.Err())
	}
	other, cancelOther := context.WithCancel(context.Background())
	go func() { _, _, err := waitOrdViewerFetch(other, s, "id", f); finished <- err }()
	cancelOther()
	if err := <-finished; err != context.Canceled || work.Err() != context.Canceled || s.pending["id"] != nil {
		t.Fatal("abandoned dependency retained work", err, work.Err())
	}
}

func Test072CanceledResolveDoesNotAllocateViewerSession(t *testing.T) {
	a := ordContentSecurityApp(t)
	id := strings.Repeat("ad", 32) + "i0"
	saveOrdFixture050(t, a, id, "", []byte("resolved content"))
	rec, _, err := a.loadOrdRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", a.runtimeURL+"/api/v1/ord/resolve", nil).WithContext(ctx)
	if a.attachOrdViewerForRequest(r, &rec) || rec.ViewerSession != "" || len(a.ordViewers) != 0 {
		t.Fatal("canceled response created an orphan viewer session")
	}
}
