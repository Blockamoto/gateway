package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/trace"
	"sync"
	"testing"
	"time"
)

// Opt-in real-source diagnostic. All writers and runtime state belong to
// t.TempDir; the supplied fixture is read-only. Use -trace to attribute actual
// file Sync/Rename calls to the native flush and future-point publication stacks.
// No application profile, network peer, Core connection or retained Raw producer
// is used. This is a local replay, not a network-throughput measurement.
func TestSharedBlockModernSkewDiagnostic(t *testing.T) {
	fixture := os.Getenv("GATEWAY_MODERN_POINT_FIXTURE")
	if fixture == "" {
		t.Skip("set GATEWAY_MODERN_POINT_FIXTURE to an authenticated32-block fixture for the TempDir-only trace diagnostic")
	}
	var manifest struct {
		Network   string `json:"network"`
		HeaderSHA string `json:"header_sha256"`
		Blocks    []struct {
			Height       int64  `json:"height"`
			Hash         string `json:"block_hash"`
			File         string `json:"filename"`
			Bytes        int    `json:"bytes"`
			SHA          string `json:"sha256"`
			Transactions uint64 `json:"transactions"`
		} `json:"blocks"`
	}
	raw, err := os.ReadFile(filepath.Join(fixture, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Network != "bitcoin-mainnet" || len(manifest.Blocks) < 32 || manifest.Blocks[0].Height != bitmapDevelopmentStart {
		t.Fatal("expected mainnet Standard-origin32-block fixture")
	}
	manifest.Blocks = manifest.Blocks[:32]
	for i, b := range manifest.Blocks {
		if b.Height != bitmapDevelopmentStart+int64(i) || !validHash(b.Hash) || !validHash(b.SHA) || filepath.Base(b.File) != b.File || b.Bytes < 81 || b.Bytes > maxMessageSize || b.Transactions == 0 {
			t.Fatal("invalid fixture row", i, b)
		}
	}
	a := runTestApp(t)
	headerSource, err := os.Open(filepath.Join(fixture, "headers.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer headerSource.Close()
	headers, err := os.OpenFile(a.headersPath, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.New()
	headerBytes, err := io.Copy(io.MultiWriter(headers, digest), headerSource)
	closeErr := headers.Close()
	if err != nil || closeErr != nil || headerBytes%80 != 0 || headerBytes/80 <= manifest.Blocks[31].Height || hex.EncodeToString(digest.Sum(nil)) != manifest.HeaderSHA {
		t.Fatal("fixture header digest/extent mismatch", err, closeErr)
	}
	lastHeader := make([]byte, 80)
	if _, err = headerSource.ReadAt(lastHeader, headerBytes-80); err != nil {
		t.Fatal(err)
	}
	tipHash := hash256(lastHeader)
	a.setStatus(func(s *appStatus) {
		s.HeaderHeight = headerBytes/80 - 1
		s.HeaderCount = headerBytes / 80
		s.TipHash = reverseHex(tipHash[:])
		s.Ready = true
		s.Syncing = false
	})
	var mu sync.Mutex
	calls := map[int64]int{}
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, _ string) (blockView, error) {
		if err := ctx.Err(); err != nil {
			return blockView{}, err
		}
		index := target.Height - bitmapDevelopmentStart
		if index < 0 || index >= int64(len(manifest.Blocks)) {
			return blockView{}, fmt.Errorf("unexpected source %d", target.Height)
		}
		row := manifest.Blocks[index]
		payload, err := os.ReadFile(filepath.Join(fixture, row.File))
		if err != nil {
			return blockView{}, err
		}
		sum := sha256.Sum256(payload)
		if len(payload) != row.Bytes || hex.EncodeToString(sum[:]) != row.SHA || target.HashDisplay != row.Hash {
			return blockView{}, fmt.Errorf("fixture raw/source identity mismatch at %d", target.Height)
		}
		block, err := parseBlockDetailed(target.Height, row.Hash, target.ExpectedHeader, payload, "authenticated modern skew fixture", false)
		if err != nil {
			return blockView{}, err
		}
		if block.TransactionCount != row.Transactions {
			return blockView{}, fmt.Errorf("fixture transaction denominator mismatch")
		}
		mu.Lock()
		calls[target.Height]++
		mu.Unlock()
		// Preserve the normal source hook. Coordinator ownership defers these
		// facts until the actual per-body consumer plan has been constructed.
		a.ingestResolvedVerifiedBlock(ctx, block)
		return block, nil
	}
	t.Cleanup(func() {
		a.indexMu.Lock()
		cancel := a.indexCancel
		a.indexMu.Unlock()
		if cancel != nil {
			cancel()
		}
	})
	wait := func() indexRunDocument {
		deadline := time.Now().Add(4 * time.Minute)
		for time.Now().Before(deadline) {
			d := a.indexRunSnapshot()
			a.indexMu.Lock()
			active := a.indexCancel != nil
			a.indexMu.Unlock()
			if d.Run != nil && !active {
				if d.Run.State != "complete" {
					t.Fatal("diagnostic run did not complete", d.Run)
				}
				return d
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("modern skew diagnostic exceeded its4-minute owned-run deadline")
		return indexRunDocument{}
	}
	first, seedEnd, end := manifest.Blocks[0].Height, manifest.Blocks[15].Height, manifest.Blocks[31].Height
	seed := indexRunRequest{In: &first, Out: &seedEnd, Consumers: []indexRunSelection{{Index: bitmapStandardDistrictID, Armed: true}, {Index: bitmapStandardParcelID, Armed: true}}}
	region := trace.StartRegion(context.Background(), "SharedFanout.Modern32.PreseedBitmap16")
	if _, err = a.startIndexRun(context.Background(), seed, ""); err != nil {
		region.End()
		t.Fatal(err)
	}
	seedDone := wait()
	region.End()
	mu.Lock()
	calls = map[int64]int{}
	mu.Unlock()
	request := indexRunRequest{In: &first, Out: &end, Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}, {Index: bitmapStandardDistrictID, Armed: true}, {Index: bitmapStandardParcelID, Armed: true}}}
	region = trace.StartRegion(context.Background(), "SharedFanout.Modern32.SkewedRun")
	started := time.Now()
	if _, err = a.startIndexRun(context.Background(), request, ""); err != nil {
		region.End()
		t.Fatal(err)
	}
	done := wait()
	elapsed := time.Since(started)
	region.End()
	if done.Run.Counts.SourceReads != 32 || done.Run.Counts.DecodedBlocks != 32 || done.Run.Counts.InscriptionExtractions != 32 {
		t.Fatal("modern shared facts were duplicated", done.Run.Counts)
	}
	mu.Lock()
	for _, b := range manifest.Blocks {
		if calls[b.Height] != 1 {
			t.Fatal("fixture body acquired more/less than once", b.Height, calls)
		}
	}
	mu.Unlock()
	points, pointRows, pointBytes := 0, 0, int64(0)
	var pointSources []map[string]any
	if err = walkBrowseTXPoints(context.Background(), a.dataDir, func(point indexBrowseTXPoint) error {
		if point.Height < manifest.Blocks[16].Height || point.Height > end {
			return fmt.Errorf("native cohort also acquired a point owner at %d", point.Height)
		}
		b, have, err := a.browsedBatchContext(context.Background(), "tx-locator", point.Height, point.BlockHash)
		if err != nil {
			return err
		}
		if !have {
			return fmt.Errorf("point membership has no complete saved batch")
		}
		entries := transactionBatchEntries(b)
		if len(entries) != int(manifest.Blocks[point.Height-first].Transactions) {
			return fmt.Errorf("future point lost ordered transaction rows")
		}
		encoded, err := json.Marshal(b)
		if err != nil {
			return err
		}
		points++
		pointRows += len(entries)
		pointBytes += int64(len(encoded))
		pointSources = append(pointSources, map[string]any{"height": point.Height, "commitment": point.Commitment, "rows": len(entries)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if points != 16 {
		t.Fatal("future complete points were lost or duplicated", points)
	}
	heads := map[string]any{}
	for _, id := range []string{bitmapStandardDistrictID, bitmapStandardParcelID} {
		h, err := readBitmapDevelopmentHead(context.Background(), a.dataDir, id)
		if err != nil || h == nil || h.Height != end {
			t.Fatal(id, h, err)
		}
		heads[id] = map[string]any{"height": h.Height, "commitment": h.Commitment, "winner_root": h.WinnerRoot, "candidate_root": h.CandidateRoot, "target_root": h.TargetRoot, "block_root": h.BlockRoot}
	}
	tx, err := indexStoreHead(a.dataDir, "tx-locator")
	if err != nil || tx.checkpoint == nil || tx.checkpoint.Height != end || tx.checkpoint.From != first {
		t.Fatal("native TX prefix incomplete", tx, err)
	}
	heads["tx-locator"] = tx.checkpoint
	if rawStore, err := indexStoreHead(a.dataDir, "blocks"); err != nil || rawStore.checkpoint != nil {
		t.Fatal("Raw Off created native coverage", rawStore, err)
	}
	encoded, err := json.Marshal(map[string]any{"scope": "authentic32-block local replay; TempDir-only; no network throughput claim", "elapsed_ns": elapsed.Nanoseconds(), "preseed_counts": seedDone.Run.Counts, "counts": done.Run.Counts, "future_points": points, "future_point_rows": pointRows, "future_point_json_bytes": pointBytes, "point_sources": pointSources, "heads": heads})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("SHARED_FANOUT_MODERN_SKEW", string(encoded))
}
