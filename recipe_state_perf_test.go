package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecipeDurablePinnedReadAndCommitFence(t *testing.T) {
	ctx := context.Background()
	s, id := durableTestStore(t)
	first, err := s.beginPinned(ctx, 0, id.Genesis, "", durableTestGuard, recipeStateTestLimits())
	if err != nil {
		t.Fatal(err)
	}
	competing, err := s.begin(ctx, 0, id.Genesis, "", durableTestGuard, recipeStateTestLimits())
	if err != nil {
		t.Fatal(err)
	}
	key := recipeStateTestKey("t", "k")
	if err = first.Put(ctx, key, []byte("first")); err != nil {
		t.Fatal(err)
	}
	v, err := first.Get(ctx, key)
	if err != nil || string(v.Bytes) != "first" {
		t.Fatal(v, err)
	}
	if _, err = competing.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = first.Get(ctx, recipeStateTestKey("t", "new")); !errors.Is(err, errRecipeStateConflict) {
		t.Fatal("stale head accepted", err)
	}
	if _, err = first.Commit(ctx); !errors.Is(err, errRecipeStateConflict) {
		t.Fatal("stale pinned transaction committed", err)
	}
}
func TestRecipeDurablePinnedCorruptReceiptCannotPublish(t *testing.T) {
	ctx := context.Background()
	s, id := durableTestStore(t)
	tx := durableTestBegin(t, s, 0, id.Genesis, "")
	if _, err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	head, err := s.status()
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := s.beginPinned(ctx, 1, strings.Repeat("c", 64), id.Genesis, durableTestGuard, recipeStateTestLimits())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.dir, "receipts", head.Point.Receipt+".json"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = pinned.Put(ctx, recipeStateTestKey("t", "k"), []byte("value")); err != nil {
		t.Fatal("pinned read should use the previously authenticated head", err)
	}
	before, err := os.ReadFile(filepath.Join(s.dir, "head.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pinned.Commit(ctx); err == nil {
		t.Fatal("corrupted predecessor accepted at publication")
	}
	after, err := os.ReadFile(filepath.Join(s.dir, "head.json"))
	if err != nil || string(after) != string(before) {
		t.Fatal("failed publication changed head", err)
	}
}

func benchmarkDurableLargeReceipt(b *testing.B) (*recipeDurableStore, recipeDurableHead) {
	b.Helper()
	id := durableTestID()
	s, err := openRecipeDurableStore(b.TempDir(), id)
	if err != nil {
		b.Fatal(err)
	}
	if err = s.Create(); err != nil {
		b.Fatal(err)
	}
	head, err := s.status()
	if err != nil {
		b.Fatal(err)
	}
	point := recipeDurablePoint{Checkpoint: recipeDurableCheckpoint(id, head.Point.Checkpoint, 0, id.Genesis), Height: 0, BlockHash: id.Genesis}
	images := make([]recipeStateBefore, 1000)
	for i := range images {
		images[i] = recipeStateBefore{Key: recipeStateTestKey("t", fmt.Sprint(i)), Value: recipeStateValue{Present: true, Bytes: []byte(strings.Repeat("a", 512))}}
	}
	receipt := recipeDurableReceipt{Schema: 1, Identity: id, Previous: head.Point, Current: point, Batch: recipeStateBatch{BeforeImages: images}}
	data, err := json.Marshal(receipt)
	if err != nil {
		b.Fatal(err)
	}
	digest := recipeStoreHash(data)
	if err = recipeStorePutImmutable(filepath.Join(s.dir, "receipts"), digest, data); err != nil {
		b.Fatal(err)
	}
	point.Receipt = digest
	head.Revision = 1
	head.Point = point
	if err = s.saveHead(head); err != nil {
		b.Fatal(err)
	}
	head, err = s.status()
	if err != nil {
		b.Fatal(err)
	}
	return s, head
}
func BenchmarkRecipeDurableReceiptReads(b *testing.B) {
	for _, kind := range []string{"original", "pinned"} {
		b.Run(kind, func(b *testing.B) {
			s, head := benchmarkDurableLargeReceipt(b)
			base := recipeStateVersion{Revision: head.Revision, Checkpoint: head.Point.Checkpoint}
			inner := &recipeDurableForward{store: s, base: base}
			var reader recipeStateBackend = inner
			if kind == "pinned" {
				reader = &recipeDurablePinnedForward{inner: inner, head: head}
			}
			ctx := context.Background()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := reader.ReadState(ctx, base, recipeStateTestKey("t", "missing")); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
func BenchmarkRecipeStageVerificationBudget(b *testing.B) {
	for _, limit := range []int{1, 4} {
		b.Run(fmt.Sprintf("chunks_%d", limit), func(b *testing.B) {
			b.StopTimer()
			for i := 0; i < b.N; i++ {
				s, err := recipeStageOpen(b.TempDir(), durableTestID())
				if err != nil {
					b.Fatal(err)
				}
				if err = s.create(); err != nil {
					b.Fatal(err)
				}
				st, err := s.begin(context.Background(), 0, s.identity.Genesis, "", recipeTestHash("a"), 128)
				if err != nil {
					b.Fatal(err)
				}
				for start := 0; start < 128; start += 32 {
					ops := make([]recipeStageOperation, 32)
					for j := range ops {
						ops[j] = recipeStageOperation{recipeStateTestKey("t", fmt.Sprint(start+j)), recipeStateTestValue("v")}
					}
					st, err = s.append(context.Background(), st.ID, st.Sequence, uint64(start), uint64(start+32), ops)
					if err != nil {
						b.Fatal(err)
					}
				}
				st, err = s.seal(context.Background(), st.ID, st.Sequence)
				if err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				for {
					done, e := s.verify(context.Background(), st.ID, limit)
					if e != nil {
						b.Fatal(e)
					}
					if done {
						break
					}
				}
				b.StopTimer()
			}
		})
	}
}

func TestRecipeStageVerifyCacheEvictsByBytesAndCount(t *testing.T) {
	s := stageTestOpen(t)
	cache := newRecipeStageVerifyCache(s, context.Background())
	for i := 0; i < 80; i++ {
		key := fmt.Sprintf("%064x", i+1)
		page := ordinalPage{Entries: []ordinalKV{{Key: key, Value: make([]byte, 300000)}}}
		cache.owner.cache[key] = page
		cache.owner.cacheEntries[key] = cache.owner.cacheOrder.PushFront(key)
	}
	cache.trim()
	var total int64
	for _, page := range cache.owner.cache {
		total += recipeStagePageBytes(page)
	}
	if len(cache.owner.cache) > recipeStageCacheMaxPages || total > recipeStageCacheMaxBytes {
		t.Fatal("cache exceeded retained limits", len(cache.owner.cache), total)
	}
}
func TestRecipeStageVerificationDetectsCorruptionBetweenCalls(t *testing.T) {
	ctx := context.Background()
	s := stageTestOpen(t)
	id := durableTestID()
	st, err := s.begin(ctx, 0, id.Genesis, "", recipeTestHash("a"), 2)
	if err != nil {
		t.Fatal(err)
	}
	key := recipeStateTestKey("t", "k")
	st = stageTestAppend(t, s, st, 0, 1, []recipeStageOperation{{key, recipeStateTestValue("one")}})
	priorRoot := st.Root
	st = stageTestAppend(t, s, st, 1, 2, []recipeStageOperation{{key, recipeStateTestValue("two")}})
	st, err = s.seal(ctx, st.ID, st.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	done, err := s.verify(ctx, st.ID, 1)
	if err != nil || done {
		t.Fatal(done, err)
	}
	path := s.tree(ctx, priorRoot).pagePath(priorRoot)
	if err = os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.verify(ctx, st.ID, 1); err == nil {
		t.Fatal("cached page masked corruption across verification calls")
	}
	if _, err = s.publish(ctx, st.ID, stageTestGuard); err == nil {
		t.Fatal("corrupted stage published")
	}
}
