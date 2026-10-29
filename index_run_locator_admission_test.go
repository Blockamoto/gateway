package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// Exercise the actual durable amendment and physical store flush, without
// starting a background worker or altering any non-disposable profile.
func locatorAdmissionFixture(t *testing.T, live bool) (*app, *indexStore, indexRunDocument, []blockView) {
	t.Helper()
	a := runTestApp(t)
	zero := int64(0)
	request := indexRunRequest{In: &zero, Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true, Live: live}, {Index: "txo-spender", Armed: true, Live: live}}}
	doc, err := a.prepareIndexRun(request)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := a.planIndexRun(context.Background(), doc.Prepared)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	for i := range plan.Consumers {
		plan.Consumers[i].AdmissionState = "admitted"
	}
	doc.Run = &indexRunRecord{ID: "locator-live-admission", AdmissionRevision: 1, State: "running", Phase: "boundary", FrontierHeight: 1, Request: doc.Prepared, InitialTargetHeight: plan.TargetHeight, TargetHeight: plan.TargetHeight, TargetHash: plan.TargetHash, Consumers: plan.Consumers}
	saveLocatorAdmissionFixture(t, a, doc)
	raws := [][]byte{testGenesisBlockPayload(t), mainnetBlockOne(t)}
	blocks := make([]blockView, 0, len(raws))
	for height, raw := range raws {
		hash := hash256(raw[:80])
		block, e := parseBlockDetailed(int64(height), reverseHex(hash[:]), raw[:80], raw, "locator admission fixture", false)
		if e != nil {
			t.Fatal(e)
		}
		blocks = append(blocks, block)
	}
	store, err := openIndexStore(a.dataDir, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.BeginLocatorBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.AbortLocatorBatch)
	return a, store, cloneIndexRunDocument(doc), blocks
}
func saveLocatorAdmissionFixture(t *testing.T, a *app, doc indexRunDocument) {
	t.Helper()
	a.indexRunMu.Lock()
	a.indexRunState = cloneIndexRunDocument(doc)
	err := a.persistIndexRunLocked()
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}
func stageLocatorAdmissionBlock(t *testing.T, a *app, s *indexStore, doc indexRunDocument, block blockView) {
	t.Helper()
	selection, _ := runSelection(doc.Run.Request, "tx-locator")
	progress, _ := indexRunProgressFor(doc.Run, "tx-locator")
	guard, release := a.indexRunLocatorBatchGuard(context.Background(), doc.Run.ID, selection, progress, block.Height, block.Hash, false)
	t.Cleanup(release)
	s.locatorContext = context.WithValue(context.Background(), indexLocatorBatchGuardKey{}, guard)
	if err := s.appendPreparedBlock(block, 0, "ephemeral", nil); err != nil {
		t.Fatal(err)
	}
}

func TestTimelineRunLocatorPendingFlushAcceptsLegalLiveAmendment(t *testing.T) {
	for _, live := range []bool{false, true} {
		for _, cap := range []string{"records", "bytes"} {
			for _, member := range []string{"tx-locator", "txo-spender"} {
				t.Run(fmt.Sprintf("live_%t_to_%t/%s/%s", live, !live, cap, member), func(t *testing.T) {
					a, s, doc, blocks := locatorAdmissionFixture(t, live)
					unrelated, err := openIndexStore(a.dataDir, "txo-spender")
					if err != nil {
						t.Fatal(err)
					}
					if err = unrelated.appendBlock(blocks[0], 0, "ephemeral"); err != nil {
						t.Fatal(err)
					}
					before := unrelated.checkpoint.Commitment
					stageLocatorAdmissionBlock(t, a, s, doc, blocks[0])
					if n, _, _ := s.LocatorBatchStats(); n != 1 || s.DurableCheckpoint() != nil {
						t.Fatal("first block was not staged", n, s.DurableCheckpoint())
					}
					amended, err := a.amendIndexRun(context.Background(), indexRunAmendment{RunID: doc.Run.ID, AdmissionRevision: 1, Consumers: []indexRunSelection{{Index: member, Armed: true, Live: !live}}})
					if err != nil || amended.Run.AdmissionRevision != 2 {
						t.Fatal(amended, err)
					}
					if cap == "records" {
						s.locatorBatch.maxRecords = 2
					} else {
						s.locatorBatch.maxJSONBytes = 1
					}
					stageLocatorAdmissionBlock(t, a, s, amended, blocks[1])
					if n, _, _ := s.LocatorBatchStats(); n != 0 {
						t.Fatal("early flush retained a poisoned stage", n)
					}
					if checkpoint := s.DurableCheckpoint(); checkpoint == nil || checkpoint.Height != 1 {
						t.Fatal("legal Live amendment prevented durable advancement", checkpoint)
					}
					reopened, err := indexStoreHead(a.dataDir, "tx-locator")
					if err != nil || reopened.checkpoint == nil || reopened.checkpoint.Height != 1 {
						t.Fatal("flushed locator prefix was not durable", reopened, err)
					}
					other, err := indexStoreHead(a.dataDir, "txo-spender")
					if err != nil || other.checkpoint == nil || other.checkpoint.Commitment != before {
						t.Fatal("locator flush changed another consumer", other, err)
					}
					if err = unrelated.appendBlock(blocks[1], 0, "ephemeral"); err != nil {
						t.Fatal("unrelated consumer stopped advancing", err)
					}
					other, err = indexStoreHead(a.dataDir, "txo-spender")
					if err != nil || other.checkpoint == nil || other.checkpoint.Height != 1 {
						t.Fatal("unrelated consumer did not continue", other, err)
					}
				})
			}
		}
	}
}

