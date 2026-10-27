package verifiedblock

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/btcutil/v2"
	"github.com/btcsuite/btcd/wire/v2"
)

func TestRejectImpossibleDeclaredTransactionCount(t *testing.T) {
	raw, a := genesis(t)
	// Header is intact, but a four-billion transaction count cannot fit in
	// the supplied bytes. Reject in the decoder before accepting any block.
	malformed := append(append([]byte(nil), raw[:80]...), 0xfe, 0xff, 0xff, 0xff, 0xff)
	if _, err := Verify(malformed, a); err == nil || !strings.Contains(err.Error(), "decode block") {
		t.Fatalf("impossible count not rejected by decoder: %v", err)
	}
}

func TestRejectDeclaredWitnessCountBeyondRemainingBytes(t *testing.T) {
	raw, anchor := genesis(t)
	// A valid header followed by one witness transaction that claims four
	// million stack items but provides only the locktime. btcd's bytes.Reader
	// bound must reject it without preallocating the advertised empty stack.
	malformed := append([]byte(nil), raw[:80]...)
	malformed = append(malformed, 1)                   // block transaction count
	malformed = append(malformed, 1, 0, 0, 0, 0, 1, 1) // version, witness marker/flag, input count
	malformed = append(malformed, make([]byte, 32)...)
	malformed = append(malformed, 0xff, 0xff, 0xff, 0xff)    // prevout index
	malformed = append(malformed, 0, 0xff, 0xff, 0xff, 0xff) // empty script, sequence
	malformed = append(malformed, 1)                         // output count
	malformed = append(malformed, make([]byte, 8)...)
	malformed = append(malformed, 0) // empty output script
	malformed = append(malformed, 0xfe)
	var count [4]byte
	binary.LittleEndian.PutUint32(count[:], 4_000_000)
	malformed = append(malformed, count[:]...)
	malformed = append(malformed, 0, 0, 0, 0) // locktime, not four million items
	if _, err := Verify(malformed, anchor); err == nil || !strings.Contains(err.Error(), "decode block") {
		t.Fatalf("unavailable witness items were not rejected by decoder: %v", err)
	}
}

func TestPinnedWitnessActivationAndDuplicateMerkleMutation(t *testing.T) {
	dir, headersPath := os.Getenv("GATEWAY_VERIFIED_VECTOR_DIR"), os.Getenv("GATEWAY_VERIFIED_HEADERS")
	if dir == "" || headersPath == "" {
		t.Skip("set GATEWAY_VERIFIED_VECTOR_DIR and GATEWAY_VERIFIED_HEADERS")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "blocks", "00000000000000000000ab688a4ea75513640091a019b3a469825800c1070f82.blk"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != "4ab2f4938c1e4692c1d4d9adf712b28ecfc70117a2e36ef954de6f1691239ee7" {
		t.Fatal("fixture changed")
	}
	h, err := os.Open(headersPath)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	var anchor Anchor
	anchor.Height = 825334
	if _, err = h.ReadAt(anchor.ExpectedHeader[:], 825334*80); err != nil {
		t.Fatal(err)
	}
	b, err := Verify(raw, anchor)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Summary().WitnessRulesChecked || b.Summary().SelectedChainProven || b.Summary().FullConsensusValidated {
		t.Fatalf("overstated witness/chain evidence: %+v", b.Summary())
	}
	// Height is supplied by the caller: the real post-activation witness must
	// be rejected under a claimed pre-activation height. Passing a different
	// post-activation height demonstrates that this check cannot authenticate
	// the caller's height or selected chain.
	before := anchor
	before.Height = mainnetSegwitHeight - 1
	if _, err = Verify(raw, before); err == nil || !strings.Contains(err.Error(), "witness data before") {
		t.Fatalf("pre-activation witness not rejected: %v", err)
	}
	claimed := anchor
	claimed.Height = mainnetSegwitHeight
	other, err := Verify(raw, claimed)
	if err != nil || other.Summary().Height != claimed.Height || other.Summary().SelectedChainProven {
		t.Fatalf("caller-height semantics differ: block=%v err=%v", other, err)
	}
	var msg wire.MsgBlock
	if err = msg.Deserialize(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	if len(msg.Transactions)%2 != 1 {
		t.Fatal("fixture must have an odd number of transactions")
	}
	msg.Transactions = append(msg.Transactions, msg.Transactions[len(msg.Transactions)-1].Copy())
	// The duplicate of the final leaf yields the same Merkle root: rejecting
	// it exercises duplicate-tx protection rather than a mere root mismatch.
	root := blockchain.CalcMerkleRoot(btcutil.NewBlock(&msg).Transactions(), false)
	if root != msg.Header.MerkleRoot {
		t.Fatal("mutation did not preserve Merkle root")
	}
	var altered bytes.Buffer
	if err = msg.Serialize(&altered); err != nil {
		t.Fatal(err)
	}
	_, err = Verify(altered.Bytes(), anchor)
	var rule blockchain.RuleError
	if !errors.As(err, &rule) || rule.ErrorCode != blockchain.ErrDuplicateTx {
		t.Fatalf("same-root duplicate not rejected specifically: %v", err)
	}
}
