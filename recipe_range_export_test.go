package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Synthetic committed-row fixtures: publication authority is supplied by the
// test. Export validates retained receipt lineage and selected local headers;
// it does not independently admit raw blocks or assert consensus validity.
func recipeRangeExportFixture(t *testing.T, emptySecond bool) (*app, resolvedSavedRecipe, *recipeStore) {
	t.Helper()
	a, view, _ := savedRecipeRunFixture(t)
	root, err := a.ResolveSavedRecipe(context.Background(), view.Recipe.ID, view.Revision, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	store := recipeTestOpen(t, a.dataDir, root.ID, root.SemanticHash)
	unit := recipeStoreUnit{Schema: 1, Fingerprint: root.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Rows: []recipeStoreRow{{Key: `["items","a"]`, Value: json.RawMessage(`{"ref":"a","exact":9007199254740993}`)}, {Key: `["items","b"]`, Value: json.RawMessage(`{"ref":"b"}`)}}}
	if err = store.Publish(context.Background(), unit, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if emptySecond {
		header, err := readHeaderAt(a.headersPath, 1)
		if err != nil {
			t.Fatal(err)
		}
		hash := hash256(header)
		unit.Height = 1
		unit.BlockHash = reverseHex(hash[:])
		unit.Rows = []recipeStoreRow{}
		if err = store.Publish(context.Background(), unit, recipeTestGuard); err != nil {
			t.Fatal(err)
		}
	}
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) {
		t.Error("export fetched a source block")
		return blockView{}, fmt.Errorf("unexpected source read")
	}
	return a, root, store
}
func recipeRangeExportLines(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	result := []map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	for {
		var record map[string]any
		err := decoder.Decode(&record)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, record)
	}
	return result
}
func TestRecipeRangeExportExplicitCoverageAndExactRows(t *testing.T) {
	for _, second := range []bool{false, true} {
		t.Run(fmt.Sprint(second), func(t *testing.T) {
			a, root, _ := recipeRangeExportFixture(t, second)
			var output bytes.Buffer
			meta, err := a.writeRecipeRangeExport(context.Background(), root, 0, 1, &output)
			if err != nil {
				t.Fatal(err)
			}
			if meta.Rows != 2 || meta.CoveredUnits != 1+int64(boolInt(second)) || meta.UncoveredBlocks != int64(boolInt(!second)) {
				t.Fatal(meta)
			}
			lines := recipeRangeExportLines(t, output.Bytes())
			if lines[0]["type"] != "manifest" || lines[len(lines)-1]["type"] != "complete" {
				t.Fatal("missing artifact boundaries")
			}
			if !strings.Contains(output.String(), "9007199254740993") {
				t.Fatal("exact integer changed")
			}
			units, rows, gaps := 0, 0, 0
			for _, line := range lines {
				switch line["type"] {
				case "row":
					rows++
				case "unit":
					units++
					if line["height"] == json.Number("1") && line["row_count"] != json.Number("0") {
						t.Fatal("evaluated empty block became uncovered")
					}
				case "uncovered":
					gaps++
					if line["from"] != json.Number("1") || line["to"] != json.Number("1") {
						t.Fatal(line)
					}
				}
			}
			if rows != 2 || units != int(meta.CoveredUnits) || gaps != int(meta.UncoveredBlocks) {
				t.Fatal(rows, units, gaps)
			}
		})
	}
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

type recipeRangeExportMutationWriter struct {
	bytes.Buffer
	mutate func()
	done   bool
}

func (w *recipeRangeExportMutationWriter) Write(data []byte) (int, error) {
	n, err := w.Buffer.Write(data)
	if !w.done && bytes.Contains(data, []byte(`"type":"row"`)) {
		w.done = true
		w.mutate()
	}
	return n, err
}
func TestRecipeRangeExportRejectsPriorRowsAndAncestorChanges(t *testing.T) {
	for _, mode := range []string{"ancestor", "header", "history", "cancel", "outside-append"} {
		t.Run(mode, func(t *testing.T) {
			a, _, _ := recipeRangeExportFixture(t, false)
			leaf := recipeLineageSaveFixture(t, a, "export-leaf", nil)
			leafRef, leafStore := recipeLineagePublishFixture(t, a, leaf, nil)
			middle := recipeLineageSaveFixture(t, a, "export-middle", []recipeDependency{recipeGraphFixtureEdge("up", leaf)})
			middleRef, _ := recipeLineagePublishFixture(t, a, middle, []recipeDependencyReceipt{{Name: "up", Instance: leaf.ID, SemanticHash: leaf.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Unit: leafRef.Unit}})
			root := recipeLineageSaveFixture(t, a, "export-root", []recipeDependency{recipeGraphFixtureEdge("middle", middle)})
			store := recipeTestOpen(t, a.dataDir, root.ID, root.SemanticHash)
			unit := recipeStoreUnit{Schema: 1, Fingerprint: root.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Rows: []recipeStoreRow{{Key: `["items","x"]`, Value: json.RawMessage(`{"ref":"x"}`)}}, Lineage: []recipeDependencyReceipt{{Name: "middle", Instance: middle.ID, SemanticHash: middle.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Unit: middleRef.Unit}}}
			if err := store.publish(context.Background(), unit, nil, recipeTestGuard, func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writer := &recipeRangeExportMutationWriter{mutate: func() {
				switch mode {
				case "ancestor":
					if err := leafStore.Reconcile(context.Background(), map[int64]string{0: genesisHashDisplay}, recipeTestGuard); err != nil {
						t.Fatal(err)
					}
				case "header":
					data, err := os.ReadFile(a.headersPath)
					if err != nil {
						t.Fatal(err)
					}
					data[0] ^= 1
					a.headerChainMu.Lock()
					err = os.WriteFile(a.headersPath, data, 0600)
					a.headerChainMu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
				case "history":
					head, err := leafStore.head()
					if err != nil {
						t.Fatal(err)
					}
					raw, _ := json.Marshal(head)
					if err = os.Remove(filepath.Join(leafStore.dir, "heads", recipeStoreHash(raw)+".json")); err != nil {
						t.Fatal(err)
					}
				case "cancel":
					cancel()
				case "outside-append":
					header, err := readHeaderAt(a.headersPath, 1)
					if err != nil {
						t.Fatal(err)
					}
					hash := hash256(header)
					unit := recipeStoreUnit{Schema: 1, Fingerprint: leaf.SemanticHash, Height: 1, BlockHash: reverseHex(hash[:]), Rows: []recipeStoreRow{}}
					if err = leafStore.Publish(ctx, unit, recipeTestGuard); err != nil {
						t.Fatal(err)
					}
				}
			}}
			_, err := a.writeRecipeRangeExport(ctx, root, 0, 0, writer)
			if !writer.done {
				t.Fatal("mutation did not run")
			}
			if mode == "outside-append" {
				if err != nil {
					t.Fatal("outside append invalidated immutable export range", err)
				}
			} else if err == nil {
				t.Fatal("accepted changed export evidence", mode)
			}
			if err != nil && strings.Contains(writer.String(), `"type":"complete"`) {
				t.Fatal("failed artifact claims completeness")
			}
		})
	}
}

func TestRecipeRangeExportHTTPStagesBeforeAttachment(t *testing.T) {
	a, root, store := recipeRangeExportFixture(t, false)
	path := fmt.Sprintf("/api/v1/index/recipes/export?id=%s&revision=%s&semantic_hash=%s&from=0&to=1", root.ID, root.Revision, root.SemanticHash)
	call := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("GET", "http://127.0.0.1:9090"+path, nil)
		request.RemoteAddr = "127.0.0.1:50000"
		request.Header.Set("Origin", "http://127.0.0.1:9090")
		response := httptest.NewRecorder()
		indexInterfaceHandler(a).ServeHTTP(response, request)
		return response
	}
	response := call(path)
	if response.Code != 200 || response.Header().Get("X-Gateway-Export-Artifact-Complete") != "true" || response.Header().Get("X-Gateway-Export-Partial") != "true" || !strings.HasPrefix(response.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatal(response.Code, response.Header(), response.Body.String())
	}
	digest := sha256.Sum256(response.Body.Bytes())
	if response.Header().Get("X-Gateway-Export-SHA256") != hex.EncodeToString(digest[:]) {
		t.Fatal("artifact checksum differs")
	}
	if response = call(path + "&from=0"); response.Code != http.StatusBadRequest {
		t.Fatal("duplicate query accepted")
	}
	head, _ := store.head()
	var ref recipeStoreRef
	_, _ = store.tree(context.Background(), head).get(recipeStoreHeightKey(0), &ref)
	if err := os.WriteFile(filepath.Join(store.dir, "units", ref.Unit+".json"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	response = call(path)
	if response.Code != 409 || response.Header().Get("Content-Disposition") != "" || response.Header().Get("X-Gateway-Export-Artifact-Complete") != "" {
		t.Fatal("corrupt output became an attachment", response.Code, response.Header())
	}
}
