package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const satIndexProfile = "gateway-sat-fifo-mainnet-v1"
const ordinalRecoveryBlocks = 144

type satRange struct {
	Start uint64 `json:"start"`
	End   uint64 `json:"end"`
}
type satIndexOutput struct {
	Point      satlinePoint `json:"point"`
	Value      uint64       `json:"value"`
	Ranges     []satRange   `json:"ranges"`
	RangesRoot string       `json:"ranges_root,omitempty"`
	State      string       `json:"state"`
}
type satIndexLocation struct {
	Start uint64       `json:"start"`
	End   uint64       `json:"end"`
	Point satlinePoint `json:"point"`
	State string       `json:"state"`
}
type satIndexSnapshot struct {
	DuplicateCoinbases []satIndexIncarnation `json:"duplicate_coinbases,omitempty"`
	InscriptionRoot    string                `json:"inscription_root,omitempty"`
	InscriptionState   string                `json:"inscription_state,omitempty"`
	Movements          []satIndexMovement    `json:"movements,omitempty"`
	HistoryCoverage    []heightInterval      `json:"history_coverage,omitempty"`
	Profile            string                `json:"profile"`
	UTXORoot           string                `json:"utxo_root"`
	LocationRoot       string                `json:"location_root"`
	LostSats           uint64                `json:"lost_sats"`
	DestroyedSats      uint64                `json:"destroyed_sats"`
	Outputs            uint64                `json:"outputs"`
	RetainHistory      bool                  `json:"retain_history"`
}
type satIndexIncarnation struct {
	TxID   string `json:"txid"`
	Height int64  `json:"height"`
}
type satIndexMovement struct {
	TxID     string             `json:"txid"`
	Index    int                `json:"index"`
	Coinbase bool               `json:"coinbase"`
	Inputs   []satMovementInput `json:"inputs,omitempty"`
	Outputs  []uint64           `json:"output_values"`
}
type satMovementInput struct {
	TxID  string `json:"txid"`
	Vout  uint32 `json:"vout"`
	Value uint64 `json:"value"`
}
type satIndexAnswer struct {
	Sat              uint64           `json:"sat_number"`
	Known            bool             `json:"known"`
	State            string           `json:"state"`
	Point            *satlinePoint    `json:"satpoint,omitempty"`
	Origin           *satlineIssuance `json:"origin,omitempty"`
	Snapshot         *indexCheckpoint `json:"snapshot,omitempty"`
	ChainState       string           `json:"chain_state"`
	HistoryRetained  bool             `json:"history_retained"`
	HistoryRecording bool             `json:"history_recording"`
	HistoryCoverage  []heightInterval `json:"history_coverage"`
	Note             string           `json:"note"`
}

func ordinalOutpointKey(txid string, vout uint32) string {
	// Exact lookup only: distribute same-transaction outputs instead of
	// rewriting a 64-nibble common txid prefix once an output set splits.
	sum := sha256.Sum256([]byte(satlineOutpointKey(txid, vout)))
	return hex.EncodeToString(sum[:])
}
func satRangeKey(sat uint64) string        { return fmt.Sprintf("%016x", sat) }
func (s *indexStore) ordinalPages() string { return filepath.Join(s.dir, "ordinal-pages") }
func (s *indexStore) ordinalContext() context.Context {
	if s.locatorContext != nil {
		return s.locatorContext
	}
	return context.Background()
}
func satRangesValue(ranges []satRange) (uint64, error) {
	return satRangesValueContext(context.Background(), ranges)
}
func satRangesValueContext(ctx context.Context, ranges []satRange) (uint64, error) {
	var total uint64
	for i, r := range ranges {
		if i%1024 == 0 {
			if e := ctx.Err(); e != nil {
				return 0, e
			}
		}
		if r.End <= r.Start || r.End > theoreticalSatSupply() {
			return 0, fmt.Errorf("invalid sat interval")
		}
		var e error
		total, e = addUint64(total, r.End-r.Start)
		if e != nil {
			return 0, e
		}
	}
	return total, nil
}

