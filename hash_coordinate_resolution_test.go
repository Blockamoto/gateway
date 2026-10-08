package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This is an ordinary Bitcoin wire fixture, not a Gateway/Core/Ord service.
// A counter makes accidental repeated block downloads observable.
func hashCoordinateBitcoinSource(t *testing.T, blocks ...[]byte) (string, *atomic.Int32) {
	t.Helper()
	byHash := map[string][]byte{}
	for _, raw := range blocks {
		h := hash256(raw[:80])
		byHash[reverseHex(h[:])] = raw
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	requests := &atomic.Int32{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				m, err := readMessage(c)
				if err != nil || m.command != "version" {
					return
				}
				v := makeVersionPayload(c.RemoteAddr().String(), nodeNetworkService|nodeWitnessService)
				binary.LittleEndian.PutUint32(v[:4], 70016)
				if writeMessage(c, "version", v) != nil || writeMessage(c, "verack", nil) != nil {
					return
				}
				for {
					m, err = readMessage(c)
					if err != nil {
						return
					}
					switch m.command {
					case "getdata":
						count, size, err := decodeVarInt(m.payload)
						if err != nil || count != 1 || len(m.payload) != size+36 {
							return
						}
						raw, ok := byHash[reverseHex(m.payload[size+4:size+36])]
						requests.Add(1)
						if !ok {
							_ = writeMessage(c, "notfound", m.payload)
							continue
						}
						if writeMessage(c, "block", raw) != nil {
							return
						}
					case "ping":
						if writeMessage(c, "pong", m.payload) != nil {
							return
						}
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), requests
}

func hashCoordinateFreshApp(t *testing.T) (*app, []byte) {
	t.Helper()
	a := independentTestApp(t)
	a.settings.CacheBlocks = false
	a.settings.ServeData = false
	a.settings.OrdEnabled = true
	return a, testGenesisBlockPayload(t)
}

func hashCoordinateConnect(t *testing.T, a *app, peer string) {
	t.Helper()
	a.network.add(peer, "fixture_bootstrap", 0)
	a.network.start()
	eventually051(t, func() bool { return len(a.network.listSessions()) == 1 })
	if len(a.network.gatewayPeers()) != 0 {
		t.Fatal("ordinary Bitcoin fixture unexpectedly negotiated Gateway")
	}
}

func Test063HashTransactionFromOrdinaryBitcoinWithoutIndex(t *testing.T) {
	a, genesis := hashCoordinateFreshApp(t)
	peer, requests := hashCoordinateBitcoinSource(t, genesis)
	hashCoordinateConnect(t, a, peer)
	block, err := parseBlockDetailed(0, genesisHashDisplay, genesis[:80], genesis, "fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	txid := block.Transactions[0].TxID
	result, err := a.resolveLocalTarget(txid + ".0.bitcoin")
	if err != nil {
		t.Fatal(err)
	}
	tx := result.Result.Tx
	if tx == nil || tx.TxID != txid || tx.SourceNetwork != "bitcoin" || !tx.TransactionVerified || !tx.LocatorVerified || tx.FromCache {
		t.Fatalf("lost exact identity or source evidence: %+v", tx)
	}
	if requests.Load() != 1 {
		t.Fatalf("wanted one block request, got %d", requests.Load())
	}
	if files, _ := filepath.Glob(filepath.Join(a.dataDir, "blocks", "raw", "*.block")); len(files) != 0 {
		t.Fatal("disabled raw retention was ignored")
	}
	if _, err := a.resolveLocalTarget(strings.Repeat("2", 64) + ".0.bitcoin"); err == nil || !strings.Contains(err.Error(), "not in supplied block") {
		t.Fatalf("missing hash was not an explicit absence in this block: %v", err)
	}
}

func Test063HashInscriptionUsesSingleBitcoinBlock(t *testing.T) {
	if !releaseFeatureAvailable("inscriptions") {
		t.Skip("0.6.6 release lock: inscription resolution is unavailable")
	}
	a, genesis := hashCoordinateFreshApp(t)
	raw, err := os.ReadFile(filepath.Join("docs", "bitmap-evidence", "genesis-block.raw"))
	if err != nil {
		t.Fatal(err)
	}
	// Sparse fixture header file: the reveal header is explicitly trusted by
	// this test. This is NOT a mainnet header-sync or public-network acceptance.
	f, err := os.OpenFile(a.headersPath, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(raw[:80], 792435*80)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("header fixture: %v %v", err, closeErr)
	}
	a.status.HeaderCount, a.status.HeaderHeight = 792436, 792435
	peer, requests := hashCoordinateBitcoinSource(t, raw, genesis)
	hashCoordinateConnect(t, a, peer)
	id := hashCoordinateFixtureID + "i0"
	result, err := a.resolveLocalTarget(id + ".792435.bitcoin")
	if err != nil {
		t.Fatal(err)
	}
	rec := result.Result.Ord
	if rec == nil || rec.ID != id || rec.Height != 792435 || rec.Provider != "bitcoin_on_demand_witness" || rec.Evidence != "header_anchored" {
		t.Fatalf("lost inscription identity/evidence: %+v", rec)
	}
	_, body, err := a.loadOrdRecord(id)
	if err != nil || string(body) != "0.bitmap" {
		t.Fatalf("wrong reveal body: %q %v", body, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("re-fetched checked reveal block: %d requests", requests.Load())
	}
	// The correct inscription is now cached. An explicit wrong block must not
	// return that cached body, consult an Ord adapter or switch to another block.
	if _, err := a.resolveLocalTarget(id + ".0.bitcoin"); err == nil {
		t.Fatal("cached inscription overrode an explicit wrong reveal block")
	}
	if requests.Load() != 2 {
		t.Fatalf("wrong-height request escaped its block: %d", requests.Load())
	}
	if _, err := a.resolveLocalTarget(hashCoordinateFixtureID + "i2147483647.792435.bitcoin"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("absent envelope was not rejected: %v", err)
	}
	if files, _ := filepath.Glob(filepath.Join(a.dataDir, "blocks", "raw", "*.block")); len(files) != 0 {
		t.Fatal("inscription resolution accumulated raw blocks against policy")
	}
}

func Test063CheckedRevealPreservesEvidenceAndModulePolicy(t *testing.T) {
	if !releaseFeatureAvailable("inscriptions") {
		t.Skip("0.6.6 release lock: inscription resolution is unavailable")
	}
	tx := inscriptionTestTx(inscriptionTestScript(nil, []byte("body")))
	block := inscriptionTestBlock(tx)
	block.Verification.HeaderChainMatch = false
	block.VerificationState = "pending_header_validation"
	a := &app{dataDir: t.TempDir(), settings: appSettings{OrdEnabled: true}}
	rec, err := a.resolveInscriptionInBlock(context.Background(), block, tx.TxID, 0)
	if err != nil || rec.Evidence != "pending_header_validation" {
		t.Fatalf("upgraded chain assurance: %+v %v", rec, err)
	}
	for _, flag := range []string{"witness", "merkle"} {
		bad := block
		if flag == "witness" {
			bad.Verification.WitnessCommitment = false
		} else {
			bad.Verification.MerkleRoot = false
		}
		if _, err := a.resolveInscriptionInBlock(context.Background(), bad, tx.TxID, 0); err == nil {
			t.Fatalf("accepted missing %s evidence", flag)
		}
	}
	if _, err := a.resolveInscriptionInBlock(context.Background(), block, tx.TxID, -1); err == nil {
		t.Fatal("accepted negative index")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.resolveInscriptionInBlock(ctx, block, tx.TxID, 0); err != context.Canceled {
		t.Fatalf("ignored cancellation: %v", err)
	}
	a.settings.OrdEnabled = false
	if _, err := a.resolveInscriptionInBlock(context.Background(), block, tx.TxID, 0); err == nil {
		t.Fatal("ignored module policy")
	}
}

func Test063ExplicitRevealHintAlsoReusesBlock(t *testing.T) {
	if !releaseFeatureAvailable("inscriptions") {
		t.Skip("0.6.6 release lock: inscription resolution is unavailable")
	}
	a, genesis := hashCoordinateFreshApp(t)
	peer, requests := hashCoordinateBitcoinSource(t, genesis)
	hashCoordinateConnect(t, a, peer)
	_, err := a.resolveInscription(context.Background(), fmt.Sprintf("%si0", strings.Repeat("2", 64)), "0")
	if err == nil || !strings.Contains(err.Error(), "witness commitment") {
		t.Fatalf("genesis cannot prove inscription witness: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("explicit hint fetched %d blocks", requests.Load())
	}
}
