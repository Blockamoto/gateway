package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
)

// Frozen pre-recipe selection/commit loop, test-only differential oracle.
func (s *addressHistoryStore) legacyVisitBlockFacts(ctx context.Context, b addressHistoryBlock, visit func(string, string, outputFactRef, outputFact) error) error {
	if b.Height < 0 || !validHash(b.BlockHash) || b.DestinationVersion < 0 || b.DestinationVersion > 1 {
		return fmt.Errorf("invalid address scanned block")
	}
	count := uint64(0)
	previous := ""
	for _, hash := range b.FactPages {
		if hash <= previous {
			return fmt.Errorf("unordered/duplicate address fact pages")
		}
		previous = hash
		p, e := readOutputFactPageContext(ctx, s.root, hash)
		if e != nil {
			return e
		}
		if p.Height != b.Height || p.BlockHash != b.BlockHash || !p.Values || !p.Scripts {
			return fmt.Errorf("incomplete or mismatched address fact page")
		}
		for i, row := range p.Rows {
			script, e := hex.DecodeString(*row.Script)
			if e != nil {
				return e
			}
			typ, address := describeScript(script)
			if b.DestinationVersion == 1 {
				typ, address = describeDestinationScript(script)
			}
			if address == "" {
				continue
			}
			f := outputFact{Height: b.Height, BlockHash: b.BlockHash, TxIndex: row.TxIndex, Vout: row.Vout, ValueSats: *row.Value, ScriptPubKey: *row.Script, Coinbase: row.TxIndex == 0}
			if e = visit(address, typ, outputFactRef{hash, uint32(i)}, f); e != nil {
				return e
			}
			count++
		}
	}
	if count != b.AddressOutputs {
		return fmt.Errorf("address block output count mismatch")
	}
	return ctx.Err()
}
func (s *addressHistoryStore) legacyCommitFacts(ctx context.Context, height int64, hash string, refs map[string]outputFactRef, hashAt func(int64) (string, error)) error {
	if height < 0 || !validHash(hash) || hashAt == nil {
		return fmt.Errorf("address history needs selected block evidence")
	}
	if len(refs) > 500000 {
		return fmt.Errorf("address output fact references exceed block budget")
	}
	actual, e := hashAt(height)
	if e != nil {
		return e
	}
	if actual != hash {
		return fmt.Errorf("address source block is disconnected")
	}
	addresses := s.tree("addresses", s.head.AddressRoot, ctx)
	blocks := s.tree("blocks", s.head.BlocksRoot, ctx)
	type changedBucket struct {
		bucket addressHistoryBucket
		rows   *ordinalTree
	}
	changed := map[string]*changedBucket{}
	load := func(address, typ string) (*changedBucket, error) {
		if b, ok := changed[address]; ok {
			return b, nil
		}
		var b addressHistoryBucket
		have, e := addresses.get(addressHistoryAddressKey(address), &b)
		if e != nil {
			return nil, e
		}
		if have && (b.Address != address || b.ScriptType != typ || b.RowsRoot != "" && !validHash(b.RowsRoot)) {
			return nil, fmt.Errorf("address bucket identity mismatch")
		}
		if !have {
			b = addressHistoryBucket{Address: address, ScriptType: typ}
		}
		c := &changedBucket{b, s.tree("rows", b.RowsRoot, ctx)}
		changed[address] = c
		return c, nil
	}
	var prior addressHistoryBlock
	have, e := blocks.get(addressHistoryHeightKey(height), &prior)
	if e != nil {
		return e
	}
	if have {
		if prior.Height != height {
			return fmt.Errorf("address scanned block key mismatch")
		}
		e = s.legacyVisitBlockFacts(ctx, prior, func(address, typ string, ref outputFactRef, f outputFact) error {
			b, e := load(address, typ)
			if e != nil {
				return e
			}
			key, _ := addressHistoryPositionKey(outputCoordinate(int(f.Vout), f.TxIndex, f.Height))
			var row addressHistoryRow
			ok, e := b.rows.get(key, &row)
			if e != nil {
				return e
			}
			if !ok || row.BlockHash != prior.BlockHash || row.Fact != ref || b.bucket.OutputCount == 0 || b.bucket.ValueSats < f.ValueSats {
				return fmt.Errorf("address prior output identity/accounting mismatch")
			}
			b.rows.remove(key)
			b.bucket.OutputCount--
			b.bucket.ValueSats -= f.ValueSats
			return nil
		})
		if e != nil {
			return e
		}
	}
	pageSet := map[string]bool{}
	for pos, ref := range refs {
		c, ok := parseBODCoordinate(pos)
		if !ok || c.Kind != coordOutput || c.Height != height || pos != outputCoordinate(c.IOIndex, c.TxIndex, c.Height) {
			return fmt.Errorf("address source fact position mismatch")
		}
		if !validHash(ref.Page) || ref.Row >= outputFactPageRows {
			return fmt.Errorf("invalid address source fact reference")
		}
		pageSet[ref.Page] = true
	}
	b := addressHistoryBlock{Height: height, BlockHash: hash, FactPages: []string{}, DestinationVersion: 1}
	// Every row in a shared page must be present in the complete
	// source mapping; a partial TXIO map cannot manufacture scanned coverage.
	matched := 0
	for page := range pageSet {
		p, e := readOutputFactPageContext(ctx, s.root, page)
		if e != nil {
			return e
		}
		if !p.Values || !p.Scripts || p.Height != height || p.BlockHash != hash {
			return fmt.Errorf("address source fact identity mismatch")
		}
		addressed := false
		for i, r := range p.Rows {
			pos := outputCoordinate(int(r.Vout), r.TxIndex, height)
			if ref, ok := refs[pos]; !ok || ref != (outputFactRef{page, uint32(i)}) {
				return fmt.Errorf("incomplete address source fact mapping")
			}
			matched++
			script, _ := hex.DecodeString(*r.Script)
			_, address := describeDestinationScript(script)
			if address != "" {
				addressed = true
				b.AddressOutputs++
			}
		}
		if addressed {
			b.FactPages = append(b.FactPages, page)
		}
	}
	if matched != len(refs) {
		return fmt.Errorf("address source fact row mismatch")
	}
	sort.Strings(b.FactPages)
	if e = s.legacyVisitBlockFacts(ctx, b, func(address, typ string, ref outputFactRef, f outputFact) error {
		c, e := load(address, typ)
		if e != nil {
			return e
		}
		pos := outputCoordinate(int(f.Vout), f.TxIndex, f.Height)
		key, e := addressHistoryPositionKey(pos)
		if e != nil {
			return e
		}
		if math.MaxUint64-c.bucket.ValueSats < f.ValueSats || c.bucket.OutputCount == math.MaxUint64 {
			return fmt.Errorf("address totals overflow")
		}
		if e = c.rows.put(key, addressHistoryRow{pos, hash, ref}); e != nil {
			return e
		}
		c.bucket.OutputCount++
		c.bucket.ValueSats += f.ValueSats
		return nil
	}); e != nil {
		return e
	}
	for address, c := range changed {
		root, e := c.rows.flush()
		if e != nil {
			return e
		}
		c.bucket.RowsRoot = root
		if c.bucket.OutputCount == 0 {
			addresses.remove(addressHistoryAddressKey(address))
		} else if e = addresses.put(addressHistoryAddressKey(address), c.bucket); e != nil {
			return e
		}
	}
	if e = blocks.put(addressHistoryHeightKey(height), b); e != nil {
		return e
	}
	ar, e := addresses.flush()
	if e != nil {
		return e
	}
	br, e := blocks.flush()
	if e != nil {
		return e
	}
	h := s.head
	h.AddressRoot, h.BlocksRoot = ar, br
	h.Coverage = addressHistoryCoverage(h.Coverage, height, true)
	h.P2PKCoverage = addressHistoryCoverage(h.P2PKCoverage, height, true)
	h.LastHeight, h.LastBlockHash = height, hash
	h.PinnedPages = append([]string(nil), b.FactPages...)
	h.Height = h.Coverage[len(h.Coverage)-1].To
	var tip addressHistoryBlock
	ok, e := blocks.get(addressHistoryHeightKey(h.Height), &tip)
	if e != nil {
		return e
	}
	if !ok || tip.Height != h.Height {
		return fmt.Errorf("address snapshot tip unavailable")
	}
	h.BlockHash = tip.BlockHash
	actual, e = hashAt(height)
	if e != nil {
		return e
	}
	if actual != hash {
		return fmt.Errorf("address selected chain changed before publication")
	}
	actual, e = hashAt(h.Height)
	if e != nil {
		return e
	}
	if actual != h.BlockHash {
		return fmt.Errorf("address history requires selected-chain reconciliation")
	}
	return s.publish(ctx, h)
}
