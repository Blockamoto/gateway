package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bitmapTestBlock(height int64, hash, prev, body string) blockView {
	b := inscriptionTestBlock(inscriptionTestTx(inscriptionTestScript(nil, []byte(body))))
	b.Height = height
	b.Hash = hash
	b.PreviousBlockHash = prev
	return b
}
func TestIndexStoreResumeCollisionAndReorg(t *testing.T) {
	root := t.TempDir()
	s, e := openIndexStore(root, "bitmap")
	if e != nil {
		t.Fatal(e)
	}
	first := bitmapTestBlock(792435, strings.Repeat("1", 64), strings.Repeat("0", 64), "0.bitmap")
	if e = s.appendBlock(first, 792435, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	original := s.checkpoint.Commitment
	second := bitmapTestBlock(792436, strings.Repeat("2", 64), first.Hash, "0.bitmap")
	second.Transactions[1].TxID = strings.Repeat("4", 64)
	if e = s.appendBlock(second, 792435, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	resumed, e := openIndexStore(root, "bitmap")
	if e != nil {
		t.Fatal(e)
	}
	chain, e := resumed.chain()
	if e != nil || len(chain) != 2 || chain[1].Bitmap[0].Accepted || chain[1].Bitmap[0].EarlierWinner != chain[0].Bitmap[0].Inscription {
		t.Fatalf("collision/resume %+v %v", chain, e)
	}
	if e = resumed.reconcile(func(int64) (string, error) { return "", fmt.Errorf("offline") }); e == nil || resumed.checkpoint.Height != 792436 {
		t.Fatal("unavailable evidence rewound state")
	}
	if e = resumed.reconcile(func(h int64) (string, error) {
		if h == 792435 {
			return first.Hash, nil
		}
		return strings.Repeat("5", 64), nil
	}); e != nil {
		t.Fatal(e)
	}
	if resumed.checkpoint.Commitment != original {
		t.Fatal("did not roll back to common checkpoint")
	}
	alternative := second
	alternative.Hash = strings.Repeat("5", 64)
	if e = resumed.appendBlock(alternative, 792435, "ephemeral"); e != nil {
		t.Fatal(e)
	}
}

func TestIndexStoreResumeThroughBlockWithoutCandidates(t *testing.T) {
	for _, id := range []string{"bitmap", "inscriptions"} {
		t.Run(id, func(t *testing.T) {
			root := t.TempDir()
			s, err := openIndexStore(root, id)
			if err != nil {
				t.Fatal(err)
			}
			first := bitmapTestBlock(792435, strings.Repeat("1", 64), strings.Repeat("0", 64), "0.bitmap")
			if err = s.appendBlock(first, 792435, "ephemeral"); err != nil {
				t.Fatal(err)
			}
			prior := s.checkpoint.Commitment
			empty := first
			empty.Height++
			empty.PreviousBlockHash = first.Hash
			empty.Hash = strings.Repeat("2", 64)
			empty.Transactions = inscriptionTestBlock().Transactions
			empty.TransactionCount = uint64(len(empty.Transactions))
			if err = s.appendBlock(empty, 792435, "ephemeral"); err != nil {
				t.Fatal(err)
			}
			resumed, err := openIndexStore(root, id)
			if err != nil {
				t.Fatal(err)
			}
			if resumed.checkpoint.Height != empty.Height || resumed.checkpoint.PreviousCommitment != prior {
				t.Fatal("empty block did not preserve contiguous checkpoint")
			}
		})
	}
}
func TestIndexStoreOrphanAndCorruption(t *testing.T) {
	root := t.TempDir()
	s, _ := openIndexStore(root, "bitmap")
	b := bitmapTestBlock(792435, strings.Repeat("1", 64), strings.Repeat("0", 64), "0.bitmap")
	if e := s.appendBlock(b, 792435, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(s.dir, "commits", strings.Repeat("f", 64)+".json"), []byte("interrupted"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := openIndexStore(root, "bitmap"); e != nil {
		t.Fatal("orphan became visible", e)
	}
	if e := os.WriteFile(filepath.Join(s.dir, "commits", s.checkpoint.Commitment+".json"), []byte(`{}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := openIndexStore(root, "bitmap"); e == nil {
		t.Fatal("corrupt selected checkpoint accepted")
	}
}
func TestIndexStoreNoGapOrUnanchoredPromotion(t *testing.T) {
	s, _ := openIndexStore(t.TempDir(), "bitmap")
	b := bitmapTestBlock(792436, strings.Repeat("1", 64), strings.Repeat("0", 64), "0.bitmap")
	if e := s.appendBlock(b, 792435, "ephemeral"); e == nil {
		t.Fatal("accepted missing genesis")
	}
	b.Height = 792435
	b.Verification.HeaderChainMatch = false
	if e := s.appendBlock(b, 792435, "ephemeral"); e == nil {
		t.Fatal("unanchored block promoted")
	}
}
func TestIndexFingerprintIndependentOfProvider(t *testing.T) {
	a, _ := openIndexStore(t.TempDir(), "inscriptions")
	b, _ := openIndexStore(t.TempDir(), "inscriptions")
	block := bitmapTestBlock(792435, strings.Repeat("1", 64), strings.Repeat("0", 64), "0.bitmap")
	if e := a.appendBlock(block, 792435, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	block.Verification.ConsensusValidated = true
	block.SourcePeer = "Core"
	block.VerificationState = "consensus_validated"
	if e := b.appendBlock(block, 792435, "retain"); e != nil {
		t.Fatal(e)
	}
	if a.checkpoint.Commitment != b.checkpoint.Commitment {
		t.Fatal("provider/retention changed semantic fingerprint")
	}
}
func TestBitmapBaseCanonicalAndFutureCandidates(t *testing.T) {
	bodies := []string{"0.bitmap", "00.bitmap", " 1.bitmap", "1.bitmap", "2.bitmap", "18446744073709551616.bitmap", "3.BITMAP"}
	occ := []inscriptionOccurrence{}
	for i, body := range bodies {
		occ = append(occ, inscriptionOccurrence{ID: fmt.Sprint(i), BlockHeight: 1, BlockOrder: i, Body: []byte(body)})
	}
	rows := bitmapCandidates(occ, map[uint64]string{})
	if len(rows) != 6 || !rows[0].Accepted || rows[1].Accepted || rows[2].Accepted || !rows[3].Accepted || rows[4].Reason != "future_block" || rows[5].Accepted {
		t.Fatalf("wrong rules: %+v", rows)
	}
}

func TestBitmapRealOriginBlock(t *testing.T) {
	raw, e := os.ReadFile(filepath.Join("docs", "bitmap-evidence", "genesis-block.raw"))
	if e != nil {
		t.Fatal(e)
	}
	hash := "000000000000000000035d4eee4c3b3864b503ed5c6b35a92de219529b34ca2c"
	// The pinned fixture's selected-chain position is independently documented by
	// the evidence verifier. This unit test authenticates body/header commitments;
	// it does not pretend to sync the complete mainnet header chain.
	block, e := parseBlockDetailed(792435, hash, raw[:80], raw, "fixture", false)
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	s, e := openIndexStore(root, "bitmap")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.appendBlock(block, 792435, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	if got := s.winners[0]; got != "86539aff946c437af8088955827b7e6ff48fc6192836d4071b697b5359b7a732i0" {
		t.Fatalf("origin = %q", got)
	}
	if _, e = os.Stat(filepath.Join(root, "blocks")); !os.IsNotExist(e) {
		t.Fatal("derivation unexpectedly retained Bitcoin source blocks")
	}
	reopened, e := openIndexStore(root, "bitmap")
	if e != nil || reopened.checkpoint.Commitment != s.checkpoint.Commitment {
		t.Fatalf("origin restart: %v", e)
	}
}
