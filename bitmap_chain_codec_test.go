package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBitmapChainDevCodecFixedVectorsAndRegistryHistory(t *testing.T) {
	codec, err := bitmapChainCodecFor(bitmapChainCodecVersion)
	if err != nil {
		t.Fatal(err)
	}
	var tx, source []byte
	for n := byte(0); n < 32; n++ {
		tx = append(tx, n)
		source = append(source, n+32)
	}
	isolated, err := codec.Outcome([]string{hex.EncodeToString(tx) + "i513"})
	if err != nil || isolated != "0c0da5df7750e2b98b672bad3684eee982780de8833015c53097e8cc56966413" {
		t.Fatal(isolated, err)
	}
	empty, err := codec.Outcome(nil)
	if err != nil || empty != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatal(empty, err)
	}
	slot := bitmapChainRuleSlot{DefinitionBytes: []byte("example immutable definition"), IntroducedHeight: 7, ActivationHeights: []int64{7, 20}, RetirementHeights: []int64{12}}
	registry, err := codec.Registry([]bitmapChainRuleSlot{slot}, 7)
	if err != nil || registry != "a127d493cae621b50a734ab9563fdcde2cf88f4926da4400fe4bd5cb3d531b60" {
		t.Fatal(registry, err)
	}
	oc, final, err := codec.Step(strings.Repeat("0", 64), hex.EncodeToString(source), registry, strings.Repeat("0", 64), isolated)
	if err != nil || oc != "1eba01470bba7a0401ed057d9f93c05bb1c3e4cdd2513e9159e6a0a48e36ee9b" || final != "1de86e72c6f1b182cdf69639c8008d4a321e2b5ae6b99c9de1cc8806133845bd" {
		t.Fatal(oc, final, err)
	}
	before, _ := codec.Registry([]bitmapChainRuleSlot{slot}, 6)
	inactive, _ := codec.Registry([]bitmapChainRuleSlot{slot}, 12)
	reactive, _ := codec.Registry([]bitmapChainRuleSlot{slot}, 20)
	if before != empty || inactive != bitmapChainSHA(make([]byte, 32)) || reactive != registry {
		t.Fatal("slot introduction or activation history changed", before, inactive, reactive)
	}
	invalidIntroduction := slot
	invalidIntroduction.IntroducedHeight = 6
	if _, err := codec.Registry([]bitmapChainRuleSlot{invalidIntroduction}, 6); err == nil {
		t.Fatal("slot introduced before its first activation")
	}
	if changed, _, _ := codec.Step(final, hex.EncodeToString(source), registry, strings.Repeat("0", 64), isolated); changed != oc {
		t.Fatal("final input leaked into independent outcome chain")
	}
	_, again, _ := codec.Step(final, hex.EncodeToString(source), registry, strings.Repeat("0", 64), isolated)
	if again == final {
		t.Fatal("previous FINAL hash omitted")
	}
	id1, id2 := strings.Repeat("1", 64)+"i2", strings.Repeat("2", 64)+"i1"
	a, _ := codec.Outcome([]string{id1, id2})
	b, _ := codec.Outcome([]string{id2, id1})
	if a != b {
		t.Fatal("mechanical ID sort is nondeterministic")
	}
	if _, err := codec.Outcome([]string{id1, id1}); err == nil {
		t.Fatal("duplicate identity accepted")
	}
	if _, err := bitmapChainCodecFor("future-unreviewed-codec"); err == nil {
		t.Fatal("unrecognized codec silently resumed")
	}
}

