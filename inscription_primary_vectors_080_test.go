//go:build gateway_district_diff

package main

// Optional local verification of independently transported raw Bitcoin blocks.
// Checks only explicit raw tag declarations, never inferred ownership or parent proof.
import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	engine "./experiments/common-engine"
)

type inscriptionVectorCase struct {
	Height           int64  `json:"height"`
	BlockHash        string `json:"block_hash"`
	TxID             string `json:"txid"`
	InscriptionIndex uint32 `json:"inscription_index"`
	Bytes            int    `json:"bytes"`
	SHA256           string `json:"sha256"`
	Status           string `json:"status"`
	Filename         string `json:"filename"`
	URL              string `json:"url"`
}
type inscriptionVectorEvidence struct {
	Height                  int64               `json:"height"`
	BlockHash               string              `json:"block_hash"`
	TxID                    string              `json:"txid"`
	InscriptionIndex        uint32              `json:"inscription_index"`
	TxIndex                 int                 `json:"tx_index"`
	Blocator                string              `json:"blocator"`
	RawSHA256               string              `json:"raw_sha256"`
	SourceURL               string              `json:"source_url"`
	ParserProfile           string              `json:"parser_profile"`
	PayloadHex              []string            `json:"payload_hex"`
	RawTagValues            map[string][]string `json:"raw_tag_values"`
	MetadataHex             string              `json:"metadata_hex"`
	ParentRelationshipState string              `json:"parent_relationship_state"`
	Incomplete              bool                `json:"incomplete"`
}

