package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func sharedSealedGenesis(t *testing.T) blockView {
	t.Helper()
	raw := testGenesisBlockPayload(t)
	b, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "private spool fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	x, err := extractInscriptionOccurrences(b)
	if err != nil {
		t.Fatal(err)
	}
	b.inscriptionExtraction = &x
	return b
}

func TestSharedBlockSpillRestoresAuthenticatedFactsWithoutParsing(t *testing.T) {
	b := sharedSealedGenesis(t)
	q := &resolvedIngestionQueue{maxResidentBytes: 1}
	defer q.close()
	if err := queueResolvedBlock(q, b); err != nil {
		t.Fatal(err)
	}
	if len(q.blocks) != 1 || q.blocks[0].block != nil || q.spoolDir == "" || q.queuedBytes == 0 {
		t.Fatal("spill not exercised", q)
	}
	dir := q.spoolDir
	restored, have, err := q.next()
	if err != nil || !have {
		t.Fatal(have, err)
	}
	if q.queuedBytes != 0 || len(q.blocks) != 0 {
		t.Fatal("drained bytes retained", q.queuedBytes)
	}
	if !bytes.Equal(restored.verifiedRaw, b.verifiedRaw) || restored.verifiedRawProof == nil || *restored.verifiedRawProof != *b.verifiedRawProof || restored.Transactions[0]._txHash != b.Transactions[0]._txHash {
		t.Fatal("private verified facts changed")
	}
	if err := validateCompleteInscriptionInventory(restored, restored.inscriptionExtraction); err != nil {
		t.Fatal(err)
	}
	// Both paths consume the same sealed extraction and produce identical native
	// commitments. No source acquisition or Bitcoin-body decode is available.
	var commitments []string
	for _, block := range []blockView{b, restored} {
		s, err := openIndexStore(t.TempDir(), "inscriptions")
		if err != nil {
			t.Fatal(err)
		}
		s.head.Mode = "lean"
		if err = s.appendPreparedBlock(block, 0, "ephemeral", block.inscriptionExtraction); err != nil {
			t.Fatal(err)
		}
		commitments = append(commitments, s.checkpoint.Commitment)
	}
	if commitments[0] != commitments[1] {
		t.Fatal("spill changed native result", commitments)
	}
	if err = q.close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("private spill directory survived", err)
	}
}

func TestSharedBlockSpillCorruptionAndCancellationFailClosedCleanup(t *testing.T) {
	for _, mode := range []string{"corrupt", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			a := runTestApp(t)
			if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "inscriptions", Armed: true}}}); err != nil {
				t.Fatal(err)
			}
			q := &resolvedIngestionQueue{maxResidentBytes: 1}
			defer q.close()
			if err := queueResolvedBlock(q, sharedSealedGenesis(t)); err != nil {
				t.Fatal(err)
			}
			dir := q.spoolDir
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), resolvedIngestionKey{}, q))
			defer cancel()
			if mode == "cancel" {
				cancel()
			} else {
				path := q.blocks[0].path
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				raw[len(raw)-1] ^= 1
				if err = os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			a.drainResolvedBlockIngestion(ctx, q)
			if mode == "corrupt" && (q.err() == nil || len(q.results) == 0 || !strings.Contains(q.results[0].Error, "checksum")) {
				t.Fatal("corruption not observable", q.err(), q.results)
			}
			if _, have, err := a.browsedBatchContext(context.Background(), "inscriptions", 0, genesisHashDisplay); err != nil || have {
				t.Fatal("untrusted/cancelled spool published", have, err)
			}
			if err := q.close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("cancel/error spill leaked", err)
			}
		})
	}
}

func TestSharedBlockSpillAggregateQuotaAndDrainedByteAccounting(t *testing.T) {
	b := sharedSealedGenesis(t)
	raw, err := json.Marshal(resolvedSpoolBlock{Block: b, Raw: b.verifiedRaw, Extraction: b.inscriptionExtraction})
	if err != nil {
		t.Fatal(err)
	}
	otherRaw := mainnetBlockOne(t)
	h := hash256(otherRaw[:80])
	other, err := parseBlockDetailed(1, reverseHex(h[:]), otherRaw[:80], otherRaw, "quota fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, drainFirst := range []bool{false, true} {
		q := &resolvedIngestionQueue{maxResidentBytes: 1, maxQueuedBytes: int64(len(raw)) + 1}
		if err = queueResolvedBlock(q, b); err != nil {
			t.Fatal(err)
		}
		dir := q.spoolDir
		if drainFirst {
			if _, have, err := q.next(); err != nil || !have {
				t.Fatal(have, err)
			}
		}
		err = queueResolvedBlock(q, other)
		if drainFirst && err != nil {
			t.Fatal("drained quota was not released", err)
		}
		if !drainFirst && (err == nil || !strings.Contains(err.Error(), "quota") || q.err() == nil) {
			t.Fatal("aggregate quota not enforced", err)
		}
		if q.queuedBytes > q.maxQueuedBytes {
			t.Fatal("operation exceeded disk quota", q.queuedBytes)
		}
		if err = q.close(); err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("quota cleanup leaked", err)
		}
	}
}
