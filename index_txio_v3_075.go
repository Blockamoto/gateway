package main

import (
	"fmt"
	"strconv"
)

func validateTXIOV3Batch(b indexBatch) error {
	p := b.Spenders
	if len(p.Origins) != 0 || len(p.PreviousTransactions) != 0 || !validHash(p.LocatorRef) || p.TransactionCount == 0 || len(p.OutputCounts) != int(p.TransactionCount) {
		return fmt.Errorf("invalid positional TXIO catalogue")
	}
	pending := map[uint32]bool{}
	for _, r := range p.PendingTranslations {
		if uint64(r.Row) >= uint64(len(p.Rows)) || !validHash(r.PrevTxID) || pending[r.Row] || p.Rows[r.Row].Output != "" {
			return fmt.Errorf("invalid pending TXIO translation")
		}
		pending[r.Row] = true
	}
	seen := map[string]bool{}
	lastTx, lastVin := -1, -1
	for i, r := range p.Rows {
		if r.TxID != "" || r.PrevTxID != "" || r.PreviousTransaction != nil || r.TxIndex < 1 || r.TxIndex >= int(p.TransactionCount) || r.Vin < 0 || r.TxIndex < lastTx || r.TxIndex == lastTx && r.Vin != lastVin+1 || r.TxIndex != lastTx && r.Vin != 0 {
			return fmt.Errorf("invalid positional TXIO input")
		}
		lastTx, lastVin = r.TxIndex, r.Vin
		in := inputCoordinate(r.Vin, r.TxIndex, b.Checkpoint.Height)
		if seen[in] {
			return fmt.Errorf("duplicate TXIO input")
		}
		seen[in] = true
		if r.Output == "" {
			if !pending[uint32(i)] {
				return fmt.Errorf("missing pending TXIO outpoint")
			}
			continue
		}
		c, ok := parseBODCoordinate(r.Output)
		if !ok || c.Kind != coordOutput || c.TxID != "" || outputCoordinate(c.IOIndex, c.TxIndex, c.Height) != r.Output || uint64(c.IOIndex) != uint64(r.Vout) || c.Height > b.Checkpoint.Height || c.Height == b.Checkpoint.Height && c.TxIndex >= r.TxIndex || !validHash(p.CreationAnchors[strconv.FormatInt(c.Height, 10)]) {
			return fmt.Errorf("invalid positional TXIO predecessor")
		}
		if c.Height == b.Checkpoint.Height && (c.TxIndex >= len(p.OutputCounts) || r.Vout >= p.OutputCounts[c.TxIndex] || p.CreationAnchors[strconv.FormatInt(c.Height, 10)] != b.Checkpoint.BlockHash) {
			return fmt.Errorf("TXIO same-block output mismatch")
		}
	}
	for h, hash := range p.CreationAnchors {
		n, e := strconv.ParseInt(h, 10, 64)
		if e != nil || n < 0 || strconv.FormatInt(n, 10) != h || !validHash(hash) {
			return fmt.Errorf("invalid TXIO creation anchor")
		}
	}
	count := uint64(0)
	for _, n := range p.OutputCounts {
		count += uint64(n)
	}
	if p.Values || p.Scripts {
		if uint64(len(p.OutputFacts)) != count {
			return fmt.Errorf("incomplete TXIO output facts")
		}
		for pos, ref := range p.OutputFacts {
			c, ok := parseBODCoordinate(pos)
			if !ok || c.Kind != coordOutput || pos != outputCoordinate(c.IOIndex, c.TxIndex, c.Height) || c.Height != b.Checkpoint.Height || c.TxIndex >= len(p.OutputCounts) || uint64(c.IOIndex) >= uint64(p.OutputCounts[c.TxIndex]) || !validHash(ref.Page) || ref.Row >= outputFactPageRows {
				return fmt.Errorf("invalid TXIO fact reference")
			}
		}
	} else if len(p.OutputFacts) != 0 {
		return fmt.Errorf("unexpected TXIO output facts")
	}
	return nil
}
