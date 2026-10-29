package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func inscriptionInventoryFixture(t *testing.T) (*app, blockView, inscriptionBlockResult) {
	t.Helper()
	a := runTestApp(t)
	block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap", "ordinary inscription", "bad", "also bad")
	first := inscriptionTestScript([]byte{1}, []byte("text/plain"), nil, []byte("0.bitmap"))
	second := inscriptionTestScript([]byte{1}, []byte("text/plain"), nil, []byte("second ordinary inscription"))
	block.Transactions[1].Inputs[0].Witness = ordWitness050(append(first, second...))
	for _, i := range []int{3, 4} {
		block.Transactions[i].Inputs[0].Witness = ordWitness050([]byte{0x4c})
	}
	installRunProtocolHeader(t, a, &block)
	extracted, err := extractInscriptionOccurrences(block)
	if err != nil || len(extracted.Occurrences) != 3 || len(extracted.Diagnostics) != 2 {
		t.Fatal("inventory fixture", extracted, err)
	}
	return a, block, extracted
}

func TestInscriptionInventoryRejectsPartialOrChangedPreparedExtraction(t *testing.T) {
	_, block, full := inscriptionInventoryFixture(t)
	for _, test := range []struct {
		name   string
		change func(*inscriptionBlockResult)
	}{
		{"claim_subset", func(x *inscriptionBlockResult) { x.Occurrences = x.Occurrences[:1] }},
		{"duplicate_identity", func(x *inscriptionBlockResult) { x.Occurrences[2] = x.Occurrences[0] }},
		{"changed_order", func(x *inscriptionBlockResult) {
			x.Occurrences[0], x.Occurrences[1] = x.Occurrences[1], x.Occurrences[0]
		}},
		{"changed_identity", func(x *inscriptionBlockResult) { x.Occurrences[0].TxID = testID(991) }},
		{"changed_coordinate", func(x *inscriptionBlockResult) { x.Occurrences[0].TxIndex = 2 }},
		{"reference", func(x *inscriptionBlockResult) { x.ReferenceCommit = strings.Repeat("0", 40) }},
		{"dropped_diagnostic", func(x *inscriptionBlockResult) { x.Diagnostics = x.Diagnostics[:1] }},
		{"changed_diagnostic", func(x *inscriptionBlockResult) { x.Diagnostics[0].Code = "different" }},
		{"reordered_diagnostics", func(x *inscriptionBlockResult) {
			x.Diagnostics[0], x.Diagnostics[1] = x.Diagnostics[1], x.Diagnostics[0]
		}},
		{"no_private_receipt", func(x *inscriptionBlockResult) { x.inventoryReceipt = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			x := full
			x.Occurrences = append([]inscriptionOccurrence(nil), full.Occurrences...)
			x.Diagnostics = append([]inscriptionExtractionDiagnostic(nil), full.Diagnostics...)
			test.change(&x)
			for _, mode := range []string{"lean", "full"} {
				choice := indexRunSelection{Index: "inscriptions", indexRunOptions: indexRunOptions{Mode: mode}}
				if _, err := singletonBrowseBatchMode(choice, block, &x); err == nil {
					t.Fatal("changed extraction became a complete browsed inventory", mode)
				}
				store, err := openIndexStore(t.TempDir(), "inscriptions")
				if err != nil {
					t.Fatal(err)
				}
				store.head.Mode = mode
				if err := store.appendPreparedBlock(block, block.Height, "ephemeral", &x); err == nil {
					t.Fatal("changed extraction became a sequential inventory", mode)
				}
				if _, err := os.Stat(store.dir); !os.IsNotExist(err) || store.checkpoint != nil {
					t.Fatal("rejected extraction wrote global inventory", mode, err)
				}
			}
		})
	}
	raw, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	var decoded inscriptionBlockResult
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := validateCompleteInscriptionInventory(block, &decoded); err == nil {
		t.Fatal("serialized extraction acquired private full-inventory authority")
	}
}

