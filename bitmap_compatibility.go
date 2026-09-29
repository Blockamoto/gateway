package main

import "strings"

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