// Fragmentation is historical, not bounded by the size of the transaction
// spending an output. Large interval sequences therefore live in independent
// radix pages, never in one oversized UTXO JSON value.
func (s *indexStore) storeSatOutput(output satIndexOutput) (satIndexOutput, error) {
	if len(output.Ranges) <= 64 {
		return output, nil
	}
	tree := newOrdinalTree(s.ordinalPages(), "").withContext(s.ordinalContext())
	for i := 0; i < len(output.Ranges); i += 256 {
		end := i + 256
		if end > len(output.Ranges) {
			end = len(output.Ranges)
		}
		if e := tree.put(satRangeKey(uint64(i)), output.Ranges[i:end]); e != nil {
			return output, e
		}
	}
	root, e := tree.flush()
	if e != nil {
		return output, e
	}
	output.Ranges = nil
	output.RangesRoot = root
	return output, nil
}
func loadSatOutputRanges(dir string, output satIndexOutput) ([]satRange, error) {
	return loadSatOutputRangesContext(context.Background(), dir, output)
}
func loadSatOutputRangesContext(ctx context.Context, dir string, output satIndexOutput) ([]satRange, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if output.RangesRoot == "" {
		return output.Ranges, nil
	}
	if len(output.Ranges) != 0 {
		return nil, fmt.Errorf("ambiguous sat interval storage")
	}
	tree := newOrdinalTree(dir, output.RangesRoot).withContext(ctx)
	ranges := []satRange{}
	var expected uint64
	e := tree.walk(func(kv ordinalKV) error {
		if kv.Key != satRangeKey(expected) {
			return fmt.Errorf("segmented sat interval gap")
		}
		var chunk []satRange
		if json.Unmarshal(kv.Value, &chunk) != nil || len(chunk) == 0 || len(chunk) > 256 {
			return fmt.Errorf("invalid sat interval segment")
		}
		ranges = append(ranges, chunk...)
		expected += uint64(len(chunk))
		return nil
	})
	return ranges, e
}

