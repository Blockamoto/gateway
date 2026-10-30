package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type recipeSparseHashResponse struct {
	header http.Header
	code   int
	bytes  int64
	hash   hash.Hash
}

func (w *recipeSparseHashResponse) Header() http.Header  { return w.header }
func (w *recipeSparseHashResponse) WriteHeader(code int) { w.code = code }
func (w *recipeSparseHashResponse) Write(data []byte) (int, error) {
	if w.code == 0 {
		w.code = 200
	}
	n, err := w.hash.Write(data)
	w.bytes += int64(n)
	return n, err
}

func waitRecipeSparseLargeRun(t *testing.T, a *app) indexRunDocument {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		document := a.indexRunSnapshot()
		if document.Run != nil {
			state := document.Run.State
			if state == "complete" || state == "complete_with_errors" || state == "failed" || state == "paused" || state == "cancelled" {
				a.indexMu.Lock()
				active := a.indexCancel != nil
				a.indexMu.Unlock()
				if !active {
					return document
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("large-output run did not settle: %+v", a.indexRunSnapshot().Run)
	return indexRunDocument{}
}

func TestRecipeSparseCoordinatorLargeEventBlockParentAndDownload(t *testing.T) {
	// Both programs consume a real authenticated Bitcoin genesis block. The
	// parent repeats a legal variable across 120 columns in each of three tables, producing >64 MiB
	// from one event without a giant source field or giant recipe document.
	ctx := context.Background()
	a, source := transactionRunFixture(t)
	d := locatorRecipeProgram(t).definition
	d.ID = "large-parent"
	d.Name = d.ID
	literal, _ := json.Marshal(strings.Repeat("p", 200<<10))
	d.Steps = append([]recipeStep{{Op: "let", As: "payload", Value: &recipeExpr{Literal: literal}}}, d.Steps...)
	for i := 0; i < 120; i++ {
		d.Steps[len(d.Steps)-1].Row[fmt.Sprintf("payload_%02d", i)] = recipeExpr{Variable: "payload"}
	}
	for i := 1; i < 3; i++ {
		table := d.Tables[0]
		table.Name = fmt.Sprintf("copy_%d", i)
		d.Tables = append(d.Tables, table)
		step := d.Steps[len(d.Steps)-1]
		step.Table = table.Name
		d.Steps = append(d.Steps, step)
	}
	d.Exports = []recipeExport{{Name: "tx", Kind: "same_block", Table: d.Tables[0].Name, Column: "txid", Type: "text"}}
	raw, _ := json.Marshal(d)
	parent := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(raw), "")))
	childDefinition := locatorRecipeProgram(t).definition
	childDefinition.ID = "large-child"
	childDefinition.Name = childDefinition.ID
	childDefinition.Dependencies = []recipeDependency{{Name: "parent", Instance: parent.Recipe.ID, Revision: parent.Revision, SemanticHash: parent.SemanticHash, Export: "tx", Coverage: "same_block", Type: "text"}}
	childDefinition.Steps[len(childDefinition.Steps)-1].Row["parent_txid"] = recipeExpr{Call: "dependency.lookup", Args: []recipeExpr{{Literal: json.RawMessage(`"parent"`)}, {Field: []string{"txid"}}}}
	raw, _ = json.Marshal(childDefinition)
	child := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(raw), "")))
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		return source[target.Height], nil
	}
	z := int64(0)
	request := indexRunRequest{In: &z, Out: &z, Consumers: []indexRunSelection{{Index: parent.Recipe.ID, Armed: true}, {Index: child.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, request)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, request, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	done := waitRecipeSparseLargeRun(t, a)
	if done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
	page, _, err := a.readRecipeOutput(ctx, parent.Recipe.ID, parent.Revision, parent.SemanticHash, 0, 0, 50, "")
	if err != nil || page["row_count"] != 3 || page["oversized_row"] != true || page["row_download"] == nil || len(page["rows"].([]recipeStoreRow)) != 0 {
		t.Fatal("large row not discoverable", page, err)
	}
	store := recipeTestOpen(t, a.dataDir, parent.Recipe.ID, parent.SemanticHash)
	head, err := store.head()
	if err != nil {
		t.Fatal(err)
	}
	var ref recipeStoreRef
	if found, err := store.tree(ctx, head).get(recipeStoreHeightKey(0), &ref); err != nil || !found {
		t.Fatal(found, err)
	}
	view, err := store.openSparseView(ctx, ref, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	if view.bytes <= recipeMaxBlockOutputBytes || view.info.Rows != 3 {
		t.Fatal("fixture no longer exercises previous block boundary", view.bytes)
	}
	coverage, err := store.Coverage(ctx, 0, 0, nil, 0, 1)
	if err != nil || len(coverage.Anchors) != 1 {
		t.Fatal("large coverage unavailable", coverage, err)
	}
	childPage, _, err := a.readRecipeOutput(ctx, child.Recipe.ID, child.Revision, child.SemanticHash, 0, 0, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	var childRow map[string]any
	if err = json.Unmarshal(childPage["rows"].([]recipeStoreRow)[0].Value, &childRow); err != nil || childRow["parent_txid"] != source[0].Transactions[0].TxID {
		t.Fatal("large parent scalar lookup failed", childRow, err)
	}
	resolved, err := a.ResolveSavedRecipe(ctx, parent.Recipe.ID, parent.Revision, parent.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"row", "unit"} {
		path := recipeOutputDownloadURL(resolved, 0, 0, ref.Unit, kind)
		if kind == "row" && path != page["row_download"] {
			t.Fatal("row attachment identity differs")
		}
		request := httptest.NewRequest("GET", "http://127.0.0.1:9090"+path, nil)
		request.RemoteAddr = "127.0.0.1:50000"
		request.Header.Set("Origin", "http://127.0.0.1:9090")
		writer := &recipeSparseHashResponse{header: http.Header{}, hash: sha256.New()}
		indexInterfaceHandler(a).ServeHTTP(writer, request)
		if writer.code != 200 || !strings.HasPrefix(writer.header.Get("Content-Disposition"), "attachment;") {
			t.Fatal("download failed", kind, writer.code, writer.header)
		}
		if kind == "unit" && (writer.bytes != view.bytes || hex.EncodeToString(writer.hash.Sum(nil)) != ref.Unit) {
			t.Fatal("unit attachment changed committed bytes")
		}
		if kind == "row" {
			span, err := view.RowSpan(0)
			if err != nil || writer.bytes != span.Row.Bytes {
				t.Fatal("row attachment extent differs", err)
			}
		}
	}
	artifact := &recipeSparseHashResponse{header: http.Header{}, hash: sha256.New()}
	export, err := a.writeRecipeRangeExport(ctx, resolved, 0, 0, artifact)
	if err != nil || export.Rows != 3 || export.CoveredUnits != 1 || export.UncoveredBlocks != 0 || artifact.bytes <= view.bytes {
		t.Fatal("large generic range export failed", export, artifact.bytes, err)
	}
	// A second finite run must reuse this large committed prefix without
	// reading the source block again.
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) {
		t.Error("reused large prefix fetched source")
		return blockView{}, fmt.Errorf("unexpected source read")
	}
	plan, err = a.planIndexRun(ctx, request)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, request, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done = waitRecipeSparseLargeRun(t, a); done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
}
