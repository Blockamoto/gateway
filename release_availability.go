package main

import (
	"fmt"
	"net/http"
)

// Release availability is deliberately independent of user settings and saved
// state. Upgrading a profile cannot opt a testing build into unreleased work.
// Experimental Sat state has an explicitly versioned profile for reviewed rebuild.
func releaseFeatureAvailable(id string) bool {
	return id == "headers" || id == "blocks" || id == "inscriptions" || id == "tx-locator" || id == "txo-spender" || id == "txio" || id == "utxo-state" || id == "address-state" || id == "address-history" || id == "sat-state" || bitmapBuiltinExecutionAvailable(id)
}

// Related inscription transaction locators ship with the transaction stage,
// including their optional scan and retroactive enrichment controls.
func releaseInscriptionLocatorsAvailable() bool {
	return releaseFeatureAvailable("tx-locator")
}

func releaseAvailability() map[string]bool {
	available := map[string]bool{"gateway-peerhood": false}
	for _, d := range indexDefinitions() {
		available[d.ID] = releaseFeatureAvailable(d.ID)
	}
	return available
}

func releaseLockReason(id string) string {
	if bitmapConsumerID(id) {
		if bitmapConsumerKind(id) == "chain" {
			return errBitmapChainDeferred.Error()
		}
		return errBitmapComparisonDeferred.Error()
	}
	if bitmapCustomConsumerID(id) {
		return errBitmapComparisonDeferred.Error()
	}
	if id == "satline" {
		return "Satline is deferred in v0.7.7; forward and reverse traces and resume are unavailable. Saved trace data is retained. Sats indexing and saved range/location lookups remain available."
	}
	return fmt.Sprintf("%s is locked in this testing build. Headers, Bitcoin Blocks, Inscriptions, TX ID Locators, IO Links, UTXO set, Addresses and Sats are available; additional features will be enabled after separate validation. Saved data is retained.", id)
}

func requireReleaseFeature(id string) error {
	if !releaseFeatureAvailable(id) {
		return fmt.Errorf("%s", releaseLockReason(id))
	}
	return nil
}

func requireReleaseIndexRequest(req indexBuildRequest) error {
	if req.Index == "inscriptions" && req.ConventionalIDs != nil && *req.ConventionalIDs && !releaseInscriptionLocatorsAvailable() {
		return fmt.Errorf("Related transaction locators are locked until the TX ID Locators stage. Positional inscription indexing remains available.")
	}
	if req.RetainSatHistory {
		if err := requireReleaseFeature("sat-state"); err != nil {
			return err
		}
		hasSats := req.Index == "sat-state"
		for _, id := range req.Outputs {
			hasSats = hasSats || id == "sat-state"
		}
		if !hasSats {
			return fmt.Errorf("historical Sat ranges belong to an explicit Sat index build")
		}
	}
	for _, id := range append([]string{req.Index}, req.Outputs...) {
		if err := requireReleaseFeature(id); err != nil {
			return err
		}
	}
	return nil
}

func releaseHTTPFeature(w http.ResponseWriter, id string) bool {
	if err := requireReleaseFeature(id); err != nil {
		jsonError(w, http.StatusForbidden, err)
		return false
	}
	return true
}
