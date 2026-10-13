package main

import (
	"context"
	"testing"
)

// A sparse scan may already contain the selected tip while earlier blocks in
// the explicitly enabled Live range remain absent. Tip height is therefore
// neither a completeness test nor a safe next-build position.
func Test076AddressHistoryLiveFillsEarlierGapWhenSparseHeadAlreadyAtTip(t *testing.T) {
	ctx := context.Background()
	a := prepareLiveTestApp063(t)
	raw, hash := installBlockOneHeader063(t, a)
	peer, requests := hashCoordinateBitcoinSource(t, raw)
	hashCoordinateConnect(t, a, peer)
	a.setStatus(func(st *appStatus) {
		st.HeaderState, st.Syncing, st.Error = "current", false, ""
	})
	block, err := parseBlockDetailed(1, hash, raw[:80], raw, "real mainnet fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	history, err := openAddressHistoryStoreContext(ctx, a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err = history.CommitBlockContext(ctx, block, a.addressSelectedHashes(ctx, a.currentChainAuthority())); err != nil {
		t.Fatal(err)
	}
	if history.head.Height != 1 || len(history.head.Coverage) != 1 || history.head.Coverage[0] != (heightInterval{1, 1}) {
		t.Fatalf("sparse history fixture did not reach the selected tip: %+v", history.head)
	}
	priorCommitment := history.head.Commitment
	zero := int64(0)
	if err = a.writeIndexLivePolicy(indexLivePolicy{Index: "address-history", InitialFrom: &zero, Mode: "lean", Retention: "ephemeral"}); err != nil {
		t.Fatal(err)
	}
	before, err := a.setIndexLivePolicy(indexLiveRequest{Index: "address-history", Action: "enable"})
	if err != nil || before.State != "catching_up" || !before.LagKnown || before.Lag != 1 || before.CheckpointHeight != before.TipHeight || before.CheckpointHeight != 1 {
		t.Fatalf("sparse tip incorrectly reported caught up: %+v %v", before, err)
	}
	a.liveIndexTick()
	job := waitLiveComplete064(t, a)
	if job.Index != "address-history" || job.From != 0 || job.To != 0 || job.Height != 0 || job.BlocksChecked != 1 {
		t.Fatalf("Live did not schedule only the earliest missing block: %+v", job)
	}
	afterHistory, err := openAddressHistoryStoreContext(ctx, a.dataDir)
	if err != nil || afterHistory.head.Height != 1 || afterHistory.head.Commitment == priorCommitment || len(afterHistory.head.Coverage) != 1 || afterHistory.head.Coverage[0] != (heightInterval{0, 1}) {
		t.Fatalf("Live failed to fill the earlier gap while retaining the existing tip: %+v %v", afterHistory, err)
	}
	policies, err := a.indexLivePolicies()
	if err != nil {
		t.Fatal(err)
	}
	after := a.indexLiveView("address-history", policies["address-history"])
	if after.State != "synced" || !after.LagKnown || after.Lag != 0 || after.CheckpointHeight != 1 {
		t.Fatalf("complete selected Live range did not become caught up: %+v", after)
	}
	a.liveIndexTick()
	if a.indexJobSnapshot().ID != job.ID {
		t.Fatal("Live scheduled redundant work after the gap was filled")
	}
	if requests.Load() != 0 {
		t.Fatal("filling a locally available earlier gap unnecessarily fetched the already indexed tip")
	}
}
