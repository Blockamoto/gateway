// Package verifiedblock is an isolated mainnet block-integrity experiment.
// It does not verify selected-chain membership or full Bitcoin consensus.
package verifiedblock

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/btcutil/v2"
	"github.com/btcsuite/btcd/chaincfg/v2"
	"github.com/btcsuite/btcd/wire/v2"
)

const maxBlockBytes = 4_000_000
const mainnetSegwitHeight = 481824

// Anchor is supplied by the caller. ExpectedHeader must come from a separately
// authenticated chain authority; matching it here does not prove that authority
// or that the header is currently selected. This experiment is mainnet only.
type Anchor struct {
	Height         uint64
	ExpectedHeader [80]byte
}

type Summary struct {
	Height                uint64
	Hash                  string
	PreviousHash          string
	TransactionCount      int
	Weight                int64
	MatchesSuppliedHeader bool
	// WitnessRulesChecked describes rules selected by the caller-provided
	// height; it does not authenticate that height or prove chain membership.
	WitnessRulesChecked    bool
	SelectedChainProven    bool
	FullConsensusValidated bool
}

type Input struct {
	PreviousTxID   string
	PreviousOutput uint32
	Coinbase       bool
	Witness        [][]byte
}
type Output struct {
	Value  uint64
	Script []byte
}
type Transaction struct {
	ID, WitnessID string
	Inputs        []Input
	Outputs       []Output
}

// VerifiedBlock owns an internally decoded block. Accessors return detached
// values, so caller changes cannot alter later results or integrity evidence.
type VerifiedBlock struct {
	summary Summary
	block   *wire.MsgBlock
}

func (b *VerifiedBlock) Summary() Summary { return b.summary }
func (b *VerifiedBlock) Transaction(index int) (Transaction, bool) {
	if index < 0 || index >= len(b.block.Transactions) {
		return Transaction{}, false
	}
	tx := b.block.Transactions[index]
	t := Transaction{ID: tx.TxHash().String(), WitnessID: tx.WitnessHash().String(), Inputs: make([]Input, len(tx.TxIn)), Outputs: make([]Output, len(tx.TxOut))}
	for i, in := range tx.TxIn {
		coinbase := index == 0 && len(tx.TxIn) == 1 && in.PreviousOutPoint.Index == ^uint32(0) && in.PreviousOutPoint.Hash == ([32]byte{})
		t.Inputs[i] = Input{Coinbase: coinbase, Witness: make([][]byte, len(in.Witness))}
		if !coinbase {
			t.Inputs[i].PreviousTxID = in.PreviousOutPoint.Hash.String()
			t.Inputs[i].PreviousOutput = in.PreviousOutPoint.Index
		}
		for j, item := range in.Witness {
			t.Inputs[i].Witness[j] = append([]byte(nil), item...)
		}
	}
	for i, out := range tx.TxOut {
		t.Outputs[i] = Output{Value: uint64(out.Value), Script: append([]byte(nil), out.PkScript...)}
	}
	return t, true
}

// Verify checks a serialized block against one caller-provided mainnet header.
// It delegates parsing and context-free sanity to btcd, separately checks the
// witness commitment and block weight, and rejects trailing bytes. No header
// ancestry, difficulty transition, script, UTXO, or full consensus is proven.
func Verify(raw []byte, anchor Anchor) (*VerifiedBlock, error) {
	if len(raw) < 81 || len(raw) > maxBlockBytes {
		return nil, errors.New("serialized block outside size limit")
	}
	// Check and decode the same owned snapshot. Checking the caller's header
	// before copying would permit a reused input buffer to change between the
	// check and decoding. Callers must still avoid concurrent writes while the
	// snapshot itself is being copied.
	snapshot := append([]byte(nil), raw...)
	if !bytes.Equal(snapshot[:80], anchor.ExpectedHeader[:]) {
		return nil, errors.New("block header differs from supplied header")
	}
	reader := bytes.NewReader(snapshot)
	var block wire.MsgBlock
	if err := block.Deserialize(reader); err != nil {
		return nil, fmt.Errorf("decode block: %w", err)
	}
	if reader.Len() != 0 {
		return nil, errors.New("trailing block bytes")
	}
	wrapped := btcutil.NewBlock(&block)
	if err := blockchain.CheckBlockSanity(wrapped, chaincfg.MainNetParams.PowLimit, blockchain.NewMedianTime()); err != nil {
		return nil, fmt.Errorf("context-free block sanity: %w", err)
	}
	if anchor.Height < mainnetSegwitHeight {
		for _, tx := range block.Transactions {
			if tx.HasWitness() {
				return nil, errors.New("witness data before mainnet SegWit activation")
			}
		}
	} else if err := blockchain.ValidateWitnessCommitment(wrapped); err != nil {
		return nil, fmt.Errorf("witness commitment: %w", err)
	}
	weight := blockchain.GetBlockWeight(wrapped)
	if weight > 4_000_000 {
		return nil, errors.New("block weight exceeds limit")
	}
	summary := Summary{Height: anchor.Height, Hash: block.BlockHash().String(), PreviousHash: block.Header.PrevBlock.String(), TransactionCount: len(block.Transactions), Weight: weight, MatchesSuppliedHeader: true, WitnessRulesChecked: true}
	return &VerifiedBlock{summary: summary, block: &block}, nil
}
