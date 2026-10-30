package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func projectionTestSave(t *testing.T, a *app, d recipeDefinition) resolvedSavedRecipe {
	t.Helper()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	view, canonical, err := recipeRegistryCompile(raw)
	if err != nil {
		t.Fatal(err)
	}
	recipeRegistryMu.Lock()
	view, err = recipeRegistrySave(context.Background(), a.dataDir, view, canonical, "", "")
	recipeRegistryMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := a.ResolveSavedRecipe(context.Background(), d.ID, view.Revision, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestRecipeProjectionOutputPinnedPagesAndParentAuthority(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	g := projectionTestGeneration(t, a.dataDir, "query-addresses", false)
	resolved := projectionTestSave(t, a, g.program.definition)
	read := func(height int64, token, cursor string) (map[string]any, int, error) {
		return a.readProjectionRecipeOutput(ctx, resolved, height, 1, token, cursor)
	}
	if out, _, err := read(0, "", ""); err != nil || out["state"] != "uncovered" {
		t.Fatal("missing dependency fabricated an evaluated output", out, err)
	}
	if _, err := os.Stat(g.profileRoot); !os.IsNotExist(err) {
		t.Fatal("query created projection state", err)
	}
	u, err := openUTXOStoreContext(ctx, a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	blocks := recipeCoordinatorSource(t)
	canonical := func(height int64) (string, error) {
		header, err := a.readSelectedHeader(height)
		if err != nil {
			return "", err
		}
		digest := hash256(header)
		return reverseHex(digest[:]), nil
	}
	// The fixture guard provides registry serialization, not production consent.
	for height := int64(0); height <= 1; height++ {
		utxoApply(t, u, blocks[height])
		if err = g.applySnapshot(ctx, a, height, "", canonical, projectionGenerationFixtureGuard, nil); err != nil {
			t.Fatal(err)
		}
	}
	empty, _, err := read(0, "", "")
	if err != nil || empty["state"] != "covered" || empty["included_output_count"] != "0" || len(empty["rows"].([]recipeStoreRow)) != 0 {
		t.Fatal("evaluated empty genesis unavailable", empty, err)
	}
	old, _, err := read(1, "", "")
	if err != nil || old["included_output_count"] != "1" || old["next_cursor"] != nil {
		t.Fatal("block-one projection", old, err)
	}
	oldUnit := old["unit"].(string)
	header, err := a.readSelectedHeader(1)
	if err != nil {
		t.Fatal(err)
	}
	previous, _ := hex.DecodeString(blocks[1].Hash)
	for i := 0; i < 32; i++ {
		header[4+i] = previous[31-i]
	}
	header[79] ^= 1
	digest := hash256(header)
	tx := utxoFixtureCoinbase(88001, 3, 5, 7)
	tx.Outputs[0].ScriptPubKey, tx.Outputs[1].ScriptPubKey, tx.Outputs[2].ScriptPubKey = address076P2PKH, address076P2PKH, address076P2SH
	b2 := utxoFixtureBlock(2, blocks[1].Hash, tx)
	b2.Hash = reverseHex(digest[:])
	file, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteAt(header, 160)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	utxoApply(t, u, b2)
	if err = g.applySnapshot(ctx, a, 2, "", canonical, projectionGenerationFixtureGuard, nil); err != nil {
		t.Fatal(err)
	}
	continued, _, err := read(1, oldUnit, "")
	if err != nil || indexDigest(continued) != indexDigest(old) {
		t.Fatal("outside append changed historical output", continued, err)
	}
	seen, cursor, unit := map[string]bool{}, "", ""
	for {
		out, _, err := read(2, unit, cursor)
		if err != nil || out["included_output_count"] != "4" || out["excluded_rows_retained"] != false {
			t.Fatal(out, err)
		}
		for _, row := range out["rows"].([]recipeStoreRow) {
			if seen[row.Key] {
				t.Fatal("duplicate member page", row.Key)
			}
			seen[row.Key] = true
		}
		if out["next_cursor"] == nil {
			break
		}
		cursor, unit = out["next_cursor"].(string), out["unit"].(string)
	}
	if len(seen) != 4 || cursor == "" {
		t.Fatal("member traversal lost outputs", seen)
	}
	filtered, _, err := a.readProjectionRecipeDestination(ctx, resolved, 2, 1, "", "", address076P2PKH)
	if err != nil || filtered["balance_sats"] != "8" || filtered["output_count"] != "2" || filtered["scope"] != "recipe_members" || filtered["complete"] != true || len(filtered["rows"].([]recipeStoreRow)) != 1 || filtered["next_cursor"] == nil {
		t.Fatal("direct reverse destination lookup", filtered, err)
	}
	filteredUnit, filteredCursor := filtered["unit"].(string), filtered["next_cursor"].(string)
	_, paymentAddress, err := addressStateScript(address076P2PKH)
	if err != nil {
		t.Fatal(err)
	}
	last, _, err := a.readProjectionRecipeDestination(ctx, resolved, 2, 1, filteredUnit, filteredCursor, paymentAddress)
	if err != nil || len(last["rows"].([]recipeStoreRow)) != 1 || last["next_cursor"] != nil || last["unit"] != filteredUnit {
		t.Fatal("address and exact script did not share a pinned identity", last, err)
	}
	if _, code, err := a.readProjectionRecipeDestination(ctx, resolved, 2, 1, filteredUnit, filteredCursor, address076P2SH); err == nil || code != 409 {
		t.Fatal("destination continuation crossed scripts", code, err)
	}
	if _, code, err := read(2, filteredUnit, filteredCursor); err == nil || code != 409 {
		t.Fatal("filtered token was accepted as an unfiltered page", code, err)
	}
	absent, _, err := a.readProjectionRecipeDestination(ctx, resolved, 2, 1, "", "", "0014"+strings.Repeat("44", 20))
	if err != nil || absent["state"] != "covered" || absent["output_count"] != "0" || absent["balance_sats"] != "0" || len(absent["rows"].([]recipeStoreRow)) != 0 {
		t.Fatal("known absent destination was not an evaluated zero", absent, err)
	}
	params := url.Values{"id": {resolved.ID}, "revision": {resolved.Revision}, "semantic_hash": {resolved.SemanticHash}, "height": {"2"}, "destination": {paymentAddress}, "limit": {"1"}}
	response := indexInterfaceCall(indexInterfaceHandler(a), "GET", "/api/v1/index/recipes/output?"+params.Encode(), "")
	var wire map[string]any
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &wire) != nil || wire["balance_sats"] != "8" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("destination HTTP route", response.Code, response.Body.String())
	}
	for _, input := range []string{"", "invalid", "51"} {
		params.Set("destination", input)
		response = indexInterfaceCall(indexInterfaceHandler(a), "GET", "/api/v1/index/recipes/output?"+params.Encode(), "")
		if response.Code != 400 {
			t.Fatal("invalid destination became an all-members query", input, response.Code)
		}
	}
	for _, bad := range []struct {
		token, cursor string
		code          int
	}{{"", cursor, 400}, {unit, "invalid", 400}, {strings.Repeat("f", 64), cursor, 409}, {unit, strings.Repeat("f", 64) + ":" + strings.Repeat("f", 64), 409}} {
		if _, code, err := read(2, bad.token, bad.cursor); err == nil || code != bad.code {
			t.Fatal("invalid continuation accepted", bad, code, err)
		}
	}
	file, err = os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	header[79] ^= 1
	_, err = file.WriteAt(header, 160)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, code, err := read(2, unit, ""); err == nil || code != 409 {
		t.Fatal("disconnected selected header exposed old members", code, err)
	}
	if err = os.Remove(filepath.Join(a.dataDir, "indexes", "utxo-state", "head.json")); err != nil {
		t.Fatal(err)
	}
	if _, code, err := read(1, oldUnit, ""); err == nil || code != 409 {
		t.Fatal("lost parent exposed committed child", code, err)
	}
	if _, code, err := a.readProjectionRecipeDestination(ctx, resolved, 1, 1, "", "", paymentAddress); err == nil || code != 409 {
		t.Fatal("lost parent became a destination zero", code, err)
	}
}

func TestRecipeProjectionGraphScopesAndSelectedOrder(t *testing.T) {
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	view := saveOrderedRecipeForRun(t, a, "graph-coins", false)
	parent, err := a.ResolveSavedRecipe(ctx, view.Recipe.ID, view.Revision, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	g := projectionTestGeneration(t, a.dataDir, "graph-addresses", false)
	builtin := projectionTestSave(t, a, g.program.definition)
	graph, err := a.buildRecipeDependencyGraph(ctx, builtin, nil)
	if err != nil || len(graph.Nodes) != 2 || len(graph.Edges) != 1 || graph.Edges[0].Scope != "builtin" || graph.Edges[0].ParentSelected {
		t.Fatal("builtin graph inferred saved coverage", graph, err)
	}
	d := g.program.definition
	d.ID, d.Name = "saved-graph-addresses", "Saved graph addresses"
	d.Dependencies[0] = recipeDependency{Name: "coins", Scope: "saved", Instance: parent.ID, Revision: parent.Revision, SemanticHash: parent.SemanticHash, Export: "coins", Type: "coin", Coverage: "snapshot_delta"}
	child := projectionTestSave(t, a, d)
	order, err := recipeOrderSelectedDAG(ctx, []resolvedSavedRecipe{child, parent}, a.ResolveSavedRecipe)
	if err != nil || len(order) != 2 || order[0].Instance != parent.ID || order[1].Instance != child.ID {
		t.Fatal("saved checkpoint parent was not ordered before child", order, err)
	}
	if _, err = os.Stat(g.profileRoot); !os.IsNotExist(err) {
		t.Fatal("definition graph created source state", err)
	}
}
