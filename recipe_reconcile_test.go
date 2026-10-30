package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func recipeReconcileFixture(t *testing.T, count int) (*app, *recipeLocalConsumer, *recipeStore, recipeRunBinding, recipeAdmissionPlan) {
	t.Helper()
	ctx := context.Background()
	a, store, _, _ := recipeGuardFixture(t)
	header, err := a.readSelectedHeader(1)
	if err != nil {
		t.Fatal(err)
	}
	a.headerChainMu.Lock()
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY|os.O_TRUNC, 0)
	if err == nil {
		for i := 0; i < count; i++ {
			copyHeader := append([]byte(nil), header...)
			binary.LittleEndian.PutUint32(copyHeader[76:], uint32(i))
			if _, err = f.Write(copyHeader); err != nil {
				break
			}
		}
		if e := f.Close(); err == nil {
			err = e
		}
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	target := int64(count - 1)
	targetHeader, err := a.readSelectedHeader(target)
	if err != nil {
		t.Fatal(err)
	}
	hash := hash256(targetHeader)
	targetHash := reverseHex(hash[:])
	a.indexRunMu.Lock()
	run := a.indexRunState.Run
	run.Request.Out = &target
	run.InitialTargetHeight = target
	run.TargetHeight = target
	run.TargetHash = targetHash
	a.indexRunMu.Unlock()
	resolved, err := a.ResolveSavedRecipe(ctx, "custom-one", "", "")
	if err != nil {
		t.Fatal(err)
	}
	a.indexRunMu.Lock()
	binding, err := recipeBindRun(a.indexRunState.Run, "custom-one", resolved.SemanticHash, resolved.Revision)
	req := a.indexRunState.Run.Request
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := recipeAdmissionBuild(resolved, req, target, targetHash)
	if err != nil {
		t.Fatal(err)
	}
	local, err := a.openRecipeLocalConsumer(ctx, plan, binding)
	if err != nil {
		t.Fatal(err)
	}
	recipeStoreMutex.Lock()
	head, err := store.head()
	if err == nil {
		head, err = store.initialize(head)
	}
	if err == nil {
		tree := store.tree(ctx, head)
		for i := 0; i < count; i++ {
			unit := recipeStoreUnit{Schema: 1, Fingerprint: store.fingerprint, Height: int64(i), BlockHash: recipeTestHash("a"), Rows: []recipeStoreRow{}}
			var data []byte
			data, err = json.Marshal(unit)
			if err != nil {
				break
			}
			digest := recipeStoreHash(data)
			if err = recipeStorePutImmutable(filepath.Join(store.dir, "units"), digest, data); err != nil {
				break
			}
			if err = tree.put(recipeStoreHeightKey(int64(i)), recipeStoreRef{BlockHash: unit.BlockHash, Unit: digest}); err != nil {
				break
			}
		}
		if err == nil {
			var root string
			root, err = tree.flush()
			if err == nil {
				err = store.publishHead(head, root)
			}
		}
	}
	recipeStoreMutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return a, local, store, binding, plan
}
func TestRecipeReconcileDeepPagesAndRetainsOldUnits(t *testing.T) {
	ctx := context.Background()
	_, local, store, _, _ := recipeReconcileFixture(t, recipeCoverageMaxPage+7)
	old := recipeStoreUnit{Schema: 1, Fingerprint: store.fingerprint, Height: 0, BlockHash: recipeTestHash("a"), Rows: []recipeStoreRow{}}
	data, _ := json.Marshal(old)
	next, err := local.Next(ctx)
	if err != nil || next.Work == nil || next.Work.Height != 0 {
		t.Fatalf("reconciled next: %+v %v", next, err)
	}
	for _, h := range []int64{0, recipeCoverageMaxPage + 6} {
		if _, found, e := store.Read(ctx, h); e != nil || found {
			t.Fatalf("stale anchor %d survived: %v %v", h, found, e)
		}
	}
	if _, err = os.Stat(filepath.Join(store.dir, "units", recipeStoreHash(data)+".json")); err != nil {
		t.Fatalf("old immutable unit lost: %v", err)
	}
	if _, pending, err := store.inspectReconcileFence(); err != nil || pending {
		t.Fatalf("fence not cleared: %v %v", pending, err)
	}
}
func TestRecipeReconcileFenceRecoversAfterPauseAndPartialBatch(t *testing.T) {
	ctx := context.Background()
	a, local, store, _, _ := recipeReconcileFixture(t, 3)
	page, err := store.Coverage(ctx, 0, 2, nil, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.beginReconcileFence(ctx, page.Revision, 0, 2, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Read(ctx, 0); !errors.Is(err, errRecipeReconcileFenced) {
		t.Fatalf("fenced read exposed: %v", err)
	}
	resolved, err := a.ResolveSavedRecipe(ctx, "custom-one", "", "")
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/index/recipes/output?id=custom-one&revision=%s&semantic_hash=%s&height=0", resolved.Revision, resolved.SemanticHash)
	response := indexInterfaceCall(indexInterfaceHandler(a), "GET", path, "")
	if response.Code != 409 {
		t.Fatalf("fenced HTTP output exposed: %d %s", response.Code, response.Body.String())
	}
	if _, err = store.Coverage(ctx, 0, 2, nil, 0, 3); !errors.Is(err, errRecipeReconcileFenced) {
		t.Fatalf("fenced coverage exposed: %v", err)
	}
	if err = store.Publish(ctx, recipeStoreUnit{Schema: 1, Fingerprint: store.fingerprint, Height: 5, BlockHash: recipeTestHash("b"), Rows: []recipeStoreRow{}}, recipeTestGuard); !errors.Is(err, errRecipeReconcileFenced) {
		t.Fatalf("fenced write accepted: %v", err)
	}
	if err = store.reconcile(ctx, page.Revision, map[int64]string{0: recipeTestHash("a")}, recipeTestGuard, true); err != nil {
		t.Fatal(err)
	}
	reopened, err := openRecipeStore(a.dataDir, "custom-one", store.fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = reopened.Read(ctx, 0); !errors.Is(err, errRecipeReconcileFenced) {
		t.Fatalf("restart exposed partial state: %v", err)
	}
	a.indexRunMu.Lock()
	a.indexRunState.Run.State = "pausing"
	a.indexRunMu.Unlock()
	if _, err = local.Next(ctx); err == nil {
		t.Fatal("paused run reconciled")
	}
	if _, _, err = reopened.Read(ctx, 1); !errors.Is(err, errRecipeReconcileFenced) {
		t.Fatalf("pause removed fence: %v", err)
	}
	a.indexRunMu.Lock()
	a.indexRunState.Run.State = "running"
	a.indexRunMu.Unlock()
	next, err := local.Next(ctx)
	if err != nil || next.Work == nil || next.Work.Height != 0 {
		t.Fatalf("resume failed: %+v %v", next, err)
	}
	if _, pending, err := store.inspectReconcileFence(); err != nil || pending {
		t.Fatalf("fence remains: %v %v", pending, err)
	}
}
func TestRecipeReconcileMissingHeadersDoesNotMutate(t *testing.T) {
	ctx := context.Background()
	a, local, store, _, _ := recipeReconcileFixture(t, 3)
	before, err := store.Coverage(ctx, 0, 2, nil, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(a.headersPath, a.headersPath+".offline"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(a.headersPath+".offline", a.headersPath)
	if _, err = local.Next(ctx); err == nil {
		t.Fatal("missing evidence accepted")
	}
	after, err := store.Coverage(ctx, 0, 2, nil, 0, 3)
	if err != nil || after.Revision != before.Revision || len(after.Anchors) != 3 {
		t.Fatalf("missing evidence changed coverage: %+v %v", after, err)
	}
	if _, pending, err := store.inspectReconcileFence(); err != nil || pending {
		t.Fatalf("missing evidence created fence: %v %v", pending, err)
	}
}

func TestRecipeReconcileMarkedBoundaryFailsClosed(t *testing.T) {
	ctx := context.Background()
	a, _, store, _, plan := recipeReconcileFixture(t, 3)
	a.indexRunMu.Lock()
	run := a.indexRunState.Run
	run.Request.OutHash = plan.TargetHash
	req := run.Request
	a.indexRunMu.Unlock()
	resolved, err := a.ResolveSavedRecipe(ctx, "custom-one", "", "")
	if err != nil {
		t.Fatal(err)
	}
	a.indexRunMu.Lock()
	binding, err := recipeBindRun(a.indexRunState.Run, "custom-one", resolved.SemanticHash, resolved.Revision)
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	marked, err := recipeAdmissionBuild(resolved, req, plan.TargetHeight, plan.TargetHash)
	if err != nil {
		t.Fatal(err)
	}
	local, err := a.openRecipeLocalConsumer(ctx, marked, binding)
	if err != nil {
		t.Fatal(err)
	}
	header, err := a.readSelectedHeader(2)
	if err != nil {
		t.Fatal(err)
	}
	header[79] ^= 1
	digest := hash256(header)
	changed := reverseHex(digest[:])
	a.headerChainMu.Lock()
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err == nil {
		_, err = f.WriteAt(header, 160)
		_ = f.Close()
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = local.Rebind(ctx, 2, changed); err == nil {
		t.Fatal("marked Out silently rebound")
	}
	coverage, err := store.Coverage(ctx, 0, 2, nil, 0, 3)
	if err != nil || len(coverage.Anchors) != 3 {
		t.Fatalf("marked reorg changed coverage: %+v %v", coverage, err)
	}
	if _, pending, err := store.inspectReconcileFence(); err != nil || pending {
		t.Fatalf("marked reorg started fence: %v %v", pending, err)
	}
}
func TestRecipeReconcileStaleAuditAndCorruptFenceFailClosed(t *testing.T) {
	ctx := context.Background()
	_, _, store, _, _ := recipeReconcileFixture(t, 3)
	before, err := store.Coverage(ctx, 0, 2, nil, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	unit := recipeStoreUnit{Schema: 1, Fingerprint: store.fingerprint, Height: 3, BlockHash: recipeTestHash("b"), Rows: []recipeStoreRow{}}
	if err = store.Publish(ctx, unit, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if err = store.beginReconcileFence(ctx, before.Revision, 0, 2, recipeTestGuard); err == nil {
		t.Fatal("stale audit started fence")
	}
	if _, pending, err := store.inspectReconcileFence(); err != nil || pending {
		t.Fatalf("stale audit left fence: %v %v", pending, err)
	}
	if err = os.WriteFile(store.reconcileFencePath(), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Read(ctx, 0); err == nil {
		t.Fatal("corrupt fence allowed read")
	}
	if _, err = store.Coverage(ctx, 0, 2, nil, 0, 3); err == nil {
		t.Fatal("corrupt fence allowed coverage")
	}
	if err = store.Publish(ctx, unit, recipeTestGuard); err == nil {
		t.Fatal("corrupt fence allowed write")
	}
}

func TestRecipeReconcileWidenedFenceSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	a, _, store, _, _ := recipeReconcileFixture(t, 3)
	before, err := store.Coverage(ctx, 0, 2, nil, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.beginReconcileFence(ctx, before.Revision, 1, 1, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if _, err = store.coverage(ctx, 0, 2, nil, before.Revision, 3, true); err == nil {
		t.Fatal("unfenced range read")
	}
	if err = store.reconcile(ctx, before.Revision, map[int64]string{0: recipeTestHash("a")}, recipeTestGuard, true); err == nil {
		t.Fatal("unfenced range mutated")
	}
	if err = store.beginReconcileFence(ctx, before.Revision, 0, 2, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	if err = store.reconcile(ctx, before.Revision, map[int64]string{0: recipeTestHash("a")}, recipeTestGuard, true); err != nil {
		t.Fatal(err)
	}
	reopened, err := openRecipeStore(a.dataDir, "custom-one", store.fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	fence, pending, err := reopened.inspectReconcileFence()
	if err != nil || !pending || fence.From != 0 || fence.To != 2 {
		t.Fatalf("widened fence lost: %+v %v %v", fence, pending, err)
	}
	if err = reopened.finishReconcileFence(ctx, before.Revision+1, 1, 1, recipeTestGuard); err == nil {
		t.Fatal("narrow recovery cleared widened fence")
	}
	if err = reopened.beginReconcileFence(ctx, before.Revision+1, 1, 1, recipeTestGuard); err == nil {
		t.Fatal("narrow recovery adopted widened fence")
	}
	if _, _, err = reopened.Read(ctx, 0); !errors.Is(err, errRecipeReconcileFenced) {
		t.Fatalf("partial widened coverage exposed: %v", err)
	}
}

func TestRecipeFiniteTargetRebindReconcilesUnmarkedReorg(t *testing.T) {
	ctx := context.Background()
	a, local, binding, _, _ := recipeLocalConsumerFixture(t)
	oldPlan := local.consumer.plan
	original := oldPlan.TargetHash
	stale := recipeStoreUnit{Schema: 1, Fingerprint: binding.Fingerprint, Height: 1, BlockHash: original, Rows: []recipeStoreRow{}}
	if err := local.consumer.store.Publish(ctx, stale, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	header, err := a.readSelectedHeader(1)
	if err != nil {
		t.Fatal(err)
	}
	header[79] ^= 1
	digest := hash256(header)
	replacement := reverseHex(digest[:])
	a.headerChainMu.Lock()
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err == nil {
		_, err = f.WriteAt(header, 80)
		_ = f.Close()
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = local.Rebind(ctx, 1, replacement); err != nil {
		t.Fatal(err)
	}
	next, err := local.Next(ctx)
	if err != nil || next.Work == nil || next.Work.Height != 0 {
		t.Fatalf("finite reorg: %+v %v", next, err)
	}
	if _, found, err := local.consumer.store.Read(ctx, 1); err != nil || found {
		t.Fatalf("stale finite anchor survived: %v %v", found, err)
	}
	if local.consumer.plan.Revision != oldPlan.Revision || local.consumer.plan.SemanticHash != oldPlan.SemanticHash || local.consumer.plan.TargetHeight != oldPlan.TargetHeight {
		t.Fatal("finite reorg changed admitted recipe identity or range")
	}
}

func TestRecipeReconcileRejectsTargetChangeBetweenPages(t *testing.T) {
	ctx := context.Background()
	a, local, store, _, plan := recipeReconcileFixture(t, recipeCoverageMaxPage+1)
	first, err := local.reconcilePage(ctx, plan, nil, 0, false)
	if err != nil || first.Next == nil {
		t.Fatalf("first page: %+v %v", first, err)
	}
	header, err := a.readSelectedHeader(plan.TargetHeight)
	if err != nil {
		t.Fatal(err)
	}
	header[79] ^= 1
	a.headerChainMu.Lock()
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err == nil {
		_, err = f.WriteAt(header, plan.TargetHeight*80)
		_ = f.Close()
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = local.reconcilePage(ctx, plan, first.Next, first.Revision, false); err == nil {
		t.Fatal("changed snapshot accepted on later page")
	}
	all, err := store.Coverage(ctx, 0, plan.TargetHeight, nil, first.Revision, 1)
	if err != nil || all.Revision != first.Revision {
		t.Fatalf("changed snapshot mutated coverage: %+v %v", all, err)
	}
	if _, pending, err := store.inspectReconcileFence(); err != nil || pending {
		t.Fatalf("changed snapshot created fence: %v %v", pending, err)
	}
}
func TestRecipeReconcileCannotClearFenceAfterAuthorityChange(t *testing.T) {
	ctx := context.Background()
	a, _, store, binding, plan := recipeReconcileFixture(t, 3)
	page, err := store.Coverage(ctx, 0, 2, nil, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	token := recipeLocalSnapshotToken(plan.TargetHeight, plan.TargetHash)
	guard := func(commit func() error) error {
		return a.recipeLocalConsumerGuard(ctx, binding)(plan, recipeConsumerWork{PlanID: plan.PlanID, Height: plan.TargetHeight, ChainSnapshot: token}, blockView{Height: plan.TargetHeight, Hash: plan.TargetHash}, commit)
	}
	if err = store.beginReconcileFence(ctx, page.Revision, 0, 2, guard); err != nil {
		t.Fatal(err)
	}
	batch := map[int64]string{}
	for _, anchor := range page.Anchors {
		batch[anchor.Height] = anchor.BlockHash
	}
	if err = store.reconcile(ctx, page.Revision, batch, guard, true); err != nil {
		t.Fatal(err)
	}
	a.indexRunMu.Lock()
	a.indexRunState.Run.State = "pausing"
	a.indexRunMu.Unlock()
	if err = store.finishReconcileFence(ctx, page.Revision+1, 0, 2, guard); err == nil {
		t.Fatal("paused run cleared fence")
	}
	a.indexRunMu.Lock()
	a.indexRunState.Run.State = "running"
	a.indexRunMu.Unlock()
	header, err := a.readSelectedHeader(2)
	if err != nil {
		t.Fatal(err)
	}
	header[79] ^= 1
	a.headerChainMu.Lock()
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err == nil {
		_, err = f.WriteAt(header, 160)
		_ = f.Close()
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = store.finishReconcileFence(ctx, page.Revision+1, 0, 2, guard); err == nil {
		t.Fatal("changed selected target cleared fence")
	}
	if _, _, err = store.Read(ctx, 0); !errors.Is(err, errRecipeReconcileFenced) {
		t.Fatalf("partial coverage exposed: %v", err)
	}
}
