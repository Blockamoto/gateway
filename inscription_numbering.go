package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// Rules pinned to the same Ord source as envelope extraction. Number assignment
// follows output placement order, including fee inscriptions delayed until the
// block's coinbase, not envelope extraction order or a local occurrence count.
const inscriptionNumberingProfile = "ord-03c9b87b14fe1e2c38191b860a08988345ec4e5f-mainnet-numbering-v1"
const firstMainnetInscriptionHeight int64 = 767430
const mainnetJubileeHeight int64 = 824544

type inscriptionNumberingSnapshot struct {
	Profile     string `json:"profile"`
	State       string `json:"state"`
	Reason      string `json:"reason,omitempty"`
	StateRoot   string `json:"state_root,omitempty"`
	RecordsRoot string `json:"records_root,omitempty"`
	Blessed     int64  `json:"blessed"`
	Cursed      int64  `json:"cursed"`
	Sequence    uint64 `json:"sequence"`
}
type numberedInscription struct {
	ID         string  `json:"id"`
	Number     int64   `json:"number"`
	Sequence   uint64  `json:"sequence"`
	Vindicated bool    `json:"vindicated,omitempty"`
	Unbound    bool    `json:"unbound,omitempty"`
	Sat        *uint64 `json:"sat_number,omitempty"`
	Height     int64   `json:"height"`
	BlockHash  string  `json:"block_hash"`
}
type inscriptionAtOffset struct {
	ID       string `json:"id"`
	Offset   uint64 `json:"offset"`
	Sequence uint64 `json:"sequence"`
}
type inscriptionNumberingOutput struct {
	Value        uint64                `json:"value"`
	Inscriptions []inscriptionAtOffset `json:"inscriptions"`
}
type floatingInscription struct {
	id                          string
	offset                      uint64
	sequence                    uint64
	occurrence                  *inscriptionOccurrence
	cursed, vindicated, unbound bool
}

