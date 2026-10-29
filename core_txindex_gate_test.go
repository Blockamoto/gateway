package main

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func Test064CoreHistoricalTxLookupRequiresUsableIndex(t *testing.T) {
	for _, tc := range []struct {
		name                                        string
		missing, unsynced, behind, ibd, unavailable bool
		allowed                                     bool
	}{
		{name: "missing", missing: true},
		{name: "unsynced", unsynced: true},
		{name: "synced_flag_but_behind", behind: true},
		{name: "initial_block_download", ibd: true},
		{name: "disconnected", unavailable: true},
		{name: "synced_through_current_tip", allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			txid := strings.Repeat("a", 64)
			var txCalls, headerCalls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					Method string            `json:"method"`
					Params []json.RawMessage `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					t.Error(err)
					return
				}
				var result any
				switch q.Method {
				case "getblockchaininfo":
					if tc.unavailable {
						_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": -28, "message": "Core warming up"}})
						return
					}
					result = map[string]any{"blocks": 100, "headers": 100, "bestblockhash": genesisHashDisplay, "initialblockdownload": tc.ibd}
				case "getindexinfo":
					indexes := map[string]any{}
					if !tc.missing {
						height := 100
						if tc.behind {
							height = 99
						}
						indexes["txindex"] = map[string]any{"synced": !tc.unsynced, "best_block_height": height}
					}
					result = indexes
				case "getrawtransaction":
					txCalls.Add(1)
					if len(q.Params) != 2 || string(q.Params[0]) != `"`+txid+`"` || string(q.Params[1]) != "true" {
						t.Errorf("unexpected bare historical lookup parameters: %s", q.Params)
					}
					result = map[string]any{"txid": txid, "blockhash": genesisHashDisplay}
				case "getblockheader":
					headerCalls.Add(1)
					result = map[string]any{"height": 42}
				default:
					t.Errorf("unexpected RPC method %s", q.Method)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil})
			}))
			defer server.Close()
			location, err := coreTxLocation(testRPCSettings050(t, server.URL), txid)
			if !tc.allowed {
				if err == nil || txCalls.Load() != 0 || headerCalls.Load() != 0 {
					t.Fatalf("unusable txindex queried: tx=%d header=%d error=%v", txCalls.Load(), headerCalls.Load(), err)
				}
				return
			}
			if err != nil || txCalls.Load() != 1 || headerCalls.Load() != 1 || location.TxID != txid || location.BlockHash != genesisHashDisplay || location.Height != 42 {
				t.Fatalf("synced txindex lookup failed: %+v tx=%d header=%d error=%v", location, txCalls.Load(), headerCalls.Load(), err)
			}
		})
	}
}

func Test064KnownGatewayLocationUsesCoreBlocksWithoutTxIndex(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	if err := os.Remove(filepath.Join(a.dataDir, "blocks", "raw", "0-"+genesisHashDisplay+".block")); err != nil {
		t.Fatal(err)
	}
	a.cacheIndex = newCacheIndex()
	var txCalls, blockCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		var result any
		switch q.Method {
		case "getblockchaininfo":
			result = map[string]any{"blocks": 0, "headers": 0, "bestblockhash": genesisHashDisplay}
		case "getindexinfo":
			result = map[string]any{}
		case "getblockhash":
			result = genesisHashDisplay
		case "getblockheader":
			result = map[string]any{"height": 0, "confirmations": 1}
		case "getblock":
			blockCalls.Add(1)
			result = hex.EncodeToString(raw)
		case "getrawtransaction":
			txCalls.Add(1)
			t.Error("known block location attempted bare transaction lookup")
		default:
			t.Errorf("unexpected RPC method %s", q.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil})
	}))
	defer server.Close()
	a.settings = testRPCSettings050(t, server.URL)
	a.settings.CoreMountDisabled = true
	block, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	id := block.Transactions[0].TxID
	r, err := a.verifyTxLocation(id, txLocation{TxID: id, Height: 0, BlockHash: genesisHashDisplay, TxIndex: 0}, "known Gateway location")
	if err != nil || !r.TransactionVerified || r.Transaction.TxID != id || txCalls.Load() != 0 || blockCalls.Load() == 0 {
		t.Fatalf("known location requires txindex: %+v blocks=%d transactions=%d error=%v", r, blockCalls.Load(), txCalls.Load(), err)
	}
}
