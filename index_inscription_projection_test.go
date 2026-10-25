package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Install authenticated native heads, not injected status instances. The old
// point recipe stays readable, but it cannot attest a whole-block inventory.
func inscriptionProjectionSource(t *testing.T, legacy, point bool) (*app, indexBatch, map[string][]byte) {
	t.Helper()
	a := runTestApp(t)
	a.settings.OrdEnabled = true
	d, err := findIndexDefinition("inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	if legacy {
		d = legacyIndexDefinition(d)
	}
	origin, end := int64(792435), int64(792442)
	previousHash, previousCommit := "", ""
	files := map[string][]byte{}
	var last indexBatch
	for h := origin; h <= end; h++ {
		block := bitmapDevelopmentFixtureBlock(h, previousHash, "0.bitmap", "ordinary inscription")
		installRunProtocolHeader(t, a, &block)
		extracted, err := extractInscriptionOccurrences(block)
		if err != nil {
			t.Fatal(err)
		}
		batch, err := singletonBrowseBatchMode(indexRunSelection{Index: "inscriptions", indexRunOptions: indexRunOptions{Mode: "full", ConventionalIDs: boolPointer(false)}}, block, &extracted)
		if err != nil {
			t.Fatal(err)
		}
		if legacy {
			batch.InscriptionBlock, batch.InscriptionCoordinates = nil, nil
		}
		if point {
			batch.Inscriptions = batch.Inscriptions[:1]
			if batch.InscriptionCoordinates != nil {
				batch.InscriptionCoordinates = batch.InscriptionCoordinates[:1]
			}
			batch.Diagnostics = nil
			// This authenticated legacy metadata spelling is accepted by the
			// reader too; it must not turn a point source into numbering readiness.
			batch.Numbering = &inscriptionNumberingSnapshot{Profile: inscriptionNumberingProfile, State: "complete"}
		}
		batch.Checkpoint = indexCheckpoint{Schema: d.CheckpointSchema, Definition: d.ID, Version: d.Version, RuleHash: d.RuleHash, Network: d.Network, From: origin, Height: h, BlockHash: block.Hash, PreviousCommitment: previousCommit, DependencyFingerprint: indexDigest(struct{ Block, Parser, Reference string }{block.Hash, inscriptionParserProfile, inscriptionReferenceCommit})}
		if point {
			batch.Checkpoint.DependencyFingerprint = indexDigest(struct{ KnownOccurrence string }{batch.Inscriptions[0].ID})
		}
		batch.Checkpoint.RecordsHash = batchRecordsHash(batch)
		batch.Checkpoint.Commitment = checkpointHash(batch.Checkpoint)
		raw, err := json.Marshal(batch)
		if err != nil {
			t.Fatal(err)
		}
		if legacy {
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
		}
		name := filepath.Join(a.dataDir, "indexes", "inscriptions", "commits", batch.Checkpoint.Commitment+".json")
		if err := atomicWriteBytes(name, raw); err != nil {
			t.Fatal(err)
		}
		files[name] = raw
		last, previousHash, previousCommit = batch, block.Hash, batch.Checkpoint.Commitment
	}
	headPath := filepath.Join(a.dataDir, "indexes", "inscriptions", "head.json")
	if err := atomicWriteJSON(headPath, indexHead{Mode: "full", Commitment: previousCommit, Retention: "ephemeral"}); err != nil {
		t.Fatal(err)
	}
	files[headPath], err = os.ReadFile(headPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.prepareIndexRun(indexRunRequest{In: &origin, Out: &end, Consumers: []indexRunSelection{{Index: "inscriptions", Armed: false}}}); err != nil {
		t.Fatal(err)
	}
	return a, last, files
}

func TestInscriptionNativeHeadInventoryProjection(t *testing.T) {
	for _, recipe := range []struct {
		name          string
		legacy, point bool
	}{{"current_complete", false, false}, {"legacy_complete", true, false}, {"current_point", false, true}, {"legacy_point", true, true}} {
		t.Run(recipe.name, func(t *testing.T) {
			a, last, files := inscriptionProjectionSource(t, recipe.legacy, recipe.point)
			s, err := indexStoreHead(a.dataDir, "inscriptions")
			if err != nil || s.checkpoint == nil || s.checkpoint.Commitment != last.Checkpoint.Commitment {
				t.Fatal("native head was not accepted by the real reader", err)
			}
			saved, err := s.readBatch(s.checkpoint.Commitment)
			if err != nil || inscriptionBatchInventoryComplete(saved) == recipe.point {
				t.Fatal("native recipe scope was not distinguished", err)
			}
			status := a.indexStatus()
			var instance *indexInstance
			for i := range status.Instances {
				if status.Instances[i].Definition == "inscriptions" {
					instance = &status.Instances[i]
				}
			}
			if instance == nil || !instance.Queryable || instance.Checkpoint.Commitment != last.Checkpoint.Commitment {
				t.Fatal("saved positive records/checkpoint stopped being inspectable", instance)
			}
			track := timelineTrack(t, status.Timeline, "inscriptions")
			if recipe.point {
				if len(instance.Coverage) != 0 || len(instance.Gaps) != 0 || len(track.Coverage) != 0 || len(track.RecordedCoverage) != 0 || track.CoverageComplete || instance.Completeness != "known_occurrences_only; complete_block_inventory_unknown" {
					t.Fatal("point-only native range became complete inventory", *instance, track)
				}
			} else {
				want := []heightInterval{{792435, 792442}}
				if !reflect.DeepEqual(instance.Coverage, want) || !reflect.DeepEqual(track.Coverage, want) || !track.CoverageComplete {
					t.Fatal("saved eight-block complete inventory disappeared while Off", *instance, track)
				}
			}
			query, err := a.indexQueryBlock(context.Background(), "inscriptions", last.Checkpoint.Height, 10)
			if err != nil {
				t.Fatal(err)
			}
			view := query.(map[string]any)
			if view["known"].(bool) == recipe.point || view["total_known"].(bool) == recipe.point || view["inventory_complete"].(bool) == recipe.point || view["known_occurrences"].(int) != len(last.Inscriptions) {
				t.Fatal("native point query lost positives or inferred complete inventory", view)
			}
			for _, provider := range status.Providers {
				if provider.ID == "inscriptions" && len(provider.Coverage) != 0 {
					t.Fatal("unrelated provider invented native inventory coverage", provider)
				}
				if recipe.point && provider.ID == "inscriptions" && provider.Provider == "gateway_canonical_numbering" && provider.Queryable {
					t.Fatal("point-source metadata became complete numbering readiness", provider)
				}
				if provider.ID == "inscriptions" && provider.Provider == "gateway_known_id" && !provider.Queryable {
					t.Fatal("inventory projection disabled the independent known-ID resolver", provider)
				}
			}
			for name, before := range files {
				after, err := os.ReadFile(name)
				if err != nil || !reflect.DeepEqual(after, before) {
					t.Fatal("projection changed saved native bytes", name, err)
				}
			}
			if folder := os.Getenv("GATEWAY_INSCRIPTION_PROJECTION_DIR"); folder != "" {
				if err := os.MkdirAll(folder, 0700); err != nil {
					t.Fatal(err)
				}
				if err := atomicWriteJSON(filepath.Join(folder, recipe.name+".json"), status); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
