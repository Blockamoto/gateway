package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bitmapTargetFixture(t *testing.T, claims ...string) (*app, *bitmapRunConsumer, blockView) {
	t.Helper()
	a := runTestApp(t)
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", claims...)
	installRunProtocolHeader(t, a, &b)
	s := bitmapTestConsumer(t, a.dataDir, bitmapCanonicalDistrictID)
	if err := s.Apply(context.Background(), b, nil); err != nil {
		t.Fatal(err)
	}
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) {
		t.Fatal("target query fetched a Bitcoin body")
		return blockView{}, nil
	}
	return a, s, b
}
func bitmapTargetSum(v bitmapTargetCoverageView) (uint64, uint64) {
	winners, unknown := uint64(0), uint64(0)
	for _, bin := range v.Bins {
		winners += bin.Winners
		unknown += bin.Unknown
	}
	return winners, unknown
}
func TestBitmapTargetCoverageDistrictAxisCommittedOutcomeAndCache(t *testing.T) {
	a, s, _ := bitmapTargetFixture(t, "0.bitmap", "10.bitmap", "20.bitmap", "20.bitmap")
	q := bitmapTargetCoverageQuery{Axis: "blocks", From: 0, To: 29, Pixels: 3}
	before, err := os.ReadFile(filepath.Join(s.dir, "head.json"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := a.bitmapTargetCoverageContext(context.Background(), q)
	if err != nil || v.State != "ready" || !v.QueryComplete || v.CacheHit || len(v.Bins) != 3 || v.Snapshot.Height != 792435 {
		t.Fatal(v, err)
	}
	for i, bin := range v.Bins {
		if bin.From != uint64(i*10) || bin.To != uint64(i*10+9) || bin.Winners != 1 || bin.Targets != 10 || bin.Unknown != 9 {
			t.Fatal("target pixel mapping", bin)
		}
	}
	if v.ExaminedWinners != 3 || v.AnchorChecks != 1 || v.ReadPages == 0 || v.CacheKey == "" {
		t.Fatal("bounded work missing", v)
	}
	oldKey := v.CacheKey
	v.Bins[0].Winners = 999
	again, err := a.bitmapTargetCoverageContext(context.Background(), q)
	if err != nil || !again.CacheHit || again.Bins[0].Winners != 1 || again.ExaminedWinners != 0 || again.ReadPages != 0 || again.ReadBytes != 0 || again.CacheKey != oldKey {
		t.Fatal("immutable cache", again, err)
	}
	reveal, err := a.bitmapTargetCoverageContext(context.Background(), bitmapTargetCoverageQuery{Axis: "blocks", From: 792435, To: 792435, Pixels: 1})
	if err != nil || reveal.State != "ready" || reveal.Bins[0].Winners != 0 || reveal.Bins[0].Unknown != 1 {
		t.Fatal("reveal coverage was drawn as target coverage", reveal, err)
	}
	after, _ := os.ReadFile(filepath.Join(s.dir, "head.json"))
	if string(before) != string(after) {
		t.Fatal("read-only query changed head")
	}
	if doc := a.indexRunSnapshot(); doc.Run != nil || doc.Configured {
		t.Fatal("query armed or ran an index", doc)
	}
}
func TestBitmapTargetCoveragePixelWorkAndVisibleRangePruning(t *testing.T) {
	claims := []string{}
	for n := 0; n < 160; n++ {
		claims = append(claims, fmt.Sprintf("%d.bitmap", n))
	}
	a, _, _ := bitmapTargetFixture(t, claims...)
	one, err := a.bitmapTargetCoverageContext(context.Background(), bitmapTargetCoverageQuery{Axis: "blocks", From: 0, To: 159, Pixels: 1})
	winners, unknown := bitmapTargetSum(one)
	if err != nil || one.State != "partial" || one.QueryComplete || !one.Truncated || one.ExaminedWinners > 4 || winners != 4 || unknown != 156 || one.Limits.Winners != 4 {
		t.Fatal("one pixel enumerated a winner set", one, err)
	}
	four, err := a.bitmapTargetCoverageContext(context.Background(), bitmapTargetCoverageQuery{Axis: "blocks", From: 0, To: 159, Pixels: 4})
	if err != nil || four.State != "partial" || four.ExaminedWinners > 16 {
		t.Fatal(four, err)
	}
	for _, bin := range four.Bins {
		if bin.Winners != 4 || bin.Unknown != 36 {
			t.Fatal("pixels did not get bounded independent samples", four)
		}
	}
	point, err := a.bitmapTargetCoverageContext(context.Background(), bitmapTargetCoverageQuery{Axis: "blocks", From: 159, To: 159, Pixels: 1000})
	if err != nil || point.State != "ready" || !point.QueryComplete || point.ExaminedWinners != 1 || point.Bins[0].Winners != 1 || point.Limits.Winners != 4 || point.ReadPages >= 160 {
		t.Fatal("visible radix lookup scanned unrelated winners", point, err)
	}
}
func TestBitmapTargetCoverageTransientMissingPageRetriesSameHead(t *testing.T) {
	claims := []string{}
	for n := 0; n < 90; n++ {
		claims = append(claims, fmt.Sprintf("%d.bitmap", n))
	}
	a, s, _ := bitmapTargetFixture(t, claims...)
	tree := newOrdinalTree(filepath.Join(s.dir, "pages"), s.head.WinnerRoot)
	key := bitmapTargetKey(0, nil)
	hash := s.head.WinnerRoot
	for depth := 0; ; depth++ {
		page, err := tree.read(hash)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Children) == 0 {
			break
		}
		hash = page.Children[ordinalNibble(key, depth)]
	}
	if hash == s.head.WinnerRoot {
		t.Fatal("fixture must have a nonroot target page")
	}
	path := tree.pagePath(hash)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	q := bitmapTargetCoverageQuery{Axis: "blocks", From: 0, To: 0, Pixels: 1}
	first, err := a.bitmapTargetCoverageContext(context.Background(), q)
	if err != nil || first.State != "partial" || first.CacheHit || first.QueryComplete || first.Bins[0].Winners != 0 {
		t.Fatal("missing target page", first, err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	second, err := a.bitmapTargetCoverageContext(context.Background(), q)
	if err != nil || second.CacheHit || second.State != "ready" || !second.QueryComplete || second.Bins[0].Winners != 1 || second.Snapshot.Commitment != first.Snapshot.Commitment {
		t.Fatal("transient partial was cached forever", second, err)
	}
}
func TestBitmapTargetCoverageOnePixelReadsBothDeepCommittedTrees(t *testing.T) {
	a := runTestApp(t)
	s := bitmapTestConsumer(t, a.dataDir, bitmapCanonicalDistrictID)
	previous := ""
	for n := int64(0); n < 130; n++ {
		block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart+n, previous, fmt.Sprintf("%d.bitmap", n))
		installRunProtocolHeader(t, a, &block)
		if err := s.Apply(context.Background(), block, nil); err != nil {
			t.Fatal(n, err)
		}
		previous = block.Hash
	}
	// The visible target is in another radix branch. Removing an unrelated
	// winner leaf proves that the query is range-pruned rather than a full walk.
	tree := newOrdinalTree(filepath.Join(s.dir, "pages"), s.head.WinnerRoot)
	hash := s.head.WinnerRoot
	key := bitmapTargetKey(0, nil)
	for depth := 0; ; depth++ {
		page, err := tree.read(hash)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Children) == 0 {
			break
		}
		hash = page.Children[ordinalNibble(key, depth)]
	}
	if hash == s.head.WinnerRoot {
		t.Fatal("winner fixture was not a radix branch")
	}
	if err := os.Remove(tree.pagePath(hash)); err != nil {
		t.Fatal(err)
	}
	v, err := a.bitmapTargetCoverageContext(context.Background(), bitmapTargetCoverageQuery{Axis: "blocks", From: 129, To: 129, Pixels: 1})
	if err != nil || v.State != "ready" || !v.QueryComplete || v.Bins[0].Winners != 1 || v.ExaminedWinners != 1 || v.ReadPages > v.Limits.Pages || v.Limits.Pages < 64 {
		t.Fatal("fixed-depth winner/outcome proof exceeded tiny pixel floor", v, err)
	}
}
func TestBitmapTargetCoveragePendingMissingHeadSnapshotAndReorgStayUnknown(t *testing.T) {
	ctx := context.Background()
	a, d, b0 := bitmapTargetFixture(t, "0.bitmap")
	parcel := bitmapTestConsumer(t, a.dataDir, bitmapCanonicalParcelID)
	if err := parcel.Apply(ctx, b0, nil); err != nil {
		t.Fatal(err)
	}
	parent, _ := hex.DecodeString(b0.Transactions[1].TxID)
	for i, j := 0, len(parent)-1; i < j; i, j = i+1, j-1 {
		parent[i], parent[j] = parent[j], parent[i]
	}
	b1 := bitmapDevelopmentFixtureBlock(b0.Height+1, b0.Hash, "0.0.bitmap")
	b1.Transactions[1].Inputs[0].Witness = ordWitness050(inscriptionTestScript([]byte{1}, []byte("text/plain"), []byte{3}, parent, nil, []byte("0.0.bitmap")))
	installRunProtocolHeader(t, a, &b1)
	old := d.head.Commitment
	if err := d.Apply(ctx, b1, nil); err != nil {
		t.Fatal(err)
	}
	parcel.SetBlockSource(func(_ context.Context, h int64) (blockView, error) {
		if h == b0.Height {
			return b0, nil
		}
		if h != 0 {
			t.Fatal(h)
		}
		return utxoFixtureBlock(0, "", utxoFixtureCoinbase(998300, 50)), nil
	})
	if err := parcel.Apply(ctx, b1, nil); !errors.Is(err, ErrBitmapEvidenceRequired) {
		t.Fatal("fixture must stay pending", err)
	}
	district := uint64(0)
	v, err := a.bitmapTargetCoverageContext(ctx, bitmapTargetCoverageQuery{Axis: "transactions", District: &district, From: 0, To: 2, Pixels: 3})
	winners, unknown := bitmapTargetSum(v)
	if err != nil || v.State != "ready" || winners != 0 || unknown != 3 || v.Snapshot.Height != b0.Height {
		t.Fatal("pending candidate became selected or absent", v, err)
	}
	changed, err := a.bitmapTargetCoverageContext(ctx, bitmapTargetCoverageQuery{Axis: "blocks", From: 0, To: 2, Pixels: 3, Snapshot: old})
	if err != nil || changed.State != "changed" {
		t.Fatal(changed, err)
	}
	_, unknown = bitmapTargetSum(changed)
	if unknown != 3 {
		t.Fatal("changed snapshot painted targets", changed)
	}
	// A changed selected header at the saved head must invalidate any cache.
	b1.Hash = indexDigest("different Bitcoin header")
	header := make([]byte, 80)
	copy(header, []byte("replacement target query fixture"))
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(header, b1.Height*80)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	v, err = a.bitmapTargetCoverageContext(ctx, bitmapTargetCoverageQuery{Axis: "blocks", From: 0, To: 2, Pixels: 3})
	if err != nil || v.State != "stale" {
		t.Fatal(v, err)
	}
	winners, unknown = bitmapTargetSum(v)
	if winners != 0 || unknown != 3 {
		t.Fatal("stale source painted targets", v)
	}
	fresh := runTestApp(t)
	v, err = fresh.bitmapTargetCoverageContext(ctx, bitmapTargetCoverageQuery{Axis: "blocks", From: 0, To: 1, Pixels: 2})
	if err != nil || v.State != "not_built" || v.Snapshot != nil {
		t.Fatal(v, err)
	}
	winners, unknown = bitmapTargetSum(v)
	if winners != 0 || unknown != 2 {
		t.Fatal(v)
	}
}
func TestBitmapTargetCoverageRequiresSelectedOutcomeMembership(t *testing.T) {
	a, s, _ := bitmapTargetFixture(t, "0.bitmap")
	// This intentionally constructs authenticated but semantically inconsistent
	// saved data. A winner-root row alone must not paint a selected claim.
	h := *s.head
	tree := newOrdinalTree(filepath.Join(s.dir, "pages"), h.BlockRoot)
	row := h.LastBlock
	row.WinnerBlocators = []string{}
	if err := tree.put(fmt.Sprintf("%016x", h.Height), row); err != nil {
		t.Fatal(err)
	}
	var err error
	h.BlockRoot, err = tree.flush()
	if err != nil {
		t.Fatal(err)
	}
	h.LastBlock = row
	h.Commitment = bitmapDevelopmentDigest(h)
	if err = writeBitmapChainJSON(context.Background(), filepath.Join(s.dir, "commits", h.Commitment[:2], h.Commitment+".json"), h); err != nil {
		t.Fatal(err)
	}
	if err = writeBitmapChainJSON(context.Background(), filepath.Join(s.dir, "head.json"), struct {
		Commitment string `json:"commitment"`
	}{h.Commitment}); err != nil {
		t.Fatal(err)
	}
	v, err := a.bitmapTargetCoverageContext(context.Background(), bitmapTargetCoverageQuery{Axis: "blocks", From: 0, To: 0, Pixels: 1})
	if err != nil || v.State != "partial" || v.QueryComplete || v.Bins[0].Winners != 0 || v.Bins[0].Unknown != 1 {
		t.Fatal("unvalidated selected row was painted", v, err)
	}
}

func TestBitmapTargetCoverageParcelTransactionAxisFromRealApply(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	sat, err := openIndexStore(a.dataDir, "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(998400, subsidyAtHeight(0)))
	coinbase := utxoFixtureCoinbase(998401, 10, subsidyAtHeight(1)-10)
	b1 := utxoFixtureBlock(1, b0.Hash, coinbase)
	for _, block := range []blockView{b0, b1} {
		if err = sat.appendBlock(block, 0, "pruned"); err != nil {
			t.Fatal(err)
		}
	}
	// Explicit structural fixture precondition, not a claim of Bitcoin/Sat
	// consensus history: retain the tiny valid state at a synthetic pre-origin
	// checkpoint. The actual parent reveal, Sat identities/ranges, child proof
	// receipt and both Bitmap winner publications below use their real writers.
	// This avoids inventing a Parcel winner or running792k synthetic Sat blocks.
	seed, err := sat.readBatch(sat.head.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	seed.Checkpoint.Height = bitmapDevelopmentStart - 1
	seed.Checkpoint.BlockHash = indexDigest("synthetic target-coverage pre-origin state")
	seed.Checkpoint.Commitment = checkpointHash(seed.Checkpoint)
	if err = atomicWriteJSON(filepath.Join(sat.dir, "commits", seed.Checkpoint.Commitment+".json"), seed); err != nil {
		t.Fatal(err)
	}
	sat.head.Commitment = seed.Checkpoint.Commitment
	sat.head.SatSavepointsHash = satSavepointsDigest077(sat.head)
	if err = atomicWriteJSON(filepath.Join(sat.dir, "head.json"), sat.head); err != nil {
		t.Fatal(err)
	}
	parentScript := inscriptionTestScript([]byte{1}, []byte("text/plain"), nil, []byte("792435.bitmap"))
	parentTx := utxoFixtureSpend(998402, []inputView{{N: 0, PrevTxID: coinbase.TxID, Witness: ordWitness050(parentScript)}}, 10)
	parentBlock := utxoFixtureBlock(bitmapDevelopmentStart, seed.Checkpoint.BlockHash, utxoFixtureCoinbase(998403, subsidyAtHeight(bitmapDevelopmentStart)), parentTx)
	installRunProtocolHeader(t, a, &parentBlock)
	if err = sat.appendBlock(parentBlock, 0, "pruned"); err != nil {
		t.Fatal("derive real parent Sat context", err)
	}
	district := bitmapTestConsumer(t, a.dataDir, bitmapCanonicalDistrictID)
	parcel := bitmapTestConsumer(t, a.dataDir, bitmapCanonicalParcelID)
	for _, consumer := range []*bitmapRunConsumer{district, parcel} {
		if err = consumer.Apply(ctx, parentBlock, nil); err != nil {
			t.Fatal(err)
		}
	}
	parentBytes, _ := hex.DecodeString(parentTx.TxID)
	for i, j := 0, len(parentBytes)-1; i < j; i, j = i+1, j-1 {
		parentBytes[i], parentBytes[j] = parentBytes[j], parentBytes[i]
	}
	childScript := inscriptionTestScript([]byte{1}, []byte("text/plain"), []byte{3}, parentBytes, nil, []byte("1.792435.bitmap"))
	childTx := utxoFixtureSpend(998404, []inputView{{N: 0, PrevTxID: parentTx.TxID, Witness: ordWitness050(childScript)}}, 10)
	childBlock := utxoFixtureBlock(parentBlock.Height+1, parentBlock.Hash, utxoFixtureCoinbase(998405, subsidyAtHeight(parentBlock.Height+1)), childTx)
	installRunProtocolHeader(t, a, &childBlock)
	parcel.SetBlockSource(func(_ context.Context, height int64) (blockView, error) {
		if height != parentBlock.Height {
			t.Fatalf("unexpected dependency height%d", height)
		}
		return parentBlock, nil
	})
	if err = district.Apply(ctx, childBlock, nil); err != nil {
		t.Fatal(err)
	}
	if err = parcel.Apply(ctx, childBlock, nil); err != nil {
		t.Fatal("real selected Parcel publication", err)
	}
	if parcel.head.WinnerCount != 1 {
		t.Fatal("nonempty Parcel fixture was not selected", parcel.head)
	}
	target := uint64(parentBlock.Height)
	v, err := a.bitmapTargetCoverageContext(ctx, bitmapTargetCoverageQuery{Axis: "transactions", District: &target, From: 0, To: 3, Pixels: 4})
	if err != nil || v.State != "ready" || !v.QueryComplete || v.Bins[1].Winners != 1 || v.Bins[1].Unknown != 0 || v.Snapshot.Height != childBlock.Height {
		t.Fatal("Parcel target transaction differs from reveal block", v, err)
	}
	for _, i := range []int{0, 2, 3} {
		if v.Bins[i].Winners != 0 || v.Bins[i].Unknown != 1 {
			t.Fatal("missing positions must remain unknown", v)
		}
	}
}
func TestBitmapTargetCoverageHTTPBoundsAndNoMutation(t *testing.T) {
	a, _, _ := bitmapTargetFixture(t, "0.bitmap")
	for _, query := range []string{"axis=outputs&from=0&to=1", "axis=transactions&from=0&to=1", "axis=blocks&from=2&to=1", "axis=blocks&from=0&to=1&pixels=0", "axis=blocks&from=0&to=1&pixels=1025", "axis=blocks&from=0&to=9007199254740992", "axis=blocks&from=00&to=1", "axis=blocks&from=0&to=1&district=0", "axis=blocks&from=0&to=1&instance=bitmap-districts-canonical-v2"} {
		w := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/bitmap/target-coverage?"+query, nil)
		request.RemoteAddr = "127.0.0.1:1234"
		a.handleBitmapTargetCoverage(w, request)
		if w.Code != 400 {
			t.Fatal(query, w.Code, w.Body.String())
		}
	}
	for _, test := range []struct {
		method, url string
		status      int
	}{{http.MethodGet, "http://untrusted.invalid/api/v1/bitmap/target-coverage?from=0&to=1", 403}, {http.MethodPost, "http://127.0.0.1/api/v1/bitmap/target-coverage?from=0&to=1", 405}, {http.MethodGet, "http://127.0.0.1/api/v1/bitmap/target-coverage?from=0&to=1", 200}} {
		w := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.url, nil)
		request.RemoteAddr = "127.0.0.1:1234"
		a.handleBitmapTargetCoverage(w, request)
		if w.Code != test.status {
			t.Fatal(test, w.Code, w.Body.String())
		}
		if w.Code == 200 && (!strings.Contains(w.Body.String(), `"unknown":1`) || w.Header().Get("Cache-Control") != "no-store") {
			t.Fatal(w.Header(), w.Body.String())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.bitmapTargetCoverageContext(ctx, bitmapTargetCoverageQuery{Axis: "blocks", From: 0, To: 1, Pixels: 2}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
