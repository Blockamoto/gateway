package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func satRuntime077Real(t *testing.T) (*app, *indexStore, blockView, blockView) {
	t.Helper()
	a, raw0 := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.NetworkDisabled = true
	raw1, hash1 := installBlockOneHeader063(t, a)
	b0, err := parseBlockDetailed(0, genesisHashDisplay, raw0[:80], raw0, "fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	b1, err := parseBlockDetailed(1, hash1, raw1[:80], raw1, "fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	s, err := openIndexStore(a.dataDir, "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []blockView{b0, b1} {
		if err = s.appendBlock(b, 0, "pruned"); err != nil {
			t.Fatal(err)
		}
	}
	return a, s, b0, b1
}

func Test077SatHTTPGenuineBlocksSearchPrivacyAndNoImplicitJobs(t *testing.T) {
	ctx := context.Background()
	a, s, b0, b1 := satRuntime077Real(t)
	// The durable Sat facts remain sufficient after the original genesis body is evicted.
	if err := os.Remove(filepath.Join(a.dataDir, "blocks", "raw", "0-"+b0.Hash+".block")); err != nil {
		t.Fatal(err)
	}
	genesis, err := a.satViewContext077(ctx, 0)
	if err != nil || genesis.State != "ready" || genesis.Location == nil || genesis.Location.State != "genesis_unspendable" || genesis.Location.Address != "" || genesis.Location.Output != "0.0.0" || genesis.Snapshot.Height != 1 || genesis.Snapshot.BlockHash != b1.Hash || genesis.Snapshot.Commitment != s.checkpoint.Commitment || genesis.Snapshot.ConsensusValidated {
		t.Fatal(genesis, err)
	}
	live, err := a.satViewContext077(ctx, firstSatAtHeight(1))
	if err != nil || live.State != "ready" || live.Location.State != "unspent" || live.Location.Output != "0.0.1" || live.Location.Offset != 0 {
		t.Fatal(live, err)
	}
	ranges, err := a.satOutputContext077(ctx, "0.0.1", 50, "")
	if err != nil || ranges.State != "ready" || ranges.Value != subsidyAtHeight(1) || len(ranges.Ranges) != 1 || ranges.Ranges[0] != (satViewRange077{Start: firstSatAtHeight(1), End: firstSatAtHeight(2), Offset: 0}) || ranges.HasMore {
		t.Fatal(ranges, err)
	}
	search, err := a.resolveSearchInputContext(ctx, "5000000000.sat")
	if err != nil || search.Kind != "sat" || search.Sat == nil || search.Sat.SatNumber != firstSatAtHeight(1) {
		t.Fatal(search, err)
	}
	unknown, err := a.satOutputContext077(ctx, "0.0.2", 5, "")
	if err != nil || unknown.State == "ready" || len(unknown.Ranges) != 0 {
		t.Fatal("unknown output became empty known ranges", unknown, err)
	}
	future, err := a.satViewContext077(ctx, firstSatAtHeight(2))
	if err != nil || future.State != "not_issued_at_snapshot" || future.Location != nil {
		t.Fatal(future, err)
	}
	for _, path := range []string{"job.json", "queue.json"} {
		if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", path)); !os.IsNotExist(err) {
			t.Fatal("lookup created a job", path, err)
		}
	}
	for _, asset := range []string{"sat-view.js", "sat-panel.js"} {
		r := httptest.NewRecorder()
		a.handleShellAsset(r, httptest.NewRequest("GET", "/shell/"+asset, nil))
		if r.Code != 200 {
			t.Fatal("new shell asset unavailable", asset, r.Code)
		}
	}
}

