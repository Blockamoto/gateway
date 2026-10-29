package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const addressRuntime076Address = "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"

func Test076AddressCurrentDependencyDoesNotBuildUTXOOrReportZero(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	if _, err := a.planIndexBuild(indexBuildRequest{Index: "address-state"}); err == nil {
		t.Fatal("current dependency silently accepted")
	}
	r := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://127.0.0.1:9090/api/v1/address/view?address="+addressRuntime076Address, nil)
	req.RemoteAddr = "127.0.0.1:1234"
	a.handleAddressView(r, req)
	var out addressViewResult
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || out.State != "dependency_missing" || out.CurrentBalanceSats != nil {
		t.Fatal(r.Code, r.Body.String())
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", "utxo-state", "head.json")); !os.IsNotExist(err) {
		t.Fatal("lookup built UTXO", err)
	}
	legacy := httptest.NewRecorder()
	a.handleAddressUTXOs(legacy, httptest.NewRequest("POST", "http://127.0.0.1:9090/api/v1/address/utxos", strings.NewReader(`{"address":"`+addressRuntime076Address+`"}`)))
	if legacy.Code < 400 || !strings.Contains(legacy.Body.String(), "locked") {
		t.Fatal("local address index unlocked legacy remote/Core scan shortcut", legacy.Code, legacy.Body.String())
	}
	for _, url := range []string{"?address=bad", "?address=" + addressRuntime076Address + "&limit=101", "?address=" + addressRuntime076Address + "&mode=wallet"} {
		r := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "http://127.0.0.1:9090/api/v1/address/view"+url, nil)
		req.RemoteAddr = "127.0.0.1:1234"
		a.handleAddressView(r, req)
		if r.Code != 400 {
			t.Fatal(url, r.Code)
		}
	}
	r = httptest.NewRecorder()
	a.handleAddressView(r, httptest.NewRequest("GET", "http://untrusted.example/api/v1/address/view?address="+addressRuntime076Address, nil))
	if r.Code != 403 {
		t.Fatal("remote request escaped", r.Code)
	}
}
func Test076AddressJobsBuildFromSavedStateAndSparseHistoryOffline(t *testing.T) {
	ctx := context.Background()
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.NetworkDisabled = true, true
	b, err := a.fetchAndDecodeContext(ctx, "0")
	if err != nil {
		t.Fatal(err)
	}
	u, err := openUTXOStoreContext(ctx, a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	utxoApply(t, u, b)
	for _, request := range []indexBuildRequest{{Index: "address-state", From: int64Pointer076(0)}, {Index: "address-state", To: int64Pointer076(0)}, {Index: "address-state", Mode: "full"}} {
		if _, err := a.planIndexBuild(request); err == nil {
			t.Fatal("invalid current range accepted", request)
		}
	}
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{
		{Index: "utxo-state", Armed: true, Live: true},
		{Index: "address-state", Armed: true},
	}}); err != nil {
		t.Fatal(err)
	}
	j, err := a.startIndexBuild(indexBuildRequest{Index: "address-state"})
	if err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	j, err = a.waitQueuedIndexJob(wait, j.ID)
	if err != nil || j.State != "complete" {
		t.Fatal(j, err)
	}
	out, err := a.addressViewContext(ctx, addressRuntime076Address, "current", "", 50)
	if err != nil || out.State != "ready" || out.CurrentBalanceSats == nil || *out.CurrentBalanceSats != 0 || out.Snapshot.BlockHash != b.Hash || out.Snapshot.ConsensusValidated {
		t.Fatal(out, err)
	}
	armLegacyBuildTracks(t, a, indexBuildRequest{Index: "address-history"})
	history, err := a.startIndexBuild(indexBuildRequest{Index: "address-history", From: int64Pointer076(0), To: int64Pointer076(0)})
	if err != nil {
		t.Fatal(err)
	}
	history, err = a.waitQueuedIndexJob(wait, history.ID)
	if err != nil || history.State != "complete" {
		t.Fatal(history, err)
	}
	out, err = a.addressViewContext(ctx, addressRuntime076Address, "historical", "", 50)
	if err != nil || out.State != "ready" || out.CurrentBalanceSats != nil || out.ReceivedSats == nil || *out.ReceivedSats != 0 || len(out.Coverage) != 1 {
		t.Fatal(out, err)
	}
	search, err := a.resolveSearchInputContext(ctx, addressRuntime076Address)
	if err != nil || search.Kind != "address" || search.AddressIndex == nil || search.Address != nil {
		t.Fatal(search, err)
	}
	providers := a.indexProviderViews()
	if row := indexProviderRow(t, providers, "address-state", "gateway_address_state"); !row.Queryable || row.Serveable || row.Snapshot.Height != 0 || len(row.Coverage) != 1 || row.Coverage[0] != (heightInterval{0, 0}) {
		t.Fatal("saved current provider metadata", row)
	}
	if row := indexProviderRow(t, providers, "address-history", "gateway_address_history"); !row.Queryable || row.Serveable || row.Completeness != "explicit_scanned_ranges" {
		t.Fatal("saved historical provider metadata", row)
	}
	for _, asset := range []string{"address-view.js", "address-panel.js"} {
		r := httptest.NewRecorder()
		a.handleShellAsset(r, httptest.NewRequest("GET", "/shell/"+asset, nil))
		if r.Code != 200 {
			t.Fatal(asset, r.Code)
		}
	}
}

