package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func stageTestGuard(_ recipeStageManifest, f func() error) error { return f() }
func stageTestOpen(t *testing.T) *recipeStageStore {
	t.Helper()
	s, e := recipeStageOpen(t.TempDir(), durableTestID())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.create(); e != nil {
		t.Fatal(e)
	}
	return s
}
func stageTestAppend(t *testing.T, s *recipeStageStore, st recipeStageCheckpoint, start, end uint64, ops []recipeStageOperation) recipeStageCheckpoint {
	t.Helper()
	v, e := s.append(context.Background(), st.ID, st.Sequence, start, end, ops)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func stageTestComplete(t *testing.T, s *recipeStageStore, st recipeStageCheckpoint) recipeStageHead {
	t.Helper()
	ctx := context.Background()
	st, e := s.seal(ctx, st.ID, st.Sequence)
	if e != nil {
		t.Fatal(e)
	}
	for {
		done, err := s.verify(ctx, st.ID, 1)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	h, e := s.publish(ctx, st.ID, stageTestGuard)
	if e != nil {
		t.Fatal(e)
	}
	return h
}
func TestRecipeStagingChunksFirstImagesAndUndo(t *testing.T) {
	ctx := context.Background()
	s := stageTestOpen(t)
	identity := durableTestID()
	key := recipeStateTestKey("coins", "same")
	temp := recipeStateTestKey("coins", "transient")
	old := recipeStateTestValue("old")
	initial, e := s.begin(ctx, 0, identity.Genesis, "", recipeTestHash("a"), 1)
	if e != nil {
		t.Fatal(e)
	}
	initial = stageTestAppend(t, s, initial, 0, 1, []recipeStageOperation{{key, old}})
	head0 := stageTestComplete(t, s, initial)
	hash := recipeTestHash("c")
	st, e := s.begin(ctx, 1, hash, identity.Genesis, recipeTestHash("d"), 300)
	if e != nil {
		t.Fatal(e)
	}
	st = stageTestAppend(t, s, st, 0, 1, []recipeStageOperation{{key, recipeStateTestValue("first")}, {temp, recipeStateTestValue("born")}})
	staged, err := s.readStaged(ctx, st.ID, st.Sequence, key)
	if err != nil || string(staged.Bytes) != "first" {
		t.Fatal("private read-your-writes", staged, err)
	}
	if _, err = s.readStaged(ctx, st.ID, st.Sequence-1, key); !errors.Is(err, errRecipeStateConflict) {
		t.Fatal("stale stage revision read", err)
	}
	for n := 1; n < 300; {
		end := n + 80
		if end > 300 {
			end = 300
		}
		ops := make([]recipeStageOperation, 0, end-n)
		for i := n; i < end; i++ {
			ops = append(ops, recipeStageOperation{recipeStateTestKey("coins", fmt.Sprintf("%04d", i)), recipeStateTestValue("v")})
		}
		st = stageTestAppend(t, s, st, uint64(n), uint64(end), ops)
		n = end
	}
	st = stageTestAppend(t, s, st, 300, 300, []recipeStageOperation{{key, recipeStateTestValue("new")}, {temp, recipeStateValue{}}})
	before, e := s.read(ctx, key)
	if e != nil || string(before.Bytes) != "old" {
		t.Fatal("private stage exposed", before, e)
	}
	st, e = s.seal(ctx, st.ID, st.Sequence)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.publish(ctx, st.ID, stageTestGuard); e == nil {
		t.Fatal("unverified stage published")
	}
	// Verify one immutable chunk per call, reopening the stage between calls.
	checks := 0
	for {
		reopened := *s
		s = &reopened
		done, e := s.verify(ctx, st.ID, 1)
		if e != nil {
			t.Fatal(e)
		}
		checks++
		if done {
			break
		}
	}
	if checks != int(st.Sequence) {
		t.Fatal("verification did not traverse every chunk", checks, st.Sequence)
	}
	h, e := s.publish(ctx, st.ID, func(manifest recipeStageManifest, f func() error) error {
		if manifest.SourceCommitment != recipeTestHash("d") || manifest.SourceTotal != 300 || manifest.Touched != 301 || manifest.Net != 300 {
			return fmt.Errorf("manifest mismatch: %+v", manifest)
		}
		return f()
	})
	if e != nil {
		t.Fatal(e)
	}
	got, e := s.read(ctx, key)
	if e != nil || string(got.Bytes) != "new" {
		t.Fatal(got, e)
	}
	got, e = s.read(ctx, temp)
	if e != nil || got.Present {
		t.Fatal("transient persisted", got, e)
	}
	manifest, e := s.manifest(h.Manifest)
	if e != nil {
		t.Fatal(e)
	}
	entry, found, e := recipeStageJournalGet(s.tree(ctx, manifest.Journal), key)
	if e != nil || !found || string(entry.Before.Bytes) != "old" || string(entry.After.Bytes) != "new" {
		t.Fatal("lost first before image", entry, e)
	}
	entry, found, e = recipeStageJournalGet(s.tree(ctx, manifest.Journal), temp)
	if e != nil || !found || entry.Before.Present || entry.After.Present {
		t.Fatal("transient before image lost", entry, e)
	}
	reverted, e := s.undo(ctx, h, recipeTestGuard)
	if e != nil || reverted.Root != head0.Root || reverted.Revision != h.Revision+1 {
		t.Fatal(reverted, e)
	}
	got, e = s.read(ctx, key)
	if e != nil || string(got.Bytes) != "old" {
		t.Fatal("undo", got, e)
	}
}
func TestRecipeStagingCrashAndConflicts(t *testing.T) {
	ctx := context.Background()
	s := stageTestOpen(t)
	id := durableTestID()
	st, e := s.begin(ctx, 0, id.Genesis, "", recipeTestHash("a"), 1)
	if e != nil {
		t.Fatal(e)
	}
	op := []recipeStageOperation{{recipeStateTestKey("a", "b"), recipeStateTestValue("value")}}
	for _, stage := range []string{"pages", "chunk"} {
		s.hook = func(actual string) error {
			if actual == stage {
				return errors.New("interrupted")
			}
			return nil
		}
		if _, e = s.append(ctx, st.ID, st.Sequence, 0, 1, op); e == nil {
			t.Fatal("fault accepted", stage)
		}
		current, e := s.checkpoint(st.ID)
		if e != nil || current.Sequence != 0 || current.Cursor != 0 {
			t.Fatal(current, e)
		}
		head, e := s.status()
		if e != nil || head.Height != -1 {
			t.Fatal(head, e)
		}
	}
	s.hook = nil
	st = stageTestAppend(t, s, st, 0, 1, op)
	st, e = s.seal(ctx, st.ID, st.Sequence)
	if e != nil {
		t.Fatal(e)
	}
	done, e := s.verify(ctx, st.ID, 1)
	if e != nil || !done {
		t.Fatal(done, e)
	}
	s.hook = func(stage string) error {
		if stage == "manifest" {
			return errors.New("interrupted")
		}
		return nil
	}
	if _, e = s.publish(ctx, st.ID, stageTestGuard); e == nil {
		t.Fatal("fault accepted")
	}
	h, e := s.status()
	if e != nil || h.Height != -1 {
		t.Fatal(h, e)
	}
	s.hook = nil
	denied := errors.New("denied")
	if _, e = s.publish(ctx, st.ID, func(recipeStageManifest, func() error) error { return denied }); !errors.Is(e, denied) {
		t.Fatal(e)
	}
	cancel, cancelFn := context.WithCancel(ctx)
	cancelFn()
	if _, e = s.publish(cancel, st.ID, stageTestGuard); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	other, e := s.begin(ctx, 0, id.Genesis, "", recipeTestHash("a"), 1)
	if e != nil {
		t.Fatal(e)
	}
	other = stageTestAppend(t, s, other, 0, 1, op)
	other, e = s.seal(ctx, other.ID, other.Sequence)
	if e != nil {
		t.Fatal(e)
	}
	if done, e = s.verify(ctx, other.ID, 1); e != nil || !done {
		t.Fatal(done, e)
	}
	if _, e = s.publish(ctx, st.ID, stageTestGuard); e != nil {
		t.Fatal(e)
	}
	if _, e = s.publish(ctx, other.ID, stageTestGuard); !errors.Is(e, errRecipeStateConflict) {
		t.Fatal("stale staged head accepted", e)
	}
}
func TestRecipeStagingMissingChunkAndIncompleteSource(t *testing.T) {
	ctx := context.Background()
	s := stageTestOpen(t)
	id := durableTestID()
	st, e := s.begin(ctx, 0, id.Genesis, "", recipeTestHash("a"), 2)
	if e != nil {
		t.Fatal(e)
	}
	st = stageTestAppend(t, s, st, 0, 1, []recipeStageOperation{{recipeStateTestKey("a", "b"), recipeStateTestValue("x")}})
	if _, e = s.seal(ctx, st.ID, st.Sequence); e == nil {
		t.Fatal("incomplete source sealed")
	}
	st = stageTestAppend(t, s, st, 1, 2, nil)
	st, e = s.seal(ctx, st.ID, st.Sequence)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(s.dir, "chunks", st.LastChunk+".json")); e != nil {
		t.Fatal(e)
	}
	if _, e = s.verify(ctx, st.ID, 1); e == nil {
		t.Fatal("missing chunk accepted")
	}
	if _, e = s.publish(ctx, st.ID, stageTestGuard); e == nil {
		t.Fatal("unverified stage published")
	}
	h, e := s.status()
	if e != nil || h.Height != -1 {
		t.Fatal(h, e)
	}
}

func TestRecipeStagingRejectsOversizedChunkBeforeMutation(t *testing.T) {
	ctx := context.Background()
	s := stageTestOpen(t)
	id := durableTestID()
	st, err := s.begin(ctx, 0, id.Genesis, "", recipeTestHash("a"), 1)
	if err != nil {
		t.Fatal(err)
	}
	ops := []recipeStageOperation{{recipeStateTestKey("a", "b"), recipeStateValue{Present: true, Bytes: make([]byte, recipeStageChunkBytes)}}}
	if _, err = s.append(ctx, st.ID, st.Sequence, 0, 1, ops); !errors.Is(err, errRecipeStateBudget) {
		t.Fatal("oversized chunk accepted", err)
	}
	got, err := s.checkpoint(st.ID)
	if err != nil || got.Sequence != 0 || got.Cursor != 0 {
		t.Fatal("oversized chunk advanced stage", got, err)
	}
}

func TestRecipeStagingBeyondSingleBatchLimit(t *testing.T) {
	if os.Getenv("GATEWAY_STAGING_STRESS") != "1" {
		t.Skip("set GATEWAY_STAGING_STRESS=1 for the durable 4,100-key stress test")
	}
	ctx := context.Background()
	s := stageTestOpen(t)
	id := durableTestID()
	const total = 4100
	st, err := s.begin(ctx, 0, id.Genesis, "", recipeTestHash("a"), total)
	if err != nil {
		t.Fatal(err)
	}
	for start := 0; start < total; {
		end := start + recipeStageChunkOps
		if end > total {
			end = total
		}
		ops := make([]recipeStageOperation, 0, end-start)
		for i := start; i < end; i++ {
			ops = append(ops, recipeStageOperation{recipeStateTestKey("coins", fmt.Sprintf("%08d", i)), recipeStateTestValue("v")})
		}
		st = stageTestAppend(t, s, st, uint64(start), uint64(end), ops)
		start = end
	}
	head := stageTestComplete(t, s, st)
	manifest, err := s.manifest(head.Manifest)
	if err != nil || manifest.Touched != total || manifest.Net != total || manifest.Chunks <= 1 {
		t.Fatalf("incomplete staged manifest: %+v %v", manifest, err)
	}
	for _, i := range []int{0, 4095, 4099} {
		v, e := s.read(ctx, recipeStateTestKey("coins", fmt.Sprintf("%08d", i)))
		if e != nil || !v.Present || string(v.Bytes) != "v" {
			t.Fatal(i, v, e)
		}
	}
}
