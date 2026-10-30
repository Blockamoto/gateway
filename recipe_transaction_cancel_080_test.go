package main

import (
	"context"
	"testing"
	"time"
)

func TestRecipeTransactionOffFencesInFlight(t *testing.T) {
	ctx := context.Background()
	a, source := transactionRunFixture(t)
	v := saveTransactionRecipeForRun(t, a, "tx-off", false)
	entered, release := make(chan struct{}), make(chan struct{})
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, _ string) (blockView, error) {
		close(entered)
		select {
		case <-release:
			return source[target.Height], nil
		case <-ctx.Done():
			return blockView{}, ctx.Err()
		}
	}
	z := int64(0)
	req := indexRunRequest{In: &z, Out: &z, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true}}}
	plan, e := a.planIndexRun(ctx, req)
	if e != nil || !plan.Ready {
		t.Fatalf("plan: %+v %v", plan, e)
	}
	started, e := a.startIndexRun(ctx, req, plan.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(15 * time.Second):
		t.Fatal("source not entered")
	}
	off := plan.Request.Consumers[0]
	off.Armed = false
	if _, e = a.amendIndexRun(ctx, indexRunAmendment{RunID: started.Run.ID, AdmissionRevision: started.Run.AdmissionRevision, Consumers: []indexRunSelection{off}}); e != nil {
		t.Fatal(e)
	}
	close(release)
	waitRun(t, a)
	store, e := openRecipeStore(a.dataDir, v.Recipe.ID, v.SemanticHash)
	if e != nil {
		t.Fatal(e)
	}
	if _, found, e := store.Read(ctx, 0); e != nil || found {
		t.Fatalf("Off published pending source: %v %v", found, e)
	}
}