// takeSatRanges consumes intervals in transaction input order. Numeric sat
// order is deliberately irrelevant: ordinal movement is FIFO by input order.
func takeSatRanges(stream *[]satRange, count uint64) ([]satRange, error) {
	return takeSatRangesContext(context.Background(), stream, count)
}
func takeSatRangesContext(ctx context.Context, stream *[]satRange, count uint64) ([]satRange, error) {
	out := []satRange{}
	for count > 0 {
		if len(out)%1024 == 0 {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
		}
		if len(*stream) == 0 {
			return nil, fmt.Errorf("transaction outputs exceed resolved input sats")
		}
		r := (*stream)[0]
		n := r.End - r.Start
		if n <= count {
			out = append(out, r)
			*stream = (*stream)[1:]
			count -= n
		} else {
			out = append(out, satRange{r.Start, r.Start + count})
			(*stream)[0].Start += count
			count = 0
		}
	}
	return out, nil
}
func (s *indexStore) deriveSatBlock(block blockView) (*satIndexSnapshot, error) {
	return s.deriveSatPreparedBlock(block, nil)
}
func (s *indexStore) deriveSatPreparedBlock(block blockView, prepared *inscriptionBlockResult) (*satIndexSnapshot, error) {
	if e := s.ordinalContext().Err(); e != nil {
		return nil, e
	}
	if prepared != nil && (prepared.BlockHash != block.Hash || prepared.BlockHeight != block.Height || prepared.ParserProfile != inscriptionParserProfile) {
		return nil, fmt.Errorf("prepared inscription evidence does not match Sat block")
	}
	if block.Height < 0 || !integrityVerified(block) || !block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated || len(block.Transactions) == 0 || !block.Transactions[0].Coinbase {
		return nil, fmt.Errorf("Sat index requires an anchored complete Bitcoin block with coinbase")
	}
	snapshot := satIndexSnapshot{Profile: satIndexProfile, RetainHistory: s.retainSatHistory}
	if s.checkpoint != nil {
		previous, e := s.readBatch(s.checkpoint.Commitment)
		if e != nil {
			return nil, e
		}
		if previous.Sats == nil || previous.Sats.Profile != satIndexProfile {
			return nil, fmt.Errorf("Sat index prerequisite snapshot is missing or incompatible")
		}
		snapshot = *previous.Sats
		snapshot.RetainHistory = s.retainSatHistory
	} else if block.Height != 0 {
		return nil, fmt.Errorf("Sat index requires continuous history from genesis; isolated blocks cannot establish input sat identity")
	}
	utxos := newOrdinalTree(s.ordinalPages(), snapshot.UTXORoot).withContext(s.ordinalContext())
	locations := newOrdinalTree(s.ordinalPages(), snapshot.LocationRoot).withContext(s.ordinalContext())
	identities := newOrdinalTree(s.ordinalPages(), snapshot.InscriptionRoot).withContext(s.ordinalContext())
	snapshot.Movements = nil
	if block.Height == 91842 || block.Height == 91880 {
		snapshot.DuplicateCoinbases = append(snapshot.DuplicateCoinbases, satIndexIncarnation{block.Transactions[0].TxID, block.Height})
	}
	if snapshot.RetainHistory {
		snapshot.HistoryCoverage = mergeIntervals(append(snapshot.HistoryCoverage, heightInterval{From: block.Height, To: block.Height}))
	}
	byTx := map[string][]inscriptionOccurrence{}
	var extracted inscriptionBlockResult
	var extractionErr error
	if prepared != nil {
		extracted = *prepared
	} else {
		extracted, extractionErr = extractInscriptionOccurrences(block)
	}
	snapshot.InscriptionState = "complete_this_block"
	if extractionErr != nil {
		snapshot.InscriptionState = "identity_extraction_unavailable: " + extractionErr.Error()
	} else {
		for _, o := range extracted.Occurrences {
			byTx[o.TxID] = append(byTx[o.TxID], o)
		}
	}
	coinbase := []satRange{}
	if subsidy := subsidyAtHeight(block.Height); subsidy > 0 {
		first := firstSatAtHeight(block.Height)
		coinbase = append(coinbase, satRange{first, first + subsidy})
	}
	locate := func(ranges []satRange, point satlinePoint, state string) error {
		for _, r := range ranges {
			loc := satIndexLocation{Start: r.Start, End: r.End, Point: point, State: state}
			if e := locations.put(satRangeKey(r.Start), loc); e != nil {
				return e
			}
			point.Offset += r.End - r.Start
		}
		return nil
	}
	process := func(tx transactionView, isCoinbase bool) error {
		if e := s.ordinalContext().Err(); e != nil {
			return e
		}
		if !validHash(tx.TxID) || tx.Coinbase != isCoinbase {
			return fmt.Errorf("invalid Sat index transaction identity or coinbase position")
		}
		stream := []satRange{}
		movement := satIndexMovement{TxID: tx.TxID, Index: tx.Index, Coinbase: isCoinbase}
		inputValues := []uint64{}
		if isCoinbase {
			stream = append(stream, coinbase...)
		} else {
			for _, in := range tx.Inputs {
				if in.Coinbase || !validHash(in.PrevTxID) {
					return fmt.Errorf("invalid Sat index input")
				}
				key := ordinalOutpointKey(in.PrevTxID, in.PrevVout)
				var old satIndexOutput
				ok, e := utxos.get(key, &old)
				if e != nil {
					return e
				}
				if !ok {
					return fmt.Errorf("Sat index missing historical input %s:%d; no partial current-state claim is possible", in.PrevTxID, in.PrevVout)
				}
				if old.State == "genesis_unspendable" || old.State == "burned" {
					return fmt.Errorf("Sat index cannot spend a provably unspendable output")
				}
				old.Ranges, e = loadSatOutputRangesContext(s.ordinalContext(), s.ordinalPages(), old)
				if e != nil {
					return e
				}
				value, e := satRangesValueContext(s.ordinalContext(), old.Ranges)
				if e != nil {
					return e
				}
				if value != old.Value {
					return fmt.Errorf("Sat index input interval/value mismatch")
				}
				stream = append(stream, old.Ranges...)
				inputValues = append(inputValues, old.Value)
				if snapshot.RetainHistory {
					movement.Inputs = append(movement.Inputs, satMovementInput{in.PrevTxID, in.PrevVout, old.Value})
				}
				utxos.remove(key)
				snapshot.Outputs--
				for i, r := range old.Ranges {
					if i%1024 == 0 {
						if e := s.ordinalContext().Err(); e != nil {
							return e
						}
					}
					locations.remove(satRangeKey(r.Start))
				}
			}
		}
		if !isCoinbase {
			total, e := sumOutputs(tx)
			if e != nil {
				return e
			}
			for _, o := range byTx[tx.TxID] {
				if o.InputIndex < 0 || o.InputIndex >= len(inputValues) {
					return fmt.Errorf("inscription input position is outside resolved sat stream")
				}
				identity := inscriptionSatResult{ID: o.ID, Profile: inscriptionSatProfile, State: "unbound", RevealHeight: block.Height, RevealHash: block.Hash, Note: "Derived with continuous Sat index FIFO state; current ownership is separate."}
				if !o.Envelope.Unbound && inputValues[o.InputIndex] > 0 {
					position := uint64(0)
					for i := 0; i < o.InputIndex; i++ {
						position += inputValues[i]
					}
					if o.Envelope.Pointer != nil && *o.Envelope.Pointer < total {
						position = *o.Envelope.Pointer
					}
					for i, interval := range stream {
						if i%1024 == 0 {
							if e := s.ordinalContext().Err(); e != nil {
								return e
							}
						}
						if position < interval.End-interval.Start {
							sat := interval.Start + position
							identity.SatNumber = &sat
							identity.Known = true
							identity.State = "known"
							break
						}
						position -= interval.End - interval.Start
					}
					if !identity.Known {
						return fmt.Errorf("inscription sat offset exceeds resolved stream")
					}
				}
				if e = identities.put(ordinalInscriptionKey(o.ID), identity); e != nil {
					return e
				}
			}
		}
		for i, out := range tx.Outputs {
			if snapshot.RetainHistory {
				movement.Outputs = append(movement.Outputs, out.ValueSats)
			}
			if out.N != i {
				return fmt.Errorf("Sat index output order mismatch")
			}
			ranges, e := takeSatRangesContext(s.ordinalContext(), &stream, out.ValueSats)
			if e != nil {
				return e
			}
			key := ordinalOutpointKey(tx.TxID, uint32(i))
			var replaced satIndexOutput
			if ok, e := utxos.get(key, &replaced); e != nil {
				return e
			} else if ok {
				if !isCoinbase || block.Height != 91842 && block.Height != 91880 {
					return fmt.Errorf("unexpected duplicate unspent outpoint")
				}
				replaced.Ranges, e = loadSatOutputRangesContext(s.ordinalContext(), s.ordinalPages(), replaced)
				if e != nil {
					return e
				}
				for i, r := range replaced.Ranges {
					if i%1024 == 0 {
						if e := s.ordinalContext().Err(); e != nil {
							return e
						}
					}
					locations.remove(satRangeKey(r.Start))
				}
				if e = locate(replaced.Ranges, replaced.Point, "bip30_destroyed"); e != nil {
					return e
				}
				snapshot.DestroyedSats += replaced.Value
				snapshot.Outputs--
			}
			state := "unspent"
			if block.Height == 0 {
				state = "genesis_unspendable"
			} else if strings.HasPrefix(strings.ToLower(out.ScriptPubKey), "6a") {
				state = "burned"
			}
			point := satlinePoint{TxID: tx.TxID, Vout: uint32(i), Height: block.Height, BlockHash: block.Hash, TxIndex: tx.Index}
			stored, e := s.storeSatOutput(satIndexOutput{Point: point, Value: out.ValueSats, Ranges: ranges, State: state})
			if e != nil {
				return e
			}
			if e = utxos.put(key, stored); e != nil {
				return e
			}
			snapshot.Outputs++
			if e = locate(ranges, point, state); e != nil {
				return e
			}
		}
		if snapshot.RetainHistory {
			snapshot.Movements = append(snapshot.Movements, movement)
		}
		if !isCoinbase {
			coinbase = append(coinbase, stream...)
		} else {
			point := satlinePoint{TxID: strings.Repeat("0", 64), Vout: ^uint32(0), Offset: snapshot.LostSats, Height: block.Height, BlockHash: block.Hash}
			if e := locate(stream, point, "lost"); e != nil {
				return e
			}
			value, e := satRangesValueContext(s.ordinalContext(), stream)
			if e != nil {
				return e
			}
			snapshot.LostSats += value
		}
		return nil
	}
	for i, tx := range block.Transactions {
		if tx.Index != i {
			return nil, fmt.Errorf("Sat index transaction order mismatch")
		}
		if i > 0 {
			if e := process(tx, false); e != nil {
				return nil, e
			}
		}
	}
	if e := process(block.Transactions[0], true); e != nil {
		return nil, e
	}
	var e error
	snapshot.UTXORoot, e = utxos.flush()
	if e != nil {
		return nil, e
	}
	snapshot.LocationRoot, e = locations.flush()
	if e != nil {
		return nil, e
	}
	snapshot.InscriptionRoot, e = identities.flush()
	if e != nil {
		return nil, e
	}
	if snapshot.RetainHistory {
		// Persist archive liveness before the head can select this snapshot.
		// An interrupted orphan archive only delays reclamation; it cannot
		// create selected coverage or cause retained history to be deleted.
		dir := filepath.Join(s.dir, "ordinal-history")
		if e = atomicWriteJSON(filepath.Join(dir, indexDigest(snapshot)+".json"), []string{snapshot.UTXORoot, snapshot.LocationRoot, snapshot.InscriptionRoot}); e != nil {
			return nil, e
		}
		if e = syncDirectory(dir); e != nil {
			return nil, e
		}
		if e = syncDirectory(s.dir); e != nil {
			return nil, e
		}
	}
	return &snapshot, nil
}
func (a *app) indexedSat(ctx context.Context, sat uint64) (satIndexAnswer, error) {
	if err := requireReleaseFeature("sat-state"); err != nil {
		return satIndexAnswer{}, err
	}
	return a.readIndexedSat(ctx, sat)
}

