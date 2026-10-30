package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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
		return c, fmt.Errorf("%w: invalid blocator output", ErrSatStateCorrupt)
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
	program, err := s.recipeSatProgram()
	if err != nil {
		return nil, err
	}
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
	p.RetainHistory = len(program.definition.Tables) == 2
	p.Movements = nil
	e := newSatEditor077(ctx, s, p)
	var refs map[string]outputFactRef
	if s.recipeSatPrivate {
		refs, err = storeBlockOutputFactLayersUncachedContext(ctx, s.profileRoot, block, true, true)
	} else {
		refs, err = storeBlockOutputFactsContext(ctx, s.profileRoot, block)
	}
	if err != nil {
		return nil, err
	}
	occurrences := map[string][]inscriptionOccurrence{}
	var extraction inscriptionBlockResult
	if s.recipeSatPrivate && prepared == nil && block.inscriptionExtraction != nil {
		if err = validateCompleteInscriptionInventory(block, block.inscriptionExtraction); err != nil {
			return nil, err
		}
		prepared = block.inscriptionExtraction
	}
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
	adapter := &recipeSatAdapter{e: e, block: block, facts: refs, inputValues: make([][]uint64, len(block.Transactions)), created: map[string]bool{}, historical: map[string]bool{}, identities: map[string]bool{}}
	defer adapter.AbortOrdered()
	source := func(emit func(map[string]recipeOrderedValue) error) error {
		return adapter.walk(ctx, occurrences, emit)
	}
	if err = recipeOrderedExecuteStateStream(ctx, *program.ordered, adapter, source, adapter.emit); err != nil {
		return nil, err
	}
	if err = storeSatFeeBlockContext(ctx, s.profileRoot, block, adapter.inputValues); err != nil {
		return nil, err
	}
	if err = e.flush(); err != nil {
		return nil, err
	}
	return &e.snapshot, nil
}