func Test077SatHTTPRejectsMalformedRemoteAndMutationRequests(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	for _, tc := range []struct {
		path    string
		handler http.HandlerFunc
	}{{"/api/v1/sats/view?sat=0", a.handleSatView077}, {"/api/v1/sats/output?output=0.0.0", a.handleSatOutput077}, {"/api/v1/sats/address?address=" + addressRuntime076Address, a.handleSatAddress077}} {
		remote := httptest.NewRecorder()
		aReq := httptest.NewRequest("GET", "http://untrusted.invalid"+tc.path, nil)
		tc.handler(remote, aReq)
		if remote.Code != 403 {
			t.Fatal("private Sat API accepted untrusted request", tc.path, remote.Code, remote.Body.String())
		}
		post := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "http://127.0.0.1:9090"+tc.path, nil)
		req.RemoteAddr = "127.0.0.1:1234"
		tc.handler(post, req)
		if post.Code != 405 {
			t.Fatal(tc.path, post.Code)
		}
	}
	for _, input := range []string{"-1", "00", "01", "bad", "2100000000000000", "18446744073709551616"} {
		r := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "http://127.0.0.1:9090/api/v1/sats/view?sat="+input, nil)
		req.RemoteAddr = "127.0.0.1:1234"
		a.handleSatView077(r, req)
		if r.Code != 400 {
			t.Fatal(input, r.Code, r.Body.String())
		}
	}
	r := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://127.0.0.1:9090/api/v1/sats/view?sat=0", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	a.handleSatView077(r, req)
	var missing satViewResult077
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &missing) != nil || missing.State != "not_built" || missing.Location != nil {
		t.Fatal(r.Code, r.Body.String())
	}
}

