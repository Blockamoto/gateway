package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func recipeConsumerFixture(t *testing.T, origin, out int64) (*app, recipeAdmissionPlan, *recipeSparseConsumer) {
	t.Helper()
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	h := indexInterfaceHandler(a)
	source := strings.Replace(registryRecipe, `"origin":0`, fmt.Sprint(`"origin":`, origin), 1)
	registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(source, "")))
	from := int64(0)
	req := indexRunRequest{In: &from, Out: &out, Consumers: []indexRunSelection{{Index: "custom-one", Armed: true}}}
	plan, err := a.PlanSavedRecipeAdmission(ctx, "custom-one", req, out, recipeTestHash("b"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.openRecipeSparseConsumer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	return a, plan, c
}
func recipeConsumerGenesis(t *testing.T) (blockView, *inscriptionBlockResult) {
	t.Helper()
	header, err := hex.DecodeString(genesisHeaderHex)
	if err != nil {
		t.Fatal(err)
	}
	block, err := parseBlockDetailed(0, genesisHashDisplay, header, testGenesisBlockPayload(t), "fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	extraction, err := extractInscriptionOccurrences(block)
	if err != nil {
		t.Fatal(err)
	}
	return block, &extraction
}
func recipeConsumerAllow(_ recipeAdmissionPlan, _ recipeConsumerWork, _ blockView, publish func() error) error {
	return publish()
}
func recipeConsumerSelected(height int64) (string, error) {
	if height == 0 {
		return genesisHashDisplay, nil
	}
	return recipeTestHash("b"), nil
}
func TestRecipeConsumerSparseHolesResumeAndEmpty(t *testing.T) {
	ctx := context.Background()
	a, plan, c := recipeConsumerFixture(t, 0, 3)
	for _, height := range []int64{1, 3} {
		if err := c.store.Publish(ctx, recipeStoreUnit{Schema: 1, Fingerprint: plan.SemanticHash, Height: height, BlockHash: recipeTestHash("b"), Rows: []recipeStoreRow{}}, recipeTestGuard); err != nil {
			t.Fatal(err)
		}
	}
	p, err := c.Next(ctx, "chain-1", recipeConsumerSelected)
	if err != nil || p.Work == nil || p.Work.Height != 0 {
		t.Fatalf("first hole: %+v %v", p, err)
	}
	block, inventory := recipeConsumerGenesis(t)
	if err = c.Apply(ctx, *p.Work, block, inventory, recipeConsumerAllow); err != nil {
		t.Fatal(err)
	}
	stored, found, err := c.store.Read(ctx, 0)
	if err != nil || !found || stored.Rows == nil || len(stored.Rows) != 0 {
		t.Fatalf("empty committed outcome: %+v %v %v", stored, found, err)
	}
	calls := []int64{}
	selected := func(h int64) (string, error) { calls = append(calls, h); return recipeConsumerSelected(h) }
	p, err = c.Next(ctx, "chain-1", selected)
	if err != nil || p.Work == nil || p.Work.Height != 2 {
		t.Fatalf("second hole: %+v %v", p, err)
	}
	if len(calls) != 1 || calls[0] != 1 {
		t.Fatalf("unchanged prefix rechecked unexpectedly: %v", calls)
	}
	resumed, err := a.openRecipeSparseConsumer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	p, err = resumed.Next(ctx, "chain-1", recipeConsumerSelected)
	if err != nil || p.Work == nil || p.Work.Height != 2 {
		t.Fatalf("resume skipped hole: %+v %v", p, err)
	}
	if err = c.store.Publish(ctx, recipeStoreUnit{Schema: 1, Fingerprint: plan.SemanticHash, Height: 2, BlockHash: recipeTestHash("b"), Rows: []recipeStoreRow{}}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	p, err = resumed.Next(ctx, "chain-1", recipeConsumerSelected)
	if err != nil || p.State != "range_complete" || p.Work != nil {
		t.Fatalf("complete snapshot: %+v %v", p, err)
	}
}
func TestRecipeConsumerRejectsStaleWorkAndDeniedGuard(t *testing.T) {
	ctx := context.Background()
	_, plan, c := recipeConsumerFixture(t, 0, 3)
	block, inventory := recipeConsumerGenesis(t)
	p, err := c.Next(ctx, "chain-1", recipeConsumerSelected)
	if err != nil {
		t.Fatal(err)
	}
	forged := *p.Work
	forged.Height = 1
	if err = c.Apply(ctx, forged, block, inventory, recipeConsumerAllow); err == nil {
		t.Fatal("accepted modified work")
	}
	p.Work.Height = 1
	if err = c.Apply(ctx, *p.Work, block, inventory, recipeConsumerAllow); err == nil {
		t.Fatal("returned work aliases the pending admission")
	}
	p, err = c.Next(ctx, "chain-1", recipeConsumerSelected)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Apply(ctx, *p.Work, block, nil, recipeConsumerAllow); err == nil {
		t.Fatal("accepted incomplete extraction")
	}
	p, err = c.Next(ctx, "chain-1", recipeConsumerSelected)
	if err != nil {
		t.Fatal(err)
	}
	denial := errors.New("admission revoked")
	if err = c.Apply(ctx, *p.Work, block, inventory, func(recipeAdmissionPlan, recipeConsumerWork, blockView, func() error) error { return denial }); !errors.Is(err, denial) {
		t.Fatal(err)
	}
	if _, found, err := c.store.Read(ctx, 0); err != nil || found {
		t.Fatalf("denied result visible: %v %v", found, err)
	}
	p, err = c.Next(ctx, "chain-1", recipeConsumerSelected)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.store.Publish(ctx, recipeStoreUnit{Schema: 1, Fingerprint: plan.SemanticHash, Height: 2, BlockHash: recipeTestHash("b"), Rows: []recipeStoreRow{}}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if err = c.Apply(ctx, *p.Work, block, inventory, recipeConsumerAllow); err == nil {
		t.Fatal("stale coverage work published")
	}
	if _, found, err := c.store.Read(ctx, 0); err != nil || found {
		t.Fatalf("stale result visible: %v %v", found, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = c.Next(cancelled, "chain-1", recipeConsumerSelected); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestRecipeConsumerOriginAndReorgEvidence(t *testing.T) {
	ctx := context.Background()
	_, plan, c := recipeConsumerFixture(t, 2, 3)
	for _, height := range []int64{2, 3} {
		if err := c.store.Publish(ctx, recipeStoreUnit{Schema: 1, Fingerprint: plan.SemanticHash, Height: height, BlockHash: recipeTestHash("b"), Rows: []recipeStoreRow{}}, recipeTestGuard); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Next(ctx, "chain-1", func(int64) (string, error) { return "", errors.New("unavailable") }); err == nil {
		t.Fatal("missing chain evidence accepted")
	}
	p, err := c.Next(ctx, "chain-1", recipeConsumerSelected)
	if err != nil || p.State != "range_complete" {
		t.Fatalf("range complete: %+v %v", p, err)
	}
	if _, err = c.Next(ctx, "chain-2", func(int64) (string, error) { return recipeTestHash("c"), nil }); !errors.Is(err, errRecipeConsumerReorg) {
		t.Fatalf("reorg not detected: %v", err)
	}
	review, err := c.ReorgPlan(ctx, nil, 0, 8, func(h int64) (string, error) {
		if h == 2 {
			return recipeTestHash("c"), nil
		}
		return recipeTestHash("b"), nil
	})
	if err != nil || len(review.Anchors) != 1 || review.Anchors[0].Height != 2 {
		t.Fatalf("reorg review: %+v %v", review, err)
	}
	if _, found, err := c.store.Read(ctx, 2); err != nil || !found {
		t.Fatalf("review changed coverage: %v %v", found, err)
	}
	_, _, noWork := recipeConsumerFixture(t, 10, 3)
	empty, err := noWork.Next(ctx, "chain-1", recipeConsumerSelected)
	if err != nil || empty.State != "no_work" || empty.Work != nil || empty.Evidence != "none_no_work" {
		t.Fatalf("pre-origin work: %+v %v", empty, err)
	}
}