func TestInscriptionInventoryLeanFullEmptyAndNativeBytes(t *testing.T) {
	_, block, extracted := inscriptionInventoryFixture(t)
	for _, mode := range []string{"lean", "full"} {
		choice := indexRunSelection{Index: "inscriptions", indexRunOptions: indexRunOptions{Mode: mode}}
		prepared, err := singletonBrowseBatchMode(choice, block, &extracted)
		if err != nil || !inscriptionBatchInventoryComplete(prepared) || len(prepared.InscriptionCoordinates) != 3 {
			t.Fatal("complete extraction lost inventory", mode, prepared, err)
		}
		fresh, err := singletonBrowseBatchMode(choice, block, nil)
		if err != nil || prepared.Checkpoint.Commitment != fresh.Checkpoint.Commitment || prepared.Checkpoint.RecordsHash != fresh.Checkpoint.RecordsHash {
			t.Fatal("private receipt changed native browsed bytes", mode, err)
		}
		var commits []string
		for _, shared := range []*inscriptionBlockResult{nil, &extracted} {
			store, err := openIndexStore(t.TempDir(), "inscriptions")
			if err != nil {
				t.Fatal(err)
			}
			store.head.Mode = mode
			if err := store.appendPreparedBlock(block, block.Height, "ephemeral", shared); err != nil {
				t.Fatal(err)
			}
			commits = append(commits, store.head.Commitment)
		}
		if commits[0] != commits[1] {
			t.Fatal("private receipt changed native sequential commitment", commits)
		}
	}
	empty := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "")
	batch, err := singletonBrowseBatch("inscriptions", empty, nil)
	if err != nil || !inscriptionBatchInventoryComplete(batch) || len(batch.InscriptionCoordinates) != 0 {
		t.Fatal("fully scanned empty block is not complete", batch, err)
	}
}

func TestBitmapExtractionWithInscriptionsOffHasNoGlobalPublication(t *testing.T) {
	a, block, _ := inscriptionInventoryFixture(t)
	request := indexRunRequest{Consumers: []indexRunSelection{{Index: "inscriptions", Armed: false}, {Index: "tx-locator", Armed: false}, {Index: bitmapStandardDistrictID, Armed: true}}}
	if _, err := a.prepareIndexRun(request); err != nil {
		t.Fatal(err)
	}
	for _, result := range a.ingestResolvedVerifiedBlock(context.Background(), block) {
		if result.State == "error" || result.State == "unavailable" {
			t.Fatal(result)
		}
	}
	bitmap, err := readBitmapDevelopmentHead(context.Background(), a.dataDir, bitmapStandardDistrictID)
	if err != nil || bitmap == nil || bitmap.WinnerCount != 1 {
		t.Fatal("Bitmap did not retain its independently owned claim", bitmap, err)
	}
	for _, path := range []string{filepath.Join(a.dataDir, "indexes", "inscriptions"), filepath.Join(a.dataDir, "indexes", "browse", "inscriptions"), filepath.Join(a.dataDir, "indexes", inscriptionLocatorIndex)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("Bitmap extraction wrote an Off global inscription store", path, err)
		}
	}
	request.Consumers[0].Armed = true
	request.Consumers[0].Mode = "lean"
	if _, err := a.prepareIndexRun(request); err != nil {
		t.Fatal(err)
	}
	for _, result := range a.ingestResolvedVerifiedBlock(context.Background(), block) {
		if result.State == "error" || result.State == "unavailable" {
			t.Fatal(result)
		}
	}
	batch, have, err := a.browsedBatchContext(context.Background(), "inscriptions", block.Height, block.Hash)
	if err != nil || !have || !inscriptionBatchInventoryComplete(batch) || len(batch.InscriptionCoordinates) != 3 {
		t.Fatal("explicitly armed inventory omitted non-Bitmap inscriptions", batch, have, err)
	}
	if source := a.browseProviderSource("inscriptions"); !source.Complete || !reflect.DeepEqual(source.Ranges, []heightInterval{{block.Height, block.Height}}) {
		t.Fatal("complete independent inventory did not establish its exact block", source)
	}
	if a.indexRunSnapshot().Run != nil {
		t.Fatal("resolved source started an indexing run")
	}
}

