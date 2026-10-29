package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Simulate an existing, valid genesis-era index head from before the new
// mainnet write floor. Planning must preserve it without attempting to append
// the unavailable gap leading to the inscription origin.
func TestInscriptionLegacyCheckpointBeforeOriginRemainsReadable079(t *testing.T) {
	a := runTestApp(t)
	raw := testGenesisBlockPayload(t)
	block, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "genesis fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	block.Verification.HeaderChainMatch = true
	batch, err := singletonBrowseBatch("inscriptions", block, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(a.dataDir, "indexes", "inscriptions")
	if err = os.MkdirAll(filepath.Join(dir, "commits"), 0700); err != nil {
		t.Fatal(err)
	}
	commit := filepath.Join(dir, "commits", batch.Checkpoint.Commitment+".json")
	head := filepath.Join(dir, "head.json")
	if err = atomicWriteJSON(commit, batch); err != nil {
		t.Fatal(err)
	}
	if err = atomicWriteJSON(head, indexHead{Mode: "lean", Commitment: batch.Checkpoint.Commitment, Retention: "ephemeral"}); err != nil {
		t.Fatal(err)
	}
	beforeHead, err := os.ReadFile(head)
	if err != nil {
		t.Fatal(err)
	}
	beforeCommit, err := os.ReadFile(commit)
	if err != nil {
		t.Fatal(err)
	}
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: "inscriptions", Armed: true}}}
	plan, err := a.planIndexRun(context.Background(), req)
	if err != nil || len(plan.Consumers) != 1 {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	p := plan.Consumers[0]
	if p.From != firstMainnetInscriptionHeight || p.State != "needs_prerequisite" || !strings.Contains(p.Reason, "cannot be extended across the gap") {
		t.Fatalf("unsafe old checkpoint plan: %+v", p)
	}
	store, err := indexStoreHead(a.dataDir, "inscriptions")
	if err != nil || store.checkpoint == nil || store.checkpoint.Commitment != batch.Checkpoint.Commitment {
		t.Fatalf("old checkpoint unavailable: %+v %v", store, err)
	}
	if _, err = store.readBatch(batch.Checkpoint.Commitment); err != nil {
		t.Fatalf("old record unreadable: %v", err)
	}
	afterHead, err := os.ReadFile(head)
	if err != nil || string(afterHead) != string(beforeHead) {
		t.Fatal("planning changed the old head", err)
	}
	afterCommit, err := os.ReadFile(commit)
	if err != nil || string(afterCommit) != string(beforeCommit) {
		t.Fatal("planning changed the old record", err)
	}
}
