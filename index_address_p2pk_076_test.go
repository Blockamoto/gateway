package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const address076PublicKey = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
const address076UncompressedKey = "0479be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798" + "483ada7726a3c4655da4fbfc0e1108a8fd17b448a68554199c47d08ffb10d4b8"
const address076SameKeyP2PKH = "76a914751e76e8199196d454941c45d1b3a323f1433bd688ac"

func Test076P2PKDestinationStrictPointsAndExactEncodings(t *testing.T) {
	for _, key := range []string{address076PublicKey, "03" + address076PublicKey[2:], address076UncompressedKey} {
		name, script, kind, err := bitcoinDestinationScript(strings.ToUpper(key))
		raw, _ := hex.DecodeString(script)
		if err != nil || name != key || kind != "p2pk" || script != hex.EncodeToString(append(append([]byte{byte(len(key) / 2)}, mustDecodeAddress076(t, key)...), 0xac)) {
			t.Fatal(name, script, kind, err)
		}
		if typ, conventional := describeScript(raw); typ != "p2pk" || conventional != "" {
			t.Fatal("bare key fabricated a payment address", typ, conventional)
		}
		if typ, destination := describeDestinationScript(raw); typ != "p2pk" || destination != key {
			t.Fatal(typ, destination)
		}
	}
	for _, key := range []string{
		"02" + strings.Repeat("ff", 32), // x is outside the field.
		"02" + strings.Repeat("00", 32), // x^3+7 is not a curve square.
		"04" + address076UncompressedKey[2:66] + strings.Repeat("00", 32),
		"06" + address076UncompressedKey[2:], // Hybrid SEC encoding is not accepted.
		"00" + address076PublicKey[2:],
		address076PublicKey[:64], address076PublicKey + "00", strings.Repeat("z", 66),
	} {
		if _, _, _, err := bitcoinDestinationScript(key); err == nil {
			t.Fatal("invalid public key accepted", key)
		}
	}
	for _, key := range []string{"02" + strings.Repeat("00", 32), "04" + address076UncompressedKey[2:66] + strings.Repeat("00", 32)} {
		raw := append(append([]byte{byte(len(key) / 2)}, mustDecodeAddress076(t, key)...), 0xac)
		if _, destination := describeDestinationScript(raw); destination != "" {
			t.Fatal("off-curve script indexed", destination)
		}
	}
}