// readIndexedSat validates an existing local snapshot. User-facing lookup must
// go through indexedSat so retained data cannot bypass release policy.
func (a *app) readIndexedSat(ctx context.Context, sat uint64) (satIndexAnswer, error) {
	out := satIndexAnswer{Sat: sat, State: "unknown", ChainState: "unavailable", Note: "A current placement requires a continuous Sat index snapshot on the selected chain."}
	origin, ok := issuanceForSat(sat)
	if !ok {
		return out, fmt.Errorf("sat number is outside Bitcoin's theoretical supply")
	}
	out.Origin = &origin
	if e := ctx.Err(); e != nil {
		return out, e
	}
	s, e := indexStoreHead(a.dataDir, "sat-state")
	if e != nil {
		return out, e
	}
	if s.checkpoint == nil {
		return out, nil
	}
	out.Snapshot = s.checkpoint
	out.ChainState = a.indexChainState(s.checkpoint)
	if out.ChainState != "selected_chain" {
		return out, nil
	}
	if origin.Height > s.checkpoint.Height {
		out.State = "not_issued_at_snapshot"
		return out, nil
	}
	b, e := s.readBatch(s.checkpoint.Commitment)
	if e != nil {
		return out, e
	}
	if b.Sats == nil {
		return out, fmt.Errorf("Sat index snapshot is missing")
	}
	var loc satIndexLocation
	t := newOrdinalTree(s.ordinalPages(), b.Sats.LocationRoot)
	ok, e = t.predecessor(satRangeKey(sat), &loc)
	if e != nil {
		return out, e
	}
	if !ok || sat < loc.Start || sat >= loc.End {
		return out, fmt.Errorf("Sat index snapshot has an unexplained identity gap")
	}
	point := loc.Point
	point.Offset += sat - loc.Start
	out.Known = true
	out.State = loc.State
	out.Point = &point
	out.HistoryRecording = b.Sats.RetainHistory
	out.HistoryCoverage = append([]heightInterval{}, b.Sats.HistoryCoverage...)
	out.HistoryRetained = len(out.HistoryCoverage) == 1 && out.HistoryCoverage[0].From == 0 && out.HistoryCoverage[0].To == s.checkpoint.Height
	out.Note = "Ordinal FIFO placement at the stated verified-chain snapshot. Header anchoring is not full script/UTXO consensus validation; later blocks may move this sat."
	return out, nil
}
func (s *indexStore) ordinalRoots(b indexBatch) []string {
	roots := []string{}
	if b.Sats != nil {
		roots = append(roots, b.Sats.UTXORoot, b.Sats.LocationRoot, b.Sats.InscriptionRoot)
	}
	if b.Numbering != nil {
		roots = append(roots, b.Numbering.StateRoot, b.Numbering.RecordsRoot)
	}
	return roots
}

