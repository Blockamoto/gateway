package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func numberNavigationApp077(t *testing.T) *app {
	t.Helper()
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = true
	return a
}

func numberNavigationFiles077(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(raw)
		out[strings.TrimPrefix(path, root)] = hex.EncodeToString(digest[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func numberNavigationRequest077(a *app, method, input string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:9090/api/v1/navigate/number?number="+url.QueryEscape(input), nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	a.handleNumberNavigation077(w, r)
	return w
}

func Test077NumberNavigationSelectedTipBoundaryCanonicalAndReadOnly(t *testing.T) {
	a := numberNavigationApp077(t)
	a.setStatus(func(st *appStatus) { st.HeaderTargetHeight = 900000; st.HeaderTargetSource = "peer estimate" })
	before := numberNavigationFiles077(t, a.dataDir)
	for _, tc := range []struct{ input, kind, address string }{
		{"0", "block", "0.bitcoin"},
		{" 0000 ", "block", "0.bitcoin"},
		{"1", "sat", "1.sat"},
		{" 000170 ", "sat", "170.sat"},
		{"9007199254740993", "sat", "9007199254740993.sat"},
		{"18446744073709551615", "sat", "18446744073709551615.sat"},
	} {
		w := numberNavigationRequest077(a, "GET", tc.input)
		var got numberNavigation077
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Kind != tc.kind || got.Address != tc.address || got.TipHeight != 0 || got.TipHash != genesisHashDisplay {
			t.Fatalf("%q: status=%d body=%s", tc.input, w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("dynamic classification was cacheable")
		}
	}
	if after := numberNavigationFiles077(t, a.dataDir); !reflect.DeepEqual(before, after) {
		t.Fatalf("classification changed profile files: before=%v after=%v", before, after)
	}
	if len(a.objects) != 0 || len(a.decoded) != 0 || a.indexCancel != nil || len(a.satlineJobs) != 0 {
		t.Fatal("classification started block resolution, an index job or Satline storage")
	}
}

func Test077NumberNavigationAdvanceReorgAndUnavailableAuthority(t *testing.T) {
	a := numberNavigationApp077(t)
	get := func(kind string, height int64, hash string) {
		t.Helper()
		got, err := a.classifyNumberNavigation077(context.Background(), "1")
		if err != nil || got.Kind != kind || got.TipHeight != height || got.TipHash != hash {
			t.Fatal(got, err)
		}
	}
	get("sat", 0, genesisHashDisplay)
	_, hash := installBlockOneHeader063(t, a)
	get("block", 1, hash)
	a.setStatus(func(st *appStatus) { st.HeaderCount = 1; st.HeaderHeight = 0; st.TipHash = "stale-status-label" })
	get("sat", 0, genesisHashDisplay)
	a.setStatus(func(st *appStatus) {
		st.HeaderCount, st.HeaderHeight = 0, -1
		st.HeaderTargetHeight, st.HeaderTargetSource = 999999, "untrusted peer estimate"
	})
	if _, err := a.classifyNumberNavigation077(context.Background(), "0"); !errors.Is(err, errNumberNavigationTip077) {
		t.Fatal("unknown selected tip became a block from an advertised target", err)
	}
	w := numberNavigationRequest077(a, "GET", "0")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "N.bitcoin or N.sat") {
		t.Fatal(w.Code, w.Body.String())
	}
	a.setStatus(func(st *appStatus) { st.HeaderCount = 100; st.HeaderHeight = 99 })
	if _, err := a.classifyNumberNavigation077(context.Background(), "0"); !errors.Is(err, errNumberNavigationTip077) {
		t.Fatal("missing selected header was accepted as authority", err)
	}
}

func Test077NumberNavigationCoreAuthorityWithoutRawFetch(t *testing.T) {
	a := numberNavigationApp077(t)
	hash := strings.Repeat("ab", 32)
	settings, calls, stop := satline077RPC(t, map[int64]string{170: hash})
	defer stop()
	// Even the optional Core reachability probe stays inside this fake server.
	if err := os.WriteFile(filepath.Join(settings.BitcoinDataDir, "bitcoin.conf"), []byte(fmt.Sprintf("port=%d\n", settings.RPCPort)), 0600); err != nil {
		t.Fatal(err)
	}
	a.settings = settings
	for _, tc := range []struct{ input, kind string }{{"170", "block"}, {"171", "sat"}} {
		got, err := a.classifyNumberNavigation077(context.Background(), tc.input)
		if err != nil || got.Kind != tc.kind || got.TipHeight != 170 || got.TipHash != hash {
			t.Fatal(got, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("number classification requested raw Core evidence", calls.Load())
	}
}

func Test077NumberNavigationRejectsInvalidAndPrivateRequests(t *testing.T) {
	a := numberNavigationApp077(t)
	for _, input := range []string{"", "-1", "+0", "1.5", "1e3", "x", "１", "18446744073709551616", strings.Repeat("0", 1025)} {
		w := numberNavigationRequest077(a, "GET", input)
		if w.Code != 400 {
			t.Fatalf("%q: %d %s", input, w.Code, w.Body.String())
		}
	}
	if w := numberNavigationRequest077(a, "POST", "0"); w.Code != 405 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, tc := range []struct{ host, remote, origin string }{
		{"untrusted.invalid", "127.0.0.1:1234", ""},
		{"127.0.0.1:9090", "192.0.2.1:1234", ""},
		{"127.0.0.1:9090", "127.0.0.1:1234", "https://untrusted.invalid"},
	} {
		r := httptest.NewRequest("GET", "http://"+tc.host+"/api/v1/navigate/number?number=0", nil)
		r.RemoteAddr = tc.remote
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		a.handleNumberNavigation077(w, r)
		if w.Code != 403 {
			t.Fatal("private classification accepted", tc, w.Code)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.classifyNumberNavigation077(ctx, "0"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.registerGatewayRoutes(mux)
	r := httptest.NewRequest("GET", "http://127.0.0.1:9090/api/v1/navigate/number?number=0", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "0.bitcoin") {
		t.Fatal("registered number route was not read-only GET", w.Code, w.Body.String())
	}
}

func Test077NumberNavigationResolverOverridesAndExistingIdentities(t *testing.T) {
	a := numberNavigationApp077(t)
	raw, hash := installBlockOneHeader063(t, a)
	if err := os.WriteFile(filepath.Join(a.dataDir, "blocks", "raw", "1-"+hash+".block"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.cacheMu.Lock()
	a.cacheIndex.Blocks[hash] = cachedBlockEntry{Height: 1, Hash: hash, Prefix: "1-" + hash}
	a.cacheMu.Unlock()
	// The selected tip is genesis, but explicit block one remains addressable.
	a.setStatus(func(st *appStatus) { st.HeaderHeight = 0 })
	for _, tc := range []struct{ input, kind string }{{"0000", "block"}, {"1", "sat"}, {"0.sat", "sat"}, {"1.bitcoin", "block"}} {
		got, err := a.resolveSearchInputContext(context.Background(), tc.input)
		if err != nil || got.Kind != tc.kind {
			t.Fatal(tc, got, err)
		}
	}
	resolved, err := a.resolveLocalTargetContext(context.Background(), "0001")
	if err != nil || resolved.Target.Friendly != "1.sat" || resolved.Target.Input != "0001" || resolved.Result.Kind != "sat" {
		t.Fatal(resolved, err)
	}
	for _, method := range []string{"navigate", "search"} {
		key := "address"
		if method == "search" {
			key = "query"
		}
		r := httptest.NewRequest("POST", "http://127.0.0.1:9090/api/v1/"+method, strings.NewReader(fmt.Sprintf(`{"%s":"1"}`, key)))
		r.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		if method == "navigate" {
			a.handleNavigate(w, r)
		} else {
			a.handleSearch(w, r)
		}
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"kind":"sat"`) {
			t.Fatal(method, w.Code, w.Body.String())
		}
	}
	key := "02" + strings.Repeat("0", 63) + "1"
	if _, _, _, err := bitcoinDestinationScript(key); err != nil {
		t.Fatal("numeric-only SEC fixture is invalid", err)
	}
	for _, identity := range []string{strings.Repeat("0", 64), key} {
		if bareNumberInput077(identity) {
			t.Fatal("existing hash/public key was captured as a number", identity)
		}
		if w := numberNavigationRequest077(a, "GET", identity); identity == key && w.Code != 400 {
			t.Fatal("explicit number intent failed to reject oversized public-key digits", w.Code)
		}
	}
	a.setStatus(func(st *appStatus) { st.HeaderHeight = -1 })
	got, err := a.resolveSearchInputContext(context.Background(), key)
	if err != nil || got.Kind != "address" {
		t.Fatal("valid numeric public-key lookup required numeric chain authority", got, err)
	}
	if _, err := a.resolveSearchInputContext(context.Background(), "1"); !errors.Is(err, errNumberNavigationTip077) {
		t.Fatal("bare input guessed without a selected tip", err)
	}
	for _, explicit := range []string{"0.sat", "1.bitcoin"} {
		if _, err := a.resolveSearchInputContext(context.Background(), explicit); err != nil {
			t.Fatal("explicit suffix incorrectly needed numeric classification", explicit, err)
		}
	}
	for _, invalid := range []string{"-1", "+0", "18446744073709551616"} {
		if _, err := a.resolveSearchInputContext(context.Background(), invalid); err == nil {
			t.Fatal("invalid integer became a block", invalid)
		}
	}
}

func Test077SatNameResolverReadOnlyAliasesAndExplicitIdentity(t *testing.T) {
	a := numberNavigationApp077(t)
	a.setStatus(func(st *appStatus) { st.HeaderHeight, st.HeaderCount = -1, 0 })
	for _, tc := range []struct {
		input, friendly string
		number          uint64
	}{
		{"nvtdijuwxlp", "nvtdijuwxlp.sat", 0},
		{"nvtdijuwxlp.sat", "nvtdijuwxlp.sat", 0},
		{"NVtdijuwxlp.SAT", "nvtdijuwxlp.sat", 0},
		{"god://satoshi.sat", "satoshi.sat", 2099994106992659},
		{"god://SATOSHI.SAT", "satoshi.sat", 2099994106992659},
		{"satoshi.sat", "satoshi.sat", 2099994106992659},
		{"0.sat", "0.sat", 0},
		{"5000000000.SAT", "5000000000.sat", 5000000000},
	} {
		got, err := a.resolveLocalTargetContext(context.Background(), tc.input)
		if err != nil || got.Target.Kind != "sat" || got.Target.Namespace != "sat" || got.Target.Input != tc.input || got.Target.Friendly != tc.friendly || got.Target.CanonicalURI != "god://"+tc.friendly || got.Result.Kind != "sat" || got.Result.Sat == nil || got.Result.Sat.SatNumber != tc.number {
			t.Fatal(tc, got, err)
		}
		query, err := a.resolveSearchInputContext(context.Background(), tc.input)
		if err != nil || query.Kind != "sat" || query.Query != tc.friendly || query.Sat.SatNumber != tc.number {
			t.Fatal("search disagreed with the direct name resolver", tc, query, err)
		}
	}
	for _, reserved := range []string{"bitcoin", "tip", "latest", "settings.gateway", "170.bitcoin", address076PublicKey, genesisHashDisplay} {
		if bareSatNameInput077(reserved) {
			t.Fatal("existing resource was captured as a Sat name", reserved)
		}
	}
	for _, invalid := range []string{"SATOSHI", "satoshi1.sat", "zzzzzzzzzzz.sat"} {
		if _, err := a.resolveLocalTargetContext(context.Background(), invalid); err == nil {
			t.Fatal("invalid ordinal name became a resource", invalid)
		}
	}
	if a.indexCancel != nil || len(a.satlineJobs) != 0 {
		t.Fatal("Sat name lookup started indexing or tracing")
	}
}
