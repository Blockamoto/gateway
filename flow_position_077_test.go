package main

import (
	"context"
	"testing"
)

func Test077FlowPositionsRequireKnownSelectedCreation(t *testing.T) {
	parent := utxoFixtureSpend(77110, nil, 7)
	parent.Index = 1
	spend := utxoFixtureSpend(77111, []inputView{{N: 0, PrevTxID: parent.TxID, PrevVout: 0}}, 6)
	spend.Index = 2
	b := utxoFixtureBlock(1, testID(77100), utxoFixtureCoinbase(77101, 50), parent, spend)
	settings, stop := fakeCoreRPC(t, b.Height, b.Hash)
	defer stop()
	a := independentTestApp(t)
	a.settings = settings
	inputs := a.flowInputValues(context.Background(), b, spend)
	if len(inputs) != 1 || inputs[0].PreviousOutput != "0.1.1" || inputs[0].CreationBlockHash != b.Hash || inputs[0].Value == nil || *inputs[0].Value != 7 {
		t.Fatal(inputs)
	}
	// Knowing raw order without a selected-chain anchor is not enough to claim
	// a navigable creation position in the current chain.
	a.settings.CoreDisabled = true
	inputs = a.flowInputValues(context.Background(), b, spend)
	if inputs[0].PreviousOutput != "" || inputs[0].CreationBlockHash != "" {
		t.Fatal("unanchored parent advertised blocator", inputs)
	}
}
