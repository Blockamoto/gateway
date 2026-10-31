package main

import (
	"context"
	"fmt"
	"strconv"
)

type recipeTablePointEvidence struct {
	Height    int64
	BlockHash string
}
type recipeIdentityPosition struct {
	TxIndex int
	Index   uint32
}

func recipeInscriptionPointKey(key string) (bodCoordinate, bool) {
	p, ok := parseBODCoordinate(key)
	return p, ok && p.Kind == coordInscription && p.TxID == "" && p.Height >= 0 && p.TxIndex >= 0 && uint64(p.TxIndex) <= uint64(^uint32(0)) && p.InscriptionIndex >= 0 && uint64(p.InscriptionIndex) <= uint64(^uint32(0)) && key == inscriptionCoordinate(uint32(p.TxIndex), uint32(p.InscriptionIndex), p.Height)
}

func (r *recipeTablePointLookups) readIdentity(ctx context.Context, key string) (recipeOrderedValue, error) {
	p, ok := recipeInscriptionPointKey(key)
	if !ok || p.Height > r.block.Height {
		return recipeOrderedValue{}, fmt.Errorf("identity source requires a canonical nonfuture inscription coordinate")
	}
	if value, ok := r.identities[key]; ok {
		return value, nil
	}
	inventory := r.lastInventory
	if inventory == nil || inventory.BlockHeight != p.Height || inventory.BlockHash != r.anchors[p.Height] {
		block, err := r.loadBlock(ctx, p.Height)
		if err != nil {
			return recipeOrderedValue{}, err
		}
		inventory = block.inscriptionExtraction
		if inventory == nil {
			parsed, err := extractInscriptionOccurrences(block)
			if err != nil {
				return recipeOrderedValue{}, err
			}
			inventory = &parsed
		}
		admitted, err := recipeAdmitInscriptionSource(ctx, block, inventory)
		if err != nil {
			return recipeOrderedValue{}, err
		}
		inventory = admitted.inventory
		positions := map[recipeIdentityPosition]int{}
		for index, o := range inventory.Occurrences {
			if err := ctx.Err(); err != nil {
				return recipeOrderedValue{}, err
			}
			positions[recipeIdentityPosition{o.TxIndex, o.Index}] = index
		}
		r.lastPositions, r.lastInventory = positions, inventory
	}
	value := recipeOrderedValue{Kind: "missing"}
	if index, found := r.lastPositions[recipeIdentityPosition{p.TxIndex, uint32(p.InscriptionIndex)}]; found {
		o := inventory.Occurrences[index]
		if !validHash(o.TxID) || o.ID != o.TxID+"i"+strconv.FormatUint(uint64(o.Index), 10) {
			return recipeOrderedValue{}, fmt.Errorf("admitted inscription identity differs from source")
		}
		value = recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"ref": rovText(key), "id": rovText(o.ID), "txid": rovText(o.TxID), "block": rovText(inventory.BlockHash), "height": rovUint(uint64(p.Height)), "tx_index": rovUint(uint64(p.TxIndex)), "reveal_index": rovUint(uint64(p.InscriptionIndex))}}
	}
	r.identities[key] = value
	return value, nil
}

func recipeCheckIdentityPointFact(f recipeImportedFact) error {
	p, ok := recipeInscriptionPointKey(f.Key)
	if !ok || p.Height > f.Snapshot.Height || len(f.Evidence) != 1 || f.Evidence[0].Height != p.Height || !validHash(f.Evidence[0].BlockHash) || p.Height == f.Snapshot.Height && f.Evidence[0].BlockHash != f.Snapshot.BlockHash {
		return fmt.Errorf("owned identity source anchor changed")
	}
	v := f.Value
	if v.Kind == "missing" {
		return nil
	}
	if v.Kind != "object" || len(v.Object) != 7 {
		return fmt.Errorf("owned identity shape changed")
	}
	for _, name := range []string{"ref", "id", "txid", "block"} {
		if v.Object[name].Kind != "text" {
			return fmt.Errorf("owned identity text changed")
		}
	}
	for _, name := range []string{"height", "tx_index", "reveal_index"} {
		if v.Object[name].Kind != "uint" {
			return fmt.Errorf("owned identity coordinate changed")
		}
	}
	if v.Object["ref"].Text != f.Key || !validHash(v.Object["txid"].Text) || v.Object["id"].Text != v.Object["txid"].Text+"i"+strconv.Itoa(p.InscriptionIndex) || v.Object["block"].Text != f.Evidence[0].BlockHash || v.Object["height"].Uint != uint64(p.Height) || v.Object["tx_index"].Uint != uint64(p.TxIndex) || v.Object["reveal_index"].Uint != uint64(p.InscriptionIndex) {
		return fmt.Errorf("owned identity differs from its coordinate/source")
	}
	return nil
}

func (r *recipeTablePointLookups) adoptEvidence(ctx context.Context, f recipeImportedFact) error {
	evidence := f.Evidence
	if f.Lookup.Source == "bitcoin.block_context" {
		evidence = []recipeTablePointEvidence{{Height: int64(f.Value.Object["height"].Uint), BlockHash: f.Value.Object["block"].Text}}
	}
	for _, anchor := range evidence {
		if r.a == nil {
			return fmt.Errorf("selected point source authority unavailable")
		}
		header, err := r.a.readSelectedHeader(anchor.Height)
		if err != nil {
			return err
		}
		digest := hash256(header)
		if reverseHex(digest[:]) != anchor.BlockHash {
			return fmt.Errorf("owned point source is on a replaced selected branch")
		}
		if prior := r.anchors[anchor.Height]; prior != "" && prior != anchor.BlockHash {
			return fmt.Errorf("conflicting point source anchors")
		}
		r.anchors[anchor.Height] = anchor.BlockHash
	}
	return ctx.Err()
}
