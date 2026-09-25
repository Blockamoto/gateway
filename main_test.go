package main

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGenesisHeader(t *testing.T) {
	h, _ := hex.DecodeString(genesisHeaderHex)
	got := hash256(h)
	if reverseHex(got[:]) != genesisHashDisplay {
		t.Fatalf("genesis hash mismatch")
	}
	if err := verifyPoW(h); err != nil {
		t.Fatalf("genesis PoW: %v", err)
	}
}

func TestGenesisTransactionParseAndMerkle(t *testing.T) {
	txHex := "0100000001" +
		"0000000000000000000000000000000000000000000000000000000000000000ffffffff" +
		"4d04ffff001d0104455468652054696d65732030332f4a616e2f32303039204368616e63656c6c6f72206f6e206272696e6b206f66207365636f6e64206261696c6f757420666f722062616e6b73" +
		"ffffffff01" +
		"00f2052a01000000" +
		"43" +
		"4104678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f61deb649f6bc3f4cef38c4f35504e51ec112de5c384df7ba0b8d578a4c702b6bf11d5fac" +
		"00000000"
	b, err := hex.DecodeString(txHex)
	if err != nil {
		t.Fatal(err)
	}
	p := &byteParser{b: b}
	tx, err := parseTransaction(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if tx.TxID != "4a5e1e4baab89f3a32518a88c31bc87f618f76673e2cc77ab2127b7afdeda33b" {
		t.Fatalf("txid %s", tx.TxID)
	}
	if !tx.Coinbase || tx.OutputSats != 5_000_000_000 {
		t.Fatalf("bad genesis tx parse")
	}
	root := merkleRoot([][32]byte{tx._txHash})
	if reverseHex(root[:]) != tx.TxID {
		t.Fatalf("single-tx merkle mismatch")
	}
}

func TestScriptAddressTypes(t *testing.T) {
	p2wpkh, _ := hex.DecodeString("0014751e76e8199196d454941c45d1b3a323f1433bd6")
	typ, addr := describeScript(p2wpkh)
	if typ != "p2wpkh" || addr != "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4" {
		t.Fatalf("got %s %s", typ, addr)
	}
}

func TestGenesisBlockDetailed(t *testing.T) {
	txHex := "0100000001" +
		"0000000000000000000000000000000000000000000000000000000000000000ffffffff" +
		"4d04ffff001d0104455468652054696d65732030332f4a616e2f32303039204368616e63656c6c6f72206f6e206272696e6b206f66207365636f6e64206261696c6f757420666f722062616e6b73" +
		"ffffffff01" +
		"00f2052a01000000" +
		"43" +
		"4104678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f61deb649f6bc3f4cef38c4f35504e51ec112de5c384df7ba0b8d578a4c702b6bf11d5fac" +
		"00000000"
	h, _ := hex.DecodeString(genesisHeaderHex)
	tx, _ := hex.DecodeString(txHex)
	payload := append(append(append([]byte{}, h...), 0x01), tx...)
	view, err := parseBlockDetailed(0, genesisHashDisplay, h, payload, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	if view.TransactionCount != 1 || !view.Verification.MerkleRoot || view.SerializedBytes != 285 {
		t.Fatalf("unexpected genesis block parse: txs=%d merkle=%v size=%d", view.TransactionCount, view.Verification.MerkleRoot, view.SerializedBytes)
	}
}

func TestBODCoordinates(t *testing.T) {
	cases := []struct {
		in     string
		kind   coordinateKind
		h      int64
		tx, io int
	}{
		{"127.840000", coordTransaction, 840000, 127, -1},
		{"2.127.840000", coordOutput, 840000, 127, 2},
		{"i3.127.840000", coordInput, 840000, 127, 3},
	}
	for _, c := range cases {
		got, ok := parseBODCoordinate(c.in)
		if !ok || got.Kind != c.kind || got.Height != c.h || got.TxIndex != c.tx || got.IOIndex != c.io {
			t.Fatalf("parse %s: %+v ok=%v", c.in, got, ok)
		}
	}
	if txCoordinate(127, 840000) != "127.840000" || outputCoordinate(2, 127, 840000) != "2.127.840000" || inputCoordinate(3, 127, 840000) != "i3.127.840000" {
		t.Fatal("coordinate formatting mismatch")
	}
}

func TestFetchCachedGenesisUnifiedStorage(t *testing.T) {
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
	raw := append(append(append([]byte{}, h...), 0x01), tx...)
	prefix := "0-" + genesisHashDisplay
	if err := os.WriteFile(filepath.Join(dataDir, "blocks", "raw", prefix+".block"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	a := &app{dataDir: dataDir, headersPath: filepath.Join(dataDir, "headers", "headers.bin"), settings: appSettings{CacheBlocks: false}, status: appStatus{HeaderCount: 1, HeaderHeight: 0, Ready: true}, cacheIndex: newCacheIndex()}
	a.cacheIndex.Blocks[genesisHashDisplay] = cachedBlockEntry{Height: 0, Hash: genesisHashDisplay, Prefix: prefix}
	v, err := a.fetchAndDecode("0")
	if err != nil {
		t.Fatal(err)
	}
	if v.Transactions[0].Coordinate != "0.0" || v.Transactions[0].Outputs[0].Coordinate != "0.0.0" {
		t.Fatalf("coords %+v", v.Transactions[0])
	}
}

func TestLoadCacheIndexAndLocalStorage(t *testing.T) {
	dataDir := t.TempDir()
	for _, d := range []string{"headers", "blocks/raw", "blocks/hex", "blocks/json"} {
		_ = os.MkdirAll(filepath.Join(dataDir, d), 0755)
	}
	h, _ := hex.DecodeString(genesisHeaderHex)
	_ = os.WriteFile(filepath.Join(dataDir, "headers", "headers.bin"), h, 0644)
	txHex := "0100000001" + "0000000000000000000000000000000000000000000000000000000000000000ffffffff" + "4d04ffff001d0104455468652054696d65732030332f4a616e2f32303039204368616e63656c6c6f72206f6e206272696e6b206f66207365636f6e64206261696c6f757420666f722062616e6b73" + "ffffffff01" + "00f2052a01000000" + "43" + "4104678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f61deb649f6bc3f4cef38c4f35504e51ec112de5c384df7ba0b8d578a4c702b6bf11d5fac" + "00000000"
	tx, _ := hex.DecodeString(txHex)
	raw := append(append(append([]byte{}, h...), 0x01), tx...)
	pre := "0-" + genesisHashDisplay
	_ = os.WriteFile(filepath.Join(dataDir, "blocks", "raw", pre+".block"), raw, 0644)
	idx := newCacheIndex()
	idx.Blocks[genesisHashDisplay] = cachedBlockEntry{Height: 0, Hash: genesisHashDisplay, Prefix: pre}
	b, _ := json.Marshal(idx)
	_ = os.WriteFile(filepath.Join(dataDir, "cache-index.json"), b, 0644)
	a := &app{dataDir: dataDir, headersPath: filepath.Join(dataDir, "headers", "headers.bin"), settings: appSettings{}, status: appStatus{HeaderCount: 1, HeaderHeight: 0, Ready: true}, cacheIndex: newCacheIndex()}
	a.loadCacheIndex()
	if len(a.cacheIndex.Blocks) != 1 {
		t.Fatalf("cache blocks %d", len(a.cacheIndex.Blocks))
	}
	target, _ := a.localBlockTarget(0)
	hit, err := a.localStorageBlock(target)
	if err != nil {
		t.Fatal(err)
	}
	if hit.Network != "cache" {
		t.Fatalf("hit %+v", hit)
	}
}
