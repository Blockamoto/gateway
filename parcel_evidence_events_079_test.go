package main

import "testing"

func TestParcelEvidenceEventsAreRouteScopedAndDeduplicated079(t *testing.T) {
	root := t.TempDir()
	if parcelEvidenceEpoch079(root, "tx-locators") != 0 || parcelEvidenceEpoch079(root, "txio-links") != 0 {
		t.Fatal("new root inherited evidence events")
	}
	notifyParcelIndexCommit079(root, "tx-locator")
	if parcelEvidenceEpoch079(root, "tx-locators") != 1 || parcelEvidenceEpoch079(root, "txio-links") != 0 {
		t.Fatal("unrelated route was advanced")
	}
	notifyParcelDistrictEvidence079(root, 123)
	if parcelDistrictEvidenceEpoch079(root, 123) != 1 || parcelDistrictEvidenceEpoch079(root, 124) != 0 || parcelEvidenceEpoch079(root, "txio-links") != 0 {
		t.Fatal("District event was not height-scoped")
	}
	notifyParcelEvidence079(root, "retained-raw")
	if parcelEvidenceEpoch079(root, "retained-raw") != 1 || parcelEvidenceEpoch079(root, "txio-links") != 0 {
		t.Fatal("retained Raw event was not route-scoped")
	}
}
