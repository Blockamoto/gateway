package main

import (
	"context"
	"path/filepath"
	"testing"
)

// installLegacyRawLocators models reverse shards already written by an older
// Raw format. Production Raw append, open and lookup may never call this path.
func installLegacyRawLocators(t *testing.T, s *indexStore) {
	t.Helper()
	if s.definition.ID != "blocks" {
		t.Fatal("legacy Raw fixture requires a Blocks store")
	}
	var batches []indexBatch
	if err := s.walk(func(b indexBatch) error { batches = append(batches, b); return nil }); err != nil {
		t.Fatal(err)
	}
	// Raw repair now restores block-height membership only. Install this before
	// the explicit old reverse evidence, without restoring a Raw lookup builder.
	if err := s.ensureIndexLocators(context.Background()); err != nil {
		t.Fatal(err)
	}
	indexLocatorMu.Lock()
	defer indexLocatorMu.Unlock()
	for i := len(batches) - 1; i >= 0; i-- {
		b := batches[i]
		if b.Bitcoin == nil || (b.Bitcoin.BodySchema != 0 && b.Bitcoin.BodySchema != 2) {
			t.Fatal("preexisting reverse fixture requires a legacy Raw batch")
		}
		grouped := map[string][][]byte{}
		for _, entry := range transactionBatchEntries(b) {
			path, err := graphShardPath(s.dir, s.locatorKind(), entry.TxID)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := encodeIndexLocator(indexLocatorRecord{TxID: entry.TxID, Commit: b.Checkpoint.Commitment, Height: b.Checkpoint.Height, TxIndex: int(entry.TxIndex)})
			if err != nil {
				t.Fatal(err)
			}
			grouped[path] = append(grouped[path], raw)
		}
		for path, rows := range grouped {
			if err := s.makeLocatorDirectories(filepath.Dir(path)); err != nil {
				t.Fatal(err)
			}
			if err := s.appendBitcoinLocatorRecords(path, rows); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.writeActiveLocatorHead(s.checkpoint); err != nil {
		t.Fatal(err)
	}
}
