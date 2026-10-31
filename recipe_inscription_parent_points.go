package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

const recipeInscriptionParentLookupProfile = "gateway-selected-inscription-parent-replay-point-v1"

func recipeInscriptionParentLookupBindings() []recipeSourceLookup {
	result := []recipeSourceLookup{}
	for _, provider := range []string{"retained-raw", "tx-locators", "txio-links"} {
		lookup := recipeSourceLookup{Name: "parent_" + strings.ReplaceAll(provider, "-", "_"), Source: "bitcoin.inscription_parent", Provider: provider, Key: "inscription_pair", Type: "inscription_parent"}
		lookup.Revision = indexDigest(struct{ Profile, Source, Provider, Key, Type string }{recipeInscriptionParentLookupProfile, lookup.Source, lookup.Provider, lookup.Key, lookup.Type})
		lookup.SemanticHash = indexDigest(struct {
			Revision, Network, Ord, Parser, Authority string
			Verifier                                  int
		}{lookup.Revision, "bitcoin-mainnet", ordParentReference, inscriptionParserProfile, "selected-checked-replay-owned-receipt-blocks-and-local-attestations-v1", blockVerifierVersion})
		result = append(result, lookup)
	}
	return result
}

// Pair keys put the current child first, followed by a semicolon and parent.
// Positional inscription references contain no semicolon.
func recipeInscriptionParentKey(key string, through int64) (child, parent bodCoordinate, ok bool) {
	parts := strings.Split(key, ";")
	if len(parts) != 2 {
		return
	}
	child, ok = recipeInscriptionPointKey(parts[0])
	if !ok || child.Height != through {
		ok = false
		return
	}
	parent, ok = recipeInscriptionPointKey(parts[1])
	if !ok || parent.Height > through {
		ok = false
		return
	}
	return
}

type recipeParentPointResult struct {
	Value    recipeOrderedValue
	Evidence []recipeTablePointEvidence
	Blobs    []recipeSourceBlobInput
}

func recipeParentPointCacheKey(lookup recipeSourceLookup, key string) string {
	return indexDigest(struct {
		Lookup recipeSourceLookup
		Key    string
	}{lookup, key})
}

func (r *recipeTablePointLookups) parentSource(ctx context.Context, provider string) (inscriptionParentReplaySource, error) {
	if source, ok := r.parentSources[provider]; ok {
		return source, nil
	}
	if r.a == nil {
		return inscriptionParentReplaySource{}, fmt.Errorf("%w: selected relationship authority unavailable", errRecipeDependencyUnavailable)
	}
	readCtx := context.WithValue(context.WithValue(ctx, parcelEvidenceReadKey079{}, true), indexLocatorReadOnlyKey{}, true)
	source := inscriptionParentReplaySource{app: r.a, root: r.a.dataDir, hashAt: r.a.addressSelectedHashes(readCtx, r.a.currentChainAuthority())}
	timeline := r.a.parcelEvidenceTimeline079()
	var err error
	switch provider {
	case "retained-raw":
		source.spender, source.transaction, source.blockAt, err = r.a.newParcelRawRoute079(readCtx, timeline, r.block.Height, nil)
	case "tx-locators":
		source.spender, source.transaction, source.blockAt, err = r.a.newParcelLocatorRoute079(readCtx, timeline, r.block.Height, nil)
	case "txio-links":
		var prepare func(context.Context) error
		prepare, source.spender, source.transaction, source.blockAt, err = r.a.newParcelTXIORoute079(readCtx, timeline, r.block.Height, nil)
		if err == nil && prepare != nil {
			err = prepare(readCtx)
		}
	default:
		err = fmt.Errorf("explicit relationship proof provider required")
	}
	if err != nil {
		if ctx.Err() != nil {
			return source, ctx.Err()
		}
		return source, fmt.Errorf("%w: relationship proof prefix unavailable (%v)", errRecipeDependencyUnavailable, err)
	}
	if r.parentSources == nil {
		r.parentSources = map[string]inscriptionParentReplaySource{}
	}
	r.parentSources[provider] = source
	return source, nil
}

type recipeParentInventoryPoint struct {
	Height    int64
	Hash      string
	Inventory *inscriptionBlockResult
	Positions map[recipeIdentityPosition]int
}

