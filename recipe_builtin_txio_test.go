package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestRecipeTXIOInputsSemanticBinding(t *testing.T) {
	if _, err := builtinTXIOInputsRecipe(); err != nil {
		t.Fatal(err)
	}
	if err := recipeTXIOInputsCheckEquivalence(builtinTXIOInputsSemantic); err != nil {
		t.Fatal(err)
	}
	if recipeTXIOInputsCheckEquivalence(testID(991232)) == nil {
		t.Fatal("changed input recipe reused native generation")
	}
}

func TestRecipeTXIOInputsNativeRawParity(t *testing.T) {
	block, _ := recipeDependencyReferenceBlock(t)
	actual, err := deriveIndexSpendersContext(context.Background(), block)
	if err != nil {
		t.Fatal(err)
	}
	want, err := deriveIndexSpendersOracle(block)
	if err != nil || !reflect.DeepEqual(actual, want) {
		t.Fatal("native raw spend proposal parity", err)
	}
}

func TestRecipeTXIOInputsCounterEmptyAndCancellation(t *testing.T) {
	ctx := context.Background()
	block := inscriptionTestBlock(transactionView{TxID: testID(192233), Inputs: []inputView{{PrevTxID: testID(192231), PrevVout: 1}, {PrevTxID: testID(192232), PrevVout: 2}}, Outputs: []outputView{{N: 0}}}, transactionView{TxID: testID(192234), Inputs: []inputView{{PrevTxID: testID(192233)}}, Outputs: []outputView{{N: 0}}})
	got, err := deriveIndexSpendersContext(ctx, block)
	if err != nil || len(got.Rows) != 3 || got.Rows[0].Vin != 0 || got.Rows[1].Vin != 1 || got.Rows[2].Vin != 0 {
		t.Fatal("input counter did not reset per transaction", got, err)
	}
	before, err := deriveIndexSpendersOracle(block)
	if err != nil || !reflect.DeepEqual(got, before) {
		t.Fatal("multi-input raw rows changed", err)
	}
	for _, source := range recipeCoordinatorSource(t) {
		got, err = deriveIndexSpendersContext(ctx, source)
		if err != nil || got.Rows == nil || len(got.Rows) != 0 {
			t.Fatal("coinbase-only block is not evaluated empty", got, err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if out, err := deriveIndexSpendersContext(cancelled, block); !errors.Is(err, context.Canceled) || out != nil {
		t.Fatal("cancelled source returned partial input rows", out, err)
	}
	block.Transactions[1].Inputs[0].Coinbase = true
	if _, err := deriveIndexSpendersContext(ctx, block); err == nil {
		t.Fatal("coinbase marker accepted on a spending input")
	}
	m := recipeOrderedMachine{ctx: ctx, vars: map[string]recipeOrderedValue{}}
	for _, tc := range []struct {
		height, tx, vin uint64
		want            string
	}{{0, 0, 0, "i0.0.0"}, {792435, 3138, 7, "i7.3138.792435"}} {
		v, err := m.eval(roCall("input_ref", roLit(rovUint(tc.height)), roLit(rovUint(tc.tx)), roLit(rovUint(tc.vin))), 0)
		if err != nil || v.Kind != "text" || v.Text != tc.want {
			t.Fatal(v, err)
		}
	}
	if _, err := m.eval(roCall("input_ref", roLit(rovUint(^uint64(0))), roLit(rovUint(0)), roLit(rovUint(0))), 0); err == nil {
		t.Fatal("overflowing input height accepted")
	}
}

func TestRecipeTXIOInputsPublicVerifiedReferenceRange(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	block, _ := recipeDependencyReferenceBlock(t)
	raw, _, err := exactVerifiedBlockPayload(block)
	if err != nil {
		t.Fatal(err)
	}
	// This sparse selected-header fixture tests local publication authority,
	// not a replay or consensus validation of the intervening mainnet chain.
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(raw[:80], block.Height*80)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	a.setStatus(func(s *appStatus) {
		s.HeaderHeight = block.Height
		s.HeaderCount = block.Height + 1
		s.TipHash = block.Hash
	})
	block.Verification.HeaderChainMatch = true
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != block.Height {
			t.Error("hidden body read outside selected range", target.Height)
		}
		return block, nil
	}
	p, err := builtinTXIOInputsRecipe()
	if err != nil {
		t.Fatal(err)
	}
	d := p.definition
	d.ID = "my-spending-inputs"
	d.Name = "My spending input outpoints"
	encoded, _ := json.Marshal(d)
	v := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(encoded), "")))
	height := block.Height
	req := indexRunRequest{In: &height, Out: &height, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready || plan.RequiresGenesis {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a); done.Run.State != "complete" || done.Run.Counts.SourceReads != 1 {
		t.Fatal(done.Run)
	}
	out, _, err := a.readRecipeOutput(ctx, v.Recipe.ID, v.Revision, v.SemanticHash, height, 0, 50, "")
	want, sourceErr := deriveIndexSpendersOracle(block)
	if err != nil || sourceErr != nil || out["state"] != "covered" || out["row_count"] != len(want.Rows) {
		t.Fatal(out, err, sourceErr)
	}
	rows := out["rows"].([]recipeStoreRow)
	if len(rows) == 0 {
		t.Fatal("verified spending inputs disappeared")
	}
	var row struct {
		Ref, BlockHash, PrevTxID, TxID string
		PrevVout, TxIndex, InputIndex  uint64
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(rows[0].Value, &fields) != nil {
		t.Fatal("invalid spending input JSON")
	}
	json.Unmarshal(fields["ref"], &row.Ref)
	json.Unmarshal(fields["block_hash"], &row.BlockHash)
	json.Unmarshal(fields["prev_txid"], &row.PrevTxID)
	json.Unmarshal(fields["txid"], &row.TxID)
	json.Unmarshal(fields["prev_vout"], &row.PrevVout)
	json.Unmarshal(fields["tx_index"], &row.TxIndex)
	json.Unmarshal(fields["input_index"], &row.InputIndex)
	if row.BlockHash != block.Hash || row.Ref != inputCoordinate(int(row.InputIndex), int(row.TxIndex), height) || row.PrevTxID != block.Transactions[row.TxIndex].Inputs[row.InputIndex].PrevTxID || row.TxID != block.Transactions[row.TxIndex].TxID {
		t.Fatal("saved source input identity changed", row)
	}
}
