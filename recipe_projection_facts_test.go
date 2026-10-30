package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRecipeProjectionFactsRejectCorruptIncompleteAndCanceled(t *testing.T) {
	ctx := context.Background()
	u, _, block := address076Source(t, []uint64{3}, []string{address076P2PKH})
	var member utxoMember
	if err := u.trees(ctx, u.head.Checkpoint)["live"].walk(func(kv ordinalKV) error { return json.Unmarshal(kv.Value, &member) }); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"target-corrupt", "source-corrupt", "incomplete", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			from, to := t.TempDir(), t.TempDir()
			refs, err := storeBlockOutputFactLayersContext(ctx, from, block, true, mode != "incomplete")
			if err != nil {
				t.Fatal(err)
			}
			m := member
			m.FactRef = refs[member.Position]
			source, err := outputFactPath(from, m.FactRef.Page)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "source-corrupt" {
				if err = os.WriteFile(source, []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "target-corrupt" {
				target, err := outputFactPath(to, m.FactRef.Page)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(target, []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			work := ctx
			if mode == "canceled" {
				var cancel context.CancelFunc
				work, cancel = context.WithCancel(ctx)
				cancel()
			}
			err = newRecipeProjectionFactImporter(from, to).copy(work, m)
			if err == nil || mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("invalid facts imported", err)
			}
		})
	}
}