func (r *recipeTablePointLookups) parentOccurrence(ctx context.Context, block blockView, position bodCoordinate) (inscriptionOccurrence, bool, error) {
	if err := ctx.Err(); err != nil {
		return inscriptionOccurrence{}, false, err
	}
	cached := r.parentPrevious
	if block.Height == r.block.Height {
		cached = r.parentCurrent
	}
	if cached != nil && cached.Height == block.Height && cached.Hash == block.Hash {
		index, have := cached.Positions[recipeIdentityPosition{position.TxIndex, uint32(position.InscriptionIndex)}]
		if !have {
			return inscriptionOccurrence{}, false, nil
		}
		return cached.Inventory.Occurrences[index], true, nil
	}
	parsed, err := extractInscriptionOccurrences(block)
	if err != nil {
		return inscriptionOccurrence{}, false, err
	}
	admitted, err := recipeAdmitInscriptionSource(ctx, block, &parsed)
	if err != nil {
		return inscriptionOccurrence{}, false, err
	}
	point := &recipeParentInventoryPoint{Height: block.Height, Hash: block.Hash, Inventory: admitted.inventory, Positions: map[recipeIdentityPosition]int{}}
	for index, o := range admitted.inventory.Occurrences {
		if err = ctx.Err(); err != nil {
			return o, false, err
		}
		point.Positions[recipeIdentityPosition{o.TxIndex, o.Index}] = index
	}
	if block.Height == r.block.Height {
		r.parentCurrent = point
	} else {
		r.parentPrevious = point
	}
	index, have := point.Positions[recipeIdentityPosition{position.TxIndex, uint32(position.InscriptionIndex)}]
	if !have {
		return inscriptionOccurrence{}, false, nil
	}
	return point.Inventory.Occurrences[index], true, nil
}

func (r *recipeTablePointLookups) readParent(ctx context.Context, lookup recipeSourceLookup, key string) (recipeOrderedValue, error) {
	childPosition, parentPosition, ok := recipeInscriptionParentKey(key, r.block.Height)
	if !ok {
		return recipeOrderedValue{}, fmt.Errorf("relationship source requires current-child;nonfuture-parent positional references")
	}
	cacheKey := recipeParentPointCacheKey(lookup, key)
	if result, have := r.parentFacts[cacheKey]; have {
		return result.Value, nil
	}
	source, err := r.parentSource(ctx, lookup.Provider)
	if err != nil {
		return recipeOrderedValue{}, err
	}
	anchors := map[int64]string{}
	hashAt := source.hashAt
	source.hashAt = func(height int64) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		hash, err := hashAt(height)
		if err != nil {
			return "", err
		}
		if !validHash(hash) || height < 0 || height > r.block.Height {
			return "", fmt.Errorf("relationship source anchor outside admitted range")
		}
		if old := r.anchors[height]; old != "" && old != hash {
			return "", fmt.Errorf("relationship source anchor changed")
		}
		anchors[height], r.anchors[height] = hash, hash
		return hash, nil
	}
	for _, height := range []int64{childPosition.Height, parentPosition.Height} {
		if _, err = source.hashAt(height); err != nil {
			return recipeOrderedValue{}, err
		}
	}
	if anchors[r.block.Height] != r.block.Hash {
		return recipeOrderedValue{}, fmt.Errorf("relationship child is on a replaced branch")
	}
	parentBlock := r.block
	if parentPosition.Height != r.block.Height {
		parentBlock, err = source.blockAt(ctx, parentPosition.Height)
		if err != nil {
			if ctx.Err() != nil {
				return recipeOrderedValue{}, ctx.Err()
			}
			return recipeOrderedValue{}, fmt.Errorf("%w: parent reveal bytes unavailable (%v)", errRecipeDependencyUnavailable, err)
		}
		if parentBlock.Hash != anchors[parentPosition.Height] || parentBlock.Height != parentPosition.Height {
			return recipeOrderedValue{}, fmt.Errorf("relationship parent source changed")
		}
	}
	child, haveChild, err := r.parentOccurrence(ctx, r.block, childPosition)
	if err != nil {
		return recipeOrderedValue{}, err
	}
	parent, haveParent, err := r.parentOccurrence(ctx, parentBlock, parentPosition)
	if err != nil {
		return recipeOrderedValue{}, err
	}
	result := recipeParentPointResult{Value: recipeOrderedValue{Kind: "missing"}}
	declared := false
	for _, id := range child.Envelope.Parents {
		if id == parent.ID {
			declared = true
			break
		}
	}
	if haveChild && haveParent && declared {
		parts := strings.Split(key, ";")
		proof, e := replayInscriptionParent(ctx, source, r.block, child, inscriptionParentReplayReference{ID: parent.ID, Ref: parts[1], BlockHash: parentBlock.Hash, Height: parentBlock.Height, TxIndex: parent.TxIndex, Index: parent.Index})
		if e != nil {
			if ctx.Err() != nil {
				return recipeOrderedValue{}, ctx.Err()
			}
			return recipeOrderedValue{}, fmt.Errorf("%w: relationship replay unavailable (%v)", errRecipeDependencyUnavailable, e)
		}
		raw, e := json.Marshal(proof)
		if e != nil {
			return recipeOrderedValue{}, e
		}
		result.Value = recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"child_ref": rovText(parts[0]), "parent_ref": rovText(parts[1]), "state": rovText(proof.RelationState), "proof": {Kind: "bytes", Bytes: raw}}}
		var receipt parcelReplayReceipt079
		path := parcelReplayReceiptPath079(source.root, proof.EvidenceHash)
		if e = readBitmapJSON(ctx, path, &receipt); e != nil {
			return recipeOrderedValue{}, e
		}
		if indexDigest(receipt) != proof.EvidenceHash {
			return recipeOrderedValue{}, fmt.Errorf("relationship receipt changed")
		}
		addBlob := func(role, path string) error {
			file, e := os.Open(path)
			if e != nil {
				return e
			}
			defer file.Close()
			info, e := file.Stat()
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > parcelReplayMaxBytes079 {
				return fmt.Errorf("relationship evidence extent invalid")
			}
			hash, e := recipeSourceFileDigest(ctx, file)
			if e != nil {
				return e
			}
			result.Blobs = append(result.Blobs, recipeSourceBlobInput{Ref: recipeSourceBlob{Role: role, Hash: hash, Bytes: info.Size()}, Path: path})
			return nil
		}
		if e = addBlob("receipt", path); e != nil {
			return recipeOrderedValue{}, e
		}
		for _, block := range receipt.Blocks {
			if _, e = source.hashAt(block.Height); e != nil {
				return recipeOrderedValue{}, e
			}
			if anchors[block.Height] != block.Hash {
				return recipeOrderedValue{}, fmt.Errorf("relationship receipt left selected branch")
			}
			if e = addBlob("block", parcelReplayBlockPath079(source.root, block.SHA256)); e != nil {
				return recipeOrderedValue{}, e
			}
			if result.Blobs[len(result.Blobs)-1].Ref.Hash != block.SHA256 {
				return recipeOrderedValue{}, fmt.Errorf("relationship block digest changed")
			}
		}
		if receipt.Negative != nil {
			if _, e = source.hashAt(receipt.Negative.Through); e != nil {
				return recipeOrderedValue{}, e
			}
			if e = addBlob("attestation", parcelScanAttestationPath079(source.root, receipt.Negative.Attestation)); e != nil {
				return recipeOrderedValue{}, e
			}
		}
	}
	heights := make([]int64, 0, len(anchors))
	for height := range anchors {
		heights = append(heights, height)
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
	for _, height := range heights {
		result.Evidence = append(result.Evidence, recipeTablePointEvidence{Height: height, BlockHash: anchors[height]})
	}
	if r.parentFacts == nil {
		r.parentFacts = map[string]recipeParentPointResult{}
	}
	r.parentFacts[cacheKey] = result
	return result.Value, nil
}

