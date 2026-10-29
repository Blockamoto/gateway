package main

// Experimental, positive-only detached Parcel provenance receipts. This is not
// an admission path for the Parcel index: callers must separately establish
// prerequisite coverage and the selected District interpretation.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"./internal/updateapply"
)

const parcelReplayMaxBlocks079 = 128
const parcelReplayMaxBytes079 = 64 << 20

type parcelReplayBlock079 struct {
	Height int64  `json:"height"`
	Hash   string `json:"hash"`
	SHA256 string `json:"sha256"`
}

type parcelReplayReceipt079 struct {
	Schema         int                      `json:"schema"`
	Ord            string                   `json:"ord_reference"`
	ParentID       string                   `json:"parent_id"`
	ChildID        string                   `json:"child_id"`
	State          string                   `json:"state"`
	ParentBlocator string                   `json:"parent_blocator"`
	ChildBlocator  string                   `json:"child_blocator"`
	Negative       *parcelReplayNegative079 `json:"negative,omitempty"`
	Blocks         []parcelReplayBlock079   `json:"blocks"`
}

// A long negative proof replays the retained selected Raw interval on demand.
// Its endpoint and exact satpoint are part of the content-addressed receipt.
type parcelReplayNegative079 struct {
	Point       satlinePoint `json:"point"`
	Through     int64        `json:"through"`
	Attestation string       `json:"attestation"`
}

func parcelReplayBlockPath079(root, hash string) string {
	return filepath.Join(root, "indexes", "bitmap-parent-replay-v3", "blocks", hash[:2], hash+".block")
}
func parcelReplayReceiptPath079(root, hash string) string {
	return filepath.Join(root, "indexes", "bitmap-parent-replay-v3", "receipts", hash[:2], hash+".json")
}

