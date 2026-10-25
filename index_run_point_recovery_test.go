package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func sharedSavedGenesisPoint(t *testing.T) (*app, *indexRunConsumer, blockView) {
	t.Helper()
	a := runTestApp(t)
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}}}); err != nil {
		t.Fatal(err)
	}
	b := sharedSealedGenesis(t)
	for _, row := range a.ingestResolvedVerifiedBlock(context.Background(), b) {
		if row.State == "error" || row.State == "unavailable" {
			t.Fatal(row)
		}
	}
	s, err := openIndexStore(a.dataDir, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	s.head.Mode = "lean"
	if err = s.BeginLocatorBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := &indexRunConsumer{selection: indexRunSelection{Index: "tx-locator", Armed: true}, progress: indexRunProgress{Index: "tx-locator", From: 0, Height: -1}, store: s, reconciled: true}
	t.Cleanup(c.abortLocatorBatch)
	return a, c, b
}

func TestSharedBlockLegacyBitcoinPointFallsBackWithoutWaitingOrRewriting(t *testing.T) {
	a := runTestApp(t)
	b := sharedSealedGenesis(t)
	s := a.newBrowseStore("tx-locator")
	d := legacyIndexDefinition(s.definition)
	batch := indexBatch{PreviousBlockHash: b.PreviousBlockHash, Bitcoin: &bitcoinIndexBlock{VerifierVersion: blockVerifierVersion, TxIDs: []string{b.Transactions[0].TxID}}}
	c := indexCheckpoint{Schema: d.CheckpointSchema, Definition: d.ID, Version: d.Version, RuleHash: d.RuleHash, Network: d.Network, From: 0, Height: 0, BlockHash: b.Hash, DependencyFingerprint: indexDigest(struct{ Block, Parser, Reference string }{b.Hash, inscriptionParserProfile, inscriptionReferenceCommit}), RecordsHash: batchRecordsHash(batch)}
	c.Commitment = checkpointHash(c)
	batch.Checkpoint = c
	path := filepath.Join(s.dir, "commits", c.Commitment+".json")
	if err := atomicWriteJSON(path, batch); err != nil {
		t.Fatal(err)
	}
	headPath := filepath.Join(s.dir, "head.json")
	if err := atomicWriteJSON(headPath, indexBrowseHead{Schema: 1, Blocks: map[string]string{"0": c.Commitment}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	headBefore, _ := os.ReadFile(headPath)
	if got, have, err := a.browsedBatchContext(context.Background(), "tx-locator", 0, b.Hash); err != nil || !have || got.Checkpoint.Version != 1 || got.TransactionLocator != nil || got.Bitcoin == nil {
		t.Fatal("genuine v1 point not authenticated", have, err)
	}
	var sources int
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) { sources++; return b, nil }
	zero := int64(0)
	if _, err := a.startIndexRun(context.Background(), indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}}}, ""); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" || sources != 1 || done.Run.Counts.SourceReads != 1 || done.Run.Counts.DecodedBlocks != 1 {
		t.Fatal("legacy point caused waiting or duplicate acquisition", done.Run, sources)
	}
	if after, _ := os.ReadFile(path); string(before) != string(after) {
		t.Fatal("legacy checkpoint rewritten")
	}
	if after, _ := os.ReadFile(headPath); string(headBefore) != string(after) {
		t.Fatal("legacy point membership rewritten")
	}
	current, err := indexStoreHead(a.dataDir, "tx-locator")
	if err != nil || current.checkpoint == nil || current.checkpoint.Version != 2 || current.checkpoint.Height != 0 {
		t.Fatal("normal current writer did not finish", current, err)
	}
}

func TestSharedBlockPointPromotionAbortPreservesSavedIsland(t *testing.T) {
	a, c, b := sharedSavedGenesisPoint(t)
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			if err := c.store.BeginLocatorBatch(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		if promoted, err := a.promoteRunTransactionPoint(context.Background(), c, 0, b.Hash, "ephemeral"); err != nil || !promoted {
			t.Fatal(promoted, err)
		}
		if n, _, _ := c.store.LocatorBatchStats(); n != 1 || c.checkpoint() != nil {
			t.Fatal("promotion was not volatile", n)
		}
		if _, have, err := a.browsedBatchContext(context.Background(), "tx-locator", 0, b.Hash); err != nil || !have {
			t.Fatal("logical promotion hid durable point", have, err)
		}
		if attempt == 0 {
			c.abortLocatorBatch()
		}
	}
	if err := c.store.FlushLocatorBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.checkpoint() == nil || c.checkpoint().Height != 0 {
		t.Fatal("point replay failed to become durable")
	}
}

