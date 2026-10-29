package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParcelLiveAdmissionRequiresLiveDependencies079(t *testing.T) {
	for _, route := range []struct{ name, producer string }{{"retained-raw", "blocks"}, {"tx-locators", "tx-locator"}, {"txio-links", "txo-spender"}} {
		t.Run(route.name, func(t *testing.T) {
			zero, end := int64(0), int64(800000)
			req := indexRunRequest{In: &zero, Out: &end, Consumers: []indexRunSelection{{Index: bitmapStandardDistrictID, Armed: true, Live: true}, {Index: route.producer, Armed: true, Live: true}, {Index: bitmapReplayParcelID, Armed: true, Live: true, indexRunOptions: indexRunOptions{ParcelEvidenceRoute: route.name}}}}
			if _, err := normalizeIndexRunRequest(req); err != nil {
				t.Fatalf("valid capped Live rejected: %v", err)
			}
			req.Out = nil
			if _, err := normalizeIndexRunRequest(req); err != nil {
				t.Fatalf("valid Live rejected: %v", err)
			}
			if route.producer == "txo-spender" {
				factsOnly := req
				factsOnly.Consumers = append([]indexRunSelection(nil), req.Consumers...)
				for i := range factsOnly.Consumers {
					if factsOnly.Consumers[i].Index == route.producer {
						factsOnly.Consumers[i].Armed = false
						factsOnly.Consumers[i].RetainOutputValues = boolPointer(true)
					}
				}
				if _, err := normalizeIndexRunRequest(factsOnly); err == nil {
					t.Fatal("output values alone cannot act as the armed IO Links proof producer")
				}
			}
			for _, id := range []string{bitmapStandardDistrictID, route.producer} {
				bad := req
				bad.Consumers = append([]indexRunSelection(nil), req.Consumers...)
				for i := range bad.Consumers {
					if bad.Consumers[i].Index == id {
						bad.Consumers[i].Live = false
					}
				}
				if _, err := normalizeIndexRunRequest(bad); err == nil {
					t.Fatalf("Live accepted without %s Live", id)
				}
				a := runTestApp(t)
				saved, err := a.prepareIndexRun(bad)
				if err != nil {
					t.Fatal(err)
				}
				selected, _ := runSelection(saved.Prepared, bitmapReplayParcelID)
				if selected.Armed || selected.Live {
					t.Fatal("invalid staged Live Parcel remained armed")
				}
			}
		})
	}
	d, ok := bitmapFixedIndexDefinition(bitmapReplayParcelID)
	if !ok || d.LiveSupported == nil || !*d.LiveSupported {
		t.Fatal("catalog does not expose Live")
	}
}

func TestParcelTXIOLiveReusesAuthenticatedPrefix079(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	a.settings.NetworkDisabled = true
	genesis, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "genesis fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openIndexStore(a.dataDir, "txo-spender")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.appendBlock(genesis, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	b1 := txioTestBlock(t, a, 1, genesisHashDisplay, 1)
	if err = store.appendBlock(b1, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	blocks := map[int64]blockView{0: genesis, 1: b1}
	selected := func(h int64) (string, error) {
		b, ok := blocks[h]
		if !ok {
			return "", fmt.Errorf("missing selected %d", h)
		}
		return b.Hash, nil
	}
	var reads []int64
	read := func(ctx context.Context, h int64) (blockView, error) {
		if err := ctx.Err(); err != nil {
			return blockView{}, err
		}
		reads = append(reads, h)
		b, ok := blocks[h]
		if !ok {
			return blockView{}, fmt.Errorf("missing block")
		}
		return b, nil
	}
	audit, err := auditParcelTXIO079(context.Background(), store, 1, selected, nil, read)
	if err != nil || !audit.Complete {
		t.Fatalf("initial audit: %+v %v", audit, err)
	}
	b2 := txioTestBlock(t, a, 2, b1.Hash, 2)
	blocks[2] = b2
	if err = store.appendBlock(b2, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	reads = nil
	audit, err = auditParcelTXIO079(context.Background(), store, 2, selected, nil, read)
	if err != nil || !audit.Complete {
		t.Fatalf("extended audit: %+v %v", audit, err)
	}
	if len(reads) != 1 || reads[0] != 2 {
		t.Fatalf("unchanged trusted history was re-audited: %v", reads)
	}
	b3 := txioTestBlock(t, a, 3, b2.Hash, 3)
	blocks[3] = b3
	if err = store.appendBlock(b3, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	changed := func(h int64) (string, error) {
		if h == 2 {
			return testID(998), nil
		}
		return selected(h)
	}
	if _, err = auditParcelTXIO079(context.Background(), store, 3, changed, nil, read); err == nil {
		t.Fatal("reorganized ancestor audit accepted")
	}
	// An authenticated-prefix mismatch must fail closed and cannot advance.
	p := filepath.Join(a.dataDir, "indexes", "parcel-txio-audits-v2", audit.TargetCommit+".json")
	if err = os.WriteFile(p, []byte(strings.Repeat("x", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = auditParcelTXIO079(context.Background(), store, 3, selected, nil, read); err == nil {
		t.Fatal("tampered ancestor audit accepted")
	}
}
