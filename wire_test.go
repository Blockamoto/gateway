package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testAppWithGenesis(t *testing.T) (*app, []byte) {
	t.Helper()
	dataDir := t.TempDir()
	for _, d := range []string{"headers", "blocks/raw", "blocks/hex", "blocks/json"} {
		if err := os.MkdirAll(filepath.Join(dataDir, d), 0755); err != nil {
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
	_ = os.WriteFile(filepath.Join(dataDir, "blocks", "raw", prefix+".block"), payload, 0644)
	jb, _ := json.Marshal(view)
	_ = os.WriteFile(filepath.Join(dataDir, "blocks", "json", prefix+".json"), jb, 0644)
	a := &app{dataDir: dataDir, headersPath: filepath.Join(dataDir, "headers", "headers.bin"), status: appStatus{HeaderCount: 1, HeaderHeight: 0, Ready: true}, settings: appSettings{CacheBlocks: true, ServeData: true}, cacheIndex: newCacheIndex(), coverage: newCoverageState()}
	a.source = newOverlayServer(a)
	a.loadCacheIndex()
	t.Cleanup(func() { drainTestAppWriters064(t, a) })
	return a, payload
}

// Temporary profiles outlive all accepted fixture work. A completed index job
// can still have a delayed cache save pending, so waiting only for its job state
// lets TempDir cleanup race a real writer on Windows.
func drainTestAppWriters064(t *testing.T, a *app) {
	t.Helper()
	if a.network != nil {
		a.network.stop()
	}
	if a.source != nil {
		a.source.stopServer()
	}
	a.stopKnowledgeWritersForUpdate()
	a.indexMu.Lock()
	if a.indexCancel != nil {
		a.indexCancel()
	}
	a.indexMu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for {
		a.indexMu.Lock()
		active := a.indexCancel != nil
		a.indexMu.Unlock()
		if !active {
			break
		}
		if time.Now().After(deadline) {
			t.Error("fixture index writer did not stop before temporary profile cleanup")
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	done := make(chan struct{})
	go func() { a.updateBackgroundWork.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("fixture knowledge writer did not drain before temporary profile cleanup")
	}
}

func TestBIP434NegotiationBetweenBODPeers(t *testing.T) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Gateway exchange remains locked; ordinary Bitcoin serving is tested separately")
	}
	a, _ := testAppWithGenesis(t)
	if err := a.source.start(); err != nil {
		t.Skipf("BOD port unavailable: %v", err)
	}
	defer a.source.stopServer()
	time.Sleep(20 * time.Millisecond)
	p, err := connectPeer(fmt.Sprintf("127.0.0.1:%d", overlayTCPPort))
	if err != nil {
		t.Fatal(err)
	}
	defer p.conn.Close()
	if !p.bod || p.bodWire != bodWireVersion {
		t.Fatalf("BOD not negotiated: bod=%v wire=%d", p.bod, p.bodWire)
	}
}

func TestOrdinaryBitcoinPeerRemainsUsableWithoutBOD(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		c, e := ln.Accept()
		if e != nil {
			done <- e
			return
		}
		defer c.Close()
		// Receive client's version.
		m, e := readMessage(c)
		if e != nil || m.command != "version" {
			done <- fmt.Errorf("version: %v %s", e, m.command)
			return
		}
		// Advertise >=70017 but deliberately no BOD feature.
		if e = writeMessage(c, "version", makeVersionPayload(c.RemoteAddr().String(), 0)); e != nil {
			done <- e
			return
		}
		gotVerack := false
		deadline := time.Now().Add(3 * time.Second)
		_ = c.SetReadDeadline(deadline)
		for !gotVerack {
			m, e = readMessage(c)
			if e != nil {
				done <- e
				return
			}
			if m.command == "verack" {
				gotVerack = true
			}
			// feature is valid but intentionally ignored.
		}
		if e = writeMessage(c, "verack", nil); e != nil {
			done <- e
			return
		}
		done <- nil
	}()
	p, err := connectPeer(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer p.conn.Close()
	if p.bod {
		t.Fatal("ordinary peer was incorrectly marked BOD")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestUnknownIsNotNegative(t *testing.T) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Gateway exchange remains locked; ordinary Bitcoin serving is tested separately")
	}
	a, _ := testAppWithGenesis(t)
	if err := a.source.start(); err != nil {
		t.Skipf("port: %v", err)
	}
	defer a.source.stopServer()
	time.Sleep(20 * time.Millisecond)
	_, err := queryOverlayPeer(overlayPeer{Addr: fmt.Sprintf("127.0.0.1:%d", overlayTCPPort)}, overlayRequest{Type: "txloc", TxID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err == nil || !errors.Is(err, errBODUnknown) {
		t.Fatalf("wanted distinct unknown, got %v", err)
	}
}

func TestFalseTxLocationRejectedByBitcoinVerification(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	fakeTx := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	loc := txLocation{TxID: fakeTx, BlockHash: genesisHashDisplay, Height: 0, TxIndex: 0}
	_, err := a.verifyTxLocation(fakeTx, loc, "malicious test peer")
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("was not in it")) {
		t.Fatalf("false locator not rejected: %v", err)
	}
	if _, e := a.cachedTxLocation(fakeTx); e == nil {
		t.Fatal("false claim poisoned verified index")
	}
}

func TestRangeSyncPersistsCoverage(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.loadCoverage()
	a.loadSyncState()
	if err := a.startSync(syncRequest{Mode: "range", FromHeight: 0, ToHeight: 0}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		st := a.getSyncState()
		if st.Complete {
			break
		}
		if st.Error != "" {
			t.Fatal(st.Error)
		}
		time.Sleep(25 * time.Millisecond)
	}
	st := a.getSyncState()
	if !st.Complete || st.Current != 0 {
		t.Fatalf("sync state: %+v", st)
	}
	a.coverageMu.RLock()
	c := a.coverage
	a.coverageMu.RUnlock()
	if len(c.IndexedBlocks) != 1 || c.IndexedBlocks[0].From != 0 || c.IndexedBlocks[0].To != 0 {
		t.Fatalf("coverage: %+v", c)
	}
	// A fresh app can read the persisted coverage.
	b := &app{dataDir: a.dataDir}
	b.loadCoverage()
	b.coverageMu.RLock()
	defer b.coverageMu.RUnlock()
	if len(b.coverage.IndexedBlocks) != 1 {
		t.Fatal("coverage did not persist")
	}
}

func TestFeaturePayloadRoundTrip(t *testing.T) {
	raw := makeFeaturePayload(bodFeatureID, bodFeatureData())
	id, data, err := parseFeaturePayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if id != bodFeatureID {
		t.Fatalf("id %q", id)
	}
	if w, ok := featureWireVersion(data); !ok || w != 1 {
		t.Fatalf("wire %d %v", w, ok)
	}
}

func TestStandardGetDataSerialization(t *testing.T) {
	var b bytes.Buffer
	b.Write(encodeVarInt(1))
	_ = binary.Write(&b, binary.LittleEndian, uint32(2))
	raw, _ := displayHashRaw(genesisHashDisplay)
	b.Write(raw[:])
	if b.Len() != 37 {
		t.Fatalf("getdata len %d", b.Len())
	}
}

func TestResumeRangeSyncFromPersistedState(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.loadCoverage()
	a.syncJob = syncJobState{Mode: "range", From: 0, To: 0, SnapshotTip: 0, Current: -1, Running: true, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := a.saveSyncState(); err != nil {
		t.Fatal(err)
	}
	b := &app{dataDir: a.dataDir, headersPath: a.headersPath, status: appStatus{HeaderCount: 1, HeaderHeight: 0, Ready: true}, settings: appSettings{CacheBlocks: true, ServeData: true}, cacheIndex: newCacheIndex(), coverage: newCoverageState()}
	b.source = newOverlayServer(b)
	b.loadCacheIndex()
	b.loadCoverage()
	b.loadSyncState()
	b.resumeSyncIfNeeded()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		st := b.getSyncState()
		if st.Complete {
			break
		}
		if st.Error != "" {
			t.Fatal(st.Error)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if st := b.getSyncState(); !st.Complete || st.Current != 0 {
		t.Fatalf("resume failed: %+v", st)
	}
}

func TestV035DataCompatibilityNeedsNoHeaderResync(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	before, err := os.ReadFile(a.headersPath)
	if err != nil {
		t.Fatal(err)
	}
	c := currentCompatibility()
	if c.HeaderResyncRequired || c.RawBlockRedownload || c.IndexRebuild || c.CacheMetadataRebuild {
		t.Fatalf("unexpected migration cost: %+v", c)
	}
	// Loading the schema-2 v0.3.5-style cache index must preserve it in place.
	a.loadCacheIndex()
	after, err := os.ReadFile(a.headersPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("header file changed during 0.3.6 migration")
	}
}
