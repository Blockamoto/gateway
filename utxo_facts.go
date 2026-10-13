package main

import (
	"context"
	"fmt"
)

const utxoFactCachePages = 128
const utxoFactCacheBytes = 16 << 20

type utxoCachedFacts struct {
	page  outputFactPage
	bytes int
}

// The cache exists only for one block/query/undo operation. Every cached page
// has passed the shared checksum and structural reader. Bound both the number
// of pages and their decoded row/script payload, including large scripts.
type utxoFactReader struct {
	ctx       context.Context
	root      string
	pages     map[string]utxoCachedFacts
	order     []string
	bytes     int
	pageReads uint64
}

func newUTXOFactReader(ctx context.Context, root string) *utxoFactReader {
	return &utxoFactReader{ctx: ctx, root: root, pages: map[string]utxoCachedFacts{}}
}

func (r *utxoFactReader) read(ref outputFactRef) (outputFact, error) {
	if e := r.ctx.Err(); e != nil {
		return outputFact{}, e
	}
	cached, found := r.pages[ref.Page]
	if !found {
		p, e := readOutputFactPageContext(r.ctx, r.root, ref.Page)
		if e != nil {
			return outputFact{}, e
		}
		r.pageReads++
		size := 512 + len(p.Rows)*128
		for _, row := range p.Rows {
			if row.Script != nil {
				size += len(*row.Script)
			}
		}
		cached = utxoCachedFacts{p, size}
		if size <= utxoFactCacheBytes {
			for len(r.order) > 0 && (len(r.pages) >= utxoFactCachePages || r.bytes+size > utxoFactCacheBytes) {
				old := r.order[0]
				r.order = r.order[1:]
				r.bytes -= r.pages[old].bytes
				delete(r.pages, old)
			}
			r.pages[ref.Page] = cached
			r.order = append(r.order, ref.Page)
			r.bytes += size
		}
	}
	p := cached.page
	if !p.Values || !p.Scripts {
		return outputFact{}, fmt.Errorf("live/recovery coin lacks complete output facts")
	}
	if uint64(ref.Row) >= uint64(len(p.Rows)) {
		return outputFact{}, fmt.Errorf("output fact row outside page")
	}
	row := p.Rows[ref.Row]
	return outputFact{Height: p.Height, BlockHash: p.BlockHash, TxIndex: row.TxIndex, Vout: row.Vout, ValueSats: *row.Value, ScriptPubKey: *row.Script, Coinbase: row.TxIndex == 0}, nil
}
