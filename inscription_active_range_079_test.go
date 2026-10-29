package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInscriptionActiveRangeKeepsReviewedRunMarks079(t *testing.T) {
	origin := firstMainnetInscriptionHeight
	future := origin + 10
	end := origin + 20
	selection := indexRunSelection{Index: "inscriptions", Armed: true}
	d := indexRunDocument{
		Configured: true,
		Prepared:   indexRunRequest{In: &future, Consumers: []indexRunSelection{selection}},
		Run: &indexRunRecord{
			State:   "running",
			Request: indexRunRequest{In: &origin, Out: &end, Consumers: []indexRunSelection{selection}},
			Consumers: []indexRunProgress{{Index: "inscriptions", From: origin, To: end,
				AdmissionState: "admitted"}},
		},
	}
	if err := resolvedInscriptionRangeAllowed(d, origin+1); err != nil {
		t.Fatalf("future In changed the active reviewed range: %v", err)
	}
	d.Run.State = "complete"
	if err := resolvedInscriptionRangeAllowed(d, origin+1); err == nil {
		t.Fatal("future In was ignored after the active run ended")
	}
	if err := resolvedInscriptionRangeAllowed(d, future); err != nil {
		t.Fatalf("future In rejected its own first block: %v", err)
	}
}

func TestInscriptionCommitBoundaryRejectsPreOrigin079(t *testing.T) {
	for _, id := range []string{"inscriptions", inscriptionLocatorIndex} {
		t.Run(id, func(t *testing.T) {
			root := t.TempDir()
			store, err := openIndexStore(root, id)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.commitPreparedIndexBatch(indexBatch{}, firstMainnetInscriptionHeight-1, testID(1), 0, "ephemeral"); err == nil {
				t.Fatal("low-level inscription commit accepted a pre-origin block")
			}
			if _, err := os.Stat(filepath.Join(root, "indexes", id, "commits")); !os.IsNotExist(err) {
				t.Fatalf("rejected commit wrote records: %v", err)
			}
		})
	}
}
