package main

import (
	"fmt"
	"net/http"
)

// Release availability is deliberately independent of user settings and saved
// state. Upgrading a profile cannot opt a testing build into unreleased work.
// Keep the derivation recipes and data formats unchanged for future promotion.
func releaseFeatureAvailable(id string) bool {
	return id == "headers" || id == "blocks" || id == "inscriptions"
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
	return fmt.Sprintf("%s is locked in this testing build. Headers, Bitcoin Blocks and Inscriptions are available; additional features will be enabled after separate validation. Saved data is retained.", id)
}

func requireReleaseFeature(id string) error {
	if !releaseFeatureAvailable(id) {
		return fmt.Errorf("%s", releaseLockReason(id))
	}
	return nil
}

func requireReleaseIndexRequest(req indexBuildRequest) error {
	if req.Index == "inscriptions" && req.ConventionalIDs != nil && *req.ConventionalIDs && !releaseInscriptionLocatorsAvailable() {
		return fmt.Errorf("Related transaction locators are locked until the Transaction Index stage. Positional inscription indexing remains available.")
	}
	if req.RetainSatHistory {
		if err := requireReleaseFeature("sat-state"); err != nil {
			return err
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
