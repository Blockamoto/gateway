package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func recipeLocalConsumerFixture(t *testing.T) (*app, *recipeLocalConsumer, recipeRunBinding, blockView, *inscriptionBlockResult) {
	t.Helper()
	ctx := context.Background()
	a, _, binding, _ := recipeGuardFixture(t)
	header, err := a.readSelectedHeader(1)
	if err != nil {
		t.Fatal(err)
	}
	hash := hash256(header)
	resolved, err := a.ResolveSavedRecipe(ctx, binding.Index, binding.Revision, binding.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	a.indexRunMu.Lock()
	req, err := recipeAdmissionCloneRequest(a.indexRunState.Run.Request)
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := recipeAdmissionBuild(resolved, req, 1, reverseHex(hash[:]))
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := a.openRecipeLocalConsumer(ctx, plan, binding)
	if err != nil {
		t.Fatal(err)
	}
	block, result := recipeConsumerGenesis(t)
	return a, consumer, binding, block, result
}

func TestRecipeLocalConsumerNextAndApply(t *testing.T) {
	_, consumer, _, block, result := recipeLocalConsumerFixture(t)
	p, err := consumer.Next(context.Background())
	if err != nil || p.Work == nil || p.Work.Height != 0 || p.Evidence != "selected_local_headers_as_of_snapshot" {
		t.Fatalf("local next: %+v %v", p, err)
	}
	if err = consumer.Apply(context.Background(), *p.Work, block, result); err != nil {
		t.Fatal(err)
	}
	u, ok, err := consumer.consumer.store.Read(context.Background(), 0)
	if err != nil || !ok || u.BlockHash != genesisHashDisplay || u.Rows == nil {
		t.Fatalf("receipt: %+v %v %v", u, ok, err)
	}
	p, err = consumer.Next(context.Background())
	if err != nil || p.Work == nil || p.Work.Height != 1 {
		t.Fatalf("next block: %+v %v", p, err)
	}
}

func TestRecipeLocalConsumerRejectsChangedSnapshotAndAdmission(t *testing.T) {
	a, consumer, _, block, result := recipeLocalConsumerFixture(t)
	p, err := consumer.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Replace the selected target header after planning. This is a synthetic
	// mutation for race coverage, not a valid alternative Bitcoin chain.
	header, err := a.readSelectedHeader(0)
	if err != nil {
		t.Fatal(err)
	}
	a.headerChainMu.Lock()
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err == nil {
		_, err = f.WriteAt(header, 80)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = consumer.Apply(context.Background(), *p.Work, block, result); err == nil {
		t.Fatal("changed snapshot accepted")
	}
	recipeGuardUncovered(t, consumer.consumer.store, 0)
	if _, err = consumer.Next(context.Background()); err == nil {
		t.Fatal("changed target accepted by Next")
	}

	a, consumer, _, block, result = recipeLocalConsumerFixture(t)
	p, err = consumer.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.indexRunMu.Lock()
	a.indexRunState.Run.State = "pausing"
	a.indexRunMu.Unlock()
	if err = consumer.Apply(context.Background(), *p.Work, block, result); err == nil {
		t.Fatal("stopped admission accepted")
	}
	recipeGuardUncovered(t, consumer.consumer.store, 0)
}

func TestRecipeLocalConsumerChecksWorkAndPlan(t *testing.T) {
	a, consumer, binding, block, _ := recipeLocalConsumerFixture(t)
	p, err := consumer.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	called := false
	guard := a.recipeLocalConsumerGuard(context.Background(), binding)
	bad := *p.Work
	bad.ChainSnapshot = "caller-asserted"
	if err = guard(consumer.consumer.plan, bad, block, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("untrusted snapshot accepted")
	}
	bad = *p.Work
	bad.PlanID = recipeTestHash("f")
	if err = guard(consumer.consumer.plan, bad, block, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("unbound plan accepted")
	}
	plan := consumer.consumer.plan
	plan.TargetHeight = 0
	if err = guard(plan, *p.Work, block, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("modified plan accepted")
	}
	plan = consumer.consumer.plan
	plan.Schema = 1
	plan.PlanID = recipeAdmissionDigest(plan)
	old := *p.Work
	old.PlanID = plan.PlanID
	if err = guard(plan, old, block, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("unsupported admission schema accepted")
	}
}

func TestRecipeLocalConsumerAdmissionIsPerTrack(t *testing.T) {
	a, consumer, _, block, result := recipeLocalConsumerFixture(t)
	work, err := consumer.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.indexRunMu.Lock()
	a.indexRunState.Run.AdmissionRevision++
	a.indexRunState.Run.Request.Consumers = append(a.indexRunState.Run.Request.Consumers, indexRunSelection{Index: "tx-locator", Armed: true})
	a.indexRunMu.Unlock()
	if err = consumer.Apply(context.Background(), *work.Work, block, result); err != nil {
		t.Fatalf("unrelated amendment revoked track: %v", err)
	}

	a, consumer, _, block, result = recipeLocalConsumerFixture(t)
	work, err = consumer.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.indexRunMu.Lock()
	// Simulate an Off -> On cycle that returns to the same visible selection.
	a.indexRunState.Run.AdmissionRevision += 2
	a.indexRunState.Run.Consumers[0].ConfigurationRevision++
	a.indexRunMu.Unlock()
	if err = consumer.Apply(context.Background(), *work.Work, block, result); err == nil {
		t.Fatal("re-admitted track reused stale staged work")
	}
	recipeGuardUncovered(t, consumer.consumer.store, 0)
}

func TestRecipeLocalConsumerDoesNotAdoptUntrustedCoverageCache(t *testing.T) {
	a, local, binding, _, _ := recipeLocalConsumerFixture(t)
	ctx := context.Background()
	plan := local.consumer.plan
	wrongHash := recipeTestHash("c")
	unit := recipeStoreUnit{Schema: 1, Fingerprint: binding.Fingerprint, Height: 0, BlockHash: wrongHash, Rows: []recipeStoreRow{}}
	if err := local.consumer.store.Publish(ctx, unit, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	other, err := a.openRecipeSparseConsumer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := a.recipeLocalObserve(ctx, plan, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := other.Next(ctx, token, func(int64) (string, error) { return wrongHash, nil })
	if err != nil || claimed.Work == nil || claimed.Work.Height != 1 {
		t.Fatalf("untrusted setup: %+v %v", claimed, err)
	}
	checked, err := local.Next(ctx)
	if err != nil || checked.Work == nil || checked.Work.Height != 0 {
		t.Fatalf("untrusted cache was adopted: %+v %v", checked, err)
	}
	recipeGuardUncovered(t, local.consumer.store, 0)
}

func TestRecipeLocalNextDetectsSnapshotChangeWithEmptyCoverage(t *testing.T) {
	a, local, _, _, _ := recipeLocalConsumerFixture(t)
	observed, resume := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := a.recipeLocalNextObserved(context.Background(), local.consumer, func() { close(observed); <-resume })
		done <- err
	}()
	select {
	case <-observed:
	case <-time.After(3 * time.Second):
		t.Fatal("initial observation blocked")
	}
	header, err := a.readSelectedHeader(0)
	if err != nil {
		t.Fatal(err)
	}
	a.headerChainMu.Lock()
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if err == nil {
		_, err = f.WriteAt(header, 80)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	close(resume)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("snapshot changed between checks")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Next remained blocked")
	}
	recipeGuardUncovered(t, local.consumer.store, 0)
}

func TestRecipeLocalNextDoesNotHoldHeaderWhileWaitingForConsumer(t *testing.T) {
	a, consumer, _, _, _ := recipeLocalConsumerFixture(t)
	// While Next is waiting for the consumer, a selected-header writer must
	// still be able to enter. Holding header while waiting for consumer would
	// invert Apply's consumer -> header order.
	consumer.consumer.mu.Lock()
	done := make(chan error, 1)
	observed := make(chan struct{})
	go func() {
		_, err := a.recipeLocalNextObserved(context.Background(), consumer.consumer, func() { close(observed) })
		done <- err
	}()
	select {
	case <-observed:
	case <-time.After(3 * time.Second):
		consumer.consumer.mu.Unlock()
		t.Fatal("initial observation blocked")
	}
	writer := make(chan struct{})
	go func() { a.headerChainMu.Lock(); a.headerChainMu.Unlock(); close(writer) }()
	select {
	case <-writer:
	case <-time.After(3 * time.Second):
		consumer.consumer.mu.Unlock()
		t.Fatal("Next held header while waiting for consumer")
	}
	consumer.consumer.mu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Next did not finish")
	}
}

func TestRecipeLiveRebindReusesOnlyUnchangedPrefix(t *testing.T) {
	ctx := context.Background()
	a, _, req := savedRecipeRunFixture(t)
	req.Out = nil
	req.Consumers[0].Live = true
	a.setStatus(func(st *appStatus) { st.HeaderHeight = 0; st.HeaderCount = 1; st.TipHash = genesisHashDisplay })
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	installRecipeCoordinatorFixture(t, a, plan)
	snap := a.indexRunSnapshot()
	binding, err := recipeBindRun(snap.Run, "custom-one", plan.Consumers[0].RecipeSemanticHash, plan.Consumers[0].RecipeRevision)
	if err != nil {
		t.Fatal(err)
	}
	local, err := a.openRecipeLocalConsumer(ctx, plan.Consumers[0].Recipe.expand(plan.Request), binding)
	if err != nil {
		t.Fatal(err)
	}
	block, inventory := recipeConsumerGenesis(t)
	first, err := local.Next(ctx)
	if err != nil || first.Work == nil {
		t.Fatalf("next: %+v %v", first, err)
	}
	if err = local.Apply(ctx, *first.Work, block, inventory); err != nil {
		t.Fatal(err)
	}
	complete, err := local.Next(ctx)
	if err != nil || complete.State != "range_complete" {
		t.Fatalf("complete: %+v %v", complete, err)
	}
	header, err := a.readSelectedHeader(1)
	if err != nil {
		t.Fatal(err)
	}
	digest := hash256(header)
	nextHash := reverseHex(digest[:])
	a.indexRunMu.Lock()
	a.indexRunState.Run.TargetHeight = 1
	a.indexRunState.Run.TargetHash = nextHash
	a.indexRunMu.Unlock()
	if err = local.Rebind(ctx, 1, nextHash); err != nil {
		t.Fatal(err)
	}
	local.consumer.mu.Lock()
	cursor, observed, has := local.consumer.cursor, local.consumer.observedChain, local.consumer.haveObservation
	local.consumer.mu.Unlock()
	if cursor != 1 || !has || observed != recipeLocalSnapshotToken(1, nextHash) {
		t.Fatalf("unchanged prefix discarded: %d %t %s", cursor, has, observed)
	}
	next, err := local.Next(ctx)
	if err != nil || next.Work == nil || next.Work.Height != 1 {
		t.Fatalf("extended next: %+v %v", next, err)
	}
	// Corrupt the old prefix while leaving the current endpoint untouched. Force
	// a new target identity so rebind cannot retain the previous cached prefix.
	a.headerChainMu.Lock()
	f, e := os.OpenFile(a.headersPath, os.O_WRONLY, 0)
	if e == nil {
		_, e = f.WriteAt(header, 0)
		_ = f.Close()
	}
	a.headerChainMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	a.indexRunMu.Lock()
	a.indexRunState.Run.TargetHeight = 0
	a.indexRunState.Run.TargetHash = nextHash
	a.indexRunMu.Unlock()
	if err = local.Rebind(ctx, 0, nextHash); err != nil {
		t.Fatal(err)
	}
	blockOne := recipeCoordinatorSource(t)[1]
	if err = local.Apply(ctx, *next.Work, blockOne, inventory); err == nil {
		t.Fatal("stale work survived target rebind")
	}
	recipeGuardUncovered(t, local.consumer.store, 1)
	if _, err = a.recipeLocalNext(ctx, local.consumer); !errors.Is(err, errRecipeConsumerReorg) {
		t.Fatalf("changed prefix not detected: %v", err)
	}
	recovered, err := local.Next(ctx)
	if err != nil || recovered.Work == nil || recovered.Work.Height != 0 {
		t.Fatalf("changed prefix did not reconcile: %+v %v", recovered, err)
	}
}
