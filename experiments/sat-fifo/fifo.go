// Package fifo is a fixture-only FIFO range operation experiment.
package fifo

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

type Program struct {
	Version int      `json:"version"`
	ID      string   `json:"id"`
	Scope   string   `json:"scope"`
	Ops     []string `json:"ops"`
}

func ReadProgram(r io.Reader) (Program, error) {
	var p Program
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return p, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return p, errors.New("trailing program JSON")
	}
	return p, p.Validate()
}
func (p Program) Validate() error {
	if p.Version != 1 || strings.TrimSpace(p.ID) == "" || p.Scope != "ordinary-transaction" {
		return errors.New("invalid program identity or scope")
	}
	if len(p.Ops) != 3 || p.Ops[0] != "concatenate-input-ranges" || p.Ops[1] != "allocate-outputs-fifo" || p.Ops[2] != "retain-fee-remainder" {
		return errors.New("unsupported operation sequence")
	}
	return nil
}

// Ranges are half-open ordinal intervals in stream order, not sorted order.
type Range struct{ Start, End uint64 }
type Outpoint struct {
	Transaction string
	Output      uint32
}

// Evidence is a caller-supplied fixture assertion. Anchor is required, but is
// not authenticated by this experiment.
type Evidence struct {
	Anchor string
	Value  uint64
	Ranges []Range
}
type Input struct {
	Outpoint Outpoint
	Evidence *Evidence
}
type Transaction struct {
	Inputs       []Input
	OutputValues []uint64
}
type Result struct {
	Outputs    [][]Range
	Fee        []Range
	InputValue uint64
	FeeValue   uint64
}

// Execute returns no partial result if required evidence is absent or
// inconsistent. It never mutates caller-owned ranges. It assumes an ordinary
// transaction and performs no Bitcoin or selected-chain authentication.
func Execute(p Program, tx Transaction) (Result, error) {
	if err := p.Validate(); err != nil {
		return Result{}, err
	}
	if len(tx.Inputs) == 0 {
		return Result{}, errors.New("ordinary transaction requires inputs")
	}
	var stream []Range
	var result Result
	for _, op := range p.Ops {
		switch op {
		case "concatenate-input-ranges":
			var err error
			stream, result.InputValue, err = concatenate(tx.Inputs)
			if err != nil {
				return Result{}, err
			}
		case "allocate-outputs-fifo":
			result.Outputs = make([][]Range, 0, len(tx.OutputValues))
			for _, n := range tx.OutputValues {
				out, rest, err := take(stream, n)
				if err != nil {
					return Result{}, err
				}
				result.Outputs = append(result.Outputs, out)
				stream = rest
			}
		case "retain-fee-remainder":
			var err error
			result.FeeValue, err = rangeValue(stream)
			if err != nil {
				return Result{}, err
			}
			result.Fee = stream
		}
	}
	return result, nil
}

func concatenate(inputs []Input) ([]Range, uint64, error) {
	var stream []Range
	var total uint64
	seen := map[Outpoint]bool{}
	for _, in := range inputs {
		if in.Outpoint.Transaction == "" {
			return nil, 0, errors.New("missing outpoint")
		}
		if seen[in.Outpoint] {
			return nil, 0, errors.New("duplicate input")
		}
		seen[in.Outpoint] = true
		if in.Evidence == nil || in.Evidence.Anchor == "" {
			return nil, 0, errors.New("unresolved input range evidence")
		}
		value, err := rangeValue(in.Evidence.Ranges)
		if err != nil {
			return nil, 0, err
		}
		if value != in.Evidence.Value {
			return nil, 0, errors.New("input range/value mismatch")
		}
		if total > math.MaxUint64-value {
			return nil, 0, errors.New("input value overflow")
		}
		total += value
		stream = append(stream, in.Evidence.Ranges...)
	}
	// Double ownership of one ordinal makes identity conservation invalid even
	// if the aggregate values match. Validate on a sorted copy, not stream order.
	sorted := append([]Range(nil), stream...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Start < sorted[i-1].End {
			return nil, 0, errors.New("overlapping input ordinal ranges")
		}
	}
	return stream, total, nil
}

func rangeValue(ranges []Range) (uint64, error) {
	var total uint64
	for _, r := range ranges {
		if r.End <= r.Start {
			return 0, errors.New("invalid half-open range")
		}
		n := r.End - r.Start
		if total > math.MaxUint64-n {
			return 0, errors.New("range value overflow")
		}
		total += n
	}
	return total, nil
}
func take(stream []Range, n uint64) ([]Range, []Range, error) {
	out := []Range{}
	for n > 0 {
		if len(stream) == 0 {
			return nil, nil, fmt.Errorf("outputs exceed input sats")
		}
		r := stream[0]
		size := r.End - r.Start
		if size <= n {
			out = append(out, r)
			stream = stream[1:]
			n -= size
		} else {
			out = append(out, Range{Start: r.Start, End: r.Start + n})
			stream[0].Start += n
			n = 0
		}
	}
	return out, stream, nil
}