func TestTimelineRunLocatorPendingFlushRetainsAdmissionFences(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*indexRunDocument)
		want   error
	}{
		{"prepared Off", func(d *indexRunDocument) {
			for i := range d.Prepared.Consumers {
				if d.Prepared.Consumers[i].Index == "tx-locator" {
					d.Prepared.Consumers[i].Armed = false
				}
			}
		}, nil},
		{"requested Off", func(d *indexRunDocument) {
			for i := range d.Run.Request.Consumers {
				if d.Run.Request.Consumers[i].Index == "tx-locator" {
					d.Run.Request.Consumers[i].Armed = false
				}
			}
		}, errIndexRunConsumerStopped},
		{"replacement run", func(d *indexRunDocument) { d.Run.ID = "replacement" }, errIndexRunConsumerStopped},
		{"changed boundary", func(d *indexRunDocument) {
			for i := range d.Run.Consumers {
				if d.Run.Consumers[i].Index == "tx-locator" {
					next := int64(1)
					d.Run.Consumers[i].EffectiveFrom = &next
				}
			}
		}, errIndexRunAdmissionChanged},
		{"changed admitted options", func(d *indexRunDocument) {
			for i := range d.Run.Request.Consumers {
				if d.Run.Request.Consumers[i].Index == "tx-locator" {
					d.Run.Request.Consumers[i].Mode = "full"
				}
			}
		}, errIndexRunAdmissionChanged},
		{"changed options", func(d *indexRunDocument) {
			for i := range d.Prepared.Consumers {
				if d.Prepared.Consumers[i].Index == "tx-locator" {
					d.Prepared.Consumers[i].Mode = "full"
				}
			}
		}, nil},
		{"clipped Out", func(d *indexRunDocument) { zero := int64(0); d.Run.Request.Out = &zero }, errIndexRunAdmissionChanged},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, s, doc, blocks := locatorAdmissionFixture(t, false)
			stageLocatorAdmissionBlock(t, a, s, doc, blocks[0])
			stageLocatorAdmissionBlock(t, a, s, doc, blocks[1])
			test.change(&doc)
			saveLocatorAdmissionFixture(t, a, doc)
			err := s.FlushLocatorBatch(context.Background())
			if test.want == nil {
				if err != nil {
					t.Fatal("staged choices changed the admitted run", err)
				}
				current, readErr := indexStoreHead(a.dataDir, "tx-locator")
				if readErr != nil || current.checkpoint == nil || current.checkpoint.Height != 1 {
					t.Fatal("admitted batch did not publish", current, readErr)
				}
				return
			}
			if !errors.Is(err, test.want) {
				t.Fatal("changed intent published pending locator batch", err, test.want)
			}
			current, err := indexStoreHead(a.dataDir, "tx-locator")
			if err != nil || current.checkpoint != nil {
				t.Fatal("rejected batch advanced durable head", current, err)
			}
		})
	}
}
