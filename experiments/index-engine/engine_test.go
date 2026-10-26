package engine

import (
	"errors"
	"os"
	"strings"
	"testing"
)

type testResolver map[string]LookupResult

func (r testResolver) ResolveTransaction(id string) (LookupResult, bool, error) {
	x, ok := r[id]
	return x, ok, nil
}

type failedResolver struct{}

func (failedResolver) ResolveTransaction(string) (LookupResult, bool, error) {
	return LookupResult{}, false, errors.New("unavailable")
}

func definition(t *testing.T, path string) Definition {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := DecodeDefinition(f)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestIndependentSparseDefinitionsAndUnresolvedLookup(t *testing.T) {
	block := Block{Height: 800000, Hash: "selected-block", Transactions: []Transaction{
		{ID: "coinbase", Inputs: []Input{{Coinbase: true}}},
		{ID: "spend", Inputs: []Input{{PreviousTxID: "known", PreviousOutput: 2}, {PreviousTxID: "missing", PreviousOutput: 3}}},
	}}
	transactions, err := Run(definition(t, "definitions/transactions.json"), block, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(transactions.Rows) != 2 || transactions.Rows[1].Cells[1].Value != "1.800000" {
		t.Fatalf("wrong transaction rows: %+v", transactions)
	}
	spends, err := Run(definition(t, "definitions/spends.json"), block, testResolver{"known": {Value: "4.700000", Evidence: Evidence{BlockHash: "prior-block"}}})
	if err != nil {
		t.Fatal(err)
	}
	if spends.Height != 800000 || spends.BlockHash != "selected-block" || len(spends.Rows) != 2 || spends.Unresolved != 1 {
		t.Fatalf("wrong sparse result: %+v", spends)
	}
	if spends.Rows[0].Cells[0].Value != "i0.1.800000" || spends.Rows[1].Cells[0].Value != "i1.1.800000" {
		t.Fatalf("wrong input blocators: %+v", spends.Rows)
	}
	known := spends.Rows[0].Cells[3]
	if known.State != "known" || known.Value != "4.700000" || known.Evidence.BlockHash != "prior-block" {
		t.Fatalf("wrong known evidence: %+v", known)
	}
	missing := spends.Rows[1].Cells[3]
	if missing.State != "unresolved" || missing.Value != nil || missing.Evidence != nil {
		t.Fatalf("missing evidence became fact: %+v", missing)
	}
	if spends.Rows[1].Cells[1].Value != "missing" || spends.Rows[1].Cells[2].Value != uint64(3) {
		t.Fatal("unresolved outpoint not retained")
	}
}

func TestEmptyBlockAndFailureDoNotBecomeCoverageClaims(t *testing.T) {
	d := definition(t, "definitions/spends.json")
	empty, err := Run(d, Block{Height: 10, Hash: "block-10"}, nil)
	if err != nil || empty.Height != 10 || empty.BlockHash != "block-10" || len(empty.Rows) != 0 {
		t.Fatalf("empty receipt: %+v %v", empty, err)
	}
	block := Block{Height: 10, Hash: "block-10", Transactions: []Transaction{{ID: "t", Inputs: []Input{{PreviousTxID: "a"}}}}}
	if _, err := Run(d, block, failedResolver{}); err == nil {
		t.Fatal("resolver error was accepted")
	}
	if _, err := Run(d, block, testResolver{"a": {Value: "1.2"}}); err == nil {
		t.Fatal("unanchored lookup was accepted")
	}
	block.Hash = ""
	if _, err := Run(d, block, testResolver{}); err == nil {
		t.Fatal("unanchored block was accepted")
	}
}

func TestRejectInvalidDefinitions(t *testing.T) {
	for _, data := range []string{
		`{"version":1,"id":"x","source":"transactions","columns":[{"name":"x","type":"uint","field":"tx.id"}]}`,
		`{"version":1,"id":"x","source":"transactions","columns":[{"name":"x","type":"text","lookup":{"table":"unknown","key":"tx.id"}}]}`,
		`{"version":1,"id":"x","source":"transactions","columns":[{"name":"x","type":"text","lookup":{"table":"tx-position","key":"tx.blocator"}}]}`,
		`{"version":1,"id":"x","source":"inputs","columns":[{"name":"x","type":"text","lookup":{"table":"tx-position","key":"input.blocator"}}]}`,
		`{"version":1,"id":"x","source":"transactions","columns":[{"name":"x","type":"text","lookup":{"table":"tx-position","key":"input.previous_txid"}}]}`,
		`{"version":1,"id":"x","source":"transactions","columns":[{"name":"x","type":"text","field":"tx.id"}],"unexpected":true}`,
		`{"version":1,"id":"x","source":"transactions","columns":[{"name":"x","type":"text","field":"tx.id"}]} {}`,
	} {
		if _, err := DecodeDefinition(strings.NewReader(data)); err == nil {
			t.Fatalf("accepted invalid definition: %s", data)
		}
	}
}
