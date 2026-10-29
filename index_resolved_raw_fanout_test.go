package main

import (
	"context"
	"os"
	"reflect"
	"testing"
)

func TestSharedBlockQueuedRawProducerAuthorityResidentAndSpilled(t *testing.T) {
	for _, spill := range []bool{false, true} {
		name := "resident"
		if spill {
			name = "spilled"
		}
		t.Run(name, func(t *testing.T) {
			a := runTestApp(t)
			d, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}, {Index: "blocks", Armed: true}}})
			if err != nil {
				t.Fatal(err)
			}
			d.Run = &indexRunRecord{ID: "raw-dependency-fanout", State: "waiting", Request: d.Prepared,
				Consumers: []indexRunProgress{{Index: "tx-locator", From: 0, To: 1, Height: -1, AdmissionState: "admitted"}}}
			a.indexRunMu.Lock()
			a.indexRunState = d
			err = a.persistIndexRunLocked()
			a.indexRunMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			var blocks []blockView
			for height, raw := range [][]byte{testGenesisBlockPayload(t), mainnetBlockOne(t)} {
				hash := hash256(raw[:80])
				block, err := parseBlockDetailed(int64(height), reverseHex(hash[:]), raw[:80], raw, "Raw queued fanout fixture", false)
				if err != nil {
					t.Fatal(err)
				}
				produced, err := a.rawProducerReceiptContext(context.Background(), block)
				if err != nil || !produced {
					t.Fatal("fixture did not actually retain its source body", produced, err)
				}
				blocks = append(blocks, block)
			}
			receipt, err := rawBodyCatalogReceipt(context.Background(), a.dataDir, blockTarget{Height: 0, HashDisplay: blocks[0].Hash})
			if err != nil || receipt == nil || receipt.Segment == nil {
				t.Fatal(receipt, err)
			}
			bodyPath := rawSegmentPath(a.dataDir, receipt.Segment.File)
			bodyBefore := offLocatorFiles(t, rawSegmentDir(a.dataDir))
			if _, err = os.Stat(bodyPath); err != nil {
				t.Fatal(err)
			}
			session := newBrowseTXPointSession(a, context.Background(), d.Run.ID)
			defer session.Abort()
			queue := &resolvedIngestionQueue{}
			if spill {
				queue.maxResidentBytes = 1
			}
			defer queue.close()
			if err = queueResolvedBlock(queue, blocks[0]); err != nil {
				t.Fatal(err)
			}
			if (queue.blocks[0].block == nil) != spill {
				t.Fatal("requested queue representation was not exercised")
			}
			ctx := context.WithValue(context.Background(), resolvedIngestionKey{}, queue)
			ctx = context.WithValue(ctx, indexBrowseTXPointSessionKey{}, session)
			ctx = context.WithValue(ctx, indexRunCoordinatorSourceKey{}, true)
			ctx = context.WithValue(ctx, indexRunSourceOwnershipKey{}, indexRunSourceOwnership{RunID: d.Run.ID, Height: 1, Hash: blocks[1].Hash})
			ctx = context.WithValue(ctx, indexRunPreparedKey{}, &indexRunPrepared{block: blocks[1], rawProduced: true})
			a.drainResolvedBlockIngestion(ctx, queue)
			for _, row := range queue.results {
				if row.State == "error" || row.State == "unavailable" {
					t.Fatal("retained Raw dependency was not offered to armed TX", row)
				}
			}
			if _, have, err := session.Lookup(context.Background(), 0, blocks[0].Hash); err != nil || !have {
				t.Fatal("deferred body lost its real Raw producer authority", have, err)
			}
			if err = session.Flush(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, have, err := a.browsedBatchContext(context.Background(), "tx-locator", 0, blocks[0].Hash); err != nil || !have {
				t.Fatal("complete dependency TX positions were not saved", have, err)
			}
			if after := offLocatorFiles(t, rawSegmentDir(a.dataDir)); !reflect.DeepEqual(bodyBefore, after) {
				t.Fatal("fanout rewrote or duplicated the already retained source body")
			}
			if raw, err := indexStoreHead(a.dataDir, "blocks"); err != nil || raw.checkpoint != nil {
				t.Fatal("incidental Raw retention fabricated a contiguous Raw prefix", raw, err)
			}
		})
	}
}
