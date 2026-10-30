package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestRecipeSecondaryLocalAuthorityFences(t *testing.T) {
	ctx := context.Background()
	a, _ := testAppWithGenesis(t)
	v := saveTransactionRecipeForRun(t, a, "secondary-local", false)
	p, e := compileRecipe(v.Recipe)
	if e != nil {
		t.Fatal(e)
	}
	s, e := openRecipeStore(a.dataDir, v.Recipe.ID, v.SemanticHash)
	if e != nil {
		t.Fatal(e)
	}
	key, _ := json.Marshal([]string{"transactions", "txid"})
	value := json.RawMessage(`{"txid":"txid","ref":"0.0"}`)
	unit := recipeStoreUnit{Schema: 1, Fingerprint: v.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Rows: []recipeStoreRow{{Key: string(key), Value: value}}}
	if e = s.Publish(ctx, unit, recipeTestGuard); e != nil {
		t.Fatal(e)
	}
	g := a.recipeSecondaryLocalGuard(ctx)
	if _, e = a.recipeSecondarySelectedHash(ctx, 1<<60, genesisHashDisplay, 0); e == nil {
		t.Fatal("accepted overflowing selected prefix height")
	}
	if e = g(recipeSecondaryReceipt{PrefixHeight: 1 << 60, PrefixHash: genesisHashDisplay}, func() error { return nil }); e == nil {
		t.Fatal("guard accepted overflowing prefix")
	}
	selected := func(h int64) (string, error) { return a.recipeSecondarySelectedHash(ctx, 0, genesisHashDisplay, h) }
	id, e := s.buildSecondary(ctx, p, recipeSecondaryDescriptor{Table: "transactions"}, v.Revision, 0, 0, 0, genesisHashDisplay, selected, g)
	if e != nil {
		t.Fatal(e)
	}
	page, e := s.querySecondary(ctx, id, "txid", "", 10, g)
	if e != nil || len(page.Rows) != 1 {
		t.Fatalf("local query: %+v %v", page, e)
	}
	// The intentionally altered header is a reorg/fence fixture, not a valid chain.
	a.headerChainMu.Lock()
	raw, e := os.ReadFile(a.headersPath)
	if e == nil {
		replacement := append([]byte(nil), raw...)
		replacement[79] ^= 1
		e = os.WriteFile(a.headersPath, replacement, 0600)
	}
	a.headerChainMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if page, err := s.querySecondary(ctx, id, "txid", "", 10, g); err == nil || page.Receipt != "" || len(page.Coverage) != 0 {
		t.Fatalf("query accepted or exposed partial result for changed selected prefix: %+v %v", page, err)
	}
	a.headerChainMu.Lock()
	e = os.WriteFile(a.headersPath, raw, 0600)
	a.headerChainMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.querySecondary(ctx, id, "txid", "", 10, g); e != nil {
		t.Fatalf("restored selected prefix: %v", e)
	}
	// An arbitrary caller callback claiming a wrong anchor cannot override the
	// real selected headers held by the local publication guard.
	if e = s.Reconcile(ctx, map[int64]string{0: genesisHashDisplay}, recipeTestGuard); e != nil {
		t.Fatal(e)
	}
	fake := indexDigest("not-selected")
	unit.BlockHash = fake
	if e = s.Publish(ctx, unit, recipeTestGuard); e != nil {
		t.Fatal(e)
	}
	_, e = s.buildSecondary(ctx, p, recipeSecondaryDescriptor{Table: "transactions"}, v.Revision, 0, 0, 0, genesisHashDisplay, func(int64) (string, error) { return fake, nil }, g)
	if e == nil {
		t.Fatal("accepted a callback claiming off-chain coverage")
	}

}