func (a *app) indexInstanceNote(s *indexStore) string {
	if s.checkpoint == nil {
		return "No committed coverage."
	}
	if s.definition.ID == "sat-state" {
		return "Ordinal FIFO identity and placement at this selected-chain snapshot; it is not full Bitcoin script consensus validation. Raw blocks and full movement history are independent retention choices. Recent 144-block recovery state is essential; older recovery needs retained history or an explicit rebuild."
	}
	if s.definition.ID == "inscriptions" {
		if state, checkpoint, e := s.numberingView(); e == nil && state != nil && checkpoint != nil {
			return fmt.Sprintf("Canonical numbering is derived with pinned historical rules through block %d. Sat identity and ownership require their own checked evidence.", checkpoint.Height)
		}
		b, e := s.readBatch(s.checkpoint.Commitment)
		if e != nil {
			return "Inscription records require checkpoint recovery: " + e.Error()
		}
		if b.Numbering != nil && b.Numbering.State == "complete" {
			return "Stable inscription IDs and canonical numbers derived with the pinned historical rules through this checkpoint. Sat identity and ownership require their own checked evidence."
		}
		if b.Numbering != nil {
			return "Stable inscription IDs remain useful. Canonical numbers unavailable: " + b.Numbering.Reason
		}
		return "Existing occurrence records remain valid. Canonical numbering needs historical enrichment from the first inscription; an occurrence count is not a canonical number."
	}
	return "Private derived state. Coverage belongs to the committed range; missing dependencies are not inferred."
}