func mustDecodeAddress076(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func legacyCurrentAddress076(t *testing.T, u *utxoStore) *addressStateCheckpoint {
	t.Helper()
	ctx := context.Background()
	e := newAddressStateEditor(ctx, u.root, addressStateCheckpoint{PageGeneration: indexDigest("old-destinations:" + u.Head().Commitment)})
	err := u.trees(ctx, *u.Head())["live"].walk(func(kv ordinalKV) error {
		var member utxoMember
		if err := json.Unmarshal(kv.Value, &member); err != nil {
			return err
		}
		return e.change(member, true)
	})
	if err == nil {
		err = e.flush()
	}
	if err == nil {
		err = e.bind(*u.Head())
	}
	if err == nil {
		err = publishAddressStateContext(ctx, u.root, e.cp, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return &e.cp
}

func Test076P2PKCurrentUpgradeSeparateBalancesAndRawFreeRecovery(t *testing.T) {
	ctx := context.Background()
	compressed, uncompressed := "21"+address076PublicKey+"ac", "41"+address076UncompressedKey+"ac"
	u, b0, b1 := address076Source(t, []uint64{10, 20, 30, 4}, []string{compressed, uncompressed, address076SameKeyP2PKH, "2102" + strings.Repeat("00", 32) + "ac"})
	old := legacyCurrentAddress076(t, u)
	if old.OutputCount != 1 || old.ExcludedOutputCount != 3 {
		t.Fatal(old)
	}
	if _, err := lookupAddressStateContext(ctx, u.root, compressed, 10, ""); !errors.Is(err, ErrAddressDestinationUpgrade) {
		t.Fatal("old scan claimed P2PK zero", err)
	}
	if conventional, err := lookupAddressStateContext(ctx, u.root, address076SameKeyP2PKH, 10, ""); err != nil || conventional.BalanceSats != 30 {
		t.Fatal(conventional, err)
	}
	// A normal UTXO commit keeps an old projection healthy without silently
	// claiming that all historical live P2PK outputs have been backfilled.
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(76070, 5))
	b2.Transactions[0].Outputs[0].ScriptPubKey = compressed
	utxoApply(t, u, b2)
	if status, err := addressStateStatusContext(ctx, u.root); err != nil || status.State != "ready" {
		t.Fatal(status, err)
	}
	if _, err := lookupAddressStateContext(ctx, u.root, compressed, 10, ""); !errors.Is(err, ErrAddressDestinationUpgrade) {
		t.Fatal(err)
	}
	upgraded, err := buildAddressStateContext(ctx, u.root, nil)
	if err != nil || upgraded.DestinationVersion != 1 {
		t.Fatal(upgraded, err)
	}
	archives, err := filepath.Glob(filepath.Join(addressStateDirectory(u.root), "prior-heads", "*.json"))
	if err != nil || len(archives) != 1 {
		t.Fatal("prior evidence not preserved", archives, err)
	}
	for _, vector := range []struct {
		script string
		value  uint64
		count  uint64
	}{{compressed, 15, 2}, {uncompressed, 20, 1}, {address076SameKeyP2PKH, 30, 1}} {
		r, err := lookupAddressStateContext(ctx, u.root, vector.script, 10, "")
		if err != nil || !r.Complete || r.BalanceSats != vector.value || r.OutputCount != vector.count {
			t.Fatal(r, err)
		}
	}
	spend := utxoFixtureSpend(76071, []inputView{{PrevTxID: b1.Transactions[0].TxID}}, 8)
	spend.Outputs[0].ScriptPubKey = address076SameKeyP2PKH
	b3 := utxoFixtureBlock(3, b2.Hash, utxoFixtureCoinbase(76072, 1), spend)
	utxoApply(t, u, b3)
	r, err := lookupAddressStateContext(ctx, u.root, compressed, 10, "")
	if err != nil || r.BalanceSats != 5 {
		t.Fatal(r, err)
	}
	hashes := map[int64]string{0: b0.Hash, 1: b1.Hash, 2: b2.Hash}
	if err = u.ReconcileSelected(ctx, 2, func(_ context.Context, h int64) (string, error) { return addressHistoryTestHashAt(hashes)(h) }); err != nil {
		t.Fatal(err)
	}
	cold, err := openUTXOStoreContext(ctx, u.root)
	if err != nil {
		t.Fatal(err)
	}
	r, err = lookupAddressStateContext(ctx, cold.root, compressed, 10, "")
	if err != nil || r.BalanceSats != 15 || r.Checkpoint.BlockHash != b2.Hash {
		t.Fatal("rollback/restart", r, err)
	}
}

// Simulate the actual prior implementation: conventional rows and counts, no
// P2PK rows, no destination version, and all original immutable facts retained.
func legacyHistoryDestinations076(t *testing.T, s *addressHistoryStore, height int64, keys ...string) {
	t.Helper()
	ctx := context.Background()
	addresses := s.tree("addresses", s.head.AddressRoot, ctx)
	for _, key := range keys {
		addresses.remove(addressHistoryAddressKey(key))
	}
	ar, err := addresses.flush()
	if err != nil {
		t.Fatal(err)
	}
	blocks := s.tree("blocks", s.head.BlocksRoot, ctx)
	var block addressHistoryBlock
	have, err := blocks.get(addressHistoryHeightKey(height), &block)
	if err != nil || !have {
		t.Fatal(err)
	}
	block.DestinationVersion = 0
	block.AddressOutputs = 0
	// Pages may also contain P2PK rows; the legacy scanner skipped those rows.
	for _, hash := range block.FactPages {
		page, err := readOutputFactPageContext(ctx, s.root, hash)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Rows {
			if _, address := describeScript(mustDecodeAddress076(t, *row.Script)); address != "" {
				block.AddressOutputs++
			}
		}
	}
	if err = blocks.put(addressHistoryHeightKey(height), block); err != nil {
		t.Fatal(err)
	}
	br, err := blocks.flush()
	if err != nil {
		t.Fatal(err)
	}
	h := s.head
	h.AddressRoot, h.BlocksRoot, h.P2PKCoverage = ar, br, nil
	if err = s.publish(ctx, h); err != nil {
		t.Fatal(err)
	}
}

func Test076P2PKHistoryBackfillCoveragePinsAndSeparation(t *testing.T) {
	ctx := context.Background()
	s, err := openAddressHistoryStoreContext(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	compressed, uncompressed := "21"+address076PublicKey+"ac", "41"+address076UncompressedKey+"ac"
	hashes := map[int64]string{10: strings.Repeat("1", 64), 20: strings.Repeat("2", 64)}
	hashAt := addressHistoryTestHashAt(hashes)
	block := addressHistoryTestBlock(10, hashes[10], outputView{ValueSats: 11, ScriptPubKey: compressed}, outputView{ValueSats: 12, ScriptPubKey: uncompressed}, outputView{ValueSats: 13, ScriptPubKey: address076SameKeyP2PKH})
	if err = s.CommitBlockContext(ctx, block, hashAt); err != nil {
		t.Fatal(err)
	}
	legacyHistoryDestinations076(t, s, 10, address076PublicKey, address076UncompressedKey)
	prior := s.head.Commitment
	if r, err := s.QueryContext(ctx, address076PublicKey, "", 10, hashAt); err != nil || r.State != "rebuild_required" || len(r.Coverage) != 0 {
		t.Fatal("legacy coverage claimed zero", r, err)
	}
	_, p2pkh := describeDestinationScript(mustDecodeAddress076(t, address076SameKeyP2PKH))
	if r, err := s.QueryContext(ctx, p2pkh, "", 10, hashAt); err != nil || r.ReceivedSats != 13 {
		t.Fatal(r, err)
	}
	if err = s.VisitFactPagesContext(ctx, func(string) error { return nil }); err != nil {
		t.Fatal("legacy pins rejected", err)
	}
	if err = s.CommitBlockContext(ctx, addressHistoryTestBlock(20, hashes[20], outputView{ValueSats: 14, ScriptPubKey: compressed}), hashAt); err != nil {
		t.Fatal(err)
	}
	if r, err := s.QueryContext(ctx, address076PublicKey, "", 10, hashAt); err != nil || r.ReceivedSats != 14 || len(r.Coverage) != 1 || r.Coverage[0] != (heightInterval{20, 20}) || len(r.Gaps) != 1 || r.Gaps[0] != (heightInterval{10, 19}) {
		t.Fatal("partial P2PK backfill claimed old coverage", r, err)
	}
	if err = s.CommitBlockContext(ctx, block, hashAt); err != nil {
		t.Fatal(err)
	}
	cold, err := openAddressHistoryStoreContext(ctx, s.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		key   string
		total uint64
		count uint64
	}{{address076PublicKey, 25, 2}, {address076UncompressedKey, 12, 1}, {p2pkh, 13, 1}} {
		r, err := cold.QueryContext(ctx, v.key, "", 10, hashAt)
		if err != nil || r.ReceivedSats != v.total || r.ReceivedOutputCount != v.count || r.Complete {
			t.Fatal(r, err)
		}
	}
	if err = cold.VisitFactPagesContext(ctx, func(string) error { return nil }); err != nil {
		t.Fatal("new/prior fact pins lost", err)
	}
	if _, err = os.Stat(filepath.Join(s.dir, "commits", prior[:2], prior+".json")); err != nil {
		t.Fatal("prior scan destroyed", err)
	}
	hashes[20] = strings.Repeat("3", 64)
	if err = cold.ReconcileContext(ctx, hashAt); err != nil {
		t.Fatal(err)
	}
	r, err := cold.QueryContext(ctx, address076PublicKey, "", 10, hashAt)
	if err != nil || r.ReceivedSats != 11 || len(r.Coverage) != 1 || r.Coverage[0] != (heightInterval{10, 10}) {
		t.Fatal("P2PK reorg retained disconnected coverage", r, err)
	}
}

func Test076P2PKAddressAPIAndSearchUseDestinationNotP2PKH(t *testing.T) {
	ctx := context.Background()
	compressed := "21" + address076PublicKey + "ac"
	u, _, b := address076Source(t, []uint64{42, 7}, []string{compressed, address076SameKeyP2PKH})
	legacyCurrentAddress076(t, u)
	settings, stop := fakeCoreRPC(t, b.Height, b.Hash)
	defer stop()
	a := independentTestApp(t)
	a.dataDir, a.settings = u.root, settings
	a.status = appStatus{HeaderHeight: -1}
	r, err := a.addressViewContext(ctx, address076PublicKey, "current", "", 10)
	if err != nil || r.State != "rebuild_required" || r.CurrentBalanceSats != nil || r.DestinationType != "p2pk" {
		t.Fatal(r, err)
	}
	status, instances := a.indexAddressStatus(nil)
	if status.Current.State != "ready" || status.Current.P2PKSupported || status.Current.DestinationVersion != 0 || !strings.Contains(status.Current.Note, "rebuild") {
		t.Fatal("legacy current capability was overstated", status)
	}
	for _, instance := range instances {
		if instance.Definition == "address-state" && instance.Completeness != "complete_live_conventional_address_set_at_snapshot" {
			t.Fatal(instance)
		}
	}
	if _, err = buildAddressStateContext(ctx, u.root, nil); err != nil {
		t.Fatal(err)
	}
	r, err = a.addressViewContext(ctx, strings.ToUpper(address076PublicKey), "current", "", 10)
	if err != nil || r.Address != address076PublicKey || r.DestinationType != "p2pk" || r.CurrentBalanceSats == nil || *r.CurrentBalanceSats != 42 || len(r.Outputs) != 1 || r.Outputs[0].ScriptType != "p2pk" {
		t.Fatal(r, err)
	}
	search, err := a.resolveSearchInputContext(ctx, address076PublicKey)
	if err != nil || search.Kind != "address" || search.AddressIndex == nil || search.AddressIndex.DestinationType != "p2pk" {
		t.Fatal(search, err)
	}
}

func Test076P2PKStagedLegacyBuildRestartsWithoutFalseCoverage(t *testing.T) {
	ctx := context.Background()
	compressed := "21" + address076PublicKey + "ac"
	u, _, _ := address076Source(t, []uint64{5, 9}, []string{compressed, address076SameKeyP2PKH})
	old := legacyCurrentAddress076(t, u)
	last := ""
	if err := u.trees(ctx, *u.Head())["live"].walk(func(kv ordinalKV) error { last = kv.Key; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := saveAddressBuildContext(ctx, u.root, addressStateBuildManifest{Source: *u.Head(), Projection: *old, OutputsChecked: u.Head().LiveOutputs, After: last}); err != nil {
		t.Fatal(err)
	}
	var report addressStateBuildProgress
	if _, err := buildAddressStateContext(ctx, u.root, func(p addressStateBuildProgress) { report = p }); err != nil {
		t.Fatal(err)
	}
	if report.Resumed || !strings.Contains(report.RestartReason, "destination coverage") {
		t.Fatal(report)
	}
	r, err := lookupAddressStateContext(ctx, u.root, compressed, 10, "")
	if err != nil || r.BalanceSats != 5 || !r.Complete {
		t.Fatal(r, err)
	}
	archives, err := filepath.Glob(filepath.Join(addressStateDirectory(u.root), "prior-builds", "*.json"))
	if err != nil || len(archives) != 1 {
		t.Fatal("old staged evidence not retained", archives, err)
	}
}
