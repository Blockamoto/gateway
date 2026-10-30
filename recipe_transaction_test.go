package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func locatorRecipeProgram(t *testing.T) *recipeProgram {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("recipes", "tx-id-locator.json"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := parseRecipe(data)
	if err != nil {
		t.Fatal(err)
	}
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func checkLocatorRows(t *testing.T, p *recipeProgram, b blockView, want *transactionLocatorPayload) {
	t.Helper()
	rows, err := p.evaluateTransactionBlock(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	got := rows["transactions"]
	if len(got) != len(want.Block.Transactions) {
		t.Fatalf("count %d, want %d", len(got), len(want.Block.Transactions))
	}
	for _, entry := range want.Block.Transactions {
		row, ok := got[entry.TxID]
		if !ok || row["ref"] != txCoordinate(int(entry.TxIndex), b.Height) || row["txid"] != entry.TxID || row["block_hash"] != b.Hash || row["height"] != json.Number(fmt.Sprint(b.Height)) || row["tx_index"] != json.Number(fmt.Sprint(entry.TxIndex)) {
			t.Fatalf("position %d: %#v", entry.TxIndex, row)
		}
	}
}
func TestRecipeTransactionLocatorDifferential(t *testing.T) {
	p := locatorRecipeProgram(t)
	_, genesis := testAppWithGenesis(t)
	b0, err := parseBlockDetailed(0, genesisHashDisplay, genesis[:80], genesis, "fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	one := mainnetBlockOne(t)
	h := hash256(one[:80])
	b1, err := parseBlockDetailed(1, reverseHex(h[:]), one[:80], one, "fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	blocks := map[string]blockView{"genesis": b0, "block1": b1}
	if path := os.Getenv("GATEWAY_RECIPE_REFERENCE_BLOCK"); path != "" {
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		b, e := parseBlockDetailed(908657, "00000000000000000001d253a31be91698f02d4851078e3cd5b4e29048219d26", nil, raw, "fixture", false)
		if e != nil {
			t.Fatal(e)
		}
		blocks["reference"] = b
	}
	for name, b := range blocks {
		t.Run(name, func(t *testing.T) { checkLocatorRows(t, p, b, transactionLocatorFromBlock(b)) })
	}
	var oracle map[string]*transactionLocatorPayload
	path := os.Getenv("GATEWAY_LOCATOR_ORACLE")
	if path == "" {
		path = filepath.Join("testdata", "locator-079-oracle.json")
	}
	{
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		if e = json.Unmarshal(raw, &oracle); e != nil {
			t.Fatal(e)
		}
		for name, b := range blocks {
			if oracle[name] == nil {
				if name == "reference" && os.Getenv("GATEWAY_LOCATOR_ORACLE") == "" {
					continue
				}
				t.Fatalf("missing 0.7.9 oracle %s", name)
			}
			checkLocatorRows(t, p, b, oracle[name])
		}
	}
	// The same transaction ID can have distinct block-scoped coordinates. These
	// deliberately relabeled fixture heights exercise representation only and
	// make no claim that the genesis block occurred at those chain heights.
	other, e := parseBlockDetailed(1, genesisHashDisplay, nil, genesis, "synthetic-height fixture", false)
	if e != nil {
		t.Fatal(e)
	}
	left, e := p.evaluateTransactionBlock(context.Background(), b0)
	if e != nil {
		t.Fatal(e)
	}
	right, e := p.evaluateTransactionBlock(context.Background(), other)
	if e != nil {
		t.Fatal(e)
	}
	txid := b0.Transactions[0].TxID
	if left["transactions"][txid]["ref"] == right["transactions"][txid]["ref"] {
		t.Fatal("block-scoped transaction occurrences collapsed")
	}
	// Invented decoded-only blocks remain untrusted even with public verification flags.
	if _, err := p.evaluateTransactionBlock(context.Background(), locatorBatchTestBlock(10, "", 3)); err == nil {
		t.Fatal("accepted fabricated source")
	}
	// Synthetic positional projections use the same generic event evaluator, but
	// do not assert that the fabricated source passed block verification.
	synthetic := locatorBatchTestBlock(10, "", 3)
	syntheticWant := transactionLocatorFromBlock(synthetic)
	if oracle != nil {
		if oracle["synthetic"] == nil {
			t.Fatal("missing synthetic 0.7.9 oracle")
		}
		syntheticWant = oracle["synthetic"]
	}
	for _, entry := range syntheticWant.Block.Transactions {
		event := map[string]any{"txid": entry.TxID, "ref": txCoordinate(int(entry.TxIndex), 10), "height": json.Number("10"), "tx_index": json.Number(fmt.Sprint(entry.TxIndex)), "block_hash": synthetic.Hash}
		rows, e := p.evaluate(event)
		if e != nil || rows["transactions"][entry.TxID]["ref"] != event["ref"] {
			t.Fatalf("synthetic event %d: %v", entry.TxIndex, e)
		}
	}
}
func TestRecipeTransactionSourceSafetyAndGating(t *testing.T) {
	p := locatorRecipeProgram(t)
	_, raw := testAppWithGenesis(t)
	b, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	tampered := b
	tampered.Transactions = append([]transactionView(nil), b.Transactions...)
	tampered.Transactions[0].TxID = strings.Repeat("a", 64)
	rows, err := p.evaluateTransactionBlock(context.Background(), tampered)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rows["transactions"][b.Transactions[0].TxID]; !ok {
		t.Fatal("trusted mutated view instead of verified raw bytes")
	}
	bad := b
	bad.verifiedRaw = append([]byte(nil), b.verifiedRaw...)
	bad.verifiedRaw[len(bad.verifiedRaw)-1] ^= 1
	if _, err = p.evaluateTransactionBlock(context.Background(), bad); err == nil {
		t.Fatal("accepted altered bytes")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = p.evaluateTransactionBlock(ctx, b); err == nil {
		t.Fatal("ignored cancellation")
	}
	source, err := os.ReadFile(filepath.Join("recipes", "tx-id-locator.json"))
	if err != nil {
		t.Fatal(err)
	}
	view, _, err := recipeRegistryCompile(source)
	if err != nil || !view.Validated || view.Runnable {
		t.Fatalf("source capability: %+v %v", view, err)
	}
	resolved, err := recipeResolveView(view)
	if err != nil || resolved.Execution != "source_scheduling_unavailable" {
		t.Fatalf("resolver: %+v %v", resolved, err)
	}
	if _, err = recipeAdmissionBuild(resolved, indexRunRequest{}, 0, genesisHashDisplay); err == nil {
		t.Fatal("transaction source admitted to reveal scheduler")
	}
	if _, err = p.evaluateInscriptionBlock(context.Background(), b, nil); err == nil {
		t.Fatal("transaction program accepted by reveal adapter")
	}
	// Filtered transactions still yield a complete, explicitly empty evaluated map.
	d := p.definition
	d.Steps = append([]recipeStep{{Op: "require", Test: &recipeExpr{Call: "eq", Args: []recipeExpr{{Field: []string{"txid"}}, {Literal: json.RawMessage(`"never"`)}}}}}, d.Steps...)
	filtered, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := filtered.evaluateTransactionBlock(context.Background(), b)
	if err != nil || empty["transactions"] == nil || len(empty["transactions"]) != 0 {
		t.Fatalf("empty: %#v %v", empty, err)
	}
}