// Movement archives are independent of block retention. The recent recovery
// window is essential state, not a claim to retain complete historical lineage.
func (s *indexStore) collectOrdinalState() error {
	if s.checkpoint == nil || s.checkpoint.Height%256 != 255 && !ordinalCollectionPending(s.ordinalPages()) {
		return nil
	}
	ctx := s.locatorContext
	if ctx == nil {
		ctx = context.Background()
	}
	return collectOrdinalPagesContext(ctx, s.ordinalPages(), func(visit func(string) error) error {
		// The existing profile lock and one index writer serialize mutation,
		// enrichment, reconciliation and collection. Management readers remain
		// free to query; a no-longer-retained old snapshot fails closed.
		next := s.head.Commitment
		for n := 0; next != "" && n <= ordinalRecoveryBlocks; n++ {
			if e := ctx.Err(); e != nil {
				return e
			}
			b, e := s.readBatch(next)
			if e != nil {
				return e
			}
			for _, root := range s.ordinalRoots(b) {
				if e = visit(root); e != nil {
					return e
				}
			}
			next = b.Checkpoint.PreviousCommitment
		}
		dir := filepath.Join(s.dir, "ordinal-history")
		folder, e := os.Open(dir)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if e == nil {
			defer folder.Close()
			for {
				files, readErr := folder.ReadDir(128)
				for _, file := range files {
					if e = ctx.Err(); e != nil {
						return e
					}
					if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
						continue
					}
					data, e := os.ReadFile(filepath.Join(dir, file.Name()))
					if e != nil {
						return e
					}
					var retained []string
					if len(data) > 1024 || json.Unmarshal(data, &retained) != nil || len(retained) < 2 || len(retained) > 3 {
						return fmt.Errorf("ordinal history receipt needs repair before cleanup")
					}
					for _, root := range retained {
						if e = visit(root); e != nil {
							return e
						}
					}
				}
				if readErr == io.EOF {
					break
				}
				if readErr != nil {
					return readErr
				}
			}
		}
		if s.definition.ID == "inscriptions" {
			head, e := s.loadNumberingEnrichment()
			if e != nil {
				return e
			}
			for n := 0; head != nil && n <= ordinalRecoveryBlocks; n++ {
				if e = ctx.Err(); e != nil {
					return e
				}
				if e = visit(head.Snapshot.StateRoot); e != nil {
					return e
				}
				if e = visit(head.Snapshot.RecordsRoot); e != nil {
					return e
				}
				if head.Previous == "" {
					break
				}
				head, e = s.readNumberingEnrichment(head.Previous)
				if e != nil {
					return e
				}
			}
		}
		return nil
	})
}

func (s *indexStore) checkOrdinalRecovery(batch indexBatch) error {
	for _, root := range s.ordinalRoots(batch) {
		if root == "" {
			continue
		}
		tree := newOrdinalTree(s.ordinalPages(), root)
		if _, e := tree.read(root); e != nil {
			return fmt.Errorf("ordinal recovery snapshot at height %d is unavailable; restore retained movement history or rebuild from the required origin: %w", batch.Checkpoint.Height, e)
		}
	}
	return nil
}
