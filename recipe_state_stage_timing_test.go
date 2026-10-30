package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// Deliberately opt-in: this writes thousands of durable immutable pages. The
// same sealed stage is verified twice so only the verification budget changes.
func TestRecipeStageStressPhaseTimings(t *testing.T) {
	if os.Getenv("GATEWAY_STAGING_TIMING") != "1" {
		t.Skip("set GATEWAY_STAGING_TIMING=1 for 4,100-key phase timings")
	}
	ctx := context.Background()
	s := stageTestOpen(t)
	const total = 4100
	st, err := s.begin(ctx, 0, s.identity.Genesis, "", recipeTestHash("a"), total)
	if err != nil {
		t.Fatal(err)
	}
	startTime := time.Now()
	for start := 0; start < total; {
		end := start + recipeStageChunkOps
		if end > total {
			end = total
		}
		ops := make([]recipeStageOperation, 0, end-start)
		for i := start; i < end; i++ {
			ops = append(ops, recipeStageOperation{recipeStateTestKey("coins", fmt.Sprintf("%08d", i)), recipeStateTestValue("v")})
		}
		st, err = s.append(ctx, st.ID, st.Sequence, uint64(start), uint64(end), ops)
		if err != nil {
			t.Fatal(err)
		}
		start = end
	}
	appendTime := time.Since(startTime)
	sealed, err := s.seal(ctx, st.ID, st.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	var first recipeStageCheckpoint
	for _, budget := range []int{1, 32} {
		if budget != 1 {
			// Restore the same sealed, unverified checkpoint. Immutable pages and
			// chunks are untouched; this reset is test-only and private.
			if err := s.saveCheckpoint(sealed); err != nil {
				t.Fatal(err)
			}
		}
		calls := 0
		startTime = time.Now()
		for {
			calls++
			done, e := s.verify(ctx, st.ID, budget)
			if e != nil {
				t.Fatal(e)
			}
			if done {
				break
			}
		}
		duration := time.Since(startTime)
		verified, e := s.checkpoint(st.ID)
		if e != nil || !verified.Verified {
			t.Fatal("verification checkpoint", e)
		}
		if budget == 1 {
			first = verified
		} else if first != verified {
			t.Fatal("verification budgets produced different authenticated checkpoints")
		}
		t.Logf("keys=%d chunks=%d append=%s verify_budget=%d verify_calls=%d verify=%s", total, sealed.Sequence, appendTime, budget, calls, duration)
	}
	if _, err = s.publish(ctx, st.ID, stageTestGuard); err != nil {
		t.Fatal(err)
	}
}
