package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func Test077CachedInscriptionIdentityCannotFallbackToOldMirror(t *testing.T) {
	a, _, _, b1 := satRuntime077Real(t)
	id := testID(77910) + "i0"
	number := firstSatAtHeight(1)
	record := inscriptionSatResult{ID: id, Known: true, SatNumber: &number, State: "known", Profile: inscriptionSatProfile, RevealHeight: 1, RevealHash: b1.Hash}
	path := a.inscriptionSatPath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	bytes, _ := json.Marshal(record)
	if err := os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	if known, err := a.knownInscriptionSat(id, 1, b1.Hash); err != nil || known == nil || *known != number {
		t.Fatal(known, err)
	}
	// Selected Core knows a later tip but cannot supply the reveal height. The
	// still-present local mirror must not be substituted for that evidence.
	settings, _, stop := satline077RPC(t, map[int64]string{0: genesisHashDisplay, 2: testID(77912)})
	defer stop()
	a.settings = settings
	if known, err := a.knownInscriptionSat(id, 1, b1.Hash); err == nil || known != nil {
		t.Fatal("old mirror authenticated cached identity", known, err)
	}
	if unchanged, err := os.ReadFile(path); err != nil || string(unchanged) != string(bytes) {
		t.Fatal("unavailable reveal changed cached evidence", err)
	}
}
