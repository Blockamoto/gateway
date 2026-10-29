package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
)

func (a *app) sparseTXIORows(ctx context.Context, direction, position, txid string, vout uint32) ([]txioMatch, error) {
	s, head, err := a.browseStore("txo-spender")
	if err != nil {
		return nil, err
	}
	heights := []int64{}
	if direction == "input" {
		c, err := parseTXIOPosition(direction, position)
		if err != nil {
			return nil, err
		}
		heights = append(heights, c.Height)
	} else {
		for key := range head.Blocks {
			h, _ := strconv.ParseInt(key, 10, 64)
			heights = append(heights, h)
		}
		sort.Slice(heights, func(i, j int) bool { return heights[i] > heights[j] })
	}
	rows := []txioMatch{}
	for n, h := range heights {
		if n >= indexExplorerMaxBatches {
			break
		}
		b, have, err := a.browsedBatchContext(ctx, "txo-spender", h, "")
		if err != nil {
			return nil, err
		}
		if !have || b.Spenders == nil {
			continue
		}
		s.checkpoint = &b.Checkpoint
		for i := range b.Spenders.Rows {
			r, err := s.expandedTXIORow(ctx, b, i)
			if err != nil {
				return nil, err
			}
			input := inputCoordinate(r.Vin, r.TxIndex, h)
			if txid != "" {
				if r.PrevTxID != txid || r.Vout != vout {
					continue
				}
			} else if direction == "input" && input != position || direction == "output" && r.Output != position {
				continue
			}
			creationHash := ""
			if r.Output != "" {
				c, _ := parseBODCoordinate(r.Output)
				creationHash = b.Spenders.CreationAnchors[strconv.FormatInt(c.Height, 10)]
				selected, err := a.canonicalHashAtHeight(c.Height)
				if err != nil {
					return nil, err
				}
				if selected != creationHash {
					continue
				}
			}
			rows = append(rows, txioMatch{indexSpendMatch{r, h, b.Checkpoint.BlockHash}, input, creationHash})
		}
	}
	return rows, nil
}
func mergeTXIORows(a, b []txioMatch) []txioMatch {
	seen := map[string]bool{}
	out := []txioMatch{}
	for _, group := range [][]txioMatch{a, b} {
		for _, r := range group {
			key := fmt.Sprintf("%s:%s:%s", r.BlockHash, r.Input, r.Output)
			if !seen[key] {
				seen[key] = true
				out = append(out, r)
			}
		}
	}
	return out
}
func completeTXIOResult(out txioResult) txioResult {
	if len(out.Rows) > 0 {
		out.State = "known"
		out.Absence = ""
		for _, r := range out.Rows {
			if r.Output == "" {
				out.State = "unresolved"
				out.Note = "The spending input and original outpoint are retained; its creation position is not known."
			}
		}
	}
	return out
}
