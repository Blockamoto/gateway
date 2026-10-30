package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRecipeRangeHeaderSnapshotMeasurement(t *testing.T) {
	if os.Getenv("GATEWAY_RECIPE_RANGE_MEASURE") != "1" {
		t.Skip("opt-in synthetic header-file snapshot I/O measurement")
	}
	// Repeated real block-one header bytes make an 80 MB I/O fixture. This is
	// not a valid chain and measures hashing only, not source/PoW validation.
	header := mainnetBlockOne(t)[:80]
	path := filepath.Join(t.TempDir(), "headers.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	const headers = 1_000_000
	chunk := bytes.Repeat(header, 4096)
	for remaining := headers; remaining > 0; {
		n := min(remaining, 4096)
		if _, err = f.Write(chunk[:n*80]); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		remaining -= n
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	a := &app{headersPath: path}
	for _, count := range []int64{10_000, 1_000_000} {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		for run := 0; run < 5; run++ {
			a.headerChainMu.RLock()
			_, err = a.recipeHeaderRangeHashLocked(context.Background(), 0, count-1)
			a.headerChainMu.RUnlock()
			if err != nil {
				t.Fatal(err)
			}
		}
		runtime.ReadMemStats(&after)
		t.Logf("synthetic warm header-file interval: headers=%d bytes=%d mean=%s allocated_bytes_per_read=%d", count, count*80, time.Since(start)/5, (after.TotalAlloc-before.TotalAlloc)/5)
	}
}
