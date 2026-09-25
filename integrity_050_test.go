package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func Test050MerkleCorruptionIsRejected(t *testing.T) {
	raw := testGenesisBlockPayload(t)
	raw[len(raw)-1] ^= 1
	header, _ := hex.DecodeString(genesisHeaderHex)
	if _, e := parseBlockDetailed(0, genesisHashDisplay, header, raw, "core", false); e == nil || !strings.Contains(e.Error(), "Merkle") {
		t.Fatalf("corrupt body was not rejected: %v", e)
	}
}
func Test050IntegrityReceiptAndGraphGate(t *testing.T) {
	a, v := initGraphTestApp(t)
	for _, label := range []string{"old_verifier", "merkle", "pow", "witness"} {
		t.Run(label, func(t *testing.T) {
			bad := v
			switch label {
			case "old_verifier":
				bad.Verification.VerifierVersion = 0
			case "merkle":
				bad.Verification.MerkleRoot = false
			case "pow":
				bad.Verification.ProofOfWork = false
			case "witness":
				bad.Verification.WitnessPresent = true
				bad.Verification.WitnessCommitment = false
			}
			if integrityVerified(bad) {
				t.Fatal("bad receipt accepted")
			}
			if e := a.graphIndexBlock(bad, true); e == nil {
				t.Fatal("graph accepted invalid integrity receipt")
			}
		})
	}
}
func witnessFixture050() []transactionView {
	reserved := make([]byte, 32)
	h := hash256([]byte("unit-test-witness-transaction"))
	root := merkleRoot([][32]byte{{}, h})
	commitment := hash256(append(root[:], reserved...))
	script := append([]byte{0x6a, 0x24, 0xaa, 0x21, 0xa9, 0xed}, commitment[:]...)
	return []transactionView{{Segwit: true, Coinbase: true, Inputs: []inputView{{Witness: []string{hex.EncodeToString(reserved)}}}, Outputs: []outputView{{ScriptPubKey: hex.EncodeToString(script)}}}, {Segwit: true, WTxID: reverseHex(h[:])}}
}
func Test050WitnessCommitmentVectors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func([]transactionView)
		want   bool
	}{
		{"valid", func(v []transactionView) {}, true},
		{"altered_witness", func(v []transactionView) { v[1].WTxID = strings.Repeat("ab", 32) }, false},
		{"missing_commitment", func(v []transactionView) { v[0].Outputs = nil }, false},
		{"wrong_reserved_size", func(v []transactionView) { v[0].Inputs[0].Witness = []string{"00"} }, false},
		{"missing_reserved", func(v []transactionView) { v[0].Inputs[0].Witness = nil }, false},
		{"highest_index_wins_valid", func(v []transactionView) {
			good := v[0].Outputs[0]
			bad := outputView{ScriptPubKey: "6a24aa21a9ed" + strings.Repeat("00", 32)}
			v[0].Outputs = []outputView{bad, good}
		}, true},
		{"highest_index_wins_invalid", func(v []transactionView) {
			v[0].Outputs = append(v[0].Outputs, outputView{ScriptPubKey: "6a24aa21a9ed" + strings.Repeat("00", 32)})
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := witnessFixture050()
			c.mutate(v)
			ok, e := verifyWitnessCommitment(v)
			if (e == nil && ok) != c.want {
				t.Fatalf("ok=%v err=%v", ok, e)
			}
		})
	}
}
func Test050LegacyNoWitnessHasNoWitnessReceipt(t *testing.T) {
	v := []transactionView{{Inputs: []inputView{{}}, Outputs: []outputView{{}}}}
	ok, e := verifyWitnessCommitment(v)
	if e != nil || ok {
		t.Fatalf("unexpected %v %v", ok, e)
	}
}
func Test050MerkleMutationDetection(t *testing.T) {
	a := hash256([]byte("a"))
	b := hash256([]byte("b"))
	c := hash256([]byte("c"))
	if merkleMutated([][32]byte{a, b, c}) {
		t.Fatal("odd duplicate padding is not mutation")
	}
	if !merkleMutated([][32]byte{a, b, c, c}) {
		t.Fatal("duplicate actual leaves not detected")
	}
}
func Test050MinimalCompactSize(t *testing.T) {
	for _, b := range [][]byte{{0xfd, 1, 0}, {0xfe, 0xff, 0, 0, 0}, {0xff, 1, 0, 0, 0, 0, 0, 0, 0}} {
		p := byteParser{b: b}
		if _, _, e := p.varIntRaw(); e == nil {
			t.Fatalf("accepted %x", b)
		}
	}
}
func Test050AtomicReceiptReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "record.json")
	for _, b := range [][]byte{[]byte("first"), []byte("replacement")} {
		if e := atomicWriteBytes(path, b); e != nil {
			t.Fatal(e)
		}
		got, e := os.ReadFile(path)
		if e != nil || !bytes.Equal(got, b) {
			t.Fatal("readback", e)
		}
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".gateway-*"))
	if len(files) != 0 {
		t.Fatal("orphaned temporary files", files)
	}
}
