//go:build gateway_district_diff

package main

// Compare projected raw inscription fields to the existing complete extractor.
// This is an opt-in corpus check, not an Ord conformance proof.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	engine "./experiments/common-engine"
)

func inscriptionDiffEqualStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func inscriptionDiffPending(v []string) string {
	for i := 0; i < len(v); i += 2 {
		if v[i] == "" {
			return ""
		}
		if i+1 == len(v) {
			return v[i]
		}
	}
	return ""
}
func TestInscriptionRealBlockProjection080(t *testing.T) {
	dir := os.Getenv("GATEWAY_080_078_FIXTURE")
	if dir == "" {
		t.Skip("set GATEWAY_080_078_FIXTURE to the isolated baseline-078-owned directory")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	manifest := districtDiffRead(t, filepath.Join(dir, "inputs", "manifest.json"))
	if districtDiffHash(manifest) != "d7836322fa33db18de5aecf9d2a49039ad244be7e6bb5b0cce690438cca7bf8a" {
		t.Fatal("fixture manifest changed")
	}
	var refs districtDiffFixture
	if err = json.Unmarshal(manifest, &refs); err != nil {
		t.Fatal(err)
	}
	headers := districtDiffRead(t, filepath.Join(dir, "inputs", "headers.bin"))
	if districtDiffHash(headers) != "534eea7cc69ef24fc359141cd030bd4f8d7e788a1f7634f15402d05a5cf77155" || len(refs.SelectedBlocks) != 32 {
		t.Fatal("header fixture or block count changed")
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
	subject, err := engine.New(recipe)
	if err != nil {
		t.Fatal(err)
	}
	totalTx, totalReveal, meta, metaprotocol, parent, incomplete := 0, 0, 0, 0, 0, 0
	lastHash := ""
	for blockIndex, ref := range refs.SelectedBlocks {
		if ref.Height != 792435+int64(blockIndex) {
			t.Fatal("selected fixture is not consecutive")
		}
		offset := ref.Height * 80
		if offset < 0 || offset+80 > int64(len(headers)) {
			t.Fatal("missing selected header")
		}
		header := headers[offset : offset+80]
		raw := districtDiffRead(t, filepath.Join(dir, "archive", "blocks", ref.Hash[:2], ref.Hash+".blk"))
		if len(raw) != ref.Bytes || districtDiffHash(raw) != ref.SHA256 || !bytes.Equal(raw[:80], header) {
			t.Fatalf("source mismatch at %d", ref.Height)
		}
		block, err := parseBlockDetailed(ref.Height, ref.Hash, header, raw, "isolated inscription projection fixture", false)
		if err != nil || !integrityVerified(block) || !block.Verification.HeaderChainMatch || len(block.Transactions) != ref.Transactions {
			t.Fatalf("block integrity at %d: %v", ref.Height, err)
		}
		if blockIndex > 0 && block.PreviousBlockHash != lastHash {
			t.Fatalf("broken header link at %d", ref.Height)
		}
		extracted, err := extractInscriptionOccurrences(block)
		if err != nil {
			t.Fatalf("extraction at %d: %v", ref.Height, err)
		}
		if err = validateCompleteInscriptionInventory(block, &extracted); err != nil {
			t.Fatalf("incomplete inventory at %d: %v", ref.Height, err)
		}
		input := engine.Block{Height: uint64(ref.Height), Hash: block.Hash, ParentHash: block.PreviousBlockHash, Transactions: make([]engine.Transaction, len(block.Transactions))}
		for i, tx := range block.Transactions {
			input.Transactions[i] = engine.Transaction{ID: tx.TxID, Coinbase: tx.Coinbase, RevealsComplete: true}
		}
		lastTx := -1
		for order, o := range extracted.Occurrences {
			if o.BlockOrder != order || o.TxIndex < lastTx || o.TxIndex < 0 || o.TxIndex >= len(input.Transactions) {
				t.Fatalf("reveal order at %d", ref.Height)
			}
			lastTx = o.TxIndex
			tx := &input.Transactions[o.TxIndex]
			if tx.ID != o.TxID || int(o.Index) != len(tx.Reveals) {
				t.Fatalf("transaction reveal order at %d", ref.Height)
			}
			tx.Reveals = append(tx.Reveals, engine.Reveal{PayloadAvailable: true, ParserProfile: o.ParserProfile, PayloadHex: append([]string(nil), o.PayloadHex...)})
		}
		receipt, err := subject.Apply(input)
		if err != nil {
			t.Fatalf("projection at %d: %v", ref.Height, err)
		}
		if receipt.Height != uint64(ref.Height) || receipt.Hash != ref.Hash || len(receipt.Rows) != len(extracted.Occurrences) {
			t.Fatalf("projection coverage at %d", ref.Height)
		}
		for i, o := range extracted.Occurrences {
			row := receipt.Rows[i]
			cells := map[string]string{}
			for _, cell := range row.Cells {
				if _, exists := cells[cell.Field]; exists {
					t.Fatalf("duplicate field %s", cell.Field)
				}
				cells[cell.Field] = cell.Value
			}
			if len(cells) != 12 || cells["inscription.blocator"] != inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height) || cells["parser.profile"] != o.ParserProfile {
				t.Fatalf("identity/profile differs at %d:%d", ref.Height, i)
			}
			var payload []string
			if err = json.Unmarshal([]byte(cells["payload.pushes.hex.json"]), &payload); err != nil || !inscriptionDiffEqualStrings(payload, o.PayloadHex) {
				t.Fatalf("raw pushes differ at %d:%d: %v", ref.Height, i, err)
			}
			var fields map[string][]string
			if err = json.Unmarshal([]byte(cells["fields.hex.json"]), &fields); err != nil || !reflect.DeepEqual(fields, o.Envelope.Fields) {
				t.Fatalf("raw tag fields differ at %d:%d: %v", ref.Height, i, err)
			}
			if cells["field.dangling.tag.hex"] != inscriptionDiffPending(o.PayloadHex) {
				t.Fatalf("dangling field differs at %d:%d", ref.Height, i)
			}
			if cells["metadata.hex"] != o.Envelope.Metadata || cells["field.incomplete"] != strconv.FormatBool(o.Envelope.Incomplete) {
				t.Fatalf("metadata/incomplete differs at %d:%d", ref.Height, i)
			}
			metaState, protoState, parentState := "absent", "absent", "not-declared"
			if len(o.Envelope.Fields["05"]) > 0 {
				metaState = "present"
				meta++
			}
			if len(o.Envelope.Fields["07"]) > 0 {
				protoState = "present"
				metaprotocol++
			}
			if len(o.Envelope.Fields["03"]) > 0 {
				parentState = "declared-unresolved"
				parent++
			}
			switch inscriptionDiffPending(o.PayloadHex) {
			case "05":
				if len(o.Envelope.Fields["05"]) == 0 {
					metaState = "incomplete"
				}
			case "07":
				if len(o.Envelope.Fields["07"]) == 0 {
					protoState = "incomplete"
				}
			case "03":
				if len(o.Envelope.Fields["03"]) == 0 {
					parentState = "declared-incomplete"
				}
			}
			if o.Envelope.Incomplete {
				incomplete++
			}
			if cells["metadata.state"] != metaState || cells["metaprotocol.state"] != protoState || cells["parent.relationship.state"] != parentState {
				t.Fatalf("declaration state differs at %d:%d", ref.Height, i)
			}
			var proto, parents []string
			if err = json.Unmarshal([]byte(cells["metaprotocol.values.hex.json"]), &proto); err != nil || !inscriptionDiffEqualStrings(proto, o.Envelope.Fields["07"]) {
				t.Fatalf("metaprotocol bytes differ at %d:%d", ref.Height, i)
			}
			if err = json.Unmarshal([]byte(cells["parent.declarations.hex.json"]), &parents); err != nil || !inscriptionDiffEqualStrings(parents, o.Envelope.Fields["03"]) {
				t.Fatalf("parent declaration bytes differ at %d:%d", ref.Height, i)
			}
		}
		totalTx += len(block.Transactions)
		totalReveal += len(extracted.Occurrences)
		lastHash = ref.Hash
	}
	view, err := subject.View()
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Receipts) != 32 || totalTx != 64767 || totalReveal != 12457 {
		t.Fatalf("fixture coverage changed: %d/%d/%d", len(view.Receipts), totalTx, totalReveal)
	}
	t.Log(fmt.Sprintf("PASS: 32 blocks, %d transactions, %d reveals; metadata=%d metaprotocol=%d parent_declarations=%d incomplete=%d", totalTx, totalReveal, meta, metaprotocol, parent, incomplete))
}
