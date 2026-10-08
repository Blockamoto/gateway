package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func Test068ServingDefaultsAndIndependentGatewayLock(t *testing.T) {
	s := defaultSettings()
	if !s.ServeData || s.ServeGatewayData || !bitcoinListenerEnabled(s) {
		t.Fatal("fresh installs must enable ordinary Bitcoin serving only")
	}
	s.ServeData, s.ServeGatewayData, s.SatlineEnabled, s.SatlineServePublished = false, true, true, true
	if bitcoinListenerEnabled(s) || s.public().ServeGatewayData {
		t.Fatal("saved Gateway sharing preference bypasses the release lock")
	}
	a := independentTestApp(t)
	a.gatewayListen = "127.0.0.1:0"
	body, _ := json.Marshal(s)
	w := httptest.NewRecorder()
	a.handleSettings(w, httptest.NewRequest("POST", "/api/v1/settings", bytes.NewReader(body)))
	if w.Code != http.StatusOK || a.settings.ServeGatewayData || a.source.running() {
		t.Fatalf("crafted Gateway setting activated sharing: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	a.handleRuntimeToggleServing(w, httptest.NewRequest("POST", "/api/v1/runtime/toggle-serving", nil))
	if w.Code != http.StatusOK || !a.settings.ServeData || !a.source.running() || a.settings.ServeGatewayData {
		t.Fatalf("tray toggle must enable only Bitcoin serving: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	a.handleRuntimeToggleServing(w, httptest.NewRequest("POST", "/api/v1/runtime/toggle-serving", nil))
	if w.Code != http.StatusOK || a.settings.ServeData || a.source.running() {
		t.Fatal("tray toggle did not stop Bitcoin listener")
	}
}

func Test068BitcoinWirePrivateBlocksAndServingOff(t *testing.T) {
	a, payload := testAppWithGenesis(t)
	a.gatewayListen = "127.0.0.1:0"
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.ShareCache = true, true, true
	if err := a.source.start(); err != nil {
		t.Fatal(err)
	}
	c, flags := ordinaryBitcoinHandshake(t, a.source.tcp.Addr().String())
	defer c.Close()
	if flags != nodeWitnessService {
		t.Fatalf("sparse serving claimed coverage: %x", flags)
	}
	rawHash, _ := displayHashRaw(genesisHashDisplay)
	var request bytes.Buffer
	request.WriteByte(1)
	_ = binary.Write(&request, binary.LittleEndian, uint32(0x40000002))
	request.Write(rawHash[:])
	get := func(want string) {
		t.Helper()
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		if err := writeMessage(c, "getdata", request.Bytes()); err != nil {
			t.Fatal(err)
		}
		m, err := readMessage(c)
		if err != nil || m.command != want {
			t.Fatalf("want %s, got %s: %v", want, m.command, err)
		}
		if want == "block" && !bytes.Equal(m.payload, payload) {
			t.Fatal("served bytes changed")
		}
		if want == "notfound" && !bytes.Equal(m.payload, request.Bytes()) {
			t.Fatal("wrong denied inventory")
		}
	}
	get("block")
	a.cacheMu.Lock()
	entry := a.cacheIndex.Blocks[genesisHashDisplay]
	entry.Private = true
	a.cacheIndex.Blocks[genesisHashDisplay] = entry
	a.cacheMu.Unlock()
	get("notfound")
	// Changing privacy for future blocks must not republish an existing object.
	a.settingsMu.Lock()
	a.settings.PrivacyMode = false
	a.settingsMu.Unlock()
	get("notfound")
	a.cacheMu.Lock()
	entry.Private = false
	a.cacheIndex.Blocks[genesisHashDisplay] = entry
	a.cacheMu.Unlock()
	a.settingsMu.Lock()
	a.settings.ShareCache = false
	a.settingsMu.Unlock()
	get("notfound")
	a.settingsMu.Lock()
	a.settings.ShareCache, a.settings.ServeData = true, false
	a.settingsMu.Unlock()
	get("notfound")
	if protocols := a.localGatewayProtocols(); len(protocols) != 0 {
		t.Fatal("Bitcoin serving unlocked Gateway protocols")
	}
}

func Test068BitcoinProviderAdvertisement(t *testing.T) {
	for _, tc := range []struct {
		name        string
		pruned, ibd bool
		pruneHeight any
		services    string
		want        uint64
	}{
		{"full", false, false, nil, "9", nodeNetworkService | nodeWitnessService},
		{"pruned", true, false, 713, "408", nodeNetworkLimitedService | nodeWitnessService},
		{"pruned-too-short", true, false, 714, "408", nodeWitnessService},
		{"pruned-no-range", true, false, nil, "408", nodeWitnessService},
		{"pruned-no-witness", true, false, 500, "400", nodeWitnessService},
		{"catching-up", false, true, nil, "9", nodeWitnessService},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Method string `json:"method"`
					ID     any    `json:"id"`
				}
				_ = json.NewDecoder(r.Body).Decode(&request)
				var result any
				if request.Method == "getblockchaininfo" {
					result = map[string]any{"chain": "main", "blocks": 1000, "headers": 1000, "bestblockhash": genesisHashDisplay, "pruned": tc.pruned, "pruneheight": tc.pruneHeight, "initialblockdownload": tc.ibd}
				} else if request.Method == "getnetworkinfo" {
					result = map[string]any{"localservices": tc.services}
				} else {
					t.Errorf("unexpected RPC %s", request.Method)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": request.ID, "result": result, "error": nil})
			}))
			defer rpc.Close()
			_, port, _ := net.SplitHostPort(strings.TrimPrefix(rpc.URL, "http://"))
			numericPort, _ := strconv.Atoi(port)
			a := &app{settings: appSettings{ServeData: true, BitcoinDataDir: t.TempDir(), RPCAuthMode: "userpass", RPCUser: "fixture", RPCPassword: "fixture", RPCPort: numericPort}}
			a.refreshBitcoinServing(a.settings)
			flags, _ := a.localBitcoinAdvertisement()
			if flags != tc.want {
				t.Fatalf("services %x, want %x: %+v", flags, tc.want, a.currentBitcoinServing())
			}
			a.servingMu.Lock()
			a.servingReadiness.Checked = time.Now().Add(-13 * time.Second)
			a.servingMu.Unlock()
			if flags, _ = a.localBitcoinAdvertisement(); flags != nodeWitnessService {
				t.Fatal("stale provider still advertises chain coverage")
			}
			a.settings.ServeData = false
			if flags, _ = a.localBitcoinAdvertisement(); flags != 0 {
				t.Fatal("disabled serving still advertises service")
			}
		})
	}
}

