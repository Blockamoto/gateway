package main

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestStripBlockWitnessHonorsBIP144Serialization(t *testing.T) {
	header := make([]byte, 80)
	// One deliberately simple witness transaction. It need not be consensus
	// valid for this serialization test; the byte layout is valid.
	tx := []byte{
		0x01, 0x00, 0x00, 0x00, // version
		0x00, 0x01, // marker + flag
		0x01, // vin count
	}
	tx = append(tx, bytes.Repeat([]byte{0x11}, 32)...)
	tx = append(tx, 0x00, 0x00, 0x00, 0x00)                         // prev vout
	tx = append(tx, 0x00)                                           // scriptSig length
	tx = append(tx, 0xff, 0xff, 0xff, 0xff)                         // sequence
	tx = append(tx, 0x01)                                           // vout count
	tx = append(tx, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00) // 1 sat
	tx = append(tx, 0x00)                                           // scriptPubKey length
	tx = append(tx, 0x01, 0x01, 0xaa)                               // witness: 1 item, 1 byte, aa
	tx = append(tx, 0x00, 0x00, 0x00, 0x00)                         // locktime

	block := append(append(append([]byte{}, header...), 0x01), tx...)
	stripped, err := stripBlockWitness(block)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stripped[81:], []byte{0x00, 0x01, 0x01}) && len(stripped) == len(block) {
		t.Fatal("witness serialization was not removed")
	}
	if len(stripped) != len(block)-5 { // marker+flag (2) + witness encoding (3)
		t.Fatalf("stripped length %d, want %d", len(stripped), len(block)-5)
	}
	if !bytes.Equal(stripped[:80], header) || stripped[80] != 0x01 {
		t.Fatal("block header or transaction count changed")
	}
}

func ordinaryBitcoinHandshake(t *testing.T, addr string) (net.Conn, uint64) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	first, err := readMessage(c)
	if err != nil || first.command != "version" || len(first.payload) < 12 {
		c.Close()
		t.Fatalf("server version: %s %v", first.command, err)
	}
	services := binary.LittleEndian.Uint64(first.payload[4:12])
	if err := writeMessage(c, "version", makeVersionPayload(addr, 0)); err != nil {
		c.Close()
		t.Fatal(err)
	}
	if err := writeMessage(c, "verack", nil); err != nil {
		c.Close()
		t.Fatal(err)
	}
	gotVerack := false
	for !gotVerack {
		m, err := readMessage(c)
		if err != nil {
			c.Close()
			t.Fatal(err)
		}
		switch m.command {
		case "verack":
			gotVerack = true
		case "feature":
			if !releaseFeatureAvailable("gateway-peerhood") {
				t.Fatal("locked Gateway feature offered to ordinary Bitcoin peer")
			}
		case "ping":
			_ = writeMessage(c, "pong", m.payload)
		default:
			// In particular, ignore the optional BIP434 feature proposal. This
			// client deliberately never sends a Gateway feature response.
		}
	}
	_ = c.SetDeadline(time.Time{})
	return c, services
}

func TestOrdinaryBitcoinPeerGetsMountedCoreBlockWithoutGateway(t *testing.T) {
	dataDir := t.TempDir()
	blocksDir := t.TempDir()
	payload := testGenesisBlockPayload(t)
	writeSyntheticCoreBlockFile(t, blocksDir, payload, [8]byte{})
	a := newCoreMountTestApp(t, dataDir, blocksDir)
	a.refreshCoreBlockStore()
	a.settings.ServeData = true
	a.gatewayListen = "127.0.0.1:0"
	s := newOverlayServer(a)
	a.source = s
	if err := s.start(); err != nil {
		t.Skipf("serving port unavailable: %v", err)
	}
	defer s.stopServer()

	addr := s.tcp.Addr().String()
	c, services := ordinaryBitcoinHandshake(t, addr)
	defer c.Close()
	if services&nodeNetworkService != 0 {
		t.Fatal("Gateway Client must not advertise NODE_NETWORK for sparse block serving")
	}
	if services&nodeWitnessService == 0 {
		t.Fatal("serving peer should advertise NODE_WITNESS because witness block responses are supported")
	}

	rawHash, _ := displayHashRaw(genesisHashDisplay)
	var req bytes.Buffer
	req.Write(encodeVarInt(1))
	_ = binary.Write(&req, binary.LittleEndian, uint32(2)) // MSG_BLOCK
	req.Write(rawHash[:])
	if err := writeMessage(c, "getdata", req.Bytes()); err != nil {
		t.Fatal(err)
	}
	m, err := waitForCommand(&peerConn{conn: c}, "block", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(m.payload, payload) {
		t.Fatalf("served block differs from mounted Core bytes: got %d want %d", len(m.payload), len(payload))
	}
	st := s.bitcoinServingStatus()
	// TCP delivery can precede the serving goroutine's accounting commit.
	// Wait for that completion, not an assumed scheduler ordering.
	deadline := time.Now().Add(time.Second)
	for st.GetDataRequests == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		st = s.bitcoinServingStatus()
	}
	if st.BlocksServed != 1 || st.GetDataRequests != 1 || st.NODE_NETWORK || st.NODE_NETWORK_LIMITED {
		t.Fatalf("unexpected serving status: %+v", st)
	}
}

func TestStandardGetDataReturnsMultiNotFound(t *testing.T) {
	a := &app{settings: appSettings{ServeData: true}, cacheIndex: newCacheIndex(), gatewayListen: "127.0.0.1:0"}
	s := newOverlayServer(a)
	a.source = s
	if err := s.start(); err != nil {
		t.Skipf("serving port unavailable: %v", err)
	}
	defer s.stopServer()
	c, _ := ordinaryBitcoinHandshake(t, s.tcp.Addr().String())
	defer c.Close()

	var req bytes.Buffer
	req.Write(encodeVarInt(2))
	for i := byte(1); i <= 2; i++ {
		_ = binary.Write(&req, binary.LittleEndian, uint32(2))
		req.Write(bytes.Repeat([]byte{i}, 32))
	}
	if err := writeMessage(c, "getdata", req.Bytes()); err != nil {
		t.Fatal(err)
	}
	m, err := waitForCommand(&peerConn{conn: c}, "notfound", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	n, used, err := decodeCompactSizeMinimal(m.payload)
	if err != nil || n != 2 || len(m.payload) != used+72 {
		t.Fatalf("notfound payload count=%d len=%d err=%v", n, len(m.payload), err)
	}
}
