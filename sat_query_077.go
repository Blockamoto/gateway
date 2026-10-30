package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type satStateStatus077 struct {
	State                 string            `json:"state"`
	Checkpoint            *indexCheckpoint  `json:"checkpoint"`
	Snapshot              *satIndexSnapshot `json:"snapshot"`
	Savepoints            []satSavepoint077 `json:"savepoints"`
	OldestSavepointHeight int64             `json:"oldest_savepoint_height"`
	IdentityCoverage      []heightInterval  `json:"identity_coverage"`
	HistoricalCoverage    []heightInterval  `json:"historical_coverage"`
	OutputCoverage        []heightInterval  `json:"output_coverage"`
}
type satNumberResult077 struct {
	Point        *satlinePoint    `json:"satpoint,omitempty"`
	Snapshot     *indexCheckpoint `json:"snapshot"`
	SatNumber    uint64           `json:"sat_number"`
	Known        bool             `json:"known"`
	State        string           `json:"state"`
	Output       string           `json:"output,omitempty"`
	Offset       uint64           `json:"offset"`
	FactRef      *outputFactRef   `json:"facts,omitempty"`
	ValueSats    uint64           `json:"value_sats"`
	ScriptPubKey string           `json:"script_pub_key"`
	LostHeight   int64            `json:"lost_height,omitempty"`
	LostHash     string           `json:"lost_hash,omitempty"`
}
type satOutputRangesResult077 struct {
	Snapshot        *indexCheckpoint `json:"snapshot"`
	Output          string           `json:"output"`
	State           string           `json:"state"`
	Ranges          []satRange       `json:"ranges"`
	RangeOffset     uint64           `json:"range_offset"`
	RangeCount      uint64           `json:"range_count"`
	FactRef         *outputFactRef   `json:"facts,omitempty"`
	ValueSats       uint64           `json:"value_sats"`
	ScriptPubKey    string           `json:"script_pub_key"`
	HistoryRetained bool             `json:"history_retained"`
	NextCursor      string           `json:"next_cursor"`
	HasMore         bool             `json:"has_more"`
}
type satInscriptionRow077 struct {
	ID           string `json:"id"`
	SatNumber    uint64 `json:"sat_number"`
	RevealHeight int64  `json:"reveal_height"`
	RevealHash   string `json:"reveal_hash"`
}
type satInscriptionPage077 struct {
	Snapshot         *indexCheckpoint       `json:"snapshot"`
	Rows             []satInscriptionRow077 `json:"rows"`
	IdentityCoverage []heightInterval       `json:"identity_coverage"`
	IdentityComplete bool                   `json:"identity_complete"`
	NextCursor       string                 `json:"next_cursor"`
	HasMore          bool                   `json:"has_more"`
}
type satRangeCursor077 struct {
	Commitment string `json:"commitment"`
	Output     string `json:"output"`
	Next       uint64 `json:"next"`
	Offset     uint64 `json:"offset"`
	Checksum   string `json:"checksum"`
}

