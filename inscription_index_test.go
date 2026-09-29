package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// These fixtures isolate extraction from an already decoded block, using
// synthetic integrity receipts. Real Bitcoin authentication remains the block
// decoder's responsibility and is tested in integrity_050_test.go.
func inscriptionTestBlock(transactions ...transactionView) blockView {
	txs := []transactionView{{TxID: strings.Repeat("0", 64), Coinbase: true}}
	txs = append(txs, transactions...)
	for i := range txs {
		txs[i].Index = i
	}
	return blockView{Height: 792435, Hash: strings.Repeat("1", 64), Transactions: txs, TransactionCount: uint64(len(txs)),
		Verification: verificationView{VerifierVersion: blockVerifierVersion, HeaderHash: true, ProofOfWork: true, MerkleRoot: true, TransactionsParsed: true, WitnessPresent: true, WitnessCommitment: true, HeaderChainMatch: true}}
}

func inscriptionTestScript(parts ...[]byte) []byte {
	s := []byte{0, 0x63, 3, 'o', 'r', 'd'}
	for _, p := range parts {
		switch {
		case len(p) <= 75:
			s = append(s, byte(len(p)))
		case len(p) <= 255:
			s = append(s, 0x4c, byte(len(p)))
		case len(p) <= 65535:
			s = append(s, 0x4d, byte(len(p)), byte(len(p)>>8))
		default:
			s = append(s, 0x4e)
			s = binary.LittleEndian.AppendUint32(s, uint32(len(p)))
		}
		s = append(s, p...)
	}
	return append(s, 0x68)
}

func inscriptionTestTx(inputs ...[]byte) transactionView {
	tx := transactionView{TxID: strings.Repeat("2", 64), WTxID: strings.Repeat("3", 64)}
	for i, script := range inputs {
		tx.Inputs = append(tx.Inputs, inputView{N: i, Witness: ordWitness050(script)})
	}
	return tx
}

func TestInscriptionIndexOccurrenceOrderAndUnknownNumbering(t *testing.T) {
	one := inscriptionTestScript([]byte{1}, []byte("text/plain"), nil, []byte("0.bitmap"))
	two := inscriptionTestScript(nil, []byte("1.bitmap"))
	first := inscriptionTestTx(append(append([]byte{}, one...), two...), two)
	second := inscriptionTestTx(one)
	second.TxID = strings.Repeat("4", 64)
	block := inscriptionTestBlock(first, second)
	got, err := extractInscriptionOccurrences(block)
	if err != nil || len(got.Occurrences) != 4 {
		t.Fatalf("extract: %+v %v", got, err)
	}
	for i, want := range []struct{ tx, input, envelope, suffix int }{{1, 0, 0, 0}, {1, 0, 1, 1}, {1, 1, 0, 2}, {2, 0, 0, 0}} {
		r := got.Occurrences[i]
		if r.TxIndex != want.tx || r.InputIndex != want.input || r.EnvelopeIndex != want.envelope || r.Index != uint32(want.suffix) || r.BlockOrder != i || r.Envelope.Input != want.input || r.Envelope.Offset != want.envelope {
			t.Fatalf("wrong occurrence order: %+v", r)
		}
		if r.ID != block.Transactions[want.tx].TxID+"i"+string(rune('0'+want.suffix)) || r.CanonicalNumberKnown || r.BlockHeight != block.Height || r.BlockHash != block.Hash || r.VerificationState != "header_anchored" {
			t.Fatalf("wrong identity/evidence: %+v", r)
		}
	}
	if got.CanonicalNumberKnown || !got.Occurrences[0].HasContentType || got.Occurrences[1].HasContentType || string(got.Occurrences[0].Body) != "0.bitmap" || got.Occurrences[1].ScriptOffset != len(one) {
		t.Fatal("content/position metadata lost")
	}
	again, err := extractInscriptionOccurrences(block)
	b1, _ := json.Marshal(got)
	b2, _ := json.Marshal(again)
	if err != nil || !bytes.Equal(b1, b2) {
		t.Fatal("extraction is not deterministic")
	}
	// Output must not alias the decoded input's mutable witness list.
	block.Transactions[1].Inputs[0].Witness[0] = "00"
	if string(got.Occurrences[0].Body) != "0.bitmap" {
		t.Fatal("output aliases input")
	}
}

