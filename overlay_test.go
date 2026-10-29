package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fakeCore(t *testing.T) (string, func()) {
	dir, stop, _ := fakeCoreWithScanCounter(t)
	return dir, stop
}

func fakeCoreWithScanCounter(t *testing.T) (string, func(), *atomic.Int64) {
	t.Helper()
	scans := &atomic.Int64{}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".cookie"), []byte("user:pass"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bitcoin.conf"), []byte("rpcport="+strconv.Itoa(port)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	txid := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	blockhash := genesisHashDisplay
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		var result any
		switch q.Method {
		case "getblockchaininfo":
			result = map[string]any{"blocks": 0, "bestblockhash": blockhash}
		case "getindexinfo":
			result = map[string]any{"txindex": map[string]any{"synced": true, "best_block_height": 0}, "txospenderindex": map[string]any{"synced": true, "best_block_height": 0}}
		case "getrawtransaction":
			result = map[string]any{"txid": txid, "blockhash": blockhash}
		case "getblockheader":
			result = map[string]any{"height": 0}
		case "gettxspendingprevout":
			result = []any{map[string]any{"txid": txid, "vout": 1, "spendingtxid": "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", "blockhash": blockhash}}
		case "scantxoutset":
			scans.Add(1)
			result = map[string]any{"success": true, "txouts": 1, "height": 0, "bestblock": blockhash, "total_amount": 1.25, "unspents": []any{map[string]any{"txid": txid, "vout": 2, "scriptPubKey": "0014abcd", "desc": "addr(bc1qtest)#x", "amount": 1.25, "blockhash": blockhash, "confirmations": 1}}}
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"result": nil, "error": map[string]any{"code": -1, "message": "unknown"}, "id": "bod"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil, "id": "bod"})
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	return dir, func() { _ = srv.Close() }, scans
}

func TestCoreLookupAndOverlay(t *testing.T) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Gateway exchange remains locked; ordinary Bitcoin serving is tested separately")
	}
	dir, stopCore, scans := fakeCoreWithScanCounter(t)
	defer stopCore()
	txid := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	settings := appSettings{BitcoinDataDir: dir, RPCAuthMode: "auto", ServeData: true}
	loc, err := coreTxLocation(settings, txid)
	if err != nil || loc.Height != 0 {
		t.Fatalf("txloc: %+v %v", loc, err)
	}
	spend, err := coreSpendLocation(settings, txid, 1)
	if err != nil || !spend.Found {
		t.Fatalf("spend: %+v %v", spend, err)
	}
	utxos, err := coreAddressUTXOs(settings, "bc1qtest")
	if err != nil || len(utxos.UTXOs) != 1 || utxos.TotalSats != 125000000 || utxos.UTXOs[0].Height != 0 {
		t.Fatalf("utxos: %+v %v", utxos, err)
	}

	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "headers"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureHeaderFile(filepath.Join(dataDir, "headers", "headers.bin")); err != nil {
		t.Fatal(err)
	}
	a := &app{dataDir: dataDir, headersPath: filepath.Join(dataDir, "headers", "headers.bin"), settings: settings, status: appStatus{HeaderCount: 1, HeaderHeight: 0, Ready: true}, cacheIndex: newCacheIndex()}
	s := newOverlayServer(a)
	a.source = s
	if err := s.start(); err != nil {
		t.Skipf("overlay port unavailable: %v", err)
	}
	defer s.stopServer()
	time.Sleep(30 * time.Millisecond)
	resp, err := queryOverlayPeer(overlayPeer{Addr: fmt.Sprintf("127.0.0.1:%d", overlayTCPPort)}, overlayRequest{Version: overlayProtocolVersion, Type: "txloc", TxID: txid})
	if err != nil || resp.TxLocation == nil || resp.TxLocation.Height != 0 {
		t.Fatalf("overlay: %+v %v", resp, err)
	}
	addrResp, err := queryOverlayPeer(overlayPeer{Addr: fmt.Sprintf("127.0.0.1:%d", overlayTCPPort)}, overlayRequest{Version: overlayProtocolVersion, Type: "address_utxos", Address: "bc1qtest"})
	if err == nil || !strings.Contains(err.Error(), "unsupported") || addrResp.AddressUTXOs != nil {
		t.Fatalf("remote address scan must be unsupported: %+v %v", addrResp, err)
	}
	if got := scans.Load(); got != 1 {
		t.Fatalf("remote request triggered Core scan: got %d scans, want only the explicit local scan", got)
	}
}