func recipeCheckParentPointFact(f recipeImportedFact) error {
	child, parent, ok := recipeInscriptionParentKey(f.Key, f.Snapshot.Height)
	if !ok || len(f.Evidence) == 0 || len(f.Evidence) > parcelReplayMaxBlocks079+2 {
		return fmt.Errorf("owned relationship key/anchors changed")
	}
	anchors := map[int64]string{}
	last := int64(-1)
	for _, e := range f.Evidence {
		if e.Height <= last || e.Height > f.Snapshot.Height || !validHash(e.BlockHash) {
			return fmt.Errorf("owned relationship source anchors changed")
		}
		last = e.Height
		anchors[e.Height] = e.BlockHash
	}
	if anchors[child.Height] != f.Snapshot.BlockHash || anchors[parent.Height] == "" {
		return fmt.Errorf("owned relationship endpoints changed")
	}
	if f.Value.Kind == "missing" {
		if len(f.Blobs) != 0 {
			return fmt.Errorf("absent relationship has proof blobs")
		}
		return nil
	}
	v := f.Value.Object
	parts := strings.Split(f.Key, ";")
	if f.Value.Kind != "object" || len(v) != 4 || v["child_ref"].Kind != "text" || v["child_ref"].Text != parts[0] || v["parent_ref"].Kind != "text" || v["parent_ref"].Text != parts[1] || v["state"].Kind != "text" || (v["state"].Text != "verified" && v["state"].Text != "rejected") || v["proof"].Kind != "bytes" {
		return fmt.Errorf("owned relationship value changed")
	}
	var proof bitmapParentProof
	if json.Unmarshal(v["proof"].Bytes, &proof) != nil || !proof.Verified || proof.ReplayVersion != 3 || proof.RelationState != v["state"].Text || proof.ChildBlocator != parts[0] || proof.ParentBlocator != parts[1] || proof.ChildBlockHash != f.Snapshot.BlockHash || !isInscriptionID(proof.ParentID) || !isInscriptionID(proof.ChildID) || !validHash(proof.EvidenceHash) {
		return fmt.Errorf("owned relationship proof changed")
	}
	if len(f.Blobs) < 2 || len(f.Blobs) > parcelReplayMaxBlocks079+2 {
		return fmt.Errorf("owned relationship proof evidence missing")
	}
	return nil
}
