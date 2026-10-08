package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Bitcoin mainnet block 1 (215 bytes), independently checked by the production
// parser. Wire fixture cross-reference: btcsuite/btcd/wire/msgblock_test.go.
// The tests below use local sockets, not a connection to public mainnet peers.
func mainnetBlockOne(t *testing.T) []byte {
	t.Helper()
	raw := "01000000" + "6fe28c0ab6f1b372c1a6a246ae63f74f931e8365e15a089c68d6190000000000" +
		"982051fd1e4ba744bbbe680e1fee14677ba1a3c3540bf7b1cdb606e857233e0e" + "61bc6649ffff001d01e36299" +
		"010100000001" + strings.Repeat("00", 32) + "ffffffff0704ffff001d0104ffffffff0100f2052a010000004341" +
		"0496b538e853519c726a2c91e61ec11600ae1390813a627c66fb8be7947be63c52da7589379515d4e0a604f8141781e62294721166bf621e73a82cbf2342c858ee" + "ac00000000"
	b, e := hex.DecodeString(raw)
	if e != nil {
		t.Fatal(e)
	}
	if len(b) != 215 {
		t.Fatalf("block1 size %d", len(b))
	}
	h := hash256(b[:80])
	if _, e := parseBlockDetailed(-1, reverseHex(h[:]), nil, b, "fixture", false); e != nil {
		t.Fatal(e)
	}
	return b
}
func independentTestApp(t *testing.T) *app {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "headers", "headers.bin")
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := ensureHeaderFile(path); e != nil {
		t.Fatal(e)
	}
	s := defaultSettings()
	s.CoreDisabled = true
	s.CoreMountDisabled = true
	s.BitcoinDataDir = ""
	s.BitcoinBlocksDir = ""
	s.OrdURL = ""
	s.ShareCache = false
	s.PrivacyMode = true
	a := &app{dataDir: dir, headersPath: path, settings: s, cacheIndex: newCacheIndex(), coverage: newCoverageState(), headerWake: make(chan struct{}, 1), gatewayListen: "127.0.0.1:0"}
	if e := os.MkdirAll(filepath.Join(dir, "blocks", "raw"), 0700); e != nil {
		t.Fatal(e)
	}
	a.initCoreBlockStore()
	a.initGraphStore()
	a.initSatlineStore()
	a.primeHeaderStatus()
	a.source = newOverlayServer(a)
	a.network = newBitcoinNetwork(a)
	a.network.noBootstrap = true
	a.network.allowPrivate = true
	t.Cleanup(func() { a.network.stop(); a.source.stopServer() })
	return a
}
func eventually051(t *testing.T, check func() bool) {
	t.Helper()
	end := time.Now().Add(6 * time.Second)
	for time.Now().Before(end) {
		if check() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

// An ordinary protocol-70016 Bitcoin fixture. It does not negotiate Gateway.
// It may gossip a completely separate, initially unknown Gateway endpoint.
func ordinaryFixture051(t *testing.T, block []byte, gossip string) string {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	conns := []net.Conn{}
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(20 * time.Second))
				m, e := readMessage(c)
				if e != nil || m.command != "version" {
					return
				}
				payload := makeVersionPayload(ln.Addr().String(), nodeNetworkService|nodeWitnessService)
				binary.LittleEndian.PutUint32(payload[:4], 70016)
				binary.LittleEndian.PutUint32(payload[len(payload)-5:len(payload)-1], 1)
				if writeMessage(c, "version", payload) != nil {
					return
				}
				if writeMessage(c, "verack", nil) != nil {
					return
				}
				for {
					m, e := readMessage(c)
					if e != nil {
						return
					}
					switch m.command {
					case "feature":
						return // client MUST NOT send this to an older peer.
					case "ping":
						_ = writeMessage(c, "pong", m.payload)
					case "getaddr":
						if gossip != "" {
							_ = writeMessage(c, "addr", encodeAddressList([]advertisedPeer{{Addr: gossip, Services: nodeNetworkService}}))
						}
					case "getheaders":
						out := []byte{0}
						if len(block) > 80 {
							prev := hash256(block[:80])
							if !bytes.Contains(m.payload, prev[:]) {
								out = append([]byte{1}, block[:80]...)
								out = append(out, 0)
							}
						}
						_ = writeMessage(c, "headers", out)
					case "getdata":
						if len(block) > 80 {
							_ = writeMessage(c, "block", block)
						} else {
							_ = writeMessage(c, "notfound", m.payload)
						}
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String()
}
func Test051StandaloneKnownHashBeforeHeadersAndRestart(t *testing.T) {
	block := mainnetBlockOne(t)
	hash := hash256(block[:80])
	display := reverseHex(hash[:])
	addr := ordinaryFixture051(t, block, "")
	a := independentTestApp(t)
	a.network.add(addr, "manual_bitcoin", 0)
	a.network.start()
	eventually051(t, func() bool { return len(a.network.listSessions()) == 1 })
	if _, e := a.resolveBlockTarget("1"); e == nil || !strings.Contains(e.Error(), "WAITING_FOR_HEADERS") {
		t.Fatalf("height without mapping: %v", e)
	}
	v, e := a.fetchAndDecode(display)
	if e != nil {
		t.Fatal(e)
	}
	if v.Hash != display || v.Verification.HeaderChainMatch || v.Verification.ConsensusValidated || v.SourcePeer != addr || v.SourceNetwork != "bitcoin" {
		t.Fatalf("known hash evidence: %+v", v)
	}
	if a.getStatus().HeaderHeight != 0 {
		t.Fatal("hash lookup secretly required header synchronization")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e = a.syncIndependentHeaders(ctx); e != nil {
		t.Fatal(e)
	}
	if st := a.getStatus(); st.HeaderHeight != 1 || st.TipHash != display || st.HeaderSource != addr {
		t.Fatalf("status: %+v", st)
	}
	anchored, e := a.fetchAndDecode("1")
	if e != nil || !anchored.Verification.HeaderChainMatch {
		t.Fatalf("anchor upgrade: %+v %v", anchored, e)
	}
	a.network.persist()
	if e = a.saveCacheIndex(); e != nil {
		t.Fatal(e)
	}
	a.network.stop()
	b := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, cacheIndex: newCacheIndex(), coverage: newCoverageState()}
	b.settings.NetworkDisabled = true
	b.initCoreBlockStore()
	b.initGraphStore()
	b.loadCacheIndex()
	b.primeHeaderStatus()
	b.source = newOverlayServer(b)
	b.network = newBitcoinNetwork(b)
	defer b.network.stop()
	if b.getStatus().HeaderHeight != 1 || len(b.network.addresses) == 0 {
		t.Fatal("restart lost headers or saved addresses")
	}
	again, e := b.fetchAndDecode("1")
	if e != nil || !again.FromCache || again.SourceNetwork != "cache" || !again.Verification.HeaderChainMatch {
		t.Fatalf("offline reuse: %+v %v", again, e)
	}
	t.Logf("Core disabled, no mount: ordinary peer %s supplied block1 %s before headers; header synchronization and offline cache restart passed", addr, display)
}
func Test051UnavailableAnchorPreservesCompletedHops(t *testing.T) {
	a := independentTestApp(t)
	p := satlinePoint{TxID: testID(30), Height: 1, BlockHash: testID(31)}
	end := satlinePoint{TxID: testID(40), Height: 3, BlockHash: testID(41)}
	q := satlineQuery{Kind: "satpoint", Input: fmt.Sprintf("%s:0:0", p.TxID)}
	result := satlineResult{Mode: "satpoint", State: "STEP_LIMIT", StartSatpoint: &p, CurrentSatpoint: &end, HopCount: 2, Hops: []satlineHop{{Index: 0, BlockHeight: 2, BlockHash: testID(32), Source: p, Destination: end}, {Index: 1, BlockHeight: 3, BlockHash: end.BlockHash, Source: end, Destination: end}}}
	if e := a.saveSatlineRecord(q.Kind, q.key(), q.Input, result); e != nil {
		t.Fatal(e)
	}
	got, _ := a.prepareSatlineBase(q, false, context.Background())
	if len(got.Hops) != 2 || got.OperationalReason != "WAITING_FOR_CHAIN_ANCHOR" || got.State == "STALE_CHAIN" {
		t.Fatalf("unavailable != conflict: %+v", got)
	}
	saved, ok := a.loadSatlineRecord(q.Kind, q.key())
	if !ok || len(saved.Result.Hops) != 2 {
		t.Fatal("saved hops lost")
	}
}
func Test051TriStateCheckpointAndTipExtension(t *testing.T) {
	expected := testID(123)
	cases := []struct {
		name, actual string
		err          error
		want         anchorState
	}{
		{"match", expected, nil, anchorMatches}, {"extension_same_ancestor", expected, nil, anchorMatches}, {"actual_conflict", testID(124), nil, anchorConflict}, {"RPC_refused", "", fmt.Errorf("connection refused"), anchorUnavailable}, {"timeout", "", context.DeadlineExceeded, anchorUnavailable}, {"missing_header", "", nil, anchorUnavailable}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := checkChainAnchor(7, expected, func(int64) (string, error) { return c.actual, c.err })
			if got.Status != c.want {
				t.Fatalf("%+v", got)
			}
		})
	}
}
func Test051UnavailableZeroHopIsNotReorg(t *testing.T) {
	if !releaseFeatureAvailable("satline") {
		t.Skip("0.6.6 release lock: live Satline traversal is unavailable; anchor-state unit checks remain active")
	}
	a := independentTestApp(t)
	a.status = appStatus{HeaderHeight: -1}
	q := satlineQuery{Kind: "sat", Input: "0"}
	got := a.runSatlineLocal(context.Background(), q, "start", 0, nil)
	if got.State == "STALE_CHAIN" || strings.Contains(got.Note, "Chain changed") {
		t.Fatalf("false zero-hop reorg: %+v", got)
	}
	if got.OperationalReason != "WAITING_FOR_CHAIN_ANCHOR" {
		t.Fatalf("missing dependency: %+v", got)
	}
}
func Test051HeaderControlPersistsWithoutDeletingData(t *testing.T) {
	a := independentTestApp(t)
	before, e := os.ReadFile(a.headersPath)
	if e != nil {
		t.Fatal(e)
	}
	for _, action := range []string{"pause", "resume"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/v1/headers", strings.NewReader(`{"action":"`+action+`"}`))
		a.handleHeaderControl(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		s := a.loadSettings()
		if s.HeadersPaused != (action == "pause") {
			t.Fatal("setting not persisted")
		}
	}
	after, _ := os.ReadFile(a.headersPath)
	if !bytes.Equal(before, after) {
		t.Fatal("header control erased progress")
	}
}
func Test051RejectNonCanonicalHeaderResponse(t *testing.T) {
	for _, payload := range [][]byte{{0xfd, 0, 0}, {0, 1}, {0xfe, 0, 0, 0, 0}} {
		if _, e := parseHeadersPayload(payload); e == nil {
			t.Fatalf("accepted malformed headers %x", payload)
		}
	}
}
func Test051MissingCoreDoesNotCapIndependentAuthority(t *testing.T) {
	a := independentTestApp(t)
	block := mainnetBlockOne(t)
	f, e := os.OpenFile(a.headersPath, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = f.Write(block[:80])
	_ = f.Close()
	a.status.HeaderCount = 2
	a.status.HeaderHeight = 1
	a.status.TipHash = genesisHashDisplay
	current := a.currentChainAuthority()
	hash := hash256(block[:80])
	if current.Height != 1 || current.Hash != reverseHex(hash[:]) || current.Source != "bod_headers" {
		t.Fatalf("stale status hash used as anchor: %+v", current)
	}
}

// These are local HTTP state-machine checks, not Windows extension approval.
func Test051BrowserApprovalRequiresAuthenticatedNativeExchange(t *testing.T) {
	a := independentTestApp(t)
	a.settings.NetworkDisabled = true
	if e := writeRuntimeInfo(a.dataDir, "http://127.0.0.1:19999"); e != nil {
		t.Fatal(e)
	}
	a.runtimeURL = "http://127.0.0.1:19999"
	heartbeat := httptest.NewRecorder()
	a.handleBrowserCompanionHeartbeat(heartbeat, httptest.NewRequest("POST", "/", strings.NewReader(`{"version":"0.5.1","browser":"fixture"}`)))
	if heartbeat.Code != 200 || !a.browserStatus().CompanionActive || a.browserStatus().NativeActive {
		t.Fatal("ordinary heartbeat falsely established native readiness")
	}
	denied := httptest.NewRecorder()
	a.handleNativeControl(denied, httptest.NewRequest("POST", "/", strings.NewReader(`{"action":"status"}`)))
	if denied.Code != 403 || a.browserStatus().NativeActive {
		t.Fatal("missing token established native readiness")
	}
	token, e := readRuntimeInfo(a.dataDir)
	if e != nil {
		t.Fatal(e)
	}
	native := httptest.NewRequest("POST", "/", strings.NewReader(`{"action":"open","address":".gateway","version":"0.5.1"}`))
	native.Header.Set("X-Gateway-Token", token.Token)
	opened := httptest.NewRecorder()
	a.handleNativeControl(opened, native)
	if opened.Code != 200 || !a.browserStatus().NativeActive {
		t.Fatalf("authenticated connection failed: %s", opened.Body.String())
	}
	if !a.readSetup().NavigationAt.IsZero() {
		t.Fatal("opening a resource is not yet displaying it")
	}
	var ack struct {
		ID string `json:"id"`
	}
	if e := json.Unmarshal(opened.Body.Bytes(), &ack); e != nil {
		t.Fatal(e)
	}
	displayed := httptest.NewRecorder()
	a.handleActivation(displayed, httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"id":%q,"stage":"displayed"}`, ack.ID))))
	if displayed.Code != 200 || a.readSetup().NavigationAddress != ".gateway" || a.readSetup().NavigationAt.IsZero() {
		t.Fatalf("displayed acknowledgement missing: %s", displayed.Body.String())
	}
	a.browserCompanion.mu.Lock()
	a.browserCompanion.nativeSeen = time.Now().Add(-3 * time.Minute)
	a.browserCompanion.mu.Unlock()
	if a.browserStatus().NativeActive {
		t.Fatal("stale native session reported connected")
	}
}
func Test051SetupResumeAndIncompleteCompletionPreservePrivacy(t *testing.T) {
	a := independentTestApp(t)
	a.settings.NetworkDisabled = true
	a.settings.SatlineUsePeers = false
	a.settings.SatlineServePublished = false
	save := httptest.NewRecorder()
	a.handleSetup(save, httptest.NewRequest("POST", "/", strings.NewReader(`{"action":"save","stage":4,"browser":"edge","profile":"Profile 2"}`)))
	if save.Code != 200 {
		t.Fatal(save.Body.String())
	}
	p := a.readSetup()
	if p.Stage != 4 || p.Completed || p.Browser != "edge" || p.Profile != "Profile 2" {
		t.Fatalf("resume choices lost: %+v", p)
	}
	invalid := httptest.NewRecorder()
	a.handleSetup(invalid, httptest.NewRequest("POST", "/", strings.NewReader(`{"action":"save","stage":4,"browser":"chrome","profile":"../../other"}`)))
	if invalid.Code != 400 {
		t.Fatal("unsafe browser profile accepted")
	}
	done := httptest.NewRecorder()
	a.handleSetup(done, httptest.NewRequest("POST", "/", strings.NewReader(`{"action":"complete"}`)))
	if done.Code != 200 {
		t.Fatal(done.Body.String())
	}
	var result struct {
		Pending  []string                 `json:"pending"`
		Progress setupProgress            `json:"progress"`
		Browser  browserIntegrationStatus `json:"browser"`
	}
	if e := json.Unmarshal(done.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if !result.Progress.Completed || len(result.Pending) < 3 || result.Browser.NativeActive {
		t.Fatal("finishing incomplete setup falsely completed network/browser readiness")
	}
	if a.settings.SatlineUsePeers || a.settings.SatlineServePublished || !a.settings.PrivacyMode || a.settings.ShareCache {
		t.Fatal("setup completion altered privacy")
	}
	fresh := &app{dataDir: a.dataDir}
	if p := fresh.readSetup(); !p.Completed || p.Profile != "Profile 2" {
		t.Fatal("restart did not retain setup choices")
	}
}
func Test051HeaderBadBatchRollsBack(t *testing.T) {
	a := independentTestApp(t)
	raw := mainnetBlockOne(t)
	before, e := os.ReadFile(a.headersPath)
	if e != nil {
		t.Fatal(e)
	}
	// A valid first header followed by the same height-1 header cannot link as height 2.
	_, e = a.appendCheckedHeaders(context.Background(), a.headersPath, 1, [][]byte{raw[:80], raw[:80]})
	if e == nil {
		t.Fatal("invalid batch was accepted")
	}
	after, e := os.ReadFile(a.headersPath)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected batch altered the selected header chain")
	}
}

type rejectingHTTP051 struct {
	mu           *sync.Mutex
	destinations *[]string
}

func (d rejectingHTTP051) RoundTrip(r *http.Request) (*http.Response, error) {
	d.mu.Lock()
	*d.destinations = append(*d.destinations, r.URL.String())
	d.mu.Unlock()
	return nil, fmt.Errorf("HTTP disabled by destination audit")
}
func Test051NoHTTPFallbackForPeerOrUnavailableLookup(t *testing.T) {
	var mu sync.Mutex
	destinations := []string{}
	oldDefault, oldCore := http.DefaultTransport, coreTransport
	http.DefaultTransport = rejectingHTTP051{&mu, &destinations}
	coreTransport = oldCore.Clone()
	coreTransport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		mu.Lock()
		destinations = append(destinations, "core:"+address)
		mu.Unlock()
		return nil, fmt.Errorf("Core transport disabled by audit")
	}
	defer func() { http.DefaultTransport = oldDefault; coreTransport = oldCore }()
	block := mainnetBlockOne(t)
	hash := hash256(block[:80])
	a := independentTestApp(t)
	addr := ordinaryFixture051(t, block, "")
	a.network.add(addr, "fixture_bootstrap", 0)
	a.network.start()
	eventually051(t, func() bool { return len(a.network.listSessions()) == 1 })
	if _, e := a.fetchAndDecode(reverseHex(hash[:])); e != nil {
		t.Fatal(e)
	}
	a.settingsMu.Lock()
	a.settings.NetworkDisabled = true
	a.settings.OrdEnabled = true
	a.settingsMu.Unlock()
	a.network.maintain()
	if _, e := a.resolveBlockTarget("900000"); e == nil {
		t.Fatal("unavailable height invented")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, e := a.resolveInscription(ctx, strings.Repeat("b", 64)+"i0", ""); e == nil {
		t.Fatal("missing inscription invented")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(destinations) != 0 {
		t.Fatalf("unexpected HTTP destination(s): %v", destinations)
	}
	t.Log("Ordinary Bitcoin peer supplied non-genesis block; missing height/inscription stayed unavailable; no HTTP data destination was attempted. Controlled fixture, not a public-network firewall test.")
}