func TestSharedBlockPointAutoFlushFailureAbortsBeforeGuardRelease(t *testing.T) {
	a, c, b := sharedSavedGenesisPoint(t)
	c.store.locatorBatch.maxRecords = 1 // accept the logical commit, then flush it
	fault := errors.New("injected promotion auto-flush acknowledgement failure")
	c.store.locatorSyncDirectory = func(string) error { return fault }
	guard, cancel := context.WithCancel(context.Background())
	ctx := context.WithValue(guard, indexLocatorBatchGuardKey{}, guard)
	promoted, err := a.promoteRunTransactionPoint(ctx, c, 0, b.Hash, "ephemeral")
	if promoted || !errors.Is(err, fault) {
		t.Fatal("automatic-flush fault not reached", promoted, err)
	}
	// The actual promotion error path must reset the stage before its caller
	// releases/cancels the guard. No cancelled staged checkpoint can be skipped.
	cancel()
	if c.store.locatorBatch != nil || c.logicalCheckpoint() != nil || c.next(0) != 0 || c.reconciled || c.progress.AddedBlocks != 0 {
		t.Fatal("failed promotion retained a cancelled logical step", c)
	}
	if _, have, err := a.browsedBatchContext(context.Background(), "tx-locator", 0, b.Hash); err != nil || !have {
		t.Fatal("failed promotion lost original point", have, err)
	}
	c.store.locatorSyncDirectory = nil
	if err = c.store.BeginLocatorBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if promoted, err = a.promoteRunTransactionPoint(context.Background(), c, 0, b.Hash, "ephemeral"); err != nil || !promoted {
		t.Fatal("exact zero-source replay failed", promoted, err)
	}
	if err = c.store.FlushLocatorBatch(context.Background()); err != nil {
		t.Fatal("retry retained poisoned guard", err)
	}
}

func TestSharedBlockPointPromotionRejectsUnprovenOrderedFacts(t *testing.T) {
	for _, mode := range []string{"reordered", "subset", "source_commitment", "stale_source"} {
		t.Run(mode, func(t *testing.T) {
			a := runTestApp(t)
			if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}}}); err != nil {
				t.Fatal(err)
			}
			b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
			sharedProtocolHeader(t, a, &b)
			batch, err := singletonBrowseBatch("tx-locator", b, nil)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "reordered":
				p := batch.TransactionLocator.Block.Transactions
				p[0].TxID, p[1].TxID = p[1].TxID, p[0].TxID
			case "subset":
				batch.TransactionLocator.Block.Transactions = batch.TransactionLocator.Block.Transactions[:1]
				one := uint32(1)
				batch.TransactionLocator.Block.TransactionCount = &one
			case "source_commitment":
				batch.TransactionLocator.SourceCommitment = indexDigest("unrelated sparse source")
			case "stale_source":
				batch.Checkpoint.BlockHash = genesisHashDisplay
			}
			// Compose an internally hash-consistent adversarial saved point. Native
			// receipt authentication alone cannot prove its all-transaction claim;
			// the independent selected-header Merkle root must still agree.
			batch.Checkpoint.RecordsHash = batchRecordsHash(batch)
			batch.Checkpoint.Commitment = checkpointHash(batch.Checkpoint)
			browse := a.newBrowseStore("tx-locator")
			if err = atomicWriteJSON(filepath.Join(browse.dir, "commits", batch.Checkpoint.Commitment+".json"), batch); err != nil {
				t.Fatal(err)
			}
			if err = atomicWriteJSON(filepath.Join(browse.dir, "head.json"), indexBrowseHead{Schema: 1, Blocks: map[string]string{fmt.Sprint(b.Height): batch.Checkpoint.Commitment}}); err != nil {
				t.Fatal(err)
			}
			s, err := openIndexStore(a.dataDir, "tx-locator")
			if err != nil {
				t.Fatal(err)
			}
			s.head.Mode = "lean"
			if err = s.BeginLocatorBatch(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer s.AbortLocatorBatch()
			c := &indexRunConsumer{selection: indexRunSelection{Index: "tx-locator", Armed: true}, progress: indexRunProgress{From: b.Height, Height: b.Height - 1}, store: s}
			promoted, err := a.promoteRunTransactionPoint(context.Background(), c, b.Height, b.Hash, "ephemeral")
			if promoted || mode != "stale_source" && err == nil {
				t.Fatal("unproven point promoted", promoted, err)
			}
			if c.logicalCheckpoint() != nil {
				t.Fatal("invalid point advanced staged head")
			}
		})
	}
}
