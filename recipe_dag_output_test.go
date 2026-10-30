package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestRecipeDAGOutputDistinguishesUncoveredFromUnknownLineage(t *testing.T) {
	ctx := context.Background()
	a, plan, _, _, first, _, child, _ := recipeDAGInspectFixture(t)
	path := func(height int64) string {
		return fmt.Sprintf("/api/v1/index/recipes/output?id=%s&revision=%s&semantic_hash=%s&height=%d", plan.Index, plan.Revision, plan.SemanticHash, height)
	}
	handler := indexInterfaceHandler(a)
	read := func(height int64) (int, map[string]any) {
		t.Helper()
		r := indexInterfaceCall(handler, "GET", path(height), "")
		var body map[string]any
		if e := json.Unmarshal(r.Body.Bytes(), &body); e != nil {
			t.Fatal(e)
		}
		return r.Code, body
	}
	code, body := read(0)
	if code != http.StatusOK || body["state"] != "covered" || body["row_count"] != float64(0) {
		t.Fatalf("verified empty DAG unit: %d %+v", code, body)
	}
	code, body = read(1)
	if code != http.StatusOK || body["state"] != "uncovered" || body["rows"] != nil {
		t.Fatalf("missing child coverage: %d %+v", code, body)
	}
	if err := first.Reconcile(ctx, map[int64]string{0: genesisHashDisplay}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	code, body = read(0)
	if code != http.StatusConflict {
		t.Fatalf("missing ancestor exposed child: %d %+v", code, body)
	}
	if err := first.Publish(ctx, recipeStoreUnit{Schema: 1, Fingerprint: first.fingerprint, Height: 0, BlockHash: genesisHashDisplay, Rows: []recipeStoreRow{}}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	recipeStoreMutex.Lock()
	head, err := child.head()
	recipeStoreMutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.beginReconcileFence(ctx, head.Revision, 0, 1, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	code, body = read(0)
	if code != http.StatusConflict {
		t.Fatalf("fenced child exposed: %d %+v", code, body)
	}
}
