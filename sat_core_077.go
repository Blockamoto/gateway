package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

var ErrSatStateCorrupt = errors.New("Sat state is incomplete or corrupt")
var ErrSatRebuildRequired = errors.New("Sat recovery savepoint unavailable; reviewed genesis rebuild required")
var ErrSatProfileIncompatible = errors.New("experimental Sat profile is incompatible; reviewed rebuild required")

func satCorruptRead077(ctx context.Context, what string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("%w: %s (%v)", ErrSatStateCorrupt, what, err)
}

func (e *satEditor077) heightCount(height int64, add bool) error {
	key := indexDigest(height)
	var n uint64
	have, err := e.counts.get(key, &n)
	if err != nil {
		return satCorruptRead077(e.counts.ctx, "creation-height counts", err)
	}
	e.changedHeights[height] = true
	if add {
		if n == math.MaxUint64 {
			return fmt.Errorf("Sat output count overflow")
		}
		return e.counts.put(key, n+1)
	}
	if !have || n == 0 {
		return fmt.Errorf("%w: creation-height count", ErrSatStateCorrupt)
	}
	if n == 1 {
		e.counts.remove(key)
		return nil
	}
	return e.counts.put(key, n-1)
}
func (e *satEditor077) updateCoverage() error {
	for height := range e.changedHeights {
		var n uint64
		have, err := e.counts.get(indexDigest(height), &n)
		if err != nil {
			return satCorruptRead077(e.counts.ctx, "creation-height counts", err)
		}
		if have && n > 0 {
			e.snapshot.OutputCoverage = mergeIntervals(append(e.snapshot.OutputCoverage, heightInterval{height, height}))
			continue
		}
		var out []heightInterval
		for _, r := range e.snapshot.OutputCoverage {
			if height < r.From || height > r.To {
				out = append(out, r)
				continue
			}
			if r.From < height {
				out = append(out, heightInterval{r.From, height - 1})
			}
			if height < r.To {
				out = append(out, heightInterval{height + 1, r.To})
			}
		}
		e.snapshot.OutputCoverage = out
	}
	return nil
}

// Runtime compatibility fields are hydrated from shared facts and the compact
// transaction catalogue. New output/location pages never repeat these fields.
func (o satIndexOutput) MarshalJSON() ([]byte, error) {
	if o.Output == "" {
		type legacy satIndexOutput
		return json.Marshal(legacy(o))
	}
	return json.Marshal(struct {
		Output string         `json:"output"`
		Facts  *outputFactRef `json:"facts"`
		Ranges []satRange     `json:"ranges,omitempty"`
		Root   string         `json:"ranges_root,omitempty"`
		Count  uint64         `json:"range_count"`
		State  string         `json:"state"`
	}{o.Output, o.FactRef, o.Ranges, o.RangesRoot, o.RangeCount, o.State})
}
func (l satIndexLocation) MarshalJSON() ([]byte, error) {
	if l.Output == "" && l.State != "lost" {
		type legacy satIndexLocation
		return json.Marshal(legacy(l))
	}
	return json.Marshal(struct {
		Start  uint64 `json:"start"`
		End    uint64 `json:"end"`
		Output string `json:"output,omitempty"`
		Offset uint64 `json:"offset"`
		State  string `json:"state"`
		Height int64  `json:"lost_height,omitempty"`
		Hash   string `json:"lost_hash,omitempty"`
	}{l.Start, l.End, l.Output, l.Offset, l.State, l.LostHeight, l.LostHash})
}

type satNative077 struct {
	TxID  string `json:"txid"`
	Count uint64 `json:"outputs"`
}
type satRangeSegment077 struct {
	Offset uint64     `json:"offset"`
	Ranges []satRange `json:"ranges"`
}
type satEditor077 struct {
	s                                                                     *indexStore
	snapshot                                                              satIndexSnapshot
	outputs, locations, bridge, native, pins, history, ids, bySat, counts *ordinalTree
	changedHeights                                                        map[int64]bool
	facts                                                                 *utxoFactReader
}