func Test077SatReverseRequiresLocalExplicitTrace(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	remote := httptest.NewRecorder()
	a.handleSatReverse077(remote, httptest.NewRequest("POST", "http://untrusted.invalid/api/v1/sats/reverse", strings.NewReader(`{"input":"0.0.0.0"}`)))
	if remote.Code != 403 {
		t.Fatal("remote trace accepted", remote.Code)
	}
	local := func(method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(method, "http://127.0.0.1:9090/api/v1/sats/reverse", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1234"
		a.handleSatReverse077(r, req)
		return r
	}
	for _, test := range []struct{ method, body string }{{"GET", ""}, {"POST", `{"input":"bad"}`}, {"POST", `{"input":"0.0.0.0","rebuild":true}`}} {
		if r := local(test.method, test.body); r.Code != http.StatusForbidden || !strings.Contains(r.Body.String(), "deferred") {
			t.Fatal("deferred reverse trace remained callable", r.Code, r.Body.String())
		}
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", "sat-state", "head.json")); !os.IsNotExist(err) {
		t.Fatal("trace guards started Sat builder", err)
	}
}

func Test077SatAddressHoldingsBoundedZeroCoverageAndRootedCursor(t *testing.T) {
	ctx := context.Background()
	values, scripts := make([]uint64, 40), make([]string, 40)
	for i := range values {
		values[i] = 1
		scripts[i] = address076P2PKH
	}
	u, b0, b1 := address076Source(t, values, scripts)
	if _, err := buildAddressStateContext(ctx, u.root, nil); err != nil {
		t.Fatal(err)
	}
	s, err := openIndexStore(u.root, "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []blockView{b0, b1} {
		if err = s.appendBlock(b, 0, "pruned"); err != nil {
			t.Fatal(err)
		}
	}
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(77999, subsidyAtHeight(2)))
	settings, calls, stop := satline077RPC(t, map[int64]string{0: b0.Hash, 1: b1.Hash, 2: b2.Hash})
	defer stop()
	a := independentTestApp(t)
	a.dataDir = u.root
	a.settings = settings
	a.settings.NetworkDisabled = true
	a.status = appStatus{HeaderHeight: -1}
	raw, _ := hex.DecodeString(address076P2PKH)
	_, address := describeScript(raw)
	first, err := a.satAddressContext077(ctx, address, 1, "")
	if err != nil || first.State != "ready" || first.Complete || len(first.Inscriptions) != 0 || len(first.Coverage) != 1 || first.Coverage[0] != (heightInterval{0, 1}) || !first.HasMore || first.NextCursor == "" || first.AddressSnapshot == nil || first.Snapshot.BlockHash != b1.Hash || first.Snapshot.Commitment == first.AddressSnapshot.Commitment {
		t.Fatal("zero known identities must remain partial with independent roots", first, err)
	}
	cursor := first.NextCursor
	pages := 1
	for cursor != "" {
		page, err := a.satAddressContext077(ctx, address, 1, cursor)
		if err != nil || page.State != "ready" || page.Complete || len(page.Inscriptions) != 0 || page.Snapshot.Commitment != first.Snapshot.Commitment || page.AddressSnapshot.Commitment != first.AddressSnapshot.Commitment {
			t.Fatal(page, err)
		}
		pages++
		cursor = page.NextCursor
		if pages > 4 {
			t.Fatal("bounded paging did not terminate")
		}
	}
	if pages != 3 || calls.Load() != 0 {
		t.Fatal("holdings must page output-local facts without raw RPC", pages, calls.Load())
	}
	if _, err = a.satAddressContext077(ctx, addressRuntime076Address, 1, first.NextCursor); err == nil {
		t.Fatal("cursor crossed addresses")
	}
	if err = s.appendBlock(b2, 0, "pruned"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.satAddressContext077(ctx, address, 1, first.NextCursor); err == nil {
		t.Fatal("old Sat commitment cursor survived replacement")
	}
	lagging, err := a.satAddressContext077(ctx, address, 1, "")
	if err != nil || lagging.State != "catching_up" || len(lagging.Inscriptions) != 0 || lagging.Complete {
		t.Fatal("different processed tips joined", lagging, err)
	}
}

func Test077SatAddressKnownIdentityPagingUsesMatchingOutputAndOffset(t *testing.T) {
	ctx := context.Background()
	u, b0, b1 := address076Source(t, []uint64{4}, []string{address076P2PKH})
	reveal := utxoFixtureSpend(77881, []inputView{{N: 0, PrevTxID: b1.Transactions[0].TxID, PrevVout: 0}}, 4)
	reveal.Outputs[0].ScriptPubKey = address076P2PKH
	reveal.Inputs[0].Witness = ordWitness050(append(inscriptionTestScript(nil, []byte("first")), inscriptionTestScript(nil, []byte("second"))...))
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(77882, subsidyAtHeight(2)), reveal)
	utxoApply(t, u, b2)
	if _, err := buildAddressStateContext(ctx, u.root, nil); err != nil {
		t.Fatal(err)
	}
	s, err := openIndexStore(u.root, "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []blockView{b0, b1, b2} {
		if err = s.appendBlock(b, 0, "pruned"); err != nil {
			t.Fatal(err)
		}
	}
	settings, calls, stop := satline077RPC(t, map[int64]string{0: b0.Hash, 1: b1.Hash, 2: b2.Hash})
	defer stop()
	a := independentTestApp(t)
	a.dataDir = u.root
	a.settings = settings
	a.settings.NetworkDisabled = true
	a.status = appStatus{HeaderHeight: -1}
	raw, _ := hex.DecodeString(address076P2PKH)
	_, address := describeScript(raw)
	first, err := a.satAddressContext077(ctx, address, 1, "")
	if err != nil || first.State != "ready" || len(first.Inscriptions) != 1 || !first.HasMore || first.NextCursor == "" || first.Complete {
		t.Fatal(first, err)
	}
	second, err := a.satAddressContext077(ctx, address, 1, first.NextCursor)
	if err != nil || second.State != "ready" || len(second.Inscriptions) != 1 || second.HasMore || second.Complete {
		t.Fatal(second, err)
	}
	if first.Inscriptions[0].ID == second.Inscriptions[0].ID {
		t.Fatal("duplicate identity across page boundary")
	}
	for _, row := range append(first.Inscriptions, second.Inscriptions...) {
		if row.SatNumber != firstSatAtHeight(1) || row.Output != "0.1.2" || row.Offset != 0 {
			t.Fatal("holding not joined to exact output-relative sat", row)
		}
	}
	if first.Snapshot.Height != 2 || first.AddressSnapshot.Height != 2 || first.Snapshot.BlockHash != b2.Hash || first.AddressSnapshot.BlockHash != b2.Hash || calls.Load() != 0 {
		t.Fatal("holdings fetched raw or joined different anchors", first, calls.Load())
	}
}
