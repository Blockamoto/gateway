package main

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"strconv"
	"sync"
)

//go:embed recipes/builtins/headers.json
var builtinHeaderJSON []byte

//go:embed recipes/builtins/raw-blocks.json
var builtinRawBlockJSON []byte

//go:embed recipes/builtins/raw-block-bodies.json
var builtinRawBodyJSON []byte

var builtinSourceRetentionOnce [3]sync.Once
var builtinSourceRetentionPrograms [3]*recipeProgram
var builtinSourceRetentionErrors [3]error
var builtinSourceRetentionSemantics [3]string

func recipeSourceRetentionSlot(source string) int {
	switch source {
	case recipeHeaderSource:
		return 0
	case recipeBlockSource:
		return 1
	case recipeBlockBodySource:
		return 2
	}
	return -1
}
func builtinSourceRetentionBytes(source string) []byte {
	switch source {
	case recipeHeaderSource:
		return builtinHeaderJSON
	case recipeBlockSource:
		return builtinRawBlockJSON
	case recipeBlockBodySource:
		return builtinRawBodyJSON
	}
	return nil
}
func builtinSourceRetentionRecipe(source string) (*recipeProgram, string, error) {
	slot := recipeSourceRetentionSlot(source)
	if slot < 0 {
		return nil, "", fmt.Errorf("source retention contract unavailable")
	}
	builtinSourceRetentionOnce[slot].Do(func() {
		d, err := parseRecipe(builtinSourceRetentionBytes(source))
		if err == nil {
			builtinSourceRetentionPrograms[slot], err = compileRecipe(d)
		}
		builtinSourceRetentionErrors[slot] = err
		if err != nil {
			return
		}
		physical := "gateway-raw-body-segment-catalog-v3"
		if source == recipeHeaderSource {
			physical = "bitcoin-mainnet-selected-headers-80-byte-file-v1"
		}
		builtinSourceRetentionSemantics[slot] = indexDigest(struct {
			Source, Program, Physical string
			Verifier                  int
		}{source, builtinSourceRetentionPrograms[slot].semanticHash(), physical, blockVerifierVersion})
	})
	return builtinSourceRetentionPrograms[slot], builtinSourceRetentionSemantics[slot], builtinSourceRetentionErrors[slot]
}

var recipeSourceRetentionEquivalent = [3]string{"fdd3a9afb329f21925bf99f0ba93560d729d8528244d1789a3aee70bb80db2bb", "d8ec591a631997771fa2610ad01a119e514af2ab2a3b15019b29f09ee2a92f7a", "8bd115523fdc96ef079180deb7c5847ce2f7051d554682ca562fbc7673f2418e"}

func recipeSourceRetentionCheck(source, semantic string) error {
	slot := recipeSourceRetentionSlot(source)
	if slot < 0 || semantic != recipeSourceRetentionEquivalent[slot] {
		return fmt.Errorf("source JSON/codec changed; explicit migration required (%s actual %s)", source, semantic)
	}
	return nil
}

func recipeSourceScalarEqual(a, b recipeOrderedValue) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case "text":
		return a.Text == b.Text
	case "uint":
		return a.Uint == b.Uint
	case "bytes":
		return bytes.Equal(a.Bytes, b.Bytes)
	}
	return false
}

// A codec accepts exact field proposals from one source event. It carries no
// synchronization, chain-selection, retention consent or publication policy.
func recipeNativeSourceProposal(ctx context.Context, source string, event map[string]recipeOrderedValue) (recipeOrderedValue, error) {
	p, semantic, err := builtinSourceRetentionRecipe(source)
	if err != nil {
		return recipeOrderedValue{}, err
	}
	if err = recipeSourceRetentionCheck(source, semantic); err != nil {
		return recipeOrderedValue{}, err
	}
	table, key := "headers", strconv.FormatUint(event["height"].Uint, 10)
	names := []string{"height", "block", "previous_block", "merkle_root", "version", "time", "bits", "nonce", "raw_header"}
	if source != recipeHeaderSource {
		table = "blocks"
		names = []string{"height", "block", "raw_header", "raw_block", "body_sha256", "transaction_count", "serialized_bytes", "stripped_bytes", "weight"}
	}
	if source == recipeBlockBodySource {
		table, key = "bodies", event["block"].Text
		names = []string{"block", "raw_header", "raw_block", "body_sha256", "transaction_count", "serialized_bytes", "stripped_bytes", "weight"}
	}
	var proposal recipeOrderedValue
	count := 0
	err = p.evaluateOrderedSparseTypedEventsInto(ctx, func(emit func(map[string]recipeOrderedValue) error) error { return emit(event) }, func(gotTable, gotKey string, value recipeOrderedValue) error {
		count++
		if count != 1 || gotTable != table || gotKey != key || value.Kind != "object" || len(value.Object) != len(names)+1 || value.Object["ref"].Kind != "text" || value.Object["ref"].Text != key {
			return fmt.Errorf("source proposal differs from native identity/schema")
		}
		for _, name := range names {
			if !recipeSourceScalarEqual(value.Object[name], event[name]) {
				return fmt.Errorf("source proposal field %s differs from admitted bytes", name)
			}
		}
		proposal = value
		return nil
	})
	if err != nil {
		return proposal, err
	}
	if count != 1 {
		return proposal, fmt.Errorf("native source retention needs one complete proposal")
	}
	return proposal, nil
}

func recipeNativeHeaderBytes(ctx context.Context, height int64, header []byte) ([]byte, error) {
	digest := hash256(header)
	event, err := recipeHeaderSourceEvent(ctx, height, reverseHex(digest[:]), header)
	if err != nil {
		return nil, err
	}
	proposal, err := recipeNativeSourceProposal(ctx, recipeHeaderSource, event)
	if err != nil {
		return nil, err
	}
	return proposal.Object["raw_header"].Bytes, nil
}

type recipeNativeRawProposal struct {
	Raw          []byte
	Transactions uint64
}

func recipeNativeRawBytes(ctx context.Context, block blockView) (recipeNativeRawProposal, error) {
	source := recipeBlockSource
	var event map[string]recipeOrderedValue
	var err error
	if block.Height < 0 {
		source = recipeBlockBodySource
		event, err = recipeBlockBodySourceEvent(ctx, block)
	} else {
		event, err = recipeBlockSourceEvent(ctx, block)
	}
	if err != nil {
		return recipeNativeRawProposal{}, err
	}
	proposal, err := recipeNativeSourceProposal(ctx, source, event)
	if err != nil {
		return recipeNativeRawProposal{}, err
	}
	return recipeNativeRawProposal{Raw: proposal.Object["raw_block"].Bytes, Transactions: proposal.Object["transaction_count"].Uint}, nil
}
