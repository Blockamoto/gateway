package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This fixture deliberately uses decoded synthetic blocks, not a consensus
// network. It tests Satline's trust boundary against an independent local data
// provider, while the existing real-genesis tests cover Bitcoin parsing.
func satlinePeerFixture(t *testing.T) (*memorySatlineBackend, satlineQuery, satlineResult) {
	t.Helper()
	a, b, c, d := testID(6001), testID(6002), testID(6003), testID(6004)
	b0 := testBlock(0, testCoinbase(a, 1000, 200))
	tx1 := testTx(b, []inputView{{PrevTxID: a, PrevVout: 0}}, 100, 900)
	b1 := testBlock(1, testCoinbase(testID(6011), subsidyAtHeight(1)), tx1)
	// Earlier transaction pays a 10-sat fee. The target in tx2's second input
	// lands in its fee tail, exercising ordered inputs, fees, and coinbase.
	earlier := testTx(c, []inputView{{PrevTxID: a, PrevVout: 1}}, 190)
	tx2 := testTx(d, []inputView{{PrevTxID: b, PrevVout: 0}, {PrevTxID: b, PrevVout: 1}}, 990)
	b2 := testBlock(2, testCoinbase(testID(6012), subsidyAtHeight(2)+20), earlier, tx2)
	backend := &memorySatlineBackend{blocks: map[int64]blockView{0: b0, 1: b1, 2: b2}, tip: 2}
	out := newSatlineResolver(backend).resolveSat(995, 0)
	if len(out.Hops) != 2 || out.Hops[1].Type != "fee_to_coinbase" || out.Hops[1].PriorBlockFeesSats != 10 {
		t.Fatalf("bad fixture %+v", out)
	}
	return backend, satlineQuery{Kind: "sat", Input: "995"}, out
}
func fixtureCanonical(b *memorySatlineBackend) func(int64) (string, error) {
	return func(h int64) (string, error) { v, e := b.BlockByHeight(h); return v.Hash, e }
}
func segmentFixture(q satlineQuery, r satlineResult) satlineSegment {
	return satlineSegment{Query: q, Start: *r.BirthSatpoint, Hops: append([]satlineHop(nil), r.Hops...), Next: len(r.Hops)}
}
func TestSatlinePeerRecomputesOrdinaryAndFeeHops(t *testing.T) {
	b, q, r := satlinePeerFixture(t)
	s := segmentFixture(q, r)
	if path := os.Getenv("GATEWAY_UI_FIXTURE"); path != "" {
		raw, _ := json.MarshalIndent(r, "", "  ")
		if e := os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	// Labels supplied by a peer cannot upgrade the locally anchored evidence.
	for i := range s.Hops {
		s.Hops[i].VerificationState = "consensus_validated"
		s.Hops[i].SpenderProvider = "trust-me"
	}
	got, reads, e := verifySatlineSegment(context.Background(), b, fixtureCanonical(b), *r.BirthSatpoint, s)
	if e != nil {
		t.Fatal(e)
	}
	if got.State != "UNRESOLVED" || got.Snapshot != nil || got.VerificationState != "header_anchored" || reads == 0 || len(got.Hops) != 2 {
		t.Fatalf("unsafe result %+v reads=%d", got, reads)
	}
	if !sameSatlinePoint(*got.CurrentSatpoint, *r.CurrentSatpoint) {
		t.Fatal("wrong final satpoint")
	}
}
func TestSatlinePeerRejectsForgedClaims(t *testing.T) {
	cases := map[string]func(*satlineSegment){
		"offset-in-real-block":    func(s *satlineSegment) { s.Hops[0].Destination.Offset++ },
		"input-position":          func(s *satlineSegment) { s.Hops[0].InputStreamPosition++ },
		"wrong-input":             func(s *satlineSegment) { s.Hops[1].SpendingVin = 0 },
		"earlier-fees":            func(s *satlineSegment) { s.Hops[1].PriorBlockFeesSats++ },
		"coinbase-position":       func(s *satlineSegment) { s.Hops[1].CoinbaseStreamOffset++ },
		"disconnected-prefix":     func(s *satlineSegment) { s.Hops[0].Source.Offset-- },
		"disconnected-second-hop": func(s *satlineSegment) { s.Hops[1].Source.Offset-- },
		"false-block":             func(s *satlineSegment) { s.Hops[0].BlockHash = testID(99) },
		"missing-tx":              func(s *satlineSegment) { s.Hops[0].SpendingTxID = testID(99) },
		"wrong-type":              func(s *satlineSegment) { s.Hops[0].Type = "fee_to_coinbase" },
		"empty-segment":           func(s *satlineSegment) { s.Hops = nil },
		"too-many-hops":           func(s *satlineSegment) { s.Hops = make([]satlineHop, maxSatlineSegmentHops+1) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			b, q, r := satlinePeerFixture(t)
			s := segmentFixture(q, r)
			mutate(&s)
			if _, _, e := verifySatlineSegment(context.Background(), b, fixtureCanonical(b), *r.BirthSatpoint, s); e == nil {
				t.Fatal("forged claim accepted")
			}
		})
	}
}
func TestSatlinePeerRejectsUnavailableOrStaleEvidence(t *testing.T) {
	for _, mode := range []string{"missing", "unanchored", "stale", "cancelled", "reorg-during-commit"} {
		t.Run(mode, func(t *testing.T) {
			b, q, r := satlinePeerFixture(t)
			s := segmentFixture(q, r)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			canonical := fixtureCanonical(b)
			switch mode {
			case "missing":
				delete(b.blocks, 1)
			case "unanchored":
				v := b.blocks[1]
				v.VerificationState = "received"
				b.blocks[1] = v
			case "stale":
				canonical = func(h int64) (string, error) {
					if h == 1 {
						return testID(55), nil
					}
					return fixtureCanonical(b)(h)
				}
			case "cancelled":
				cancel()
			case "reorg-during-commit":
				seen := 0
				canonical = func(h int64) (string, error) {
					if h == 2 {
						seen++
						if seen > 1 {
							return testID(55), nil
						}
					}
					return fixtureCanonical(b)(h)
				}
			}
			if _, _, e := verifySatlineSegment(ctx, b, canonical, *r.BirthSatpoint, s); e == nil {
				t.Fatal("untrusted evidence accepted")
			}
		})
	}
}
func TestSatlineWireRejectsInjectedStateAndOversize(t *testing.T) {
	var env satlineEnvelope
	if strictSatlineJSON([]byte(`{"wire":1,"confirmed_state":"UNSPENT_AT_SNAPSHOT"}`), &env) == nil {
		t.Fatal("unspecified state accepted")
	}
	if strictSatlineJSON([]byte(`{"wire":1}{"wire":1}`), &env) == nil {
		t.Fatal("trailing JSON accepted")
	}
	var header [24]byte
	copy(header[:4], mainnetMagic[:])
	copy(header[4:16], "satmsg")
	binary.LittleEndian.PutUint32(header[16:20], maxSatlineWireBytes+1)
	if _, e := readMessage(bytes.NewReader(header[:])); e == nil || !strings.Contains(e.Error(), "too large") {
		t.Fatalf("oversize must fail before payload allocation: %v", e)
	}
}
func TestSatlinePrivacyFlagsAndImmutablePublication(t *testing.T) {
	b, q, r := satlinePeerFixture(t)
	a := &app{dataDir: t.TempDir(), settings: defaultSettings()}
	a.settings.SatlineEnabled = true
	a.initSatlineStore()
	if a.satlineNetworkingEnabled() {
		t.Fatal("networking is on by default")
	}
	if e := a.saveSatlineRecord(q.Kind, q.key(), q.Input, r); e != nil {
		t.Fatal(e)
	}
	req := satlineSegmentRequest{Query: q, Start: *r.BirthSatpoint, Limit: 32}
	a.settings.SatlineServePublished = true
	if _, e := a.readSatlineSegment(req); e == nil || e.Error() != "not_cached" {
		t.Fatalf("private cache was served: %v", e)
	}
	// Publication API ancestry is separately tested with actual genesis headers.
	publication := satlinePublication{VerifierVersion: blockVerifierVersion, Schema: 1, Query: q, Start: *r.BirthSatpoint, Hops: r.Hops[:1]}
	if e := atomicWriteJSON(a.satlinePublicationPath(q), publication); e != nil {
		t.Fatal(e)
	}
	if e := a.saveSatlineRecord(q.Kind, q.key(), q.Input, r); e != nil {
		t.Fatal(e)
	}
	seg, e := a.readSatlineSegment(req)
	if e != nil || len(seg.Hops) != 1 || seg.More {
		t.Fatalf("private extension leaked: %+v %v", seg, e)
	}
	if _, _, e = verifySatlineSegment(context.Background(), b, fixtureCanonical(b), *r.BirthSatpoint, seg); e != nil {
		t.Fatal(e)
	}
	a.settings.SatlineEnabled = false
	if _, e = a.readSatlineSegment(req); e == nil {
		t.Fatal("disabled module served")
	}
	a.settings.SatlineEnabled = true
	if e = a.storeSatlinePublication(q, false); e != nil {
		t.Fatal(e)
	}
	if _, e = a.readSatlineSegment(req); e == nil {
		t.Fatal("withdrawn publication served")
	}
}
func TestSatlineExplicitPublicationAndStepStart(t *testing.T) {
	if !releaseFeatureAvailable("satline") {
		t.Skip("0.6.6 release lock: interactive Satline traversal and publication are unavailable")
	}
	a, g := initGraphTestApp(t)
	a.settings.SatlineEnabled = true
	a.initSatlineStore()
	if e := a.graphIndexBlock(g, true); e != nil {
		t.Fatal(e)
	}
	q := satlineQuery{Kind: "sat", Input: "0"}
	r := a.runSatlineLocal(context.Background(), q, "start", 1, nil)
	if len(r.Hops) != 0 || r.BirthSatpoint == nil || r.State != "STEP_LIMIT" {
		t.Fatalf("start advanced graph: %+v", r)
	}
	if e := a.publishSatlineRecord(q, true); e != nil {
		t.Fatal(e)
	}
	if _, e := a.loadSatlinePublication(q); e != nil {
		t.Fatal(e)
	}
	r = a.runSatlineLocal(context.Background(), q, "next", 1, nil)
	if r.State != "CURRENTLY_UNSPENT" {
		t.Fatalf("next failed: %+v", r)
	}
	if e := a.removeSatlineRecord(q.Kind, q.key()); e != nil {
		t.Fatal(e)
	}
	if _, e := a.loadSatlinePublication(q); !os.IsNotExist(e) {
		t.Fatalf("removed record publication survives %v", e)
	}
}
func TestSatlineGatewayAdvertisementStaysLocked(t *testing.T) {
	a := &app{settings: defaultSettings()}
	has := func() bool {
		_, ok := protocolVersionSupported(a.localGatewayProtocols(), satlineProtocolID, 1, 1)
		return ok
	}
	if has() {
		t.Fatal("default advertises")
	}
	a.settings.SatlineUsePeers = true
	if has() {
		t.Fatal("saved receiving setting bypassed release lock")
	}
	a.settings.SatlineUsePeers = false
	a.settings.SatlineServePublished = true
	if has() {
		t.Fatal("saved serving setting bypassed release lock")
	}
	a.settings.SatlineEnabled = false
	if has() {
		t.Fatal("disabled module advertises")
	}
}
func TestSatlineRateBudget(t *testing.T) {
	a := &app{}
	for i := 0; i < 8; i++ {
		if !a.satlineRateAllowed("127.0.0.1:1234") {
			t.Fatal("early rate rejection")
		}
	}
	if a.satlineRateAllowed("127.0.0.1:1234") {
		t.Fatal("rate burst exceeded")
	}
}
func TestSatlinePrivateAPIBoundary(t *testing.T) {
	a := &app{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	for _, tc := range []struct {
		name, host, remote, origin, site, method, ctype string
		want                                            int
	}{
		{"local", "127.0.0.1:18000", "127.0.0.1:1111", "", "same-origin", "GET", "", 204},
		{"trusted-gateway-resource-origin", "0.bitcoin", "127.0.0.1:1111", "", "same-origin", "GET", "", 204},
		{"remote", "127.0.0.1:18000", "192.0.2.5:1111", "", "", "GET", "", 403},
		{"csrf", "127.0.0.1:18000", "127.0.0.1:1111", "https://evil.example", "cross-site", "POST", "application/json", 403},
		{"form", "127.0.0.1:18000", "127.0.0.1:1111", "", "same-origin", "POST", "text/plain", 415},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://"+tc.host+"/api/satline/ui/records", nil)
			r.RemoteAddr = tc.remote
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Sec-Fetch-Site", tc.site)
			r.Header.Set("Content-Type", tc.ctype)
			w := httptest.NewRecorder()
			a.guardSatlineAPI(next).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d", w.Code, tc.want)
			}
		})
	}
}
func TestSatlineEmbeddedUIAssets(t *testing.T) {
	a := &app{}
	for _, path := range []string{"/satline", "/satline/app.js", "/satline/style.css"} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:9999"+path, nil)
		r.RemoteAddr = "127.0.0.1:3333"
		w := httptest.NewRecorder()
		a.handleSatlinePage(w, r)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Satline locked") || strings.Contains(w.Body.String(), "<script") {
			t.Fatalf("locked UI was exposed %s %d", path, w.Code)
		}
	}
}

