package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime/pprof"
	"runtime/trace"
	"testing"
	"time"
)

type bitmapPerformanceRecord struct {
	Height           int64  `json:"height"`
	Claims           int    `json:"claims"`
	ApplyNanoseconds int64  `json:"apply_nanoseconds"`
	Candidates       uint64 `json:"candidates"`
	Winners          uint64 `json:"winners"`
	PageFiles        int    `json:"page_files"`
	PageBytes        int64  `json:"page_bytes"`
	NewPageFiles     int    `json:"new_page_files"`
	NewPageBytes     int64  `json:"new_page_bytes"`
	Commitment       string `json:"commitment"`
	WinnerRoot       string `json:"winner_root"`
	CandidateRoot    string `json:"candidate_root"`
	TargetRoot       string `json:"target_root"`
	BlockRoot        string `json:"block_root"`
}

// This isolates the real Standard Apply and durable page writer in TempDir.
// Its synthetic, structurally verified blocks are not Bitcoin throughput or
// independently authenticated mainnet history. No application worker runs.
//
// Example diagnostic invocation (choose fresh ignored output filenames):
// go test -run '^TestBitmapPerformanceDiagnostic$' -count=1 -v -trace <trace> -cpuprofile <cpu>
func TestBitmapPerformanceDiagnostic(t *testing.T) {
	const claimsPerBlock = 1000
	root, ctx := t.TempDir(), context.Background()
	adapter, err := openBitmapRunConsumer(ctx, &app{dataDir: root}, bitmapStandardDistrictID)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	consumer := adapter.(*bitmapRunConsumer)
	selected := map[int64]string{}
	if err = consumer.Reconcile(ctx, func(height int64) (string, error) {
		hash, ok := selected[height]
		if !ok {
			return "", fmt.Errorf("diagnostic selected anchor unavailable")
		}
		return hash, nil
	}); err != nil {
		t.Fatal(err)
	}
	previous := ""
	pageFiles, pageBytes := 0, int64(0)
	t.Log("STRUCTURAL DIAGNOSTIC: two TempDir-only Standard Apply blocks; no network, real-chain throughput or global canonical-history claim")
	for blockNumber := 0; blockNumber < 2; blockNumber++ {
		claims := make([]string, claimsPerBlock)
		for i := range claims {
			district := i
			if blockNumber == 1 && i >= claimsPerBlock/2 {
				district = i + claimsPerBlock/2
			}
			claims[i] = fmt.Sprintf("%d.bitmap", district)
		}
		block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart+int64(blockNumber), previous, claims...)
		selected[block.Height] = block.Hash
		started := time.Now()
		pprof.Do(ctx, pprof.Labels("bitmap_phase", "apply", "fixture_block", fmt.Sprint(blockNumber)), func(ctx context.Context) {
			region := trace.StartRegion(ctx, fmt.Sprintf("Bitmap.Apply.%d", block.Height))
			err = consumer.Apply(ctx, block, nil)
			region.End()
		})
		elapsed := time.Since(started)
		if err != nil {
			t.Fatal(err)
		}
		head, err := readBitmapDevelopmentHead(ctx, root, bitmapStandardDistrictID)
		expectedWinners := uint64(claimsPerBlock + blockNumber*claimsPerBlock/2)
		if err != nil || head == nil || head.CandidateCount != uint64((blockNumber+1)*claimsPerBlock) || head.WinnerCount != expectedWinners {
			t.Fatal("diagnostic normal writer result", head, err)
		}
		files, bytes := bitmapPerformancePageCensus(t, filepath.Join(consumer.dir, "pages"))
		record := bitmapPerformanceRecord{Height: block.Height, Claims: len(claims), ApplyNanoseconds: elapsed.Nanoseconds(), Candidates: head.CandidateCount, Winners: head.WinnerCount, PageFiles: files, PageBytes: bytes, NewPageFiles: files - pageFiles, NewPageBytes: bytes - pageBytes, Commitment: head.Commitment, WinnerRoot: head.WinnerRoot, CandidateRoot: head.CandidateRoot, TargetRoot: head.TargetRoot, BlockRoot: head.BlockRoot}
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		t.Log("BITMAP_APPLY_DIAGNOSTIC", string(encoded))
		pageFiles, pageBytes, previous = files, bytes, block.Hash
	}
}

func bitmapPerformancePageCensus(t *testing.T, dir string) (int, int64) {
	t.Helper()
	files, entries := 0, 0
	var bytes int64
	err := filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		entries++
		if entries > 20000 {
			return fmt.Errorf("diagnostic page census exceeded its fixture bound")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unexpected diagnostic page entry")
		}
		files++
		bytes += info.Size()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files, bytes
}

var bitmapPerformanceMarshalSink []byte

// Each benchmark operation serializes 1000 representative native District
// candidates through the existing custom Marshaler, without disk I/O.
func BenchmarkBitmapCandidateMarshal1000(b *testing.B) {
	candidates := make([]bitmapDevelopmentCandidate, 1000)
	for i := range candidates {
		district := uint64(i)
		candidates[i] = bitmapDevelopmentCandidate{ID: testID(uint64(i)) + "i0", Blocator: inscriptionCoordinate(uint32(i+1), 0, bitmapDevelopmentStart), Kind: "district", District: &district, RevealCoordinate: inscriptionCoordinate(uint32(i+1), 0, bitmapDevelopmentStart), RevealHeight: bitmapDevelopmentStart, RevealHash: testID(99999), TxIndex: i + 1, ContentHash: testID(uint64(i + 10000)), Content: fmt.Sprintf("%d.bitmap", i), Eligibility: "eligible", Selection: "selected", Reason: "first_qualifying_canonical_reveal", Ownership: "unknown", DecodeState: "pass"}
	}
	encoded, err := json.Marshal(candidates[0])
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(encoded) * len(candidates)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, candidate := range candidates {
			encoded, err = json.Marshal(candidate)
			if err != nil {
				b.Fatal(err)
			}
			bitmapPerformanceMarshalSink = encoded
		}
	}
}
