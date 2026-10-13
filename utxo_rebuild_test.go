package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUTXOExplicitRebuildArchivesPinsAndResumesOnce(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, e := openUTXOStoreContext(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(1000, 50))
	utxoApply(t, s, b0)
	b1 := utxoFixtureBlock(1, b0.Hash, utxoFixtureCoinbase(1001, 7))
	utxoApply(t, s, b1)
	oldCoin := utxoRequireCoin(t, s, "0.0.1", 7)
	oldCommit := s.Head().Commitment
	jobID := "reviewed-rebuild-job"
	if e = resetUTXOForRebuild(ctx, root, jobID); e != nil {
		t.Fatal(e)
	}
	archive := filepath.Join(root, "indexes", "utxo-state-archives", indexDigest(jobID))
	if _, e = os.Stat(filepath.Join(archive, "head.json")); e != nil {
		t.Fatal("old state not archived", e)
	}
	s, e = openUTXOStoreContext(ctx, root)
	if e != nil || s.Head() != nil {
		t.Fatal("rebuild did not start an empty prefix", e)
	}
	utxoApply(t, s, b0)
	replacement := utxoFixtureBlock(1, b0.Hash, utxoFixtureCoinbase(1002, 11))
	replacement.Hash = testID(1002001)
	utxoApply(t, s, replacement)
	newCommit := s.Head().Commitment
	if newCommit == oldCommit {
		t.Fatal("fixture did not change state")
	}
	if e = resetUTXOForRebuild(ctx, root, jobID); e != nil {
		t.Fatal(e)
	}
	s, e = openUTXOStoreContext(ctx, root)
	if e != nil || s.Head().Commitment != newCommit {
		t.Fatal("same rebuild job reset committed progress", e)
	}
	pins := map[string]bool{}
	if e = s.VisitFactPages(ctx, func(p string) error { pins[p] = true; return nil }); e != nil || !pins[oldCoin.FactRef.Page] {
		t.Fatal("archived live Coin facts lost their pins", e)
	}
	if e = s.CollectPages(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(archive, "commits", oldCommit+".json")); e != nil {
		t.Fatal("current collection deleted archived state", e)
	}
	if e = os.Remove(filepath.Join(archive, "head.json")); e != nil {
		t.Fatal(e)
	}
	if e = s.VisitFactPages(ctx, func(string) error { return nil }); !errors.Is(e, ErrUTXOStateCorrupt) {
		t.Fatal("missing archived head allowed shared fact collection", e)
	}
}

func TestUTXORebuildCanArchiveCorruptHeadAndRecoverInterruptedRename(t *testing.T) {
	ctx := context.Background()
	for _, scenario := range []string{"corrupt_head", "after_rename"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			s, e := openUTXOStoreContext(ctx, root)
			if e != nil {
				t.Fatal(e)
			}
			utxoApply(t, s, utxoFixtureBlock(0, "", utxoFixtureCoinbase(1010, 50)))
			jobID := "rebuild-" + scenario
			key := indexDigest(jobID)
			if scenario == "corrupt_head" {
				if e = os.WriteFile(filepath.Join(s.dir, "head.json"), []byte("broken prior head"), 0600); e != nil {
					t.Fatal(e)
				}
				if _, e = openUTXOStoreContext(ctx, root); !errors.Is(e, ErrUTXOStateCorrupt) {
					t.Fatal("normal open accepted corruption", e)
				}
			} else {
				marker := utxoRebuildMarker{Schema: 1, JobID: jobID, PriorHead: true}
				marker.Checksum = utxoRebuildDigest(marker)
				if e = atomicWriteJSON(filepath.Join(root, "indexes", "utxo-rebuild-jobs", key+".json"), marker); e != nil {
					t.Fatal(e)
				}
				archive := filepath.Join(root, "indexes", "utxo-state-archives", key)
				if e = os.MkdirAll(filepath.Dir(archive), 0755); e != nil {
					t.Fatal(e)
				}
				if e = os.Rename(s.dir, archive); e != nil {
					t.Fatal(e)
				}
			}
			if e = resetUTXOForRebuild(ctx, root, jobID); e != nil {
				t.Fatal(e)
			}
			s, e = openUTXOStoreContext(ctx, root)
			if e != nil || s.Head() != nil {
				t.Fatal("reviewed rebuild could not restart from genesis", e)
			}
			if scenario == "corrupt_head" {
				if e = s.VisitFactPages(ctx, func(string) error { return nil }); !errors.Is(e, ErrUTXOStateCorrupt) {
					t.Fatal("corrupt archived state did not stop shared collection", e)
				}
			}
		})
	}
}