// HashAt must consult locally verified selected-chain headers. A block hash or
// an internally consistent receipt by itself is not a selected-chain anchor.
func replayParcelParent079(ctx context.Context, receipt parcelReplayReceipt079, raw map[string][]byte, hashAt func(int64) (string, error)) error {
	return replayParcelParentWithSource079(ctx, receipt, raw, hashAt, nil, "")
}
func replayParcelParentWithSource079(ctx context.Context, receipt parcelReplayReceipt079, raw map[string][]byte, hashAt func(int64) (string, error), read func(context.Context, int64, string) (*blockView, error), attestationRoot string) error {
	if receipt.Schema != 3 || receipt.Ord != ordParentReference || (receipt.State != "verified" && receipt.State != "rejected") || hashAt == nil || len(receipt.Blocks) == 0 || len(receipt.Blocks) > parcelReplayMaxBlocks079 {
		return fmt.Errorf("invalid Parcel replay receipt")
	}
	backend := &parcelReplayBackend079{blocks: map[int64]blockView{}, ctx: ctx, hashAt: hashAt, readRaw: read, negative: receipt.Negative, attestationRoot: attestationRoot}
	total := 0
	for _, ref := range receipt.Blocks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if ref.Height < 0 || !validHash(ref.Hash) || !validHash(ref.SHA256) {
			return fmt.Errorf("invalid block reference")
		}
		if _, exists := backend.blocks[ref.Height]; exists {
			return fmt.Errorf("duplicate receipt block height")
		}
		body, ok := raw[ref.SHA256]
		total += len(body)
		if !ok || len(body) < 81 || total > parcelReplayMaxBytes079 {
			return fmt.Errorf("missing or oversized receipt block")
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != ref.SHA256 {
			return fmt.Errorf("receipt block digest mismatch")
		}
		selected, err := hashAt(ref.Height)
		if err != nil || !strings.EqualFold(selected, ref.Hash) {
			return fmt.Errorf("receipt block is not on selected chain")
		}
		block, err := parseBlockDetailed(ref.Height, ref.Hash, body[:80], body, "detached Parcel replay", false)
		if err != nil || !integrityVerified(block) {
			return fmt.Errorf("invalid receipt block: %v", err)
		}
		// Headers and Merkle roots alone do not establish transaction ordering
		// validity. Never use a later transaction in the same block as a prevout.
		positions := make(map[string]int, len(block.Transactions))
		for i, tx := range block.Transactions {
			positions[strings.ToLower(tx.TxID)] = i
		}
		for i, tx := range block.Transactions {
			for _, in := range tx.Inputs {
				if earlier, ok := positions[strings.ToLower(in.PrevTxID)]; !in.Coinbase && ok && earlier >= i {
					return fmt.Errorf("receipt contains a nonpreceding same-block prevout")
				}
			}
		}
		backend.blocks[ref.Height] = block
	}
	var parent, child *inscriptionOccurrence
	var parentBlock, childBlock blockView
	for _, block := range backend.blocks {
		parsed, err := extractInscriptionOccurrences(block)
		if err != nil {
			return err
		}
		for i := range parsed.Occurrences {
			o := parsed.Occurrences[i]
			if strings.EqualFold(o.ID, receipt.ParentID) {
				if parent != nil {
					return fmt.Errorf("ambiguous parent")
				}
				p := o
				parent = &p
				parentBlock = block
			}
			if strings.EqualFold(o.ID, receipt.ChildID) {
				if child != nil {
					return fmt.Errorf("ambiguous child")
				}
				p := o
				child = &p
				childBlock = block
			}
		}
	}
	if parent == nil || child == nil || !parentBlock.Verification.WitnessCommitment || !childBlock.Verification.WitnessCommitment {
		return fmt.Errorf("authenticated parent and child reveals required")
	}
	backend.through = childBlock.Height
	if receipt.ParentBlocator != inscriptionCoordinate(uint32(parent.TxIndex), parent.Index, parentBlock.Height) || receipt.ChildBlocator != inscriptionCoordinate(uint32(child.TxIndex), child.Index, childBlock.Height) {
		return fmt.Errorf("Parcel receipt inscription position mismatch")
	}
	declared := false
	for _, id := range child.Envelope.Parents {
		if strings.EqualFold(id, parent.ID) {
			declared = true
			break
		}
	}
	if !declared {
		return fmt.Errorf("child does not declare parent")
	}
	if strings.EqualFold(parent.TxID, child.TxID) {
		childRecord := ordRecord{ID: child.ID, TxID: child.TxID, Index: int(child.Index), Height: childBlock.Height}
		state, reason := ordSameTransactionRelation079(childRecord, childBlock.Transactions[child.TxIndex], parent.ID)
		if state != receipt.State {
			return fmt.Errorf("parent replay %s: %s", state, reason)
		}
		return ordRequireSelected(hashAt, childBlock.Height, childBlock.Hash)
	}
	resolver := newSatlineResolver(backend)
	resolver.ctx = ctx
	resolver.cacheBlock(parentBlock)
	tx := parentBlock.Transactions[parent.TxIndex]
	placement, err := deriveOrdProvenancePlacement(ctx, tx, parent.Envelope, func(i int) (uint64, error) {
		in := tx.Inputs[i]
		return resolver.prevoutValue(in.PrevTxID, in.PrevVout, parentBlock.Height)
	})
	if err != nil {
		return err
	}
	if placement.State != "verified_reveal_output" && placement.State != "fee_to_coinbase" {
		return fmt.Errorf("parent placement unresolved: %s", placement.Note)
	}
	position, dest := placement.Position, tx
	if placement.State == "fee_to_coinbase" {
		outputs, err := sumOutputs(tx)
		if err != nil {
			return err
		}
		prior, err := resolver.priorBlockFees(parentBlock, parent.TxIndex)
		if err != nil {
			return err
		}
		position, err = addUint64(position-outputs, prior)
		if err == nil {
			position, err = addUint64(position, subsidyAtHeight(parentBlock.Height))
		}
		if err != nil {
			return err
		}
		dest = parentBlock.Transactions[0]
	}
	vout, offset, ok := mapStreamPosition(position, dest.Outputs)
	if !ok {
		return fmt.Errorf("parent placement is outside claimed outputs")
	}
	point := satlinePoint{TxID: dest.TxID, Vout: vout, Offset: offset, Height: parentBlock.Height, BlockHash: parentBlock.Hash, TxIndex: dest.Index}
	state, reason, _, _ := ordParentAtReveal(resolver, point, childBlock.Transactions[child.TxIndex], childBlock.Height, child.TxIndex, hashAt)
	if state != receipt.State {
		return fmt.Errorf("parent replay %s: %s", state, reason)
	}
	return nil
}

