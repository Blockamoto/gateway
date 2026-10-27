package verifiedblock

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/wire/v2"
)

const genesisHex = "01000000" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"3ba3edfd7a7b12b27ac72c3e67768f617fc81bc3888a51323a9fb8aa4b1e5e4a" +
	"29ab5f49ffff001d1dac2b7c" + "01" +
	"0100000001" + "0000000000000000000000000000000000000000000000000000000000000000ffffffff" +
	"4d04ffff001d0104455468652054696d65732030332f4a616e2f32303039204368616e63656c6c6f72206f6e206272696e6b206f66207365636f6e64206261696c6f757420666f722062616e6b73" +
	"ffffffff01" + "00f2052a01000000" + "43" +
	"4104678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f61deb649f6bc3f4cef38c4f35504e51ec112de5c384df7ba0b8d578a4c702b6bf11d5fac" + "00000000"

func genesis(t *testing.T) ([]byte, Anchor) {
	t.Helper()
	raw, err := hex.DecodeString(genesisHex)
	if err != nil {
		t.Fatal(err)
	}
	a := Anchor{}
	copy(a.ExpectedHeader[:], raw[:80])
	return raw, a
}

func TestGenesisAndDetachedViews(t *testing.T) {
	raw, a := genesis(t)
	b, err := Verify(raw, a)
	if err != nil {
		t.Fatal(err)
	}
	s := b.Summary()
	if s.Hash != "000000000019d6689c085ae165831e934ff763ae46a2a6c172b3f1b60a8ce26f" || s.TransactionCount != 1 || !s.MatchesSuppliedHeader || !s.WitnessRulesChecked || s.SelectedChainProven || s.FullConsensusValidated {
		t.Fatalf("incorrect evidence scope: %+v", s)
	}
	tx, ok := b.Transaction(0)
	if !ok || tx.ID != "4a5e1e4baab89f3a32518a88c31bc87f618f76673e2cc77ab2127b7afdeda33b" || len(tx.Inputs) != 1 || !tx.Inputs[0].Coinbase || len(tx.Outputs) != 1 || tx.Outputs[0].Value != 5000000000 {
		t.Fatalf("incorrect genesis transaction: %+v", tx)
	}
	original := tx.Outputs[0].Script[0]
	tx.Outputs[0].Script[0] ^= 0xff
	raw[len(raw)-1] ^= 1
	again, _ := b.Transaction(0)
	if again.Outputs[0].Script[0] != original || again.ID != tx.ID {
		t.Fatal("caller modified verified block")
	}
	if _, ok := b.Transaction(-1); ok {
		t.Fatal("accepted negative position")
	}
	if _, ok := b.Transaction(1); ok {
		t.Fatal("accepted out-of-range position")
	}
}

func TestRejectMalformedAndTamperedBodies(t *testing.T) {
	raw, anchor := genesis(t)
	badHeader := anchor
	badHeader.ExpectedHeader[0] ^= 1
	wrongBody := append([]byte(nil), raw...)
	wrongBody[len(wrongBody)-1] ^= 1
	trailing := append(append([]byte(nil), raw...), 0)
	noncanonicalCount := append(append(append([]byte(nil), raw[:80]...), 0xfd, 0x01, 0x00), raw[81:]...)
	for name, tc := range map[string]struct {
		raw    []byte
		anchor Anchor
	}{
		"short": {raw[:80], anchor}, "wrong header": {raw, badHeader}, "merkle mismatch": {wrongBody, anchor},
		"trailing": {trailing, anchor}, "oversized": {make([]byte, maxBlockBytes+1), anchor}, "noncanonical compact size": {noncanonicalCount, anchor},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Verify(tc.raw, tc.anchor); err == nil {
				t.Fatal("accepted malformed or modified body")
			}
		})
	}
}

