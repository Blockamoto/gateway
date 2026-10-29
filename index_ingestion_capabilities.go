package main

func resolvedIngestionCapabilities(d indexRunDocument) []indexBrowsePolicy {
	rows := []indexBrowsePolicy{}
	add := func(id, mode, reason string, available bool) {
		owner := resolvedSelectionOwner(id)
		if id == "txio-links" {
			owner = "txo-spender"
		}
		if id == "txio-sat-ranges" {
			owner = "sat-state"
		}
		c, ok := runSelection(d.Prepared, owner)
		enabled := d.Configured && ok && resolvedSelectionEnabled(c, id)
		if id == "txio-sat-ranges" {
			enabled = enabled && c.RetainSatHistory
		}
		rows = append(rows, indexBrowsePolicy{Index: id, Enabled: enabled && available, Available: available, Capability: mode, Reason: reason})
	}
	add("headers", "chain_authority", "Selected validated header prefix is maintained by the header subsystem; bodies do not infer missing headers.", true)
	add("blocks", "sparse", "Retain this complete verified body only when Raw is armed. Other derivations work with Raw Off.", true)
	add("tx-locator", "sparse", "Complete ordered transaction positions for this source block; no intervening coverage.", true)
	add("txio-links", "sparse_positive", "Positive input/outpoint relationships; unavailable creation-position translations remain unresolved.", true)
	add("txio-values", "sparse", "Independently owned exact creation values; no TXIO links or UTXO membership required.", true)
	add("txio-scripts", "sparse", "Independently owned exact creation scripts; no TXIO links or UTXO membership required.", true)
	add("txio-sat-ranges", "exact_next_with_prior_fifo_state", "Optional historical range retention while armed Sats consumes its exact next block with authenticated FIFO inputs.", true)
	add("utxo-state", "exact_next_with_complete_state", "Genesis or the exact next block after the complete selected UTXO checkpoint; missing inputs never start a prefix scan.", true)
	add("address-state", "matching_utxo_projection", "Project the matching complete UTXO checkpoint only; no UTXO job starts automatically.", true)
	add("address-history", "sparse", "Authenticated output history for supported destinations, including an explicit empty scanned block.", true)
	add("inscriptions", "sparse", "Current Lean/Full and conventional-ID choices apply to this block's own envelope extraction.", true)
	add(bitmapStandardDistrictID, "exact_next", "Bitmap Districts Standard V1 from its authenticated origin; no skipped prefix or custom execution.", true)
	add(bitmapStandardParcelID, "disabled", errBitmapParcelsDeferred.Error(), false)
	add(bitmapReplayParcelID, "coordinated_run_only", "An explicit reviewed run is required; browsing does not initiate Parcel evidence work.", false)
	add("bitmap-district-chain-dev-v1", "disabled", "Chain execution is disabled while its draft codec is reviewed; saved state remains inspectable.", false)
	add("bitmap-parcel-chain-dev-v1", "disabled", "Chain execution is disabled while its draft codec is reviewed; saved state remains inspectable.", false)
	return rows
}