func ordinalInscriptionKey(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

func (s *indexStore) deriveInscriptionNumbering(block blockView, from int64, occurrences []inscriptionOccurrence) (*inscriptionNumberingSnapshot, error) {
	return s.deriveInscriptionNumberingWithBase(block, from, occurrences, nil)
}
func (s *indexStore) deriveInscriptionNumberingWithBase(block blockView, from int64, occurrences []inscriptionOccurrence, base *inscriptionNumberingSnapshot) (*inscriptionNumberingSnapshot, error) {
	state := inscriptionNumberingSnapshot{Profile: inscriptionNumberingProfile, State: "complete"}
	unavailable := func(reason string) *inscriptionNumberingSnapshot {
		state.State = "unavailable"
		state.Reason = reason
		for i := range occurrences {
			occurrences[i].CanonicalNumberKnown = false
			occurrences[i].CanonicalNumber = nil
			occurrences[i].NumberingProfile = inscriptionNumberingProfile
			occurrences[i].NumberingState = reason
		}
		return &state
	}
	if from > firstMainnetInscriptionHeight {
		return unavailable("partial_history"), nil
	}
	if base != nil {
		state = *base
	} else if s.checkpoint != nil && s.checkpoint.Height >= firstMainnetInscriptionHeight {
		previous, e := s.readBatch(s.checkpoint.Commitment)
		if e != nil {
			return nil, e
		}
		if previous.Numbering == nil || previous.Numbering.Profile != inscriptionNumberingProfile || previous.Numbering.State != "complete" {
			enrichment, e := s.loadNumberingEnrichment()
			if e != nil {
				return nil, e
			}
			if enrichment == nil || enrichment.Source.Commitment != s.checkpoint.Commitment || enrichment.Snapshot.State != "complete" {
				return unavailable("prior_numbering_gap; historical_enrichment_required"), nil
			}
			state = enrichment.Snapshot
		} else {
			state = *previous.Numbering
		}
	}
	if block.Height < firstMainnetInscriptionHeight {
		return &state, nil
	}
	states := newOrdinalTree(s.ordinalPages(), state.StateRoot).withContext(s.ordinalContext())
	records := newOrdinalTree(s.ordinalPages(), state.RecordsRoot).withContext(s.ordinalContext())
	byTx := map[string][]*inscriptionOccurrence{}
	for i := range occurrences {
		o := &occurrences[i]
		byTx[o.TxID] = append(byTx[o.TxID], o)
	}
	// Nothing needs external input values until a block introduces or moves an
	// inscription. Inspecting occupancy is a local bounded page lookup.
	active := len(occurrences) > 0
	if !active {
		for _, tx := range block.Transactions {
			for _, in := range tx.Inputs {
				if in.Coinbase {
					continue
				}
				var prior inscriptionNumberingOutput
				ok, e := states.get(ordinalOutpointKey(in.PrevTxID, in.PrevVout), &prior)
				if e != nil {
					return nil, e
				}
				if ok && len(prior.Inscriptions) > 0 {
					active = true
					break
				}
			}
			if active {
				break
			}
		}
	}
	if !active {
		return &state, nil
	}
	if s.numberingValues == nil {
		return unavailable("verified_input_values_unavailable"), nil
	}
	if len(block.Transactions) == 0 || !block.Transactions[0].Coinbase {
		return unavailable("coinbase_evidence_unavailable"), nil
	}
	// Resolve every input before mutating the per-block trees. Fee ordering may
	// depend on transactions with no inscription. Missing values leave the
	// useful occurrence scan intact and never commit a canonical-number claim.
	values := map[string][]uint64{}
	created := map[string]uint64{}
	for ti, tx := range block.Transactions {
		if ti > 0 {
			vals, e := s.numberingValues(tx, block.Height)
			if e != nil {
				return unavailable("verified_input_dependency: " + e.Error()), nil
			}
			if len(vals) != len(tx.Inputs) {
				return unavailable("verified_input_count_mismatch"), nil
			}
			for vi, in := range tx.Inputs {
				if v, ok := created[ordinalOutpointKey(in.PrevTxID, in.PrevVout)]; ok {
					vals[vi] = v
				}
			}
			values[tx.TxID] = vals
		}
		for i, out := range tx.Outputs {
			created[ordinalOutpointKey(tx.TxID, uint32(i))] = out.ValueSats
		}
	}
	reward := subsidyAtHeight(block.Height)
	feeInscriptions := []floatingInscription{}
	place := func(f floatingInscription) (inscriptionAtOffset, error) {
		if f.occurrence == nil {
			return inscriptionAtOffset{f.id, 0, f.sequence}, nil
		}
		number := state.Blessed
		if f.cursed {
			state.Cursed++
			number = -state.Cursed
		} else {
			state.Blessed++
		}
		r := numberedInscription{ID: f.id, Number: number, Sequence: state.Sequence, Vindicated: f.vindicated, Unbound: f.unbound, Height: block.Height, BlockHash: block.Hash}
		state.Sequence++
		if e := records.put(ordinalInscriptionKey(f.id), r); e != nil {
			return inscriptionAtOffset{}, e
		}
		o := f.occurrence
		o.CanonicalNumberKnown = true
		o.CanonicalNumber = &r.Number
		o.NumberingProfile = inscriptionNumberingProfile
		o.NumberingState = "canonical_at_snapshot"
		return inscriptionAtOffset{f.id, 0, r.Sequence}, nil
	}
	process := func(tx transactionView, isCoinbase bool) error {
		floating := []floatingInscription{}
		var inputTotal uint64
		outputTotal, e := sumOutputs(tx)
		if e != nil {
			return e
		}
		type occupancy struct {
			id    string
			count int
		}
		occupied := map[uint64]occupancy{}
		if isCoinbase {
			floating = append(floating, feeInscriptions...)
			inputTotal = reward
		} else {
			for vi, in := range tx.Inputs {
				key := ordinalOutpointKey(in.PrevTxID, in.PrevVout)
				var old inscriptionNumberingOutput
				ok, e := states.get(key, &old)
				if e != nil {
					return e
				}
				if ok {
					states.remove(key)
					sort.SliceStable(old.Inscriptions, func(i, j int) bool { return old.Inscriptions[i].Sequence < old.Inscriptions[j].Sequence })
					for _, entry := range old.Inscriptions {
						offset, e := addUint64(inputTotal, entry.Offset)
						if e != nil {
							return e
						}
						floating = append(floating, floatingInscription{id: entry.ID, offset: offset, sequence: entry.Sequence})
						x := occupied[offset]
						if x.count == 0 {
							x.id = entry.ID
						}
						x.count++
						occupied[offset] = x
					}
				}
				value := values[tx.TxID][vi]
				offset := inputTotal
				inputTotal, e = addUint64(inputTotal, value)
				if e != nil {
					return e
				}
				for _, o := range byTx[tx.TxID] {
					if o.InputIndex != vi {
						continue
					}
					env := o.Envelope
					curse := ""
					switch {
					case env.Unbound:
						curse = "unrecognized_even_field"
					case env.Duplicate:
						curse = "duplicate_field"
					case env.Incomplete:
						curse = "incomplete_field"
					case o.InputIndex != 0:
						curse = "not_in_first_input"
					case o.EnvelopeIndex != 0:
						curse = "not_at_offset_zero"
					case env.Pointer != nil:
						curse = "pointer"
					case env.Pushnum:
						curse = "pushnum"
					case env.Stutter:
						curse = "stutter"
					default:
						if existing := occupied[offset]; existing.count > 0 {
							if existing.count > 1 {
								curse = "reinscription"
							} else {
								var initial numberedInscription
								known, e := records.get(ordinalInscriptionKey(existing.id), &initial)
								if e != nil {
									return e
								}
								if !known {
									return fmt.Errorf("historical reinscription prerequisite missing")
								}
								if initial.Number >= 0 && !initial.Vindicated {
									curse = "reinscription"
								}
							}
						}
					}
					position := offset
					if env.Pointer != nil && *env.Pointer < outputTotal {
						position = *env.Pointer
					}
					jubilee := block.Height >= mainnetJubileeHeight
					floating = append(floating, floatingInscription{id: o.ID, offset: position, occurrence: o, cursed: curse != "" && !jubilee, vindicated: curse != "" && jubilee, unbound: value == 0 || env.Unbound})
					x := occupied[position]
					if x.count == 0 {
						x.id = o.ID
					}
					x.count++
					occupied[position] = x
				}
			}
		}
		if outputTotal > inputTotal {
			return fmt.Errorf("numbering outputs exceed verified inputs")
		}
		sort.SliceStable(floating, func(i, j int) bool { return floating[i].offset < floating[j].offset })
		var consumed uint64
		cursor := 0
		for i, out := range tx.Outputs {
			end, e := addUint64(consumed, out.ValueSats)
			if e != nil {
				return e
			}
			entry := inscriptionNumberingOutput{Value: out.ValueSats}
			for cursor < len(floating) && floating[cursor].offset < end {
				f := floating[cursor]
				cursor++
				located, e := place(f)
				if e != nil {
					return e
				}
				if f.unbound {
					continue
				}
				located.Offset = f.offset - consumed
				entry.Inscriptions = append(entry.Inscriptions, located)
			}
			if len(entry.Inscriptions) > 0 {
				if e = states.put(ordinalOutpointKey(tx.TxID, uint32(i)), entry); e != nil {
					return e
				}
			}
			consumed = end
		}
		for _, f := range floating[cursor:] {
			if isCoinbase {
				if _, e := place(f); e != nil {
					return e
				}
			} else {
				f.offset = reward + f.offset - outputTotal
				feeInscriptions = append(feeInscriptions, f)
			}
		}
		if !isCoinbase {
			reward, e = addUint64(reward, inputTotal-outputTotal)
			if e != nil {
				return e
			}
		}
		return nil
	}
	for i, tx := range block.Transactions {
		if i > 0 {
			if e := process(tx, false); e != nil {
				return unavailable("numbering_dependency: " + e.Error()), nil
			}
		}
	}
	if e := process(block.Transactions[0], true); e != nil {
		return unavailable("numbering_dependency: " + e.Error()), nil
	}
	var e error
	state.StateRoot, e = states.flush()
	if e != nil {
		return nil, e
	}
	state.RecordsRoot, e = records.flush()
	if e != nil {
		return nil, e
	}
	return &state, nil
}

// The resolver already prefers local verified block/index evidence and only
// falls back to providers for missing transactions. No retention or publication
// setting is changed by needing historical values.
func (a *app) indexNumberingValues(ctx context.Context, tx transactionView, height int64) ([]uint64, error) {
	r := newSatlineResolver(contextSatlineBackend{ctx: ctx, appSatlineBackend: appSatlineBackend{a: a}})
	r.ctx = ctx
	r.primeInputValues(tx, height)
	values := make([]uint64, len(tx.Inputs))
	for i, in := range tx.Inputs {
		v, e := r.prevoutValue(in.PrevTxID, in.PrevVout, height)
		if e != nil {
			return nil, e
		}
		values[i] = v
	}
	return values, nil
}

func (a *app) indexNumberingBlockValues(ctx context.Context, block blockView) func(transactionView, int64) ([]uint64, error) {
	r := newSatlineResolver(contextSatlineBackend{appSatlineBackend{a}, ctx})
	r.ctx = ctx
	r.cacheBlock(block)
	return func(tx transactionView, height int64) ([]uint64, error) {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		r.primeInputValues(tx, height)
		values := make([]uint64, len(tx.Inputs))
		for i, in := range tx.Inputs {
			value, e := r.prevoutValue(in.PrevTxID, in.PrevVout, height)
			if e != nil {
				return nil, e
			}
			values[i] = value
		}
		return values, nil
	}
}