func TestInscriptionInventoryPreservesLegacyFullRecipeButNotPointSource(t *testing.T) {
	for _, pointOnly := range []bool{false, true} {
		name := "legacy_full_scan"
		if pointOnly {
			name = "known_occurrence_source"
		}
		t.Run(name, func(t *testing.T) {
			a, block, extracted := inscriptionInventoryFixture(t)
			definition, err := findIndexDefinition("inscriptions")
			if err != nil {
				t.Fatal(err)
			}
			legacy := legacyIndexDefinition(definition)
			batch := indexBatch{PreviousBlockHash: block.PreviousBlockHash, Inscriptions: extracted.Occurrences, Diagnostics: extracted.Diagnostics}
			batch.Checkpoint = indexCheckpoint{Schema: legacy.CheckpointSchema, Definition: legacy.ID, Version: legacy.Version, RuleHash: legacy.RuleHash, Network: legacy.Network, From: block.Height, Height: block.Height, BlockHash: block.Hash, DependencyFingerprint: indexDigest(struct{ Block, Parser, Reference string }{block.Hash, inscriptionParserProfile, inscriptionReferenceCommit})}
			if pointOnly {
				batch.Inscriptions = batch.Inscriptions[:1]
				batch.Diagnostics = nil
				batch.Checkpoint.DependencyFingerprint = indexDigest(struct{ KnownOccurrence string }{batch.Inscriptions[0].ID})
			}
			batch.Checkpoint.RecordsHash = batchRecordsHash(batch)
			batch.Checkpoint.Commitment = checkpointHash(batch.Checkpoint)
			dir := filepath.Join(a.dataDir, "indexes", "browse", "inscriptions")
			commitPath := filepath.Join(dir, "commits", batch.Checkpoint.Commitment+".json")
			// Install old-format bytes only in this disposable compatibility
			// fixture. Normal publication always uses the full extractor above.
			raw, err := json.Marshal(batch)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			fields["inscriptions"] = fields["full_inscriptions"]
			delete(fields, "full_inscriptions")
			raw, err = json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if err := atomicWriteBytes(commitPath, raw); err != nil {
				t.Fatal(err)
			}
			if err := atomicWriteJSON(filepath.Join(dir, "head.json"), indexBrowseHead{Schema: 1, Blocks: map[string]string{strconv.FormatInt(block.Height, 10): batch.Checkpoint.Commitment}}); err != nil {
				t.Fatal(err)
			}
			saved, have, err := a.browsedBatchContext(context.Background(), "inscriptions", block.Height, block.Hash)
			if err != nil || !have || inscriptionBatchInventoryComplete(saved) == pointOnly {
				t.Fatal("legacy inventory/source scope misclassified", have, err)
			}
			source := a.browseProviderSource("inscriptions")
			if pointOnly {
				if len(source.Ranges) != 0 || source.Complete {
					t.Fatal("one known occurrence became complete block inventory", source)
				}
			} else if !source.Complete || !reflect.DeepEqual(source.Ranges, []heightInterval{{block.Height, block.Height}}) {
				t.Fatal("legitimate old Full scan lost saved coverage", source)
			}
			rows, _, _, complete, err := a.browseQueryRows(context.Background(), "inscriptions", 10, map[string]bool{})
			if err != nil || !complete || len(rows) != 1 {
				t.Fatal("known saved occurrences stopped being queryable", rows, err)
			}
			rowJSON, _ := json.Marshal(rows[0])
			var row struct {
				InventoryComplete bool `json:"inventory_complete"`
			}
			if err := json.Unmarshal(rowJSON, &row); err != nil || row.InventoryComplete == pointOnly {
				t.Fatal("query inventory scope missing", string(rowJSON), err)
			}
			query, err := a.indexQueryBlock(context.Background(), "inscriptions", block.Height, 10)
			if err != nil {
				t.Fatal(err)
			}
			view := query.(map[string]any)
			for _, flag := range []string{"known", "scanned", "total_known", "inventory_complete"} {
				if value, present := view[flag].(bool); !present || value == pointOnly {
					t.Fatal("point query complete-inventory flag is wrong", flag, view)
				}
			}
			if view["known_occurrences"] != len(batch.Inscriptions) {
				t.Fatal("point query inferred complete inventory from known occurrences", view)
			}
			after, err := os.ReadFile(commitPath)
			if err != nil || string(after) != string(raw) {
				t.Fatal("compatibility read rewrote saved bytes", err)
			}
		})
	}
}

