package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func assertHeaderRollbackFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("header file changed: got %d bytes, want %d", len(got), len(want))
	}
}

func assertHeaderRollbackMarker(t *testing.T, path string, pending bool) {
	t.Helper()
	_, err := os.Stat(headerAppendMarkerPath051(path))
	if pending && err != nil || !pending && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending marker=%v: %v", pending, err)
	}
}

func TestHeaderRollbackRepairRejectThenAppendAndRestart(t *testing.T) {
	a := independentTestApp(t)
	header := mainnetBlockOne(t)[:80]
	before, err := os.ReadFile(a.headersPath)
	if err != nil {
		t.Fatal(err)
	}
	status := a.getStatus()
	count, err := a.appendCheckedHeaders(context.Background(), a.headersPath, 1, [][]byte{header, header})
	if err == nil || count != 1 {
		t.Fatalf("invalid batch: count=%d error=%v", count, err)
	}
	assertHeaderRollbackFile(t, a.headersPath, before)
	assertHeaderRollbackMarker(t, a.headersPath, false)
	if got := a.getStatus(); got.HeaderCount != status.HeaderCount || got.HeaderHeight != status.HeaderHeight || got.TipHash != status.TipHash {
		t.Fatal("rejected batch changed selected header status")
	}
	a.primeHeaderStatus()
	if got := a.getStatus(); got.HeaderCount != 1 || got.HeaderHeight != 0 || got.TipHash != genesisHashDisplay {
		t.Fatalf("reloaded rejected batch changed selected tip: %+v", got)
	}
	count, err = a.appendCheckedHeaders(context.Background(), a.headersPath, 1, [][]byte{header})
	if err != nil || count != 2 {
		t.Fatalf("valid retry: count=%d error=%v", count, err)
	}
	assertHeaderRollbackFile(t, a.headersPath, append(append([]byte(nil), before...), header...))
	assertHeaderRollbackMarker(t, a.headersPath, false)
	a.primeHeaderStatus()
	tip := hash256(header)
	if got := a.getStatus(); got.HeaderCount != 2 || got.HeaderHeight != 1 || got.TipHash != reverseHex(tip[:]) {
		t.Fatalf("valid committed batch not reloadable: %+v", got)
	}
}

