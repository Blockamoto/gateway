package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func testGenesisBlockPayload(t *testing.T) []byte {
	t.Helper()
	txHex := "0100000001" +
		"0000000000000000000000000000000000000000000000000000000000000000ffffffff" +
		"4d04ffff001d0104455468652054696d65732030332f4a616e2f32303039204368616e63656c6c6f72206f6e206272696e6b206f66207365636f6e64206261696c6f757420666f722062616e6b73" +
		"ffffffff01" + "00f2052a01000000" + "43" +
		"4104678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f61deb649f6bc3f4cef38c4f35504e51ec112de5c384df7ba0b8d578a4c702b6bf11d5fac" + "00000000"
	h, err := hex.DecodeString(genesisHeaderHex)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := hex.DecodeString(txHex)
	if err != nil {
		t.Fatal(err)
	}
	return append(append(append([]byte{}, h...), 0x01), tx...)
}

func writeSyntheticCoreBlockFile(t *testing.T, blocksDir string, payload []byte, key [8]byte) string {
	t.Helper()
	if err := os.MkdirAll(blocksDir, 0755); err != nil {
		t.Fatal(err)
	}
	record := make([]byte, 8+len(payload))
	copy(record[:4], mainnetDiskMagic[:])
	binary.LittleEndian.PutUint32(record[4:8], uint32(len(payload)))
	copy(record[8:], payload)
	coreXOR(record, 0, key)
	path := filepath.Join(blocksDir, "blk00000.dat")
	if err := os.WriteFile(path, record, 0644); err != nil {
		t.Fatal(err)
	}
	if key != ([8]byte{}) {
		xorFile := append([]byte{8}, key[:]...) // Core serializes the 8-byte key as a vector.
		if err := os.WriteFile(filepath.Join(blocksDir, "xor.dat"), xorFile, 0644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func newCoreMountTestApp(t *testing.T, dataDir, blocksDir string) *app {
	t.Helper()
	headersDir := filepath.Join(dataDir, "headers")
	if err := os.MkdirAll(headersDir, 0755); err != nil {
		t.Fatal(err)
	}
	h, _ := hex.DecodeString(genesisHeaderHex)
	if err := os.WriteFile(filepath.Join(headersDir, "headers.bin"), h, 0644); err != nil {
		t.Fatal(err)
	}
	a := &app{
		dataDir:     dataDir,
		headersPath: filepath.Join(headersDir, "headers.bin"),
		settings: appSettings{
			CacheBlocks:      true,
			BitcoinBlocksDir: blocksDir,
			RPCAuthMode:      "auto",
		},
		status:     appStatus{HeaderCount: 1, HeaderHeight: 0, Ready: true},
		cacheIndex: newCacheIndex(),
	}
	a.initCoreBlockStore()
	return a
}

func TestCoreStoreOfflineXORMountAndRestart(t *testing.T) {
	dataDir := t.TempDir()
	blocksDir := filepath.Join(t.TempDir(), "blocks")
	payload := testGenesisBlockPayload(t)
	key := [8]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	coreFile := writeSyntheticCoreBlockFile(t, blocksDir, payload, key)
	before, err := os.ReadFile(coreFile)
	if err != nil {
		t.Fatal(err)
	}

	a := newCoreMountTestApp(t, dataDir, blocksDir)
	a.refreshCoreBlockStore()
	st := a.coreStoreStatusView()
	if !st.Mounted || !st.ScanComplete || !st.XOREnabled || st.IndexedBlocks != 1 || st.AnchoredBlocks != 1 {
		t.Fatalf("unexpected mount status: %+v", st)
	}
	bd, err := a.mountedCoreBlock(genesisHashDisplay)
	if err != nil {
		t.Fatal(err)
	}
	if bd.Height != 0 || !bytes.Equal(bd.Raw, payload) {
		t.Fatalf("mounted block mismatch: height=%d bytes=%d", bd.Height, len(bd.Raw))
	}

	target, err := a.localBlockTarget(0)
	if err != nil {
		t.Fatal(err)
	}
	view, err := a.fetchAndDecodeTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	if view.SourceNetwork != "core_mount" || view.VerificationState != "header_anchored" || view.Verification.ConsensusValidated {
		t.Fatalf("offline Core mount verification semantics wrong: source=%s state=%s consensus=%v", view.SourceNetwork, view.VerificationState, view.Verification.ConsensusValidated)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "blocks", "raw", "0-"+genesisHashDisplay+".block")); !os.IsNotExist(err) {
		t.Fatalf("mounted Core block was duplicated into BOD raw storage; stat err=%v", err)
	}
	after, err := os.ReadFile(coreFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("Core blk*.dat was modified")
	}

	// Restart against the same BOD data directory. The persisted locator should
	// reload and its active-chain height should be re-derived from local headers.
	a2 := newCoreMountTestApp(t, dataDir, blocksDir)
	a2.refreshCoreBlockStore()
	bd2, err := a2.mountedCoreBlock(genesisHashDisplay)
	if err != nil {
		t.Fatal(err)
	}
	if bd2.Height != 0 || !bytes.Equal(bd2.Raw, payload) {
		t.Fatalf("restart failed to reuse mount: height=%d bytes=%d", bd2.Height, len(bd2.Raw))
	}
}

func TestCoreStorePrunedFileDisappearsFromLiveMount(t *testing.T) {
	dataDir := t.TempDir()
	blocksDir := filepath.Join(t.TempDir(), "blocks")
	coreFile := writeSyntheticCoreBlockFile(t, blocksDir, testGenesisBlockPayload(t), [8]byte{})
	a := newCoreMountTestApp(t, dataDir, blocksDir)
	a.refreshCoreBlockStore()
	if _, err := a.mountedCoreBlock(genesisHashDisplay); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(coreFile); err != nil {
		t.Fatal(err)
	}
	a.refreshCoreBlockStore()
	if _, err := a.mountedCoreBlock(genesisHashDisplay); err == nil {
		t.Fatal("pruned/deleted Core block file remained live in the mount")
	}
}

func TestResolveCoreBlocksDirHonorsBitcoinBlocksDirSemantics(t *testing.T) {
	dataDir := t.TempDir()
	external := filepath.Join(t.TempDir(), "external-block-root")
	if err := os.WriteFile(filepath.Join(dataDir, "bitcoin.conf"), []byte("blocksdir="+external+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := resolveCoreBlocksDir(appSettings{BitcoinDataDir: dataDir})
	want := filepath.Join(external, "blocks")
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("blocksdir resolution: got %q want %q", got, want)
	}

	explicit := filepath.Join(t.TempDir(), "already-the-blocks-dir")
	got = resolveCoreBlocksDir(appSettings{BitcoinDataDir: dataDir, BitcoinBlocksDir: explicit})
	if filepath.Clean(got) != filepath.Clean(explicit) {
		t.Fatalf("explicit bitcoin_blocks_dir should directly contain blk*.dat: got %q want %q", got, explicit)
	}
}

func TestUnifiedContinuationCombinesMountedCoreAndBODKnowledge(t *testing.T) {
	dataDir := t.TempDir()
	blocksDir := filepath.Join(t.TempDir(), "blocks")
	writeSyntheticCoreBlockFile(t, blocksDir, testGenesisBlockPayload(t), [8]byte{})
	a := newCoreMountTestApp(t, dataDir, blocksDir)
	a.refreshCoreBlockStore()
	if got := a.unifiedContinuationHeight(); got != 1 {
		t.Fatalf("mounted genesis should make continuation height 1, got %d", got)
	}

	// Add one locally header-anchored BOD-owned knowledge row at height 1. The
	// continuation calculation should treat Core + BOD as one contiguous store.
	h2 := make([]byte, 80)
	prev := mustDisplayHashRawForTest(t, genesisHashDisplay)
	copy(h2[4:36], prev[:])
	h2[68] = 1
	d := hash256(h2)
	hash2 := reverseHex(d[:])
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(h2); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	a.status.HeaderCount = 2
	a.status.HeaderHeight = 1
	a.cacheIndex.Blocks[hash2] = cachedBlockEntry{Height: 1, Hash: hash2, Prefix: "1-" + hash2}
	if got := a.unifiedContinuationHeight(); got != 2 {
		t.Fatalf("Core height 0 + BOD height 1 should continue at 2, got %d", got)
	}
}

func mustDisplayHashRawForTest(t *testing.T, h string) [32]byte {
	t.Helper()
	raw, err := displayHashRaw(h)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCoreStoreSidecarIsBoundToSelectedBlocksDirectory(t *testing.T) {
	dataDir := t.TempDir()
	blocksA := filepath.Join(t.TempDir(), "blocks")
	blocksB := filepath.Join(t.TempDir(), "blocks")
	payloadA := testGenesisBlockPayload(t)
	writeSyntheticCoreBlockFile(t, blocksA, payloadA, [8]byte{})

	payloadB := append([]byte(nil), payloadA...)
	payloadB[76] ^= 0x01 // different header/hash, same valid serialized block shape for locator scanning
	writeSyntheticCoreBlockFile(t, blocksB, payloadB, [8]byte{})
	newHashRaw := hash256(payloadB[:80])
	newHash := reverseHex(newHashRaw[:])

	a := newCoreMountTestApp(t, dataDir, blocksA)
	a.refreshCoreBlockStore()
	if a.coreStoreStatusView().IndexedBlocks != 1 {
		t.Fatalf("expected one locator in first store")
	}

	a.settings.BitcoinBlocksDir = blocksB
	a.initCoreBlockStore()
	a.refreshCoreBlockStore()
	if st := a.coreStoreStatusView(); st.IndexedBlocks != 1 {
		t.Fatalf("switching Core stores mixed locator histories: %+v", st)
	}
	if _, err := a.mountedCoreLocationByHash(genesisHashDisplay); err == nil {
		t.Fatal("old Core store hash leaked into new mount")
	}
	if _, err := a.mountedCoreLocationByHash(newHash); err != nil {
		t.Fatalf("new Core store hash missing: %v", err)
	}

	// Restart must also load only the locator history belonging to blocksB.
	a2 := newCoreMountTestApp(t, dataDir, blocksB)
	a2.refreshCoreBlockStore()
	if st := a2.coreStoreStatusView(); st.IndexedBlocks != 1 {
		t.Fatalf("restart loaded mixed sidecar histories: %+v", st)
	}
	if _, err := a2.mountedCoreLocationByHash(genesisHashDisplay); err == nil {
		t.Fatal("stale locator survived restart after Core-store switch")
	}
}
