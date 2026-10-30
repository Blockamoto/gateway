package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// Ranges preserve FIFO sequence, including adjacent and numerically unordered
// intervals. Values are immutable across expression frames. The wire format
// uses decimal strings, matching the interpreter's other unsigned values.
type recipeOrderedRanges []satRange

func (r recipeOrderedRanges) MarshalJSON() ([]byte, error) {
	rows := make([]map[string]string, 0, len(r))
	for _, v := range r {
		rows = append(rows, map[string]string{"start": strconv.FormatUint(v.Start, 10), "end": strconv.FormatUint(v.End, 10)})
	}
	return json.Marshal(rows)
}

func (r *recipeOrderedRanges) UnmarshalJSON(raw []byte) error {
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil || rows == nil || len(rows) > recipeOrderedMaxWork {
		return fmt.Errorf("ordered ranges require an interval array")
	}
	values := make(recipeOrderedRanges, 0, len(rows))
	for _, row := range rows {
		m, err := recipeOrderedJSONObj(row, "start", "end")
		if err != nil || len(m) != 2 {
			return fmt.Errorf("invalid ordered interval")
		}
		var interval satRange
		for name, dest := range map[string]*uint64{"start": &interval.Start, "end": &interval.End} {
			var text string
			if json.Unmarshal(m[name], &text) != nil {
				return fmt.Errorf("ordered interval requires decimal strings")
			}
			n, err := strconv.ParseUint(text, 10, 64)
			if err != nil || strconv.FormatUint(n, 10) != text {
				return fmt.Errorf("invalid ordered interval number")
			}
			*dest = n
		}
		if interval.Start >= interval.End || interval.End > theoreticalSatSupply() {
			return fmt.Errorf("invalid ordered interval bounds")
		}
		values = append(values, interval)
	}
	*r = values
	return nil
}

func rovRanges(ranges []satRange) recipeOrderedValue {
	return recipeOrderedValue{Kind: "ranges", Ranges: append(make(recipeOrderedRanges, 0, len(ranges)), ranges...)}
}

func (m *recipeOrderedMachine) rangeCall(name string, args []recipeOrderedValue) (recipeOrderedValue, bool, error) {
	fail := func() (recipeOrderedValue, bool, error) {
		return recipeOrderedValue{}, true, fmt.Errorf("invalid arguments to ordered function %s", name)
	}
	// Charge all interval scans to the current event's structural-work budget.
	// The streaming host resets this per event, with cancellation throughout.
	charge := func(n int) error {
		n = n/1024 + 1
		if n > recipeOrderedMaxWork-m.work {
			return errRecipeStateBudget
		}
		m.work += n
		return m.ctx.Err()
	}
	switch name {
	case "range_empty":
		if len(args) != 0 {
			return fail()
		}
		return rovRanges(nil), true, nil
	case "range_subsidy":
		if len(args) != 1 || args[0].Kind != "uint" || args[0].Uint > uint64(^uint64(0)>>1) {
			return fail()
		}
		height := int64(args[0].Uint)
		ranges := []satRange{}
		if n := subsidyAtHeight(height); n > 0 {
			first := firstSatAtHeight(height)
			ranges = append(ranges, satRange{first, first + n})
		}
		return rovRanges(ranges), true, nil
	case "range_concat":
		if len(args) != 2 || args[0].Kind != "ranges" || args[1].Kind != "ranges" {
			return fail()
		}
		n := len(args[0].Ranges) + len(args[1].Ranges)
		if err := charge(n); err != nil {
			return recipeOrderedValue{}, true, err
		}
		ranges := make(recipeOrderedRanges, 0, n)
		ranges = append(ranges, args[0].Ranges...)
		ranges = append(ranges, args[1].Ranges...)
		return recipeOrderedValue{Kind: "ranges", Ranges: ranges}, true, nil
	case "range_split", "range_at", "range_value":
		want := 2
		if name == "range_value" {
			want = 1
		}
		if len(args) != want || args[0].Kind != "ranges" || want == 2 && args[1].Kind != "uint" {
			return fail()
		}
		if err := charge(len(args[0].Ranges)); err != nil {
			return recipeOrderedValue{}, true, err
		}
		if name == "range_value" {
			n, err := satRangesValueContext(m.ctx, args[0].Ranges)
			return rovUint(n), true, err
		}
		if name == "range_at" {
			offset := args[1].Uint
			for i, r := range args[0].Ranges {
				if i%1024 == 0 {
					if err := m.ctx.Err(); err != nil {
						return recipeOrderedValue{}, true, err
					}
				}
				if offset < r.End-r.Start {
					return rovUint(r.Start + offset), true, nil
				}
				offset -= r.End - r.Start
			}
			return recipeOrderedValue{Kind: "missing"}, true, nil
		}
		taken, rest, err := recipeSplitImmutableRanges(m.ctx, args[0].Ranges, args[1].Uint)
		if err != nil {
			return recipeOrderedValue{}, true, err
		}
		return recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"taken": {Kind: "ranges", Ranges: taken}, "rest": {Kind: "ranges", Ranges: rest}}}, true, nil
	}
	return recipeOrderedValue{}, false, nil
}

// Share immutable whole-interval spans. Only a split inside one interval needs
// new boundary storage; exact boundaries never copy the unconsumed stream.
func recipeSplitImmutableRanges(ctx context.Context, ranges recipeOrderedRanges, count uint64) (recipeOrderedRanges, recipeOrderedRanges, error) {
	if count == 0 {
		return recipeOrderedRanges{}, ranges, nil
	}
	for i, r := range ranges {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
		}
		n := r.End - r.Start
		if count == n {
			return ranges[:i+1], ranges[i+1:], nil
		}
		if count < n {
			taken := append(make(recipeOrderedRanges, 0, i+1), ranges[:i]...)
			taken = append(taken, satRange{r.Start, r.Start + count})
			rest := make(recipeOrderedRanges, 1, len(ranges)-i)
			rest[0] = satRange{r.Start + count, r.End}
			rest = append(rest, ranges[i+1:]...)
			return taken, rest, nil
		}
		count -= n
	}
	return nil, nil, fmt.Errorf("transaction outputs exceed resolved input sats")
}
