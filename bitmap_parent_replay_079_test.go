package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

	// The following District and Parcel claim strings are synthetic. They test
	// candidate selection with a real Ord relationship, not a real Bitmap claim.
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
	claims := []inscriptionOccurrence{}
	for _, o := range all.Occurrences {
		if o.TxID == childOccurrence.TxID && (o.Index == 40 || o.Index == 41 || o.Index == 42) {
			o.Body = []byte("0.0.bitmap")
			o.Envelope.ContentType = "text/plain"
			if o.Index == 40 {
				o.Envelope.Parents = nil
			}
			claims = append(claims, o)
		}
	}
	if len(claims) != 3 {
		t.Fatalf("synthetic candidate fixture missing occurrences: %d", len(claims))
	}
	prepared := inscriptionBlockResult{BlockHeight: childBlock.Height, BlockHash: childBlock.Hash, ParserProfile: inscriptionParserProfile, ReferenceCommit: inscriptionReferenceCommit, Occurrences: claims}
	actualSpender := parcel.parcelRawSpender
	parcel.parcelRawSpender = func(context.Context, satlinePoint, int64, parcelScanObserver079) (spenderLookupView, error) {
		return spenderLookupView{ConfirmedState: "unknown", Note: "fixture evidence missing"}, nil
	}
	if err := parcel.Apply(ctx, childBlock, &prepared); !errors.Is(err, ErrBitmapEvidenceRequired) {
		t.Fatalf("missing movement must remain pending: %v", err)
	}
	if parcel.head.Height != childBlock.Height-1 || parcel.head.WinnerCount != 0 {
		t.Fatal("pending declaration advanced coverage or reserved a winner")
	}
	parcel.parcelRawSpender = actualSpender
	if err := parcel.Apply(ctx, childBlock, &prepared); err != nil {
		t.Fatalf("synthetic candidate processing: %v", err)
	}
	if parcel.head.WinnerCount != 1 || parcel.head.CandidateCount != 3 {
		t.Fatalf("first winner selection: %+v", parcel.head)
	}
	reopened, err := readBitmapDevelopmentHead(ctx, root, bitmapReplayParcelID)
	if err != nil || reopened == nil || reopened.Commitment != parcel.head.Commitment {
		t.Fatalf("candidate restart: %+v %v", reopened, err)
	}
	var selectedWinner bitmapDevelopmentCandidate
	have, err := newOrdinalTree(filepath.Join(parcel.dir, "pages"), reopened.WinnerRoot).withContext(ctx).get(bitmapTargetKey(0, &district), &selectedWinner)
	if err != nil || !have || selectedWinner.Blocator != childPos {
		t.Fatalf("candidate winner: %+v %v", selectedWinner, err)
	}
	if err := verifyBitmapCandidateEvidence079(ctx, app, bitmapReplayParcelID, selectedWinner, hashAt); err != nil {
		t.Fatalf("candidate replay binding: %v", err)
	}
	if err := verifyBitmapCandidateEvidence079(ctx, app, bitmapReplayParcelID, selectedWinner, reorg); err == nil {
		t.Fatal("candidate accepted replaced movement block")
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
