package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A shared verified body owned by the genesis TX Locator must not be offered
// as an Inscriptions scan merely because Inscriptions is also armed. The
// fixture uses a real genesis body and synthetic selected protocol-height
// blocks; no user's profile or live process is involved.
func TestInscriptionFanoutRespectsAdmittedStart079(t *testing.T) {
	origin := firstMainnetInscriptionHeight
	zero := int64(0)
	cases := []struct {
		name     string
		globalIn *int64
		from     int64
		early    int64
	}{
		{"default origin with genesis locator", nil, origin, 0},
		{"global genesis does not override origin", &zero, origin, 0},
		{"later admitted start", &zero, origin + 2, origin + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := runTestApp(t)
			end := tc.from + 1
			request := indexRunRequest{In: tc.globalIn, Out: &end, Consumers: []indexRunSelection{
				{Index: "tx-locator", Armed: true},
				{Index: "inscriptions", Armed: true, indexRunOptions: indexRunOptions{Mode: "full", ConventionalIDs: boolPointer(false)}},
			}}
			doc, err := a.prepareIndexRun(request)
			if err != nil {
				t.Fatal(err)
			}
			doc.Run = &indexRunRecord{ID: "inscription-range-regression", State: "running", Request: doc.Prepared,
				InitialTargetHeight: end, TargetHeight: end,
				Consumers: []indexRunProgress{
					{Index: "tx-locator", From: 0, To: end, Height: -1, AdmissionState: "admitted"},
					{Index: "inscriptions", From: tc.from, To: end, Height: tc.from - 1, AdmissionState: "admitted"},
				},
			}
			a.indexRunMu.Lock()
			a.indexRunState = doc
			err = a.persistIndexRunLocked()
			a.indexRunMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}

			var early blockView
			if tc.early == 0 {
				raw := testGenesisBlockPayload(t)
				early, err = parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "real genesis", false)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				early = bitmapDevelopmentFixtureBlock(tc.early, "", "early")
				installRunProtocolHeader(t, a, &early)
			}
			prepareResolvedBlockExtraction(context.Background(), a.dataDir, &early)
			if early.inscriptionExtraction != nil {
				t.Fatal("out-of-range block was parsed as an inscription inventory")
			}
			prepared := &indexRunPrepared{block: early}
			txCalls := 0
			ctx := context.WithValue(context.Background(), indexRunPreparedKey{}, prepared)
			ctx = context.WithValue(ctx, indexRunSourceOwnershipKey{}, indexRunSourceOwnership{RunID: doc.Run.ID, Height: early.Height, Hash: early.Hash})
			ctx = context.WithValue(ctx, indexRunCohortOwnersKey{}, map[string]bool{"tx-locator": true})
			txStore, err := openIndexStore(a.dataDir, "tx-locator")
			if err != nil {
				t.Fatal(err)
			}
			txStore.head.Mode = "lean"
			ctx = context.WithValue(ctx, indexRunBlockConsumersKey{}, map[string]indexRunBlockConsumer{"tx-locator": func(work context.Context) error {
				txCalls++
				if early.Height != 0 {
					return nil
				}
				txStore.locatorContext = work
				return txStore.appendPreparedBlock(early, 0, "ephemeral", nil)
			}})
			rows := a.ingestArmedResolvedBlock(ctx, early)
			if txCalls != 1 {
				t.Fatalf("TX Locator owner did not process its block: %d", txCalls)
			}
			if early.Height == 0 && (txStore.checkpoint == nil || txStore.checkpoint.Height != 0) {
				t.Fatal("TX Locator did not commit the actual genesis block")
			}
			if prepared.extractionAttempted || prepared.extraction != nil {
				t.Fatal("out-of-range block triggered shared inscription extraction")
			}
			for _, row := range rows {
				if row.Index == "inscriptions" && (row.State == "indexed" || row.State == "staged" || row.State == "already_indexed") {
					t.Fatalf("out-of-range inscription capture claimed success: %+v", row)
				}
			}
			headPath := filepath.Join(a.dataDir, "indexes", "browse", "inscriptions", "head.json")
			if _, err := os.Stat(headPath); !os.IsNotExist(err) {
				t.Fatalf("out-of-range block wrote inscription coverage: %v", err)
			}
			if _, have, err := a.browsedBatchContext(context.Background(), "inscriptions", early.Height, early.Hash); err != nil || have {
				t.Fatalf("out-of-range inscription result persisted: have=%t err=%v", have, err)
			}
			store, err := indexStoreHead(a.dataDir, "inscriptions")
			if err != nil || store.checkpoint != nil {
				t.Fatalf("out-of-range block advanced inscription checkpoint: %v", err)
			}
			// A direct sparse commit cannot bypass the same admitted-range fence.
			batch, err := singletonBrowseBatchMode(indexRunSelection{Index: "inscriptions", indexRunOptions: indexRunOptions{Mode: "full", ConventionalIDs: boolPointer(false)}}, early, nil)
			if err != nil {
				t.Fatal(err)
			}
			if added, err := a.persistBrowseBatch(context.Background(), "inscriptions", batch); err == nil || added {
				t.Fatalf("direct out-of-range sparse commit accepted: added=%t err=%v", added, err)
			}
			commitPath := filepath.Join(a.dataDir, "indexes", "browse", "inscriptions", "commits", batch.Checkpoint.Commitment+".json")
			if _, err := os.Stat(commitPath); !os.IsNotExist(err) {
				t.Fatalf("out-of-range sparse record written: %v", err)
			}

			eligible := bitmapDevelopmentFixtureBlock(tc.from, "", "eligible")
			installRunProtocolHeader(t, a, &eligible)
			eligiblePrepared := &indexRunPrepared{block: eligible}
			eligibleCtx := context.WithValue(context.Background(), indexRunPreparedKey{}, eligiblePrepared)
			eligibleCtx = context.WithValue(eligibleCtx, indexRunSourceOwnershipKey{}, indexRunSourceOwnership{RunID: doc.Run.ID, Height: eligible.Height, Hash: eligible.Hash})
			eligibleCtx = context.WithValue(eligibleCtx, indexRunCohortOwnersKey{}, map[string]bool{"tx-locator": true})
			eligibleCtx = context.WithValue(eligibleCtx, indexRunBlockConsumersKey{}, map[string]indexRunBlockConsumer{"tx-locator": func(context.Context) error { return nil }})
			rows = a.ingestArmedResolvedBlock(eligibleCtx, eligible)
			for _, row := range rows {
				if row.Index == "inscriptions" && (row.State == "error" || row.State == "unavailable" || row.State == "off") {
					t.Fatalf("eligible capture rejected: %+v", row)
				}
			}
			saved, have, err := a.browsedBatchContext(context.Background(), "inscriptions", eligible.Height, eligible.Hash)
			if err != nil || !have || saved.InscriptionBlock == nil || len(saved.InscriptionCoordinates) == 0 {
				t.Fatalf("eligible inscription capture missing: have=%t coords=%v err=%v", have, saved.InscriptionCoordinates, err)
			}
			_, head, err := a.browseStore("inscriptions")
			if err != nil || len(head.Blocks) != 1 {
				t.Fatalf("coverage contains unexpected blocks: %+v %v", head.Blocks, err)
			}
		})
	}
}

