package main

import (
	"context"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestBitmapCanonicalASCIICharsetAndIdentity(t *testing.T) {
	p := bitmapCanonicalProfile("district")
	for _, media := range []string{"text/plain", "TEXT/PLAIN; charset=utf-16", "text/plain; charset=unknown; example=allowed"} {
		got, reason := bitmapDecodeProfileBody(p, []byte("0.bitmap"), media)
		if got != "0.bitmap" || reason != "" {
			t.Fatalf("%s: %q %s", media, got, reason)
		}
	}
	for _, tc := range []struct {
		body          []byte
		media, reason string
	}{
		{[]byte("0.bitmap"), "text/html", "text_plain_required"},
		{[]byte{0xef, 0xbb, 0xbf, '0', '.', 'b', 'i', 't', 'm', 'a', 'p'}, "text/plain", "invalid_ascii"},
		{[]byte{0xff, 0xfe, '0', 0}, "text/plain; charset=utf-16", "invalid_ascii"},
	} {
		if _, reason := bitmapDecodeProfileBody(p, tc.body, tc.media); reason != tc.reason {
			t.Fatal(reason, tc.reason)
		}
	}
	hash := bitmapProfileHash(p)
	p.ReferenceRevision = strings.Repeat("1", 40)
	if bitmapProfileHash(p) == hash {
		t.Fatal("canonical pinned source is not authenticated")
	}
	p = bitmapCanonicalProfile("district")
	p.DecoderPolicy = "different"
	if bitmapProfileHash(p) == hash {
		t.Fatal("canonical implementation policy is not authenticated")
	}
	if bitmapProfileHash(bitmapProfile("district")) == hash {
		t.Fatal("legacy state shares canonical identity")
	}
}

func TestBitmapCanonicalFirstWholeASCIIRevealAndSafeRestart(t *testing.T) {
	ctx, root := context.Background(), t.TempDir()
	canonical := bitmapTestConsumer(t, root, bitmapCanonicalV1DistrictID)
	block := bitmapDevelopmentFixtureBlock(0, "", "0.bitmap", "0.bitmap", "01.bitmap", "1.bitmap", "0.BITMAP", "0.bitmap\n", " 0.bitmap", "0.bitmap.bitmap")
	block.Transactions[1].Inputs[0].Witness = ordWitness050(inscriptionTestScript([]byte{1}, []byte("TEXT/PLAIN; charset=utf-16; x=y"), []byte{11}, []byte{1}, nil, []byte("0.bitmap")))
	if err := canonical.Apply(ctx, block, nil); err != nil {
		t.Fatal(err)
	}
	if canonical.head.WinnerCount != 1 || canonical.head.CandidateCount != 8 || !canonical.head.Profile.CanonicalStandard {
		t.Fatal(canonical.head)
	}
	var selected bitmapDevelopmentCandidate
	have, err := newOrdinalTree(filepath.Join(canonical.dir, "pages"), canonical.head.WinnerRoot).withContext(ctx).get(bitmapTargetKey(0, nil), &selected)
	if err != nil || !have || selected.ID != block.Transactions[1].TxID+"i0" || selected.Reason != "first_qualifying_canonical_reveal" {
		t.Fatal(selected, err)
	}
	if got := bitmapTestConsumer(t, root, bitmapCanonicalV1DistrictID); got.head.Commitment != canonical.head.Commitment {
		t.Fatal("restart changed canonical outcome")
	}
	if got := bitmapTestConsumer(t, root, "bitmap-districts"); got.head != nil {
		t.Fatal("canonical state was silently projected into legacy identity")
	}
	next := bitmapDevelopmentFixtureBlock(1, block.Hash, "0.bitmap", "1.bitmap")
	if err = canonical.Apply(ctx, next, nil); err != nil {
		t.Fatal(err)
	}
	if canonical.head.WinnerCount != 2 || len(canonical.head.LastBlock.OrderedWinnerIDs) != 1 || canonical.head.LastBlock.OrderedWinnerIDs[0] != next.Transactions[2].TxID+"i0" {
		t.Fatal("first winner changed", canonical.head)
	}
	// New semantics never resume a checkpoint authenticated under a legacy profile.
	bad := *canonical.head
	bad.Profile = bitmapProfile("district")
	bad.ProfileHash = bitmapProfileHash(bad.Profile)
	bad.Commitment = bitmapDevelopmentDigest(bad)
	if err = writeBitmapChainJSON(ctx, filepath.Join(canonical.dir, "commits", bad.Commitment[:2], bad.Commitment+".json"), bad); err != nil {
		t.Fatal(err)
	}
	if _, err = readBitmapDevelopmentCommit(ctx, root, bitmapCanonicalV1DistrictID, bad.Commitment); err == nil {
		t.Fatal("legacy semantics resumed as canonical")
	}
	old := canonical.head.Commitment
	if err = canonical.Reconcile(ctx, func(h int64) (string, error) {
		if h == 0 {
			return block.Hash, nil
		}
		return testID(991991), nil
	}); err != nil {
		t.Fatal(err)
	}
	if canonical.head.Height != 0 || canonical.head.WinnerCount != 1 {
		t.Fatal("reorg failed to rewind canonical winners", canonical.head)
	}
	if _, err = readBitmapDevelopmentCommit(ctx, root, bitmapCanonicalV1DistrictID, old); err != nil {
		t.Fatal("reorg erased orphan inspection evidence", err)
	}
}

func TestBitmapCanonicalParcelSelectedDistrictAndProvenance(t *testing.T) {
	for _, tc := range []struct {
		name, claim             string
		declared, consumed, sat bool
		wantReason              string
		unresolved              bool
		advanceSat              bool
	}{
		{"valid", "0.0.bitmap", true, true, true, "first_qualifying_canonical_reveal", false, false},
		{"valid-retained-preblock", "0.0.bitmap", true, true, true, "first_qualifying_canonical_reveal", false, true},
		{"collected-preblock-is-gap", "0.0.bitmap", true, true, true, "saved_sat_dependency_unavailable", true, true},
		{"declaration-is-not-proof", "0.0.bitmap", true, true, false, "checked_parent_provenance_required", true, false},
		{"undeclared-selected-parent", "0.0.bitmap", false, true, true, "recognized_district_not_declared_parent", false, false},
		{"wrong-input", "0.0.bitmap", true, false, true, "parent_not_consumed_by_reveal", false, false},
		{"range", "1.0.bitmap", true, true, true, "transaction_out_of_range", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, root := context.Background(), t.TempDir()
			district := bitmapTestConsumer(t, root, bitmapCanonicalV1DistrictID)
			parcel := bitmapTestConsumer(t, root, bitmapCanonicalV1ParcelID)
			b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(86000, subsidyAtHeight(0)))
			cb := utxoFixtureCoinbase(86001, 10, subsidyAtHeight(1)-10)
			b1 := utxoFixtureBlock(1, b0.Hash, cb)
			parentTx := utxoFixtureSpend(86002, []inputView{{N: 0, PrevTxID: cb.TxID, Witness: ordWitness050(inscriptionTestScript([]byte{1}, []byte("text/plain; charset=unknown"), nil, []byte("0.bitmap")))}}, 10)
			b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(86003, subsidyAtHeight(2)), parentTx)
			var sat *indexStore
			var err error
			if tc.sat {
				sat, err = openIndexStore(root, "sat-state")
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, block := range []blockView{b0, b1, b2} {
				if tc.sat {
					if err = sat.appendBlock(block, 0, "pruned"); err != nil {
						t.Fatal(err)
					}
				}
				if err = district.Apply(ctx, block, nil); err != nil {
					t.Fatal(err)
				}
				if err = parcel.Apply(ctx, block, nil); err != nil {
					t.Fatal(err)
				}
			}
			parentBytes, _ := hex.DecodeString(parentTx.TxID)
			for i, j := 0, len(parentBytes)-1; i < j; i, j = i+1, j-1 {
				parentBytes[i], parentBytes[j] = parentBytes[j], parentBytes[i]
			}
			parts := [][]byte{[]byte{1}, []byte("text/plain; charset=utf-16")}
			if tc.declared {
				parts = append(parts, []byte{3}, parentBytes)
			}
			parts = append(parts, nil, []byte(tc.claim))
			prev := parentTx.TxID
			if !tc.consumed {
				prev = cb.TxID
			}
			child := utxoFixtureSpend(86004, []inputView{{N: 0, PrevTxID: prev, Witness: ordWitness050(inscriptionTestScript(parts...))}}, 10)
			b3 := utxoFixtureBlock(3, b2.Hash, utxoFixtureCoinbase(86005, subsidyAtHeight(3)), child)
			if tc.advanceSat {
				if err = sat.appendBlock(b3, 0, "pruned"); err != nil {
					t.Fatal(err)
				}
				if tc.name == "collected-preblock-is-gap" {
					// Collection is incremental and may need multiple bounded
					// mark passes before it can safely sweep the old state.
					for pass := 0; pass < 40; pass++ {
						err = sat.collectSat077State()
						if err == nil || !errors.Is(err, errOrdinalMaintenancePending) {
							break
						}
					}
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			parcel.SetBlockSource(func(_ context.Context, h int64) (blockView, error) {
				if h == 0 {
					return b0, nil
				}
				t.Fatalf("unexpected target block %d", h)
				return blockView{}, nil
			})
			if err = district.Apply(ctx, b3, nil); err != nil {
				t.Fatal(err)
			}
			err = parcel.Apply(ctx, b3, nil)
			if tc.unresolved {
				if !errors.Is(err, ErrBitmapEvidenceRequired) || parcel.head.Height != 2 {
					t.Fatal("missing proof became empty completion", parcel.head, err)
				}
				pending, e := readBitmapPending(ctx, root, bitmapCanonicalV1ParcelID, parcel.head)
				if e != nil || pending == nil || len(pending.Candidates) != 1 || pending.Candidates[0].Reason != tc.wantReason {
					t.Fatal(pending, e)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var c bitmapDevelopmentCandidate
			_, err = newOrdinalTree(filepath.Join(parcel.dir, "pages"), parcel.head.CandidateRoot).withContext(ctx).get(bitmapCandidateKey(bitmapDevelopmentCandidate{RevealHeight: 3, TxIndex: 1}), &c)
			if err != nil || c.Reason != tc.wantReason {
				t.Fatal(c, err)
			}
			if strings.HasPrefix(tc.name, "valid") && (parcel.head.WinnerCount != 1 || c.Provenance == nil || !c.Provenance.Verified || c.Provenance.Snapshot.Height != 2 || c.Provenance.Snapshot.BlockHash != b2.Hash) {
				t.Fatal("valid relationship not selected", c, parcel.head)
			}
			if !strings.HasPrefix(tc.name, "valid") && parcel.head.WinnerCount != 0 {
				t.Fatal("ineligible parcel selected", c)
			}
		})
	}
}

func TestBitmapCanonicalParcelMissingDistrictIsGap(t *testing.T) {
	ctx, root := context.Background(), t.TempDir()
	parcel := bitmapTestConsumer(t, root, bitmapCanonicalV1ParcelID)
	block := bitmapDevelopmentFixtureBlock(0, "", "0.0.bitmap")
	if err := parcel.Apply(ctx, block, nil); !errors.Is(err, ErrBitmapEvidenceRequired) || parcel.head != nil {
		t.Fatal("missing district became complete", parcel.head, err)
	}
	if h, err := readBitmapDevelopmentHead(ctx, root, bitmapCanonicalV1DistrictID); err != nil || h != nil {
		t.Fatal("parcel started district index", h, err)
	}
}