func Test076AddressAPIExactNonzeroBalanceSparseTotalsAndUnavailableAnchor(t *testing.T) {
	ctx := context.Background()
	u, _, b := address076Source(t, []uint64{40, 2}, []string{address076P2PKH, address076P2PKH})
	if _, err := buildAddressStateContext(ctx, u.root, nil); err != nil {
		t.Fatal(err)
	}
	settings, stop := fakeCoreRPC(t, b.Height, b.Hash)
	defer stop()
	a := independentTestApp(t)
	a.dataDir, a.settings = u.root, settings
	a.status = appStatus{HeaderHeight: -1}
	raw, _ := hex.DecodeString(address076P2PKH)
	_, address := describeScript(raw)
	first, err := a.addressViewContext(ctx, address, "current", "", 1)
	if err != nil || first.State != "ready" || first.CurrentBalanceSats == nil || *first.CurrentBalanceSats != 42 || *first.CurrentOutputCount != 2 || len(first.Outputs) != 1 || !first.HasMore || first.Snapshot.BlockHash != b.Hash {
		t.Fatal(first, err)
	}
	second, err := a.addressViewContext(ctx, address, "current", first.NextCursor, 1)
	if err != nil || len(second.Outputs) != 1 || second.HasMore || second.Outputs[0].Output == first.Outputs[0].Output || *second.CurrentBalanceSats != 42 {
		t.Fatal(second, err)
	}
	h, err := openAddressHistoryStoreContext(ctx, u.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.CommitBlockContext(ctx, b, addressHistoryTestHashAt(map[int64]string{b.Height: b.Hash})); err != nil {
		t.Fatal(err)
	}
	history, err := a.addressViewContext(ctx, address, "historical", "", 1)
	if err != nil || history.State != "ready" || history.CurrentBalanceSats != nil || history.ReceivedSats == nil || *history.ReceivedSats != 42 || history.Complete || history.Outputs[0].SpentStatus != "unknown" {
		t.Fatal("historical totals inferred balance or spend status", history, err)
	}
	a.settingsMu.Lock()
	a.settings.CoreDisabled = true
	a.settingsMu.Unlock()
	unknown, err := a.addressViewContext(ctx, address, "current", "", 1)
	if err != nil || unknown.State != "stale" || unknown.CurrentBalanceSats != nil || len(unknown.Outputs) != 0 {
		t.Fatal("unavailable anchor published an apparent balance", unknown, err)
	}
}
func int64Pointer076(n int64) *int64 { return &n }

func Test076AddressReviewedRebuildReplacesDamagedHeadAndPreservesEvidence(t *testing.T) {
	ctx := context.Background()
	u, _, b := address076Source(t, []uint64{42}, []string{address076P2PKH})
	if _, err := buildAddressStateContext(ctx, u.root, nil); err != nil {
		t.Fatal(err)
	}
	settings, stop := fakeCoreRPC(t, b.Height, b.Hash)
	defer stop()
	a := independentTestApp(t)
	a.dataDir, a.settings, a.status = u.root, settings, appStatus{HeaderHeight: -1}
	damaged := []byte("damaged prior address head")
	path := filepath.Join(addressStateDirectory(u.root), "head.json")
	if err := os.WriteFile(path, damaged, 0600); err != nil {
		t.Fatal(err)
	}
	status, _ := a.indexAddressStatus(nil)
	if status.Current.State != "rebuild_required" {
		t.Fatal(status)
	}
	before, err := a.addressViewContext(ctx, addressRuntime076Address, "current", "", 50)
	if err != nil || before.State != "rebuild_required" || before.CurrentBalanceSats != nil {
		t.Fatal(before, err)
	}
	request := indexBuildRequest{Index: "address-state", Rebuild: true}
	if _, err := a.planIndexBuild(request); err != nil {
		t.Fatal(err)
	}
	j, err := a.startIndexBuild(request)
	if err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	j, err = a.waitQueuedIndexJob(wait, j.ID)
	if err != nil || j.State != "complete" {
		t.Fatal(j, err)
	}
	raw, _ := hex.DecodeString(address076P2PKH)
	_, address := describeScript(raw)
	after, err := a.addressViewContext(ctx, address, "current", "", 50)
	if err != nil || after.State != "ready" || after.CurrentBalanceSats == nil || *after.CurrentBalanceSats != 42 {
		t.Fatal(after, err)
	}
	archive := filepath.Join(addressStateDirectory(u.root), "prior-heads", indexDigest(damaged)+".json")
	retained, err := os.ReadFile(archive)
	if err != nil || string(retained) != string(damaged) {
		t.Fatal("prior evidence was not preserved", err)
	}
}