func TestRPCUserPasswordFallbackWithoutCookie(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	dir := t.TempDir()
	conf := fmt.Sprintf("rpcport=%d\nrpcuser=alice\nrpcpassword=secret123\n", port)
	if err := os.WriteFile(filepath.Join(dir, "bitcoin.conf"), []byte(conf), 0600); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "alice" || p != "secret123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var q struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		var result any
		switch q.Method {
		case "getblockchaininfo":
			result = map[string]any{"blocks": 123}
		case "getindexinfo":
			result = map[string]any{"txindex": map[string]any{"synced": true, "best_block_height": 123}}
		default:
			result = nil
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil, "id": "bod"})
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	settings := appSettings{BitcoinDataDir: dir, RPCAuthMode: "auto"}
	st := inspectCore(settings)
	if !st.Connected || !st.TxIndex || st.AuthSource != "bitcoin.conf rpcuser/rpcpassword" || st.RPCUser != "alice" {
		t.Fatalf("unexpected status: %+v", st)
	}
}

func TestLightPeerServesVerifiedCacheAndHeaderLocation(t *testing.T) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Gateway exchange remains locked; ordinary Bitcoin serving is tested separately")
	}
	dataDir := t.TempDir()
	for _, p := range []string{filepath.Join(dataDir, "headers"), filepath.Join(dataDir, "blocks", "raw"), filepath.Join(dataDir, "blocks", "hex"), filepath.Join(dataDir, "blocks", "json")} {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	h, _ := hex.DecodeString(genesisHeaderHex)
	if err := os.WriteFile(filepath.Join(dataDir, "headers", "headers.bin"), h, 0644); err != nil {
		t.Fatal(err)
	}
	txHex := "0100000001" +
		"0000000000000000000000000000000000000000000000000000000000000000ffffffff" +
		"4d04ffff001d0104455468652054696d65732030332f4a616e2f32303039204368616e63656c6c6f72206f6e206272696e6b206f66207365636f6e64206261696c6f757420666f722062616e6b73" +
		"ffffffff01" + "00f2052a01000000" + "43" +
		"4104678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f61deb649f6bc3f4cef38c4f35504e51ec112de5c384df7ba0b8d578a4c702b6bf11d5fac" + "00000000"
	tx, _ := hex.DecodeString(txHex)
	payload := append(append(append([]byte{}, h...), 0x01), tx...)
	view, err := parseBlockDetailed(0, genesisHashDisplay, h, payload, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "0-" + genesisHashDisplay
	if err := os.WriteFile(filepath.Join(dataDir, "blocks", "raw", prefix+".block"), payload, 0644); err != nil {
		t.Fatal(err)
	}
	jb, _ := json.Marshal(view)
	if err := os.WriteFile(filepath.Join(dataDir, "blocks", "json", prefix+".json"), jb, 0644); err != nil {
		t.Fatal(err)
	}

	a := &app{dataDir: dataDir, headersPath: filepath.Join(dataDir, "headers", "headers.bin"), status: appStatus{HeaderCount: 1, HeaderHeight: 0, Ready: true}, settings: appSettings{ServeData: true, ShareCache: true}, cacheIndex: newCacheIndex()}
	a.loadCacheIndex()
	s := newOverlayServer(a)
	a.source = s
	if err := s.start(); err != nil {
		t.Skipf("overlay port unavailable: %v", err)
	}
	defer s.stopServer()
	time.Sleep(20 * time.Millisecond)
	peer := overlayPeer{Addr: fmt.Sprintf("127.0.0.1:%d", overlayTCPPort)}
	loc, err := queryOverlayPeer(peer, overlayRequest{Version: overlayProtocolVersion, Type: "blockloc", Height: 0})
	if err != nil || loc.BlockLocation == nil || loc.BlockLocation.BlockHash != genesisHashDisplay {
		t.Fatalf("blockloc: %+v %v", loc, err)
	}
	// Raw block transfer in v0.3.6 deliberately uses standard Bitcoin
	// getdata/block rather than a custom BOD message.
	pc, err := connectPeer(peer.Addr)
	if err != nil {
		t.Fatalf("bitcoin handshake to BOD peer: %v", err)
	}
	var req bytes.Buffer
	req.Write(encodeVarInt(1))
	_ = binary.Write(&req, binary.LittleEndian, uint32(2))
	rawHash, _ := displayHashRaw(genesisHashDisplay)
	req.Write(rawHash[:])
	if err := writeMessage(pc.conn, "getdata", req.Bytes()); err != nil {
		t.Fatal(err)
	}
	bm, err := waitForBlockOrNotFound(pc, 3*time.Second)
	pc.conn.Close()
	if err != nil || len(bm.payload) != len(payload) {
		t.Fatalf("standard block transfer: %d bytes %v", len(bm.payload), err)
	}
	txid := view.Transactions[0].TxID
	txr, err := queryOverlayPeer(peer, overlayRequest{Version: overlayProtocolVersion, Type: "txloc", TxID: txid})
	if err != nil || txr.TxLocation == nil || txr.TxLocation.Height != 0 {
		t.Fatalf("cached txloc: %+v %v", txr, err)
	}
}

func TestUnifiedStorageReadsCoreWithoutBODCopy(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".cookie"), []byte("user:pass"), 0600)
	_ = os.WriteFile(filepath.Join(dir, "bitcoin.conf"), []byte("rpcport="+strconv.Itoa(port)+"\n"), 0600)

	txHex := "0100000001" +
		"0000000000000000000000000000000000000000000000000000000000000000ffffffff" +
		"4d04ffff001d0104455468652054696d65732030332f4a616e2f32303039204368616e63656c6c6f72206f6e206272696e6b206f66207365636f6e64206261696c6f757420666f722062616e6b73" +
		"ffffffff01" + "00f2052a01000000" + "43" +
		"4104678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f61deb649f6bc3f4cef38c4f35504e51ec112de5c384df7ba0b8d578a4c702b6bf11d5fac" + "00000000"
	rawHex := genesisHeaderHex + "01" + txHex
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		var result any
		switch q.Method {
		case "getblockchaininfo":
			result = map[string]any{"blocks": 0, "bestblockhash": genesisHashDisplay, "pruned": false}
		case "getindexinfo":
			result = map[string]any{}
		case "getblock":
			result = rawHex
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"result": nil, "error": map[string]any{"code": -1, "message": "unknown"}, "id": "bod"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil, "id": "bod"})
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	dataDir := t.TempDir()
	for _, d := range []string{"headers", "blocks/raw", "blocks/hex", "blocks/json"} {
		_ = os.MkdirAll(filepath.Join(dataDir, d), 0755)
	}
	h, _ := hex.DecodeString(genesisHeaderHex)
	_ = os.WriteFile(filepath.Join(dataDir, "headers", "headers.bin"), h, 0644)
	a := &app{dataDir: dataDir, headersPath: filepath.Join(dataDir, "headers", "headers.bin"), settings: appSettings{BitcoinDataDir: dir, RPCAuthMode: "auto", CacheBlocks: true}, status: appStatus{HeaderCount: 1, HeaderHeight: 0, Ready: true}, cacheIndex: newCacheIndex()}
	target, err := a.localBlockTarget(0)
	if err != nil {
		t.Fatal(err)
	}
	hit, err := a.localStorageBlock(target)
	if err != nil {
		t.Fatal(err)
	}
	if hit.Network != "core" || len(hit.Raw) < 81 {
		t.Fatalf("unexpected storage hit: %+v", hit)
	}
}
