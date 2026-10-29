package main

import (
	"context"
	"testing"
)

func TestCurrentAddressRequiresArmedLiveUTXO079(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	for _, utxo := range []indexRunSelection{{Index: "utxo-state"}, {Index: "utxo-state", Armed: true}, {Index: "utxo-state", Live: true}} {
		req := indexRunRequest{Consumers: []indexRunSelection{utxo, {Index: "address-state", Armed: true}}}
		if _, err := normalizeIndexRunRequest(req); err == nil {
			t.Fatalf("accepted incomplete UTXO selection: %+v", utxo)
		}
		doc, err := a.prepareIndexRun(req)
		if err != nil {
			t.Fatal(err)
		}
		current, _ := runSelection(doc.Prepared, "address-state")
		if current.Armed || current.Live {
			t.Fatalf("Current remained staged: %+v", current)
		}
		if indexRunOptionalWorkAllowed(a.dataDir, "address-state") {
			t.Fatal("incidental Current work allowed")
		}
		if err = checkIndexRunPublication(a.indexRunConsumerContext(ctx, "address-state")); err == nil {
			t.Fatal("Current publication allowed")
		}
	}
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: "utxo-state", Armed: true, Live: true}, {Index: "address-state", Armed: true}}}
	doc, err := a.prepareIndexRun(req)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := runSelection(doc.Prepared, "address-state")
	if !current.Armed {
		t.Fatal("valid Current was disarmed")
	}
	if !indexRunOptionalWorkAllowed(a.dataDir, "address-state") {
		t.Fatal("valid Current was not eligible")
	}
	req.Consumers[0].Live = false
	doc, err = a.prepareIndexRun(req)
	if err != nil {
		t.Fatal(err)
	}
	current, _ = runSelection(doc.Prepared, "address-state")
	if current.Armed {
		t.Fatal("Current stayed armed after UTXO Live was disabled")
	}
	historical := indexRunRequest{Consumers: []indexRunSelection{{Index: "address-history", Armed: true}}}
	if _, err = normalizeIndexRunRequest(historical); err != nil {
		t.Fatalf("Historical unexpectedly requires UTXO: %v", err)
	}
}
