package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// These fixtures use the ordinary index writers and selected-header checks.
// Their decoded blocks have synthetic verification receipts, as in the parser
// and Bitmap tests; they do not claim independently mined Bitcoin history.
func exportTestApp(t *testing.T) *app {
	t.Helper()
	a := runTestApp(t)
	// The shared genesis fixture schedules a delayed cache-inventory save.
	// Drain that pre-existing setup work before measuring export mutations.
	a.updateBackgroundWork.Wait()
	return a
}

func exportTestInscriptionFixture(t *testing.T, mode string) (*app, *indexStore, blockView) {
	t.Helper()
	a := exportTestApp(t)
	script := inscriptionTestScript([]byte{1}, []byte("text/plain"), nil, []byte("export body"))
	tx := inscriptionTestTx(append(append([]byte{}, script...), script...))
	b := inscriptionTestBlock(tx)
	b.Height, b.PreviousBlockHash = 42, indexDigest("export previous block")
	installRunProtocolHeader(t, a, &b)
	s, err := openIndexStore(a.dataDir, "inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	s.head.Mode = mode
	s.numberingValues = func(transactionView, int64) ([]uint64, error) {
		t.Fatal("export fixture unexpectedly requested numbering history")
		return nil, nil
	}
	if err = s.appendBlock(b, b.Height, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) {
		t.Fatal("export must never fetch a source block")
		return blockView{}, fmt.Errorf("unexpected export acquisition")
	}
	return a, s, b
}

func exportTestFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		out[relative] = hex.EncodeToString(digest[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func exportTestUnchanged(t *testing.T, a *app, before map[string]string) {
	t.Helper()
	if after := exportTestFiles(t, a.dataDir); !reflect.DeepEqual(before, after) {
		t.Fatal("read-only export changed source/profile bytes", before, after)
	}
}

func exportTestRows(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	rows := []map[string]any{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 1024), 8<<20)
	for scanner.Scan() {
		var row map[string]any
		decoder := json.NewDecoder(strings.NewReader(scanner.Text()))
		decoder.UseNumber()
		if err := decoder.Decode(&row); err != nil {
			t.Fatal(err, scanner.Text())
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(rows) < 2 || rows[0]["type"] != "metadata" || rows[len(rows)-1]["type"] != "end" || rows[len(rows)-1]["complete"] != true {
		t.Fatal("export lacks complete framing", rows)
	}
	data := []map[string]any{}
	for _, row := range rows[1 : len(rows)-1] {
		switch row["type"] {
		case "inscription", "bitmap_winner", "source_block":
			data = append(data, row)
		case "source_membership", "coverage":
			// Pinned-source and coverage framing may differ by representation;
			// the comparison below concerns saved record fields and identities.
		default:
			t.Fatal("unknown export record type", row)
		}
	}
	return data
}

func exportTestArtifact(t *testing.T, a *app, q indexExportRequest) (indexExportMetadata, []map[string]any) {
	t.Helper()
	meta, err := a.indexExportReview(context.Background(), q)
	if err != nil || !meta.Ready || !meta.Complete || !validHash(meta.Snapshot) {
		t.Fatal(meta, err)
	}
	q.Snapshot = meta.Snapshot
	body, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.handleIndexExportDownload(w, indexExportHTTPTestRequest("POST", "download", string(body)))
	if w.Code != 200 || w.Header().Get("X-Gateway-Export-Complete") != "true" || w.Header().Get("X-Gateway-Export-Snapshot") != meta.Snapshot || w.Header().Get("Content-Length") != strconv.Itoa(w.Body.Len()) {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	digest := sha256.Sum256(w.Body.Bytes())
	if w.Header().Get("X-Gateway-Export-SHA256") != hex.EncodeToString(digest[:]) {
		t.Fatal("attachment checksum mismatch")
	}
	return meta, exportTestRows(t, w.Body.Bytes())
}

func TestIndexExportIntegrationInscriptionsMultipleOccurrencesAndLookupOnly(t *testing.T) {
	for _, mode := range []string{"lean", "full"} {
		t.Run(mode, func(t *testing.T) {
			a, _, block := exportTestInscriptionFixture(t, mode)
			before := exportTestFiles(t, a.dataDir)
			meta, native := exportTestArtifact(t, a, indexExportRequest{Index: "inscriptions", Representation: "native"})
			if meta.Blocks != 1 || meta.Rows != 3 || !reflect.DeepEqual(meta.Coverage, []heightInterval{{42, 42}}) {
				t.Fatal(meta)
			}
			if native[0]["id"] != "1i0.42" || native[1]["id"] != "1i1.42" || native[2]["type"] != "source_block" {
				t.Fatal(native)
			}
			for _, row := range native[:2] {
				record := row["record"].(map[string]any)
				_, full := record["full"]
				if full != (mode == "full") {
					t.Fatal("depth changed during export", record)
				}
			}
			missing, err := a.indexExportReview(context.Background(), indexExportRequest{Index: "inscriptions", Representation: "conventional"})
			if mode == "lean" {
				if err != nil || missing.Ready || missing.Complete || missing.MissingCount != 2 || missing.LookupRange == nil || *missing.LookupRange != (heightInterval{42, 42}) || len(missing.Requirements) != 1 || missing.Requirements[0].Index != "tx-locator" {
					t.Fatal(missing, err)
				}
				q := indexExportRequest{Index: "inscriptions", Representation: "conventional", Snapshot: missing.Snapshot}
				body, _ := json.Marshal(q)
				w := httptest.NewRecorder()
				a.handleIndexExportDownload(w, indexExportHTTPTestRequest("POST", "download", string(body)))
				if w.Code != 409 || w.Header().Get("Content-Disposition") != "" || w.Header().Get("X-Gateway-Export-Complete") != "" || strings.Contains(w.Body.String(), `"type":"inscription"`) {
					t.Fatal(w.Code, w.Header(), w.Body.String())
				}
			} else if err != nil || !missing.Ready || !missing.Complete || missing.MissingCount != 0 {
				t.Fatal("Full original IDs should suffice without an added index", missing, err)
			}
			exportTestUnchanged(t, a, before)

			locator, err := openIndexStore(a.dataDir, "tx-locator")
			if err != nil {
				t.Fatal(err)
			}
			if err = locator.appendBlock(block, block.Height, "ephemeral"); err != nil {
				t.Fatal(err)
			}
			before = exportTestFiles(t, a.dataDir)
			convertedMeta, converted := exportTestArtifact(t, a, indexExportRequest{Index: "inscriptions", Representation: "conventional"})
			if convertedMeta.Rows != meta.Rows || convertedMeta.Blocks != meta.Blocks || len(converted) != len(native) {
				t.Fatal(convertedMeta, meta)
			}
			for i := 0; i < 2; i++ {
				want := block.Transactions[1].TxID + "i" + strconv.Itoa(i)
				if converted[i]["id"] != want {
					t.Fatal("occurrence suffix/transaction position changed", converted[i], want)
				}
				native[i]["id"] = want
			}
			if !reflect.DeepEqual(native, converted) {
				t.Fatal("representation changed nonidentity fields", native, converted)
			}
			exportTestUnchanged(t, a, before)
		})
	}
}

func TestIndexExportIntegrationPinnedProgressRestartAndStagedLookup(t *testing.T) {
	a, inscriptions, block := exportTestInscriptionFixture(t, "lean")
	q := indexExportRequest{Index: "inscriptions", Representation: "native"}
	old, err := a.indexExportReview(context.Background(), q)
	if err != nil || !old.Complete {
		t.Fatal(old, err)
	}
	lookup, err := openIndexStore(a.dataDir, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	if err = lookup.BeginLocatorBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = lookup.appendBlock(block, block.Height, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	if lookup.DurableCheckpoint() != nil {
		t.Fatal("fixture unexpectedly flushed its partial logical batch")
	}
	before := exportTestFiles(t, a.dataDir)
	missing, err := a.indexExportReview(context.Background(), indexExportRequest{Index: "inscriptions", Representation: "conventional"})
	if err != nil || missing.Complete || missing.MissingCount != 2 {
		t.Fatal("staged lookup leaked into export", missing, err)
	}
	exportTestUnchanged(t, a, before)
	if err = lookup.FlushLocatorBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if meta, err := a.indexExportReview(context.Background(), indexExportRequest{Index: "inscriptions", Representation: "conventional"}); err != nil || !meta.Complete {
		t.Fatal(meta, err)
	}

	next := inscriptionTestBlock()
	next.Height, next.PreviousBlockHash = block.Height+1, block.Hash
	installRunProtocolHeader(t, a, &next)
	if err = inscriptions.appendBlock(next, block.Height, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	q.Snapshot = old.Snapshot
	before = exportTestFiles(t, a.dataDir)
	body, _ := json.Marshal(q)
	w := httptest.NewRecorder()
	a.handleIndexExportDownload(w, indexExportHTTPTestRequest("POST", "download", string(body)))
	if w.Code != 409 || w.Header().Get("Content-Disposition") != "" {
		t.Fatal("changed snapshot was silently exported", w.Code, w.Body.String())
	}
	q.Snapshot = ""
	meta, rows := exportTestArtifact(t, a, q)
	if meta.Snapshot == old.Snapshot || meta.Blocks != 2 || len(rows) != 4 || !reflect.DeepEqual(meta.Coverage, []heightInterval{{42, 43}}) {
		t.Fatal(meta, rows)
	}
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings}
	status := a.getStatus()
	restarted.setStatus(func(s *appStatus) { *s = status })
	got, err := restarted.indexExportReview(context.Background(), q)
	if err != nil || !got.Complete || got.Snapshot != meta.Snapshot {
		t.Fatal("read-only reopen changed snapshot", got, meta, err)
	}
	exportTestUnchanged(t, a, before)
}

func TestIndexExportIntegrationUnavailableNeverCreatesArtifact(t *testing.T) {
	for _, kind := range []string{"empty", "selected anchor changed", "committed bytes corrupt"} {
		t.Run(kind, func(t *testing.T) {
			q := indexExportRequest{Index: "inscriptions", Representation: "native", Snapshot: strings.Repeat("a", 64)}
			var a *app
			if kind == "empty" {
				a = exportTestApp(t)
			} else {
				var store *indexStore
				var block blockView
				a, store, block = exportTestInscriptionFixture(t, "lean")
				meta, err := a.indexExportReview(context.Background(), indexExportRequest{Index: q.Index, Representation: q.Representation})
				if err != nil || !meta.Complete {
					t.Fatal(meta, err)
				}
				q.Snapshot = meta.Snapshot
				if kind == "committed bytes corrupt" {
					if err = os.WriteFile(filepath.Join(store.dir, "commits", store.head.Commitment+".json"), []byte("{}"), 0600); err != nil {
						t.Fatal(err)
					}
				} else {
					header, err := a.readSelectedHeader(block.Height)
					if err != nil {
						t.Fatal(err)
					}
					header[0] ^= 1
					file, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
					if err != nil {
						t.Fatal(err)
					}
					_, writeErr := file.WriteAt(header, block.Height*80)
					closeErr := file.Close()
					if writeErr != nil || closeErr != nil {
						t.Fatal(writeErr, closeErr)
					}
				}
			}
			before := exportTestFiles(t, a.dataDir)
			body, _ := json.Marshal(q)
			w := httptest.NewRecorder()
			a.handleIndexExportDownload(w, indexExportHTTPTestRequest("POST", "download", string(body)))
			if w.Code != 409 || w.Header().Get("Content-Disposition") != "" || w.Header().Get("X-Gateway-Export-Complete") != "" || strings.Contains(w.Body.String(), `"type":"inscription"`) {
				t.Fatal("unavailable source became an attachment", w.Code, w.Header(), w.Body.String())
			}
			exportTestUnchanged(t, a, before)
		})
	}
}

func TestIndexExportIntegrationCoherentFullBrowseOverridesLeanRangeOnce(t *testing.T) {
	a, rangeStore, block := exportTestInscriptionFixture(t, "lean")
	height := block.Height
	if _, err := a.prepareIndexRun(indexRunRequest{In: &height, Out: &height, Consumers: []indexRunSelection{{Index: "inscriptions", Armed: true, indexRunOptions: indexRunOptions{Mode: "full"}}}}); err != nil {
		t.Fatal(err)
	}
	full, err := singletonBrowseBatchMode(indexRunSelection{Index: "inscriptions", indexRunOptions: indexRunOptions{Mode: "full"}}, block, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.persistBrowseBatch(context.Background(), "inscriptions", full); err != nil {
		t.Fatal(err)
	}
	lean, err := rangeStore.readBatch(rangeStore.head.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	if lean.Checkpoint.RecordsHash == full.Checkpoint.RecordsHash {
		t.Fatal("fixture did not create distinct coherent depth payloads")
	}
	before := exportTestFiles(t, a.dataDir)
	for _, representation := range []string{"native", "conventional"} {
		meta, rows := exportTestArtifact(t, a, indexExportRequest{Index: "inscriptions", Representation: representation})
		if meta.Blocks != 1 || meta.Rows != 3 || !reflect.DeepEqual(meta.Coverage, []heightInterval{{42, 42}}) {
			t.Fatal("overlapping saved coverage duplicated", meta)
		}
		for _, row := range rows[:2] {
			record := row["record"].(map[string]any)
			if record["mode"] != "full" || record["full"] == nil {
				t.Fatal("coherent richer committed payload was discarded", row)
			}
		}
	}
	exportTestUnchanged(t, a, before)
}

func TestIndexExportIntegrationConflictingBrowseOccurrencesFailClosed(t *testing.T) {
	a, _, block := exportTestInscriptionFixture(t, "lean")
	height := block.Height
	if _, err := a.prepareIndexRun(indexRunRequest{In: &height, Out: &height, Consumers: []indexRunSelection{{Index: "inscriptions", Armed: true}}}); err != nil {
		t.Fatal(err)
	}
	conflict := block
	conflict.Transactions = append([]transactionView(nil), block.Transactions...)
	conflict.Transactions[1] = inscriptionTestTx(inscriptionTestScript(nil, []byte("one conflicting occurrence")))
	conflict.Transactions[1].Index = 1
	full, err := singletonBrowseBatchMode(indexRunSelection{Index: "inscriptions", indexRunOptions: indexRunOptions{Mode: "full"}}, conflict, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.persistBrowseBatch(context.Background(), "inscriptions", full); err != nil {
		t.Fatal(err)
	}
	before := exportTestFiles(t, a.dataDir)
	w := httptest.NewRecorder()
	a.handleIndexExportReview(w, indexExportHTTPTestRequest("POST", "review", `{"index":"inscriptions","representation":"native"}`))
	if w.Code != 409 || w.Header().Get("Content-Disposition") != "" {
		t.Fatal("conflicting saved occurrences were merged", w.Code, w.Body.String())
	}
	exportTestUnchanged(t, a, before)
}

func TestIndexExportIntegrationLegacyLookupFallbackReadsPrefixOnce(t *testing.T) {
	const count = 24
	a, inscriptions, first := exportTestInscriptionFixture(t, "lean")
	locator, err := openIndexStore(a.dataDir, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	blocks := []blockView{first}
	for i := 1; i < count; i++ {
		tx := inscriptionTestTx(inscriptionTestScript(nil, []byte("bounded lookup fixture")))
		tx.TxID = indexDigest(fmt.Sprintf("export lookup transaction %d", i))
		block := inscriptionTestBlock(tx)
		block.Height, block.PreviousBlockHash = first.Height+int64(i), blocks[i-1].Hash
		installRunProtocolHeader(t, a, &block)
		if err = inscriptions.appendBlock(block, first.Height, "ephemeral"); err != nil {
			t.Fatal(err)
		}
		blocks = append(blocks, block)
	}
	for _, block := range blocks {
		if err = locator.appendBlock(block, first.Height, "ephemeral"); err != nil {
			t.Fatal(err)
		}
	}
	// Older valid committed ranges may have no active acceleration table. The
	// export may spill private references, but must not repair the source index.
	for _, store := range []*indexStore{inscriptions, locator} {
		path := store.locatorActivePath()
		for _, file := range []string{path, path + ".receipt.json"} {
			if err = os.Remove(file); err != nil {
				t.Fatal(err)
			}
		}
	}
	before := exportTestFiles(t, a.dataDir)
	session := &indexExportSession{a: a, ctx: context.Background(), q: indexExportRequest{Index: "inscriptions", Representation: "conventional"}, anchors: map[int64]string{}, rawHeight: -1}
	defer session.close()
	if err = session.capture(); err != nil {
		t.Fatal(err)
	}
	// Scrambled queries prevent a descending-only cursor or tiny payload cache
	// from accidentally hiding a repeated complete-prefix traversal.
	for i := 0; i < count; i++ {
		block := blocks[(i*5)%count]
		id, err := session.identity(inscriptionCoordinate(1, 0, block.Height), block.Hash)
		if err != nil || id != block.Transactions[1].TxID+"i0" {
			t.Fatal(block.Height, id, err)
		}
	}
	if session.readBlocks > 6*count+8 {
		t.Fatalf("lookup reread a committed prefix per identity: %d reads for %d blocks", session.readBlocks, count)
	}
	exportTestUnchanged(t, a, before)
}

func exportTestBitmapFixture(t *testing.T) (*app, []blockView) {
	t.Helper()
	a := exportTestApp(t)
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
	// A bounded structural precondition, as in the target-coverage fixture:
	// promote this tiny valid Sat state to the pre-origin checkpoint. All reveal
	// extraction, parent proof receipts and winners below use ordinary writers.
	seed, err := sat.readBatch(sat.head.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	seed.Checkpoint.Height = bitmapDevelopmentStart - 1
	seed.Checkpoint.BlockHash = indexDigest("synthetic export pre-origin Sat state")
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
	parent := utxoFixtureBlock(bitmapDevelopmentStart, seed.Checkpoint.BlockHash, utxoFixtureCoinbase(998403, subsidyAtHeight(bitmapDevelopmentStart)), parentTx)
	installRunProtocolHeader(t, a, &parent)
	if err = sat.appendBlock(parent, 0, "pruned"); err != nil {
		t.Fatal(err)
	}
	district := bitmapTestConsumer(t, a.dataDir, bitmapStandardDistrictID)
	parcel := bitmapTestConsumer(t, a.dataDir, bitmapStandardParcelID)
	for _, consumer := range []*bitmapRunConsumer{district, parcel} {
		if err = consumer.Apply(context.Background(), parent, nil); err != nil {
			t.Fatal(err)
		}
	}
	parentBytes, _ := hex.DecodeString(parentTx.TxID)
	for i, j := 0, len(parentBytes)-1; i < j; i, j = i+1, j-1 {
		parentBytes[i], parentBytes[j] = parentBytes[j], parentBytes[i]
	}
	childScript := inscriptionTestScript([]byte{1}, []byte("text/plain"), []byte{3}, parentBytes, nil, []byte("1.792435.bitmap"))
	// A competing later District claim shares the child's reveal transaction;
	// only the original selected winner belongs in the comparison projection.
	childScript = append(childScript, parentScript...)
	childTx := utxoFixtureSpend(998404, []inputView{{N: 0, PrevTxID: parentTx.TxID, Witness: ordWitness050(childScript)}}, 10)
	filler := utxoFixtureSpend(998406, []inputView{{N: 0, PrevTxID: indexDigest("unrelated export input")}}, 1)
	child := utxoFixtureBlock(parent.Height+1, parent.Hash, utxoFixtureCoinbase(998405, subsidyAtHeight(parent.Height+1)), filler, childTx)
	installRunProtocolHeader(t, a, &child)
	parcel.SetBlockSource(func(_ context.Context, height int64) (blockView, error) {
		if height != parent.Height {
			t.Fatalf("unexpected dependency height %d", height)
		}
		return parent, nil
	})
	for _, consumer := range []*bitmapRunConsumer{district, parcel} {
		if err = consumer.Apply(context.Background(), child, nil); err != nil {
			t.Fatal(err)
		}
	}
	if district.head.WinnerCount != 1 || parcel.head.WinnerCount != 1 {
		t.Fatal("fixture did not publish selected D/P winners", district.head, parcel.head)
	}
	locator, err := openIndexStore(a.dataDir, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range []blockView{parent, child} {
		if err = locator.appendBlock(block, parent.Height, "ephemeral"); err != nil {
			t.Fatal(err)
		}
	}
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) {
		t.Fatal("export tried to acquire a block")
		return blockView{}, fmt.Errorf("unexpected acquisition")
	}
	return a, []blockView{parent, child}
}

func exportTestReplaceIdentities(value any, identities map[string]string) any {
	switch v := value.(type) {
	case string:
		if replacement, ok := identities[v]; ok {
			return replacement
		}
		return v
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = exportTestReplaceIdentities(item, identities)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(v))
		for i, item := range v {
			out[i] = exportTestReplaceIdentities(item, identities).(map[string]any)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			out[key] = exportTestReplaceIdentities(item, identities)
		}
		return out
	default:
		return value
	}
}

func TestIndexExportIntegrationBitmapSelectedWinnersAndParentConversion(t *testing.T) {
	a, blocks := exportTestBitmapFixture(t)
	before := exportTestFiles(t, a.dataDir)
	identities := map[string]string{}
	for _, block := range blocks {
		extracted, err := extractInscriptionOccurrences(block)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range extracted.Occurrences {
			identities[inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height)] = o.ID
		}
	}
	for _, id := range []string{bitmapStandardDistrictID, bitmapStandardParcelID} {
		t.Run(id, func(t *testing.T) {
			nativeMeta, native := exportTestArtifact(t, a, indexExportRequest{Index: id, Representation: "native"})
			idMeta, converted := exportTestArtifact(t, a, indexExportRequest{Index: id, Representation: "conventional"})
			if nativeMeta.Blocks != 2 || nativeMeta.Rows != 3 || idMeta.Rows != nativeMeta.Rows {
				t.Fatal(nativeMeta, idMeta)
			}
			if len(native) != 3 || native[0]["type"] != "bitmap_winner" {
				t.Fatal(native)
			}
			if got := exportTestReplaceIdentities(native, identities); !reflect.DeepEqual(got, converted) {
				t.Fatal("representation changed nonidentity fields", got, converted)
			}
			winner := native[0]
			if id == bitmapStandardDistrictID {
				if winner["bitmap"] != json.Number("792435") || winner["id"] != "1i0.792435" {
					t.Fatal("District comparison pair lost selected target", winner)
				}
			} else {
				if winner["district"] != json.Number("792435") || winner["transaction"] != json.Number("1") || winner["id"] != "2i0.792436" {
					t.Fatal("Parcel target transaction confused with reveal position", winner)
				}
				record := winner["record"].(map[string]any)
				parents := record["parent_inscriptions"].([]any)
				proof := record["provenance"].(map[string]any)
				if len(parents) != 1 || parents[0] != "1i0.792435" || proof["parent_inscription"] != parents[0] || proof["child_inscription"] != winner["id"] {
					t.Fatal("parent/provenance references lost", record)
				}
			}
		})
	}
	exportTestUnchanged(t, a, before)
}
