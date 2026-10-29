package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// Synthetic receipts isolate sparse commit semantics; real block validation is
// exercised separately through the block-map endpoint with the genesis bytes.
func browseBlock073(t *testing.T, height int64, hash string) blockView {
	t.Helper()
	tx := inscriptionTestTx(inscriptionTestScript([]byte{1}, []byte("text/plain"), nil, []byte("first")), inscriptionTestScript(nil, []byte("second")))
	tx.TxID = indexDigest(fmt.Sprintf("browse-tx-%d", height))
	b := inscriptionTestBlock(tx)
	b.Height, b.Hash = height, hash
	b.Transactions[0].TxID = indexDigest(fmt.Sprintf("coinbase-%d", height))
	return b
}
func setBrowse073(t *testing.T, a *app, id string, on bool) {
	t.Helper()
	if _, err := a.setIndexBrowsePolicy(context.Background(), indexBrowseRequest{Index: id, Enabled: &on}); err != nil {
		t.Fatal(err)
	}
}
func Test073BrowseOptInIndependentSparseAndRestart(t *testing.T) {
	a, _, hashes := timelineFixture(t, 7)
	b2, b5 := browseBlock073(t, 2, hashes[2]), browseBlock073(t, 5, hashes[5])
	assertResults := func(got []indexBrowseResult, want map[string]string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatal("unexpected browse result count", got)
		}
		seen := map[string]bool{}
		for _, result := range got {
			if seen[result.Index] || want[result.Index] != result.State || result.Error != "" {
				t.Fatal("unexpected browse result", got)
			}
			seen[result.Index] = true
		}
	}
	if got := a.indexBrowsedBlock(context.Background(), b2, nil); len(got) != 0 {
		t.Fatal("fresh profile indexed without consent", got)
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes")); !os.IsNotExist(err) {
		t.Fatal("off browsing wrote index state", err)
	}
	setBrowse073(t, a, "tx-locator", true)
	assertResults(a.indexBrowsedBlock(context.Background(), b2, nil), map[string]string{"tx-locator": "indexed"})
	if _, available, err := a.browsedBatchContext(context.Background(), "inscriptions", 2, hashes[2]); err != nil || available {
		t.Fatal("TX preference enabled inscriptions", available, err)
	}
	setBrowse073(t, a, "inscriptions", true)
	assertResults(a.indexBrowsedBlock(context.Background(), b2, nil), map[string]string{"tx-locator": "already_indexed", "inscriptions": "indexed"})
	assertResults(a.indexBrowsedBlock(context.Background(), b5, nil), map[string]string{"tx-locator": "indexed", "inscriptions": "indexed"})
	for _, id := range []string{"inscriptions", "tx-locator"} {
		s, err := indexStoreHead(a.dataDir, id)
		if err != nil || s.checkpoint != nil {
			t.Fatal("browsing advanced sequential resume", id, s, err)
		}
		track := timelineTrack(t, a.indexTimeline(indexDefinitions(), nil, nil), id)
		if !reflect.DeepEqual(track.Coverage, []heightInterval{{2, 2}, {5, 5}}) || !reflect.DeepEqual(track.Gaps, []heightInterval{{0, 1}, {3, 4}, {6, 6}}) {
			t.Fatal("invented sparse coverage", track)
		}
		provider := indexProviderRow(t, a.indexProviderViews(), id, "gateway_browsed_index")
		if !provider.Queryable || provider.Serveable || len(provider.PublicCoverage) != 0 || !reflect.DeepEqual(provider.Coverage, track.Coverage) || provider.Completeness != "recorded_sparse_singletons" {
			t.Fatal("browsed provider lost exact private coverage", provider)
		}
	}
	b, available, err := a.browsedBatchContext(context.Background(), "inscriptions", 2, hashes[2])
	if err != nil || !available || !reflect.DeepEqual(b.InscriptionCoordinates, []string{"1i0.2", "1i1.2"}) || len(b.Inscriptions) != 0 || b.Numbering != nil {
		t.Fatal("browse inscriptions not lean", b, available, err)
	}
	value, err := a.indexQueryBlock(context.Background(), "inscriptions", 2, 1000)
	if err != nil || value.(map[string]any)["known"] != true || value.(map[string]any)["transaction_count"] != uint32(2) {
		t.Fatal(value, err)
	}
	locations, err := a.browsedTransactionLocations(context.Background(), b2.Transactions[1].TxID, "", -1)
	if err != nil || len(locations) != 1 || locations[0].TxIndex != 1 || locations[0].Height != 2 {
		t.Fatal(locations, err)
	}
	setBrowse073(t, a, "tx-locator", false)
	b3 := browseBlock073(t, 3, hashes[3])
	got := a.indexBrowsedBlock(context.Background(), b3, nil)
	if len(got) != 1 || got[0].Index != "inscriptions" {
		t.Fatal("independent off ignored", got)
	}
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.status, cacheIndex: newCacheIndex()}
	policies, err := restarted.indexBrowsePolicies()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range policies {
		if p.Index == "tx-locator" && p.Enabled || p.Index == "inscriptions" && !p.Enabled {
			t.Fatal("restart lost policy", p)
		}
	}
	if _, available, err := restarted.browsedBatchContext(context.Background(), "tx-locator", 2, hashes[2]); err != nil || !available {
		t.Fatal("turning off deleted retained data", available, err)
	}
}
func Test073BrowseConcurrentDedupReorgAndTampering(t *testing.T) {
	a, raws, hashes := timelineFixture(t, 4)
	setBrowse073(t, a, "tx-locator", true)
	setBrowse073(t, a, "inscriptions", true)
	b := browseBlock073(t, 2, hashes[2])
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for _, r := range a.indexBrowsedBlock(context.Background(), b, nil) {
				if r.State == "error" {
					t.Errorf("concurrent browse: %s", r.Error)
				}
			}
		}()
	}
	workers.Wait()
	locations, err := a.browsedTransactionLocations(context.Background(), b.Transactions[1].TxID, "", -1)
	if err != nil || len(locations) != 1 {
		t.Fatal("duplicates published", locations, err)
	}
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(raws[1][:80], 2*80)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	locations, err = a.browsedTransactionLocations(context.Background(), b.Transactions[1].TxID, "", -1)
	if err != nil || len(locations) != 0 {
		t.Fatal("reorg retained stale locator", locations, err)
	}
	if got := timelineTrack(t, a.indexTimeline(indexDefinitions(), nil, nil), "tx-locator"); len(got.Coverage) != 0 {
		t.Fatal("reorg retained stale coverage", got)
	}
	f, err = os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(raws[2][:80], 2*80)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	s, head, err := a.browseStore("inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dir, "commits", head.Blocks["2"]+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("1i0.2"), []byte("1i0.3"), 1)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.browsedBatchContext(context.Background(), "inscriptions", 2, hashes[2]); err == nil {
		t.Fatal("changed coordinate receipt accepted")
	}
	if got := timelineTrack(t, a.indexTimeline(indexDefinitions(), nil, nil), "inscriptions"); len(got.Coverage) != 0 || got.CoverageComplete {
		t.Fatal("corrupt receipt claimed coverage", got)
	}
}
func Test073BrowseEveryCompleteSourceAndSequentialResume(t *testing.T) {
	a := prepareLiveTestApp063(t)
	a.settings.NetworkDisabled = true
	setBrowse073(t, a, "tx-locator", true)
	genesis, err := a.fetchAndDecodeContext(context.Background(), "0")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.browseStore("tx-locator"); err != nil {
		t.Fatal(err)
	}
	_, head, _ := a.browseStore("tx-locator")
	if len(head.Blocks) != 1 || head.Blocks["0"] == "" {
		t.Fatal("complete verified internal fetch missed its armed track", head)
	}
	s, err := openIndexStore(a.dataDir, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	s.head.Mode = "lean"
	if err = s.appendBlock(genesis, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	before := s.checkpoint.Commitment
	blockOneRaw, hash := installBlockOneHeader063(t, a)
	one, err := parseBlockDetailed(1, hash, blockOneRaw[:80], blockOneRaw, "fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := a.indexBrowsedBlock(context.Background(), one, nil); len(got) != 1 || got[0].State != "indexed" {
		t.Fatal(got)
	}
	after, err := indexStoreHead(a.dataDir, "tx-locator")
	if err != nil || after.checkpoint.Commitment != before {
		t.Fatal("browse broke resume head", after, err)
	}
	if err = s.appendBlock(one, 0, "ephemeral"); err != nil || s.checkpoint.Height != 1 {
		t.Fatal("sequential resume broken", err)
	}
	value, err := a.indexQuery("tx-locator", 1000)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(value)
	if bytes.Count(encoded, []byte(`"selection":"all"`)) != 2 {
		t.Fatal("browse duplicated sequential query", string(encoded))
	}
}
func Test073BlockMapCompleteSharedExtractionAndSafePreviews(t *testing.T) {
	b := browseBlock073(t, 2, strings.Repeat("1", 64))
	for i := len(b.Transactions); i < 47; i++ {
		b.Transactions = append(b.Transactions, transactionView{Index: i, TxID: indexDigest(fmt.Sprint(i)), OutputSats: uint64(i), VSize: 100 + i, Weight: (100 + i) * 4})
	}
	b.TransactionCount = uint64(len(b.Transactions))
	extracted, err := extractInscriptionOccurrences(b)
	if err != nil {
		t.Fatal(err)
	}
	view, err := makeBlockMap(b, extracted)
	if err != nil || view.Total != 47 || len(view.Transactions) != 47 || view.Transactions[46].OutputSats != 46 {
		t.Fatal("map was paginated or guessed values", view, err)
	}
	first := view.Transactions[1]
	if first.InscriptionCount != 2 || first.Inscription.Coordinate != "1i0.2" || first.Inscription.Preview.Kind != "text" || first.Inscription.Preview.Text != "first" {
		t.Fatal(first)
	}
	remaining := 4 << 20
	malicious := extracted.Occurrences[0]
	malicious.Envelope.ContentType = "text/html"
	malicious.Body = []byte("<script>alert(1)</script>")
	if safeBlockMapPreview(malicious, &remaining) != nil {
		t.Fatal("active HTML exposed on management map")
	}
	malicious.Envelope.ContentType = "image/svg+xml"
	if safeBlockMapPreview(malicious, &remaining) != nil {
		t.Fatal("SVG exposed on management map")
	}
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	var imageBody bytes.Buffer
	if err = png.Encode(&imageBody, img); err != nil {
		t.Fatal(err)
	}
	malicious.Envelope.ContentType = "image/png"
	malicious.Body = imageBody.Bytes()
	preview := safeBlockMapPreview(malicious, &remaining)
	if preview == nil || preview.Kind != "image" || !strings.HasPrefix(preview.DataURL, "data:image/png;base64,") {
		t.Fatal(preview)
	}
	malicious.Envelope.ContentType = "image/jpeg"
	if safeBlockMapPreview(malicious, &remaining) != nil {
		t.Fatal("mislabeled image accepted")
	}
	remaining = 0
	malicious.Envelope.ContentType = "image/png"
	if safeBlockMapPreview(malicious, &remaining) != nil {
		t.Fatal("preview exceeded shared byte budget")
	}
	b.Transactions = b.Transactions[:40]
	if _, err = makeBlockMap(b, extracted); err == nil {
		t.Fatal("incomplete block silently accepted")
	}
}
func Test073BlockMapRealBytesEndpointAndGatePromotion(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.NetworkDisabled = true
	setBrowse073(t, a, "tx-locator", true)
	w := httptest.NewRecorder()
	a.handleIndexAPI(w, httptest.NewRequest("GET", "/api/v1/index/block-map?block=0", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var view blockMapView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Total != 1 || !view.Transactions[0].Coinbase || view.Transactions[0].OutputSats != 5000000000 || view.Indexing[0].State != "already_indexed" {
		t.Fatal(view)
	}
	if !releaseFeatureAvailable("tx-locator") || !releaseInscriptionLocatorsAvailable() || !releaseFeatureAvailable("txo-spender") || releaseFeatureAvailable("gateway-peerhood") {
		t.Fatal("stage boundaries lost")
	}
	for _, args := range [][]string{{"browse", "tx-locator", "enable"}, {"browse", "inscriptions", "disable"}} {
		if _, err := a.runIndexCLI(args); err != nil {
			t.Fatal(args, err)
		}
	}
	if _, err := a.runIndexCLI([]string{"browse", "bitmap", "enable"}); err == nil {
		t.Fatal("unsupported browse executor enabled")
	}
	if _, err := a.setIndexBrowsePolicy(context.Background(), indexBrowseRequest{Index: "tx-locator"}); err == nil {
		t.Fatal("missing consent accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.setIndexBrowsePolicy(ctx, indexBrowseRequest{Index: "tx-locator", Enabled: boolPointer(true)}); err == nil {
		t.Fatal("cancelled settings write accepted")
	}
	if _, err := a.resolveTransactionViaOverlayContext(ctx, strings.Repeat("a", 64)); err == nil {
		t.Fatal("cancelled transaction navigation accepted")
	}
	if _, err := a.verifyTxLocationContext(ctx, view.Transactions[0].TxID, txLocation{Height: 0, BlockHash: genesisHashDisplay}, "test"); err == nil {
		t.Fatal("cancelled transaction verification accepted")
	}
	id := view.Transactions[0].TxID
	if _, err := hex.DecodeString(id); err != nil {
		t.Fatal(err)
	}
	r, found, err := a.indexedTransactionContext(context.Background(), id)
	if err != nil || !found || r.TxIndex != 0 || !r.TransactionVerified {
		t.Fatal("browsed locator did not resolve real transaction", r, found, err)
	}
	indexBrowseMu.Lock()
	waiting, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	err = a.setBrowseLockedTest073(waiting)
	indexBrowseMu.Unlock()
	if err == nil {
		t.Fatal("browse writer wait ignored cancellation")
	}
}

func Test073TransactionBuildResumeAndRetroactiveInscriptionLocators(t *testing.T) {
	a := prepareLiveTestApp063(t)
	zero := int64(0)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	build := func(req indexBuildRequest) indexJob {
		t.Helper()
		if _, err := a.startIndexBuild(req); err != nil {
			t.Fatal("available build rejected", req, err)
		}
		job, err := a.waitIndexBuild(ctx)
		if err != nil || job.State != "complete" {
			t.Fatal("available build did not complete", job, err)
		}
		waitLiveWriterReleased063(t, a)
		return job
	}
	build(indexBuildRequest{Index: "tx-locator", From: &zero, To: &zero, Mode: "lean", Retention: "ephemeral"})
	s, err := indexStoreHead(a.dataDir, "tx-locator")
	if err != nil || s.checkpoint == nil || s.checkpoint.Height != 0 {
		t.Fatal("transaction build has no durable checkpoint", err)
	}
	commit := s.checkpoint.Commitment
	batch, err := s.readBatch(commit)
	if err != nil || batch.TransactionLocator == nil || len(batch.TransactionLocator.Block.Transactions) != 1 || batch.TransactionLocator.Block.Transactions[0].TxIndex != 0 {
		t.Fatal("transaction build did not persist its complete actual-position payload", batch, err)
	}
	build(indexBuildRequest{Index: "tx-locator", To: &zero, Retention: "ephemeral"})
	s, err = indexStoreHead(a.dataDir, "tx-locator")
	if err != nil || s.checkpoint.Commitment != commit {
		t.Fatal("completed transaction resume rewrote history", s, err)
	}
	build(indexBuildRequest{Index: "inscriptions", From: &zero, To: &zero, Mode: "lean", ConventionalIDs: boolPointer(false)})
	source, err := indexStoreHead(a.dataDir, "inscriptions")
	if err != nil || source.checkpoint == nil {
		t.Fatal(err)
	}
	sourceCommit := source.checkpoint.Commitment
	build(indexBuildRequest{Index: "inscriptions", From: &zero, To: &zero, ConventionalIDs: boolPointer(true)})
	source, err = indexStoreHead(a.dataDir, "inscriptions")
	if err != nil || source.checkpoint.Commitment != sourceCommit {
		t.Fatal("retroactive related locator build changed inscription records", source, err)
	}
	related, err := indexStoreHead(a.dataDir, inscriptionLocatorIndex)
	if err != nil || related.checkpoint == nil {
		t.Fatal("retroactive related locator action stayed gated", err)
	}
	b, err := related.readBatch(related.checkpoint.Commitment)
	if err != nil || b.TransactionLocator == nil || b.TransactionLocator.SourceCommitment != sourceCommit || len(b.TransactionLocator.Block.Transactions) != 0 || b.TransactionLocator.Block.TransactionCount == nil || *b.TransactionLocator.Block.TransactionCount != 1 {
		t.Fatal("known-empty enrichment lost source or denominator", b, err)
	}
}
func (a *app) setBrowseLockedTest073(ctx context.Context) error {
	_, err := a.setIndexBrowsePolicy(ctx, indexBrowseRequest{Index: "inscriptions", Enabled: boolPointer(true)})
	return err
}