func TestInscriptionWriterEnforcesMainnetOrigin079(t *testing.T) {
	root := t.TempDir()
	origin := firstMainnetInscriptionHeight
	store, err := openIndexStore(root, "inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	store.head.Mode = "full"
	before := bitmapDevelopmentFixtureBlock(origin-1, "", "before")
	if err := store.appendPreparedBlock(before, 0, "ephemeral", nil); err == nil || store.checkpoint != nil {
		t.Fatalf("pre-origin native inscription write accepted: %v", err)
	}
	locator, err := openIndexStore(root, inscriptionLocatorIndex)
	if err != nil {
		t.Fatal(err)
	}
	locator.head.Mode = "lean"
	if err := locator.appendInscriptionLocators(indexBatch{Checkpoint: indexCheckpoint{Height: origin - 1}}, &before, 0, "ephemeral"); err == nil || locator.checkpoint != nil {
		t.Fatalf("pre-origin related locator write accepted: %v", err)
	}
	atOrigin := bitmapDevelopmentFixtureBlock(origin, "", "at origin")
	extracted, err := extractInscriptionOccurrences(atOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.appendPreparedBlock(atOrigin, origin, "ephemeral", &extracted); err != nil {
		t.Fatal("origin write failed:", err)
	}
	source, err := store.readBatch(store.checkpoint.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	if err = locator.appendInscriptionLocators(source, &atOrigin, origin, "ephemeral"); err != nil {
		t.Fatal("origin related locator write failed:", err)
	}
	if locator.checkpoint == nil || locator.checkpoint.Height != origin {
		t.Fatal("origin locator not committed")
	}
	if err = requireInscriptionIngestionRange("bitcoin-testnet", 5, 0); err != nil {
		t.Fatal("mainnet origin applied to another network:", err)
	}
	if err = requireInscriptionIngestionRange("bitcoin-testnet", 5, 6); err == nil {
		t.Fatal("other-network admitted start ignored")
	}
}

func TestInscriptionOriginDoesNotSuppressOtherConsumerExtraction079(t *testing.T) {
	a := runTestApp(t)
	_, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "inscriptions", Armed: true}, {Index: "sat-state", Armed: true}}})
	if err != nil {
		t.Fatal(err)
	}
	raw := testGenesisBlockPayload(t)
	block, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "real genesis", false)
	if err != nil {
		t.Fatal(err)
	}
	prepareResolvedBlockExtraction(context.Background(), a.dataDir, &block)
	if block.inscriptionExtraction == nil || validateCompleteInscriptionInventory(block, block.inscriptionExtraction) != nil {
		t.Fatal("independent eligible Sats consumer lost shared witness extraction")
	}
}