func satRangeCursorDigest077(c satRangeCursor077) string { c.Checksum = ""; return indexDigest(c) }
func openSatQuery077(ctx context.Context, root string) (*indexStore, indexBatch, error) {
	s, err := indexStoreHead(root, "sat-state")
	if err != nil {
		return nil, indexBatch{}, err
	}
	if s.checkpoint == nil {
		return s, indexBatch{}, nil
	}
	if err = s.validateSatSavepoints077(ctx); err != nil {
		return s, indexBatch{}, err
	}
	b, err := s.readBatch(s.head.Commitment)
	if err != nil {
		return s, b, err
	}
	if err = s.validateSatRoots077(ctx, *b.Sats); err != nil {
		return s, b, err
	}
	return s, b, nil
}
func satStateStatusContext(ctx context.Context, root string) (satStateStatus077, error) {
	satStateMu.Lock()
	defer satStateMu.Unlock()
	out := satStateStatus077{State: "not_built", Savepoints: []satSavepoint077{}, OldestSavepointHeight: -1, IdentityCoverage: []heightInterval{}, HistoricalCoverage: []heightInterval{}, OutputCoverage: []heightInterval{}}
	s, b, err := openSatQuery077(ctx, root)
	if err != nil {
		return out, err
	}
	if s.checkpoint == nil {
		return out, nil
	}
	out.State = "ready"
	out.Checkpoint = &b.Checkpoint
	out.Snapshot = b.Sats
	out.Savepoints = append(out.Savepoints, s.head.SatSavepoints...)
	if len(out.Savepoints) > 0 {
		out.OldestSavepointHeight = out.Savepoints[0].Height
	}
	out.IdentityCoverage = append(out.IdentityCoverage, b.Sats.IdentityCoverage...)
	out.HistoricalCoverage = append(out.HistoricalCoverage, b.Sats.HistoryCoverage...)
	out.OutputCoverage = mergeIntervals(append(append(out.OutputCoverage, b.Sats.OutputCoverage...), b.Sats.HistoryCoverage...))
	return out, nil
}
func querySatNumberContext(ctx context.Context, root string, sat uint64) (satNumberResult077, error) {
	satStateMu.Lock()
	defer satStateMu.Unlock()
	out := satNumberResult077{SatNumber: sat, State: "unknown"}
	issuance, valid := issuanceForSat(sat)
	if !valid {
		return out, fmt.Errorf("sat outside theoretical supply")
	}
	s, b, err := openSatQuery077(ctx, root)
	if err != nil {
		return out, err
	}
	if s.checkpoint == nil {
		return out, nil
	}
	out.Snapshot = &b.Checkpoint
	if issuance.Height > b.Checkpoint.Height {
		out.State = "not_issued_at_snapshot"
		return out, nil
	}
	var loc satIndexLocation
	have, err := newOrdinalTree(s.ordinalPages(), b.Sats.LocationRoot).withContext(ctx).predecessor(satRangeKey(sat), &loc)
	if err != nil || !have || sat < loc.Start || sat >= loc.End {
		return out, fmt.Errorf("%w: location gap (%v)", ErrSatStateCorrupt, err)
	}
	out.State = loc.State
	out.Offset = loc.Offset + sat - loc.Start
	out.Output = loc.Output
	out.Known = true
	if loc.Output == "" {
		if loc.State != "lost" || !validHash(loc.LostHash) {
			return out, fmt.Errorf("%w: terminal location", ErrSatStateCorrupt)
		}
		out.LostHeight, out.LostHash = loc.LostHeight, loc.LostHash
		out.Point = &satlinePoint{TxID: strings.Repeat("0", 64), Vout: ^uint32(0), Offset: out.Offset, Height: loc.LostHeight, BlockHash: loc.LostHash}
		return out, nil
	}
	e := newSatEditor077(ctx, s, *b.Sats)
	o, have, err := e.output(loc.Output)
	if err != nil || !have {
		return out, fmt.Errorf("%w: location output (%v)", ErrSatStateCorrupt, err)
	}
	f, err := e.facts.read(*o.FactRef)
	if err != nil {
		return out, err
	}
	if out.Offset >= f.ValueSats || o.State != loc.State {
		return out, fmt.Errorf("%w: range location mismatch", ErrSatStateCorrupt)
	}
	point := o.Point
	point.Offset = out.Offset
	out.Point = &point
	out.FactRef = o.FactRef
	out.ValueSats = f.ValueSats
	out.ScriptPubKey = f.ScriptPubKey
	return out, nil
}
func (s *indexStore) satLocationPoint077(ctx context.Context, p satIndexSnapshot, l satIndexLocation) (satlinePoint, error) {
	e := newSatEditor077(ctx, s, p)
	o, have, err := e.output(l.Output)
	if err != nil || !have {
		return satlinePoint{}, fmt.Errorf("%w: location output", ErrSatStateCorrupt)
	}
	o.Point.Offset = l.Offset
	return o.Point, nil
}
func satRangePage077(ctx context.Context, dir string, o satIndexOutput, next uint64, limit int) ([]satRange, error) {
	if limit < 1 || limit > 1000 || next > o.RangeCount {
		return nil, fmt.Errorf("%w: invalid range page bounds", ErrSatStateCorrupt)
	}
	if o.RangesRoot == "" {
		if o.RangeCount != uint64(len(o.Ranges)) {
			return nil, fmt.Errorf("%w: inline range count", ErrSatStateCorrupt)
		}
		end := next + uint64(limit)
		if end > o.RangeCount {
			end = o.RangeCount
		}
		return append([]satRange{}, o.Ranges[next:end]...), ctx.Err()
	}
	if len(o.Ranges) > 0 {
		return nil, fmt.Errorf("%w: ambiguous ranges", ErrSatStateCorrupt)
	}
	out := []satRange{}
	t := newOrdinalTree(dir, o.RangesRoot).withContext(ctx)
	for next < o.RangeCount && len(out) < limit {
		start := next / 256 * 256
		var chunk []satRange
		var segment satRangeSegment077
		have, err := t.get(satRangeKey(start), &segment)
		chunk = segment.Ranges
		expected := min(uint64(256), o.RangeCount-start)
		if err != nil || !have || uint64(len(chunk)) != expected {
			return nil, fmt.Errorf("%w: range segment (%v)", ErrSatStateCorrupt, err)
		}
		for i := int(next - start); i < len(chunk) && len(out) < limit; i++ {
			out = append(out, chunk[i])
			next++
		}
	}
	return out, nil
}

