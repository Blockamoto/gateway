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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// This fixture speaks ordinary Bitcoin P2P 70016, not Gateway. Its optional
// relay filter models Core's service-mask gate, not the whole Core addrman.
// All endpoints are loopback under an explicit test-only admission exception.
type relay052 struct {
	address  string
	mu       sync.Mutex
	rows     []advertisedPeer
	requests int
	filter   bool
	conns    []net.Conn
}

func ordinaryRelay052(t *testing.T, rows []advertisedPeer, filter bool) *relay052 {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	r := &relay052{address: ln.Addr().String(), rows: rows, filter: filter}
	t.Cleanup(func() {
		ln.Close()
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, c := range r.conns {
			c.Close()
		}
	})
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			r.mu.Lock()
			r.conns = append(r.conns, c)
			r.mu.Unlock()
			go func(c net.Conn) {
				defer c.Close()
				c.SetDeadline(time.Now().Add(30 * time.Second))
				m, e := readMessage(c)
				if e != nil || m.command != "version" {
					return
				}
				payload := makeVersionPayload(r.address, nodeNetworkService|nodeWitnessService)
				binary.LittleEndian.PutUint32(payload[:4], 70016)
				if writeMessage(c, "version", payload) != nil || writeMessage(c, "verack", nil) != nil {
					return
				}
				for {
					m, e := readMessage(c)
					if e != nil {
						return
					}
					switch m.command {
					case "feature":
						return // tests must never send BIP434 to this older peer
					case "ping":
						_ = writeMessage(c, "pong", m.payload)
					case "getaddr":
						r.mu.Lock()
						r.requests++
						rs := append([]advertisedPeer(nil), r.rows...)
						r.mu.Unlock()
						_ = writeMessage(c, "addr", encodeAddressList(rs))
					case "addr", "addrv2":
						rs, e := parseAddressMessage(m)
						if e != nil {
							return
						}
						r.mu.Lock()
						for _, x := range rs {
							if !r.filter || x.Services&(nodeNetworkService|nodeNetworkLimitedService) != 0 {
								found := false
								for _, old := range r.rows {
									if old.Addr == x.Addr {
										found = true
									}
								}
								if !found {
									r.rows = append(r.rows, x)
								}
							}
						}
						r.mu.Unlock()
					case "getheaders":
						_ = writeMessage(c, "headers", []byte{0})
					case "getdata":
						_ = writeMessage(c, "notfound", m.payload)
					}
				}
			}(c)
		}
	}()
	return r
}
func seedBlockOne052(t *testing.T, a *app, private bool) (string, []byte) {
	t.Helper()
	raw := mainnetBlockOne(t)
	h := hash256(raw[:80])
	hash := reverseHex(h[:])
	if _, e := a.appendCheckedHeaders(context.Background(), a.headersPath, 1, [][]byte{raw[:80]}); e != nil {
		t.Fatal(e)
	}
	a.primeHeaderStatus()
	prefix := "1-" + hash
	if e := os.WriteFile(filepath.Join(a.dataDir, "blocks", "raw", prefix+".block"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	a.cacheMu.Lock()
	a.cacheIndex.Blocks[hash] = cachedBlockEntry{Height: 1, Hash: hash, Prefix: prefix, Private: private}
	a.cacheMu.Unlock()
	return hash, raw
}
func fastDiscovery052(n *bitcoinNetwork) {
	n.policy.BitcoinSlots = 1
	n.policy.ScoutSlots = 1
	n.policy.GatewaySlots = 1
	n.policy.TokenInterval = 10 * time.Millisecond
	n.policy.ProbeWindow = 2 * time.Second
}
func pump052(t *testing.T, n *bitcoinNetwork, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		n.maintain()
		if done() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	b, _ := json.MarshalIndent(n.snapshot(), "", "  ")
	t.Fatalf("discovery condition not reached:\n%s", b)
}
func useful052(n *bitcoinNetwork) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.usefulCountLocked() > 0
}

func Test052CrawlPastFullTransportPoolAndFetchBlock(t *testing.T) {
	server := independentTestApp(t)
	server.settings.ServeData = true
	server.settings.ShareCache = true
	hash, raw := seedBlockOne052(t, server, false)
	if e := server.source.start(); e != nil {
		t.Fatal(e)
	}
	gateway := server.source.tcp.Addr().String()
	// The first Bitcoin introduction does NOT know a Gateway at all.
	second := ordinaryRelay052(t, []advertisedPeer{{Addr: gateway, Services: gatewayExperimentalServiceBit | nodeWitnessService}}, false)
	first := ordinaryRelay052(t, []advertisedPeer{{Addr: second.address, Services: nodeNetworkService | nodeWitnessService}}, false)
	client := independentTestApp(t)
	fastDiscovery052(client.network)
	client.network.add(first.address, "fixture_bitcoin_bootstrap", nodeNetworkService)
	if len(client.network.addresses) != 1 {
		t.Fatal("test preloaded an endpoint")
	}
	pump052(t, client.network, func() bool { return useful052(client.network) })
	client.network.mu.Lock()
	proof := *client.network.discovery.FirstUseful
	_, firstStillConnected := client.network.sessions[first.address]
	client.network.mu.Unlock()
	if proof.Peer != gateway || proof.Source != "bitcoin_addr:"+second.address || proof.Hash != hash || proof.Bytes != len(raw) || proof.Type != "block_location_and_block" {
		t.Fatalf("wrong discovery evidence: %+v", proof)
	}
	if !firstStillConnected {
		t.Fatal("search interrupted the stable Bitcoin transport")
	}
	if client.getStatus().HeaderHeight != 0 {
		t.Fatal("probe unexpectedly required syncing headers")
	}
	if !strings.Contains(proof.Verification, "pending") {
		t.Fatalf("unanchored result overclaimed: %+v", proof)
	}
	pump052(t, client.network, func() bool {
		return client.network.snapshot()["discovery"].(discoveryState).Mode == "maintain" && client.network.snapshot()["search_connections"].(int) == 0
	})
	client.network.persist()
	restored := newBitcoinNetwork(client)
	defer restored.stop()
	if x := restored.ledger[gateway]; x == nil || !x.GatewayVerified || x.Source != proof.Source {
		t.Fatal("restart discarded origin or working peer")
	}
	if restored.discovery.FirstUseful == nil || restored.snapshot()["connected"].(int) != 0 || restored.snapshot()["gateway_connected"].(int) != 0 {
		t.Fatal("historical receipt was lost or promoted to a live restart session")
	}
	eventJSON, _ := json.Marshal(client.network.snapshot())
	t.Logf("A -> ordinary Bitcoin %s -> ordinary Bitcoin scout %s -> previously unknown Gateway %s. Block 1 %s: %d checked bytes; no Core, no preloaded Gateway, no public HTTP data source.\nTRACE=%s", first.address, second.address, gateway, hash, len(raw), eventJSON)
	server.source.stopServer()
	// stopServer closes the listener but existing connections may remain; close
	// this owned session to model a lost provider without waiting for its timeout.
	for _, s := range client.network.listSessions() {
		if s.peer.addr == gateway {
			s.close("fixture provider loss")
		}
	}
	client.network.maintain()
	mode := client.network.snapshot()["discovery"].(discoveryState).Mode
	if mode != "recover" {
		t.Fatalf("provider loss mode %s", mode)
	}
}
func Test052FalseBitStaysCandidate(t *testing.T) {
	ordinary := ordinaryRelay052(t, nil, false)
	a := independentTestApp(t)
	fastDiscovery052(a.network)
	a.network.add(ordinary.address, "bitcoin_addr:fixture", gatewayExperimentalServiceBit)
	pump052(t, a.network, func() bool { return a.network.snapshot()["discovery"].(discoveryState).Handshakes > 0 })
	snap := a.network.snapshot()
	if snap["gateway_connected"].(int) != 0 || snap["useful_gateway_peers"].(int) != 0 {
		t.Fatal("false service bit earned a milestone")
	}
}
func Test052PrivateAddressGossipCannotTriggerLANScans(t *testing.T) {
	a := independentTestApp(t)
	a.network.allowPrivate = false
	for _, address := range []string{"127.0.0.1:8333", "10.0.0.1:8333", "192.168.0.1:48333", "100.64.1.1:8333", "[::1]:8333", "[fd00::1]:8333", "192.0.2.1:8333", "[2001:db8::1]:8333"} {
		a.network.learn([]advertisedPeer{{Addr: address, Services: gatewayExperimentalServiceBit}}, "untrusted")
	}
	if len(a.network.addresses) != 0 {
		t.Fatalf("learned private/unroutable gossip: %+v", a.network.addresses)
	}
	if defaultSettings().LANDiscovery {
		t.Fatal("LAN path silently enabled")
	}
}

// Updated for v0.5.3: queue replacement must retain all pending ledger work.
func Test052BoundedFrontierAdmitsFreshCandidates(t *testing.T) {
	a := independentTestApp(t)
	for i := 0; i < maxBitcoinAddresses; i++ {
		a.network.add(fmt.Sprintf("127.1.%d.%d:8333", i/256, i%256), "bitcoin_addr:old", 0)
	}
	target := "127.2.0.1:48333"
	a.network.add(target, "bitcoin_addr:fresh", gatewayExperimentalServiceBit)
	a.network.mu.Lock()
	a.network.refillWorkLocked(a.network.now())
	a.network.mu.Unlock()
	if len(a.network.ledger) != maxBitcoinAddresses+1 || len(a.network.addresses) > maxBitcoinAddresses || a.network.addresses[target] == nil {
		t.Fatal("frontier refresh lost pending ledger work")
	}
}

func Test052SourceGroupAndTokenBudgets(t *testing.T) {
	a := independentTestApp(t)
	a.network.allowPrivate = false
	for i := 1; i <= 64; i++ {
		a.network.add(fmt.Sprintf("8.9.0.%d:8333", i), "bitcoin_addr:source", gatewayExperimentalServiceBit)
	}
	a.network.mu.Lock()
	a.network.refillWorkLocked(a.network.now())
	a.network.mu.Unlock()
	if len(a.network.ledger) != 64 {
		t.Fatal("diversity filtered history instead of only queue")
	}
	if len(a.network.addresses) != 32 {
		t.Fatalf("per-group bound: %d", len(a.network.addresses))
	}
	a.settings.DiscoveryPaused = true
	a.network.tokens = 0
	a.network.tokenAt = time.Now().Add(time.Hour)
	a.network.maintain()
	if len(a.network.dialing) > 0 {
		t.Fatal("zero rate budget dialed")
	}
	// Retry preserves endpoint history and cannot manufacture connection tokens.
	a.network.retry()
	if a.network.tokens != 0 {
		t.Fatal("retry bypassed rate limiter")
	}
}

type coreFixture052 struct {
	server                        *httptest.Server
	pruned, offline, wrongNetwork atomic.Bool
	blockReads                    atomic.Int32
	hash                          string
	block                         []byte
}

func newCoreFixture052(t *testing.T, a *app) *coreFixture052 {
	raw := mainnetBlockOne(t)
	h := hash256(raw[:80])
	f := &coreFixture052{hash: reverseHex(h[:]), block: raw}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.offline.Load() {
			http.Error(w, "offline fixture", 503)
			return
		}
		var body json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		handle := func(raw json.RawMessage) map[string]any {
			var q struct {
				Method string            `json:"method"`
				Params []json.RawMessage `json:"params"`
				ID     any               `json:"id"`
			}
			_ = json.Unmarshal(raw, &q)
			network := "main"
			if f.wrongNetwork.Load() {
				network = "regtest"
			}
			var result any
			var rpcErr any
			switch q.Method {
			case "getblockchaininfo":
				result = map[string]any{"chain": network, "blocks": 1, "headers": 1, "bestblockhash": f.hash, "pruned": f.pruned.Load(), "initialblockdownload": false}
			case "getnetworkinfo":
				result = map[string]any{"localservices": "0000000000000009", "localaddresses": []map[string]any{{"address": "8.8.4.4", "port": 8333}}}
			case "getindexinfo":
				result = map[string]any{}
			case "getblockhash":
				var height int
				_ = json.Unmarshal(q.Params[0], &height)
				if height == 0 {
					result = genesisHashDisplay
				} else if height == 1 {
					result = f.hash
				} else {
					rpcErr = map[string]any{"code": -8, "message": "height out of range"}
				}
			case "getblockheader":
				var hash string
				_ = json.Unmarshal(q.Params[0], &hash)
				height := 0
				header, _ := hex.DecodeString(genesisHeaderHex)
				if hash == f.hash {
					height = 1
					header = f.block[:80]
				} else if hash != genesisHashDisplay {
					rpcErr = map[string]any{"code": -5, "message": "not found"}
				}
				verbose := true
				if len(q.Params) > 1 {
					_ = json.Unmarshal(q.Params[1], &verbose)
				}
				if verbose {
					result = map[string]any{"hash": hash, "height": height, "confirmations": 2 - height}
				} else {
					result = hex.EncodeToString(header)
				}
			case "getblock":
				f.blockReads.Add(1)
				var hash string
				_ = json.Unmarshal(q.Params[0], &hash)
				if hash == f.hash {
					result = hex.EncodeToString(f.block)
				} else {
					rpcErr = map[string]any{"code": -5, "message": "not in fixture"}
				}
			default:
				rpcErr = map[string]any{"code": -32601, "message": "fixture method not implemented"}
			}
			return map[string]any{"result": result, "error": rpcErr, "id": q.ID}
		}
		w.Header().Set("Content-Type", "application/json")
		if len(body) > 0 && body[0] == '[' {
			var qs []json.RawMessage
			_ = json.Unmarshal(body, &qs)
			rs := []map[string]any{}
			for i := len(qs) - 1; i >= 0; i-- {
				rs = append(rs, handle(qs[i]))
			}
			_ = json.NewEncoder(w).Encode(rs)
		} else {
			_ = json.NewEncoder(w).Encode(handle(body))
		}
	}))
	t.Cleanup(f.server.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(f.server.URL, "http://"))
	p, _ := strconv.Atoi(port)
	a.settings.CoreDisabled = false
	a.settings.BitcoinDataDir = t.TempDir()
	a.settings.RPCPort = p
	a.settings.RPCAuthMode = "userpass"
	a.settings.RPCUser = "fixture"
	a.settings.RPCPassword = "fixture"
	a.settings.ServeData = true
	return f
}
func Test052TruthfulCoreBackedAdvertisementAndDowngrade(t *testing.T) {
	a := independentTestApp(t)
	f := newCoreFixture052(t, a)
	if flags, _ := a.localBitcoinAdvertisement(); flags&nodeNetworkService != 0 {
		t.Fatal("archival advertised before readiness")
	}
	a.refreshBitcoinServing(a.settings)
	if flags, _ := a.localBitcoinAdvertisement(); flags&(nodeNetworkService|gatewayExperimentalServiceBit) != (nodeNetworkService | gatewayExperimentalServiceBit) {
		t.Fatal("ready provider not advertised")
	}
	if e := a.source.start(); e != nil {
		t.Fatal(e)
	}
	_, port, _ := net.SplitHostPort(a.source.tcp.Addr().String())
	endpoint, origin := a.advertisedGatewayEndpoint()
	if endpoint != net.JoinHostPort("8.8.4.4", port) || origin != "core_reported_public_ip_gateway_port" {
		t.Fatalf("used Core's port instead of Gateway: %s %s", endpoint, origin)
	}
	rows, e := a.headersForBitcoinPeer(context.Background(), []string{genesisHashDisplay}, strings.Repeat("0", 64), 2000, true)
	if e != nil || len(rows) != 1 || !bytes.Equal(rows[0], f.block[:80]) {
		t.Fatalf("Core-backed getheaders: %v %d", e, len(rows))
	}
	for _, state := range []string{"pruned", "offline", "wrong_network"} {
		t.Run(state, func(t *testing.T) {
			f.pruned.Store(state == "pruned")
			f.offline.Store(state == "offline")
			f.wrongNetwork.Store(state == "wrong_network")
			a.refreshBitcoinServing(a.settings)
			if flags, _ := a.localBitcoinAdvertisement(); flags&nodeNetworkService != 0 {
				t.Fatalf("%s still archival", state)
			}
		})
	}
	f.pruned.Store(false)
	f.offline.Store(false)
	f.wrongNetwork.Store(false)
	a.refreshBitcoinServing(a.settings)
	a.settings.CoreDisabled = true
	if flags, _ := a.localBitcoinAdvertisement(); flags&nodeNetworkService != 0 {
		t.Fatal("disabling Core retained archival flag")
	}
}
func Test052PrivateCacheIsNotPublishedByCoreServing(t *testing.T) {
	a := independentTestApp(t)
	f := newCoreFixture052(t, a)
	hash, _ := seedBlockOne052(t, a, true)
	a.settings.ShareCache = false
	a.refreshBitcoinServing(a.settings)
	bd, e := a.storageBlockForServing(hash)
	if e != nil || !bytes.Equal(bd.Raw, f.block) || f.blockReads.Load() == 0 {
		t.Fatalf("did not acquire permitted provider bytes: %v", e)
	}
	if !a.cacheIndex.Blocks[hash].Private || a.settings.ShareCache {
		t.Fatal("private cache permissions changed")
	}
	a.settings.CoreDisabled = true
	if _, e := a.storageBlockForServing(hash); e == nil {
		t.Fatal("private bytes served after separate provider disabled")
	}
}
func Test052CoreBackedAnnouncementRelayedToFreshClient(t *testing.T) {
	ordinary := ordinaryRelay052(t, nil, true)
	server := independentTestApp(t)
	f := newCoreFixture052(t, server)
	server.refreshBitcoinServing(server.settings)
	if e := server.source.start(); e != nil {
		t.Fatal(e)
	}
	gateway := server.source.tcp.Addr().String()
	server.settings.AdvertiseAddr = gateway
	server.network.add(ordinary.address, "fixture_bitcoin_bootstrap", nodeNetworkService)
	pump052(t, server.network, func() bool {
		ordinary.mu.Lock()
		defer ordinary.mu.Unlock()
		for _, r := range ordinary.rows {
			if r.Addr == gateway {
				return true
			}
		}
		return false
	})
	client := independentTestApp(t)
	fastDiscovery052(client.network)
	client.network.add(ordinary.address, "fixture_bitcoin_bootstrap", nodeNetworkService)
	pump052(t, client.network, func() bool { return useful052(client.network) })
	client.network.mu.Lock()
	proof := *client.network.discovery.FirstUseful
	client.network.mu.Unlock()
	if proof.Hash != f.hash || proof.Bytes != len(f.block) || proof.Source != "bitcoin_addr:"+ordinary.address {
		t.Fatalf("wrong Core-backed discovery: %+v", proof)
	}
	t.Logf("Core RPC contract fixture -> truthfully flagged Gateway listener %s -> ordinary Bitcoin relay's service filter -> fresh client -> %d-byte block. This is a controlled filter model, NOT execution of stock bitcoind.", gateway, proof.Bytes)
}
func Test052LocatorAndAddressWireBounds(t *testing.T) {
	for _, payload := range [][]byte{nil, make([]byte, 36), append([]byte{0, 0, 0, 0}, append(encodeVarInt(102), make([]byte, 102*32+32)...)...)} {
		if _, _, e := parseChainLocator(payload); e == nil {
			t.Fatal("accepted invalid chain locator")
		}
	}
	row := advertisedPeer{Addr: "8.8.8.8:48333", Services: gatewayExperimentalServiceBit | nodeNetworkService}
	got, e := parseAddressMessage(message{command: "addr", payload: encodeAddressList([]advertisedPeer{row})})
	if e != nil || len(got) != 1 || got[0].Addr != row.Addr || got[0].Services != row.Services {
		t.Fatalf("addr round trip %v %+v", e, got)
	}
	var body bytes.Buffer
	body.WriteByte(1)
	_ = binary.Write(&body, binary.LittleEndian, uint32(time.Now().Unix()))
	body.Write(encodeVarInt(row.Services))
	body.WriteByte(1)
	body.WriteByte(4)
	body.Write(net.ParseIP("8.8.8.8").To4())
	_ = binary.Write(&body, binary.BigEndian, uint16(48333))
	got, e = parseAddressMessage(message{command: "addrv2", payload: body.Bytes()})
	if e != nil || len(got) != 1 || got[0].Addr != row.Addr || got[0].Services != row.Services {
		t.Fatalf("addrv2 decode %v %+v", e, got)
	}
	for _, payload := range [][]byte{body.Bytes()[:len(body.Bytes())-1], append(body.Bytes(), 0), encodeVarInt(1001)} {
		if _, e := parseAddressMessage(message{command: "addrv2", payload: payload}); e == nil {
			t.Fatal("accepted malformed addrv2")
		}
	}
}
