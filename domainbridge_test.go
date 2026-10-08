package main

import (
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func makeDNSQuery(name string, qtype uint16) []byte {
	p := make([]byte, 12)
	binary.BigEndian.PutUint16(p[0:2], 7)
	binary.BigEndian.PutUint16(p[4:6], 1)
	for _, label := range strings.Split(name, ".") {
		p = append(p, byte(len(label)))
		p = append(p, []byte(label)...)
	}
	p = append(p, 0, byte(qtype>>8), byte(qtype), 0, 1)
	return p
}
func TestBitcoinDNSBridgeAnswersOnlyBitcoin(t *testing.T) {
	r := dnsResponse(makeDNSQuery("2.123.750000.bitcoin", 1))
	if len(r) < 16 || binary.BigEndian.Uint16(r[6:8]) != 1 {
		t.Fatalf("expected A answer")
	}
	if got := r[len(r)-4:]; got[0] != 127 || got[1] != 0 || got[2] != 0 || got[3] != 1 {
		t.Fatalf("unexpected A answer %v", got)
	}
	r = dnsResponse(makeDNSQuery("example.com", 1))
	if binary.BigEndian.Uint16(r[2:4])&0xf != 5 {
		t.Fatalf("expected REFUSED for non-bitcoin")
	}
}
func TestBitcoinHTTPBridgeKeepsFriendlyHost(t *testing.T) {
	var gotHost, gotResolve string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Header.Get("X-Gateway-On-Demand-Host")
		gotResolve = r.URL.Query().Get("resolve")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()
	h, err := bitcoinProxyHandler(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://0.bitcoin/", nil)
	req.Host = "0.bitcoin"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 || strings.TrimSpace(rr.Body.String()) != "ok" {
		t.Fatalf("proxy status/body: %d %q", rr.Code, rr.Body.String())
	}
	if gotHost != "0.bitcoin" || gotResolve != "0.bitcoin" {
		t.Fatalf("host=%q resolve=%q", gotHost, gotResolve)
	}
}
