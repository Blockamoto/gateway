//go:build gateway_district_diff

package main

// This opt-in differential reads Gateway verified fixture data and native evidence.
// Optional export writes only a new file under build/ after every comparison passes;
// it never modifies the fixture or treats this sample as global parity.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	engine "./experiments/common-engine"
)

type districtDiffFixture struct {
	SelectedBlocks []struct {
		Height       int64  `json:"height"`
		Hash         string `json:"block_hash"`
		SHA256       string `json:"sha256"`
		Bytes        int    `json:"bytes"`
		Transactions int    `json:"transactions"`
	} `json:"selected_blocks"`
}

func districtDiffRead(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func districtDiffHash(b []byte) string { x := sha256.Sum256(b); return hex.EncodeToString(x[:]) }

func TestDistrictRealBlockDifferential080(t *testing.T) {
	dir := os.Getenv("GATEWAY_080_078_FIXTURE")
	if dir == "" {
		t.Skip("set GATEWAY_080_078_FIXTURE to the isolated baseline-078-owned directory")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	var baseline struct {
		Result           string `json:"result"`
		ArchiveUnchanged bool   `json:"archive_unchanged"`
		BinarySHA        string `json:"binary_sha256"`
	}
	if err = json.Unmarshal(districtDiffRead(t, filepath.Join(dir, "baseline-result.json")), &baseline); err != nil {
		t.Fatal(err)
	}
	if baseline.Result != "PASS" || !baseline.ArchiveUnchanged || baseline.BinarySHA != "a7c48db9bbfac6a77345e8c7abe1175d9ed40cd5c6238460f9a7a2228d824137" {
		t.Fatal("baseline is not the pinned completed native replay")
	}
	manifestBytes := districtDiffRead(t, filepath.Join(dir, "inputs", "manifest.json"))
	if districtDiffHash(manifestBytes) != "d7836322fa33db18de5aecf9d2a49039ad244be7e6bb5b0cce690438cca7bf8a" {
		t.Fatal("fixture manifest changed")
	}
	var fixture districtDiffFixture
	if err = json.Unmarshal(manifestBytes, &fixture); err != nil {
		t.Fatal(err)
	}
	headers := districtDiffRead(t, filepath.Join(dir, "inputs", "headers.bin"))
	if districtDiffHash(headers) != "534eea7cc69ef24fc359141cd030bd4f8d7e788a1f7634f15402d05a5cf77155" {
		t.Fatal("selected header file changed")
	}
	if len(fixture.SelectedBlocks) != 32 {
		t.Fatal("fixture must contain all 32 blocks")
	}
	var saved map[string]struct {
		Commits []bitmapDevelopmentHead `json:"commits"`
	}
	if err = json.Unmarshal(districtDiffRead(t, filepath.Join(dir, "replay", "00-baseline", "semantics.json")), &saved); err != nil {
		t.Fatal(err)
	}
	commits := saved[bitmapStandardDistrictID].Commits
	if len(commits) != len(fixture.SelectedBlocks) {
		t.Fatal("native saved coverage is incomplete")
	}
	f, err := os.Open(filepath.Join("experiments", "common-engine", "recipes", "bitmap-districts.json"))
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
	pages := filepath.Join(dir, "replay", "00-baseline", "profile", "indexes", "bitmap-development-v1", bitmapStandardDistrictID, "pages")
	previousCommit, previousBlock := "", ""
	totalTransactions, totalOccurrences, totalWinners := 0, 0, 0
	exportPath := os.Getenv("GATEWAY_080_078_EXPORT")
	var exportBlocks []engine.Block
	var exportReceipts []engine.Receipt
	for i, ref := range fixture.SelectedBlocks {
		if ref.Height != 792435+int64(i) {
			t.Fatalf("missing/nonconsecutive selected height %d", ref.Height)
		}
		offset := ref.Height * 80
		if offset < 0 || offset+80 > int64(len(headers)) {
			t.Fatalf("missing header %d", ref.Height)
		}
		header := headers[offset : offset+80]
		raw := districtDiffRead(t, filepath.Join(dir, "archive", "blocks", ref.Hash[:2], ref.Hash+".blk"))
		if len(raw) != ref.Bytes || districtDiffHash(raw) != ref.SHA256 || !bytes.Equal(raw[:80], header) {
			t.Fatalf("source bytes differ at %d", ref.Height)
		}
		block, err := parseBlockDetailed(ref.Height, ref.Hash, header, raw, "isolated differential fixture", false)
		if err != nil || !integrityVerified(block) || !block.Verification.HeaderChainMatch || len(block.Transactions) != ref.Transactions {
			t.Fatalf("block verification failed at %d: %v", ref.Height, err)
		}
		if i > 0 && block.PreviousBlockHash != previousBlock {
			t.Fatalf("broken selected header linkage at %d", ref.Height)
		}
		extraction, err := extractInscriptionOccurrences(block)
		if err != nil {
			t.Fatalf("extraction failed at %d: %v", ref.Height, err)
		}
		if err = validateCompleteInscriptionInventory(block, &extraction); err != nil {
			t.Fatalf("incomplete extraction at %d: %v", ref.Height, err)
		}
		input := engine.Block{Height: uint64(ref.Height), Hash: block.Hash, ParentHash: block.PreviousBlockHash, Transactions: make([]engine.Transaction, len(block.Transactions))}
		for j, tx := range block.Transactions {
			input.Transactions[j] = engine.Transaction{ID: tx.TxID, Coinbase: tx.Coinbase, RevealsComplete: true}
		}
		lastTx := -1
		for order, occ := range extraction.Occurrences {
			if occ.BlockOrder != order || occ.TxIndex < lastTx || occ.TxIndex < 0 || occ.TxIndex >= len(input.Transactions) {
				t.Fatalf("bad occurrence order at %d", ref.Height)
			}
			lastTx = occ.TxIndex
			tx := &input.Transactions[occ.TxIndex]
			if occ.TxID != tx.ID || int(occ.Index) != len(tx.Reveals) {
				t.Fatalf("nonconsecutive transaction reveal at %d:%d", ref.Height, occ.TxIndex)
			}
			tx.Reveals = append(tx.Reveals, engine.Reveal{ContentType: occ.Envelope.ContentType, Body: append([]byte(nil), occ.Body...)})
		}
		if i == 0 {
			incomplete := input
			incomplete.Transactions = append([]engine.Transaction(nil), input.Transactions...)
			incomplete.Transactions[0].RevealsComplete = false
			if _, err = subject.Apply(incomplete); err == nil {
				t.Fatal("engine accepted missing complete-inventory evidence")
			}
			v, e := subject.View()
			if e != nil || v.Head != nil || len(v.Receipts) != 0 {
				t.Fatal("incomplete extraction changed coverage")
			}
		}
		receipt, err := subject.Apply(input)
		if err != nil {
			t.Fatalf("engine rejected complete block %d: %v", ref.Height, err)
		}
		native := commits[i]
		if native.Height != ref.Height || native.BlockHash != ref.Hash || native.Previous != previousCommit || native.Commitment != bitmapDevelopmentDigest(native) || native.Index != bitmapStandardDistrictID || native.SegmentStart != 792435 || native.PreviousBlockHash != block.PreviousBlockHash || native.ProfileHash != bitmapProfileHash(native.Profile) || native.ProfileHash != bitmapProfileHash(bitmapStandardProfile("district")) || !native.Profile.CanonicalStandard || !native.LastBlock.BlockEvaluated || !native.LastBlock.CompleteScope || native.LastBlock.Height != ref.Height || native.LastBlock.BlockHash != ref.Hash {
			t.Fatalf("native checkpoint or complete coverage mismatch at %d", ref.Height)
		}
		if receipt.Height != uint64(ref.Height) || receipt.Hash != ref.Hash {
			t.Fatalf("engine coverage differs at %d", ref.Height)
		}
		got := make([]string, 0, len(receipt.Rows))
		tree := newOrdinalTree(pages, native.WinnerRoot)
		for _, row := range receipt.Rows {
			if len(row.Cells) != 2 || row.Cells[0].Field != "district" || row.Cells[1].Field != "inscription.blocator" {
				t.Fatalf("unexpected output shape at %d", ref.Height)
			}
			district, e := strconv.ParseUint(row.Cells[0].Value, 10, 64)
			if e != nil {
				t.Fatal(e)
			}
			var found bitmapDevelopmentCandidate
			have, e := tree.get(bitmapTargetKey(district, nil), &found)
			if e != nil || !have || found.District == nil || *found.District != district || found.Blocator != row.Cells[1].Value || found.Selection != "selected" {
				t.Fatalf("native target differs at %d, district %s: %v", ref.Height, row.Cells[0].Value, e)
			}
			got = append(got, row.Cells[1].Value)
		}
		if native.WinnerCount != uint64(totalWinners+len(got)) {
			t.Fatalf("native cumulative winner count differs at %d", ref.Height)
		}
		want := append([]string{}, native.LastBlock.WinnerBlocators...)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("winner order differs at %d: engine %v native %v", ref.Height, got, want)
		}
		if exportPath != "" {
			exportBlocks = append(exportBlocks, input)
			exportReceipts = append(exportReceipts, receipt)
		}
		previousCommit, previousBlock = native.Commitment, ref.Hash
		totalTransactions += len(block.Transactions)
		totalOccurrences += len(extraction.Occurrences)
		totalWinners += len(got)
	}
	view, err := subject.View()
	if err != nil {
		t.Fatal(err)
	}
	if view.Head == nil || view.Head.Height != 792466 || view.Head.Hash != previousBlock || len(view.Receipts) != 32 || totalTransactions != 64767 || totalWinners != 3 {
		t.Fatalf("final coverage/counters differ: head=%+v receipts=%d transactions=%d winners=%d", view.Head, len(view.Receipts), totalTransactions, totalWinners)
	}
	if exportPath != "" {
		// Resolve the real directory and forbid nested paths, so an export can
		// never be placed into the owned input archive or native profile.
		build, e := filepath.EvalSymlinks("build")
		if e != nil {
			t.Fatal(e)
		}
		build, e = filepath.Abs(build)
		if e != nil {
			t.Fatal(e)
		}
		dest, e := filepath.Abs(exportPath)
		if e != nil {
			t.Fatal(e)
		}
		parent, e := filepath.EvalSymlinks(filepath.Dir(dest))
		if e != nil {
			t.Fatal(e)
		}
		parent, e = filepath.Abs(parent)
		if e != nil {
			t.Fatal(e)
		}
		buildInfo, e := os.Stat(build)
		if e != nil {
			t.Fatal(e)
		}
		parentInfo, e := os.Stat(parent)
		if e != nil || !os.SameFile(buildInfo, parentInfo) {
			t.Fatal("optional export must be a new direct child of the checkout build directory")
		}
		file, e := os.OpenFile(filepath.Join(parent, filepath.Base(dest)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			t.Fatal(e)
		}
		payload := struct {
			Schema             int              `json:"schema"`
			ManifestSHA256     string           `json:"manifest_sha256"`
			HeaderSHA256       string           `json:"header_sha256"`
			NativeBinarySHA256 string           `json:"native_binary_sha256"`
			Blocks             []engine.Block   `json:"blocks"`
			Receipts           []engine.Receipt `json:"receipts"`
		}{1, districtDiffHash(manifestBytes), districtDiffHash(headers), baseline.BinarySHA, exportBlocks, exportReceipts}
		e = json.NewEncoder(file).Encode(payload)
		closeErr := file.Close()
		if e != nil {
			t.Fatal(e)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		t.Logf("exported verified complete inputs and native-matched receipts to %s", dest)
	}
	t.Log(fmt.Sprintf("PASS: 32 verified blocks, %d transactions, %d complete extracted occurrences, %d ordered District winners", totalTransactions, totalOccurrences, totalWinners))
}
