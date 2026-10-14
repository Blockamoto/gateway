package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
)

func Test077SatPointGenuineSavedOutputsAndPrivateReadOnlyHTTP(t *testing.T) {
	a, s, b0, b1 := satRuntime077Real(t)
	ctx := context.Background()
	if err := os.Remove(filepath.Join(a.dataDir, "blocks", "raw", "0-"+b0.Hash+".block")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		output, hash string
		offset, want uint64
	}{
		{"0.0.0", b0.Hash, 0, 0},
		{"0.0.0", b0.Hash, subsidyAtHeight(0) - 1, subsidyAtHeight(0) - 1},
		{"0.0.1", b1.Hash, 0, firstSatAtHeight(1)},
		{"0.0.1", b1.Hash, subsidyAtHeight(1) - 1, firstSatAtHeight(2) - 1},
	} {
		view, cp, err := querySatPointContext077(ctx, a.dataDir, tc.output, tc.hash, tc.offset)
		if err != nil || view.State != "ready" || view.Number == nil || *view.Number != tc.want || cp == nil || view.Snapshot.Commitment != s.checkpoint.Commitment || view.Snapshot.BlockHash != b1.Hash || view.Snapshot.ConsensusValidated {
			t.Fatal(tc, view, cp, err)
		}
		w := httptest.NewRecorder()
		path := "/api/v1/sats/point?output=" + tc.output + "&block_hash=" + tc.hash + "&offset=" + strconv.FormatUint(tc.offset, 10)
		a.handleSatPoint077(w, satlineReleaseRequest077("GET", path, ""))
		var got satPointView077
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Number == nil || *got.Number != tc.want || got.State != "ready" || got.Snapshot.Commitment != cp.Commitment {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct {
		output, hash string
		offset       uint64
	}{
		{"0.0.1", testID(77950), 0}, {"0.0.1", b1.Hash, subsidyAtHeight(1)}, {"0.0.0", b0.Hash, subsidyAtHeight(0)}, {"invalid", b1.Hash, 0},
	} {
		view, _, err := querySatPointContext077(ctx, a.dataDir, tc.output, tc.hash, tc.offset)
		if err == nil || view.Number != nil || view.State == "ready" {
			t.Fatal("invalid point fabricated identity", tc, view, err)
		}
	}
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		w := httptest.NewRecorder()
		a.handleSatPoint077(w, satlineReleaseRequest077(method, "/api/v1/sats/point?output=0.0.1&block_hash="+b1.Hash+"&offset=0", ""))
		if w.Code != 405 {
			t.Fatal(method, w.Code)
		}
	}
	remote := httptest.NewRecorder()
	a.handleSatPoint077(remote, httptest.NewRequest("GET", "http://untrusted.invalid/api/v1/sats/point?output=0.0.1&block_hash="+b1.Hash+"&offset=0", nil))
	if remote.Code != 403 {
		t.Fatal("remote point lookup accepted", remote.Code)
	}
	for _, offset := range []string{"-1", "00", "18446744073709551616", "1.5", ""} {
		w := httptest.NewRecorder()
		a.handleSatPoint077(w, satlineReleaseRequest077("GET", "/api/v1/sats/point?output=0.0.1&block_hash="+b1.Hash+"&offset="+offset, ""))
		if w.Code != 400 {
			t.Fatal(offset, w.Code)
		}
	}
	for _, rel := range []string{"indexes/job.json", "indexes/queue.json", "satline/metadata.json", "satline/reverse-v1"} {
		if _, err := os.Stat(filepath.Join(a.dataDir, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatal("point read created work", rel, err)
		}
	}
}

func Test077SatPointMissingHistoryNeverStartsReconstruction(t *testing.T) {
	for _, retain := range []bool{false, true} {
		s, blocks := sat077Fixture(t, retain)
		view, cp, err := querySatPointContext077(context.Background(), s.profileRoot, "0.0.1", blocks[1].Hash, 3)
		if err != nil || cp == nil {
			t.Fatal(view, err)
		}
		if retain {
			if view.State != "ready" || view.Number == nil || *view.Number != firstSatAtHeight(1)+3 {
				t.Fatal(view)
			}
		} else if view.State != "unavailable" || view.Number != nil {
			t.Fatal("missing historical ranges became known", view)
		}
		// Sat builds already retain compact fee receipts under satline/fees;
		// those are distinct from newly initiated traces and job metadata.
		for _, rel := range []string{"satline/reverse-v1", "satline/jobs", "satline/metadata.json", "indexes/job.json", "indexes/queue.json"} {
			if _, err := os.Stat(filepath.Join(s.profileRoot, filepath.FromSlash(rel))); !os.IsNotExist(err) {
				t.Fatal("point lookup started reconstruction", rel, err)
			}
		}
	}
	root := t.TempDir()
	view, cp, err := querySatPointContext077(context.Background(), root, "0.0.1", testID(77951), 0)
	if err != nil || cp != nil || view.State != "not_built" || view.Number != nil {
		t.Fatal(view, cp, err)
	}
}

func Test077SatPointStaleSelectedCheckpointWithholdsSatIdentity(t *testing.T) {
	a, _, _, b1 := satRuntime077Real(t)
	settings, rawCalls, stop := satline077RPC(t, map[int64]string{0: genesisHashDisplay, 1: testID(77952)})
	defer stop()
	a.settings = settings
	w := httptest.NewRecorder()
	a.handleSatPoint077(w, satlineReleaseRequest077("GET", "/api/v1/sats/point?output=0.0.1&block_hash="+b1.Hash+"&offset=123", ""))
	var got satPointView077
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.State != "reconciling" || got.Number != nil || got.Snapshot == nil || got.Snapshot.BlockHash != b1.Hash || rawCalls.Load() != 0 {
		t.Fatal(w.Code, w.Body.String(), rawCalls.Load())
	}
}

func satPointFragments077(t *testing.T, count int) (string, satIndexOutput, []uint64) {
	t.Helper()
	dir := t.TempDir()
	tree := newOrdinalTree(dir, "")
	ranges := make([]satRange, count)
	values := []uint64{}
	for i := range ranges {
		// FIFO order deliberately differs from numeric Sat order.
		start, size := uint64((count-i)*100), uint64(i%5+1)
		ranges[i] = satRange{Start: start, End: start + size}
		for n := uint64(0); n < size; n++ {
			values = append(values, start+n)
		}
	}
	offset := uint64(0)
	for i := 0; i < len(ranges); i += 256 {
		end := i + 256
		if end > len(ranges) {
			end = len(ranges)
		}
		if err := tree.put(satRangeKey(uint64(i)), satRangeSegment077{Offset: offset, Ranges: ranges[i:end]}); err != nil {
			t.Fatal(err)
		}
		for _, r := range ranges[i:end] {
			offset += r.End - r.Start
		}
	}
	root, err := tree.flush()
	if err != nil {
		t.Fatal(err)
	}
	return dir, satIndexOutput{Output: "0.1.2", RangesRoot: root, RangeCount: uint64(count), Value: offset}, values
}

func Test077SatPointFragmentBinarySeekUsesFIFOOffsetsAcrossPageBoundaries(t *testing.T) {
	dir, output, values := satPointFragments077(t, 769)
	for offset, want := range values {
		got, err := satNumberAtOffset077(context.Background(), dir, output, uint64(offset))
		if err != nil || got != want {
			t.Fatalf("offset%d: got%d want%d: %v", offset, got, want, err)
		}
	}
	if _, err := satNumberAtOffset077(context.Background(), dir, output, output.Value); err == nil {
		t.Fatal("output end boundary accepted")
	}
	inline := satIndexOutput{Value: 6, RangeCount: 2, Ranges: []satRange{{Start: 900, End: 902}, {Start: 100, End: 104}}}
	for _, tc := range []struct{ offset, want uint64 }{{0, 900}, {1, 901}, {2, 100}, {5, 103}} {
		got, err := satNumberAtOffset077(context.Background(), "", inline, tc.offset)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
}

type satPointCancelContext077 struct {
	context.Context
	cancel context.CancelFunc
	calls  atomic.Int32
	at     int32
}

func (c *satPointCancelContext077) Err() error {
	if c.calls.Add(1) == c.at {
		c.cancel()
	}
	return c.Context.Err()
}

func Test077SatPointCorruptionAndCancellationFailClosed(t *testing.T) {
	for _, mode := range []string{"missing_page", "corrupt_page", "missing_segment", "short_segment", "wrong_first_offset", "gap", "trailing_invalid_range", "wrong_total"} {
		t.Run(mode, func(t *testing.T) {
			dir, output, _ := satPointFragments077(t, 769)
			tree := newOrdinalTree(dir, output.RangesRoot)
			offset := uint64(0)
			switch mode {
			case "missing_page":
				if err := os.Remove(tree.pagePath(output.RangesRoot)); err != nil {
					t.Fatal(err)
				}
			case "corrupt_page":
				if err := os.WriteFile(tree.pagePath(output.RangesRoot), []byte("damaged"), 0600); err != nil {
					t.Fatal(err)
				}
			case "wrong_total":
				output.Value++
				offset = output.Value - 2
			default:
				var first satRangeSegment077
				if _, err := tree.get(satRangeKey(0), &first); err != nil {
					t.Fatal(err)
				}
				key := satRangeKey(0)
				switch mode {
				case "missing_segment":
					tree.remove(key)
				case "short_segment":
					first.Ranges = first.Ranges[:255]
				case "wrong_first_offset":
					first.Offset = 1
				case "gap":
					key = satRangeKey(256)
					if _, err := tree.get(key, &first); err != nil {
						t.Fatal(err)
					}
					first.Offset++
				case "trailing_invalid_range":
					first.Ranges[len(first.Ranges)-1].End = first.Ranges[len(first.Ranges)-1].Start
				}
				if mode != "missing_segment" {
					if err := tree.put(key, first); err != nil {
						t.Fatal(err)
					}
				}
				var err error
				output.RangesRoot, err = tree.flush()
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := satNumberAtOffset077(context.Background(), dir, output, offset); err == nil {
				t.Fatal("damaged output returned Sat identity")
			}
		})
	}
	dir, output, _ := satPointFragments077(t, 769)
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &satPointCancelContext077{Context: base, cancel: cancel, at: 3}
	if _, err := satNumberAtOffset077(ctx, dir, output, 5); !errors.Is(err, context.Canceled) || errors.Is(err, ErrSatStateCorrupt) {
		t.Fatal("cancellation misclassified as corruption", err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if _, err := satNumberAtOffset077(ctx2, "", satIndexOutput{Value: 1, RangeCount: 1, Ranges: []satRange{{Start: 1, End: 2}}}, 0); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, o := range []satIndexOutput{{Value: 2, RangeCount: 1, Ranges: []satRange{{Start: 1, End: 2}}}, {Value: 2, RangeCount: 2, Ranges: []satRange{{Start: 1, End: 2}, {Start: 3, End: 3}}}, {Value: 1, RangeCount: 1, Ranges: []satRange{{Start: theoreticalSatSupply(), End: theoreticalSatSupply() + 1}}}} {
		if _, err := satNumberAtOffset077(context.Background(), "", o, 0); !errors.Is(err, ErrSatStateCorrupt) {
			t.Fatal("malformed inline ranges accepted", o, err)
		}
	}
}
