package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func exportInventorySource(t *testing.T, legacy, point, browse bool) (*app, indexBatch) {
	t.Helper()
	a, last, _ := inscriptionProjectionSource(t, legacy, point)
	a.updateBackgroundWork.Wait()
	if browse {
		// Convert only this disposable fixture's visible source into a genuine
		// singleton, keeping its complete-scan or known-positive recipe intact.
		if err := os.Remove(filepath.Join(a.dataDir, "indexes", "inscriptions", "head.json")); err != nil {
			t.Fatal(err)
		}
		last.Checkpoint.From = last.Checkpoint.Height
		last.Checkpoint.PreviousCommitment = ""
		last.Checkpoint.Commitment = checkpointHash(last.Checkpoint)
		dir := filepath.Join(a.dataDir, "indexes", "browse", "inscriptions")
		if err := atomicWriteJSON(filepath.Join(dir, "commits", last.Checkpoint.Commitment+".json"), last); err != nil {
			t.Fatal(err)
		}
		if err := atomicWriteJSON(filepath.Join(dir, "head.json"), indexBrowseHead{Schema: 1, Blocks: map[string]string{strconv.FormatInt(last.Checkpoint.Height, 10): last.Checkpoint.Commitment}}); err != nil {
			t.Fatal(err)
		}
	}
	return a, last
}

func TestIndexExportInventoryRejectsKnownPositiveSources(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, browse := range []bool{false, true} {
			name := "current_range"
			if legacy {
				name = "legacy_range"
			}
			if browse {
				name += "_browse"
			}
			t.Run(name, func(t *testing.T) {
				a, _ := exportInventorySource(t, legacy, true, browse)
				before := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes"))
				for _, representation := range []string{"native", "conventional"} {
					q := indexExportRequest{Index: "inscriptions", Representation: representation}
					meta, err := a.indexExportReview(context.Background(), q)
					if err == nil || !strings.Contains(err.Error(), "inventory") || meta.Ready || meta.Complete || len(meta.Coverage) != 0 || meta.Blocks != 0 {
						t.Fatal("known-positive source became a complete inventory export", representation, meta, err)
					}
					if !validHash(meta.Snapshot) {
						t.Fatal("fixture did not reach captured inventory validation", meta)
					}
					for _, action := range []string{"review", "download"} {
						request := q
						if action == "download" {
							request.Snapshot = meta.Snapshot
						}
						body, err := json.Marshal(request)
						if err != nil {
							t.Fatal(err)
						}
						w := httptest.NewRecorder()
						r := indexExportHTTPTestRequest("POST", action, string(body))
						if action == "review" {
							a.handleIndexExportReview(w, r)
						} else {
							a.handleIndexExportDownload(w, r)
						}
						want := "inventory"
						if action == "download" {
							want = "snapshot_expired"
						} // Failed reviews never create downloadable pins.
						if w.Code != 409 || !strings.Contains(w.Body.String(), want) || w.Header().Get("Content-Disposition") != "" || w.Header().Get("X-Gateway-Export-Complete") != "" || strings.Contains(w.Body.String(), `"type":"inscription"`) {
							t.Fatal("partial inventory did not reach its export rejection", action, w.Code, w.Header(), w.Body.String())
						}
					}
				}
				if after := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes")); !reflect.DeepEqual(before, after) {
					t.Fatal("rejected exports changed saved source bytes or mtimes")
				}
			})
		}
	}
}

func TestIndexExportInventoryPreservesLegacyFullSources(t *testing.T) {
	for _, browse := range []bool{false, true} {
		t.Run(strconv.FormatBool(browse), func(t *testing.T) {
			a, _ := exportInventorySource(t, true, false, browse)
			before := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes"))
			for _, representation := range []string{"native", "conventional"} {
				meta, _ := exportTestArtifact(t, a, indexExportRequest{Index: "inscriptions", Representation: representation})
				if !meta.Ready || !meta.Complete || meta.Blocks == 0 || len(meta.Coverage) != 1 {
					t.Fatal("legitimate legacy Full export lost inventory", meta)
				}
			}
			if after := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes")); !reflect.DeepEqual(before, after) {
				t.Fatal("legacy exports changed original source bytes or mtimes")
			}
		})
	}
}

func TestIndexExportInventoryRejectsOlderPointPrefix(t *testing.T) {
	a, _, _ := inscriptionProjectionSource(t, true, false)
	a.updateBackgroundWork.Wait()
	s, err := indexStoreHead(a.dataDir, "inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	var batches []indexBatch
	if err = s.walk(func(b indexBatch) error { batches = append(batches, b); return nil }); err != nil {
		t.Fatal(err)
	}
	previous := ""
	for i := len(batches) - 1; i >= 0; i-- {
		b := batches[i]
		if i == len(batches)-1 {
			b.Inscriptions = b.Inscriptions[:1]
			b.Checkpoint.DependencyFingerprint = indexDigest(struct{ KnownOccurrence string }{b.Inscriptions[0].ID})
			b.Checkpoint.RecordsHash = batchRecordsHash(b)
		}
		b.Checkpoint.PreviousCommitment = previous
		b.Checkpoint.Commitment = checkpointHash(b.Checkpoint)
		if err = atomicWriteJSON(filepath.Join(s.dir, "commits", b.Checkpoint.Commitment+".json"), b); err != nil {
			t.Fatal(err)
		}
		previous = b.Checkpoint.Commitment
	}
	if err = atomicWriteJSON(filepath.Join(s.dir, "head.json"), indexHead{Mode: "full", Commitment: previous, Retention: "ephemeral"}); err != nil {
		t.Fatal(err)
	}
	before := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes"))
	for _, representation := range []string{"native", "conventional"} {
		q := indexExportRequest{Index: "inscriptions", Representation: representation}
		meta, err := a.indexExportReview(context.Background(), q)
		if err == nil || meta.Ready || meta.Complete || len(meta.Coverage) != 0 || meta.Blocks != 0 {
			t.Fatal("complete head laundered an older point-only prefix", representation, meta, err)
		}
		if !validHash(meta.Snapshot) {
			t.Fatal("prefix fixture did not reach captured inventory validation", meta)
		}
		q.Snapshot = meta.Snapshot
		body, _ := json.Marshal(q)
		w := httptest.NewRecorder()
		a.handleIndexExportDownload(w, indexExportHTTPTestRequest("POST", "download", string(body)))
		if w.Code != 409 || !strings.Contains(w.Body.String(), "snapshot_expired") || w.Header().Get("Content-Disposition") != "" || w.Header().Get("X-Gateway-Export-Complete") != "" {
			t.Fatal("older partial prefix did not reach its export rejection", w.Code, w.Header(), w.Body.String())
		}
	}
	if after := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes")); !reflect.DeepEqual(before, after) {
		t.Fatal("prefix rejection changed source bytes or mtimes")
	}
}