// This provider knows only positive transactions and spends included in the
// receipt. An omitted spender is unknown, never proof of unspentness.
type parcelReplayBackend079 struct {
	blocks          map[int64]blockView
	through         int64
	ctx             context.Context
	hashAt          func(int64) (string, error)
	readRaw         func(context.Context, int64, string) (*blockView, error)
	negative        *parcelReplayNegative079
	attestationRoot string
}

func (b *parcelReplayBackend079) ChainAuthority() chainAuthorityView { return chainAuthorityView{} }
func (b *parcelReplayBackend079) BlockByHeight(h int64) (blockView, error) {
	v, ok := b.blocks[h]
	if !ok {
		if b.readRaw == nil || b.hashAt == nil {
			return blockView{}, fmt.Errorf("receipt block %d missing", h)
		}
		hash, err := b.hashAt(h)
		if err != nil {
			return blockView{}, err
		}
		block, err := b.readRaw(b.ctx, h, hash)
		if err != nil || block == nil || block.Height != h || !strings.EqualFold(block.Hash, hash) || !integrityVerified(*block) {
			return blockView{}, fmt.Errorf("retained proof block %d unavailable", h)
		}
		return *block, nil
	}
	return v, nil
}
func (b *parcelReplayBackend079) Transaction(id string, before int64) (satlineTxEvidence, error) {
	var best *satlineTxEvidence
	for h, block := range b.blocks {
		if h > before {
			continue
		}
		for _, tx := range block.Transactions {
			if !strings.EqualFold(tx.TxID, id) || !transactionFitsHeightContext(h, tx.Coinbase, before) {
				continue
			}
			if best == nil || h > best.Height {
				v := satlineTxEvidence{Tx: tx, Height: h, BlockHash: block.Hash, VerificationState: block.VerificationState, Source: "detached Parcel receipt"}
				best = &v
			}
		}
	}
	if best == nil {
		return satlineTxEvidence{}, fmt.Errorf("receipt prevout transaction unavailable")
	}
	if replacement, ok := bip30ReplacementHeight(best.Height); ok && best.Tx.Coinbase && before >= replacement {
		return satlineTxEvidence{}, fmt.Errorf("historical duplicate coinbase incarnation is unresolved")
	}
	return *best, nil
}
func (b *parcelReplayBackend079) Spender(id string, vout uint32) (spenderLookupView, error) {
	var match *spenderLookupView
	for _, block := range b.blocks {
		for _, tx := range block.Transactions {
			for i, in := range tx.Inputs {
				if in.Coinbase || !strings.EqualFold(in.PrevTxID, id) || in.PrevVout != vout {
					continue
				}
				if match != nil {
					return spenderLookupView{}, fmt.Errorf("ambiguous receipt spender")
				}
				t := tx
				v := spenderLookupView{ConfirmedState: "confirmed_spent", Found: true, SpendingTxID: tx.TxID, SpendingVin: i, Height: block.Height, BlockHash: block.Hash, VerificationState: block.VerificationState, Transaction: &t}
				match = &v
			}
		}
	}
	if match == nil {
		return spenderLookupView{ConfirmedState: "unknown", Note: "No spending transaction in receipt"}, nil
	}
	return *match, nil
}