func TestInscriptionBrowsePublicationBoundariesDoNotExposePartialInventory(t *testing.T) {
	for _, stop := range []string{"off", "cancel"} {
		t.Run(stop, func(t *testing.T) {
			a, block, extracted := inscriptionInventoryFixture(t)
			request := indexRunRequest{Consumers: []indexRunSelection{{Index: "inscriptions", Armed: true}}}
			if _, err := a.prepareIndexRun(request); err != nil {
				t.Fatal(err)
			}
			batch, err := singletonBrowseBatch("inscriptions", block, &extracted)
			if err != nil {
				t.Fatal(err)
			}
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			pendingPath := filepath.Join(a.dataDir, "indexes", "browse", "inscriptions", "commits", batch.Checkpoint.Commitment+".json")
			stopped := false
			ctx := context.WithValue(base, indexRunPublicationKey{}, func() error {
				if _, err := os.Stat(pendingPath); err == nil && !stopped {
					stopped = true
					if stop == "cancel" {
						cancel()
					} else {
						request.Consumers[0].Armed = false
						if _, err := a.prepareIndexRun(request); err != nil {
							return err
						}
					}
				}
				return base.Err()
			})
			added, err := a.persistBrowseBatch(ctx, "inscriptions", batch)
			if added || err == nil || stop == "cancel" && !errors.Is(err, context.Canceled) || stop == "off" && !errors.Is(err, errIndexRunConsumerStopped) {
				t.Fatal("publication crossed a cancellation/Off fence", added, err)
			}
			if _, err := os.Stat(pendingPath); err != nil || !stopped {
				t.Fatal("fixture did not interrupt an actual pending immutable batch", err)
			}
			_, head, err := a.browseStore("inscriptions")
			if err != nil || len(head.Blocks) != 0 {
				t.Fatal("pending immutable batch became visible inventory", head, err)
			}
			if source := a.browseProviderSource("inscriptions"); len(source.Ranges) != 0 {
				t.Fatal("pending batch became block coverage", source)
			}
			request.Consumers[0].Armed = true
			if _, err := a.prepareIndexRun(request); err != nil {
				t.Fatal(err)
			}
			if added, err := a.persistBrowseBatch(context.Background(), "inscriptions", batch); err != nil || !added {
				t.Fatal("fresh explicit retry did not complete publication", added, err)
			}
		})
	}
}

func TestInscriptionInventoryOffTransactionFallbackDoesNotRepair(t *testing.T) {
	for _, missing := range []string{"active_receipt", "shard_receipt"} {
		t.Run(missing, func(t *testing.T) {
			a, store, txid := olderExplorerFixture064(t, "tx-locator", 2)
			if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: false}, {Index: "inscriptions", Armed: false}}}); err != nil {
				t.Fatal(err)
			}
			path := store.locatorActivePath()
			if missing == "shard_receipt" {
				var err error
				path, err = graphShardPath(store.dir, store.locatorKind(), txid)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Remove(path + ".receipt.json"); err != nil {
				t.Fatal(err)
			}
			before := offLocatorFiles(t, store.dir)
			location, found, err := a.indexedInscriptionTransactionContext(context.Background(), txid, "")
			if err != nil || !found || location.TxID != txid || location.Height != 0 || location.TxIndex != 0 {
				t.Fatal("Off transaction lookup lost authenticated saved position", location, found, err)
			}
			if after := offLocatorFiles(t, store.dir); !reflect.DeepEqual(before, after) {
				t.Fatal("Off transaction fallback changed index bytes or mtimes", before, after)
			}
		})
	}
	t.Run("related_source_binding", func(t *testing.T) {
		a, source, _ := olderExplorerFixture064(t, "inscriptions", 2)
		oldest, err := source.readBatch(source.checkpoint.PreviousCommitment)
		if err != nil {
			t.Fatal(err)
		}
		related, err := openIndexStore(a.dataDir, inscriptionLocatorIndex)
		if err != nil {
			t.Fatal(err)
		}
		related.head.Mode = "lean"
		if err := related.appendInscriptionLocators(oldest, nil, oldest.Checkpoint.From, "ephemeral"); err != nil {
			t.Fatal(err)
		}
		if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "inscriptions", Armed: false}, {Index: "tx-locator", Armed: false}}}); err != nil {
			t.Fatal(err)
		}
		for _, store := range []*indexStore{source, related} {
			if err := os.Remove(store.locatorActivePath() + ".receipt.json"); err != nil {
				t.Fatal(err)
			}
		}
		before := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes"))
		txid := oldest.Inscriptions[0].TxID
		location, found, err := a.indexedInscriptionTransactionContext(context.Background(), txid, oldest.Checkpoint.BlockHash)
		if err != nil || !found || location.Height != oldest.Checkpoint.Height || location.TxIndex != oldest.Inscriptions[0].TxIndex {
			t.Fatal("read-only related lookup lost its exact source binding", location, found, err)
		}
		if after := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes")); !reflect.DeepEqual(before, after) {
			t.Fatal("Off related lookup repaired or rewrote source/locator files")
		}
	})
}

