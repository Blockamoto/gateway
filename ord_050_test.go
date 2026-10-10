package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func ordScript050(parts ...[]byte) []byte {
	s := []byte{0, 0x63, 3, 'o', 'r', 'd'}
	for _, b := range parts {
		if len(b) <= 75 {
			s = append(s, byte(len(b)))
		} else {
			s = append(s, 76, byte(len(b)))
		}
		s = append(s, b...)
	}
	return append(s, 0x68)
}
func ordWitness050(script []byte) []string {
	control := append([]byte{0xc0}, make([]byte, 32)...)
	return []string{hex.EncodeToString(script), hex.EncodeToString(control)}
}
func Test050OrdEnvelopeOrderAndBody(t *testing.T) {
	one := ordScript050([]byte{1}, []byte("text/plain"), nil, []byte("hello"))
	two := ordScript050(nil, []byte("second"))
	tx := transactionView{Inputs: []inputView{{Witness: ordWitness050(append(one, two...))}, {Witness: ordWitness050(two)}}}
	e := parseOrdEnvelopes(tx)
	if len(e) != 3 || e[0].ContentType != "text/plain" || string(e[0].Body) != "hello" || e[1].Offset != 1 || e[2].Input != 1 || e[2].Offset != 0 {
		t.Fatalf("bad envelope extraction %+v", e)
	}
}
func Test050OrdPinnedWitnessSelectionAndMalformedScript(t *testing.T) {
	script := ordScript050(nil, []byte("body"))
	// The pinned reference recognizes unversioned witness leaves, rather than
	// assuming a P2TR prevout or a c0 control block from one reveal transaction.
	if len(parseOrdEnvelopes(transactionView{Inputs: []inputView{{Witness: []string{hex.EncodeToString(script), "00"}}}})) != 1 {
		t.Fatal("pinned unversioned witness leaf was rejected")
	}
	cases := [][]string{{hex.EncodeToString(append(script, 0x4c)), hex.EncodeToString(append([]byte{0xc0}, make([]byte, 32)...))}, {hex.EncodeToString(script)}}
	for _, w := range cases {
		if len(parseOrdEnvelopes(transactionView{Inputs: []inputView{{Witness: w}}})) != 0 {
			t.Fatal("accepted non tapscript or malformed script")
		}
	}
}
func Test050OrdAnnexAndPointer(t *testing.T) {
	script := ordScript050([]byte{2}, []byte{0xf4, 1}, nil, []byte("x"))
	w := append(ordWitness050(script), "5001")
	e := parseOrdEnvelopes(transactionView{Inputs: []inputView{{Witness: w}}})
	if len(e) != 1 || e[0].Pointer == nil || *e[0].Pointer != 500 {
		t.Fatal("annex/pointer", e)
	}
}
func Test050OrdFieldFlagsAndBodyDelimiter(t *testing.T) {
	e := parseOrdFields([][]byte{{1}, {}, {1}, []byte("text/plain"), {4}, {1}, {}, []byte("body")})
	if !e.Duplicate || !e.Unbound || string(e.Body) != "body" || e.ContentType != "" {
		t.Fatalf("field interpretation %+v", e)
	}
	e = parseOrdFields([][]byte{{1}})
	if !e.Incomplete {
		t.Fatal("incomplete field not detected")
	}
}
func Test050OrdReferenceAndPointerBounds(t *testing.T) {
	hash := strings.Repeat("12", 32)
	raw, _ := displayHashRaw(hash)
	b := append(raw[:], byte(3))
	if got := decodeInscriptionReference(b); got != hash+"i3" {
		t.Fatal(got)
	}
	if decodeInscriptionReference(append(raw[:], 0)) != "" {
		t.Fatal("nonminimal zero index accepted")
	}
	if n, ok := ordPointer([]byte{1, 0, 0, 0, 0, 0, 0, 0, 0}); !ok || n != 1 {
		t.Fatal("trailing zero pointer")
	}
	if _, ok := ordPointer([]byte{0, 0, 0, 0, 0, 0, 0, 0, 1}); ok {
		t.Fatal("pointer overflow")
	}
}
func Test050OrdAdapterURLRestricted(t *testing.T) {
	for _, s := range []string{"http://example.org:80", "file:///etc/passwd", "http://user@localhost:8000", "http://localhost:8000/?x=1", "http://localhost:8000/#x"} {
		if _, e := validatedOrdURL(s); e == nil {
			t.Fatal("unsafe adapter URL", s)
		}
	}
	for _, s := range []string{"http://127.0.0.1:8000", "http://localhost:8000"} {
		if _, e := validatedOrdURL(s); e != nil {
			t.Fatal(e)
		}
	}
}
func saveOrdFixture050(t *testing.T, a *app, id, delegate string, body []byte) {
	t.Helper()
	h := sha256.Sum256(body)
	r := ordRecord{Schema: 1, VerifierVersion: blockVerifierVersion, ID: id, Envelope: ordEnvelope{ContentType: "text/html", Delegate: delegate, HasBody: true}, SHA256: hex.EncodeToString(h[:]), Size: len(body), Evidence: "provider_reported", Provider: "local_ord_adapter", Height: -1}
	if e := a.saveOrdRecord(r, body); e != nil {
		t.Fatal(e)
	}
}
func Test050OrdContentOriginIsReadOnlySandboxed(t *testing.T) {
	if !releaseFeatureAvailable("inscriptions") {
		t.Skip("0.6.6 release lock: inscription content origin is unavailable")
	}
	a := ordContentSecurityApp(t)
	id := strings.Repeat("ab", 32) + "i0"
	body := []byte(`<script>window.parent.postMessage('test','*')</script>`)
	saveOrdFixture050(t, a, id, "", body)
	r := ordContentSameOriginRequest(a, "GET", "/content/"+id)
	w := httptest.NewRecorder()
	a.serveOrdContent(w, r)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox allow-scripts allow-same-origin") || w.Header().Get("Access-Control-Allow-Origin") != a.contentURL {
		t.Fatal("unsafe content preview", w.Code, w.Header())
	}
	for _, path := range []string{"/api/v1/settings", "/content/../../settings.json", "/api/v1/migration"} {
		w := httptest.NewRecorder()
		a.serveOrdContent(w, httptest.NewRequest("GET", "http://127.0.0.1"+path, nil))
		if w.Code != 404 {
			t.Fatal("management route at content origin", path)
		}
	}
	w = httptest.NewRecorder()
	a.serveOrdContent(w, httptest.NewRequest("POST", "http://127.0.0.1/content/"+id, nil))
	if w.Code != 405 {
		t.Fatal("content origin accepts writes")
	}
}
func Test050OrdDelegateCycleAndCorruptBody(t *testing.T) {
	a := ordContentSecurityApp(t)
	one := strings.Repeat("aa", 32) + "i0"
	two := strings.Repeat("bb", 32) + "i0"
	saveOrdFixture050(t, a, one, two, []byte("a"))
	saveOrdFixture050(t, a, two, one, []byte("b"))
	t.Run("delegate_cycle", func(t *testing.T) {
		if !releaseFeatureAvailable("inscriptions") {
			t.Skip("0.6.6 release lock: delegated content serving is unavailable")
		}
		w := httptest.NewRecorder()
		a.serveOrdContent(w, ordContentSameOriginRequest(a, "GET", "/content/"+one))
		if w.Code != http.StatusConflict {
			t.Fatal("cycle not bounded", w.Code)
		}
	})
	if !releaseFeatureAvailable("inscriptions") {
		w := httptest.NewRecorder()
		a.serveOrdContent(w, ordContentSameOriginRequest(a, "GET", "/content/"+one))
		if w.Code != http.StatusForbidden {
			t.Fatal("locked inscription content was exposed", w.Code)
		}
	}
	if e := os.WriteFile(a.ordPath(one, ".bin"), []byte("altered"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e := a.loadOrdRecord(one); e == nil {
		t.Fatal("corrupted content accepted")
	}
}
func Test050OrdAdapterAnswersRemainProviderReported(t *testing.T) {
	id := strings.Repeat("aa", 32) + "i0"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/inscription/") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"id": id, "number": 123})
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "local ord fixture")
	}))
	defer srv.Close()
	a := &app{dataDir: t.TempDir(), settings: appSettings{OrdEnabled: true, OrdURL: srv.URL}}
	r, e := a.resolveOrdAdapter(context.Background(), id, fmt.Errorf("no witness data"))
	if e != nil || r.Evidence != "provider_reported" || r.Provider != "local_ord_adapter" || r.Height != -1 {
		t.Fatalf("provider promotion %+v %v", r, e)
	}
}
