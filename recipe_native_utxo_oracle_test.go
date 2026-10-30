package main

// Frozen pre-recipe derivation, retained only as a differential test oracle.
import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

func recipeLegacyUTXOApply(t *testing.T, store *utxoStore, block blockView) {
	t.Helper()
	if err := store.applyBlockLegacyOracle(context.Background(), block); err != nil {
		t.Fatal(err)
	}
}

func utxoBIP30Exception(block blockView) bool {
	return block.Height == 91842 && block.Hash == "00000000000a4d0a398161ffc163c503763b1f4360639393e0e4c8e300e0caec" || block.Height == 91880 && block.Hash == "00000000000743f190a18c5577a3c2d2a1f610ae9601ac046a38084ccb7cd721"
}

func (s *utxoStore) applyBlockLegacyOracle(ctx context.Context, block blockView) error {
	utxoWriterMu.Lock()
	defer utxoWriterMu.Unlock()
	utxoStateMu.Lock()
	defer utxoStateMu.Unlock()
	if e := s.reload(ctx); e != nil {
		return e
	}
	if block.Height < 0 || !validHash(block.Hash) || !integrityVerified(block) || !block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated || len(block.Transactions) == 0 || block.TransactionCount != uint64(len(block.Transactions)) || !block.Transactions[0].Coinbase {
		return fmt.Errorf("UTXO processing requires a complete header-anchored block")
	}
	if s.head == nil && (block.Height != 0 || block.Hash != genesisHashDisplay || len(block.Transactions) != 1) {
		return fmt.Errorf("UTXO state must begin at Bitcoin mainnet genesis")
	}
	var c utxoCheckpoint
	if s.head != nil {
		c = s.head.Checkpoint
		if block.Height != c.Height+1 || block.PreviousBlockHash != c.BlockHash {
			return fmt.Errorf("UTXO block is not the next committed transition; reconcile first")
		}
	}
	previous := c.Commitment
	journal := utxoJournal{Schema: 1, Height: block.Height, BlockHash: block.Hash, PreviousCommitment: previous, BeforeImages: []utxoBeforeImage{}}
	edit := &utxoEdit{trees: s.trees(ctx, c), facts: newUTXOFactReader(ctx, s.root), seen: map[string]bool{}, journal: &journal}
	facts, e := storeBlockOutputFactsContext(ctx, s.root, block)
	if e != nil {
		return e
	}
	if e = s.hook("facts_written"); e != nil {
		return e
	}
	for ti, tx := range block.Transactions {
		if e = ctx.Err(); e != nil {
			return e
		}
		if tx.Index != ti || !validHash(tx.TxID) || tx.TxID != strings.ToLower(tx.TxID) || tx.Coinbase != (ti == 0) || len(tx.Inputs) == 0 || len(tx.Outputs) == 0 {
			return fmt.Errorf("invalid UTXO transaction structure/order")
		}
		if ti == 0 && (len(tx.Inputs) != 1 || !tx.Inputs[0].Coinbase || tx.Inputs[0].N != 0) {
			return fmt.Errorf("invalid UTXO coinbase input")
		}
		var inputValue, outputValue uint64
		if ti > 0 {
			for vi, in := range tx.Inputs {
				if in.N != vi || in.Coinbase || !validHash(in.PrevTxID) || in.PrevTxID != strings.ToLower(in.PrevTxID) {
					return fmt.Errorf("invalid UTXO input order/outpoint")
				}
				member, exists, err := s.memberForOutpoint(edit.trees, in.PrevTxID, in.PrevVout)
				if err != nil {
					return err
				}
				if !exists {
					return fmt.Errorf("UTXO prerequisite missing or already spent: %s:%d", in.PrevTxID, in.PrevVout)
				}
				coin, err := s.coinUsingReader(ctx, edit.trees, member, edit.facts)
				if err != nil {
					return err
				}
				if coin.ValueSats > utxoMaxMoney-inputValue {
					return fmt.Errorf("UTXO transaction inputs exceed money range")
				}
				inputValue += coin.ValueSats
				runRememberPrevout(ctx, ti, vi, coin)
				if e = s.changeCoin(ctx, edit, &c, member, in.PrevTxID, false); e != nil {
					return e
				}
			}
		}
		var duplicate utxoBridge
		hasDuplicate, err := edit.trees["bridge"].get(utxoBridgeKey(tx.TxID), &duplicate)
		if err != nil {
			return err
		}
		if hasDuplicate && !(ti == 0 && utxoBIP30Exception(block)) {
			return fmt.Errorf("duplicate transaction ID has live UTXO outputs")
		}
		for oi, out := range tx.Outputs {
			if out.N != oi || out.ValueSats > utxoMaxMoney-outputValue {
				return fmt.Errorf("invalid UTXO output order or money range")
			}
			outputValue += out.ValueSats
			script, err := hex.DecodeString(out.ScriptPubKey)
			if err != nil || out.ScriptPubKey != strings.ToLower(out.ScriptPubKey) {
				return fmt.Errorf("invalid exact output script")
			}
			if block.Height == 0 || len(script) > 10000 || len(script) > 0 && script[0] == 0x6a {
				continue
			}
			if hasDuplicate {
				var displaced utxoMember
				exists, err := edit.trees["live"].get(utxoPositionKey(duplicate.Height, duplicate.TxIndex, oi), &displaced)
				if err != nil {
					return err
				}
				if exists {
					if e = s.changeCoin(ctx, edit, &c, displaced, tx.TxID, false); e != nil {
						return e
					}
				}
			}
			position := outputCoordinate(oi, ti, block.Height)
			ref, exists := facts[position]
			if !exists {
				return fmt.Errorf("%w: required output facts missing", ErrUTXOStateCorrupt)
			}
			f, err := edit.facts.read(ref)
			if err != nil || f.Height != block.Height || f.BlockHash != block.Hash || f.TxIndex != ti || int(f.Vout) != oi || f.ValueSats != out.ValueSats || f.ScriptPubKey != out.ScriptPubKey {
				return fmt.Errorf("%w: creation fact mismatch (%v)", ErrUTXOStateCorrupt, err)
			}
			if e = s.changeCoin(ctx, edit, &c, utxoMember{position, ref}, tx.TxID, true); e != nil {
				return e
			}
		}
		if ti > 0 && outputValue > inputValue {
			return fmt.Errorf("UTXO transaction outputs exceed inputs")
		}
		if hasDuplicate {
			var old utxoTransaction
			stillLive, err := edit.trees["transactions"].get(utxoTransactionKey(duplicate.Height, duplicate.TxIndex), &old)
			if err != nil {
				return err
			}
			if stillLive {
				return fmt.Errorf("BIP30 replacement left an unresolved older incarnation")
			}
		}
	}
	return s.finishBlockEdit(ctx, block, c, previous, journal, edit)
}
