package main

// Frozen pre-recipe native FIFO derivation. Physical COW codecs remain shared;
// allocation, identity and state decisions here are independent of the JSON.
import (
	"encoding/hex"
	"fmt"
	"strings"
)

func (s *indexStore) deriveSatPhysicalOracle(block blockView, prepared *inscriptionBlockResult) (*satIndexSnapshot, error) {
	ctx := s.ordinalContext()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if block.Height < 0 || !integrityVerified(block) || !block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated || len(block.Transactions) == 0 || !block.Transactions[0].Coinbase || block.TransactionCount != uint64(len(block.Transactions)) {
		return nil, fmt.Errorf("Sat index requires complete header-anchored block")
	}
	if block.Height == 0 && block.Hash != genesisHashDisplay {
		return nil, fmt.Errorf("Sat identity requires Bitcoin mainnet genesis")
	}
	p := satIndexSnapshot{Profile: satIndexProfile, Layout: 1}
	if s.checkpoint != nil {
		b, err := s.readBatch(s.checkpoint.Commitment)
		if err != nil {
			return nil, err
		}
		if b.Sats == nil || b.Sats.Profile != satIndexProfile || b.Sats.Layout != 1 {
			return nil, ErrSatProfileIncompatible
		}
		p = *b.Sats
	} else if block.Height != 0 {
		return nil, fmt.Errorf("Sat identity requires genesis-first processing")
	}
	p.RetainHistory = s.retainSatHistory
	p.Movements = nil
	e := newSatEditor077(ctx, s, p)
	refs, err := storeBlockOutputFactsContext(ctx, s.profileRoot, block)
	if err != nil {
		return nil, err
	}
	occurrences := map[string][]inscriptionOccurrence{}
	var extraction inscriptionBlockResult
	if prepared != nil {
		if prepared.BlockHash != block.Hash || prepared.BlockHeight != block.Height || prepared.ParserProfile != inscriptionParserProfile {
			return nil, fmt.Errorf("Sat inscription evidence context mismatch")
		}
		extraction = *prepared
	} else {
		extraction, err = extractInscriptionOccurrences(block)
	}
	e.snapshot.InscriptionState = "complete_this_block"
	if err != nil {
		e.snapshot.InscriptionState = "identity_extraction_unavailable: " + err.Error()
	} else {
		e.snapshot.IdentityCoverage = mergeIntervals(append(e.snapshot.IdentityCoverage, heightInterval{block.Height, block.Height}))
		for _, o := range extraction.Occurrences {
			occurrences[o.TxID] = append(occurrences[o.TxID], o)
		}
	}
	if p.RetainHistory {
		e.snapshot.HistoryCoverage = mergeIntervals(append(e.snapshot.HistoryCoverage, heightInterval{block.Height, block.Height}))
	}
	coinbase := []satRange{}
	if n := subsidyAtHeight(block.Height); n > 0 {
		first := firstSatAtHeight(block.Height)
		coinbase = append(coinbase, satRange{first, first + n})
	}
	inputValues := make([][]uint64, len(block.Transactions))
	process := func(tx transactionView, coin bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !validHash(tx.TxID) || tx.Coinbase != coin {
			return fmt.Errorf("invalid Sat transaction identity")
		}
		stream := []satRange{}
		if coin {
			stream = append(stream, coinbase...)
		} else {
			for vin, in := range tx.Inputs {
				if in.Coinbase || !validHash(in.PrevTxID) || in.N != vin {
					return fmt.Errorf("invalid Sat input order")
				}
				var pos string
				have, err := e.bridge.get(ordinalOutpointKey(in.PrevTxID, in.PrevVout), &pos)
				if err != nil {
					return satCorruptRead077(ctx, "native bridge", err)
				}
				if !have {
					return fmt.Errorf("Sat missing input identity %s:%d", in.PrevTxID, in.PrevVout)
				}
				old, have, err := e.output(pos)
				if err != nil {
					return err
				}
				if !have || old.State != "unspent" {
					return fmt.Errorf("Sat input unavailable or unspendable")
				}
				old.Ranges, err = loadSatOutputRangesContext(ctx, s.ordinalPages(), old)
				if err != nil {
					return err
				}
				value, err := satRangesValueContext(ctx, old.Ranges)
				if err != nil || value != old.Value {
					return fmt.Errorf("%w: input range/value mismatch", ErrSatStateCorrupt)
				}
				inputValues[tx.Index] = append(inputValues[tx.Index], value)
				stream = append(stream, old.Ranges...)
				if err = e.remove(old); err != nil {
					return err
				}
				for _, r := range old.Ranges {
					e.locations.remove(satRangeKey(r.Start))
				}
			}
		}
		if !coin {
			total, err := sumOutputs(tx)
			if err != nil {
				return err
			}
			for _, o := range occurrences[tx.TxID] {
				identity := inscriptionSatResult{ID: o.ID, Profile: inscriptionSatProfile, State: "unbound", RevealHeight: block.Height, RevealHash: block.Hash, Note: "FIFO identity at saved Sat snapshot; holdings require location join"}
				if o.InputIndex < 0 || o.InputIndex >= len(inputValues[tx.Index]) {
					return fmt.Errorf("inscription input outside Sat stream")
				}
				if !o.Envelope.Unbound && inputValues[tx.Index][o.InputIndex] > 0 {
					position := uint64(0)
					for _, n := range inputValues[tx.Index][:o.InputIndex] {
						position += n
					}
					if o.Envelope.Pointer != nil && *o.Envelope.Pointer < total {
						position = *o.Envelope.Pointer
					}
					for _, r := range stream {
						if position < r.End-r.Start {
							sat := r.Start + position
							identity.SatNumber = &sat
							identity.Known = true
							identity.State = "known"
							break
						}
						position -= r.End - r.Start
					}
					if !identity.Known {
						return fmt.Errorf("inscription offset exceeds input sats")
					}
				}
				if err = e.ids.put(ordinalInscriptionKey(o.ID), identity); err != nil {
					return err
				}
				if identity.Known {
					if err = e.bySat.put(satRangeKey(*identity.SatNumber)+ordinalInscriptionKey(o.ID), identity); err != nil {
						return err
					}
				}
			}
		}
		for oi, out := range tx.Outputs {
			if out.N != oi {
				return fmt.Errorf("Sat output order mismatch")
			}
			ranges, err := takeSatRangesContext(ctx, &stream, out.ValueSats)
			if err != nil {
				return err
			}
			bridgeKey := ordinalOutpointKey(tx.TxID, uint32(oi))
			var oldpos string
			have, err := e.bridge.get(bridgeKey, &oldpos)
			if err != nil {
				return satCorruptRead077(ctx, "native bridge", err)
			}
			if have {
				if !coin || block.Height != 91842 && block.Height != 91880 {
					return fmt.Errorf("duplicate Sat outpoint")
				}
				old, exists, err := e.output(oldpos)
				if err != nil || !exists {
					return fmt.Errorf("%w: duplicate source missing", ErrSatStateCorrupt)
				}
				old.Ranges, err = loadSatOutputRangesContext(ctx, s.ordinalPages(), old)
				if err != nil {
					return err
				}
				if old.State == "unspent" {
					e.snapshot.LiveOutputs--
				}
				old.State = "bip30_destroyed"
				key, _ := satOutputKey077(old.Output)
				if err = e.outputs.put(key, old); err != nil {
					return err
				}
				if err = e.locate(old.Ranges, old.Output, old.State, block.Height, block.Hash, 0); err != nil {
					return err
				}
				e.snapshot.DestroyedSats += old.Value
			}
			pos := outputCoordinate(oi, tx.Index, block.Height)
			ref, have := refs[pos]
			if !have {
				return fmt.Errorf("%w: creation facts missing", ErrSatStateCorrupt)
			}
			script, err := hex.DecodeString(out.ScriptPubKey)
			if err != nil {
				return err
			}
			state := "unspent"
			if block.Height == 0 {
				state = "genesis_unspendable"
			} else if len(script) > 10000 || strings.HasPrefix(out.ScriptPubKey, "6a") {
				state = "burned"
			}
			stored, err := s.storeSatOutput(satIndexOutput{Output: pos, FactRef: &ref, Ranges: ranges, State: state})
			if err != nil {
				return err
			}
			if err = e.add(stored, tx.TxID, p.RetainHistory); err != nil {
				return err
			}
			if err = e.locate(ranges, pos, state, block.Height, block.Hash, 0); err != nil {
				return err
			}
		}
		if !coin {
			coinbase = append(coinbase, stream...)
		} else {
			if err = e.locate(stream, "", "lost", block.Height, block.Hash, e.snapshot.LostSats); err != nil {
				return err
			}
			n, err := satRangesValueContext(ctx, stream)
			if err != nil {
				return err
			}
			e.snapshot.LostSats += n
		}
		return nil
	}
	for i, tx := range block.Transactions {
		if tx.Index != i {
			return nil, fmt.Errorf("Sat transaction order mismatch")
		}
		if i > 0 {
			if err = process(tx, false); err != nil {
				return nil, err
			}
		}
	}
	if err = process(block.Transactions[0], true); err != nil {
		return nil, err
	}
	if block.Height == 91842 || block.Height == 91880 {
		e.snapshot.DuplicateCoinbases = append(e.snapshot.DuplicateCoinbases, satIndexIncarnation{block.Transactions[0].TxID, block.Height})
	}
	if err = storeSatFeeBlockContext(ctx, s.profileRoot, block, inputValues); err != nil {
		return nil, err
	}
	if err = e.flush(); err != nil {
		return nil, err
	}
	return &e.snapshot, nil
}
