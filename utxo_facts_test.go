package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestUTXOFactCacheAuthenticatesOncePerPageAndRefreshesEachOperation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	values := make([]uint64, 257)
	for i := range values {
		values[i] = uint64(i + 1)
	}
	block := utxoFixtureBlock(1, genesisHashDisplay, utxoFixtureCoinbase(4000, values...))
	refs, e := storeBlockOutputFactsContext(ctx, root, block)
	if e != nil {
		t.Fatal(e)
	}
	reader := newUTXOFactReader(ctx, root)
	for pass := 0; pass < 3; pass++ {
		for i, value := range values {
			ref := refs[outputCoordinate(i, 0, 1)]
			fact, e := reader.read(ref)
			if e != nil || fact.ValueSats != value || fact.Height != 1 || fact.Vout != uint32(i) || fact.ScriptPubKey != "51" || !fact.Coinbase {
				t.Fatal("cached exact facts changed", fact, e)
			}
		}
	}
	if reader.pageReads != 3 {
		t.Fatal("same page was repeatedly authenticated", reader.pageReads)
	}
	ref := refs["0.0.1"]
	path, e := outputFactPath(root, ref.Page)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("damaged"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = newUTXOFactReader(ctx, root).read(ref); !errors.Is(e, errDamagedOutputFactPage) {
		t.Fatal("next operation reused stale authentication", e)
	}
	invalid := ref
	invalid.Row = outputFactPageRows
	if _, e = reader.read(invalid); e == nil {
		t.Fatal("cached row outside page accepted")
	}
	partial, e := storeBlockOutputFactLayersContext(ctx, root, utxoFixtureBlock(2, block.Hash, utxoFixtureCoinbase(4001, 7)), true, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = reader.read(partial["0.0.2"]); e == nil {
		t.Fatal("partial history accepted as full Coin facts")
	}
	canceled, cancel := context.WithCancel(ctx)
	reader.ctx = canceled
	cancel()
	if _, e = reader.read(ref); !errors.Is(e, context.Canceled) {
		t.Fatal("cached read ignored cancellation", e)
	}
}

func TestUTXOFactCacheIsBoundedByPageCountAndPayload(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"pages", "bytes"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			reader := newUTXOFactReader(ctx, root)
			count := utxoFactCachePages + 1
			script := "51"
			if kind == "bytes" {
				count = 9
				script = strings.Repeat("51", 1<<20)
			}
			var first outputFactRef
			for i := 0; i < count; i++ {
				cb := utxoFixtureCoinbase(uint64(5000+i), 1)
				cb.Outputs[0].ScriptPubKey = script
				block := utxoFixtureBlock(int64(i+1), genesisHashDisplay, cb)
				refs, e := storeBlockOutputFactsContext(ctx, root, block)
				if e != nil {
					t.Fatal(e)
				}
				ref := refs[outputCoordinate(0, 0, int64(i+1))]
				if i == 0 {
					first = ref
				}
				if _, e = reader.read(ref); e != nil {
					t.Fatal(e)
				}
				if len(reader.pages) > utxoFactCachePages || reader.bytes > utxoFactCacheBytes {
					t.Fatal("cache exceeded bound")
				}
			}
			if _, retained := reader.pages[first.Page]; retained {
				t.Fatal("old page was not evicted at cache bound")
			}
			before := reader.pageReads
			if _, e := reader.read(first); e != nil || reader.pageReads != before+1 {
				t.Fatal("evicted page was not reauthenticated", e)
			}
		})
	}
}
