package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func browseTXPointFixture(t *testing.T, count int) (*app, []string) {
	t.Helper()
	a, _, hashes := timelineFixture(t, count)
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}}}); err != nil {
		t.Fatal(err)
	}
	return a, hashes
}
func browseTXPointBatch(t *testing.T, height int64, hash string) indexBatch {
	t.Helper()
	block := browseBlock073(t, height, hash)
	for i := range block.Transactions {
		block.Transactions[i].TxID = fmt.Sprintf("ab00%060x", height*2+int64(i))
	}
	batch, err := singletonBrowseBatch("tx-locator", block, nil)
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func TestBrowseExtractionWrapperReusesOnlySealedInventory(t *testing.T) {
	a, block, extracted := inscriptionInventoryFixture(t)
	queue := &resolvedIngestionQueue{seen: map[string]bool{}}
	defer queue.close()
	ctx := context.WithValue(context.Background(), resolvedIngestionKey{}, queue)
	partial := extracted
	partial.Occurrences = partial.Occurrences[:1]
	if result := a.indexBrowsedBlock(ctx, block, &partial); len(result) != 1 || result[0].State != "unavailable" || len(queue.blocks) != 0 {
		t.Fatal("partial extraction entered global fanout", result)
	}
	if result := a.indexBrowsedBlock(ctx, block, &extracted); len(result) != 0 || len(queue.blocks) != 1 || queue.blocks[0].block.inscriptionExtraction != &extracted {
		t.Fatal("trusted extraction was not reused", result)
	}
	queue = &resolvedIngestionQueue{seen: map[string]bool{}}
	ctx = context.WithValue(context.Background(), resolvedIngestionKey{}, queue)
	defer queue.close()
	existing := extracted
	block.inscriptionExtraction = &existing
	a.indexBrowsedBlock(ctx, block, &extracted)
	if len(queue.blocks) != 1 || queue.blocks[0].block.inscriptionExtraction != &existing {
		t.Fatal("existing trusted shared extraction replaced")
	}
}

func TestBrowseTXPointsDurabilityReopenDedupAndLegacyUnion(t *testing.T) {
	a, hashes := browseTXPointFixture(t, 8)
	ctx := context.Background()
	batch := browseTXPointBatch(t, 2, hashes[2])
	if added, err := a.persistBrowseBatch(ctx, "tx-locator", batch); err != nil || !added {
		t.Fatal(added, err)
	}
	point, have, err := readBrowseTXPoint(ctx, a.dataDir, 2)
	if err != nil || !have || point.Commitment != batch.Checkpoint.Commitment {
		t.Fatal(point, have, err)
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", "browse", "tx-locator", "head.json")); !os.IsNotExist(err) {
		t.Fatal("point created a whole-history head", err)
	}
	if native, err := indexStoreHead(a.dataDir, "tx-locator"); err != nil || native.checkpoint != nil {
		t.Fatal("point manufactured a native prefix", err)
	}
	before := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes", "browse", "tx-locator"))
	reopened := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex()}
	if added, err := reopened.persistBrowseBatch(ctx, "tx-locator", batch); err != nil || added {
		t.Fatal("reopened duplicate appended", added, err)
	}
	if after := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes", "browse", "tx-locator")); !reflect.DeepEqual(before, after) {
		t.Fatal("duplicate changed retained bytes/mtimes")
	}
	legacy := browseTXPointBatch(t, 5, hashes[5])
	s := a.newBrowseStore("tx-locator")
	if err := atomicWriteJSON(filepath.Join(s.dir, "commits", legacy.Checkpoint.Commitment+".json"), legacy); err != nil {
		t.Fatal(err)
	}
	oldHead := indexBrowseHead{Schema: 1, Blocks: map[string]string{"5": legacy.Checkpoint.Commitment}}
	if err := atomicWriteJSON(filepath.Join(s.dir, "head.json"), oldHead); err != nil {
		t.Fatal(err)
	}
	for _, entry := range transactionBatchEntries(legacy) {
		path := filepath.Join(s.dir, "locators", entry.TxID[:2]+".json")
		var rows []indexLocatorRecord
		_ = boundedBrowseJSON(path, &rows)
		rows = append(rows, indexLocatorRecord{TxID: entry.TxID, Commit: legacy.Checkpoint.Commitment, Height: 5, TxIndex: int(entry.TxIndex)})
		if err := atomicWriteJSON(path, rows); err != nil {
			t.Fatal(err)
		}
	}
	oldBytes, _ := os.ReadFile(filepath.Join(s.dir, "head.json"))
	for _, b := range []indexBatch{batch, legacy} {
		got, have, err := reopened.browsedBatchContext(ctx, "tx-locator", b.Checkpoint.Height, b.Checkpoint.BlockHash)
		if err != nil || !have || got.Checkpoint.Commitment != b.Checkpoint.Commitment {
			t.Fatal("point/legacy leaf unavailable", have, err)
		}
		rows, err := reopened.browsedTransactionLocations(ctx, transactionBatchEntries(b)[0].TxID, "", -1)
		if err != nil || len(rows) != 1 || rows[0].Height != b.Checkpoint.Height {
			t.Fatal("point/legacy TX lookup", rows, err)
		}
	}
	if _, head, err := a.browseStore("tx-locator"); err != nil || len(head.Blocks) != 2 {
		t.Fatal(head, err)
	}
	next := browseTXPointBatch(t, 6, hashes[6])
	if _, err := a.persistBrowseBatch(ctx, "tx-locator", next); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(filepath.Join(s.dir, "head.json")); string(after) != string(oldBytes) {
		t.Fatal("new point rewrote legacy membership")
	}
	if err := os.WriteFile(browseTXPointPath(a.dataDir, 2), []byte(`{"schema":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.browsedBatchContext(ctx, "tx-locator", 2, hashes[2]); err == nil {
		t.Fatal("damaged point owner fell back to other membership")
	}
	if _, _, err := a.browseStore("tx-locator"); err == nil {
		t.Fatal("damaged owner omitted from materialized projection")
	}
}

func TestBrowseTXPointFailedPublicationAndRetry(t *testing.T) {
	for _, failure := range []string{"off", "cancel", "packed_ack", "leaf_ack"} {
		t.Run(failure, func(t *testing.T) {
			a, hashes := browseTXPointFixture(t, 4)
			s := a.newBrowseStore("tx-locator")
			batch := browseTXPointBatch(t, 2, hashes[2])
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := context.Context(base)
			injected := errors.New("injected point directory acknowledgement failure")
			if failure == "packed_ack" || failure == "leaf_ack" {
				s.locatorSyncDirectory = func(dir string) error {
					if failure == "packed_ack" && strings.Contains(dir, "locators-packed-v1") {
						path, _ := s.packedLocatorPath(transactionBatchEntries(batch)[0].TxID)
						if r, e := readPackedLocatorReceipt(path); e == nil && r.Bytes > 0 {
							return injected
						}
					}
					if failure == "leaf_ack" && dir == filepath.Dir(browseTXPointPath(a.dataDir, 2)) {
						if _, e := os.Stat(browseTXPointPath(a.dataDir, 2)); e == nil {
							return injected
						}
					}
					return nil
				}
			} else {
				ctx = context.WithValue(base, indexRunPublicationKey{}, func() error {
					path, _ := s.packedLocatorPath(transactionBatchEntries(batch)[0].TxID)
					if r, e := readPackedLocatorReceipt(path); e == nil && r.Bytes > 0 {
						if failure == "cancel" {
							cancel()
						} else {
							_, e = a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: false}}})
							if e != nil {
								return e
							}
						}
					}
					return base.Err()
				})
			}
			if added, err := a.persistBrowseTXPointBatch(ctx, s, batch); err == nil || added {
				t.Fatal("injected failure was hidden", added, err)
			}
			_, visible, err := readBrowseTXPoint(context.Background(), a.dataDir, 2)
			if err != nil || visible != (failure == "leaf_ack") {
				t.Fatal("wrong point visibility boundary", visible, err)
			}
			rows, err := a.browsedTransactionLocations(context.Background(), transactionBatchEntries(batch)[0].TxID, "", -1)
			if err != nil || len(rows) != map[bool]int{true: 1, false: 0}[visible] {
				t.Fatal("orphan packed row became visible", rows, err)
			}
			if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}}}); err != nil {
				t.Fatal(err)
			}
			s.locatorSyncDirectory = nil
			if _, err := a.persistBrowseTXPointBatch(context.Background(), s, batch); err != nil {
				t.Fatal("point retry failed", err)
			}
			if s.LocatorWriteStats().RecordsWritten != 2 {
				t.Fatal("retry duplicated acknowledged rows", s.LocatorWriteStats())
			}
			if _, have, err := a.browsedBatchContext(context.Background(), "tx-locator", 2, hashes[2]); err != nil || !have {
				t.Fatal(have, err)
			}
		})
	}
}

func TestBrowseTXPointPopulatedIslandAppendDiagnostic(t *testing.T) {
	if os.Getenv("GATEWAY_TX_POINT_DIAGNOSTIC") == "" {
		t.Skip("set GATEWAY_TX_POINT_DIAGNOSTIC for the owned1024-point storage diagnostic")
	}
	a, hashes := browseTXPointFixture(t, 2050)
	s := a.newBrowseStore("tx-locator")
	var samples []map[string]any
	for i := 0; i < 1024; i++ {
		b := browseTXPointBatch(t, int64(i*2), hashes[i*2])
		before := s.LocatorWriteStats()
		started := time.Now()
		if _, err := a.persistBrowseTXPointBatch(context.Background(), s, b); err != nil {
			t.Fatal(i, err)
		}
		elapsed := time.Since(started)
		after := s.LocatorWriteStats()
		if after.RecordsWritten-before.RecordsWritten != 2 || after.BytesWritten-before.BytesWritten != 215 || after.ShardAppends-before.ShardAppends != 1 {
			t.Fatal("append depends on historical rows", i, before, after)
		}
		if i == 31 || i == 255 || i == 1023 {
			samples = append(samples, map[string]any{"points": i + 1, "append_ns": elapsed.Nanoseconds(), "appended_frame_bytes": after.BytesWritten - before.BytesWritten, "appended_records": after.RecordsWritten - before.RecordsWritten})
		}
	}
	started := time.Now()
	_, head, err := a.browseStore("tx-locator")
	projection := time.Since(started)
	if err != nil || len(head.Blocks) != 1024 {
		t.Fatal(len(head.Blocks), err)
	}
	started = time.Now()
	source := a.browseProviderSource("tx-locator")
	status := time.Since(started)
	if !source.Complete || len(source.Ranges) != 1024 {
		t.Fatal("point islands became a fake prefix", source)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "head.json")); !os.IsNotExist(err) {
		t.Fatal("normal point writes materialized history", err)
	}
	report := map[string]any{"scope": "synthetic structural storage diagnostic; no Bitcoin throughput claim", "samples": samples, "projection_ns": projection.Nanoseconds(), "timeline_source_ns": status.Nanoseconds(), "status_scope": "bounded materialized enumeration is linear in retained point count", "stats": s.LocatorWriteStats()}
	raw, _ := json.Marshal(report)
	t.Log("TX_POINT_APPEND_DIAGNOSTIC", string(raw))
}
