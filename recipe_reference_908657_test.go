package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// Optional bounded mainnet reference fixture. The block is not redistributed:
// it contains third-party on-chain inscription content with unknown licensing.
// Download raw bytes from
// https://mempool.space/api/block/00000000000000000001d253a31be91698f02d4851078e3cd5b4e29048219d26/raw
// and set GATEWAY_RECIPE_REFERENCE_BLOCK to the local file. Discovery reference:
// https://ordinals.com/inscription/466756833f52c39fc5d4ec239180be003d85caf33f86971a6bec9082c5a478aai0
// The Ord explorer is a discovery aid only; no API JSON is used as proof.
func TestRecipeReferenceMetadata908657(t *testing.T) {
	path := os.Getenv("GATEWAY_RECIPE_REFERENCE_BLOCK")
	if path == "" {
		t.Skip("set GATEWAY_RECIPE_REFERENCE_BLOCK to the pinned raw block")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1569487 {
		t.Fatalf("unexpected raw block length %d", len(raw))
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != "be1ff6065182bd4e96c73f74ca0d61d23a32c599c555c33d4ea3252ecf19fb4d" {
		t.Fatal("raw block digest mismatch")
	}
	const blockHash = "00000000000000000001d253a31be91698f02d4851078e3cd5b4e29048219d26"
	// Do not supply this block's own header as a locally authenticated header.
	// The verifier checks hash, proof of work, Merkle and witness commitments;
	// this standalone fixture does not establish selected-chain membership.
	block, err := parseBlockDetailed(908657, blockHash, nil, raw, "pinned reference block", false)
	if err != nil || !integrityVerified(block) || !block.Verification.WitnessCommitment || block.Verification.HeaderChainMatch {
		t.Fatalf("block integrity: %+v, %v", block.Verification, err)
	}
	result, err := extractInscriptionOccurrences(block)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateCompleteInscriptionInventory(block, &result); err != nil {
		t.Fatal(err)
	}
	const target = "466756833f52c39fc5d4ec239180be003d85caf33f86971a6bec9082c5a478aai0"
	const expectedValue = "b1d57d7e45e8bbe30107bb082a374db5a644be0be6b06ab3c604a36b15eaf358"
	found := false
	for _, occurrence := range result.Occurrences {
		if occurrence.ID != target {
			continue
		}
		t.Logf("occurrence: tx=%d index=%d metaprotocol=%v metadata=%s", occurrence.TxIndex, occurrence.Index, occurrence.Envelope.Fields["07"], occurrence.Envelope.Metadata)
		if len(occurrence.Envelope.Fields["07"]) != 1 || occurrence.Envelope.Fields["07"][0] != "6c616269746275" {
			t.Fatal("metaprotocol differs")
		}
		if len(occurrence.Envelope.Fields["05"]) == 0 {
			t.Fatal("metadata tag missing")
		}
		metadata, err := hex.DecodeString(occurrence.Envelope.Metadata)
		if err != nil {
			t.Fatal(err)
		}
		value, err := recipeCBORDecode(metadata)
		if err != nil {
			t.Fatalf("metadata CBOR: %v", err)
		}
		item, status := recipeCBORLookupKey(value, recipeCBORValue{Kind: recipeCBORText, Text: "labitbu"})
		if status != recipeCBORFound {
			t.Fatalf("metadata key missing: %v", status)
		}
		if item.Kind != recipeCBORText || item.Text != expectedValue || occurrence.TxIndex != 2201 || occurrence.Index != 0 {
			t.Fatalf("metadata value or position differs: %+v", item)
		}
		found = true
		break
	}
	if !found {
		t.Fatal("reference inscription not found in verified block")
	}
	definition := `{"format":"gateway-recipe","version":1,"id":"reference","name":"Reference","source":"bitcoin.reveal","origin":908657,"history":"sparse","tables":[{"name":"items","key":"ref"}],"steps":[{"op":"require","test":{"call":"and","args":[{"call":"present","args":[{"field":["metaprotocol"]}]},{"call":"eq","args":[{"field":["metaprotocol"]},{"literal":"labitbu"}]}]}},{"op":"let","as":"metadata","value":{"call":"cbor.scalar","args":[{"call":"cbor.key","args":[{"call":"cbor.decode","args":[{"field":["metadata_hex"]}]},{"literal":"labitbu"}]}]}},{"op":"require","test":{"call":"and","args":[{"call":"valid","args":[{"variable":"metadata"}]},{"call":"eq","args":[{"variable":"metadata"},{"literal":"b1d57d7e45e8bbe30107bb082a374db5a644be0be6b06ab3c604a36b15eaf358"}]}]}},{"op":"write","table":"items","row":{"ref":{"field":["ref"]},"txid":{"field":["txid"]}}}]}`
	run := func(text string) recipeRows {
		t.Helper()
		d, e := parseRecipe([]byte(text))
		if e != nil {
			t.Fatal(e)
		}
		p, e := compileRecipe(d)
		if e != nil {
			t.Fatal(e)
		}
		rows, e := p.evaluateInscriptionBlock(context.Background(), block, &result)
		if e != nil {
			t.Fatal(e)
		}
		return rows
	}
	rows := run(definition)
	matched := false
	for _, row := range rows["items"] {
		if row["ref"] == "2201i0.908657" && row["txid"] == strings.TrimSuffix(target, "i0") {
			matched = true
		}
	}
	if !matched {
		t.Fatal("compiled recipe did not select the authenticated occurrence")
	}
	changed := run(strings.Replace(definition, expectedValue, "different", 1))
	for _, row := range changed["items"] {
		if row["ref"] == "2201i0.908657" {
			t.Fatal("changed metadata predicate still selected the occurrence")
		}
	}
}
