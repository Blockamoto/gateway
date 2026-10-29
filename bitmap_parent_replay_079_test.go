package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Optional mainnet fixture: the pinned Ord handbook lists the Rug Me child.
// Supply three raw blocks in GATEWAY_PARCEL_REFERENCE_DIR (or build/parcel-reference-079)
// to exercise the complete selected-chain witness, satpoint, receipt and restart path.
func TestParcelReplayMainnetReference079(t *testing.T) {
	dir := os.Getenv("GATEWAY_PARCEL_REFERENCE_DIR")
	if dir == "" {
		dir = filepath.Join("build", "parcel-reference-079")
	}
	refs := []struct {
		height int64
		hash   string
	}{
		{818117, "000000000000000000035ef07d180022e09a52f44e7587cbac23f25613d35fc2"},
		{818174, "000000000000000000033aff8cd450e7a1a26d421a1d32e0d7701b1ea1c1a7c7"},
		{818182, "00000000000000000002407a2a6bcca709f08aa425b65de3af9b58b295c62ae5"},
	}
	ctx := context.Background()
	selected := map[int64]string{}
	blocks := make([]blockView, 0, len(refs))
	parentID := "025107e06ac442f014c09a73cd97372f69619edd00dbeacca0aac55c75efe3ffi0"
	childID := "f87a6b16a1224b65f9c8ea55e1718585b3075373581689e343860c12a70d15c7i41"
	parentPos, childPos := "", ""
	var parentOccurrence, childOccurrence inscriptionOccurrence
	byHeight := map[int64]blockView{}
	for _, ref := range refs {
		raw, err := os.ReadFile(filepath.Join(dir, ref.hash+".block"))
		if os.IsNotExist(err) {
			t.Skip("optional mainnet reference blocks unavailable")
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) < 80 {
			t.Fatal("short fixture block")
		}
		block, err := parseBlockDetailed(ref.height, ref.hash, raw[:80], raw, "mainnet reference fixture", false)
		if err != nil || !integrityVerified(block) {
			t.Fatalf("block %d: %v", ref.height, err)
		}
		selected[ref.height] = ref.hash
		blocks = append(blocks, block)
		byHeight[ref.height] = block
		parsed, err := extractInscriptionOccurrences(block)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range parsed.Occurrences {
			if o.ID == parentID {
				parentPos = inscriptionCoordinate(uint32(o.TxIndex), o.Index, ref.height)
				parentOccurrence = o
			}
			if o.ID == childID {
				childPos = inscriptionCoordinate(uint32(o.TxIndex), o.Index, ref.height)
				childOccurrence = o
			}
		}
	}
	if parentPos == "" || childPos == "" {
		t.Fatalf("fixture inscriptions missing: parent=%s child=%s", parentPos, childPos)
	}
	hashAt := func(height int64) (string, error) {
		if h, ok := selected[height]; ok {
			return h, nil
		}
		return "", fmt.Errorf("fixture height unavailable")
	}
	root := t.TempDir()
	hash, err := storeParcelReplay079(ctx, root, parentID, childID, parentPos, childPos, "verified", blocks, hashAt)
	if err != nil {
		t.Fatalf("real provenance replay: %v", err)
	}
	if err := loadParcelReplay079(ctx, root, hash, parentID, childID, parentPos, childPos, "verified", hashAt); err != nil {
		t.Fatalf("reopen receipt: %v", err)
	}
	reorg := func(height int64) (string, error) {
		if height == 818174 {
			return testID(999), nil
		}
		return hashAt(height)
	}
	if err := loadParcelReplay079(ctx, root, hash, parentID, childID, parentPos, childPos, "verified", reorg); err == nil {
		t.Fatal("replaced movement block accepted")
	}
	// Exercise the candidate capture boundary against the same authentic
	// witnesses. The selected District is a deliberately synthetic test
	// binding; this reference inscription itself is not a Bitmap claim.
	// The locator built here is intentionally a one-block partial fixture; the
	// separate admission test requires a complete genesis-first locator prefix.
	locator, err := openIndexStore(root, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	locator.head.Mode = "full"
	if err := locator.BeginLocatorBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if err := locator.appendBlock(byHeight[818117], 818117, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	if err := locator.FlushLocatorBatch(ctx); err != nil {
		t.Fatal(err)
	}
	parentInput := byHeight[818117].Transactions[parentOccurrence.TxIndex].Inputs[parentOccurrence.InputIndex]
	visits := 0
	locatorBlockAt := func(_ context.Context, h int64) (blockView, error) {
		visits++
		if visits > 1 {
			return blockView{}, fmt.Errorf("fixture fetch budget exhausted")
		}
		v, ok := byHeight[h]
		if !ok {
			return blockView{}, fmt.Errorf("fixture bytes unavailable")
		}
		return v, nil
	}
	located, err := parcelLocatorTransaction079(ctx, locator, parentInput.PrevTxID, 818117, 818182, locatorBlockAt)
	if err != nil || located.Tx.TxID != parentInput.PrevTxID || visits != 1 {
		t.Fatalf("real parent prevout locator: %+v %v", located, err)
	}
	if _, err := parcelLocatorTransaction079(ctx, locator, parentInput.PrevTxID, 818117, 818182, locatorBlockAt); err == nil {
		t.Fatal("locator lookup ignored fetch budget")
	}
	if _, err := parcelLocatorTransaction079(ctx, locator, parentInput.PrevTxID, 818117, 818182, func(context.Context, int64) (blockView, error) { return blockView{}, fmt.Errorf("bytes unavailable") }); err == nil {
		t.Fatal("locator alone provided parent value")
	}
	if _, err := parcelLocatorTransaction079(ctx, locator, parentInput.PrevTxID, 818117, 818182, func(_ context.Context, h int64) (blockView, error) {
		v := byHeight[h]
		v.Hash = testID(9999)
		return v, nil
	}); err == nil {
		t.Fatal("locator accepted a replaced block")
	}
	backend := &parcelReplayBackend079{blocks: byHeight}
	app := &app{dataDir: root}
	rawSpenderCalls := 0
	consumer := &bitmapRunConsumer{a: app, root: root, id: bitmapReplayParcelID, hashAt: hashAt,
		blockAt: func(_ context.Context, h int64) (blockView, error) {
			b, ok := byHeight[h]
			if !ok {
				return blockView{}, fmt.Errorf("missing fixture block")
			}
			return b, nil
		},
		parcelRawSpender: func(_ context.Context, p satlinePoint, _ int64, _ parcelScanObserver079) (spenderLookupView, error) {
			rawSpenderCalls++
			return backend.Spender(p.TxID, p.Vout)
		},
		parcelRawTransaction: func(_ context.Context, id string, before int64) (satlineTxEvidence, error) {
			return backend.Transaction(id, before)
		},
	}
	parent := bitmapDevelopmentCandidate{ID: parentID, Blocator: parentPos, RevealHeight: 818117, RevealHash: selected[818117], TxIndex: parentOccurrence.TxIndex, InscriptionIndex: parentOccurrence.Index}
	proof, err := consumer.checkedReplayParent(ctx, byHeight[818182], childOccurrence, parent)
	if err != nil || proof == nil || proof.RelationState != "verified" {
		t.Fatalf("candidate capture: %+v %v", proof, err)
	}
	if rawSpenderCalls == 0 {
		t.Fatal("missing local hints did not fall back to the approved Raw source")
	}
	if err := loadParcelReplay079(ctx, root, proof.EvidenceHash, parentID, childID, parentPos, childPos, "verified", hashAt); err != nil {
		t.Fatalf("captured receipt: %v", err)
	}

	// The following District binding is synthetic. The reference child is not
	// a Bitmap claim, so fabricated claim bodies must never be accepted as an
	// authenticated extraction from its real Bitcoin block.
	district := uint64(0)
	genesisRaw := testGenesisBlockPayload(t)
	genesis, err := parseBlockDetailed(0, genesisHashDisplay, genesisRaw[:80], genesisRaw, "reference fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	byHeight[0], selected[0] = genesis, genesisHashDisplay
	childBlock := byHeight[818182]
	selected[818181] = childBlock.PreviousBlockHash
	districtConsumer := bitmapTestConsumer(t, root, bitmapStandardDistrictID)
	winner := parent
	winner.Kind, winner.District, winner.Selection, winner.Eligibility = "district", &district, "selected", "eligible"
	pageDir := filepath.Join(districtConsumer.dir, "pages")
	tree := newOrdinalTree(pageDir, "").withContext(ctx)
	if err := tree.put(bitmapTargetKey(district, nil), winner); err != nil {
		t.Fatal(err)
	}
	rootHash, err := tree.flush()
	if err != nil {
		t.Fatal(err)
	}
	dh := bitmapDevelopmentHead{Schema: 1, Index: bitmapStandardDistrictID, Profile: districtConsumer.instance.Profile, ProfileHash: districtConsumer.instance.ProfileHash, Height: childBlock.Height, BlockHash: childBlock.Hash, SegmentStart: bitmapDevelopmentStart, WinnerRoot: rootHash,
		LastBlock: bitmapDevelopmentBlock{Height: childBlock.Height, BlockHash: childBlock.Hash, BlockEvaluated: true, CompleteScope: true, WinnerBlocators: []string{}}}
	if err := districtConsumer.publish(ctx, dh); err != nil {
		t.Fatal(err)
	}
	parcel := bitmapTestConsumer(t, root, bitmapReplayParcelID)
	parcel.hashAt, parcel.blockAt = consumer.hashAt, consumer.blockAt
	parcel.parcelRawSpender, parcel.parcelRawTransaction = consumer.parcelRawSpender, consumer.parcelRawTransaction
	previous := bitmapDevelopmentHead{Schema: 1, Index: parcel.id, Profile: parcel.instance.Profile, ProfileHash: parcel.instance.ProfileHash, Height: childBlock.Height - 1, BlockHash: childBlock.PreviousBlockHash, SegmentStart: bitmapDevelopmentStart, DistrictInstance: bitmapStandardDistrictID,
		LastBlock: bitmapDevelopmentBlock{Height: childBlock.Height - 1, BlockHash: childBlock.PreviousBlockHash, BlockEvaluated: true, CompleteScope: true, WinnerBlocators: []string{}}}
	if err := parcel.publish(ctx, previous); err != nil {
		t.Fatal(err)
	}
	all, err := extractInscriptionOccurrences(childBlock)
	if err != nil {
		t.Fatal(err)
	}
	forged := all
	forged.Occurrences = append([]inscriptionOccurrence(nil), all.Occurrences...)
	childIndex := -1
	for i := range forged.Occurrences {
		if forged.Occurrences[i].ID == childID {
			childIndex = i
			forged.Occurrences[i].Body = []byte("0.0.bitmap")
			forged.Occurrences[i].Envelope.ContentType = "text/plain"
			break
		}
	}
	if childIndex < 0 {
		t.Fatal("real child occurrence unavailable for tamper check")
	}
	before := parcel.head.Commitment
	if err := parcel.Apply(ctx, childBlock, &forged); err == nil || !strings.Contains(err.Error(), "complete authenticated inventory") {
		t.Fatalf("modified real occurrence accepted as a Bitmap claim: %v", err)
	}
	subset := all
	subset.Occurrences = []inscriptionOccurrence{all.Occurrences[childIndex]}
	if err := parcel.Apply(ctx, childBlock, &subset); err == nil || !strings.Contains(err.Error(), "complete authenticated inventory") {
		t.Fatalf("subset of real occurrences accepted as a complete inventory: %v", err)
	}
	reopened, err := readBitmapDevelopmentHead(ctx, root, bitmapReplayParcelID)
	if err != nil || reopened == nil || reopened.Commitment != before || reopened.Height != childBlock.Height-1 || reopened.WinnerCount != 0 {
		t.Fatalf("rejected fabricated claims changed Parcel coverage or winners: %+v %v", reopened, err)
	}
}

// The genuine genesis block has no inscriptions. It verifies the receipt's
// selected-chain and byte-integrity gates, not a positive Parcel relationship.
func TestParcelReplayRejectsMissingAndReplacedEvidence079(t *testing.T) {
	raw := testGenesisBlockPayload(t)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	receipt := parcelReplayReceipt079{Schema: 3, Ord: ordParentReference, ParentID: strings.Repeat("1", 64) + "i0", ChildID: strings.Repeat("2", 64) + "i0", State: "verified", Blocks: []parcelReplayBlock079{{Height: 0, Hash: genesisHashDisplay, SHA256: digest}}}
	selected := func(int64) (string, error) { return genesisHashDisplay, nil }
	if err := replayParcelParent079(context.Background(), receipt, map[string][]byte{digest: raw}, selected); err == nil || !strings.Contains(err.Error(), "reveals required") {
		t.Fatalf("missing inscriptions: %v", err)
	}
	replaced := func(int64) (string, error) { return strings.Repeat("a", 64), nil }
	if err := replayParcelParent079(context.Background(), receipt, map[string][]byte{digest: raw}, replaced); err == nil || !strings.Contains(err.Error(), "selected chain") {
		t.Fatalf("replaced chain: %v", err)
	}
	tampered := append([]byte{}, raw...)
	tampered[len(tampered)-1] ^= 1
	if err := replayParcelParent079(context.Background(), receipt, map[string][]byte{digest: tampered}, selected); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("tampered bytes: %v", err)
	}
}

func TestParcelReplayBackendDoesNotInferAbsence079(t *testing.T) {
	b := &parcelReplayBackend079{blocks: map[int64]blockView{}}
	result, err := b.Spender(testID(42), 0)
	if err != nil || result.ConfirmedState != "unknown" {
		t.Fatalf("missing spend must be unknown: %+v %v", result, err)
	}
}

func TestParcelReplayNegativeRequiresCompleteInterval079(t *testing.T) {
	origin := testID(440)
	b0 := testBlock(100, testCoinbase(origin, 100))
	b1 := testBlock(101, testCoinbase(testID(441), 100))
	child := testTx(testID(442), []inputView{{PrevTxID: testID(443), PrevVout: 0}}, 1)
	b2 := testBlock(102, testCoinbase(testID(444), 100), child)
	blocks := map[int64]blockView{100: b0, 101: b1, 102: b2}
	hashAt := func(h int64) (string, error) {
		block, ok := blocks[h]
		if !ok {
			return "", fmt.Errorf("missing anchor")
		}
		return block.Hash, nil
	}
	point := satlinePoint{TxID: origin, Vout: 0, Height: 100, BlockHash: b0.Hash, TxIndex: 0}
	backend := &parcelReplayBackend079{blocks: blocks, through: 102}
	state, _, _, _ := ordParentAtReveal(newSatlineResolver(backend), point, child, 102, 1, hashAt)
	if state != "rejected" {
		t.Fatalf("complete no-spend transcript: %s", state)
	}
	delete(blocks, 101)
	state, _, _, _ = ordParentAtReveal(newSatlineResolver(backend), point, child, 102, 1, hashAt)
	if state != "pending" {
		t.Fatalf("missing interval block must be pending: %s", state)
	}
}

func TestParcelReplayLoadBindsRelationship079(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	parent, child := testID(71)+"i0", testID(72)+"i0"
	receipt := parcelReplayReceipt079{Schema: 3, Ord: ordParentReference, ParentID: parent, ChildID: child, ParentBlocator: "1i0.1", ChildBlocator: "2i0.2", State: "verified",
		Blocks: []parcelReplayBlock079{{Height: 0, Hash: genesisHashDisplay, SHA256: testID(73)}}}
	hash := indexDigest(receipt)
	if err := writeBitmapChainJSON(ctx, parcelReplayReceiptPath079(root, hash), receipt); err != nil {
		t.Fatal(err)
	}
	if err := loadParcelReplay079(ctx, root, hash, child, parent, "1i0.1", "2i0.2", "verified", nil); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("different pair must fail before reading blocks: %v", err)
	}
	if err := loadParcelReplay079(ctx, root, hash, "", child, "1i0.1", "2i0.2", "verified", nil); err == nil || !strings.Contains(err.Error(), "invalid expected") {
		t.Fatalf("missing expected parent must fail: %v", err)
	}
	if err := loadParcelReplay079(ctx, root, hash, parent, child, "1i0.1", "2i0.2", "verified", nil); err == nil || strings.Contains(err.Error(), "does not match") {
		t.Fatalf("matching pair must proceed to missing block evidence: %v", err)
	}
}