func TestHeaderRollbackRepairRejectsStaleCountWithoutTruncation(t *testing.T) {
	a := independentTestApp(t)
	header := mainnetBlockOne(t)[:80]
	if _, err := a.appendCheckedHeaders(context.Background(), a.headersPath, 1, [][]byte{header}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(a.headersPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range []int64{-1, 0, 1, 3, 1 << 62} {
		if _, err := a.appendCheckedHeaders(context.Background(), a.headersPath, count, [][]byte{header}); err == nil {
			t.Fatalf("accepted mismatched/invalid starting count %d", count)
		}
		assertHeaderRollbackFile(t, a.headersPath, before)
	}
}

func TestHeaderRollbackRepairCandidateDoesNotChangeSelectedChain(t *testing.T) {
	a := independentTestApp(t)
	before, err := os.ReadFile(a.headersPath)
	if err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(t.TempDir(), "headers.candidate")
	if err := os.WriteFile(candidate, before, 0600); err != nil {
		t.Fatal(err)
	}
	header := mainnetBlockOne(t)[:80]
	if count, err := a.appendCheckedHeaders(context.Background(), candidate, 1, [][]byte{header, header}); err == nil || count != 1 {
		t.Fatalf("candidate rejection: count=%d error=%v", count, err)
	}
	assertHeaderRollbackFile(t, candidate, before)
	assertHeaderRollbackFile(t, a.headersPath, before)
	if count, err := a.appendCheckedHeaders(context.Background(), candidate, 1, [][]byte{header}); err != nil || count != 2 {
		t.Fatalf("candidate append: count=%d error=%v", count, err)
	}
	assertHeaderRollbackFile(t, a.headersPath, before)
}

type headerRollbackFaultFile struct {
	*os.File
	write    func([]byte) (int, error)
	truncate func(int64) error
	sync     func() error
}

func (f headerRollbackFaultFile) Write(b []byte) (int, error) {
	if f.write != nil {
		return f.write(b)
	}
	return f.File.Write(b)
}

func (f headerRollbackFaultFile) Truncate(size int64) error {
	if f.truncate != nil {
		return f.truncate(size)
	}
	return f.File.Truncate(size)
}

func (f headerRollbackFaultFile) Sync() error {
	if f.sync != nil {
		return f.sync()
	}
	return f.File.Sync()
}

func TestHeaderRollbackRepairIOAndCancellation(t *testing.T) {
	writeFailure := errors.New("injected header write failure")
	syncFailure := errors.New("injected header sync failure")
	for _, kind := range []string{"partial_write", "short_write", "cancel_after_write", "commit_sync"} {
		t.Run(kind, func(t *testing.T) {
			a := independentTestApp(t)
			header := mainnetBlockOne(t)[:80]
			before, err := os.ReadFile(a.headersPath)
			if err != nil {
				t.Fatal(err)
			}
			file, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fault := headerRollbackFaultFile{File: file}
			want := writeFailure
			syncCalls := 0
			fault.sync = func() error {
				syncCalls++
				if kind == "commit_sync" && syncCalls == 1 {
					return syncFailure
				}
				return file.Sync()
			}
			switch kind {
			case "partial_write", "short_write":
				fault.write = func(b []byte) (int, error) {
					n, err := file.Write(b[:17])
					if err != nil || kind == "short_write" {
						return n, err
					}
					return n, writeFailure
				}
				if kind == "short_write" {
					want = io.ErrShortWrite
				}
			case "cancel_after_write":
				want = context.Canceled
				fault.write = func(b []byte) (int, error) {
					n, err := file.Write(b)
					cancel()
					return n, err
				}
			case "commit_sync":
				want = syncFailure
			}
			count, err := appendCheckedHeaderBatch051(ctx, a.headersPath, 1, [][]byte{header}, fault)
			if count != 1 || !errors.Is(err, want) {
				t.Fatalf("count=%d error=%v; want %v", count, err, want)
			}
			assertHeaderRollbackFile(t, a.headersPath, before)
			assertHeaderRollbackMarker(t, a.headersPath, false)
			if syncCalls == 0 || kind == "commit_sync" && syncCalls != 2 {
				t.Fatalf("rollback was not synced: %d sync calls", syncCalls)
			}
		})
	}
}

func TestHeaderRollbackRepairReportsRollbackFailures(t *testing.T) {
	truncateFailure := errors.New("injected rollback truncate failure")
	syncFailure := errors.New("injected rollback sync failure")
	for _, kind := range []string{"truncate", "sync"} {
		t.Run(kind, func(t *testing.T) {
			a := independentTestApp(t)
			header := mainnetBlockOne(t)[:80]
			file, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			fault := headerRollbackFaultFile{File: file}
			want := truncateFailure
			if kind == "truncate" {
				fault.truncate = func(int64) error { return truncateFailure }
			} else {
				want = syncFailure
				fault.sync = func() error { return syncFailure }
			}
			count, err := appendCheckedHeaderBatch051(context.Background(), a.headersPath, 1, [][]byte{header, header}, fault)
			if count != 1 || !errors.Is(err, want) || !strings.Contains(err.Error(), "header does not link") {
				t.Fatalf("original or rollback failure lost: count=%d error=%v", count, err)
			}
			info, err := file.Stat()
			if err != nil {
				t.Fatal(err)
			}
			if kind == "truncate" && info.Size() != 160 || kind == "sync" && info.Size() != 80 {
				t.Fatalf("fault fixture did not exercise rollback outcome: %d bytes", info.Size())
			}
			assertHeaderRollbackMarker(t, a.headersPath, true)
			fresh := &app{headersPath: a.headersPath}
			fresh.primeHeaderStatus()
			if st := fresh.getStatus(); st.HeaderCount != 1 || st.HeaderHeight != 0 || st.TipHash != genesisHashDisplay || !st.Ready {
				t.Fatalf("restart adopted an uncommitted suffix: %+v", st)
			}
			assertHeaderRollbackMarker(t, a.headersPath, false)
			info, err = file.Stat()
			if err != nil || info.Size() != 80 {
				t.Fatalf("restart did not recover original boundary: info=%v error=%v", info, err)
			}
		})
	}
}

func TestHeaderRollbackRepairSelectedReadRecoversInterruptedAppend(t *testing.T) {
	for _, kind := range []string{"height", "hash"} {
		t.Run(kind, func(t *testing.T) {
			a := independentTestApp(t)
			before, err := os.ReadFile(a.headersPath)
			if err != nil {
				t.Fatal(err)
			}
			header := mainnetBlockOne(t)[:80]
			if err := beginHeaderAppend051(a.headersPath, 1); err != nil {
				t.Fatal(err)
			}
			// This is the on-disk state after a process stopped mid-batch. The
			// header is valid but the batch has not committed.
			if err := os.WriteFile(a.headersPath, append(append([]byte(nil), before...), header...), 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "height" {
				if _, err := a.readSelectedHeader(1); err == nil {
					t.Fatal("selected read exposed uncommitted header")
				}
			} else {
				digest := hash256(header)
				if _, _, err := a.findSelectedHeader(reverseHex(digest[:])); err == nil {
					t.Fatal("hash lookup exposed uncommitted header")
				}
			}
			assertHeaderRollbackFile(t, a.headersPath, before)
			assertHeaderRollbackMarker(t, a.headersPath, false)
			if h, err := a.readSelectedHeader(0); err != nil || !bytes.Equal(h, before) {
				t.Fatalf("original selected chain not readable after recovery: %v", err)
			}
			if count, err := a.appendCheckedHeaders(context.Background(), a.headersPath, 1, [][]byte{header}); err != nil || count != 2 {
				t.Fatalf("valid append after recovery: count=%d error=%v", count, err)
			}
		})
	}
}

func TestHeaderRollbackRepairFailedRecoveryRetainsBoundary(t *testing.T) {
	for _, kind := range []string{"truncate", "sync"} {
		t.Run(kind, func(t *testing.T) {
			a := independentTestApp(t)
			before, err := os.ReadFile(a.headersPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := beginHeaderAppend051(a.headersPath, 1); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(a.headersPath, append(append([]byte(nil), before...), mainnetBlockOne(t)[:80]...), 0600); err != nil {
				t.Fatal(err)
			}
			file, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			failure := errors.New("injected recovery " + kind + " failure")
			fault := headerRollbackFaultFile{File: file}
			if kind == "truncate" {
				fault.truncate = func(int64) error { return failure }
			} else {
				fault.sync = func() error { return failure }
			}
			if err := recoverHeaderAppendFile051(a.headersPath, fault); !errors.Is(err, failure) {
				t.Fatalf("recovery failure lost: %v", err)
			}
			assertHeaderRollbackMarker(t, a.headersPath, true)
			if count, err := ensureHeaderFile(a.headersPath); err != nil || count != 1 {
				t.Fatalf("repeated recovery: count=%d error=%v", count, err)
			}
			assertHeaderRollbackFile(t, a.headersPath, before)
			assertHeaderRollbackMarker(t, a.headersPath, false)
		})
	}
}

func TestHeaderRollbackRepairInvalidBoundaryFailsClosed(t *testing.T) {
	for _, kind := range []string{"partial_marker", "invalid_count", "wrong_tip", "missing_prefix"} {
		t.Run(kind, func(t *testing.T) {
			a := independentTestApp(t)
			before, err := os.ReadFile(a.headersPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := beginHeaderAppend051(a.headersPath, 1); err != nil {
				t.Fatal(err)
			}
			marker := headerAppendMarkerPath051(a.headersPath)
			record, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "partial_marker":
				record = record[:20]
			case "invalid_count":
				binary.LittleEndian.PutUint64(record[8:16], 1<<63)
			case "wrong_tip":
				record[16] ^= 1
			case "missing_prefix":
				binary.LittleEndian.PutUint64(record[8:16], 2)
			}
			if err := os.WriteFile(marker, record, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := a.readSelectedHeader(0); err == nil {
				t.Fatal("selected read accepted an unverifiable recovery boundary")
			}
			if _, _, err := a.findSelectedHeader(genesisHashDisplay); err == nil {
				t.Fatal("hash lookup accepted an unverifiable recovery boundary")
			}
			if _, err := a.appendCheckedHeaders(context.Background(), a.headersPath, 1, [][]byte{mainnetBlockOne(t)[:80]}); err == nil {
				t.Fatal("append bypassed an unverifiable recovery boundary")
			}
			a.primeHeaderStatus()
			if st := a.getStatus(); st.Ready || st.HeaderCount != 0 || st.HeaderHeight != -1 || st.Error == "" {
				t.Fatalf("restart trusted an unverifiable boundary: %+v", st)
			}
			assertHeaderRollbackFile(t, a.headersPath, before)
			assertHeaderRollbackMarker(t, a.headersPath, true)
		})
	}
}

func TestHeaderRollbackRepairCoreAnchoringRecoversBeforeReading(t *testing.T) {
	a := independentTestApp(t)
	before, err := os.ReadFile(a.headersPath)
	if err != nil {
		t.Fatal(err)
	}
	header := mainnetBlockOne(t)[:80]
	digest := hash256(header)
	hash := reverseHex(digest[:])
	a.coreStore.ByHash = map[string]coreBlockLocator{
		genesisHashDisplay: {Height: -1},
		hash:               {Height: -1},
	}
	if err := beginHeaderAppend051(a.headersPath, 1); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.headersPath, append(append([]byte(nil), before...), header...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.anchorCoreStoreToHeaders(); err != nil {
		t.Fatal(err)
	}
	if len(a.coreStore.ByHeight) != 1 || a.coreStore.ByHeight[0] != genesisHashDisplay || a.coreStore.ByHash[hash].Height != -1 {
		t.Fatalf("anchored an uncommitted header: %+v", a.coreStore.ByHeight)
	}
	assertHeaderRollbackFile(t, a.headersPath, before)
	assertHeaderRollbackMarker(t, a.headersPath, false)
}
