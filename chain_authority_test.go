package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func fakeCoreRPC(t *testing.T, height int64, hash string) (appSettings, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".cookie"), []byte("user:pass"), 0600); err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
			ID     any               `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		switch req.Method {
		case "getblockhash":
			var h int64
			if len(req.Params) > 0 {
				_ = json.Unmarshal(req.Params[0], &h)
			}
			if h != height {
				json.NewEncoder(w).Encode(map[string]any{"result": nil, "error": map[string]any{"code": -8, "message": "height out of range"}, "id": "bod"})
				return
			}
			result = hash
		case "getblockchaininfo":
			result = map[string]any{"blocks": height, "headers": height, "bestblockhash": hash, "pruned": false, "initialblockdownload": false}
		case "getindexinfo":
			result = map[string]any{}
		case "getblockheader":
			result = map[string]any{"height": height, "confirmations": 1}
		default:
			json.NewEncoder(w).Encode(map[string]any{"result": nil, "error": map[string]any{"code": -32601, "message": "unknown"}, "id": "bod"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil, "id": "bod"})
	})}
	go srv.Serve(ln)
	cleanup := func() { _ = srv.Close(); _ = ln.Close() }
	return appSettings{BitcoinDataDir: dir, RPCAuthMode: "cookie", RPCPort: port}, cleanup
}

func TestCoreAuthorityBypassesBehindHeaderMirror(t *testing.T) {
	hash := strings.Repeat("11", 32)
	settings, done := fakeCoreRPC(t, 900000, hash)
	defer done()
	a := &app{settings: settings, status: appStatus{HeaderCount: 1, HeaderHeight: 0, Ready: true}}
	got, err := a.resolveBlockTarget(strconv.FormatInt(900000, 10))
	if err != nil {
		t.Fatal(err)
	}
	if got.Height != 900000 || got.HashDisplay != hash || !got.ConsensusAuthority || got.ChainAuthority != "bitcoin_core" {
		t.Fatalf("unexpected target %#v", got)
	}
}

func TestCoreHashAuthorityRequiresActiveChain(t *testing.T) {
	hash := strings.Repeat("22", 32)
	settings, done := fakeCoreRPC(t, 42, hash)
	defer done()
	a := &app{settings: settings, status: appStatus{HeaderCount: 1, HeaderHeight: 0}}
	got, err := a.resolveBlockTarget(hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.Height != 42 || got.HashDisplay != hash || !got.ConsensusAuthority {
		t.Fatalf("unexpected target %#v", got)
	}
}
