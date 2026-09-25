package main

import (
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestV038ExperimentalServiceBitIsBit25(t *testing.T) {
	if bodExperimentalServiceBit != uint64(1)<<25 {
		t.Fatalf("service mask = %x", bodExperimentalServiceBit)
	}
	p := makeVersionPayload("127.0.0.1:8333", bodExperimentalServiceBit)
	if len(p) < 12 {
		t.Fatal("short version payload")
	}
	services := binary.LittleEndian.Uint64(p[4:12])
	if services&bodExperimentalServiceBit == 0 {
		t.Fatalf("BOD bit not advertised: %x", services)
	}
}

func TestV038BIP434FeatureIdentityIsParseable(t *testing.T) {
	payload := makeFeaturePayload(bodFeatureID, bodFeatureData())
	id, data, err := parseFeaturePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if id != bodFeatureID || id == "" {
		t.Fatalf("feature id %q", id)
	}
	if w, ok := featureWireVersion(data); !ok || w != bodWireVersion {
		t.Fatalf("wire negotiation failed w=%d ok=%v", w, ok)
	}
}

func TestV038AddrServiceHintRoundTrip(t *testing.T) {
	p, err := makeAddrAnnouncement("127.0.0.1:48333", bodExperimentalServiceBit)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := parseAddrPayload(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Services&bodExperimentalServiceBit == 0 {
		t.Fatalf("bad addr rows: %#v", rows)
	}
	if rows[0].Addr != "127.0.0.1:48333" {
		t.Fatalf("addr = %q", rows[0].Addr)
	}
}

func TestV038StatusAdvertisesValidatedSparseRanges(t *testing.T) {
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, "headers"), 0755); err != nil {
		t.Fatal(err)
	}
	h, err := hex.DecodeString(genesisHeaderHex)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "headers", "headers.bin"), h, 0644); err != nil {
		t.Fatal(err)
	}
	a := &app{
		dataDir: d, headersPath: filepath.Join(d, "headers", "headers.bin"),
		status:   appStatus{HeaderCount: 1, HeaderHeight: 0, TipHash: genesisHashDisplay, Ready: true},
		settings: appSettings{ShareCache: true}, cacheIndex: newCacheIndex(), coverage: newCoverageState(),
	}
	a.cacheIndex.Blocks[genesisHashDisplay] = cachedBlockEntry{Height: 0, Hash: genesisHashDisplay, Prefix: "0-" + genesisHashDisplay}
	a.cacheIndex.Heights["0"] = genesisHashDisplay
	st := a.buildBODStatus()
	if st.Snapshot.Height != 0 || st.Snapshot.Hash != genesisHashDisplay {
		t.Fatalf("snapshot: %+v", st.Snapshot)
	}
	if len(st.BlockRanges) != 1 || st.BlockRanges[0].From != 0 || st.BlockRanges[0].To != 0 {
		t.Fatalf("ranges: %+v", st.BlockRanges)
	}
	if len(st.Profiles) == 0 || st.Profiles[0] != "history" {
		t.Fatalf("profiles: %+v", st.Profiles)
	}
}

func TestV038CompatibilityDoesNotForceExpensiveDataMigration(t *testing.T) {
	c := currentCompatibility()
	if c.AppVersion != appVersion || c.WireProtocol != 1 || c.HeaderSchema != 1 || c.StorageSchema != 2 {
		t.Fatalf("compatibility: %+v", c)
	}
	if c.HeaderResyncRequired || c.RawBlockRedownload || c.IndexRebuild || c.CacheMetadataRebuild {
		t.Fatalf("unexpected expensive migration: %+v", c)
	}
}

func TestV038PrivateCacheIsNotAdvertised(t *testing.T) {
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, "headers"), 0755); err != nil {
		t.Fatal(err)
	}
	h, err := hex.DecodeString(genesisHeaderHex)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "headers", "headers.bin"), h, 0644); err != nil {
		t.Fatal(err)
	}
	a := &app{
		dataDir: d, headersPath: filepath.Join(d, "headers", "headers.bin"),
		status:   appStatus{HeaderCount: 1, HeaderHeight: 0, TipHash: genesisHashDisplay, Ready: true},
		settings: appSettings{ShareCache: true, PrivacyMode: true}, cacheIndex: newCacheIndex(), coverage: newCoverageState(),
	}
	a.cacheIndex.Blocks[genesisHashDisplay] = cachedBlockEntry{Height: 0, Hash: genesisHashDisplay, Prefix: "0-" + genesisHashDisplay, Private: true}
	a.cacheIndex.Heights["0"] = genesisHashDisplay
	st := a.buildBODStatus()
	if len(st.BlockRanges) != 0 || st.CacheBlocks != 0 {
		t.Fatalf("private cache leaked into status: ranges=%+v blocks=%d", st.BlockRanges, st.CacheBlocks)
	}
}

func TestV038PublicCacheBroadcastCanBeDisabled(t *testing.T) {
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, "headers"), 0755); err != nil {
		t.Fatal(err)
	}
	h, err := hex.DecodeString(genesisHeaderHex)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "headers", "headers.bin"), h, 0644); err != nil {
		t.Fatal(err)
	}
	a := &app{
		dataDir: d, headersPath: filepath.Join(d, "headers", "headers.bin"),
		status:   appStatus{HeaderCount: 1, HeaderHeight: 0, TipHash: genesisHashDisplay, Ready: true},
		settings: appSettings{ShareCache: false}, cacheIndex: newCacheIndex(), coverage: newCoverageState(),
	}
	a.cacheIndex.Blocks[genesisHashDisplay] = cachedBlockEntry{Height: 0, Hash: genesisHashDisplay, Prefix: "0-" + genesisHashDisplay}
	st := a.buildBODStatus()
	if len(st.BlockRanges) != 0 || st.CacheBlocks != 0 {
		t.Fatalf("disabled public cache leaked into status: ranges=%+v blocks=%d", st.BlockRanges, st.CacheBlocks)
	}
}

func TestV038PrivateDerivedKnowledgeStaysLocal(t *testing.T) {
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, "headers"), 0755); err != nil {
		t.Fatal(err)
	}
	h, err := hex.DecodeString(genesisHeaderHex)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "headers", "headers.bin"), h, 0644); err != nil {
		t.Fatal(err)
	}
	txid := strings.Repeat("11", 32)
	a := &app{
		dataDir: d, headersPath: filepath.Join(d, "headers", "headers.bin"),
		status:   appStatus{HeaderCount: 1, HeaderHeight: 0, TipHash: genesisHashDisplay, Ready: true},
		settings: appSettings{ShareCache: true}, cacheIndex: newCacheIndex(), coverage: newCoverageState(),
	}
	a.cacheIndex.Tx[txid] = txLocation{TxID: txid, BlockHash: genesisHashDisplay, Height: 0, TxIndex: 0}
	a.cacheIndex.PrivateTx[txid] = true
	if _, err := a.cachedTxLocation(txid); err != nil {
		t.Fatalf("private knowledge should remain usable locally: %v", err)
	}
	if _, err := a.cachedTxLocationForServing(txid); err == nil {
		t.Fatal("private transaction knowledge was exposed for serving")
	}
}