func bitmapCanonicalChainFixture(t *testing.T, root string, simultaneous bool) (*bitmapRunConsumer, *bitmapRunConsumer, map[int64]blockView) {
	t.Helper()
	ctx := context.Background()
	district := bitmapTestConsumer(t, root, bitmapCanonicalV1DistrictID)
	parcel := bitmapTestConsumer(t, root, bitmapCanonicalV1ParcelID)
	dchain := bitmapTestConsumer(t, root, "bitmap-district-chain-dev-v1")
	pchain := bitmapTestConsumer(t, root, "bitmap-parcel-chain-dev-v1")
	blocks := map[int64]blockView{}
	hashAt := bitmapChainTestHashes(blocks)
	for _, chain := range []*bitmapRunConsumer{dchain, pchain} {
		chain.SetBlockSource(func(context.Context, int64) (blockView, error) {
			t.Fatal("chain loaded raw block")
			return blockView{}, nil
		})
		if err := chain.Reconcile(ctx, hashAt); err != nil {
			t.Fatal(err)
		}
	}
	if err := dchain.ApplyRetainedOutcome(ctx, 0); !errors.Is(err, ErrBitmapEvidenceRequired) {
		t.Fatal("unknown origin treated as empty", err)
	}
	previous := ""
	for height := int64(0); height < 4; height++ {
		claims := []string{}
		if height == 1 {
			claims = []string{"0.bitmap", "1.bitmap"}
		}
		block := bitmapDevelopmentFixtureBlock(height, previous, claims...)
		blocks[height] = block
		previous = block.Hash
		if err := district.Apply(ctx, block, nil); err != nil {
			t.Fatal(err)
		}
		if err := parcel.Apply(ctx, block, nil); err != nil {
			t.Fatal(err)
		}
		if simultaneous && height >= 1 {
			// The block adapter must take the same outcome-only path.
			if err := dchain.Apply(ctx, block, nil); err != nil {
				t.Fatal(err)
			}
			if err := pchain.ApplyRetainedOutcome(ctx, height); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !simultaneous {
		for height := int64(1); height < 4; height++ {
			if err := dchain.ApplyRetainedOutcome(ctx, height); err != nil {
				t.Fatal(err)
			}
			if err := pchain.ApplyRetainedOutcome(ctx, height); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dchain, pchain, blocks
}

func TestBitmapChainDevSimultaneousEqualsSavedOutcomeCatchupAndRestart(t *testing.T) {
	a, b, _ := bitmapCanonicalChainFixture(t, t.TempDir(), true)
	c, d, _ := bitmapCanonicalChainFixture(t, t.TempDir(), false)
	for _, pair := range [][2]*bitmapRunConsumer{{a, c}, {b, d}} {
		left, right := pair[0].head, pair[1].head
		if left.SegmentStart != 1 || left.LastBlock.ChainCodec != bitmapChainCodecVersion || *left.LastBlock.ScopedCommitment != *right.LastBlock.ScopedCommitment || left.LastBlock.ChainHash != right.LastBlock.ChainHash {
			t.Fatal("catchup differs", left, right)
		}
		if len(left.LastBlock.OrderedWinnerIDs) != 0 || left.LastBlock.IsolatedHash != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
			t.Fatal("completed empty outcome lost", left.LastBlock)
		}
		reopened := bitmapTestConsumer(t, pair[0].root, pair[0].id)
		if reopened.head.Commitment != left.Commitment || reopened.head.ChainState.Codec != bitmapChainCodecVersion {
			t.Fatal("restart changed codec or commitment")
		}
		if err := pair[0].ApplyRetainedOutcome(context.Background(), 4); !errors.Is(err, ErrBitmapEvidenceRequired) {
			t.Fatal("missing source block became empty", err)
		}
		if pair[0].head.Height != 3 {
			t.Fatal("gap advanced checkpoint")
		}
	}
	if a.head.LastBlock.ChainHash == b.head.LastBlock.ChainHash || *a.head.LastBlock.ScopedCommitment == *b.head.LastBlock.ScopedCommitment {
		t.Fatal("district and parcel chains were mixed")
	}
	progress := indexRunProgress{Index: b.id}
	if _, err := bindIndexRunBitmapProgress(context.Background(), b.root, &progress); err != nil || progress.DistrictInstance != bitmapCanonicalV1ParcelID {
		t.Fatal("parcel outcome producer binding rejected", progress, err)
	}
	bad := *a.head
	state := *bad.ChainState
	state.Codec = "future-unreviewed-codec"
	bad.ChainState = &state
	bad.LastBlock.ChainCodec = state.Codec
	bad.DistrictReceipt = nil
	bad.Commitment = bitmapDevelopmentDigest(bad)
	if err := writeBitmapChainJSON(context.Background(), filepath.Join(a.dir, "commits", bad.Commitment[:2], bad.Commitment+".json"), bad); err != nil {
		t.Fatal(err)
	}
	if _, err := readBitmapDevelopmentCommit(context.Background(), a.root, a.id, bad.Commitment); err == nil {
		t.Fatal("unknown saved codec silently resumed")
	}
}

func TestBitmapChainDevInteriorOutcomeGapIsNotCompletedInterval(t *testing.T) {
	chain, _, _ := bitmapCanonicalChainFixture(t, t.TempDir(), true)
	ctx := context.Background()
	producer := bitmapTestConsumer(t, chain.root, bitmapCanonicalV1DistrictID)
	tree := newOrdinalTree(filepath.Join(producer.dir, "pages"), producer.head.BlockRoot).withContext(ctx)
	tree.remove(fmt.Sprintf("%016x", int64(2)))
	root, err := tree.flush()
	if err != nil {
		t.Fatal(err)
	}
	copy := *producer.head
	copy.BlockRoot = root
	if err := producer.publish(ctx, copy); err != nil {
		t.Fatal(err)
	}
	complete, err := bitmapCompletedOutcomeInterval(ctx, chain.root, producer.id, 1, 3, producer.instance.ProfileHash)
	if err != nil || complete {
		t.Fatal("missing interior outcome accepted as complete", complete, err)
	}
}

func TestBitmapChainDevPrunedRestartAndSelectedBranchRecovery(t *testing.T) {
	chain, _, blocks := bitmapCanonicalChainFixture(t, t.TempDir(), true)
	ctx := context.Background()
	producer := bitmapTestConsumer(t, chain.root, bitmapCanonicalV1DistrictID)
	producerCommitment := producer.head.Commitment
	chain.ConfigureRetention("pruned")
	if err := chain.Reconcile(ctx, bitmapChainTestHashes(blocks)); err != nil {
		t.Fatal(err)
	}
	reopened := bitmapTestConsumer(t, chain.root, chain.id)
	view, err := bitmapChainRetentionStatus(ctx, chain.root, chain.id)
	if err != nil || view.Mode != "pruned" || len(view.RestorePoints) == 0 {
		t.Fatal(view, err)
	}
	// Height 3 leaves the selected branch; Pruned recovers the retained origin.
	replacement := bitmapDevelopmentFixtureBlock(3, blocks[2].Hash, "3.bitmap")
	replacement.Hash = testID(909090)
	blocks[3] = replacement
	if err := reopened.Reconcile(ctx, bitmapChainTestHashes(blocks)); err != nil {
		t.Fatal(err)
	}
	if reopened.head.Height != 1 || reopened.head.ChainState.Codec != bitmapChainCodecVersion {
		t.Fatal("Pruned recovery failed", reopened.head)
	}
	if current, err := readBitmapDevelopmentHead(ctx, chain.root, producer.id); err != nil || current.Commitment != producerCommitment {
		t.Fatal("Chain recovery changed producer", current, err)
	}
	if err := reopened.ApplyRetainedOutcome(ctx, 2); err != nil {
		t.Fatal("recovery could not replay retained empty outcome", err)
	}
	before := reopened.head.Commitment
	if err := reopened.ApplyRetainedOutcome(ctx, 3); !errors.Is(err, ErrBitmapEvidenceRequired) {
		t.Fatal("orphan source outcome accepted", err)
	}
	if reopened.head.Commitment != before {
		t.Fatal("orphan advanced Chain")
	}
	if err := producer.Reconcile(ctx, bitmapChainTestHashes(blocks)); err != nil {
		t.Fatal(err)
	}
	if err := producer.Apply(ctx, replacement, nil); err != nil {
		t.Fatal(err)
	}
	if err := reopened.ApplyRetainedOutcome(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if reopened.head.BlockHash != replacement.Hash {
		t.Fatal("new selected branch not committed")
	}
}

func TestBitmapChainDevOwnedReceiptsReconcileWithoutProducerFiles(t *testing.T) {
	chain, _, blocks := bitmapCanonicalChainFixture(t, t.TempDir(), true)
	ctx := context.Background()
	producerDir := bitmapDevelopmentDir(chain.root, bitmapCanonicalV1DistrictID)
	if err := os.Rename(producerDir, filepath.Join(chain.root, "preserved-producer")); err != nil {
		t.Fatal(err)
	}
	reopened := bitmapTestConsumer(t, chain.root, chain.id)
	before := reopened.head.Commitment
	if err := reopened.Reconcile(ctx, bitmapChainTestHashes(blocks)); err != nil {
		t.Fatal("owned receipts depended on deleted producer", err)
	}
	if reopened.head.Commitment != before {
		t.Fatal("reconcile changed selected saved chain")
	}
	if err := reopened.ApplyRetainedOutcome(ctx, 4); !errors.Is(err, ErrBitmapEvidenceRequired) {
		t.Fatal("missing future producer not reported", err)
	}
}