func TestInscriptionPrimaryVectors080(t *testing.T) {
	dir := os.Getenv("GATEWAY_080_VECTOR_DIR")
	if dir == "" {
		t.Skip("set GATEWAY_080_VECTOR_DIR to the isolated HTTP fixture directory")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		Result    string                  `json:"result"`
		Transport string                  `json:"transport"`
		Origin    string                  `json:"origin"`
		HeaderSHA string                  `json:"header_sha256"`
		Fetched   []inscriptionVectorCase `json:"fetched"`
	}
	if err = json.Unmarshal(districtDiffRead(t, filepath.Join(dir, "acquisition-result.json")), &info); err != nil {
		t.Fatal(err)
	}
	if info.Result != "FETCHED" || info.Transport != "public-https-fixture-fallback" || info.Origin != "https://mempool.space" || info.HeaderSHA != "534eea7cc69ef24fc359141cd030bd4f8d7e788a1f7634f15402d05a5cf77155" || len(info.Fetched) != 3 {
		t.Fatal("incomplete or unrecognized fixture acquisition")
	}
	expected := map[int64]struct {
		hash, tx    string
		inscription uint32
	}{
		825334: {"00000000000000000000ab688a4ea75513640091a019b3a469825800c1070f82", "d5dca5ad2ec1cf84cf6b1dcf2592ae69b562fcff4f50d8a56370412e3c61ea2b", 229},
		839704: {"00000000000000000000561319cb5fb7015ac787156b219d43d18c0fcc8ce415", "ab924ff229beca227bf40221faf492a20b5e2ee4f084524c84a5f98b80fe527f", 1},
		844782: {"00000000000000000001ba4032567e702c2331891081ff06abad397343753d28", "c4c65ea6ea567b26e5adb645b569099683e7ff2b776cc846e9a9ea9f4cf85eac", 7},
	}
	headers := districtDiffRead(t, filepath.Join(dir, "inputs", "headers.bin"))
	if districtDiffHash(headers) != info.HeaderSHA {
		t.Fatal("selected header snapshot changed")
	}
	f, err := os.Open(filepath.Join("experiments", "common-engine", "recipes", "inscription-fields.json"))
	if err != nil {
		t.Fatal(err)
	}
	recipe, err := engine.ReadRecipe(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	evidence := make([]inscriptionVectorEvidence, 0, 3)
	for _, ref := range info.Fetched {
		want, ok := expected[ref.Height]
		if !ok || seen[ref.Height] || ref.Status != "fetched_unverified" || ref.BlockHash != want.hash || ref.TxID != want.tx || ref.InscriptionIndex != want.inscription || ref.URL != "https://mempool.space/api/block/"+want.hash+"/raw" {
			t.Fatalf("unexpected candidate at %d", ref.Height)
		}
		seen[ref.Height] = true
		offset := ref.Height * 80
		if offset < 0 || offset+80 > int64(len(headers)) {
			t.Fatal("missing selected header")
		}
		header := headers[offset : offset+80]
		raw := districtDiffRead(t, filepath.Join(dir, "blocks", ref.BlockHash+".blk"))
		if len(raw) != ref.Bytes || districtDiffHash(raw) != ref.SHA256 || !bytes.Equal(raw[:80], header) {
			t.Fatalf("raw/header mismatch at %d", ref.Height)
		}
		block, err := parseBlockDetailed(ref.Height, ref.BlockHash, header, raw, "public HTTP fixture", false)
		if err != nil || !integrityVerified(block) || !block.Verification.HeaderChainMatch || !block.Verification.WitnessCommitment {
			t.Fatalf("block/witness integrity failed at %d: %v", ref.Height, err)
		}
		extraction, err := extractInscriptionOccurrences(block)
		if err != nil {
			t.Fatalf("reveal extraction at %d: %v", ref.Height, err)
		}
		if err = validateCompleteInscriptionInventory(block, &extraction); err != nil {
			t.Fatalf("incomplete reveal inventory at %d: %v", ref.Height, err)
		}
		input := engine.Block{Height: uint64(ref.Height), Hash: block.Hash, ParentHash: block.PreviousBlockHash, Transactions: make([]engine.Transaction, len(block.Transactions))}
		for i, tx := range block.Transactions {
			input.Transactions[i] = engine.Transaction{ID: tx.TxID, Coinbase: tx.Coinbase, RevealsComplete: true}
		}
		selected := -1
		lastTx := -1
		for i, o := range extraction.Occurrences {
			if o.BlockOrder != i || o.TxIndex < lastTx || o.TxIndex < 0 || o.TxIndex >= len(input.Transactions) {
				t.Fatalf("wrong reveal order at %d", ref.Height)
			}
			lastTx = o.TxIndex
			tx := &input.Transactions[o.TxIndex]
			if tx.ID != o.TxID || int(o.Index) != len(tx.Reveals) {
				t.Fatalf("wrong transaction reveal index at %d", ref.Height)
			}
			tx.Reveals = append(tx.Reveals, engine.Reveal{PayloadAvailable: true, ParserProfile: o.ParserProfile, PayloadHex: append([]string(nil), o.PayloadHex...)})
			if o.TxID == ref.TxID && o.Index == ref.InscriptionIndex {
				if selected != -1 {
					t.Fatal("duplicate selected inscription")
				}
				selected = i
			}
		}
		if selected < 0 {
			t.Fatalf("candidate inscription not found in verified block %d", ref.Height)
		}
		subject, err := engine.New(recipe)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := subject.Apply(input)
		if err != nil || len(receipt.Rows) != len(extraction.Occurrences) {
			t.Fatalf("projection failed at %d: %v", ref.Height, err)
		}
		o := extraction.Occurrences[selected]
		row := receipt.Rows[selected]
		cells := map[string]string{}
		for _, cell := range row.Cells {
			if _, exists := cells[cell.Field]; exists {
				t.Fatal("duplicate output field")
			}
			cells[cell.Field] = cell.Value
		}
		if len(cells) != 12 || cells["inscription.blocator"] != inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height) || cells["parser.profile"] != o.ParserProfile {
			t.Fatalf("projected identity differs at %d", ref.Height)
		}
		var projectedFields map[string][]string
		var payload, parents, metaprotocol []string
		if err = json.Unmarshal([]byte(cells["fields.hex.json"]), &projectedFields); err != nil || !reflect.DeepEqual(projectedFields, o.Envelope.Fields) {
			t.Fatalf("tag values differ at %d", ref.Height)
		}
		if err = json.Unmarshal([]byte(cells["payload.pushes.hex.json"]), &payload); err != nil || !inscriptionDiffEqualStrings(payload, o.PayloadHex) {
			t.Fatalf("raw payload differs at %d", ref.Height)
		}
		if err = json.Unmarshal([]byte(cells["parent.declarations.hex.json"]), &parents); err != nil || !inscriptionDiffEqualStrings(parents, o.Envelope.Fields["03"]) {
			t.Fatalf("parent declaration differs at %d", ref.Height)
		}
		if err = json.Unmarshal([]byte(cells["metaprotocol.values.hex.json"]), &metaprotocol); err != nil || !inscriptionDiffEqualStrings(metaprotocol, o.Envelope.Fields["07"]) {
			t.Fatalf("metaprotocol differs at %d", ref.Height)
		}
		if cells["metadata.hex"] != o.Envelope.Metadata || cells["field.incomplete"] != strconv.FormatBool(o.Envelope.Incomplete) || cells["field.dangling.tag.hex"] != inscriptionDiffPending(o.PayloadHex) {
			t.Fatalf("metadata/incomplete fields differ at %d", ref.Height)
		}
		if len(parents) > 0 && cells["parent.relationship.state"] != "declared-unresolved" {
			t.Fatalf("parent declaration incorrectly resolved at %d", ref.Height)
		}
		tags := map[string][]string{}
		for _, tag := range []string{"03", "05", "07"} {
			if len(o.Envelope.Fields[tag]) > 0 {
				tags[tag] = append([]string(nil), o.Envelope.Fields[tag]...)
			}
		}
		evidence = append(evidence, inscriptionVectorEvidence{ref.Height, ref.BlockHash, ref.TxID, ref.InscriptionIndex, o.TxIndex, cells["inscription.blocator"], ref.SHA256, ref.URL, o.ParserProfile, append([]string(nil), o.PayloadHex...), tags, o.Envelope.Metadata, cells["parent.relationship.state"], o.Envelope.Incomplete})
		t.Logf("verified height=%d locator=%s tag03=%d tag05=%d tag07=%d incomplete=%v", ref.Height, cells["inscription.blocator"], len(tags["03"]), len(tags["05"]), len(tags["07"]), o.Envelope.Incomplete)
	}
	if output := os.Getenv("GATEWAY_080_VECTOR_EXPORT"); output != "" {
		build, e := filepath.EvalSymlinks("build")
		if e != nil {
			t.Fatal(e)
		}
		build, e = filepath.Abs(build)
		if e != nil {
			t.Fatal(e)
		}
		dest, e := filepath.Abs(output)
		if e != nil {
			t.Fatal(e)
		}
		parent, e := filepath.EvalSymlinks(filepath.Dir(dest))
		if e != nil {
			t.Fatal(e)
		}
		a, e := os.Stat(build)
		if e != nil {
			t.Fatal(e)
		}
		b, e := os.Stat(parent)
		if e != nil || !os.SameFile(a, b) {
			t.Fatal("export must be a new direct child of build/")
		}
		file, e := os.OpenFile(filepath.Join(parent, filepath.Base(dest)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			t.Fatal(e)
		}
		doc := struct {
			Schema    int                         `json:"schema"`
			Origin    string                      `json:"origin"`
			HeaderSHA string                      `json:"header_sha256"`
			Evidence  []inscriptionVectorEvidence `json:"evidence"`
		}{1, info.Origin, info.HeaderSHA, evidence}
		e = json.NewEncoder(file).Encode(doc)
		closeErr := file.Close()
		if e != nil {
			t.Fatal(e)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		t.Logf("saved verified raw declaration evidence to %s", dest)
	}
}
