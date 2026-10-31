package main

import (
	"context"
	"fmt"
	"sort"
)

const recipeRevealTablesSourceProfile = "gateway-typed-owned-reveal-events-v1"

func recipeTablesSourceProfileFor(source string) string {
	if source == "bitcoin.reveal" {
		return indexDigest(struct {
			Source, Parser, Reference string
			Verifier                  int
		}{recipeRevealTablesSourceProfile, inscriptionParserProfile, inscriptionReferenceCommit, blockVerifierVersion})
	}
	return recipeTablesSourceProfile
}

func recipeRevealTablesFields() []recipeFieldCapability {
	result := []recipeFieldCapability{}
	for _, group := range []struct {
		names []string
		kind  string
	}{
		{[]string{"kind", "block", "txid", "ref", "content_hash"}, "text"},
		{[]string{"height", "tx_index", "reveal_index", "input_index", "parent_count"}, "uint"},
		{[]string{"body", "content_type"}, "bytes"},
		{[]string{"has_body", "has_content_type", "incomplete_fields", "duplicate_fields"}, "bool"},
	} {
		for _, name := range group.names {
			result = append(result, recipeFieldCapability{Path: []string{name}, Type: recipeTypeSpec{Types: []string{group.kind}}})
		}
	}
	return result
}

func recipeTablesFieldsFor(source string) []recipeFieldCapability {
	if source == "bitcoin.reveal" {
		return recipeRevealTablesFields()
	}
	return recipeTablesFields()
}

// A prefix consumes all authenticated occurrences in source order, including
// empty evaluated blocks. Bodies and MIME declarations are exact own-envelope
// bytes; no delegate resolution, decoding or claim policy happens here.
func recipeRevealTablesWalk(ctx context.Context, b blockView, sink func(map[string]recipeOrderedValue) error) error {
	if ctx == nil || sink == nil {
		return fmt.Errorf("owned reveal source and sink required")
	}
	var source *recipeOrderedOwnedSource
	if p := runPrepared(ctx); p != nil && p.block.Height == b.Height && p.block.Hash == b.Hash {
		if p.orderedErr != nil {
			return p.orderedErr
		}
		source = p.orderedSource
	}
	var err error
	if source == nil {
		source, err = recipeOrderedSourceFromBlock(ctx, b)
		if err != nil {
			return err
		}
	}
	inventory := source.block.inscriptionExtraction
	if inventory == nil {
		inventory = b.inscriptionExtraction
	}
	if inventory == nil {
		extraction, e := extractInscriptionOccurrences(source.block)
		if e != nil {
			return e
		}
		inventory = &extraction
	}
	admitted, err := recipeAdmitInscriptionSource(ctx, source.block, inventory)
	if err != nil {
		return err
	}
	ordered := append([]inscriptionOccurrence(nil), admitted.inventory.Occurrences...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].TxIndex != ordered[j].TxIndex {
			return ordered[i].TxIndex < ordered[j].TxIndex
		}
		return ordered[i].Index < ordered[j].Index
	})
	event := map[string]recipeOrderedValue{"kind": rovText("reveal"), "block": rovText(b.Hash), "height": rovUint(uint64(b.Height))}
	for _, o := range ordered {
		if err = ctx.Err(); err != nil {
			return err
		}
		if o.TxIndex < 0 || o.InputIndex < 0 || o.TxIndex >= len(source.block.Transactions) || source.block.Transactions[o.TxIndex].TxID != o.TxID {
			return fmt.Errorf("reveal coordinate differs from owned transaction inventory")
		}
		event["txid"], event["ref"], event["content_hash"] = rovText(o.TxID), rovText(inscriptionCoordinate(uint32(o.TxIndex), o.Index, b.Height)), rovText(o.ContentSHA256)
		event["tx_index"], event["reveal_index"], event["input_index"], event["parent_count"] = rovUint(uint64(o.TxIndex)), rovUint(uint64(o.Index)), rovUint(uint64(o.InputIndex)), rovUint(uint64(len(o.Envelope.Parents)))
		event["body"], event["content_type"] = rovBytes(o.Body), rovBytes([]byte(o.Envelope.ContentType))
		event["has_body"], event["has_content_type"], event["incomplete_fields"], event["duplicate_fields"] = rovBool(o.Envelope.HasBody), rovBool(o.HasContentType), rovBool(o.Envelope.Incomplete), rovBool(o.Envelope.Duplicate)
		if err = sink(event); err != nil {
			return err
		}
	}
	return ctx.Err()
}
