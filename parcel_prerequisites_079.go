package main

// Parcel prerequisite observations are advisory. A contiguous timeline range
// is not proof of spend resolution, byte availability, values, or parenthood.
// Exact candidate-specific work is only known after the declarations are read.
type parcelPrerequisiteView079 struct {
	State        string                       `json:"state"`
	TargetHeight int64                        `json:"target_height"`
	TargetHash   string                       `json:"target_hash,omitempty"`
	Scope        string                       `json:"scope"`
	District     parcelPrerequisiteRoute079   `json:"district"`
	Routes       []parcelPrerequisiteRoute079 `json:"routes"`
	Reason       string                       `json:"reason"`
}
type parcelPrerequisiteRoute079 struct {
	ExecutionAvailable bool             `json:"execution_available"`
	ID                 string           `json:"id"`
	State              string           `json:"state"`
	From               int64            `json:"from"`
	To                 int64            `json:"to"`
	Coverage           []heightInterval `json:"coverage"`
	Gaps               []heightInterval `json:"gaps,omitempty"`
	Note               string           `json:"note"`
}

func parcelPrerequisites079(t indexTimelineView) parcelPrerequisiteView079 {
	out := parcelPrerequisiteView079{State: "unavailable", TargetHeight: t.TipHeight, TargetHash: t.TipHash,
		Scope:  "Conservative selected-chain inventory through the observed tip; narrower candidate-specific intervals depend on actual parent reveals and spends.",
		Reason: "Parcel execution is disabled: a complete replayable parent-evidence route is not implemented. Coverage alone cannot establish readiness.", Routes: []parcelPrerequisiteRoute079{}}
	if bitmapBuiltinExecutionAvailable(bitmapReplayParcelID) {
		out.State = "requires_review"
		out.Reason = "Bitmap Parcels requires an explicitly selected evidence route and indexing scope. Live following also requires Live Districts and the selected proof producer. Retained Raw requires a complete local inventory; coverage alone does not prove individual relationships."
	}
	out.District = parcelPrerequisiteRoute079{ID: "district", State: "unknown", From: bitmapDevelopmentStart, To: t.TipHeight, Coverage: []heightInterval{}, Note: "The selected District interpretation must have committed selected-chain evaluations through each Parcel reveal. A District winner and target-block transaction count are checked for each candidate."}
	if t.TipKnown && t.TipHeight >= bitmapDevelopmentStart {
		for _, track := range t.Tracks {
			if track.ID != bitmapStandardDistrictID {
				continue
			}
			out.District.Coverage = mergeIntervals(append([]heightInterval{}, track.RecordedCoverage...))
			if track.CoverageComplete {
				out.District.Gaps = indexCoverageGaps(out.District.Coverage, bitmapDevelopmentStart, t.TipHeight)
				out.District.State = "coverage_observed"
				if len(out.District.Gaps) > 0 {
					out.District.State = "coverage_gaps"
				}
			}
			break
		}
	}
	specs := []struct {
		id, track string
		from      int64
		note      string
	}{
		{"raw_blocks", "blocks", 0, "Durable Raw body inventory, not metadata or an external provider. Each body still requires integrity validation; finding predecessors can require scanning within the admitted scope. Input values and parent satpoint must be established."},
		{"tx_locators", "tx-locator", 0, "Transaction positions alone do not provide bytes, values, spend links, or parent proof. Required verified transaction bodies must be available or explicitly fetched."},
		{"txio_links", "txo-spender", 0, "Committed block coverage does not prove all input links are resolved. Creation and spending endpoints, verified block data and input values are still required; Values history is optional if verified bytes supply values."},
	}
	for _, s := range specs {
		route := parcelPrerequisiteRoute079{ID: s.id, ExecutionAvailable: s.id == "raw_blocks" || s.id == "tx_locators" && parcelLocatorExecutionAvailable079 || s.id == "txio_links" && parcelTXIOExecutionAvailable079, State: "unknown", From: s.from, To: t.TipHeight, Coverage: []heightInterval{}, Note: s.note}
		if t.TipKnown && t.TipHeight >= s.from {
			for _, track := range t.Tracks {
				if track.ID != s.track {
					continue
				}
				var complete bool
				if s.id == "raw_blocks" {
					if track.RetainedCoverage != nil {
						route.Coverage = append(route.Coverage, track.RetainedCoverage.Ranges...)
						complete = track.RetainedCoverage.Complete
					}
				} else {
					// Sparse browsed records are not a complete prerequisite. Only
					// selected-chain committed index instances count here.
					for _, source := range track.Sources {
						if source.Kind == "committed_records" && source.ID == "gateway_committed_index" && source.Complete {
							route.Coverage = append(route.Coverage, source.Ranges...)
							complete = true
						}
					}
				}
				route.Coverage = mergeIntervals(route.Coverage)
				if complete {
					route.Gaps = indexCoverageGaps(route.Coverage, s.from, t.TipHeight)
					if len(route.Gaps) > 0 {
						route.State = "coverage_gaps"
					} else {
						route.State = "coverage_observed"
					}
				}
				break
			}
		}
		out.Routes = append(out.Routes, route)
	}
	return out
}