// The next two tests launch distinct OS processes with separate data stores.
// The source uses the production Gateway handshake and cache-only wire server;
// the receiver uses the production request parser, verifier and store. Only
// Bitcoin evidence comes from the deterministic decoded-block fixture.
func TestSatlineTwoProcessExchange(t *testing.T) {
	if !releaseFeatureAvailable("satline") || !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("0.6.6 release lock: Satline network exchange is unavailable")
	}
	if os.Getenv("GATEWAY_SATLINE_HELPER") != "" {
		t.Skip("parent only")
	}
	root := t.TempDir()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	server := exec.CommandContext(ctx, exe, "-test.run=^TestSatlineProcessHelper$", "-test.v")
	var serverLog bytes.Buffer
	server.Stdout = &serverLog
	server.Stderr = &serverLog
	server.Env = append(os.Environ(), "GATEWAY_SATLINE_HELPER=serve", "GATEWAY_SATLINE_ROOT="+root)
	if e = server.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = server.Process.Kill(); _ = server.Wait() }()
	ready := filepath.Join(root, "ready")
	for i := 0; i < 200; i++ {
		if _, e = os.Stat(ready); e == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if _, e = os.Stat(ready); e != nil {
		t.Fatalf("source failed to start: %v", e)
	}
	recv := exec.CommandContext(ctx, exe, "-test.run=^TestSatlineProcessHelper$", "-test.v")
	recv.Env = append(os.Environ(), "GATEWAY_SATLINE_HELPER=receive", "GATEWAY_SATLINE_ROOT="+root)
	output, e := recv.CombinedOutput()
	if e != nil {
		t.Fatalf("receiver failed: %v\n%s", e, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}
func TestSatlineProcessHelper(t *testing.T) {
	mode := os.Getenv("GATEWAY_SATLINE_HELPER")
	if mode == "" {
		t.Skip("subprocess fixture")
	}
	root := os.Getenv("GATEWAY_SATLINE_ROOT")
	b, q, r := satlinePeerFixture(t)
	a := &app{dataDir: filepath.Join(root, mode), settings: defaultSettings(), gatewayListen: "127.0.0.1:0"}
	a.settings.ServeData = false
	a.initSatlineStore()
	a.source = newOverlayServer(a)
	if mode == "serve" {
		a.settings.SatlineServePublished = true
		p := satlinePublication{VerifierVersion: blockVerifierVersion, Schema: 1, Query: q, Start: *r.BirthSatpoint, Hops: r.Hops}
		if e := atomicWriteJSON(a.satlinePublicationPath(q), p); e != nil {
			t.Fatal(e)
		}
		if e := a.source.start(); e != nil {
			t.Fatal(e)
		}
		defer a.source.stopServer()
		if a.source.bitcoinServingStatus().Enabled {
			t.Fatal("Satline serving enabled Bitcoin serving")
		}
		a.source.mu.Lock()
		addr := a.source.tcp.Addr().String()
		a.source.mu.Unlock()
		if e := os.WriteFile(filepath.Join(root, "ready"), []byte(addr), 0600); e != nil {
			t.Fatal(e)
		}
		time.Sleep(24 * time.Second)
		return
	}
	a.settings.SatlineUsePeers = true
	address, e := os.ReadFile(filepath.Join(root, "ready"))
	if e != nil {
		t.Fatal(e)
	}
	req := satlineSegmentRequest{Query: q, Start: *r.BirthSatpoint, Limit: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	first, e := querySatlinePeer(ctx, string(address), req, a.localGatewayProtocols())
	if e != nil {
		t.Fatal(e)
	}
	if !first.More || len(first.Hops) != 1 {
		t.Fatal("segmentation not respected")
	}
	checked, _, e := verifySatlineSegment(ctx, b, fixtureCanonical(b), req.Start, first)
	if e != nil {
		t.Fatal(e)
	}
	base := newSatlineResolver(b).resolveSat(995, -1)
	combined := combineSatlineResult(base, base.Hops, checked)
	if e = a.saveSatlineRecord(q.Kind, q.key(), q.Input, combined); e != nil {
		t.Fatal(e)
	}
	// Restart the receiver's storage owner before requesting the continuation.
	resumed := &app{dataDir: a.dataDir, settings: a.settings}
	resumed.initSatlineStore()
	saved, ok := resumed.loadSatlineRecord(q.Kind, q.key())
	if !ok || len(saved.Result.Hops) != 1 {
		t.Fatal("restart lost accepted segment")
	}
	req.After = first.Next
	req.Start = *saved.Result.CurrentSatpoint
	second, e := querySatlinePeer(ctx, string(address), req, a.localGatewayProtocols())
	if e != nil {
		t.Fatal(e)
	}
	checked, _, e = verifySatlineSegment(ctx, b, fixtureCanonical(b), req.Start, second)
	if e != nil {
		t.Fatal(e)
	}
	result := combineSatlineResult(saved.Result, saved.Result.Hops, checked)
	if e = resumed.saveSatlineRecord(q.Kind, q.key(), q.Input, result); e != nil {
		t.Fatal(e)
	}
	if len(result.Hops) != 2 || result.State != "UNRESOLVED" || !sameSatlinePoint(*result.CurrentSatpoint, *r.CurrentSatpoint) {
		t.Fatalf("wrong accepted result %+v", result)
	}
	if entries, _ := os.ReadDir(resumed.satlinePublicRoot()); len(entries) != 0 {
		t.Fatal("received data automatically published")
	}
	// A different query is a private/non-published miss, not an unspent answer.
	req.Query = satlineQuery{Kind: "sat", Input: "994"}
	req.After = 0
	req.Start = *r.BirthSatpoint
	if _, e = querySatlinePeer(ctx, string(address), req, a.localGatewayProtocols()); e == nil || !strings.Contains(e.Error(), "not_cached") {
		t.Fatalf("private query response %v", e)
	}
	t.Log("Two separate OS processes: Gateway negotiation, two bounded segments, local FIFO verification, restart reuse and no auto-publication passed.")
}

func TestSatlinePublicationNoLocalMetadata(t *testing.T) {
	_, q, r := satlinePeerFixture(t)
	h := sanitizedSatlineHop(r.Hops[0])
	b, _ := json.Marshal(satlinePublication{VerifierVersion: blockVerifierVersion, Schema: 1, Query: q, Start: *r.BirthSatpoint, Hops: []satlineHop{h}})
	for _, bad := range []string{"created_at", "updated_at", "trust-me", "SpenderProvider"} {
		if bytes.Contains(b, []byte(bad)) {
			t.Fatalf("local metadata leak %s", bad)
		}
	}
}
func TestSatlineWorkBudget(t *testing.T) {
	b, _, _ := satlinePeerFixture(t)
	g := &satlineVerifyBackend{base: b, ctx: context.Background(), reads: maxSatlineEvidenceReads}
	if e := g.tick(); e == nil {
		t.Fatal("unbounded verification")
	}
}
func TestSatlineStepBoundaryOneLogicalHop(t *testing.T) {
	b, _, r := satlinePeerFixture(t)
	engine := newSatlineResolver(b)
	start := engine.resolveSat(995, -1)
	if len(start.Hops) != 0 {
		t.Fatal("start performed a spend")
	}
	one := engine.traverse(*start.CurrentSatpoint, 1)
	if len(one.Hops) != 1 || one.State != "STEP_LIMIT" {
		t.Fatalf("step: %+v", one)
	}
	two := engine.traverse(*one.CurrentSatpoint, 1)
	if len(two.Hops) != 1 || two.Hops[0].Type != "fee_to_coinbase" || !sameSatlinePoint(*two.CurrentSatpoint, *r.CurrentSatpoint) {
		t.Fatal("fee must be one logical step")
	}
}
