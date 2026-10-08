package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func indexInterfaceHandler(a *app) http.Handler {
	mux := http.NewServeMux()
	a.registerIndexRoutes(mux)
	mux.HandleFunc("/api/v1/runtime/ping", a.handleRuntimePing)
	return a.guardGateway(mux)
}

func indexInterfaceCall(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1:9090"+path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Origin", "http://127.0.0.1:9090")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestIndexInterfaceCLIPlanMatchesAPI(t *testing.T) {
	a := &app{}
	args := []string{"plan", "blocks", "--from=850000", "--to", "850002", "--retention", "ephemeral"}
	cli, err := a.runIndexCLI(args)
	if err != nil {
		t.Fatal(err)
	}
	r := indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/plan", `{"index":"blocks","from":850000,"to":850002,"retention":"ephemeral"}`)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var api indexPlan
	if err := json.Unmarshal(r.Body.Bytes(), &api); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cli, api) {
		t.Fatalf("CLI/API plan diverged: %#v / %#v", cli, api)
	}
	for _, bad := range [][]string{
		{"status", "extra"}, {"pause", "extra"}, {"rules", "bitmap", "other"}, {"build"},
		{"plan", "bitmap", "--from"}, {"plan", "bitmap", "--from", "garbage"},
		{"plan", "bitmap", "--to", "4", "--to=5"}, {"plan", "bitmap", "--force", "yes"},
		{"query", "bitmap", "--limit", "0"}, {"query", "bitmap", "--limit=1001"},
		{"query", "bitmap", "--limit="}, {"query", "bitmap", "--to=850000"}, {"delete", "bitmap"},
		{"publish"}, {"unpublish", "bitmap", "extra"},
		{"lookup"}, {"lookup", "bitmap"}, {"lookup", "bitmap", "0", "extra"},
		{"peers", "extra"}, {"peer"}, {"peer", "127.0.0.1:61430"}, {"peer", "127.0.0.1:61430", "bitmap", "extra"},
	} {
		if _, err := parseIndexCLI(bad); err == nil {
			t.Fatalf("accepted malformed CLI %q", bad)
		}
	}
}