func TestInscriptionIndexReferenceWitnessSelection(t *testing.T) {
	script := inscriptionTestScript(nil, []byte("body"))
	for _, control := range []string{"", "00", "c1", strings.Repeat("ff", 33)} {
		tx := inscriptionTestTx(script)
		tx.Inputs[0].Witness[1] = control
		tx.Inputs[0].Witness = append(tx.Inputs[0].Witness, "5001")
		got, err := extractInscriptionOccurrences(inscriptionTestBlock(tx))
		if err != nil || len(got.Occurrences) != 1 || got.Occurrences[0].WitnessScriptIndex != 0 {
			t.Fatalf("unversioned/annex reference extraction: control=%q result=%+v error=%v", control, got, err)
		}
	}
	for _, w := range [][]string{{hex.EncodeToString(script)}, {hex.EncodeToString(script), "50"}} {
		tx := inscriptionTestTx(script)
		tx.Inputs[0].Witness = w
		got, err := extractInscriptionOccurrences(inscriptionTestBlock(tx))
		if err != nil || len(got.Occurrences) != 0 {
			t.Fatal("key-path witness was treated as a reveal", err)
		}
	}
}

func TestInscriptionIndexPushdataAndRawBytes(t *testing.T) {
	for _, size := range []int{0, 75, 76, 255, 256, 65535, 65536} {
		body := bytes.Repeat([]byte{0xff}, size)
		script := inscriptionTestScript([]byte{9}, []byte("br"), nil, body)
		got, err := extractInscriptionOccurrences(inscriptionTestBlock(inscriptionTestTx(script)))
		if err != nil || len(got.Occurrences) != 1 || !bytes.Equal(got.Occurrences[0].Body, body) || !got.Occurrences[0].Envelope.HasBody || got.Occurrences[0].Envelope.ContentEncoding != "br" {
			t.Fatalf("pushdata size=%d extraction failed: %v", size, err)
		}
		digest := sha256.Sum256(body)
		if got.Occurrences[0].ContentSHA256 != hex.EncodeToString(digest[:]) {
			t.Fatal("raw content digest differs")
		}
	}
	// A nonminimal empty PUSHDATA1 is still PushBytes([]) in the reference.
	script := inscriptionTestScript(nil, []byte("raw"))
	script = append([]byte{0x4c, 0}, script[1:]...)
	got, err := extractInscriptionOccurrences(inscriptionTestBlock(inscriptionTestTx(script)))
	if err != nil || len(got.Occurrences) != 1 || string(got.Occurrences[0].Body) != "raw" {
		t.Fatal("nonminimal empty push changed envelope recognition", err)
	}
}

func TestInscriptionIndexMalformedScriptsDoNotShiftLaterInputIDs(t *testing.T) {
	valid := inscriptionTestScript(nil, []byte("kept"))
	for _, suffix := range [][]byte{{0x01}, {0x4c}, {0x4c, 2, 0}, {0x4d, 1}, {0x4e, 0xff, 0xff, 0xff, 0x7f}} {
		malformed := append(append([]byte{}, valid...), suffix...)
		got, err := extractInscriptionOccurrences(inscriptionTestBlock(inscriptionTestTx(malformed, valid)))
		if err != nil || len(got.Occurrences) != 1 || got.Occurrences[0].InputIndex != 1 || got.Occurrences[0].Index != 0 || len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "malformed_script" {
			t.Fatalf("malformed input retained partial envelopes or shifted IDs: %+v %v", got, err)
		}
	}
	badEnvelope := []byte{0, 0x63, 3, 'o', 'r', 'd', 0xac}
	got, err := extractInscriptionOccurrences(inscriptionTestBlock(inscriptionTestTx(append(badEnvelope, valid...))))
	if err != nil || len(got.Occurrences) != 1 || got.Occurrences[0].Index != 0 || got.Diagnostics[0].Code != "non_push_envelope_opcode" {
		t.Fatal("non-push opcode consumed a valid later envelope", err)
	}
	got, err = extractInscriptionOccurrences(inscriptionTestBlock(inscriptionTestTx(valid[:len(valid)-1])))
	if err != nil || len(got.Occurrences) != 0 || len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "unterminated_envelope" {
		t.Fatal("unterminated envelope recognized", err)
	}
}

