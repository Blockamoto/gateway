package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSatlineStorePersistsRecordsAndStatus(t *testing.T) {
	a := &app{dataDir: t.TempDir(), settings: defaultSettings()}
	a.initSatlineStore()
	if a.satlineInitErr != "" {
		t.Fatal(a.satlineInitErr)
	}
	sat := uint64(7)
	res := satlineResult{Mode: "sat", SatNumber: &sat, State: "CURRENTLY_UNSPENT", Hops: []satlineHop{{Index: 0, Type: "output_to_output", BlockHeight: 1, BlockHash: testID(1)}}, HopCount: 1}
	if err := a.saveSatlineRecord("sat", "7", "7", res); err != nil {
		t.Fatal(err)
	}
	rec, ok := a.loadSatlineRecord("sat", "7")
	if !ok || rec.Result.State != "CURRENTLY_UNSPENT" || len(rec.Result.Hops) != 1 {
		t.Fatalf("record not restored: %+v ok=%v", rec, ok)
	}
	st := a.satlineStatus()
	if st.Enabled || !st.Ready || st.StoredSats != 1 || st.PersistedHops != 1 || st.StorageSchema != 1 {
		t.Fatalf("bad status: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(a.satlineRoot(), "metadata.json")); err != nil {
		t.Fatal(err)
	}
}

func TestSatlineCheckpointValidationStopsAtReorg(t *testing.T) {
	start := satlinePoint{TxID: testID(10), Vout: 0, Offset: 0, Height: 0, BlockHash: testID(100)}
	p1 := satlinePoint{TxID: testID(11), Vout: 0, Offset: 0, Height: 1, BlockHash: testID(101)}
	p2 := satlinePoint{TxID: testID(12), Vout: 0, Offset: 0, Height: 2, BlockHash: testID(102)}
	rec := satlineRecord{Schema: 1, Kind: "satpoint", Key: "x", Result: satlineResult{Mode: "satpoint", StartSatpoint: &start, Hops: []satlineHop{
		{Index: 0, BlockHeight: 1, BlockHash: testID(101), Source: start, Destination: p1},
		{Index: 1, BlockHeight: 2, BlockHash: testID(102), Source: p1, Destination: p2},
	}}}
	chain := map[int64]string{0: testID(100), 1: testID(101), 2: testID(999)}
	valid, startOK := validateSatlineRecord(rec, func(h int64) (string, error) { return chain[h], nil })
	if !startOK || valid != 1 {
		t.Fatalf("expected one valid hop before reorg, got start=%v hops=%d", startOK, valid)
	}
}

func TestSatlineModuleCanBeDisabledIndependently(t *testing.T) {
	s := defaultSettings()
	s.SatlineEnabled = false
	a := &app{dataDir: t.TempDir(), settings: s}
	a.initSatlineStore()
	r := a.resolveSatlineSat(0, 0)
	if r.State != "MODULE_DISABLED" {
		t.Fatalf("expected disabled Satline, got %+v", r)
	}
	mods := a.bundledModuleManifests()
	if len(mods) < 2 || mods[0].Name != "Bitcoin on Demand" || mods[0].Status != "bundled_active" || mods[1].Name != "Satline" || mods[1].Status != "locked" {
		t.Fatalf("module isolation failed: %+v", mods)
	}
}

func TestSatlineProgressEmitsCompletedHop(t *testing.T) {
	coin := testID(201)
	spend := testID(202)
	b0 := testBlock(0, testCoinbase(coin, 100))
	b1 := testBlock(1, testCoinbase(testID(203), subsidyAtHeight(1)), testTx(spend, []inputView{{PrevTxID: coin, PrevVout: 0}}, 100))
	backend := &memorySatlineBackend{blocks: map[int64]blockView{0: b0, 1: b1}, tip: 1}
	r := newSatlineResolver(backend)
	emitted := 0
	r.progress = func(v satlineResult) {
		if len(v.Hops) > 0 {
			emitted = len(v.Hops)
		}
	}
	out := r.resolveSat(7, 0)
	if out.State != "CURRENTLY_UNSPENT" || emitted < 1 {
		t.Fatalf("expected per-hop progress persistence hook, out=%+v emitted=%d", out, emitted)
	}
}

func TestDefaultSettingsKeepSatlineLocked(t *testing.T) {
	if defaultSettings().SatlineEnabled {
		t.Fatal("Satline should be disabled by default in the testing release")
	}
}

func TestSatlineProtocolIdentityReservedButNotAdvertised(t *testing.T) {
	for _, p := range localGatewayProtocols() {
		if p.ID == satlineProtocolID {
			t.Fatal("Satline must not be advertised on Gateway before the networking release")
		}
	}
}

func TestSatlinePersistentSatReusesBirthCheckpoint(t *testing.T) {
	if !releaseFeatureAvailable("satline") {
		t.Skip("0.6.6 release lock: persistent Satline traversal is unavailable")
	}
	a, genesis := initGraphTestApp(t)
	a.settings.SatlineEnabled = true
	a.initSatlineStore()
	if err := a.graphIndexBlock(genesis, true); err != nil {
		t.Fatal(err)
	}
	first := a.resolveSatlineSatPersistent(0, 0, false)
	if first.State != "CURRENTLY_UNSPENT" || first.Persistence == nil || !first.Persistence.Stored {
		t.Fatalf("first persistent resolve failed: %+v", first)
	}
	second := a.resolveSatlineSatPersistent(0, 0, false)
	if second.State != "CURRENTLY_UNSPENT" || second.Persistence == nil || second.Persistence.ReusedCheckpoints < 1 {
		t.Fatalf("second resolve did not reuse birth checkpoint: %+v", second)
	}
	st := a.satlineStatus()
	if st.StoredSats != 1 {
		t.Fatalf("expected one stored sat, got %+v", st)
	}
}

func TestSatlineMetadataFailureIsIsolatedAndClearRepairs(t *testing.T) {
	a := &app{dataDir: t.TempDir(), settings: defaultSettings()}
	if err := os.MkdirAll(a.satlineRoot(), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.satlineMetaPath(), []byte("{not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	a.initSatlineStore()
	mods := a.bundledModuleManifests()
	if len(mods) < 2 || mods[0].Status != "bundled_active" || mods[1].Status != "locked" || a.satlineStatus().Error == "" {
		t.Fatalf("Satline failure leaked into BOD/module host: %+v", mods)
	}
	if err := a.clearSatlineStore(); err != nil {
		t.Fatal(err)
	}
	if st := a.satlineStatus(); !st.Ready || st.Error != "" {
		t.Fatalf("clear should repair Satline metadata: %+v", st)
	}
}
