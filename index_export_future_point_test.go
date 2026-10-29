package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"testing"
)

// The synthetic block uses the ordinary District and TX writers with a test
// selected header. It is not independently authenticated Bitcoin history.
func TestIndexExportDistrictFutureTXPointVisibilityAndNativeCatchup(t *testing.T) {
	a := exportTestApp(t)
	first := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	installRunProtocolHeader(t, a, &first)
	block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart+1, first.Hash, "1.bitmap")
	installRunProtocolHeader(t, a, &block)
	district := bitmapTestConsumer(t, a.dataDir, bitmapStandardDistrictID)
	if err := district.Apply(context.Background(), first, nil); err != nil {
		t.Fatal(err)
	}
	if err := district.Apply(context.Background(), block, nil); err != nil {
		t.Fatal(err)
	}
	if district.head == nil || district.head.WinnerCount != 2 {
		t.Fatal("fixture must contain two selected Districts", district.head)
	}
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}}}); err != nil {
		t.Fatal(err)
	}
	doc := a.indexRunSnapshot()
	doc.Run = &indexRunRecord{ID: "export-future-point", State: "waiting", Request: doc.Prepared, Consumers: []indexRunProgress{{Index: "tx-locator", From: first.Height, To: block.Height, Height: first.Height - 1, AdmissionState: "admitted"}}}
	a.indexRunMu.Lock()
	a.indexRunState = cloneIndexRunDocument(doc)
	err := a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session := newBrowseTXPointSession(a, ctx, doc.Run.ID)
	defer session.Abort()
	future := context.WithValue(ctx, indexBrowseTXPointSessionKey{}, session)
	future = context.WithValue(future, indexRunCoordinatorSourceKey{}, true)
	future = context.WithValue(future, indexRunSourceOwnershipKey{}, indexRunSourceOwnership{RunID: doc.Run.ID})
	stage := func(b blockView) indexBatch {
		t.Helper()
		batch, err := singletonBrowseBatch("tx-locator", b, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = lockIndexBrowse(future); err != nil {
			t.Fatal(err)
		}
		added, err := a.persistBrowseBatch(future, "tx-locator", batch)
		indexBrowseMu.Unlock()
		if added || !errors.Is(err, errBrowseTXPointStaged) {
			t.Fatal("expected private future point", added, err)
		}
		return batch
	}
	stage(first)
	if err = session.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	batch := stage(block)

	native, err := openIndexStore(a.dataDir, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	native.head.Mode = "full"
	if err = native.BeginLocatorBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if err = native.appendPreparedBlock(first, first.Height, "ephemeral", nil); err != nil {
		t.Fatal(err)
	}
	if err = native.appendPreparedBlock(block, first.Height, "ephemeral", nil); err != nil {
		t.Fatal(err)
	}
	if native.DurableCheckpoint() != nil {
		t.Fatal("native batch unexpectedly durable")
	}
	q := indexExportRequest{Index: bitmapStandardDistrictID, Representation: "conventional"}
	before := exportTestFiles(t, a.dataDir)
	missing, err := a.indexExportReview(ctx, q)
	if err != nil || !missing.Ready || missing.Complete || !missing.Partial || missing.OmittedWinners != 1 || missing.MissingCount != 2 || missing.LookupRange == nil || *missing.LookupRange != (heightInterval{block.Height, block.Height}) {
		t.Fatal("staged facts leaked into export or missing reference accounting changed", missing, err)
	}
	q.Snapshot = missing.Snapshot
	body, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.handleIndexExportDownload(w, indexExportHTTPTestRequest("POST", "download", string(body)))
	if w.Code != 200 || w.Header().Get("X-Gateway-Export-Artifact-Complete") != "true" || w.Header().Get("X-Gateway-Export-Complete") != "false" || w.Header().Get("X-Gateway-Export-Partial") != "true" || w.Header().Get("X-Gateway-Export-Omitted-Winners") != "1" || w.Header().Get("X-Gateway-Export-Snapshot") != missing.Snapshot || w.Header().Get("Content-Length") != strconv.Itoa(w.Body.Len()) {
		t.Fatal("partial artifact not safely downloadable", w.Code, w.Header(), w.Body.String())
	}
	digest := sha256.Sum256(w.Body.Bytes())
	if w.Header().Get("X-Gateway-Export-SHA256") != hex.EncodeToString(digest[:]) {
		t.Fatal("partial artifact checksum mismatch")
	}
	var winnerRows, sourceRows, omittedSourceRows int
	var footer map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(w.Body.Bytes()))
	scanner.Buffer(make([]byte, 1024), 8<<20)
	for scanner.Scan() {
		var row map[string]any
		if err = json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		switch row["type"] {
		case "bitmap_winner":
			winnerRows++
			if row["id"] != first.Transactions[1].TxID+"i0" {
				t.Fatal("partial export did not retain the verified winner", row)
			}
		case "source_block":
			sourceRows++
			record := row["record"].(map[string]any)
			if record["height"] == float64(block.Height) {
				omittedSourceRows++
				if record["winner_ids_complete"] != false || record["omitted_winner_inscriptions"] != float64(1) || len(record["winner_inscriptions"].([]any)) != 0 {
					t.Fatal("source block silently claimed a complete translation", record)
				}
			}
		case "end":
			footer = row
		}
	}
	if err = scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if winnerRows != 1 || sourceRows != 2 || omittedSourceRows != 1 || footer == nil || footer["complete"] != false || footer["partial"] != true || footer["omitted_winners"] != float64(1) || footer["missing_count"] != float64(2) {
		t.Fatal("partial export misstated omitted data", winnerRows, sourceRows, omittedSourceRows, footer)
	}
	exportTestUnchanged(t, a, before)
	q.Snapshot = ""

	if err = session.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	window, have, err := readBrowseTXPointWindow(ctx, a.dataDir, block.Height)
	if err != nil || !have || len(window.Points) != 2 || window.Points[1].Commitment != batch.Checkpoint.Commitment {
		t.Fatal("future v3 membership unavailable", window, have, err)
	}
	before = exportTestFiles(t, a.dataDir)
	meta, rows := exportTestArtifact(t, a, q)
	if !meta.Complete || rows[0]["id"] != first.Transactions[1].TxID+"i0" || rows[1]["id"] != block.Transactions[1].TxID+"i0" {
		t.Fatal("committed future point did not resolve winner", meta, rows)
	}
	exportTestUnchanged(t, a, before)

	if err = native.FlushLocatorBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if err = a.acknowledgeBrowseTXPointsNative(ctx, native); err != nil {
		t.Fatal(err)
	}
	before = exportTestFiles(t, a.dataDir)
	promoted, rows := exportTestArtifact(t, a, q)
	if !promoted.Complete || rows[0]["id"] != first.Transactions[1].TxID+"i0" || rows[1]["id"] != block.Transactions[1].TxID+"i0" {
		t.Fatal("native catchup broke lookup", promoted, rows)
	}
	if _, have, err = readBrowseTXPointWindow(ctx, a.dataDir, block.Height); err != nil || !have {
		t.Fatal("native acknowledgement hid physical point", have, err)
	}
	exportTestUnchanged(t, a, before)
}
