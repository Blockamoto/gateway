package main

import "strings"

type anchorState string

const (
	anchorMatches     anchorState = "matches_selected_chain"
	anchorConflict    anchorState = "conflicts_with_selected_chain"
	anchorUnavailable anchorState = "not_currently_checkable"
)

type anchorCheck struct {
	Status     anchorState `json:"status"`
	Height     int64       `json:"height"`
	Expected   string      `json:"expected,omitempty"`
	Actual     string      `json:"actual,omitempty"`
	Reason     string      `json:"reason,omitempty"`
	ValidHops  int         `json:"valid_hops"`
	StartValid bool        `json:"start_valid"`
}

func checkChainAnchor(height int64, expected string, canonical func(int64) (string, error)) anchorCheck {
	out := anchorCheck{Status: anchorUnavailable, Height: height, Expected: expected}
	if height < 0 || !validHash(expected) {
		out.Reason = "The checkpoint has no usable block anchor."
		return out
	}
	actual, err := canonical(height)
	if err != nil {
		out.Reason = err.Error()
		return out
	}
	if !validHash(actual) {
		out.Reason = "The chain provider returned no usable block hash."
		return out
	}
	out.Actual = actual
	if strings.EqualFold(expected, actual) {
		out.Status = anchorMatches
	} else {
		out.Status = anchorConflict
	}
	return out
}
func checkSatlineRecord(rec satlineRecord, canonical func(int64) (string, error)) anchorCheck {
	r := rec.Result
	var start *satlinePoint
	if r.Mode == "sat" {
		start = r.BirthSatpoint
	} else {
		start = r.StartSatpoint
	}
	if r.State == "INVALID" {
		return anchorCheck{Status: anchorMatches, StartValid: true}
	}
	if r.State == "LOST_AT_BIRTH" {
		start = &satlinePoint{Height: r.LostAtHeight, BlockHash: r.LostAtBlockHash}
	}
	if start == nil {
		return anchorCheck{Status: anchorUnavailable, Reason: "The saved result has no block anchor yet."}
	}
	out := checkChainAnchor(start.Height, start.BlockHash, canonical)
	if out.Status != anchorMatches {
		return out
	}
	out.StartValid = true
	for i, h := range r.Hops {
		c := checkChainAnchor(h.BlockHeight, h.BlockHash, canonical)
		if c.Status != anchorMatches {
			c.ValidHops = i
			c.StartValid = true
			return c
		}
		out.ValidHops = i + 1
	}
	if r.LostAtBlockHash != "" {
		c := checkChainAnchor(r.LostAtHeight, r.LostAtBlockHash, canonical)
		if c.Status != anchorMatches {
			c.ValidHops = out.ValidHops
			return c
		}
	}
	return out
}
func pendingSatlineAnchor(r satlineResult, reason string) satlineResult {
	r = satlineClone(r)
	r.AnchorStatus = string(anchorUnavailable)
	r.OperationalReason = "WAITING_FOR_CHAIN_ANCHOR"
	r.Note = "Your completed hops are saved. Gateway is preparing the chain information needed to recheck and continue without Bitcoin Core. " + reason
	return r
}