func Test068PrunedServingPreservesHeaderContinuity(t *testing.T) {
	genesis, _ := hex.DecodeString(genesisHeaderHex)
	next := append([]byte(nil), genesis...)
	prev := hash256(genesis)
	copy(next[4:36], prev[:])
	requestHash := hash256(next)
	nextHash := reverseHex(requestHash[:])
	var requestedHeights []int64
	var mu sync.Mutex
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)
		type call struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
			ID     any               `json:"id"`
		}
		answer := func(req call) map[string]any {
			var result any
			switch req.Method {
			case "getblockhash":
				var h int64
				_ = json.Unmarshal(req.Params[0], &h)
				mu.Lock()
				requestedHeights = append(requestedHeights, h)
				mu.Unlock()
				result = nextHash
			case "getblockheader":
				var verbose bool
				_ = json.Unmarshal(req.Params[1], &verbose)
				if verbose {
					result = map[string]any{"height": 0, "confirmations": 1001}
				} else {
					result = hex.EncodeToString(next)
				}
			default:
				t.Errorf("unexpected RPC %s", req.Method)
			}
			return map[string]any{"id": req.ID, "result": result, "error": nil}
		}
		if len(raw) > 0 && raw[0] == '[' {
			var requests []call
			_ = json.Unmarshal(raw, &requests)
			out := []map[string]any{}
			for _, req := range requests {
				out = append(out, answer(req))
			}
			_ = json.NewEncoder(w).Encode(out)
		} else {
			var req call
			_ = json.Unmarshal(raw, &req)
			_ = json.NewEncoder(w).Encode(answer(req))
		}
	}))
	defer rpc.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(rpc.URL, "http://"))
	numericPort, _ := strconv.Atoi(port)
	a := &app{settings: appSettings{ServeData: true, BitcoinDataDir: t.TempDir(), RPCAuthMode: "userpass", RPCUser: "fixture", RPCPassword: "fixture", RPCPort: numericPort}}
	a.servingReadiness = bitcoinServingReadiness{Limited: true, Height: 1000, Key: servingSettingsKey(a.settings), Checked: time.Now()}
	for _, headersRequest := range []bool{true, false} {
		out, err := a.headersForBitcoinPeer(context.Background(), []string{genesisHashDisplay}, strings.Repeat("0", 64), 1, headersRequest)
		if err != nil || len(out) != 1 || !bytes.Equal(out[0], next) {
			t.Fatalf("header reply %v %v", out, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requestedHeights) != 2 || requestedHeights[0] != 1 || requestedHeights[1] != 713 {
		t.Fatalf("header continuity vs retained-block inventory: %v", requestedHeights)
	}
}

func Test068OutboundBitcoinSessionServesAndKeepsGatewayLocked(t *testing.T) {
	a, payload := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.ShareCache = true, true, true
	a.network = newBitcoinNetwork(a)
	local, remote := net.Pipe()
	defer remote.Close()
	_ = sessionRepairManaged(t, a, local, "127.0.0.1:60200")
	_ = remote.SetDeadline(time.Now().Add(3 * time.Second))
	rawHash, _ := displayHashRaw(genesisHashDisplay)
	var request bytes.Buffer
	request.WriteByte(1)
	_ = binary.Write(&request, binary.LittleEndian, uint32(2))
	request.Write(rawHash[:])
	if err := writeMessage(remote, "getdata", request.Bytes()); err != nil {
		t.Fatal(err)
	}
	m, err := readMessage(remote)
	if err != nil || m.command != "block" || !bytes.Equal(m.payload, payload) {
		t.Fatalf("ordinary request failed %s %v", m.command, err)
	}
	// Even a previously negotiated fixture cannot activate a release-locked protocol.
	if err := writeMessage(remote, "gwmsg", []byte(`{"kind":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	if err := writeMessage(remote, "bodmsg", []byte(`{"wire":1,"kind":"request","type":"status","id":"locked"}`)); err != nil {
		t.Fatal(err)
	}
	if err := writeMessage(remote, "ping", make([]byte, 8)); err != nil {
		t.Fatal(err)
	}
	m, err = readMessage(remote)
	if err != nil || m.command != "pong" {
		t.Fatalf("locked protocol emitted response %s %v", m.command, err)
	}
}

func Test068BitcoinServingBoundsSlowCoreBatch(t *testing.T) {
	var requests atomic.Int64
	canceled := make(chan struct{}, 1)
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
		canceled <- struct{}{}
	}))
	defer rpc.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(rpc.URL, "http://"))
	numericPort, _ := strconv.Atoi(port)
	a := &app{settings: appSettings{ServeData: true, BitcoinDataDir: t.TempDir(), RPCAuthMode: "userpass", RPCUser: "fixture", RPCPassword: "fixture", RPCPort: numericPort}, cacheIndex: newCacheIndex()}
	a.source = newOverlayServer(a)
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	var request bytes.Buffer
	request.WriteByte(128)
	for i := 0; i < 128; i++ {
		_ = binary.Write(&request, binary.LittleEndian, uint32(2))
		request.Write(bytes.Repeat([]byte{byte(i + 1)}, 32))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	a.source.handleGetDataContext(ctx, local, request.Bytes())
	if time.Since(start) > time.Second || ctx.Err() == nil || requests.Load() != 1 {
		t.Fatalf("batch did not honor a single deadline: calls=%d elapsed=%s err=%v", requests.Load(), time.Since(start), ctx.Err())
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("provider request did not receive cancellation")
	}
}