func satRangeOffset077(ctx context.Context, dir string, o satIndexOutput, next uint64) (uint64, error) {
	if next > o.RangeCount {
		return 0, ErrSatStateCorrupt
	}
	if o.RangesRoot == "" {
		if o.RangeCount != uint64(len(o.Ranges)) {
			return 0, ErrSatStateCorrupt
		}
		return satRangesValueContext(ctx, o.Ranges[:next])
	}
	var segment satRangeSegment077
	start := next / 256 * 256
	have, err := newOrdinalTree(dir, o.RangesRoot).withContext(ctx).get(satRangeKey(start), &segment)
	if err != nil || !have || next-start >= uint64(len(segment.Ranges)) {
		return 0, fmt.Errorf("%w: range offset segment (%v)", ErrSatStateCorrupt, err)
	}
	n, err := satRangesValueContext(ctx, segment.Ranges[:next-start])
	if err != nil {
		return 0, err
	}
	return addUint64(segment.Offset, n)
}
func querySatOutputRangesContext(ctx context.Context, root, output string, limit int, cursor string) (satOutputRangesResult077, error) {
	satStateMu.Lock()
	defer satStateMu.Unlock()
	out := satOutputRangesResult077{Output: output, State: "unknown", Ranges: []satRange{}}
	if limit < 1 || limit > 1000 {
		return out, fmt.Errorf("range limit must be 1..1000")
	}
	key, err := satOutputKey077(output)
	if err != nil {
		return out, err
	}
	s, b, err := openSatQuery077(ctx, root)
	if err != nil {
		return out, err
	}
	if s.checkpoint == nil {
		return out, nil
	}
	out.Snapshot = &b.Checkpoint
	e := newSatEditor077(ctx, s, *b.Sats)
	var o satIndexOutput
	have, err := e.outputs.get(key, &o)
	if err != nil {
		return out, err
	}
	if !have {
		have, err = e.history.get(key, &o)
		out.HistoryRetained = have
	}
	if err != nil {
		return out, err
	}
	if !have {
		return out, nil
	}
	if o.Output != output || o.FactRef == nil {
		return out, fmt.Errorf("%w: output identity", ErrSatStateCorrupt)
	}
	f, err := e.facts.read(*o.FactRef)
	if err != nil {
		return out, fmt.Errorf("%w: output facts (%v)", ErrSatStateCorrupt, err)
	}
	c, _ := satPosition077(output)
	if f.Height != c.Height || f.TxIndex != c.TxIndex || int(f.Vout) != c.IOIndex {
		return out, fmt.Errorf("%w: output facts mismatch", ErrSatStateCorrupt)
	}
	next, offset := uint64(0), uint64(0)
	if cursor != "" {
		if len(cursor) > 2048 {
			return out, fmt.Errorf("invalid range cursor")
		}
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		var p satRangeCursor077
		if err != nil || json.Unmarshal(raw, &p) != nil || p.Commitment != b.Checkpoint.Commitment || p.Output != output || p.Checksum != satRangeCursorDigest077(p) || p.Next >= o.RangeCount || p.Offset > f.ValueSats {
			return out, fmt.Errorf("range cursor does not match snapshot/output")
		}
		next, offset = p.Next, p.Offset
	}
	actualOffset, err := satRangeOffset077(ctx, s.ordinalPages(), o, next)
	if err != nil {
		return out, err
	}
	if cursor != "" && actualOffset != offset {
		return out, fmt.Errorf("range cursor offset does not match authenticated page")
	}
	offset = actualOffset
	out.RangeOffset = offset
	out.Ranges, err = satRangePage077(ctx, s.ordinalPages(), o, next, limit)
	if err != nil {
		return out, err
	}
	for _, r := range out.Ranges {
		if r.End <= r.Start || r.End > theoreticalSatSupply() || r.End-r.Start > f.ValueSats-offset {
			return out, fmt.Errorf("%w: invalid output ranges", ErrSatStateCorrupt)
		}
		offset += r.End - r.Start
	}
	out.State = o.State
	out.RangeCount = o.RangeCount
	out.FactRef = o.FactRef
	out.ValueSats = f.ValueSats
	out.ScriptPubKey = f.ScriptPubKey
	next += uint64(len(out.Ranges))
	out.HasMore = next < o.RangeCount
	if out.HasMore {
		p := satRangeCursor077{Commitment: b.Checkpoint.Commitment, Output: output, Next: next, Offset: offset}
		p.Checksum = satRangeCursorDigest077(p)
		raw, _ := json.Marshal(p)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	} else if offset != f.ValueSats {
		return out, fmt.Errorf("%w: complete range/value mismatch", ErrSatStateCorrupt)
	}
	return out, nil
}
func satOutputFactContext(ctx context.Context, root, position, blockHash string) (outputFactRef, bool, error) {
	satStateMu.Lock()
	defer satStateMu.Unlock()
	key, err := satOutputKey077(position)
	if err != nil {
		return outputFactRef{}, false, err
	}
	s, b, err := openSatQuery077(ctx, root)
	if err != nil || s.checkpoint == nil {
		return outputFactRef{}, false, err
	}
	e := newSatEditor077(ctx, s, *b.Sats)
	var o satIndexOutput
	have, err := e.outputs.get(key, &o)
	if !have && err == nil {
		have, err = e.history.get(key, &o)
	}
	if err != nil || !have {
		return outputFactRef{}, false, err
	}
	if o.Output != position || o.FactRef == nil {
		return outputFactRef{}, false, ErrSatStateCorrupt
	}
	f, err := e.facts.read(*o.FactRef)
	if err != nil {
		return outputFactRef{}, false, err
	}
	if f.BlockHash != blockHash {
		return outputFactRef{}, false, nil
	}
	return *o.FactRef, true, nil
}
func satOutputForOutpointContext(ctx context.Context, root, txid string, vout uint32, before int64) (satIndexOutput, bool, error) {
	satStateMu.Lock()
	defer satStateMu.Unlock()
	s, b, err := openSatQuery077(ctx, root)
	if err != nil || s.checkpoint == nil {
		return satIndexOutput{}, false, err
	}
	e := newSatEditor077(ctx, s, *b.Sats)
	var pos string
	have, err := e.bridge.get(ordinalOutpointKey(txid, vout), &pos)
	if err != nil || !have {
		return satIndexOutput{}, false, err
	}
	o, have, err := e.output(pos)
	if have && before >= 0 && o.Point.Height > before {
		return satIndexOutput{}, false, nil
	}
	return o, have, err
}
func satInscriptionIdentityContext(ctx context.Context, root, id string) (inscriptionSatResult, bool, error) {
	satStateMu.Lock()
	defer satStateMu.Unlock()
	s, b, err := openSatQuery077(ctx, root)
	if err != nil || s.checkpoint == nil {
		return inscriptionSatResult{}, false, err
	}
	var row inscriptionSatResult
	have, err := newOrdinalTree(s.ordinalPages(), b.Sats.InscriptionRoot).withContext(ctx).get(ordinalInscriptionKey(id), &row)
	if err == nil && have && (row.ID != id || row.Profile != inscriptionSatProfile || row.RevealHeight < 0 || row.RevealHeight > b.Checkpoint.Height || !validHash(row.RevealHash) || row.Known != (row.SatNumber != nil) || row.SatNumber != nil && *row.SatNumber >= theoreticalSatSupply()) {
		return row, false, fmt.Errorf("%w: inscription identity", ErrSatStateCorrupt)
	}
	return row, have, err
}
func satWalkKeys077(ctx context.Context, t *ordinalTree, lower, upper, after string, visit func(ordinalKV) error) error {
	var walk func(string, string) error
	walk = func(hash, prefix string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if hash == "" {
			return nil
		}
		if len(prefix) > 128 {
			return ErrSatStateCorrupt
		}
		if prefix != "" {
			if len(prefix) <= len(lower) && prefix < lower[:len(prefix)] {
				return nil
			}
			if len(prefix) <= len(upper) && prefix > upper[:len(prefix)] {
				return nil
			}
			if after != "" && len(prefix) <= len(after) && prefix < after[:len(prefix)] {
				return nil
			}
		}
		p, err := t.read(hash)
		if err != nil {
			return err
		}
		for _, kv := range p.Entries {
			if kv.Key >= lower && kv.Key < upper && kv.Key > after {
				if err = visit(kv); err != nil {
					return err
				}
			}
		}
		for i, c := range p.Children {
			if c != "" {
				if err = walk(c, prefix+strconv.FormatInt(int64(i), 16)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(t.root, "")
}
func querySatRangeInscriptionsContext(ctx context.Context, root string, start, end uint64, limit int, cursor string) (satInscriptionPage077, error) {
	satStateMu.Lock()
	defer satStateMu.Unlock()
	out := satInscriptionPage077{Rows: []satInscriptionRow077{}, IdentityCoverage: []heightInterval{}}
	if start >= end || end > theoreticalSatSupply() || limit < 1 || limit > 1000 {
		return out, fmt.Errorf("invalid inscription interval/page")
	}
	s, b, err := openSatQuery077(ctx, root)
	if err != nil || s.checkpoint == nil {
		return out, err
	}
	out.Snapshot = &b.Checkpoint
	out.IdentityCoverage = b.Sats.IdentityCoverage
	out.IdentityComplete = len(indexCoverageGaps(b.Sats.IdentityCoverage, 0, b.Checkpoint.Height)) == 0
	lower, upper := satRangeKey(start), satRangeKey(end)
	after := ""
	if cursor != "" {
		if len(cursor) > 2048 {
			return out, fmt.Errorf("invalid inscription cursor")
		}
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		parts := strings.Split(string(raw), ":")
		if err != nil || len(parts) != 4 || parts[0] != b.Checkpoint.Commitment || parts[1] != lower || parts[2] != upper || !ordinalKeyValid(parts[3]) {
			return out, fmt.Errorf("inscription cursor does not match snapshot/interval")
		}
		after = parts[3]
	}
	last := ""
	err = satWalkKeys077(ctx, newOrdinalTree(s.ordinalPages(), b.Sats.InscriptionBySatRoot).withContext(ctx), lower, upper, after, func(kv ordinalKV) error {
		if len(out.Rows) >= limit {
			out.HasMore = true
			return errAddressPageFull
		}
		var row inscriptionSatResult
		if json.Unmarshal(kv.Value, &row) != nil || !row.Known || row.SatNumber == nil || *row.SatNumber < start || *row.SatNumber >= end || kv.Key != satRangeKey(*row.SatNumber)+ordinalInscriptionKey(row.ID) {
			return ErrSatStateCorrupt
		}
		out.Rows = append(out.Rows, satInscriptionRow077{row.ID, *row.SatNumber, row.RevealHeight, row.RevealHash})
		last = kv.Key
		return nil
	})
	if err != nil && err != errAddressPageFull {
		return out, err
	}
	if out.HasMore {
		out.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(b.Checkpoint.Commitment + ":" + lower + ":" + upper + ":" + last))
	}
	return out, nil
}
