package main

import (
	"encoding/json"
	"strings"
)

const bitmapOPIProfile = "opi-0a09b987c87692ec3cabd404c8bcc7367707ee9a-eligibility"

type bitmapCompatibilityResult struct {
	Profile   string `json:"profile"`
	State     string `json:"state"`
	Reason    string `json:"reason"`
	Contested bool   `json:"contested"`
	Scope     string `json:"scope"`
}

// This is a necessary-condition evaluator for the pinned OPI pipeline, not an
// OPI database reconstruction. Passing syntax/MIME cannot establish historical
// numbering, bound sat placement, first-claim coverage or an OPI winner.
func bitmapCompatibility(r bitmapRecord) bitmapCompatibilityResult {
	result := bitmapCompatibilityResult{Profile: bitmapOPIProfile, State: "historical_context_missing", Reason: "Requires pinned Ord boundness/numbering and inscription-number-ordered prior claims.", Scope: "candidate_eligibility_only; not a reconstructed compatibility winner"}
	if r.Lean {
		result.State = "not_evaluated"
		result.Reason = "Lean records retain no compatibility evidence."
		return result
	}
	if r.ContentType != "text/plain" && !strings.HasPrefix(r.ContentType, "text/plain;") {
		result.State = "excluded"
		result.Reason = "Pinned OPI ingestion/Bitmap reader requires text/plain or text/plain; parameters. Gateway base does not filter MIME."
		result.Contested = r.Accepted
	}
	// Base syntax/future rejection is already visible but does not assert the
	// complete comparator outcome or an alternative winner.
	return result
}

type bitmapQueryRecord struct {
	bitmapRecord
	CompatibilityResult bitmapCompatibilityResult `json:"compatibility_result"`
}

func (r bitmapQueryRecord) MarshalJSON() ([]byte, error) {
	if r.Lean {
		return json.Marshal(struct {
			Schema          string  `json:"schema"`
			District        *uint64 `json:"district"`
			Inscription     string  `json:"inscription"`
			RevealHeight    int64   `json:"reveal_height"`
			SatNumber       *uint64 `json:"sat_number,omitempty"`
			CanonicalNumber *int64  `json:"canonical_number,omitempty"`
		}{"bitmap-lean-v2", r.District, r.Inscription, r.Height, r.SatNumber, r.CanonicalNumber})
	}
	type full bitmapRecord
	return json.Marshal(struct {
		full
		CompatibilityResult bitmapCompatibilityResult `json:"compatibility_result"`
	}{full(r.bitmapRecord), r.CompatibilityResult})
}
