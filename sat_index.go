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

const satIndexProfile = "gateway-sat-fifo-mainnet-bps-v2"
const ordinalRecoveryBlocks = 144

type satRange struct {
	Start uint64 `json:"start"`
	End   uint64 `json:"end"`
}
type satIndexOutput struct {
	Output     string         `json:"output,omitempty"`
	FactRef    *outputFactRef `json:"facts,omitempty"`
	RangeCount uint64         `json:"range_count,omitempty"`
	Point      satlinePoint   `json:"point"`
	Value      uint64         `json:"value"`
	Ranges     []satRange     `json:"ranges"`
	RangesRoot string         `json:"ranges_root,omitempty"`
	State      string         `json:"state"`
}
type satIndexLocation struct {
	Output     string       `json:"output,omitempty"`
	Offset     uint64       `json:"offset,omitempty"`
	LostHeight int64        `json:"lost_height,omitempty"`
	LostHash   string       `json:"lost_hash,omitempty"`
	Start      uint64       `json:"start"`
	End        uint64       `json:"end"`
	Point      satlinePoint `json:"point"`
	State      string       `json:"state"`
}
type satIndexSnapshot struct {
	Layout               int                   `json:"layout,omitempty"`
	BridgeRoot           string                `json:"bridge_root,omitempty"`
	TransactionRoot      string                `json:"transaction_root,omitempty"`
	FactPinRoot          string                `json:"fact_pin_root,omitempty"`
	HistoricalOutputRoot string                `json:"historical_output_root,omitempty"`
	InscriptionBySatRoot string                `json:"inscription_by_sat_root,omitempty"`
	IdentityCoverage     []heightInterval      `json:"identity_coverage,omitempty"`
	LiveOutputs          uint64                `json:"live_outputs"`
	OutputCountsRoot     string                `json:"output_counts_root,omitempty"`
	OutputCoverage       []heightInterval      `json:"output_coverage,omitempty"`
	DuplicateCoinbases   []satIndexIncarnation `json:"duplicate_coinbases,omitempty"`
	InscriptionRoot      string                `json:"inscription_root,omitempty"`
	InscriptionState     string                `json:"inscription_state,omitempty"`
	Movements            []satIndexMovement    `json:"movements,omitempty"`
	HistoryCoverage      []heightInterval      `json:"history_coverage,omitempty"`
	Profile              string                `json:"profile"`
	UTXORoot             string                `json:"utxo_root"`
	LocationRoot         string                `json:"location_root"`
	LostSats             uint64                `json:"lost_sats"`
	DestroyedSats        uint64                `json:"destroyed_sats"`
	Outputs              uint64                `json:"outputs"`
	RetainHistory        bool                  `json:"retain_history"`
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
	output.RangeCount = uint64(len(output.Ranges))
	if len(output.Ranges) <= 64 {
		return output, nil
	}
	tree := newOrdinalTree(s.ordinalPages(), "").withContext(s.ordinalContext())
	offset := uint64(0)
	for i := 0; i < len(output.Ranges); i += 256 {
		end := i + 256
		if end > len(output.Ranges) {
			end = len(output.Ranges)
		}
		var chunk any = output.Ranges[i:end]
		if output.Output != "" {
			chunk = satRangeSegment077{Offset: offset, Ranges: output.Ranges[i:end]}
		}
		if e := tree.put(satRangeKey(uint64(i)), chunk); e != nil {
			return output, e
		}
		value, err := satRangesValueContext(s.ordinalContext(), output.Ranges[i:end])
		if err != nil {
			return output, err
		}
		offset += value
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
		if output.Output != "" && output.RangeCount != uint64(len(output.Ranges)) {
			return nil, fmt.Errorf("%w: inline range count", ErrSatStateCorrupt)
		}
		return output.Ranges, nil
	}
	if len(output.Ranges) != 0 {
		return nil, fmt.Errorf("ambiguous sat interval storage")
	}
	tree := newOrdinalTree(dir, output.RangesRoot).withContext(ctx)
	ranges := []satRange{}
	var expected, offset uint64
	e := tree.walk(func(kv ordinalKV) error {
		if kv.Key != satRangeKey(expected) {
			return fmt.Errorf("segmented sat interval gap")
		}
		var chunk []satRange
		if output.Output != "" {
			var segment satRangeSegment077
			if json.Unmarshal(kv.Value, &segment) != nil || segment.Offset != offset {
				return fmt.Errorf("%w: invalid Sat range segment prefix", ErrSatStateCorrupt)
			}
			chunk = segment.Ranges
		} else if json.Unmarshal(kv.Value, &chunk) != nil {
			return fmt.Errorf("invalid Sat range segment")
		}
		if len(chunk) == 0 || len(chunk) > 256 {
			return fmt.Errorf("invalid sat interval segment")
		}
		value, err := satRangesValueContext(ctx, chunk)
		if err != nil {
			return err
		}
		offset, err = addUint64(offset, value)
		if err != nil {
			return err
		}
		ranges = append(ranges, chunk...)
		expected += uint64(len(chunk))
		return nil
	})
	if e == nil && output.Output != "" && expected != output.RangeCount {
		e = fmt.Errorf("%w: segmented range count", ErrSatStateCorrupt)
	}
	if e != nil && output.Output != "" {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: segmented ranges (%v)", ErrSatStateCorrupt, e)
	}
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
	return s.deriveSat077Block(block, prepared)
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
	out := satIndexAnswer{Sat: sat, State: "unknown", ChainState: "unavailable", HistoryCoverage: []heightInterval{}, Note: "A placement requires an exact selected-chain Sat snapshot."}
	origin, valid := issuanceForSat(sat)
	if !valid {
		return out, fmt.Errorf("sat outside theoretical supply")
	}
	out.Origin = &origin
	authority := a.currentChainAuthority()
	status, err := satStateStatusContext(ctx, a.dataDir)
	if err != nil {
		return out, err
	}
	if status.Checkpoint == nil {
		return out, nil
	}
	out.Snapshot = status.Checkpoint
	if err = a.satCheckpointSelected077(ctx, status.Checkpoint, authority); err != nil {
		return out, nil
	}
	result, err := querySatNumberContext(ctx, a.dataDir, sat)
	if err != nil {
		return out, err
	}
	if result.Snapshot == nil || result.Snapshot.Commitment != status.Checkpoint.Commitment {
		return out, nil
	}
	if err = a.satCheckpointSelected077(ctx, result.Snapshot, authority); err != nil {
		return out, nil
	}
	out.ChainState = "selected_chain"
	out.Known = result.Known
	out.State = result.State
	out.Point = result.Point
	out.HistoryRecording = status.Snapshot.RetainHistory
	out.HistoryCoverage = status.HistoricalCoverage
	out.HistoryRetained = len(indexCoverageGaps(out.HistoryCoverage, 0, result.Snapshot.Height)) == 0
	out.Note = "FIFO placement at the stated saved snapshot; header anchoring is not full consensus validation."
	return out, nil
}
func (s *indexStore) ordinalRoots(b indexBatch) []string {
	roots := []string{}
	if b.Sats != nil {
		roots = append(roots, satSnapshotRoots077(*b.Sats)...)
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
		return "Ordinal FIFO identity and placement at this selected-chain snapshot; it is not full Bitcoin script consensus validation. Raw blocks and full movement history are independent retention choices. Savepoints retain the newest two heights divisible by12 and newest two divisible by144; recovery restores a saved point and replays, with deeper recovery requiring a reviewed rebuild."
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
	if s.definition.ID == "sat-state" {
		return s.collectSat077State()
	}
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