func TestInscriptionIndexPreservesFlagsAndUnpairedTags(t *testing.T) {
	script := inscriptionTestScript([]byte{2}, []byte{1}, []byte{2}, []byte{2}, []byte{5}, []byte{0xff}, []byte{5}, []byte{0}, []byte{22}, []byte{1}, []byte{99})
	got, err := extractInscriptionOccurrences(inscriptionTestBlock(inscriptionTestTx(script)))
	if err != nil || len(got.Occurrences) != 1 {
		t.Fatal(err)
	}
	r := got.Occurrences[0]
	if !r.Envelope.Duplicate || !r.Envelope.Incomplete || !r.Envelope.Unbound || r.Envelope.Pointer == nil || *r.Envelope.Pointer != 1 || r.Envelope.Metadata != "ff00" || r.PayloadHex[len(r.PayloadHex)-1] != "63" || r.Envelope.HasBody {
		t.Fatalf("field or raw evidence lost: %+v", r)
	}
	// Raw push-number encodings remain visible as a flag, not a curse verdict.
	for op := byte(0x51); op <= 0x60; op++ {
		script := []byte{0, 0, 0x63, 3, 'o', 'r', 'd', 0, op, 0x68}
		got, err := extractInscriptionOccurrences(inscriptionTestBlock(inscriptionTestTx(script)))
		if err != nil || len(got.Occurrences) != 1 || !got.Occurrences[0].Envelope.Stutter || !got.Occurrences[0].Envelope.Pushnum || !bytes.Equal(got.Occurrences[0].Body, []byte{op - 0x50}) {
			t.Fatalf("pushnum/stutter opcode %x: %+v %v", op, got, err)
		}
	}
}

func TestInscriptionIndexEvidenceAndBudgetsFailWithoutPartialResult(t *testing.T) {
	valid := inscriptionTestBlock(inscriptionTestTx(inscriptionTestScript(nil, []byte("body"))))
	for _, mutate := range []func(*blockView){
		func(b *blockView) { b.Verification.VerifierVersion = 0 },
		func(b *blockView) { b.Verification.WitnessCommitment = false },
		func(b *blockView) { b.Verification.WitnessCommitment = false; b.Verification.WitnessPresent = false },
		func(b *blockView) { b.TransactionCount++ },
		func(b *blockView) { b.Height = -1 },
	} {
		b := valid
		mutate(&b)
		if got, err := extractInscriptionOccurrences(b); err == nil || got.Occurrences != nil {
			t.Fatal("bad evidence produced a usable result")
		}
	}
	for _, bad := range []string{"xyz", "0", strings.Repeat("00", inscriptionMaxWitnessBytes+1)} {
		tx := inscriptionTestTx(nil)
		tx.Inputs[0].Witness[0] = bad
		if got, err := extractInscriptionOccurrences(inscriptionTestBlock(tx)); err == nil || got.Occurrences != nil {
			t.Fatal("invalid or oversized decoded witness accepted")
		}
	}
	for _, script := range [][]byte{
		bytes.Repeat(inscriptionTestScript(), inscriptionMaxOccurrences+1),
		append(append([]byte{0, 0x63, 3, 'o', 'r', 'd'}, bytes.Repeat([]byte{0}, inscriptionMaxPayloadItems+1)...), 0x68),
	} {
		if got, err := extractInscriptionOccurrences(inscriptionTestBlock(inscriptionTestTx(script))); err == nil || got.Occurrences != nil {
			t.Fatal("work budget silently truncated an indexed block")
		}
	}
	b := valid
	b.Verification.HeaderChainMatch = false
	got, err := extractInscriptionOccurrences(b)
	if err != nil || got.Occurrences[0].VerificationState != "pending_header_validation" {
		t.Fatal("extractor invented chain anchoring", err)
	}
}
