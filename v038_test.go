package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestV038PendingHeaderVerificationDoesNotRejectBlock(t *testing.T) {
	txHex := "0100000001" +
		"0000000000000000000000000000000000000000000000000000000000000000ffffffff" +
		"4d04ffff001d0104455468652054696d65732030332f4a616e2f32303039204368616e63656c6c6f72206f6e206272696e6b206f66207365636f6e64206261696c6f757420666f722062616e6b73" +
		"ffffffff01" + "00f2052a01000000" + "43" +
		"4104678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f61deb649f6bc3f4cef38c4f35504e51ec112de5c384df7ba0b8d578a4c702b6bf11d5fac" +
		"00000000"
	h, _ := hex.DecodeString(genesisHeaderHex)
	tx, _ := hex.DecodeString(txHex)
	payload := append(append(append([]byte{}, h...), 0x01), tx...)
	view, err := parseBlockDetailed(0, genesisHashDisplay, nil, payload, "peer", false)
	if err != nil {
		t.Fatal(err)
	}
	if view.VerificationState != "pending_header_validation" || view.Verification.HeaderChainMatch {
		t.Fatalf("expected pending local header verification, got state=%q match=%v", view.VerificationState, view.Verification.HeaderChainMatch)
	}
	if !view.Verification.HeaderHash || !view.Verification.ProofOfWork || !view.Verification.MerkleRoot {
		t.Fatalf("block-level checks should still pass while chain anchoring is pending: %+v", view.Verification)
	}
}

func TestV038PrefersConfiguredLocalCoreForHeaders(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "bitcoin.conf"), []byte("port=18444\n"), 0644); err != nil {
		t.Fatal(err)
	}
	a := &app{settings: appSettings{BitcoinDataDir: d}, preferred: []string{"example.invalid:8333"}}
	peers, core := a.headerPreferredPeers()
	if core != "127.0.0.1:18444" {
		t.Fatalf("core addr %q", core)
	}
	if len(peers) < 2 || peers[0] != core {
		t.Fatalf("local Core should be first header source: %#v", peers)
	}
}

func TestV038CustomSkinResolution(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "index.html"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}
	got, name, err := resolveSkinPath(d)
	if err != nil || got == "" || name != filepath.Base(d) {
		t.Fatalf("skin resolve got=%q name=%q err=%v", got, name, err)
	}
}

func TestV038CompatibilityReusesData(t *testing.T) {
	c := currentCompatibility()
	if c.AppVersion != appVersion || c.WireProtocol != 1 || c.StorageSchema != 2 || c.HeaderSchema != 1 {
		t.Fatalf("unexpected compatibility: %+v", c)
	}
	if c.HeaderResyncRequired || c.RawBlockRedownload || c.IndexRebuild || c.CacheMetadataRebuild {
		t.Fatalf("0.3.9 should reuse 0.3.8 data: %+v", c)
	}
}