func satSnapshotRoots077(p satIndexSnapshot) []string {
	return []string{p.UTXORoot, p.LocationRoot, p.BridgeRoot, p.TransactionRoot, p.FactPinRoot, p.HistoricalOutputRoot, p.InscriptionRoot, p.InscriptionBySatRoot, p.OutputCountsRoot}
}
func newSatEditor077(ctx context.Context, s *indexStore, p satIndexSnapshot) *satEditor077 {
	tree := func(root string) *ordinalTree { return newOrdinalTree(s.ordinalPages(), root).withContext(ctx) }
	return &satEditor077{s: s, snapshot: p, outputs: tree(p.UTXORoot), locations: tree(p.LocationRoot), bridge: tree(p.BridgeRoot), native: tree(p.TransactionRoot), pins: tree(p.FactPinRoot), history: tree(p.HistoricalOutputRoot), ids: tree(p.InscriptionRoot), bySat: tree(p.InscriptionBySatRoot), counts: tree(p.OutputCountsRoot), changedHeights: map[int64]bool{}, facts: newUTXOFactReader(ctx, s.profileRoot)}
}
func satPosition077(position string) (bodCoordinate, error) {
	c, ok := parseBODCoordinate(position)
	if !ok || c.Kind != coordOutput || c.TxID != "" || c.Height < 0 || c.TxIndex < 0 || c.IOIndex < 0 || position != outputCoordinate(c.IOIndex, c.TxIndex, c.Height) {
		return c, fmt.Errorf("%w: invalid BPS output", ErrSatStateCorrupt)
	}
	return c, nil
}
func satOutputKey077(position string) (string, error) {
	c, e := satPosition077(position)
	if e != nil {
		return "", e
	}
	return utxoPositionKey(c.Height, c.TxIndex, c.IOIndex), nil
}
func (e *satEditor077) hydrate(o *satIndexOutput) error {
	c, err := satPosition077(o.Output)
	if err != nil {
		return err
	}
	if o.FactRef == nil {
		return fmt.Errorf("%w: missing output facts", ErrSatStateCorrupt)
	}
	f, err := e.facts.read(*o.FactRef)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSatStateCorrupt, err)
	}
	if f.Height != c.Height || f.TxIndex != c.TxIndex || int(f.Vout) != c.IOIndex {
		return fmt.Errorf("%w: fact identity", ErrSatStateCorrupt)
	}
	var native satNative077
	have, err := e.native.get(utxoTransactionKey(c.Height, c.TxIndex), &native)
	if err != nil || !have || !validHash(native.TxID) || native.Count == 0 {
		return fmt.Errorf("%w: native catalogue (%v)", ErrSatStateCorrupt, err)
	}
	o.Value = f.ValueSats
	o.Point = satlinePoint{TxID: native.TxID, Vout: f.Vout, Height: f.Height, BlockHash: f.BlockHash, TxIndex: f.TxIndex}
	return nil
}
func (e *satEditor077) output(position string) (satIndexOutput, bool, error) {
	var o satIndexOutput
	k, err := satOutputKey077(position)
	if err != nil {
		return o, false, err
	}
	have, err := e.outputs.get(k, &o)
	if err != nil {
		return o, false, fmt.Errorf("%w: %v", ErrSatStateCorrupt, err)
	}
	if !have {
		return o, false, nil
	}
	if o.Output != position {
		return o, false, fmt.Errorf("%w: output key mismatch", ErrSatStateCorrupt)
	}
	err = e.hydrate(&o)
	return o, true, err
}
func (e *satEditor077) pin(ref outputFactRef, add bool) error {
	var n uint64
	have, err := e.pins.get(ref.Page, &n)
	if err != nil {
		return satCorruptRead077(e.pins.ctx, "fact pins", err)
	}
	if add {
		if n == math.MaxUint64 {
			return fmt.Errorf("Sat pin overflow")
		}
		return e.pins.put(ref.Page, n+1)
	}
	if !have || n == 0 {
		return fmt.Errorf("%w: missing fact pin", ErrSatStateCorrupt)
	}
	if n == 1 {
		e.pins.remove(ref.Page)
		return nil
	}
	return e.pins.put(ref.Page, n-1)
}
func (e *satEditor077) locate(ranges []satRange, output, state string, height int64, hash string, offset uint64) error {
	for i, r := range ranges {
		if i%1024 == 0 {
			if err := e.s.ordinalContext().Err(); err != nil {
				return err
			}
		}
		l := satIndexLocation{Start: r.Start, End: r.End, Output: output, Offset: offset, State: state}
		if state == "lost" {
			l.LostHeight, l.LostHash = height, hash
		}
		if err := e.locations.put(satRangeKey(r.Start), l); err != nil {
			return err
		}
		offset += r.End - r.Start
	}
	return nil
}
func (e *satEditor077) remove(o satIndexOutput) error {
	k, err := satOutputKey077(o.Output)
	if err != nil {
		return err
	}
	e.outputs.remove(k)
	if err = e.heightCount(o.Point.Height, false); err != nil {
		return err
	}
	if err = e.pin(*o.FactRef, false); err != nil {
		return err
	}
	nativeKey := utxoTransactionKey(o.Point.Height, o.Point.TxIndex)
	var n satNative077
	have, err := e.native.get(nativeKey, &n)
	if err != nil || !have || n.Count == 0 {
		return fmt.Errorf("%w: native output count", ErrSatStateCorrupt)
	}
	if n.Count == 1 {
		e.native.remove(nativeKey)
	} else {
		n.Count--
		if err = e.native.put(nativeKey, n); err != nil {
			return err
		}
	}
	e.bridge.remove(ordinalOutpointKey(o.Point.TxID, o.Point.Vout))
	e.snapshot.Outputs--
	e.snapshot.LiveOutputs--
	return nil
}
func (e *satEditor077) add(o satIndexOutput, txid string, historical bool) error {
	key, err := satOutputKey077(o.Output)
	if err != nil {
		return err
	}
	if err = e.outputs.put(key, o); err != nil {
		return err
	}
	coordinate, _ := satPosition077(o.Output)
	if err = e.heightCount(coordinate.Height, true); err != nil {
		return err
	}
	if err = e.pin(*o.FactRef, true); err != nil {
		return err
	}
	c, _ := satPosition077(o.Output)
	nativeKey := utxoTransactionKey(c.Height, c.TxIndex)
	var n satNative077
	have, err := e.native.get(nativeKey, &n)
	if err != nil {
		return satCorruptRead077(e.native.ctx, "native catalogue", err)
	}
	if have && n.TxID != txid {
		return fmt.Errorf("%w: native transaction collision", ErrSatStateCorrupt)
	}
	n.TxID = txid
	n.Count++
	if err = e.native.put(nativeKey, n); err != nil {
		return err
	}
	if err = e.bridge.put(ordinalOutpointKey(txid, uint32(c.IOIndex)), o.Output); err != nil {
		return err
	}
	e.snapshot.Outputs++
	if o.State == "unspent" {
		e.snapshot.LiveOutputs++
	}
	if historical {
		historicalOutput := o
		historicalOutput.State = "historical"
		if err = e.history.put(key, historicalOutput); err != nil {
			return err
		}
		if err = e.pin(*o.FactRef, true); err != nil {
			return err
		}
	}
	return nil
}
func (e *satEditor077) flush() error {
	if err := e.updateCoverage(); err != nil {
		return err
	}
	pairs := []struct {
		t *ordinalTree
		p *string
	}{{e.outputs, &e.snapshot.UTXORoot}, {e.locations, &e.snapshot.LocationRoot}, {e.bridge, &e.snapshot.BridgeRoot}, {e.native, &e.snapshot.TransactionRoot}, {e.pins, &e.snapshot.FactPinRoot}, {e.history, &e.snapshot.HistoricalOutputRoot}, {e.ids, &e.snapshot.InscriptionRoot}, {e.bySat, &e.snapshot.InscriptionBySatRoot}, {e.counts, &e.snapshot.OutputCountsRoot}}
	for _, p := range pairs {
		r, err := p.t.flush()
		if err != nil {
			return err
		}
		*p.p = r
	}
	return nil
}

func (s *indexStore) deriveSat077Block(block blockView, prepared *inscriptionBlockResult) (*satIndexSnapshot, error) {
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