// An absent spend is meaningful only when every block from the source point
// through the child reveal is present and scanned. A sparse receipt is never
// converted into a negative result.
func (b *parcelReplayBackend079) SpenderPoint(point satlinePoint) (spenderLookupView, error) {
	if point.Height < 0 || b.through < point.Height {
		return spenderLookupView{ConfirmedState: "unknown", Note: "Receipt range unavailable"}, nil
	}
	heights := make([]int64, 0, len(b.blocks))
	for height := range b.blocks {
		if height >= point.Height && height <= b.through {
			heights = append(heights, height)
		}
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
	var match *spenderLookupView
	scan := func(height int64, block blockView) error {
		for _, tx := range block.Transactions {
			if height == point.Height && tx.Index <= point.TxIndex {
				continue
			}
			for vin, in := range tx.Inputs {
				if in.Coinbase || !strings.EqualFold(in.PrevTxID, point.TxID) || in.PrevVout != point.Vout {
					continue
				}
				if match != nil {
					return fmt.Errorf("ambiguous receipt spender")
				}
				copyTx := tx
				v := spenderLookupView{ConfirmedState: "confirmed_spent", Found: true, SpendingTxID: tx.TxID, SpendingVin: vin, Height: height, BlockHash: block.Hash, VerificationState: block.VerificationState, Transaction: &copyTx}
				match = &v
			}
		}
		return nil
	}
	for _, height := range heights {
		if err := scan(height, b.blocks[height]); err != nil {
			return spenderLookupView{}, err
		}
	}
	if match != nil {
		return *match, nil
	}
	span := b.through - point.Height + 1
	if int64(len(heights)) != span && b.negative != nil && b.negative.Through == b.through && b.negative.Point == point && b.attestationRoot != "" && b.readRaw == nil {
		if err := verifyParcelScanAttestation079(b.ctx, b.attestationRoot, b.negative, b.hashAt); err != nil {
			return spenderLookupView{}, err
		}
		hash, err := b.hashAt(b.through)
		if err != nil {
			return spenderLookupView{}, err
		}
		return spenderLookupView{ConfirmedState: "unspent_at_snapshot", VerificationState: "header_anchored", Snapshot: &chainSnapshot{Height: b.through, Hash: hash, Source: "local_scan_attestation"}, Note: "Locally evaluated no-spend interval; independent replay requires retained Raw blocks."}, nil
	}
	if int64(len(heights)) != span && (b.negative == nil || b.negative.Through != b.through || b.negative.Point != point || b.readRaw == nil) {
		return spenderLookupView{ConfirmedState: "unknown", Note: "Receipt does not contain every block in the required interval"}, nil
	}
	if int64(len(heights)) != span {
		for height := point.Height; height <= b.through; height++ {
			if _, present := b.blocks[height]; present {
				continue
			}
			block, err := b.BlockByHeight(height)
			if err != nil {
				return spenderLookupView{}, err
			}
			if err := scan(height, block); err != nil {
				return spenderLookupView{}, err
			}
			if match != nil {
				return *match, nil
			}
		}
	}
	last, err := b.BlockByHeight(b.through)
	if err != nil {
		return spenderLookupView{}, err
	}
	return spenderLookupView{ConfirmedState: "unspent_at_snapshot", VerificationState: "header_anchored", Snapshot: &chainSnapshot{Height: b.through, Hash: last.Hash, Source: "complete_detached_replay"}, Note: "Every block in the required interval was checked."}, nil
}

// The receipt is content addressed and written only after a successful replay.
// Blocks are shared by digest so repeated proofs do not duplicate body bytes.
func storeParcelReplay079(ctx context.Context, root, parent, child, parentBlocator, childBlocator, state string, blocks []blockView, hashAt func(int64) (string, error)) (string, error) {
	return storeParcelReplayWithSource079(ctx, root, parent, child, parentBlocator, childBlocator, state, blocks, hashAt, nil, nil)
}
func storeParcelReplayWithSource079(ctx context.Context, root, parent, child, parentBlocator, childBlocator, state string, blocks []blockView, hashAt func(int64) (string, error), negative *parcelReplayNegative079, read func(context.Context, int64, string) (*blockView, error)) (string, error) {
	if len(blocks) == 0 || len(blocks) > parcelReplayMaxBlocks079 {
		return "", fmt.Errorf("receipt block count outside budget")
	}
	receipt := parcelReplayReceipt079{Schema: 3, Ord: ordParentReference, ParentID: parent, ChildID: child, ParentBlocator: parentBlocator, ChildBlocator: childBlocator, State: state, Negative: negative, Blocks: []parcelReplayBlock079{}}
	raw := map[string][]byte{}
	for _, block := range blocks {
		if len(block.verifiedRaw) < 81 {
			return "", fmt.Errorf("serialized checked block unavailable")
		}
		sum := sha256.Sum256(block.verifiedRaw)
		hash := hex.EncodeToString(sum[:])
		raw[hash] = block.verifiedRaw
		receipt.Blocks = append(receipt.Blocks, parcelReplayBlock079{Height: block.Height, Hash: block.Hash, SHA256: hash})
	}
	sort.Slice(receipt.Blocks, func(i, j int) bool { return receipt.Blocks[i].Height < receipt.Blocks[j].Height })
	// The scan attestation is the committed local evaluation. Do not silently
	// repeat a long Raw scan at publication; an explicit source audit may pass
	// a reader to replay it independently later.
	if negative != nil {
		read = nil
	}
	if err := replayParcelParentWithSource079(ctx, receipt, raw, hashAt, read, root); err != nil {
		return "", err
	}
	for hash, body := range raw {
		path := parcelReplayBlockPath079(root, hash)
		if err := updateapply.CheckPath(path); err != nil {
			return "", err
		}
		if old, err := os.ReadFile(path); err == nil {
			sum := sha256.Sum256(old)
			if hex.EncodeToString(sum[:]) != hash {
				return "", fmt.Errorf("existing receipt block digest mismatch")
			}
			continue
		} else if !os.IsNotExist(err) {
			return "", err
		}
		if err := atomicWriteBytes(path, body); err != nil {
			return "", err
		}
	}
	hash := indexDigest(receipt)
	if err := writeBitmapChainJSON(ctx, parcelReplayReceiptPath079(root, hash), receipt); err != nil {
		return "", err
	}
	return hash, nil
}

// Expected IDs bind a detached receipt to the candidate being evaluated. A
// valid receipt for a different parent/child pair is not interchangeable.
func loadParcelReplay079(ctx context.Context, root, hash, expectedParent, expectedChild, expectedParentBlocator, expectedChildBlocator, expectedState string, hashAt func(int64) (string, error)) error {
	return loadParcelReplayWithSource079(ctx, root, hash, expectedParent, expectedChild, expectedParentBlocator, expectedChildBlocator, expectedState, hashAt, nil)
}
func loadParcelReplayWithSource079(ctx context.Context, root, hash, expectedParent, expectedChild, expectedParentBlocator, expectedChildBlocator, expectedState string, hashAt func(int64) (string, error), read func(context.Context, int64, string) (*blockView, error)) error {
	if !validHash(hash) {
		return fmt.Errorf("invalid Parcel receipt hash")
	}
	if !isInscriptionID(expectedParent) || !isInscriptionID(expectedChild) || !bitmapValidInscriptionBlocator(expectedParentBlocator) || !bitmapValidInscriptionBlocator(expectedChildBlocator) || (expectedState != "verified" && expectedState != "rejected") {
		return fmt.Errorf("invalid expected Parcel relationship")
	}
	var receipt parcelReplayReceipt079
	if err := readBitmapJSON(ctx, parcelReplayReceiptPath079(root, hash), &receipt); err != nil {
		return err
	}
	if indexDigest(receipt) != hash {
		return fmt.Errorf("Parcel receipt digest mismatch")
	}
	if !strings.EqualFold(receipt.ParentID, expectedParent) || !strings.EqualFold(receipt.ChildID, expectedChild) || receipt.ParentBlocator != expectedParentBlocator || receipt.ChildBlocator != expectedChildBlocator || receipt.State != expectedState {
		return fmt.Errorf("Parcel receipt relationship does not match candidate")
	}
	if receipt.Schema != 3 || len(receipt.Blocks) == 0 || len(receipt.Blocks) > parcelReplayMaxBlocks079 {
		return fmt.Errorf("invalid Parcel receipt block count")
	}
	raw := map[string][]byte{}
	total := int64(0)
	for _, block := range receipt.Blocks {
		if !validHash(block.SHA256) {
			return fmt.Errorf("invalid block digest")
		}
		path := parcelReplayBlockPath079(root, block.SHA256)
		if err := updateapply.CheckPath(path); err != nil {
			return err
		}
		stat, err := os.Stat(path)
		if err != nil {
			return err
		}
		total += stat.Size()
		if stat.Size() < 81 || total > parcelReplayMaxBytes079 {
			return fmt.Errorf("receipt blocks exceed bounded read budget")
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		raw[block.SHA256] = body
	}
	return replayParcelParentWithSource079(ctx, receipt, raw, hashAt, read, root)
}
