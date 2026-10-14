package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func compatibility076OldUnownedFact(t *testing.T, root string) (outputFactRef, string, []byte) {
	t.Helper()
	b := utxoFixtureBlock(7, testID(760700), utxoFixtureCoinbase(760701, 17))
	refs, err := storeBlockOutputFactsContext(context.Background(), root, b)
	if err != nil {
		t.Fatal(err)
	}
	ref := refs["0.0.7"]
	path, err := outputFactPath(root, ref.Page)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err = os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return ref, path, raw
}

func Test076SharedFactCollectionPreservesUnknownSatOwners(t *testing.T) {
	for _, relative := range []string{
		"sat-state/head.json",
		"sat-state/commits/unknown.json",
		"sat-state/ordinal-pages/unknown.json",
		"sat-state-archives/preserved/head.json",
		"sat-state-archives/preserved/commits/unknown.json",
		"sat-state", // Damaged storage must not look like absent ownership.
	} {
		t.Run(relative, func(t *testing.T) {
			root := t.TempDir()
			ref, path, before := compatibility076OldUnownedFact(t, root)
			// Shape is deliberately foreign to Addresses: it must preserve
			// evidence without attempting to validate or rewrite a newer schema.
			unknown, err := json.Marshal(map[string]any{
				"profile": "gateway-sat-fifo-mainnet-bps-v2", "layout": 1,
				"sat_savepoints": []any{map[string]any{"height": 144, "fact_ref": ref}},
			})
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(root, "indexes", filepath.FromSlash(relative))
			if err = os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(marker, unknown, 0600); err != nil {
				t.Fatal(err)
			}
			if err = collectSharedOutputFactsContext(context.Background(), root); err != nil {
				t.Fatal("preserved Sat state blocked unrelated maintenance", err)
			}
			if after, err := os.ReadFile(path); err != nil || !bytes.Equal(before, after) {
				t.Fatal("Sat-only shared fact was swept or changed", err)
			}
			if after, err := os.ReadFile(marker); err != nil || !bytes.Equal(unknown, after) {
				t.Fatal("unsupported Sat evidence was changed", err)
			}
		})
	}
}

func Test076SharedFactCollectionSatDeferralAllowsCurrentWrites(t *testing.T) {
	ctx := context.Background()
	u, _, b1 := address076Source(t, []uint64{11}, []string{address076P2PKH})
	_, oldPath, before := compatibility076OldUnownedFact(t, u.root)
	marker := filepath.Join(u.root, "indexes", "sat-state", "head.json")
	if err := atomicWriteBytes(marker, []byte(`{"unknown_future_snapshot":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := collectSharedOutputFactsContext(ctx, u.root); err != nil {
		t.Fatal(err)
	}
	if _, err := buildAddressStateContext(ctx, u.root, nil); err != nil {
		t.Fatal("newer Sat data blocked current address build", err)
	}
	cb := utxoFixtureCoinbase(760702, 5)
	cb.Outputs[0].ScriptPubKey = address076P2PKH
	b2 := utxoFixtureBlock(2, b1.Hash, cb)
	utxoApply(t, u, b2)
	if err := collectSharedOutputFactsContext(ctx, u.root); err != nil {
		t.Fatal("newer Sat data blocked committed UTXO maintenance", err)
	}
	r, err := lookupAddressStateContext(ctx, u.root, address076P2PKH, 10, "")
	if err != nil || r.BalanceSats != 16 || r.OutputCount != 2 || r.Checkpoint.Height != 2 || r.Checkpoint.UTXOCommitment != u.Head().Commitment {
		t.Fatal("healthy current state did not advance", r, err)
	}
	utxoRequireCoin(t, u, "0.0.2", 5)
	if after, err := os.ReadFile(oldPath); err != nil || !bytes.Equal(before, after) {
		t.Fatal("unrecognized Sat owner lost its fact during healthy writes", err)
	}
}

func Test076SharedFactCollectionWithoutSatStillSweeps(t *testing.T) {
	root := t.TempDir()
	_, path, _ := compatibility076OldUnownedFact(t, root)
	if err := collectSharedOutputFactsContext(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("ordinary orphan collection was disabled", err)
	}
}

func Test076SharedFactCollectionSatDeferralPreservesCancellation(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "indexes", "sat-state"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := collectSharedOutputFactsContext(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatal("deferred sweep hid cancellation", err)
	}
}