// The inputs are read only. The pinned header file and each body digest are
// independently checked for fixture identity; this is not live chain evidence.
func TestPinnedRealBlockVectors(t *testing.T) {
	dir, headersPath := os.Getenv("GATEWAY_VERIFIED_VECTOR_DIR"), os.Getenv("GATEWAY_VERIFIED_HEADERS")
	if dir == "" || headersPath == "" {
		t.Skip("set GATEWAY_VERIFIED_VECTOR_DIR and GATEWAY_VERIFIED_HEADERS")
	}
	headers, err := os.Open(headersPath)
	if err != nil {
		t.Fatal(err)
	}
	defer headers.Close()
	digest := sha256.New()
	if _, err = io.Copy(digest, headers); err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(digest.Sum(nil)) != "534eea7cc69ef24fc359141cd030bd4f8d7e788a1f7634f15402d05a5cf77155" {
		t.Fatal("wrong pinned header snapshot")
	}
	vectors := []struct {
		height          uint64
		hash, sha, txid string
		position        int
	}{
		{825334, "00000000000000000000ab688a4ea75513640091a019b3a469825800c1070f82", "4ab2f4938c1e4692c1d4d9adf712b28ecfc70117a2e36ef954de6f1691239ee7", "d5dca5ad2ec1cf84cf6b1dcf2592ae69b562fcff4f50d8a56370412e3c61ea2b", 2332},
		{839704, "00000000000000000000561319cb5fb7015ac787156b219d43d18c0fcc8ce415", "02f1ecfd2b0499d3b5c034fadcb83f2ab63d37b7b4c21ee906ba155f34a128e1", "ab924ff229beca227bf40221faf492a20b5e2ee4f084524c84a5f98b80fe527f", 2286},
		{844782, "00000000000000000001ba4032567e702c2331891081ff06abad397343753d28", "d40ce4dc860de071e6b22b4ffb9626c5105a9e5cd94a5452980556fefa47339d", "c4c65ea6ea567b26e5adb645b569099683e7ff2b776cc846e9a9ea9f4cf85eac", 1118},
	}
	for _, v := range vectors {
		t.Run(fmt.Sprint(v.height), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, "blocks", v.hash+".blk"))
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(raw)
			if hex.EncodeToString(sum[:]) != v.sha {
				t.Fatal("wrong fixture body")
			}
			var a Anchor
			a.Height = v.height
			if _, err := headers.ReadAt(a.ExpectedHeader[:], int64(v.height)*80); err != nil {
				t.Fatal(err)
			}
			b, err := Verify(raw, a)
			if err != nil {
				t.Fatal(err)
			}
			if b.Summary().Hash != v.hash {
				t.Fatal("wrong block hash")
			}
			tx, ok := b.Transaction(v.position)
			if !ok || tx.ID != v.txid {
				t.Fatalf("wrong transaction at %d: %+v", v.position, tx)
			}
			witnessDetached := false
			for i, input := range tx.Inputs {
				for j, item := range input.Witness {
					if len(item) == 0 {
						continue
					}
					original := item[0]
					item[0] ^= 1
					again, _ := b.Transaction(v.position)
					if again.Inputs[i].Witness[j][0] != original {
						t.Fatal("caller modified retained witness")
					}
					witnessDetached = true
					break
				}
				if witnessDetached {
					break
				}
			}
			if !witnessDetached {
				t.Fatal("pinned inscription transaction has no nonempty witness")
			}

			// Change one nonempty witness item while preserving txids and header.
			// A valid post-SegWit block must fail its witness commitment.
			var m wire.MsgBlock
			if err := m.Deserialize(bytes.NewReader(raw)); err != nil {
				t.Fatal(err)
			}
			changed := false
			for _, transaction := range m.Transactions[1:] {
				for _, input := range transaction.TxIn {
					for _, item := range input.Witness {
						if len(item) != 0 {
							item[0] ^= 1
							changed = true
							break
						}
					}
					if changed {
						break
					}
				}
				if changed {
					break
				}
			}
			if !changed {
				t.Fatal("fixture has no nonempty witness")
			}
			var altered bytes.Buffer
			if err := m.Serialize(&altered); err != nil {
				t.Fatal(err)
			}
			if _, err := Verify(altered.Bytes(), a); err == nil || !strings.Contains(err.Error(), "witness commitment") {
				t.Fatalf("tampered witness accepted or misidentified: %v", err)
			}
		})
	}
}
