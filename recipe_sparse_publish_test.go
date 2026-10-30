package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func recipeSparsePublishFixture(t *testing.T) (*recipeStore, *recipeSparseSpool, recipeSparseStagedUnit) {
	t.Helper()
	p := recipeSpoolTestProgram(t, "")
	s := recipeTestOpen(t, t.TempDir(), "staged", p.semanticHash())
	spool, err := newRecipeSparseSpool(context.Background(), s.dir, p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = spool.Close() })
	if err := spool.Add(recipeSpoolTestRow(p, "transaction", 0)); err != nil {
		t.Fatal(err)
	}
	staged, err := spool.Seal(recipeSpoolTestHeader(p))
	if err != nil {
		t.Fatal(err)
	}
	return s, spool, staged
}

func TestRecipeSparsePublicationReopenAndUnchangedBytes(t *testing.T) {
	ctx := context.Background()
	s, spool, staged := recipeSparsePublishFixture(t)
	if err := s.publishSparseStaged(ctx, staged, 0, recipeTestGuard, nil); err != nil {
		t.Fatal(err)
	}
	head, err := s.head()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.publishSparseStaged(ctx, staged, head.Revision, recipeTestGuard, nil); err != nil {
		t.Fatal(err)
	}
	again, _ := s.head()
	if !reflect.DeepEqual(head, again) {
		t.Fatal("idempotent publication changed the head")
	}
	if err := spool.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := &recipeStore{dir: s.dir, instance: s.instance, fingerprint: s.fingerprint}
	unit, found, err := reopened.Read(ctx, staged.Header.Height)
	if err != nil || !found || len(unit.Rows) != 1 {
		t.Fatal("reopen lost published rows", found, err)
	}
	if err := recipeSparseVerifyFile(ctx, filepath.Join(s.dir, "units", staged.Digest+".json"), staged.Digest, staged.Bytes); err != nil {
		t.Fatal(err)
	}
}

func TestRecipeSparsePublicationFailuresLeaveHeadUnchanged(t *testing.T) {
	for _, mode := range []string{"denied", "no-callback", "stale", "canceled", "changed-header", "changed-file", "existing-corrupt", "lineage"} {
		t.Run(mode, func(t *testing.T) {
			s, _, staged := recipeSparsePublishFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			guard, revision := recipeStoreGuard(recipeTestGuard), uint64(0)
			switch mode {
			case "denied":
				guard = func(func() error) error { return errors.New("revoked") }
			case "no-callback":
				guard = func(func() error) error { return nil }
			case "stale":
				revision = 7
			case "canceled":
				guard = func(publish func() error) error { cancel(); return publish() }
			case "changed-header":
				staged.Header.Height++
			case "changed-file":
				file, err := os.OpenFile(staged.Path, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = file.WriteAt([]byte("Z"), staged.Bytes/2)
				_ = file.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "existing-corrupt":
				dir := filepath.Join(s.dir, "units")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, staged.Digest+".json"), []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			case "lineage":
				staged.Header.Lineage = []recipeDependencyReceipt{{Name: "parent", Instance: "parent", SemanticHash: recipeTestHash("a"), Height: 0, BlockHash: staged.Header.BlockHash, Unit: recipeTestHash("b")}}
			}
			if err := s.publishSparseStaged(ctx, staged, revision, guard, nil); err == nil {
				t.Fatal("accepted failed publication", mode)
			}
			head, err := s.head()
			if err != nil || head.Revision != 0 {
				t.Fatal("failed publication changed head", head, err)
			}
		})
	}
}

func TestRecipeSparsePublicationChecksInsideAuthorityFence(t *testing.T) {
	s, _, staged := recipeSparsePublishFixture(t)
	inside := false
	guard := func(publish func() error) error { inside = true; defer func() { inside = false }(); return publish() }
	want := errors.New("lineage changed")
	err := s.publishSparseStaged(context.Background(), staged, 0, guard, func() error {
		if !inside {
			t.Fatal("check ran outside authority fence")
		}
		return want
	})
	if !errors.Is(err, want) {
		t.Fatal(err)
	}
	head, err := s.head()
	if err != nil || head.Revision != 0 {
		t.Fatal(head, err)
	}
}
