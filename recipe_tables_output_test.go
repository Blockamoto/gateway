package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestRecipeTablesOutputSnapshotsCursorEmptyAndCorruption(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	p := recipeTablesTestProgram(t, "table-pages", 0, false)
	p.ordered.Steps[0].Body[0].Body = append(p.ordered.Steps[0].Body[0].Body,
		recipeOrderedInstruction{Op: "put", Table: "counts", Key: roLit(rovText("height")), Expr: roField("height")})
	p.definition.Ordered, _ = recipeOrderedProgramJSON(*p.ordered)
	v := saveTablesRecipeForRun(t, a, p)
	p, _ = compileRecipe(v.Recipe)
	g, _ := openRecipeTablesGeneration(a.dataDir, p)
	if err := g.create(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.apply(ctx, blocks[0], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	first, _, err := a.readRecipeOutput(ctx, v.Recipe.ID, v.Revision, v.SemanticHash, 0, 0, 1, "")
	if err != nil || first["state_entry_count"] != "2" || len(first["rows"].([]recipeStoreRow)) != 1 {
		t.Fatal(first, err)
	}
	unit, cursor := first["unit"].(string), first["next_cursor"].(string)
	page := func(token, after string) (map[string]any, int, error) {
		return a.readRecipeOutputPage(ctx, v.Recipe.ID, v.Revision, v.SemanticHash, 0, 0, 1, token, after)
	}
	last, _, err := page(unit, cursor)
	if err != nil || len(last["rows"].([]recipeStoreRow)) != 1 || last["next_cursor"] != nil {
		t.Fatal(last, err)
	}
	if first["rows"].([]recipeStoreRow)[0].Key == last["rows"].([]recipeStoreRow)[0].Key {
		t.Fatal("cursor repeated row")
	}
	if _, code, err := page("", cursor); code != 400 || err == nil {
		t.Fatal("unbound cursor", code, err)
	}
	if _, code, err := page(strings.Repeat("f", 64), cursor); code != 409 || err == nil {
		t.Fatal("wrong snapshot", code, err)
	}
	if _, code, err := page(unit, strings.Repeat("f", 68)); code != 409 || err == nil {
		t.Fatal("absent cursor", code, err)
	}
	if err = g.apply(ctx, blocks[1], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	continued, _, err := page(unit, cursor)
	if err != nil || indexDigest(continued) != indexDigest(last) {
		t.Fatal("append invalidated retained table snapshot", continued, err)
	}
	// A later display edit retains access to the admitted immutable definition.
	changed := v.Recipe
	changed.Name = "new display name"
	raw, _ := json.Marshal(changed)
	registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(raw), v.SemanticHash, v.Revision)))
	if _, _, err = page(unit, cursor); err != nil {
		t.Fatal("historical recipe revision unavailable", err)
	}
	good, _ := os.ReadFile(g.path("table-head.json"))
	if err = os.Remove(g.path("table-head.json")); err != nil {
		t.Fatal(err)
	}
	if _, code, err := page(unit, cursor); code != 409 || err == nil {
		t.Fatal("lost table head became uncovered", code, err)
	}
	if err = os.WriteFile(g.path("table-head.json"), good, 0600); err != nil {
		t.Fatal(err)
	}
	headers, _ := os.ReadFile(a.headersPath)
	bad := append([]byte(nil), headers...)
	bad[79] ^= 1
	if err = os.WriteFile(a.headersPath, bad, 0600); err != nil {
		t.Fatal(err)
	}
	if _, code, err := page(unit, cursor); code != 409 || err == nil {
		t.Fatal("disconnected table snapshot readable", code, err)
	}
	if err = os.WriteFile(a.headersPath, headers, 0600); err != nil {
		t.Fatal(err)
	}
	empty := saveTablesRecipeForRun(t, a, recipeTablesTestProgram(t, "empty-table-page", 0, true))
	ep, _ := compileRecipe(empty.Recipe)
	eg, _ := openRecipeTablesGeneration(a.dataDir, ep)
	if err = eg.create(ctx); err != nil {
		t.Fatal(err)
	}
	if err = eg.apply(ctx, blocks[0], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	out, _, err := a.readRecipeOutput(ctx, empty.Recipe.ID, empty.Revision, empty.SemanticHash, 0, 0, 1, "")
	if err != nil || out["state"] != "covered" || out["state_entry_count"] != "0" || len(out["rows"].([]recipeStoreRow)) != 0 {
		t.Fatal("evaluated empty table snapshot", out, err)
	}
}

func TestRecipeTablesOutputConsecutiveLargeRowsAndExactDownloads(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	p := recipeTablesTestProgram(t, "large-table-rows", 0, false)
	large := rovText(strings.Repeat("x", 100<<10))
	value := recipeOrderedExpr{Object: map[string]recipeOrderedExpr{"first": roVar("payload"), "second": roVar("payload")}}
	for i := 2; i < 12; i++ {
		value.Object[fmt.Sprintf("part%d", i)] = roVar("payload")
	}
	p.ordered.Steps[0].Body[0].Body = []recipeOrderedInstruction{
		{Op: "let", Name: "payload", Expr: roLit(large)},
		{Op: "put", Table: "counts", Key: roLit(rovText("one")), Expr: value},
		{Op: "put", Table: "counts", Key: roLit(rovText("two")), Expr: value},
	}
	p.definition.Ordered, _ = recipeOrderedProgramJSON(*p.ordered)
	v := saveTablesRecipeForRun(t, a, p)
	p, _ = compileRecipe(v.Recipe)
	g, _ := openRecipeTablesGeneration(a.dataDir, p)
	if err := g.create(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.apply(ctx, blocks[0], orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	params := url.Values{"id": {v.Recipe.ID}, "revision": {v.Revision}, "semantic_hash": {v.SemanticHash}, "height": {"0"}, "limit": {"100"}}
	handler := indexInterfaceHandler(a)
	seen := map[string]bool{}
	var download string
	for i := 0; i < 2; i++ {
		response := indexInterfaceCall(handler, "GET", "/api/v1/index/recipes/output?"+params.Encode(), "")
		if response.Code != 200 || response.Body.Len() > recipeOutputMaxResponseBytes {
			t.Fatal(response.Code, response.Body.Len(), response.Body.String())
		}
		var page struct {
			State, Unit, NextCursor, OversizedKey, RowDownload string
			Rows                                               []recipeStoreRow
			OversizedRow                                       bool
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(response.Body.Bytes(), &fields) != nil {
			t.Fatal("invalid JSON page")
		}
		json.Unmarshal(fields["state"], &page.State)
		json.Unmarshal(fields["unit"], &page.Unit)
		json.Unmarshal(fields["next_cursor"], &page.NextCursor)
		json.Unmarshal(fields["oversized_key"], &page.OversizedKey)
		json.Unmarshal(fields["row_download"], &page.RowDownload)
		json.Unmarshal(fields["oversized_row"], &page.OversizedRow)
		json.Unmarshal(fields["rows"], &page.Rows)
		if page.State != "covered" || !page.OversizedRow || len(page.Rows) != 0 || page.RowDownload == "" || seen[page.OversizedKey] {
			t.Fatal("large row was skipped/repeated", response.Body.String())
		}
		seen[page.OversizedKey] = true
		download = page.RowDownload
		attachment := indexInterfaceCall(handler, "GET", download, "")
		digest := sha256.Sum256(attachment.Body.Bytes())
		if attachment.Code != 200 || attachment.Body.Len() <= recipeOutputMaxResponseBytes || attachment.Header().Get("X-Content-SHA256") != hex.EncodeToString(digest[:]) || attachment.Header().Get("Content-Disposition") == "" {
			t.Fatal("large row download", attachment.Code, attachment.Header())
		}
		var row recipeStoreRow
		var typed recipeOrderedValue
		if json.Unmarshal(attachment.Body.Bytes(), &row) != nil || row.Key != page.OversizedKey || json.Unmarshal(row.Value, &typed) != nil || typed.Object["first"].Text != large.Text || typed.Object["second"].Text != large.Text {
			t.Fatal("download changed typed row")
		}
		if i == 0 && page.NextCursor == "" || i == 1 && page.NextCursor != "" {
			t.Fatal("large row continuation", i, page.NextCursor)
		}
		params.Set("unit", page.Unit)
		params.Set("cursor", page.NextCursor)
	}
	// Both rows share one immutable blob. A damaged blob must fail before
	// attachment headers or payload bytes are exposed.
	_, point, err := g.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var entry recipeTablesEntry
	if ok, err := recipeTablesTree(ctx, g.path("pages"), point.Root).get(recipeTablesKey(recipeStateKey{Table: "counts", Key: "one"}), &entry); err != nil || !ok || entry.Blob == "" {
		t.Fatal(entry, err)
	}
	if err = os.WriteFile(recipeTablesBlobPath(g.profileRoot, entry.Blob), []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	response := indexInterfaceCall(handler, "GET", download, "")
	if response.Code != 409 || response.Header().Get("Content-Disposition") != "" || response.Body.Len() > 4096 {
		t.Fatal("corrupt row escaped download fence", response.Code, response.Header())
	}
}