func TestIndexInterfacePeerInspectionIsExplicitAndNeverPromotesClaims(t *testing.T) {
	t.Skip("Gateway peerhood public inspection is intentionally locked in 0.6.6; peer validation helpers retain unit coverage.")
	provider, _ := indexPeerFixture(t, "bitmap", 2)
	if _, err := provider.setIndexPublication("bitmap", true); err != nil {
		t.Fatal(err)
	}
	client := independentTestApp(t)
	peer := "127.0.0.1:61430"
	calls := indexClaimStub(t, client, peer, provider.answerIndexPeerRequest)
	handler := indexInterfaceHandler(client)
	status := indexInterfaceCall(handler, "GET", "/api/v1/index/status", "")
	if status.Code != 200 || calls.Load() != 0 {
		t.Fatal("automatic status performed peer inspection", status.Code, calls.Load())
	}
	claims := indexInterfaceCall(handler, "GET", "/api/v1/index/peers", "")
	var view indexPeerClaimsView
	if claims.Code != 200 || json.Unmarshal(claims.Body.Bytes(), &view) != nil || view.InspectedPeers != 1 || calls.Load() != 1 || view.Verification != indexPeerClaimState {
		t.Fatal("explicit peer claims unavailable", claims.Code, claims.Body.String(), calls.Load())
	}
	if result, err := client.runIndexCLI([]string{"peers"}); err != nil || result.(indexPeerClaimsView).InspectedPeers != 1 || calls.Load() != 2 {
		t.Fatal("CLI peer claims", result, err, calls.Load())
	}
	preview := indexInterfaceCall(handler, "POST", "/api/v1/index/peer-preview", `{"peer":"`+peer+`","index":"bitmap"}`)
	var evidence indexPeerPreviewView
	if preview.Code != 200 || json.Unmarshal(preview.Body.Bytes(), &evidence) != nil || evidence.Stored || evidence.BitcoinReplayPerformed || evidence.Verification != indexPeerClaimState || calls.Load() != 4 {
		t.Fatal("API promoted peer evidence or exceeded preview", preview.Code, preview.Body.String(), calls.Load())
	}
	if result, err := client.runIndexCLI([]string{"peer", peer, "bitmap"}); err != nil || result.(indexPeerPreviewView).Stored || calls.Load() != 6 {
		t.Fatal("CLI preview", result, err, calls.Load())
	}
	unknown := indexInterfaceCall(handler, "POST", "/api/v1/index/peer-preview", `{"peer":"127.0.0.1:61431","index":"bitmap"}`)
	if unknown.Code != 400 || calls.Load() != 6 {
		t.Fatal("unknown peer triggered a query", unknown.Code, calls.Load())
	}
	wrongMethod := indexInterfaceCall(handler, "GET", "/api/v1/index/peer-preview", "")
	if wrongMethod.Code != 405 || calls.Load() != 6 {
		t.Fatal("preview GET accepted", wrongMethod.Code)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	if err := writeRuntimeInfo(client.dataDir, server.URL); err != nil {
		t.Fatal(err)
	}
	command, _ := parseIndexCLI([]string{"peer", peer, "bitmap"})
	result, handled, err := forwardIndexCLI(context.Background(), client.dataDir, command)
	if err != nil || !handled || calls.Load() != 8 {
		t.Fatal("peer preview forwarding", handled, err, calls.Load())
	}
	if err := json.Unmarshal(result.(json.RawMessage), &evidence); err != nil || evidence.Stored || evidence.BitcoinReplayPerformed {
		t.Fatal("forwarded preview promoted evidence", err, evidence)
	}
	if _, err := os.Stat(filepath.Join(client.dataDir, "indexes", "bitmap", "head.json")); !os.IsNotExist(err) {
		t.Fatal("preview persisted peer state", err)
	}
}

func TestIndexInterfaceExactDistrictLookup(t *testing.T) {
	t.Skip("Bitmap public district lookup is intentionally locked in 0.6.6; district algorithms retain unit coverage.")
	a, _ := indexPeerFixture(t, "bitmap", 2)
	handler := indexInterfaceHandler(a)
	for _, key := range []string{"0", "1", "42"} {
		cli, err := a.runIndexCLI([]string{"lookup", "bitmap", key})
		if err != nil {
			t.Fatal(key, err)
		}
		r := indexInterfaceCall(handler, "GET", "/api/v1/index/lookup?index=bitmap&key="+key, "")
		if r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
		var api indexLookupResult
		if err := json.Unmarshal(r.Body.Bytes(), &api); err != nil || !reflect.DeepEqual(cli, api) {
			t.Fatalf("lookup CLI/API mismatch for %s: %#v / %#v (%v)", key, cli, api, err)
		}
		if api.Found != (key != "42") || key == "42" && !strings.Contains(api.Absence, "not a global absence proof") {
			t.Fatal("lookup invented availability or global absence", api)
		}
	}
	for _, key := range []string{"", "01", "-1", "+1", "1.bitmap", "1.5", "18446744073709551616"} {
		if _, err := a.runIndexCLI([]string{"lookup", "bitmap", key}); err == nil {
			t.Fatal("CLI accepted noncanonical district", key)
		}
	}
	for _, path := range []string{"/api/v1/index/lookup?index=bitmap&key=01", "/api/v1/index/lookup?index=inscriptions&key=0", "/api/v1/index/lookup?index=bitmap"} {
		r := indexInterfaceCall(handler, "GET", path, "")
		if r.Code != 400 {
			t.Fatal("invalid lookup accepted", path, r.Code)
		}
	}
	command, err := parseIndexCLI([]string{"lookup", "bitmap", "9007199254740993"})
	if err != nil || command.Key != "9007199254740993" {
		t.Fatal("lookup key lost integer precision", command, err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	if err := writeRuntimeInfo(a.dataDir, server.URL); err != nil {
		t.Fatal(err)
	}
	command, _ = parseIndexCLI([]string{"lookup", "bitmap", "0"})
	result, handled, err := forwardIndexCLI(context.Background(), a.dataDir, command)
	if err != nil || !handled {
		t.Fatal("lookup forwarding failed", handled, err)
	}
	var forwarded indexLookupResult
	if err := json.Unmarshal(result.(json.RawMessage), &forwarded); err != nil || !forwarded.Found || forwarded.Record == nil || *forwarded.Record.District != 0 {
		t.Fatal("forwarding lost exact lookup", forwarded, err)
	}
}

func TestIndexInterfaceRejectsInvalidRequestsWithoutStarting(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	handler := indexInterfaceHandler(a)
	for _, body := range []string{
		``, `null`, `[]`, `{"index":"bitmap"}{}`, `{"index":"bitmap","extra":true}`,
		`{"index":"bitmap","from":850000}`, `{"index":"inscriptions","from":20,"to":10}`,
		`{"index":"inscriptions","retention":"erase"}`, `{"index":"unknown"}`,
		`{"index":"` + strings.Repeat("x", 17<<10) + `"}`,
	} {
		r := indexInterfaceCall(handler, "POST", "/api/v1/index/build", body)
		if r.Code != 400 && r.Code != 415 {
			t.Fatalf("accepted invalid request %q: %d %s", body[:min(50, len(body))], r.Code, r.Body.String())
		}
	}
	for _, route := range []string{"plan", "build", "pause", "publish", "unpublish"} {
		r := indexInterfaceCall(handler, "GET", "/api/v1/index/"+route, "")
		if r.Code != 405 || r.Header().Get("Allow") != "POST" {
			t.Fatal(route, r.Code, r.Body.String())
		}
	}
	r := indexInterfaceCall(handler, "POST", "/api/v1/index/build", `{"index":"inscription-numbering"}`)
	if r.Code != 409 || !strings.Contains(r.Body.String(), "locked in this testing build") {
		t.Fatal("numbering not explicitly unavailable", r.Code, r.Body.String())
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", "job.json")); !os.IsNotExist(err) {
		t.Fatal("invalid request started work", err)
	}
}

func TestIndexInterfacePublicationFreezesReviewedCheckpoint(t *testing.T) {
	t.Skip("Gateway peerhood publication is intentionally locked in 0.6.6; raw checkpoint publication retains unit coverage.")
	a, store := indexPeerFixture(t, "bitmap", 2)
	handler := indexInterfaceHandler(a)
	wrong := indexInterfaceCall(handler, "POST", "/api/v1/index/publish", `{"index":"bitmap","checkpoint":"`+strings.Repeat("f", 64)+`"}`)
	if wrong.Code != 409 || len(a.publishedIndexManifests()) != 0 {
		t.Fatal("published a different checkpoint than reviewed", wrong.Code, wrong.Body.String())
	}
	published := indexInterfaceCall(handler, "POST", "/api/v1/index/publish", `{"index":"bitmap","checkpoint":"`+store.checkpoint.Commitment+`"}`)
	if published.Code != 200 || len(a.publishedIndexManifests()) != 1 {
		t.Fatal(published.Code, published.Body.String())
	}
	before := store.checkpoint.Commitment
	withdrawn := indexInterfaceCall(handler, "POST", "/api/v1/index/unpublish", `{"index":"bitmap"}`)
	if withdrawn.Code != 200 || len(a.publishedIndexManifests()) != 0 {
		t.Fatal(withdrawn.Code, withdrawn.Body.String())
	}
	stillLocal, err := openIndexStore(a.dataDir, "bitmap")
	if err != nil || stillLocal.checkpoint == nil || stillLocal.checkpoint.Commitment != before {
		t.Fatal("withdrawal changed private state", err)
	}
	if _, err := a.runIndexCLI([]string{"publish", "bitmap"}); err != nil {
		t.Fatal("CLI publish", err)
	}
	if _, err := a.runIndexCLI([]string{"unpublish", "bitmap"}); err != nil || len(a.publishedIndexManifests()) != 0 {
		t.Fatal("CLI withdrawal", err)
	}
}

func TestIndexInterfaceManagementIsolationAndPageCSP(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	handler := indexInterfaceHandler(a)
	for _, path := range []string{"/indexes", "/indexes?embedded=1", "/api/v1/index/rules", "/api/v1/index/build"} {
		for _, origin := range []string{"null", "http://evil.example", "http://127.0.0.1:9091"} {
			req := httptest.NewRequest("GET", "http://127.0.0.1:9090"+path, nil)
			req.RemoteAddr = "127.0.0.1:50000"
			req.Header.Set("Origin", origin)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != 403 {
				t.Fatal("hostile origin reached index interface", path, origin, w.Code)
			}
		}
	}
	first := indexInterfaceCall(handler, "GET", "/indexes", "")
	second := indexInterfaceCall(handler, "GET", "/indexes", "")
	csp := first.Header().Get("Content-Security-Policy")
	if first.Code != 200 || !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "script-src 'nonce-") || strings.Contains(csp, "unsafe-inline") || strings.Contains(first.Body.String(), "__INDEX_NONCE__") {
		t.Fatal("index page protection missing", first.Code, csp)
	}
	if first.Body.String() == second.Body.String() {
		t.Fatal("CSP nonce reused")
	}
	embedded := indexInterfaceCall(handler, "GET", "/indexes?embedded=1", "")
	if embedded.Code != 200 || !strings.Contains(embedded.Header().Get("Content-Security-Policy"), "frame-ancestors 'self'") {
		t.Fatal("same-origin index workspace cannot embed", embedded.Code)
	}
	if first.Header().Get("Cache-Control") != "no-store" || !strings.Contains(strings.ToLower(first.Body.String()), "canonical inscription numbers") {
		t.Fatal("cache policy or numbering caveat missing")
	}
}

func TestIndexInterfaceCLIBuildWaitsForCommittedResult(t *testing.T) {
	a, _ := initGraphTestApp(t)
	a.settings.CoreDisabled = true
	result, err := a.runIndexCLI([]string{"build", "blocks", "--from", "0", "--to", "0"})
	if err != nil {
		t.Fatal(err)
	}
	job, ok := result.(indexJob)
	if !ok || job.State != "complete" || job.Height != 0 {
		t.Fatal("CLI returned before its build completed", result)
	}
	s, err := openIndexStore(a.dataDir, "blocks")
	if err != nil || s.checkpoint == nil || s.checkpoint.Height != 0 {
		t.Fatal("CLI result lacks committed checkpoint", s, err)
	}
	query := indexInterfaceCall(indexInterfaceHandler(a), "GET", "/api/v1/index/query?index=blocks&limit=50", "")
	if query.Code != 200 || !strings.Contains(query.Body.String(), genesisHashDisplay) {
		t.Fatal(query.Code, query.Body.String())
	}
}

func TestIndexInterfaceRuntimeForwardingUsesExistingEngine(t *testing.T) {
	a, _ := initGraphTestApp(t)
	a.settings.CoreDisabled = true
	server := httptest.NewServer(indexInterfaceHandler(a))
	defer server.Close()
	if err := writeRuntimeInfo(a.dataDir, server.URL); err != nil {
		t.Fatal(err)
	}
	command, err := parseIndexCLI([]string{"build", "blocks", "--from", "0", "--to", "0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, handled, err := forwardIndexCLI(ctx, a.dataDir, command)
	if err != nil || !handled {
		t.Fatal(handled, err)
	}
	job, ok := result.(indexJob)
	if !ok || job.State != "complete" || job.ID != a.indexJobSnapshot().ID || job.Height != 0 {
		t.Fatalf("forwarding did not wait on the existing engine: %#v", result)
	}
	status, handled, err := forwardIndexCLI(ctx, a.dataDir, indexCLICommand{Action: "status"})
	if !handled || err != nil || !strings.Contains(string(status.(json.RawMessage)), job.ID) {
		t.Fatal(status, handled, err)
	}
}

func TestIndexInterfaceRuntimeRejectsRemoteAndRedirectTargets(t *testing.T) {
	for _, target := range []string{"https://127.0.0.1:1234", "http://127.0.0.1:1234@evil.example", "http://localhost:1234/path", "http://localhost:1234?x=1", "http://example.com:1234", "http://127.0.0.1"} {
		if _, err := newIndexRemote(runtimeInfo{URL: target}); err == nil {
			t.Fatal("accepted unsafe local runtime URL", target)
		}
	}
	visited := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { visited = true; fmt.Fprint(w, `{}`) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	remote, err := newIndexRemote(runtimeInfo{URL: redirect.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer remote.client.CloseIdleConnections()
	if _, err = remote.request(context.Background(), "/api/v1/runtime/ping", nil); err == nil || visited {
		t.Fatal("runtime followed a redirect", err, visited)
	}
}
