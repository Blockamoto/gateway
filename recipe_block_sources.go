package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

const recipeHeaderSource = "bitcoin.header"
const recipeBlockSource = "bitcoin.block"
const recipeHeaderSourceProfile = "gateway-owned-selected-header-events-v1"
const recipeBlockSourceProfile = "gateway-owned-verified-block-events-v1"

func recipeWholeBlockSource(source string) bool {
	return source == recipeHeaderSource || source == recipeBlockSource
}
func recipeHeaderSourceFromBlock(ctx context.Context, block blockView) (map[string]recipeOrderedValue, error) {
	header := block.verifiedHeader
	if header == nil {
		raw, _, err := exactVerifiedBlockPayload(block)
		if err != nil {
			return nil, err
		}
		header = raw[:80]
	}
	return recipeHeaderSourceEvent(ctx, block.Height, block.Hash, header)
}
func (a *app) recipeHeaderRunBlock(ctx context.Context, height int64) (blockView, error) {
	if err := ctx.Err(); err != nil {
		return blockView{}, err
	}
	header, err := a.readSelectedHeader(height)
	if err != nil {
		return blockView{}, err
	}
	digest := hash256(header)
	return blockView{Height: height, Hash: reverseHex(digest[:]), PreviousBlockHash: reverseHex(header[4:36]), verifiedHeader: append([]byte(nil), header...), VerificationState: "selected_header_only", Verification: verificationView{HeaderChainMatch: true}}, nil
}

func (p *recipeProgram) evaluateBlockSourceInto(ctx context.Context, block blockView, sink func(recipeRowProposal) error) error {
	if p == nil || !recipeWholeBlockSource(p.definition.Source) || block.Height < p.definition.Origin {
		return fmt.Errorf("invalid complete-source projection")
	}
	var event map[string]recipeOrderedValue
	var err error
	if p.definition.Source == recipeHeaderSource {
		event, err = recipeHeaderSourceFromBlock(ctx, block)
	} else {
		event, err = recipeBlockSourceEvent(ctx, block)
	}
	if err != nil {
		return err
	}
	return p.evaluateOrderedSparseEventsInto(ctx, func(emit func(map[string]recipeOrderedValue) error) error { return emit(event) }, sink)
}
func recipeBlockSourceFields(source string) []recipeFieldCapability {
	if !recipeWholeBlockSource(source) {
		return nil
	}
	fields := []recipeFieldCapability{}
	add := func(kind string, names ...string) {
		for _, name := range names {
			fields = append(fields, recipeFieldCapability{Path: []string{name}, Type: recipeTypeSpec{Types: []string{kind}}})
		}
	}
	add("text", "kind", "block", "previous_block", "merkle_root")
	add("uint", "height", "version", "time", "bits", "nonce")
	add("bytes", "raw_header")
	if source == recipeBlockSource {
		add("text", "body_sha256")
		add("uint", "transaction_count", "serialized_bytes", "stripped_bytes", "weight")
		add("bytes", "raw_block")
	}
	return fields
}

// These adapters establish byte identity, not selected-chain authority. Header
// admission, consensus-context checks and publication fences belong to callers.
func recipeHeaderSourceEvent(ctx context.Context, height int64, hash string, header []byte) (map[string]recipeOrderedValue, error) {
	if ctx == nil {
		return nil, fmt.Errorf("header source context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if height < 0 || !validHash(hash) || len(header) != 80 {
		return nil, fmt.Errorf("header source identity/extent invalid")
	}
	digest := hash256(header)
	if reverseHex(digest[:]) != hash {
		return nil, fmt.Errorf("header source bytes differ from anchor")
	}
	return map[string]recipeOrderedValue{"kind": rovText("header"), "height": rovUint(uint64(height)), "block": rovText(hash), "previous_block": rovText(reverseHex(header[4:36])), "merkle_root": rovText(reverseHex(header[36:68])), "version": rovUint(uint64(binary.LittleEndian.Uint32(header[:4]))), "time": rovUint(uint64(binary.LittleEndian.Uint32(header[68:72]))), "bits": rovUint(uint64(binary.LittleEndian.Uint32(header[72:76]))), "nonce": rovUint(uint64(binary.LittleEndian.Uint32(header[76:80]))), "raw_header": rovBytes(header)}, nil
}

func recipeBlockSourceEvent(ctx context.Context, block blockView) (map[string]recipeOrderedValue, error) {
	if prepared := runPrepared(ctx); prepared != nil && prepared.block.Height == block.Height && prepared.block.Hash == block.Hash {
		if prepared.orderedErr != nil {
			return nil, prepared.orderedErr
		}
		if prepared.orderedSource != nil {
			return recipeOwnedBlockSourceEvent(ctx, prepared.orderedSource)
		}
	}
	owned, err := recipeOrderedSourceFromBlock(ctx, block)
	if err != nil {
		return nil, err
	}
	return recipeOwnedBlockSourceEvent(ctx, owned)
}
func recipeOwnedBlockSourceEvent(ctx context.Context, owned *recipeOrderedOwnedSource) (map[string]recipeOrderedValue, error) {
	if owned == nil || len(owned.block.verifiedRaw) < 81 {
		return nil, fmt.Errorf("owned complete block source required")
	}
	block := owned.block
	event, err := recipeHeaderSourceEvent(ctx, block.Height, block.Hash, block.verifiedRaw[:80])
	if err != nil {
		return nil, err
	}
	if !integrityVerified(block) || block.TransactionCount != uint64(len(block.Transactions)) || block.TransactionCount == 0 || block.StrippedBytes < 0 || block.Weight < 0 {
		return nil, fmt.Errorf("owned block inventory differs from verified bytes")
	}
	proof := block.verifiedRawProof
	if proof == nil {
		return nil, fmt.Errorf("owned block byte receipt required")
	}
	event["kind"] = rovText("block")
	event["raw_block"] = recipeOrderedValue{Kind: "bytes", Bytes: block.verifiedRaw}
	event["body_sha256"] = rovText(hex.EncodeToString(proof.SHA[:]))
	event["transaction_count"] = rovUint(block.TransactionCount)
	event["serialized_bytes"] = rovUint(uint64(len(block.verifiedRaw)))
	event["stripped_bytes"] = rovUint(uint64(block.StrippedBytes))
	event["weight"] = rovUint(uint64(block.Weight))
	return event, nil
}
