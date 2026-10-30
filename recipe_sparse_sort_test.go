package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func recipeSparseSortFixture(t *testing.T, ctx context.Context, values []string, batch, fanIn int) (*recipeSparseSort, []recipeSparseSpoolRef) {
	t.Helper()
	dir := t.TempDir()
	keys, err := os.Create(filepath.Join(dir, "keys"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = keys.Close() })
	s, err := newRecipeSparseSort(ctx, dir, keys)
	if err != nil {
		t.Fatal(err)
	}
	s.batchRows, s.fanIn = batch, fanIn
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	refs := []recipeSparseSpoolRef{}
	for i, key := range values {
		offset, err := keys.Seek(0, io.SeekEnd)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := keys.WriteString(key); err != nil {
			t.Fatal(err)
		}
		ref := recipeSparseSpoolRef{KeyOffset: offset, KeyBytes: int64(len(key)), RowOffset: int64(i * 123), RowBytes: int64(len(key) + 1), RowHash: sha256.Sum256([]byte(key))}
		copy(ref.Prefix[:], key)
		refs = append(refs, ref)
		if err := s.Add(ref); err != nil {
			t.Fatal(err)
		}
	}
	return s, refs
}

func TestRecipeSparseSortPreservesLegacyByteOrderWithLongKeysAndRuns(t *testing.T) {
	values := []string{"z", "a#", "a", "a\x00", "a\x00b", "α", "a\"", "a\\"}
	for i := 129; i >= 0; i-- {
		values = append(values, fmt.Sprintf("common-table-%04d", i))
	}
	prefix := strings.Repeat("long-key/", 20_000)
	values = append(values, prefix+"z", prefix+"a", prefix)
	want := append([]string(nil), values...)
	sort.Strings(want)
	for _, spilled := range []bool{false, true} {
		t.Run(fmt.Sprint(spilled), func(t *testing.T) {
			batch := 4096
			if spilled {
				batch = 3
			}
			s, _ := recipeSparseSortFixture(t, context.Background(), values, batch, 2)
			if len(s.files) > 16 {
				t.Fatal("run metadata grew with every spill instead of compacting levels", len(s.files))
			}
			got := []string{}
			err := s.Sorted(func(ref recipeSparseSpoolRef) error {
				key := make([]byte, ref.KeyBytes)
				if _, err := s.keys.ReadAt(key, ref.KeyOffset); err != nil {
					return err
				}
				if ref.RowHash != sha256.Sum256(key) {
					return fmt.Errorf("row identity changed during sort")
				}
				got = append(got, string(key))
				return nil
			})
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatal("stream order differs from legacy sort", len(got), err)
			}
			if err := s.Add(recipeSparseSpoolRef{}); err == nil {
				t.Fatal("consumed sorter accepted additional data")
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(s.dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != "keys" {
				t.Fatal("temporary runs leaked or the owned input was removed", entries, err)
			}
		})
	}
}

func TestRecipeSparseSortCancellationSinkFailureAndDamagedRun(t *testing.T) {
	values := []string{}
	for i := 31; i >= 0; i-- {
		values = append(values, fmt.Sprintf("key-%03d", i))
	}
	for _, batch := range []int{4096, 3} {
		t.Run(fmt.Sprintf("cancel-%d", batch), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s, _ := recipeSparseSortFixture(t, ctx, values, batch, 2)
			n := 0
			err := s.Sorted(func(recipeSparseSpoolRef) error {
				n++
				if n == 3 {
					cancel()
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) || n != 3 {
				t.Fatal("sort did not stop between emitted refs", n, err)
			}
		})
	}
	t.Run("sink", func(t *testing.T) {
		s, _ := recipeSparseSortFixture(t, context.Background(), values, 3, 2)
		want := errors.New("unit staging failed")
		if err := s.Sorted(func(recipeSparseSpoolRef) error { return want }); !errors.Is(err, want) {
			t.Fatal("sort ignored sink failure", err)
		}
	})
	t.Run("truncated-run", func(t *testing.T) {
		s, _ := recipeSparseSortFixture(t, context.Background(), values, 3, 2)
		if len(s.runs) == 0 {
			t.Fatal("fixture did not create a run")
		}
		info, err := os.Stat(s.runs[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(s.runs[0], info.Size()-1); err != nil {
			t.Fatal(err)
		}
		if err := s.Sorted(func(recipeSparseSpoolRef) error { return nil }); err == nil {
			t.Fatal("damaged run was accepted")
		}
	})
	t.Run("changed-key-spool", func(t *testing.T) {
		s, refs := recipeSparseSortFixture(t, context.Background(), values, 3, 2)
		if _, err := s.keys.WriteAt([]byte("X"), refs[0].KeyOffset); err != nil {
			t.Fatal(err)
		}
		if err := s.Sorted(func(recipeSparseSpoolRef) error { return nil }); err == nil {
			t.Fatal("changed captured key prefix was accepted")
		}
	})
}

func TestRecipeSparseSortEmptyAndInvalidExtent(t *testing.T) {
	s, _ := recipeSparseSortFixture(t, context.Background(), nil, 3, 2)
	if err := s.Sorted(func(recipeSparseSpoolRef) error { t.Fatal("empty sort emitted a row"); return nil }); err != nil {
		t.Fatal(err)
	}
	s, refs := recipeSparseSortFixture(t, context.Background(), []string{"one"}, 3, 2)
	bad := refs[0]
	bad.KeyOffset = 1 << 62
	if err := s.Add(bad); err != nil {
		t.Fatal(err)
	}
	if err := s.Sorted(func(recipeSparseSpoolRef) error { return nil }); err == nil {
		t.Fatal("out-of-range key extent accepted")
	}
}