func TestInscriptionInventoryWriterResumeRejectsPointOnlyPrefix(t *testing.T) {
	for _, scenario := range []string{"legacy_full", "point_head", "point_prefix"} {
		t.Run(scenario, func(t *testing.T) {
			a, first, extracted := inscriptionInventoryFixture(t)
			definition, err := findIndexDefinition("inscriptions")
			if err != nil {
				t.Fatal(err)
			}
			legacy := legacyIndexDefinition(definition)
			batch := indexBatch{PreviousBlockHash: first.PreviousBlockHash, Inscriptions: extracted.Occurrences, Diagnostics: extracted.Diagnostics}
			batch.Checkpoint = indexCheckpoint{Schema: legacy.CheckpointSchema, Definition: legacy.ID, Version: legacy.Version, RuleHash: legacy.RuleHash, Network: legacy.Network, From: first.Height, Height: first.Height, BlockHash: first.Hash, DependencyFingerprint: indexDigest(struct{ Block, Parser, Reference string }{first.Hash, inscriptionParserProfile, inscriptionReferenceCommit})}
			if scenario != "legacy_full" {
				batch.Inscriptions = batch.Inscriptions[:1]
				batch.Diagnostics = nil
				batch.Checkpoint.DependencyFingerprint = indexDigest(struct{ KnownOccurrence string }{batch.Inscriptions[0].ID})
			}
			batch.Checkpoint.RecordsHash = batchRecordsHash(batch)
			batch.Checkpoint.Commitment = checkpointHash(batch.Checkpoint)
			dir := filepath.Join(a.dataDir, "indexes", "inscriptions")
			commitPath := filepath.Join(dir, "commits", batch.Checkpoint.Commitment+".json")
			if err := atomicWriteJSON(commitPath, batch); err != nil {
				t.Fatal(err)
			}
			oldBytes, err := os.ReadFile(commitPath)
			if err != nil {
				t.Fatal(err)
			}
			last := batch
			if scenario == "point_prefix" {
				next := bitmapDevelopmentFixtureBlock(first.Height+1, first.Hash, "1.bitmap")
				installRunProtocolHeader(t, a, &next)
				last, err = singletonBrowseBatchMode(indexRunSelection{Index: "inscriptions", indexRunOptions: indexRunOptions{Mode: "full"}}, next, nil)
				if err != nil {
					t.Fatal(err)
				}
				last.Checkpoint.From = first.Height
				last.Checkpoint.PreviousCommitment = batch.Checkpoint.Commitment
				last.Checkpoint.Commitment = checkpointHash(last.Checkpoint)
				if err := atomicWriteJSON(filepath.Join(dir, "commits", last.Checkpoint.Commitment+".json"), last); err != nil {
					t.Fatal(err)
				}
			}
			if err := atomicWriteJSON(filepath.Join(dir, "head.json"), indexHead{Mode: "full", Commitment: last.Checkpoint.Commitment, Retention: "ephemeral"}); err != nil {
				t.Fatal(err)
			}
			request := indexRunRequest{Consumers: []indexRunSelection{{Index: "inscriptions", Armed: true, indexRunOptions: indexRunOptions{Mode: "full"}}}}
			if _, err := a.prepareIndexRun(request); err != nil {
				t.Fatal(err)
			}
			before := offLocatorFiles(t, dir)
			_, err = openIndexStoreContext(context.Background(), a.dataDir, "inscriptions")
			if scenario == "legacy_full" {
				if err != nil {
					t.Fatal("legitimate legacy full extraction cannot resume", err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "complete block-inventory") {
					t.Fatal("point-only history was accepted as a complete writer prefix", err)
				}
				if after := offLocatorFiles(t, dir); !reflect.DeepEqual(before, after) {
					t.Fatal("writer rejection repaired or rewrote a partial prefix")
				}
				request.Consumers[0].Armed = false
				if _, err := a.prepareIndexRun(request); err != nil {
					t.Fatal(err)
				}
				query, err := a.indexQueryBlock(context.Background(), "inscriptions", first.Height, 10)
				if err != nil {
					t.Fatal("positive saved occurrences became unreadable", err)
				}
				view := query.(map[string]any)
				if view["known"] != false || view["inventory_complete"] != false || view["known_occurrences"] != 1 {
					t.Fatal("point prefix became a complete block query", view)
				}
			}
			if after, err := os.ReadFile(commitPath); err != nil || string(oldBytes) != string(after) {
				t.Fatal("saved native receipt bytes changed", err)
			}
		})
	}
}
