package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// Opt-in fixture generation: the point transactions are synthetic; only
// their block anchors match the supplied real selected-header prefix. Never
// direct this at an existing profile or treat it as Bitcoin history.
func TestGenerateSustainedStatusFixture(t *testing.T) {
	dir := os.Getenv("GATEWAY_SYNTHETIC_POINTS_OUT")
	headersPath := os.Getenv("GATEWAY_SYNTHETIC_POINTS_HEADERS")
	if dir == "" || headersPath == "" {
		t.Skip("requires explicit owned fixture path and headers")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal("output must be new:", err)
	}
	rowCount := 2
	if value := os.Getenv("GATEWAY_SYNTHETIC_POINTS_ROWS"); value != "" {
		var err error
		rowCount, err = strconv.Atoi(value)
		if err != nil || rowCount < 1 || rowCount > 2000 {
			t.Fatal("row count must be between 1 and 2000")
		}
	}
	headers, err := os.ReadFile(headersPath)
	if err != nil || len(headers) < 3000*80 {
		t.Fatal("selected header prefix unavailable", err)
	}
	head := indexBrowseHead{Schema: 1, Blocks: map[string]string{}}
	for i := 0; i < 3000; i++ {
		digest := hash256(headers[i*80 : (i+1)*80])
		block := browseBlock073(t, int64(i), reverseHex(digest[:]))
		template := block.Transactions[0]
		block.Transactions = make([]transactionView, rowCount)
		for n := range block.Transactions {
			block.Transactions[n] = template
			block.Transactions[n].Index = n
			block.Transactions[n].TxID = fmt.Sprintf("%064x", int64(i)*int64(rowCount)+int64(n)+1)
		}
		block.TransactionCount = uint64(rowCount)
		batch, err := singletonBrowseBatch("tx-locator", block, nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(batch)
		if err != nil {
			t.Fatal(err)
		}
		timelineWrite(t, filepath.Join(dir, "commits", batch.Checkpoint.Commitment+".json"), raw)
		head.Blocks[strconv.Itoa(i)] = batch.Checkpoint.Commitment
	}
	raw, err := json.Marshal(head)
	if err != nil {
		t.Fatal(err)
	}
	timelineWrite(t, filepath.Join(dir, "head.json"), raw)
}
