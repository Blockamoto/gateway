package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type satPointView077 struct {
	State     string               `json:"state"`
	Output    string               `json:"output_coordinate"`
	BlockHash string               `json:"block_hash"`
	Offset    uint64               `json:"offset"`
	Number    *uint64              `json:"sat_number,omitempty"`
	Snapshot  *addressViewSnapshot `json:"snapshot"`
	Note      string               `json:"note"`
}

// Locate an output-relative offset without traversing earlier range pages.
// Range segment offsets are authenticated by the committed range tree.
func satNumberAtOffset077(ctx context.Context, dir string, output satIndexOutput, offset uint64) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if offset >= output.Value || output.RangeCount == 0 {
		return 0, fmt.Errorf("sat offset is outside the output")
	}
	ranges, start := output.Ranges, uint64(0)
	if output.RangesRoot != "" {
		if len(ranges) != 0 {
			return 0, ErrSatStateCorrupt
		}
		tree := newOrdinalTree(dir, output.RangesRoot).withContext(ctx)
		pages := (output.RangeCount-1)/256 + 1
		read := func(page uint64) (satRangeSegment077, error) {
			var segment satRangeSegment077
			have, err := tree.get(satRangeKey(page*256), &segment)
			if ctx.Err() != nil {
				return segment, ctx.Err()
			}
			want := output.RangeCount - page*256
			if want > 256 {
				want = 256
			}
			if err != nil || !have || uint64(len(segment.Ranges)) != want || segment.Offset >= output.Value || page == 0 && segment.Offset != 0 {
				return segment, fmt.Errorf("%w: range offset segment (%v)", ErrSatStateCorrupt, err)
			}
			return segment, nil
		}
		lo, hi := uint64(0), pages
		for lo < hi {
			mid := lo + (hi-lo)/2
			segment, err := read(mid)
			if err != nil {
				return 0, err
			}
			if segment.Offset <= offset {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		if lo == 0 {
			return 0, ErrSatStateCorrupt
		}
		segment, err := read(lo - 1)
		if err != nil {
			return 0, err
		}
		ranges, start = segment.Ranges, segment.Offset
		end, err := satRangesValueContext(ctx, ranges)
		if err != nil {
			return 0, err
		}
		if end > output.Value-start {
			return 0, ErrSatStateCorrupt
		}
		end += start
		if lo < pages {
			next, err := read(lo)
			if err != nil {
				return 0, err
			}
			if next.Offset != end {
				return 0, ErrSatStateCorrupt
			}
		} else if end != output.Value {
			return 0, ErrSatStateCorrupt
		}
	} else if output.RangeCount != uint64(len(ranges)) || len(ranges) > 256 {
		return 0, ErrSatStateCorrupt
	}
	var number uint64
	found := false
	for _, r := range ranges {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if r.Start >= r.End || r.End > theoreticalSatSupply() || r.End-r.Start > output.Value-start {
			return 0, ErrSatStateCorrupt
		}
		end := start + r.End - r.Start
		if offset >= start && offset < end {
			number, found = r.Start+offset-start, true
		}
		start = end
	}
	if output.RangesRoot == "" && start != output.Value {
		return 0, ErrSatStateCorrupt
	}
	if found {
		return number, nil
	}
	return 0, fmt.Errorf("%w: sat offset range gap", ErrSatStateCorrupt)
}

func querySatPointContext077(ctx context.Context, root, output, blockHash string, offset uint64) (satPointView077, *indexCheckpoint, error) {
	satStateMu.Lock()
	defer satStateMu.Unlock()
	out := satPointView077{State: "not_built", Output: output, BlockHash: blockHash, Offset: offset, Note: "Saved Sat ranges are unavailable. No history scan was started."}
	key, err := satOutputKey077(output)
	if err != nil || !validHash(blockHash) {
		return out, nil, fmt.Errorf("a valid output blocator position and block hash are required")
	}
	s, b, err := openSatQuery077(ctx, root)
	if err != nil || s.checkpoint == nil {
		return out, nil, err
	}
	out.Snapshot = satSnapshot077(&b.Checkpoint)
	e := newSatEditor077(ctx, s, *b.Sats)
	var o satIndexOutput
	have, err := e.outputs.get(key, &o)
	if err == nil && !have {
		have, err = e.history.get(key, &o)
	}
	if err != nil {
		return out, &b.Checkpoint, err
	}
	if !have {
		out.State = "unavailable"
		return out, &b.Checkpoint, nil
	}
	if o.Output != output {
		return out, &b.Checkpoint, ErrSatStateCorrupt
	}
	if err = e.hydrate(&o); err != nil {
		return out, &b.Checkpoint, err
	}
	if !strings.EqualFold(o.Point.BlockHash, blockHash) {
		return out, &b.Checkpoint, fmt.Errorf("the output belongs to different block evidence; refresh the timeline")
	}
	n, err := satNumberAtOffset077(ctx, s.ordinalPages(), o, offset)
	if err != nil {
		return out, &b.Checkpoint, err
	}
	out.State, out.Number, out.Note = "ready", &n, "Sat identity read from retained output ranges; no history scan was started."
	return out, &b.Checkpoint, nil
}

func (a *app) handleSatPoint077(w http.ResponseWriter, r *http.Request) {
	if !satHTTPRead077(w, r) {
		return
	}
	q := r.URL.Query()
	offset, err := strconv.ParseUint(q.Get("offset"), 10, 64)
	if err != nil || strconv.FormatUint(offset, 10) != q.Get("offset") {
		jsonError(w, 400, fmt.Errorf("enter a non-negative output-local sat offset"))
		return
	}
	authority := a.currentChainAuthority()
	out, cp, err := querySatPointContext077(r.Context(), a.dataDir, q.Get("output"), q.Get("block_hash"), offset)
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	if cp != nil {
		if err = a.satCheckpointSelected077(r.Context(), cp, authority); err != nil {
			out.State, out.Number, out.Note = "reconciling", nil, err.Error()
		}
	}
	writeJSON(w, out)
}
