package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func Test050UnifiedAddresses(t *testing.T) {
	id := strings.Repeat("ab", 32)
	cases := map[string]string{".gateway": ".gateway", "god://.gateway/": ".gateway", "http://home.gateway/": ".gateway", "peers.gateway": "peers.gateway", "god://0.bitcoin/": "0.bitcoin", "0.bitcoin/": "0.bitcoin", id + ":2": id + ":2", id + ":2:500": id + ":2:500", id + "i0": id + "i0", "god://open.gateway/?address=" + url.QueryEscape(id+":2:500"): id + ":2:500"}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			x, e := parseLocalResolverTarget(input)
			if e != nil || x.Friendly != want {
				t.Fatalf("got=%+v err=%v want=%s", x, e, want)
			}
		})
	}
	for _, input := range []string{"god://user@0.bitcoin/", "god://0.bitcoin/else", "god://0.bitcoin:80/", "god://open.gateway/?address=https%3A%2F%2Fevil.com", "what is 0.bitcoin", id + ":4294967296", id + ":0:18446744073709551616", "god://0.bitcoin/#x"} {
		if _, e := parseLocalResolverTarget(input); e == nil {
			t.Fatalf("accepted malformed address %s", input)
		}
	}
}
func Test050RawOutpointSatpointResolution(t *testing.T) {
	a, v := initGraphTestApp(t)
	target, e := a.localBlockTarget(0)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.fetchAndDecodeTarget(target); e != nil {
		t.Fatal(e)
	}
	store, e := openIndexStore(a.dataDir, "blocks")
	if e != nil {
		t.Fatal(e)
	}
	if e = store.appendBlock(v, 0, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	id := v.Transactions[0].TxID
	for _, q := range []string{id + ":0", id + ":0:0", id + ":0:4999999999"} {
		r, e := a.resolveSearchInput(q)
		if e != nil || r.Tx == nil {
			t.Fatalf("%s: %+v %v", q, r, e)
		}
	}
	for _, q := range []string{id + ":1", id + ":0:5000000000"} {
		if _, e = a.resolveSearchInput(q); e == nil {
			t.Fatal("bounds not checked", q)
		}
	}
}
func Test050ActivationBootstrapCarriesActualTarget(t *testing.T) {
	a := &app{}
	x, e := a.createActivation("god://0.bitcoin/")
	if e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{"http://127.0.0.1/?activation=" + x.ID, "http://127.0.0.1/?resolve=0.bitcoin", "http://0.bitcoin/"} {
		r := httptest.NewRequest("GET", q, nil)
		w := httptest.NewRecorder()
		a.serveGatewayShell(w, r)
		s := w.Body.String()
		if !strings.Contains(s, `"address":"0.bitcoin"`) {
			t.Fatal("address lost in delivered bootstrap", q)
		}
		if !strings.Contains(s, "gateway-bootstrap") {
			t.Fatal("missing inert bootstrap")
		}
	}
	b := []byte(fmt.Sprintf(`{"id":%q,"stage":"displayed"}`, x.ID))
	r := httptest.NewRequest("POST", "http://127.0.0.1/api/v1/activation", bytes.NewReader(b))
	w := httptest.NewRecorder()
	a.handleActivation(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if a.activations[x.ID].Stage != "displayed" {
		t.Fatal("display acknowledgement lost")
	}
}
func Test050BootstrapEscapesMarkup(t *testing.T) {
	a := &app{}
	r := httptest.NewRequest("GET", "http://127.0.0.1/?resolve="+url.QueryEscape(`</script><script>alert(1)</script>`), nil)
	w := httptest.NewRecorder()
	a.serveGatewayShell(w, r)
	if strings.Contains(w.Body.String(), "</script><script>alert") {
		t.Fatal("bootstrap injection")
	}
}
func Test050ManagementOriginIsolation(t *testing.T) {
	cases := []struct {
		host, origin, site, remote string
		want                       bool
	}{
		{"127.0.0.1:9090", "http://127.0.0.1:9090", "same-origin", "127.0.0.1:50000", true},
		{"0.bitcoin", "http://0.bitcoin", "same-origin", "127.0.0.1:50000", true},
		{"127.0.0.1:9090", "http://127.0.0.1:9091", "same-site", "127.0.0.1:50000", false},
		{"127.0.0.1:9090", "null", "cross-site", "127.0.0.1:50000", false},
		{"127.0.0.1:9090", "http://evil.example", "cross-site", "127.0.0.1:50000", false},
		{"evil.example", "", "none", "127.0.0.1:50000", false},
		{"127.0.0.1:9090", "", "none", "192.0.2.1:50000", false},
	}
	for i, c := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://"+c.host+"/api/v1/settings", nil)
			r.RemoteAddr = c.remote
			r.Header.Set("Origin", c.origin)
			r.Header.Set("Sec-Fetch-Site", c.site)
			if safeLocalRequest(r) != c.want {
				t.Fatalf("origin policy mismatch: %+v", c)
			}
		})
	}
}
func Test050ManagementWritesRequireJSON(t *testing.T) {
	a := &app{}
	handler := a.guardGateway(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("POST", "http://127.0.0.1/api/v1/jobs", strings.NewReader("action=cancel"))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatalf("got %d", w.Code)
	}
}
func Test050NativeControlRequiresToken(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	r := httptest.NewRequest("POST", "http://127.0.0.1/api/v1/native", strings.NewReader(`{"action":"open","address":".gateway"}`))
	w := httptest.NewRecorder()
	a.handleNativeControl(w, r)
	if w.Code != 403 {
		t.Fatal("unauthed native operation accepted")
	}
}
func Test050CoalescedReadsAndSummaryAreImmutable(t *testing.T) {
	a, _ := initGraphTestApp(t)
	target, e := a.localBlockTarget(0)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := a.fetchAndDecodeTarget(target)
			if e == nil && len(v.Transactions[0].Outputs) != 1 {
				e = fmt.Errorf("cached body changed")
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1/api/v1/navigate", strings.NewReader(`{"address":"0.bitcoin"}`))
	w := httptest.NewRecorder()
	a.handleNavigate(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var x map[string]any
	if e = json.Unmarshal(w.Body.Bytes(), &x); e != nil {
		t.Fatal(e)
	}
	v, e := a.fetchAndDecodeTarget(target)
	if e != nil || len(v.Transactions[0].Outputs) != 1 || len(v.Transactions[0].Inputs) != 1 {
		t.Fatal("summary mutated immutable decoded cache")
	}
}

func Test050SatlineFramePreservesFriendlyOrigin(t *testing.T) {
	a := &app{runtimeURL: "http://127.0.0.1:19999"}
	for _, host := range []string{"0.bitcoin", "home.gateway", "127.0.0.1:19999"} {
		r := httptest.NewRequest("GET", "http://"+host+"/satline?embedded=1", nil)
		w := httptest.NewRecorder()
		a.handleSatlinePage(w, r)
		if w.Code != 200 || w.Header().Get("Location") != "" || !strings.Contains(w.Body.String(), "Satline") {
			t.Fatalf("module redirected away from parent origin %s: %d %s", host, w.Code, w.Header().Get("Location"))
		}
	}
}
